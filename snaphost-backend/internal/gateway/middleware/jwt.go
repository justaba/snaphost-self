package middleware

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"snaphost/internal/gateway/config"
)

type contextKey string

const (
	CtxKeyUserID    = contextKey("userID")
	CtxKeyUserEmail = contextKey("userEmail")
	CtxKeyUserRole  = contextKey("userRole")
)

// For backwards compatibility with ratelimit.go
const ContextKeyUserID = string(CtxKeyUserID)

// JWKSCache caches public keys from Supabase
type JWKSCache struct {
	mu         sync.RWMutex
	keys       map[string]crypto.PublicKey
	fetchedAt  time.Time
	ttl        time.Duration
	jwksURL    string
	httpClient *http.Client
}

// NewJWKSCache creates a new JWKSCache instance
func NewJWKSCache(supabaseURL string) *JWKSCache {
	return &JWKSCache{
		keys:       make(map[string]crypto.PublicKey),
		ttl:        1 * time.Hour,
		jwksURL:    supabaseURL + "/auth/v1/.well-known/jwks.json",
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// GetKey retrieves a public key by kid
func (c *JWKSCache) GetKey(ctx context.Context, kid string) (crypto.PublicKey, error) {
	c.mu.RLock()
	key, exists := c.keys[kid]
	expired := time.Since(c.fetchedAt) > c.ttl
	c.mu.RUnlock()

	if exists && !expired {
		return key, nil
	}

	// A refresh failure is not fatal here: the cached key may still be usable,
	// and the miss below is what turns a genuinely unknown kid into an error.
	_ = c.refresh(ctx)

	c.mu.RLock()
	key, exists = c.keys[kid]
	c.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("unknown key id: %s", kid)
	}

	return key, nil
}

// Prefetch forcefully refreshes the JWKS cache
func (c *JWKSCache) Prefetch(ctx context.Context) error {
	return c.refresh(ctx)
}

// refresh fetches and parses the JWKS
func (c *JWKSCache) refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.jwksURL, nil)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code fetching JWKS: %d", resp.StatusCode)
	}

	var jwks struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			// RSA fields
			N string `json:"n"`
			E string `json:"e"`
			// EC fields (Supabase 2.x defaults to ES256 / P-256)
			Crv string `json:"crv"`
			X   string `json:"x"`
			Y   string `json:"y"`
		} `json:"keys"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return err
	}

	newKeys := make(map[string]crypto.PublicKey)
	for _, key := range jwks.Keys {
		var (
			pub crypto.PublicKey
			err error
		)
		switch key.Kty {
		case "RSA":
			pub, err = parseRSAPublicKey(key.N, key.E)
		case "EC":
			pub, err = parseECDSAPublicKey(key.Crv, key.X, key.Y)
		default:
			continue
		}
		if err != nil {
			continue
		}
		newKeys[key.Kid] = pub
	}

	c.mu.Lock()
	c.keys = newKeys
	c.fetchedAt = time.Now()
	c.mu.Unlock()

	return nil
}

// parseECDSAPublicKey parses an ECDSA public key from JWK x/y coordinates.
// Supports the curves Supabase actually issues today (P-256 / ES256), plus
// P-384 and P-521 for forward compatibility.
func parseECDSAPublicKey(crv, x, y string) (*ecdsa.PublicKey, error) {
	var curve elliptic.Curve
	switch crv {
	case "P-256":
		curve = elliptic.P256()
	case "P-384":
		curve = elliptic.P384()
	case "P-521":
		curve = elliptic.P521()
	default:
		return nil, fmt.Errorf("unsupported EC curve: %s", crv)
	}

	decX, err := base64.RawURLEncoding.DecodeString(x)
	if err != nil {
		return nil, fmt.Errorf("decode EC x: %w", err)
	}
	decY, err := base64.RawURLEncoding.DecodeString(y)
	if err != nil {
		return nil, fmt.Errorf("decode EC y: %w", err)
	}

	return &ecdsa.PublicKey{
		Curve: curve,
		X:     new(big.Int).SetBytes(decX),
		Y:     new(big.Int).SetBytes(decY),
	}, nil
}

// parseRSAPublicKey parses an RSA public key from its components
func parseRSAPublicKey(n, e string) (*rsa.PublicKey, error) {
	decodedN, err := base64.RawURLEncoding.DecodeString(n)
	if err != nil {
		return nil, err
	}

	decodedE, err := base64.RawURLEncoding.DecodeString(e)
	if err != nil {
		return nil, err
	}

	var eInt int
	for _, b := range decodedE {
		eInt = (eInt << 8) | int(b)
	}

	if eInt <= 0 {
		return nil, errors.New("invalid E value")
	}

	pubKey := &rsa.PublicKey{
		N: new(big.Int).SetBytes(decodedN),
		E: eInt,
	}

	return pubKey, nil
}

// SupabaseClaims represents the JWT claims from Supabase
type SupabaseClaims struct {
	jwt.RegisteredClaims
	Email        string                 `json:"email"`
	Role         string                 `json:"role"`
	AppMetadata  map[string]interface{} `json:"app_metadata"`
	UserMetadata map[string]interface{} `json:"user_metadata"`
	SnapHostRole string                 `json:"snaphost_role"`
}

// PublicRoutes defines routes that skip JWT validation
var PublicRoutes = map[string]bool{
	"POST:/api/v1/auth/register":       true,
	"POST:/api/v1/auth/login":          true,
	"GET:/health":                      true,
	"GET:/metrics":                     true,
	"POST:/internal/webhooks/supabase": true,
}

// VerifyToken validates a Supabase JWT against the JWKS cache and returns
// its claims. The reason string is one of: invalid_token, token_expired,
// invalid_algorithm — suitable for surfacing to the client.
func VerifyToken(ctx context.Context, jwks *JWKSCache, tokenString string) (*SupabaseClaims, string, error) {
	parser := jwt.NewParser()
	token, _, err := parser.ParseUnverified(tokenString, &SupabaseClaims{})
	if err != nil {
		return nil, "invalid_token", err
	}

	kidVal, ok := token.Header["kid"]
	if !ok {
		return nil, "invalid_token", errors.New("missing kid header")
	}
	kid, ok := kidVal.(string)
	if !ok || kid == "" {
		return nil, "invalid_token", errors.New("invalid kid header")
	}

	pubKey, err := jwks.GetKey(ctx, kid)
	if err != nil {
		return nil, "invalid_token", err
	}

	claims := &SupabaseClaims{}
	parsedToken, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (interface{}, error) {
		switch t.Method.(type) {
		case *jwt.SigningMethodRSA, *jwt.SigningMethodECDSA:
			return pubKey, nil
		default:
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
	})
	if err != nil {
		reason := "invalid_token"
		if errors.Is(err, jwt.ErrTokenExpired) {
			reason = "token_expired"
		} else if strings.Contains(err.Error(), "unexpected signing method") {
			reason = "invalid_algorithm"
		}
		return nil, reason, err
	}
	if !parsedToken.Valid {
		return nil, "invalid_token", errors.New("token reported as invalid")
	}
	return claims, "", nil
}

// JWT middleware for validating authentication tokens.
func JWT(cfg *config.Config, jwks *JWKSCache, apiKeys KeyVerifier) gin.HandlerFunc {
	return func(c *gin.Context) {
		lookupKey := c.Request.Method + ":" + c.FullPath()
		if PublicRoutes[lookupKey] {
			c.Next()
			return
		}

		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized", "reason": "missing_token"})
			return
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized", "reason": "invalid_token"})
			return
		}

		// API-key path: non-browser clients (MCP, extension, CLI) present a
		// "sk_" bearer instead of a Supabase JWT. Resolve it to a user via
		// user-billing and set the same context the JWT path would.
		if IsAPIKey(parts[1]) {
			if apiKeys == nil {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized", "reason": "api_key_unsupported"})
				return
			}
			userID, verr := apiKeys.Verify(c.Request.Context(), parts[1])
			if verr != nil {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized", "reason": "invalid_api_key"})
				return
			}
			c.Set(string(CtxKeyUserID), userID)
			c.Set(string(CtxKeyUserEmail), "")
			c.Set(string(CtxKeyUserRole), "user")
			c.Next()
			return
		}

		claims, reason, err := VerifyToken(c.Request.Context(), jwks, parts[1])
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized", "reason": reason})
			return
		}

		role := claims.SnapHostRole
		if role == "" {
			role = "user"
		}

		c.Set(string(CtxKeyUserID), claims.Subject)
		c.Set(string(CtxKeyUserEmail), claims.Email)
		c.Set(string(CtxKeyUserRole), role)

		c.Next()
	}
}
