package config

import (
	"os"
)

// Config holds the public HTTP server configuration.
type Config struct {
	Port string
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
		Port:           getEnv("PORT", "8080"),
		RBACModelPath:  getEnv("RBAC_MODEL_PATH", "rbac_model.conf"),
		RBACPolicyPath: getEnv("RBAC_POLICY_PATH", "rbac_policy.csv"),
	}, nil
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}
