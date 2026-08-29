package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"
)

const testDigest = "sha256:deadbeefcafe0000000000000000000000000000000000000000000000000000"

func newClient(t *testing.T, server *httptest.Server) (*DockerV2Client, string) {
	t.Helper()
	host := strings.TrimPrefix(server.URL, "http://")
	c := NewDockerV2Client(zap.NewNop())
	return c, host
}

func TestDockerV2Client_HappyPath(t *testing.T) {
	var deleteCalled bool
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/snaphost/proj-abc/manifests/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Docker-Content-Digest", testDigest)
			w.WriteHeader(http.StatusOK)
		case http.MethodDelete:
			deleteCalled = true
			if !strings.HasSuffix(r.URL.Path, testDigest) {
				t.Errorf("DELETE not by digest: %s", r.URL.Path)
			}
			w.WriteHeader(http.StatusAccepted)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c, host := newClient(t, srv)

	err := c.DeleteImage(context.Background(), host+"/snaphost/proj-abc:deploy1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !deleteCalled {
		t.Fatal("DELETE was not called")
	}
}

func TestDockerV2Client_AlreadyAbsent(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c, host := newClient(t, srv)

	if err := c.DeleteImage(context.Background(), host+"/snaphost/proj:dep"); err != nil {
		t.Fatalf("expected nil error for absent image, got: %v", err)
	}
}

func TestDockerV2Client_DeleteRaces404(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Docker-Content-Digest", testDigest)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c, host := newClient(t, srv)

	if err := c.DeleteImage(context.Background(), host+"/snaphost/proj:dep"); err != nil {
		t.Fatalf("expected nil error on DELETE 404 race, got: %v", err)
	}
}

func TestDockerV2Client_GetReturns500(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c, host := newClient(t, srv)

	err := c.DeleteImage(context.Background(), host+"/snaphost/proj:dep")
	if err == nil {
		t.Fatal("expected error on GET 500")
	}
}

func TestDockerV2Client_DeleteReturns405(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Docker-Content-Digest", testDigest)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c, host := newClient(t, srv)

	err := c.DeleteImage(context.Background(), host+"/snaphost/proj:dep")
	if err == nil {
		t.Fatal("expected error on DELETE 405 (delete not enabled)")
	}
}

func TestDockerV2Client_DeleteReturns500(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Docker-Content-Digest", testDigest)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c, host := newClient(t, srv)

	if err := c.DeleteImage(context.Background(), host+"/snaphost/proj:dep"); err == nil {
		t.Fatal("expected error on DELETE 500")
	}
}

func TestDockerV2Client_NetworkFailure(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	host := strings.TrimPrefix(srv.URL, "http://")
	srv.Close() // close immediately to force connection refused

	c := NewDockerV2Client(zap.NewNop())
	if err := c.DeleteImage(context.Background(), host+"/snaphost/proj:dep"); err == nil {
		t.Fatal("expected error on network failure")
	}
}

func TestDockerV2Client_MissingDigestHeader(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c, host := newClient(t, srv)

	if err := c.DeleteImage(context.Background(), host+"/snaphost/proj:dep"); err == nil {
		t.Fatal("expected error when Docker-Content-Digest header is missing")
	}
}

func TestParseImageRef(t *testing.T) {
	cases := []struct {
		name     string
		ref      string
		wantHost string
		wantName string
		wantTag  string
		wantErr  bool
	}{
		{"local registry with port", "registry:5000/snaphost/proj-abc:deploy-xyz", "registry:5000", "snaphost/proj-abc", "deploy-xyz", false},
		{"yandex cr", "cr.yandex/abc123/proj-x:v1", "cr.yandex", "abc123/proj-x", "v1", false},
		{"no slash", "myimage:tag", "", "", "", true},
		{"no tag", "registry:5000/snaphost/proj", "", "", "", true},
		{"empty tag", "registry:5000/snaphost/proj:", "", "", "", true},
		{"empty name", "registry:5000/:tag", "", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, n, tag, err := parseImageRef(tc.ref)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got host=%q name=%q tag=%q", h, n, tag)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if h != tc.wantHost || n != tc.wantName || tag != tc.wantTag {
				t.Errorf("got (%q, %q, %q), want (%q, %q, %q)", h, n, tag, tc.wantHost, tc.wantName, tc.wantTag)
			}
		})
	}
}
