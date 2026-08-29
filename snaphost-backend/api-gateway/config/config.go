package config

import (
	"errors"
	"os"
	"strconv"
)

// Services holds URLs for the backend microservices
type Services struct {
	UserBilling    string
	AIOrchestrator string
}

// Config holds all configuration for the API Gateway
type Config struct {
	Port                   string
	RedisURL               string
	SupabaseURL            string
	SupabaseWebhookSecret  string
	WebhookSecret          string
	InitialVibecoinBalance int64
	Services               Services
	RateLimitIP            int
	RateLimitUser          int
	RateLimitDeploy        int
	// MaxUploadSizeMB bounds POST /api/v1/deploys/upload bodies at the
	// gateway; keep in sync with user-billing's MAX_UPLOAD_SIZE_MB.
	MaxUploadSizeMB int
}

// Load reads configuration from environment variables and applies defaults
func Load() (*Config, error) {
	cfg := &Config{
		Port:                   getEnv("PORT", "8080"),
		RedisURL:               getEnv("REDIS_URL", "redis://redis:6379"),
		SupabaseURL:            getEnv("SUPABASE_URL", ""),
		SupabaseWebhookSecret:  getEnv("SUPABASE_WEBHOOK_SECRET", ""),
		WebhookSecret:          getEnv("WEBHOOK_SECRET", ""),
		InitialVibecoinBalance: int64(getEnvAsInt("INITIAL_VIBECOIN_BALANCE", 100)),
		Services: Services{
			UserBilling:    getEnv("USER_BILLING_URL", "http://user-billing:8081"),
			AIOrchestrator: getEnv("AI_ORCHESTRATOR_URL", "http://ai-orchestrator:8087"),
		},
		RateLimitIP:     getEnvAsInt("RATE_LIMIT_IP", 30),
		RateLimitUser:   getEnvAsInt("RATE_LIMIT_USER", 120),
		RateLimitDeploy: getEnvAsInt("RATE_LIMIT_DEPLOY", 5),
		MaxUploadSizeMB: getEnvAsInt("MAX_UPLOAD_SIZE_MB", 50),
	}

	if cfg.SupabaseURL == "" {
		return nil, errors.New("SUPABASE_URL environment variable is required")
	}
	if cfg.SupabaseWebhookSecret == "" {
		return nil, errors.New("SUPABASE_WEBHOOK_SECRET environment variable is required")
	}
	if cfg.WebhookSecret == "" {
		return nil, errors.New("WEBHOOK_SECRET environment variable is required")
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

func getEnvAsInt(key string, fallback int) int {
	strValue := getEnv(key, "")
	if strValue == "" {
		return fallback
	}
	value, err := strconv.Atoi(strValue)
	if err != nil {
		return fallback
	}
	return value
}
