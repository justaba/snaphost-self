package config

import (
	"os"
	"testing"
)

func TestRegistryInsecureAutoDetect(t *testing.T) {
	cases := []struct {
		name      string
		registry  string
		explicit  *bool // nil = unset
		wantInsec bool
	}{
		{"local host.docker.internal auto-on", "host.docker.internal:5000/snaphost", nil, true},
		{"local registry: auto-on", "registry:5000/snaphost", nil, true},
		{"localhost auto-on", "localhost:5000", nil, true},
		{"127.0.0.1 auto-on", "127.0.0.1:5000", nil, true},
		{"cr.yandex defaults secure", "cr.yandex/abc123", nil, false},
		{"explicit true wins over auto-secure", "cr.yandex/abc123", ptrBool(true), true},
		{"explicit false wins over auto-insecure", "host.docker.internal:5000", ptrBool(false), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Set required env vars for Load().
			t.Setenv("REGISTRY_URL", tc.registry)
			t.Setenv("WEBHOOK_SECRET", "test-secret")

			if tc.explicit != nil {
				if *tc.explicit {
					t.Setenv("REGISTRY_INSECURE", "true")
				} else {
					t.Setenv("REGISTRY_INSECURE", "false")
				}
			} else {
				os.Unsetenv("REGISTRY_INSECURE")
			}

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() error: %v", err)
			}

			if cfg.RegistryInsecure != tc.wantInsec {
				t.Errorf("RegistryInsecure = %v, want %v", cfg.RegistryInsecure, tc.wantInsec)
			}
		})
	}
}

func TestIsLocalRegistry(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"localhost:5000", true},
		{"127.0.0.1:5000", true},
		{"host.docker.internal:5000/snaphost", true},
		{"registry:5000/snaphost", true},
		{"cr.yandex/abc123", false},
		{"ghcr.io/myorg/myimage", false},
		{"", false},
	}

	for _, tc := range cases {
		t.Run(tc.url, func(t *testing.T) {
			got := isLocalRegistry(tc.url)
			if got != tc.want {
				t.Errorf("isLocalRegistry(%q) = %v, want %v", tc.url, got, tc.want)
			}
		})
	}
}

func ptrBool(b bool) *bool { return &b }
