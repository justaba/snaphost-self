package clone

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
)

// fakeResolver lets each test drive LookupIP results without real DNS.
type fakeResolver struct {
	ips []net.IP
	err error
}

func (f *fakeResolver) LookupIP(_ context.Context, _ string, _ string) ([]net.IP, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.ips, nil
}

func ipv4(s string) net.IP {
	ip := net.ParseIP(s)
	if ip == nil {
		panic("bad test IP: " + s)
	}
	return ip
}

func TestValidateRepoURL_IPFilter(t *testing.T) {
	allowed := []string{"github.com"}
	const repoURL = "https://github.com/owner/repo"

	cases := []struct {
		name        string
		resolverIPs []net.IP
		resolverErr error
		wantOK      bool
		wantIP      string // expected validated IP on success
		wantErr     error  // sentinel expected via errors.Is on failure
	}{
		{"happy public IPv4", []net.IP{ipv4("8.8.8.8")}, nil, true, "8.8.8.8", nil},
		{"happy public IPv6", []net.IP{ipv4("2001:4860:4860::8888")}, nil, true, "2001:4860:4860::8888", nil},
		{"loopback IPv4", []net.IP{ipv4("127.0.0.1")}, nil, false, "", ErrIPFiltered},
		{"loopback IPv6", []net.IP{ipv4("::1")}, nil, false, "", ErrIPFiltered},
		{"private 192.168", []net.IP{ipv4("192.168.1.1")}, nil, false, "", ErrIPFiltered},
		{"private 10/8", []net.IP{ipv4("10.0.0.5")}, nil, false, "", ErrIPFiltered},
		{"link-local IPv4", []net.IP{ipv4("169.254.0.1")}, nil, false, "", ErrIPFiltered},
		{"cloud metadata IPv4", []net.IP{ipv4("169.254.169.254")}, nil, false, "", ErrIPFiltered},
		{"cloud metadata IPv6 fd00:ec2", []net.IP{ipv4("fd00:ec2::254")}, nil, false, "", ErrIPFiltered},
		{"IPv4-mapped IPv6 metadata", []net.IP{ipv4("::ffff:169.254.169.254")}, nil, false, "", ErrIPFiltered},
		{"link-local IPv6", []net.IP{ipv4("fe80::1")}, nil, false, "", ErrIPFiltered},
		{"unspecified", []net.IP{ipv4("0.0.0.0")}, nil, false, "", ErrIPFiltered},
		{"multicast", []net.IP{ipv4("224.0.0.1")}, nil, false, "", ErrIPFiltered},
		{"mixed private + public picks public", []net.IP{ipv4("10.0.0.1"), ipv4("8.8.8.8")}, nil, true, "8.8.8.8", nil},
		{"all filtered", []net.IP{ipv4("127.0.0.1"), ipv4("192.168.1.1")}, nil, false, "", ErrIPFiltered},
		{"DNS lookup failure", nil, errors.New("nxdomain"), false, "", ErrResolverFailure},
		{"empty result", []net.IP{}, nil, false, "", ErrResolverFailure},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &fakeResolver{ips: tc.resolverIPs, err: tc.resolverErr}
			got, err := ValidateRepoURL(context.Background(), repoURL, allowed, r)
			if tc.wantOK {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got == nil {
					t.Fatal("got nil ValidatedURL")
				}
				if got.IP.String() != tc.wantIP {
					t.Errorf("IP: got %s want %s", got.IP, tc.wantIP)
				}
				if got.Host != "github.com" {
					t.Errorf("Host: got %s want github.com", got.Host)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error, got nil; result=%v", got)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Errorf("errors.Is(err, %v) = false; got %v", tc.wantErr, err)
			}
		})
	}
}

func TestValidateRepoURL_IPv4PreferredOverIPv6(t *testing.T) {
	// Both IPs are public; IPv4-first preference is what we expect.
	r := &fakeResolver{ips: []net.IP{ipv4("2001:4860:4860::8888"), ipv4("8.8.8.8")}}
	got, err := ValidateRepoURL(context.Background(), "https://github.com/o/r", []string{"github.com"}, r)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got.IP.String() != "8.8.8.8" {
		t.Errorf("expected IPv4 8.8.8.8 preferred, got %s", got.IP)
	}
}

func TestValidateRepoURL_IPv6OnlyFallback(t *testing.T) {
	// Resolver returns only IPv6. IPv4-first picker finds nothing, falls
	// back to any-allowed; result.IP must be the IPv6 address.
	r := &fakeResolver{ips: []net.IP{ipv4("2001:4860:4860::8888")}}
	got, err := ValidateRepoURL(context.Background(), "https://github.com/o/r", []string{"github.com"}, r)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got.IP.String() != "2001:4860:4860::8888" {
		t.Errorf("IPv6-only fallback IP: got %s want 2001:4860:4860::8888", got.IP)
	}
}

func TestValidateRepoURLSyntactic(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"valid github url", "https://github.com/owner/repo", false},
		{"valid with .git suffix", "https://github.com/owner/repo.git", false},
		{"http scheme rejected", "http://github.com/owner/repo", true},
		{"ftp scheme rejected", "ftp://github.com/owner/repo", true},
		{"hostname too long (254 chars)", "https://" + strings.Repeat("a", 254) + "/owner/repo", true},
		{"path traversal with ..", "https://github.com/owner/../etc", true},
		{"path with single segment", "https://github.com/owner", true},
		{"path root only", "https://github.com/", true},
		{"path empty", "https://github.com", true},
		{"url too long", "https://github.com/o/" + strings.Repeat("x", 2000), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRepoURLSyntactic(tc.url)
			if (err != nil) != tc.wantErr {
				t.Errorf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalidURL) {
				t.Errorf("errors.Is(err, ErrInvalidURL) = false; got %v", err)
			}
		})
	}
}

func TestValidateRepoURL_HostNotAllowedSentinel(t *testing.T) {
	r := &fakeResolver{ips: []net.IP{ipv4("8.8.8.8")}}
	_, err := ValidateRepoURL(context.Background(), "https://evil.example.com/o/r", []string{"github.com"}, r)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrHostNotAllowed) {
		t.Errorf("errors.Is(err, ErrHostNotAllowed) = false; got %v", err)
	}
}

func TestValidateRepoURL_InvalidURLSentinel(t *testing.T) {
	r := &fakeResolver{ips: []net.IP{ipv4("8.8.8.8")}}
	_, err := ValidateRepoURL(context.Background(), "http://github.com/o/r", []string{"github.com"}, r)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrInvalidURL) {
		t.Errorf("errors.Is(err, ErrInvalidURL) = false; got %v", err)
	}
}

func TestValidateRepoURL_SyntacticChecks(t *testing.T) {
	allowed := []string{"github.com"}
	r := &fakeResolver{ips: []net.IP{ipv4("8.8.8.8")}}

	cases := []struct {
		name string
		url  string
	}{
		{"http scheme rejected", "http://github.com/o/r"},
		{"ftp scheme rejected", "ftp://github.com/o/r"},
		{"host not in allowlist", "https://evil.example.com/o/r"},
		{"empty path", "https://github.com/"},
		{"single segment path", "https://github.com/owner"},
		{"url too long", "https://github.com/o/" + strings.Repeat("x", 2000)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ValidateRepoURL(context.Background(), tc.url, allowed, r); err == nil {
				t.Errorf("expected error for %s", tc.url)
			}
		})
	}
}
