package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
)

func newTestClient(t *testing.T, srv *httptest.Server) *HTTPClient {
	t.Helper()
	c := NewHTTPClient(srv.URL, "secret", zap.NewNop())
	t.Cleanup(srv.Close)
	return c
}

func startServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

func TestGenerateDockerfile_HappyPath(t *testing.T) {
	resp := GenerateResponse{Dockerfile: "FROM alpine", ExposePort: 8080}
	body, _ := json.Marshal(resp)
	srv := startServer(t, 200, string(body))
	c := newTestClient(t, srv)

	got, err := c.GenerateDockerfile(context.Background(), GenerateRequest{DeployID: "d", UserID: "u"})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got.Dockerfile != "FROM alpine" {
		t.Errorf("Dockerfile mismatch: %q", got.Dockerfile)
	}
}

func TestGenerateDockerfile_422_Refused(t *testing.T) {
	srv := startServer(t, 422, "cannot generate")
	c := newTestClient(t, srv)
	_, err := c.GenerateDockerfile(context.Background(), GenerateRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrAIRefused) {
		t.Errorf("errors.Is(err, ErrAIRefused) = false; got %v", err)
	}
	if errors.Is(err, ErrAIUnavailable) {
		t.Error("422 should not be ErrAIUnavailable")
	}
}

func TestGenerateDockerfile_429_Unavailable(t *testing.T) {
	srv := startServer(t, 429, "slow down")
	c := newTestClient(t, srv)
	_, err := c.GenerateDockerfile(context.Background(), GenerateRequest{})
	if !errors.Is(err, ErrAIUnavailable) {
		t.Errorf("errors.Is(err, ErrAIUnavailable) = false; got %v", err)
	}
}

func TestGenerateDockerfile_500_Unavailable(t *testing.T) {
	srv := startServer(t, 500, "boom")
	c := newTestClient(t, srv)
	_, err := c.GenerateDockerfile(context.Background(), GenerateRequest{})
	if !errors.Is(err, ErrAIUnavailable) {
		t.Errorf("errors.Is(err, ErrAIUnavailable) = false; got %v", err)
	}
}

func TestGenerateDockerfile_503_Unavailable(t *testing.T) {
	srv := startServer(t, 503, "down")
	c := newTestClient(t, srv)
	_, err := c.GenerateDockerfile(context.Background(), GenerateRequest{})
	if !errors.Is(err, ErrAIUnavailable) {
		t.Errorf("errors.Is(err, ErrAIUnavailable) = false; got %v", err)
	}
}

func TestGenerateDockerfile_400_Refused(t *testing.T) {
	// Other 4xx (not 422): our request is wrong, retry won't help → permanent.
	srv := startServer(t, 400, "bad request")
	c := newTestClient(t, srv)
	_, err := c.GenerateDockerfile(context.Background(), GenerateRequest{})
	if !errors.Is(err, ErrAIRefused) {
		t.Errorf("errors.Is(err, ErrAIRefused) = false; got %v", err)
	}
}

func TestGenerateDockerfile_NetworkErrorUnavailable(t *testing.T) {
	srv := startServer(t, 200, "{}")
	c := newTestClient(t, srv)
	srv.Close() // force connection refused on next call
	_, err := c.GenerateDockerfile(context.Background(), GenerateRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrAIUnavailable) {
		t.Errorf("errors.Is(err, ErrAIUnavailable) = false; got %v", err)
	}
}

func TestGenerateDockerfile_DecodeFailureRefused(t *testing.T) {
	// 200 OK but body not JSON — surfaces as permanent (contract bug),
	// not transient.
	srv := startServer(t, 200, "not json")
	c := newTestClient(t, srv)
	_, err := c.GenerateDockerfile(context.Background(), GenerateRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrAIRefused) {
		t.Errorf("errors.Is(err, ErrAIRefused) = false; got %v", err)
	}
}
