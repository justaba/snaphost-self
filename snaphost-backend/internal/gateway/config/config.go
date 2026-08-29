package config

import (
	"os"
	"strconv"
)

// Config holds the HTTP edge's configuration.
//
// It keeps shrinking. SUPABASE_URL and SUPABASE_WEBHOOK_SECRET went with the
// identity provider; WEBHOOK_SECRET, REDIS_URL and the downstream service URLs
// lost their last reader when the gateway stopped proxying and the signup
// webhook was deleted; RATE_LIMIT_IP, RATE_LIMIT_USER and RATE_LIMIT_DEPLOY
// went with Redis, along with the sliding window that shaped traffic for a
// multi-tenant API this is not. What is left is a port, an upload ceiling and
// where the Casbin files are.
type Config struct {
	Port string
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
