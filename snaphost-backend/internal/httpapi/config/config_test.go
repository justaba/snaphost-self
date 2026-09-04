package config

// Tests for the public HTTP server configuration.

import "testing"

func TestLoadNeedsNoEnvironment(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Port != "8080" {
		t.Errorf("Port = %q, want the default 8080", cfg.Port)
	}
	if cfg.EdgeProxyPort != "8081" || cfg.EdgeAskPort != "8082" {
		t.Errorf("edge ports = %q/%q, want 8081/8082", cfg.EdgeProxyPort, cfg.EdgeAskPort)
	}
}

func TestLoadRejectsInvalidOrOverlappingPorts(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{key: "PORT", value: "not-a-port"},
		{key: "EDGE_PROXY_PORT", value: "0"},
		{key: "EDGE_ASK_PORT", value: "65536"},
		{key: "EDGE_ASK_PORT", value: "8080"},
	} {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			if _, err := Load(); err == nil {
				t.Fatal("Load() succeeded, want an error")
			}
		})
	}
}
