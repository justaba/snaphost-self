package config

import (
	"os"
	"strconv"
)

// Config holds the HTTP edge's configuration.
//
// It shrank when Supabase went. SUPABASE_URL and SUPABASE_WEBHOOK_SECRET
// existed for an identity provider on the internet; WEBHOOK_SECRET, REDIS_URL
// and the downstream service URLs were left without a reader when the gateway
// stopped proxying and the signup webhook was deleted. WEBHOOK_SECRET is still
// required — by the control plane's own config, which is what actually uses it.
type Config struct {
	Port            string
	RateLimitIP     int
	RateLimitUser   int
	RateLimitDeploy int
	// MaxUploadSizeMB bounds POST /api/v1/deploys/upload bodies at the edge;
	// keep in sync with the control plane's MAX_UPLOAD_SIZE_MB.
	MaxUploadSizeMB int
	// RBACModelPath and RBACPolicyPath locate the Casbin files. They used to be
	// bare relative names resolved against the working directory, which worked
	// only because the image copies them next to the binary. Configuration
	// instead, so running the binary from anywhere behaves the same.
	RBACModelPath  string
	RBACPolicyPath string
}

// Load reads configuration from environment variables and applies defaults.
func Load() (*Config, error) {
	return &Config{
		Port:            getEnv("PORT", "8080"),
		RateLimitIP:     getEnvAsInt("RATE_LIMIT_IP", 30),
		RateLimitUser:   getEnvAsInt("RATE_LIMIT_USER", 120),
		RateLimitDeploy: getEnvAsInt("RATE_LIMIT_DEPLOY", 5),
		MaxUploadSizeMB: getEnvAsInt("MAX_UPLOAD_SIZE_MB", 50),
		RBACModelPath:   getEnv("RBAC_MODEL_PATH", "rbac_model.conf"),
		RBACPolicyPath:  getEnv("RBAC_POLICY_PATH", "rbac_policy.csv"),
	}, nil
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
