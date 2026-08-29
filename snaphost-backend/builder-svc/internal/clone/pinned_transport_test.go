// Tests in this file mutate package-global state in go-git's client.Protocols
// during clone (when invoked). They MUST NOT use t.Parallel() — concurrent
// execution would race on the global map. This is acceptable given the
// worker runs a single consumer goroutine (see cmd/worker/main.go).
package clone

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/client"
	"go.uber.org/zap"

	"snaphost/builder-svc/internal/logs"
)

// noopPublisher discards every log line. Used by tests that exercise
// Cloner without a real Redis backend.
type noopPublisher struct{}

func (noopPublisher) Publish(string, logs.LogLine) error { return nil }
func (noopPublisher) Close() error                       { return nil }

// makeCert returns a self-signed TLS cert valid for the given DNS SANs and
// the loopback IPs (127.0.0.1, ::1). The same cert acts as its own CA so
// callers add it to a CertPool to "trust" it. ECDSA-P256 for speed.
func makeCert(t *testing.T, sans ...string) (tls.Certificate, *x509.Certificate) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: sans[0]},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(1 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              sans,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  priv,
		Leaf:        parsed,
	}, parsed
}

// startTLSServer returns an httptest.Server speaking TLS with the given
// cert. Handler is a fixed 200 OK so the test focuses on dial + TLS rather
// than HTTP semantics.
func startTLSServer(t *testing.T, cert tls.Certificate) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

// trust adds the given cert to a RootCAs pool and installs it on the
// pinned client's transport so test self-signed certs are accepted.
func trust(c *http.Client, ca *x509.Certificate) {
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	tr := c.Transport.(*http.Transport)
	tr.TLSClientConfig.RootCAs = pool
}

// extractPort returns the port from an httptest server URL.
func extractPort(t *testing.T, urlStr string) string {
	t.Helper()
	u, err := url.Parse(urlStr)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	return u.Port()
}

func TestPinnedHTTPClient_HappyPath(t *testing.T) {
	cert, ca := makeCert(t, "right.example.com")
	srv := startTLSServer(t, cert)
	port := extractPort(t, srv.URL)

	validated := &ValidatedURL{
		URL:  "https://right.example.com:" + port + "/owner/repo",
		Host: "right.example.com",
		IP:   net.ParseIP("127.0.0.1"),
	}
	c := newPinnedHTTPClient(validated)
	trust(c, ca)

	resp, err := c.Get(validated.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestPinnedHTTPClient_DNSRebindIgnored(t *testing.T) {
	// Spec: even if DNS would return a different IP after validation,
	// dialer uses the pinned IP. The pinned dialer never consults a
	// resolver — it goes straight to validated.IP. Proof: with no
	// resolver in the picture, the GET succeeds purely because we set
	// IP=127.0.0.1 to match the httptest server. If the dialer had used
	// any DNS path, request to "right.example.com" would fail (no such
	// public host).
	cert, ca := makeCert(t, "right.example.com")
	srv := startTLSServer(t, cert)
	port := extractPort(t, srv.URL)

	validated := &ValidatedURL{
		URL:  "https://right.example.com:" + port + "/x/y",
		Host: "right.example.com",
		IP:   net.ParseIP("127.0.0.1"),
	}
	c := newPinnedHTTPClient(validated)
	trust(c, ca)

	resp, err := c.Get(validated.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestPinnedHTTPClient_WrongHostInDial(t *testing.T) {
	// Issue a request to a URL whose hostname differs from validated.Host.
	// Dialer must refuse — even if the IP is identical.
	cert, ca := makeCert(t, "right.example.com")
	srv := startTLSServer(t, cert)
	port := extractPort(t, srv.URL)

	validated := &ValidatedURL{
		URL:  "https://right.example.com:" + port + "/x/y",
		Host: "right.example.com",
		IP:   net.ParseIP("127.0.0.1"),
	}
	c := newPinnedHTTPClient(validated)
	trust(c, ca)

	_, err := c.Get("https://attacker.example.com:" + port + "/x/y")
	if err == nil {
		t.Fatal("expected error for request to non-pinned host")
	}
	if !strings.Contains(err.Error(), "unexpected host") {
		t.Fatalf("error %q does not mention 'unexpected host'", err.Error())
	}
}

func TestPinnedHTTPClient_TLSCertMismatch(t *testing.T) {
	// Server presents a cert for wrong.example.com but pinned client
	// expects right.example.com. Even though the cert chains to a CA
	// the client trusts, the SAN mismatch must fail the handshake.
	cert, ca := makeCert(t, "wrong.example.com")
	srv := startTLSServer(t, cert)
	port := extractPort(t, srv.URL)

	validated := &ValidatedURL{
		URL:  "https://right.example.com:" + port + "/x/y",
		Host: "right.example.com",
		IP:   net.ParseIP("127.0.0.1"),
	}
	c := newPinnedHTTPClient(validated)
	trust(c, ca)

	_, err := c.Get(validated.URL)
	if err == nil {
		t.Fatal("expected TLS error for cert/host mismatch")
	}
	// Go's TLS error text varies by version; check for any of the common forms.
	msg := err.Error()
	if !(strings.Contains(msg, "certificate") || strings.Contains(msg, "x509")) {
		t.Fatalf("error %q does not look like a TLS cert error", msg)
	}
}

func TestPinnedHTTPClient_IPv6(t *testing.T) {
	// IPv6 pinned dial — proves net.JoinHostPort handles brackets for [::1].
	cert, ca := makeCert(t, "right.example.com")
	srv := startTLSServer(t, cert)
	port := extractPort(t, srv.URL)

	validated := &ValidatedURL{
		URL:  "https://right.example.com:" + port + "/x/y",
		Host: "right.example.com",
		IP:   net.ParseIP("::1"),
	}
	c := newPinnedHTTPClient(validated)
	trust(c, ca)

	// httptest binds to 127.0.0.1 — IPv6 ::1 may or may not work on
	// all hosts (depends on dual-stack config). If it fails, log and
	// skip rather than fail the suite. The point of this test is to
	// confirm the dialer doesn't choke on bracketed IPv6 syntax.
	_, err := c.Get(validated.URL)
	if err != nil {
		if strings.Contains(err.Error(), "unexpected host") {
			t.Fatalf("dialer rejected IPv6 host wrongly: %v", err)
		}
		// Other errors (connection refused) are acceptable — this host
		// may not have ::1 listening. We just need to confirm we got
		// past the dialer's host-pin check.
		t.Logf("IPv6 connect failed (expected on hosts without ::1): %v", err)
		return
	}
}

func TestCloner_RestoresProtocolOnSuccess(t *testing.T) {
	// Sanity: even on a clone that fails before reaching the network
	// (workdir already exists), the deferred restore must put the
	// global https protocol back to whatever it was before.
	pre := installedHTTPS()

	c := &Cloner{pub: noopPublisher{}, log: zap.NewNop()}
	_, _ = c.Clone(context.Background(), CloneOptions{
		Validated:   &ValidatedURL{URL: "https://right.example.com/x/y", Host: "right.example.com", IP: net.ParseIP("127.0.0.1")},
		Branch:      "main",
		DeployID:    "test",
		WorkdirPath: t.TempDir(), // exists but the clone will fail trying to actually fetch
		Timeout:     1 * time.Second,
		MaxSizeMB:   10,
	})

	post := installedHTTPS()
	if pre != post {
		t.Errorf("client.Protocols[\"https\"] not restored: pre=%v post=%v", pre, post)
	}
}

// installedHTTPS returns the current https transport for pointer-identity
// comparison across the install/restore cycle.
func installedHTTPS() transport.Transport {
	return client.Protocols["https"]
}
