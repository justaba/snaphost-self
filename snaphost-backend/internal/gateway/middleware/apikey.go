package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// apiKeyPrefix marks a bearer credential as a SnapHost API key rather than a
// Supabase JWT. Must match the prefix minted by user-billing's apikey package.
const apiKeyPrefix = "sk_"

// IsAPIKey reports whether a bearer credential is a SnapHost API key.
func IsAPIKey(cred string) bool {
	return strings.HasPrefix(cred, apiKeyPrefix)
}

// KeyVerifier resolves an API key to its owning user id.
//
// An interface because there are two implementations and the difference is
// only how far the lookup travels: over HTTP when the control plane was a
// separate process, and straight to the repository now that it is not.
type KeyVerifier interface {
	Verify(ctx context.Context, key string) (string, error)
}

// APIKeyVerifier resolves an API key to its owning user by calling
// user-billing's internal verify endpoint with the shared webhook secret.
type APIKeyVerifier struct {
	client        *http.Client
	billingURL    string
	webhookSecret string
}

// NewAPIKeyVerifier builds a verifier targeting the user-billing base URL.
func NewAPIKeyVerifier(billingURL, webhookSecret string) *APIKeyVerifier {
	return &APIKeyVerifier{
		client:        &http.Client{Timeout: 5 * time.Second},
		billingURL:    billingURL,
		webhookSecret: webhookSecret,
	}
}

type verifyResponse struct {
	UserID string `json:"user_id"`
	KeyID  string `json:"key_id"`
}

// Verify returns the owning user ID for a valid, active API key. A non-nil error
// means the key is unknown, revoked, or the billing service is unreachable.
func (v *APIKeyVerifier) Verify(ctx context.Context, key string) (string, error) {
	payload, err := json.Marshal(map[string]string{"key": key})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.billingURL+"/internal/keys/verify", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Secret", v.webhookSecret)

	resp, err := v.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return "", fmt.Errorf("invalid api key")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("key verification failed: status %d", resp.StatusCode)
	}
	var out verifyResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.UserID == "" {
		return "", fmt.Errorf("key verification returned no user")
	}
	return out.UserID, nil
}
