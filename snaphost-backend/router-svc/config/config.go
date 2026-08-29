package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Port              string
	DomainSuffix      string
	UserBillingURL    string
	WebhookSecret     string
	YandexAuthMode    string
	YandexSAKeyPath   string
	ProxyTimeout      time.Duration
	TokenCacheTTL     time.Duration
	RouteCacheSeconds int
	// TLSAskEnabled registers the on-demand TLS `ask` endpoint. Only the
	// loopback-published VDS instance may serve it; the Yandex instance of the
	// same image sits behind a public gateway, so this defaults to off.
	TLSAskEnabled bool
}

func Load() (*Config, error) {
	cfg := &Config{
		Port:              envOrDefault("PORT", "8085"),
		DomainSuffix:      os.Getenv("DOMAIN_SUFFIX"),
		UserBillingURL:    envOrDefault("USER_BILLING_URL", "http://user-billing:8081"),
		WebhookSecret:     os.Getenv("WEBHOOK_SECRET"),
		YandexAuthMode:    envOrDefault("YANDEX_AUTH_MODE", "key_file"),
		YandexSAKeyPath:   os.Getenv("YANDEX_SA_KEY_PATH"),
		ProxyTimeout:      60 * time.Second,
		TokenCacheTTL:     10 * time.Minute,
		RouteCacheSeconds: 0,
		TLSAskEnabled:     os.Getenv("TLS_ASK_ENABLED") == "true",
	}
	if cfg.WebhookSecret == "" {
		return nil, fmt.Errorf("config: WEBHOOK_SECRET is required")
	}
	if cfg.DomainSuffix == "" {
		return nil, fmt.Errorf("config: DOMAIN_SUFFIX is required")
	}
	switch cfg.YandexAuthMode {
	case "key_file":
		if cfg.YandexSAKeyPath == "" {
			return nil, fmt.Errorf("config: YANDEX_SA_KEY_PATH is required when YANDEX_AUTH_MODE=key_file")
		}
	case "metadata":
		// Uses the service account attached to the Yandex runtime.
	default:
		return nil, fmt.Errorf("config: YANDEX_AUTH_MODE must be 'key_file' or 'metadata', got %q", cfg.YandexAuthMode)
	}
	if raw := os.Getenv("PROXY_TIMEOUT_SEC"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 {
			return nil, fmt.Errorf("config: PROXY_TIMEOUT_SEC must be a positive integer")
		}
		cfg.ProxyTimeout = time.Duration(v) * time.Second
	}
	if raw := os.Getenv("TOKEN_CACHE_TTL_SEC"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 {
			return nil, fmt.Errorf("config: TOKEN_CACHE_TTL_SEC must be a positive integer")
		}
		cfg.TokenCacheTTL = time.Duration(v) * time.Second
	}
	return cfg, nil
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
