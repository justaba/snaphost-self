package scan

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildArgs_InsecureTrue(t *testing.T) {
	s := &Scanner{registryInsecure: true}
	args := s.buildArgs("registry:5000/img:tag")

	if !containsStr(args, "--insecure") {
		t.Fatalf("expected --insecure in args, got: %v", args)
	}
	if args[len(args)-1] != "registry:5000/img:tag" {
		t.Fatalf("expected imageRef as last arg, got: %v", args)
	}
}

func TestConfigureRegistryAuthUsesScopedTemporaryDockerConfig(t *testing.T) {
	s := &Scanner{
		registryHost: "cr.yandex",
		credentials: func(context.Context) (string, string, error) {
			return "iam", "short-lived-token", nil
		},
	}
	cmd := exec.Command("trivy")
	cmd.Env = []string{"DOCKER_CONFIG=old", "PATH=test"}

	cleanup, err := s.configureRegistryAuth(context.Background(), cmd, "cr.yandex/registry/project:image")
	if err != nil {
		t.Fatalf("configureRegistryAuth() error = %v", err)
	}

	dir := envValue(cmd.Env, "DOCKER_CONFIG")
	if dir == "" || dir == "old" {
		t.Fatalf("unexpected DOCKER_CONFIG %q", dir)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("read temporary config: %v", err)
	}
	var config struct {
		Auths map[string]struct {
			Auth string `json:"auth"`
		} `json:"auths"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatalf("decode temporary config: %v", err)
	}
	wantAuth := base64.StdEncoding.EncodeToString([]byte("iam:short-lived-token"))
	if len(config.Auths) != 1 || config.Auths["cr.yandex"].Auth != wantAuth {
		t.Fatalf("temporary config is not scoped to the expected registry")
	}
	if strings.Contains(strings.Join(cmd.Args, " "), "short-lived-token") {
		t.Fatal("registry token leaked into command arguments")
	}

	cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("temporary auth directory still exists after cleanup: %v", err)
	}
}

func TestConfigureRegistryAuthRejectsUnexpectedImageHost(t *testing.T) {
	called := false
	s := &Scanner{
		registryHost: "cr.yandex",
		credentials: func(context.Context) (string, string, error) {
			called = true
			return "iam", "token", nil
		},
	}

	if _, err := s.configureRegistryAuth(context.Background(), exec.Command("trivy"), "cr.yandex.evil.example/image:tag"); err == nil {
		t.Fatal("expected unexpected image host to be rejected")
	}
	if called {
		t.Fatal("credential provider called for unexpected image host")
	}
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return strings.TrimPrefix(item, prefix)
		}
	}
	return ""
}

func TestBuildArgs_InsecureFalse(t *testing.T) {
	s := &Scanner{registryInsecure: false}
	args := s.buildArgs("cr.yandex/abc123/img:tag")

	if containsStr(args, "--insecure") {
		t.Fatalf("did not expect --insecure in args, got: %v", args)
	}
	if args[len(args)-1] != "cr.yandex/abc123/img:tag" {
		t.Fatalf("expected imageRef as last arg, got: %v", args)
	}
}

func TestBuildArgs_CoreFlagsPresent(t *testing.T) {
	s := &Scanner{registryInsecure: false}
	args := s.buildArgs("some/image:latest")

	required := []string{"image", "--image-src", "remote", "--format", "json", "--severity", "CRITICAL,HIGH", "--exit-code", "0", "--no-progress", "--timeout", "2m"}
	for _, flag := range required {
		if !containsStr(args, flag) {
			t.Errorf("expected %q in args, got: %v", flag, args)
		}
	}
}

func containsStr(ss []string, target string) bool {
	for _, s := range ss {
		if s == target {
			return true
		}
	}
	return false
}
