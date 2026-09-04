package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds the public HTTP server configuration.
type Config struct {
	Port          string
	EdgeProxyPort string
	EdgeAskPort   string
	// RBACModelPath and RBACPolicyPath locate the Casbin files. They used to be
	// bare relative names resolved against the working directory, which worked
	// only because the image copies them next to the binary. Configuration
	// instead, so running the binary from anywhere behaves the same.
	RBACModelPath  string
	RBACPolicyPath string
}

// Load reads configuration from environment variables and applies defaults.
func Load() (*Config, error) {
	port, err := getPort("PORT", "8080")
	if err != nil {
		return nil, err
	}
	proxyPort, err := getPort("EDGE_PROXY_PORT", "8081")
	if err != nil {
		return nil, err
	}
	askPort, err := getPort("EDGE_ASK_PORT", "8082")
	if err != nil {
		return nil, err
	}
	if port == proxyPort || port == askPort || proxyPort == askPort {
		return nil, fmt.Errorf("HTTP, edge proxy and edge ask ports must be different")
	}

	return &Config{
		Port:           port,
		EdgeProxyPort:  proxyPort,
		EdgeAskPort:    askPort,
		RBACModelPath:  getEnv("RBAC_MODEL_PATH", "rbac_model.conf"),
		RBACPolicyPath: getEnv("RBAC_POLICY_PATH", "rbac_policy.csv"),
	}, nil
}

func getPort(key, fallback string) (string, error) {
	value := getEnv(key, fallback)
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return "", fmt.Errorf("config: %s must be a port between 1 and 65535", key)
	}
	return value, nil
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}
