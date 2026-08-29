package build

import (
	"context"
	"testing"

	"github.com/moby/buildkit/session/auth"
)

func TestRegistryHost(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    string
		wantErr bool
	}{
		{name: "repository prefix", value: "cr.yandex/registry-id/snaphost", want: "cr.yandex"},
		{name: "explicit HTTPS", value: "https://cr.yandex/registry-id", want: "cr.yandex"},
		{name: "port", value: "registry.example:5000/project", want: "registry.example:5000"},
		{name: "empty", value: "", wantErr: true},
		{name: "missing host", value: "://bad", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := registryHost(tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("registryHost() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("registryHost() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestYandexIAMAuthProviderSupportsPublicRegistryTokenAuthority(t *testing.T) {
	provider := newYandexIAMAuthProvider("cr.yandex", func(context.Context) (string, error) {
		return "test-iam-token", nil
	})
	server, ok := provider.(auth.AuthServer)
	if !ok {
		t.Fatal("standard provider does not implement auth.AuthServer")
	}

	authority, err := server.GetTokenAuthority(context.Background(), &auth.GetTokenAuthorityRequest{
		Host: "registry-1.docker.io",
		Salt: []byte("test-salt"),
	})
	if err != nil {
		t.Fatalf("Docker Hub token authority failed: %v", err)
	}
	if len(authority.PublicKey) == 0 {
		t.Fatal("Docker Hub token authority returned an empty public key")
	}
}

func TestYandexIAMAuthProviderScopesCredentialsToExactHost(t *testing.T) {
	provider := newYandexIAMAuthProvider("cr.yandex", func(context.Context) (string, error) {
		return "test-iam-token", nil
	})
	server := provider.(auth.AuthServer)

	yandex, err := server.Credentials(context.Background(), &auth.CredentialsRequest{Host: "cr.yandex"})
	if err != nil {
		t.Fatalf("Yandex credentials failed: %v", err)
	}
	if yandex.Username != "iam" || yandex.Secret != "test-iam-token" {
		t.Fatalf("unexpected Yandex credentials: username=%q secretSet=%v", yandex.Username, yandex.Secret != "")
	}

	lookalike, err := server.Credentials(context.Background(), &auth.CredentialsRequest{Host: "cr.yandex.evil.example"})
	if err != nil {
		t.Fatalf("lookalike credentials failed: %v", err)
	}
	if lookalike.Username != "" || lookalike.Secret != "" {
		t.Fatal("Yandex credentials leaked to a lookalike host")
	}
}
