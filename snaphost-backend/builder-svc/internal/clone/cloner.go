package clone

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport/client"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"go.uber.org/zap"

	"snaphost/builder-svc/internal/logs"
)

// newPinnedHTTPClient returns an *http.Client whose DialContext is locked
// to validated.IP and whose TLS layer verifies the server certificate
// against validated.Host (not the IP). Any dial whose host portion
// differs from validated.Host is rejected — defence against redirects,
// URL parsing bugs, or future go-git fetches mid-clone.
func newPinnedHTTPClient(validated *ValidatedURL) *http.Client {
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	pinnedIP := validated.IP.String()
	pinnedHost := validated.Host
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, fmt.Errorf("clone: parse dial addr %q: %w", addr, err)
			}
			if host != pinnedHost {
				return nil, fmt.Errorf("clone: dial to unexpected host %q (pinned to %s)", host, pinnedHost)
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(pinnedIP, port))
		},
		TLSClientConfig: &tls.Config{
			ServerName: pinnedHost,
			MinVersion: tls.VersionTLS12,
		},
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		IdleConnTimeout:       90 * time.Second,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   10 * time.Minute,
	}
}

// CloneOptions contains all parameters required to clone a repository.
type CloneOptions struct {
	// Validated is the result of ValidateRepoURL — clone connections MUST
	// be pinned to Validated.IP (wired in Task 4.2; this struct exists in
	// 4.1 to carry the value through the call chain unchanged).
	Validated *ValidatedURL
	// Branch is the Git branch to check out.
	Branch string
	// DeployID is used for log publishing.
	DeployID string
	// WorkdirPath is the directory to clone into.
	WorkdirPath string
	// Timeout is the maximum duration for the clone operation.
	Timeout time.Duration
	// MaxSizeMB is the maximum allowed total size of the cloned repo in megabytes.
	MaxSizeMB int64
	// AuthToken enables HTTP basic auth for private repos (Task 14b-3).
	// Passed to the transport only — never embedded in the URL, never
	// logged. Empty means anonymous clone.
	AuthToken string
	// AuthUsername is the basic-auth username sent with AuthToken.
	// Defaults to "git", which GitHub and GitLab accept for tokens.
	AuthUsername string
}

// CloneResult holds the outcome of a successful clone.
type CloneResult struct {
	// CommitSHA is the HEAD commit SHA of the cloned repository.
	CommitSHA string
	// SizeMB is the total size of the cloned repository in megabytes.
	SizeMB int64
}

// Cloner provides Git clone functionality with logging and security checks.
type Cloner struct {
	pub logs.Publisher
	log *zap.Logger

	// installMu serialises mutations of go-git's package-global
	// client.Protocols map during PlainCloneContext. Required as long as
	// go-git v5 has no per-clone HTTPClient option (see TODO in Clone).
	installMu sync.Mutex
}

// NewCloner creates a new Cloner instance.
func NewCloner(pub logs.Publisher, log *zap.Logger) *Cloner {
	return &Cloner{pub: pub, log: log}
}

// Clone performs a shallow, single-branch clone of the repository into the
// workdir. It enforces a size limit and checks for symlink escape attacks.
func (c *Cloner) Clone(ctx context.Context, opts CloneOptions) (*CloneResult, error) {
	cloneCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	if opts.Validated == nil {
		return nil, fmt.Errorf("clone: ValidatedURL is required")
	}

	_ = c.pub.Publish(opts.DeployID, logs.LogLine{
		Stage:     "clone",
		Text:      fmt.Sprintf("cloning %s branch %s", opts.Validated.URL, opts.Branch),
		Level:     "info",
		Timestamp: time.Now().UTC(),
	})

	progressWriter := logs.NewStreamingWriter(c.pub, opts.DeployID, "clone")

	// Install a pinned-IP transport for the duration of the clone so
	// go-git connects to the validated IP instead of re-resolving DNS
	// (TOCTOU defence against DNS rebinding to e.g. 169.254.169.254).
	// The mutex serialises concurrent clones — required because go-git
	// v5.12.0 exposes only a package-global Protocols map.
	// TODO(go-git): drop the install/restore dance once go-git ships a
	// per-clone HTTPClient option on git.CloneOptions.
	c.installMu.Lock()
	defer c.installMu.Unlock()

	prev := client.Protocols["https"]
	defer func() {
		if prev != nil {
			client.InstallProtocol("https", prev)
		} else {
			client.InstallProtocol("https", githttp.DefaultClient)
		}
	}()

	pinnedClient := newPinnedHTTPClient(opts.Validated)
	client.InstallProtocol("https", githttp.NewClient(pinnedClient))

	gitOpts := &git.CloneOptions{
		URL:           opts.Validated.URL,
		Depth:         1,
		SingleBranch:  true,
		ReferenceName: plumbing.NewBranchReferenceName(opts.Branch),
		Progress:      progressWriter,
	}
	if opts.AuthToken != "" {
		username := opts.AuthUsername
		if username == "" {
			username = "git"
		}
		gitOpts.Auth = &githttp.BasicAuth{Username: username, Password: opts.AuthToken}
	}

	repo, err := git.PlainCloneContext(cloneCtx, opts.WorkdirPath, false, gitOpts)
	if err != nil {
		// go-git errors carry the URL (credential-free — auth travels in
		// the transport) but never the basic-auth secret.
		return nil, fmt.Errorf("git clone: %w", err)
	}
	progressWriter.Flush()

	// Compute total size and check for symlink escapes.
	var totalSize int64
	absWorkdir, err := filepath.Abs(opts.WorkdirPath)
	if err != nil {
		return nil, fmt.Errorf("resolve workdir path: %w", err)
	}

	err = filepath.Walk(opts.WorkdirPath, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		// Check symlinks for escape.
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := filepath.EvalSymlinks(path)
			if err != nil {
				return fmt.Errorf("resolve symlink %s: %w", path, err)
			}
			absTarget, err := filepath.Abs(target)
			if err != nil {
				return fmt.Errorf("resolve absolute path of symlink target: %w", err)
			}
			if !isWithin(absTarget, absWorkdir) {
				return fmt.Errorf("symlink_escape: %s points to %s outside workdir", path, absTarget)
			}
		}

		if !info.IsDir() {
			totalSize += info.Size()
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk workdir: %w", err)
	}

	sizeMB := totalSize / (1024 * 1024)
	if sizeMB > opts.MaxSizeMB {
		return nil, fmt.Errorf("repository size %d MB exceeds limit of %d MB", sizeMB, opts.MaxSizeMB)
	}

	// Extract HEAD commit SHA.
	head, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("get HEAD: %w", err)
	}

	_ = c.pub.Publish(opts.DeployID, logs.LogLine{
		Stage:     "clone",
		Text:      fmt.Sprintf("clone complete: commit %s, size %d MB", head.Hash().String(), sizeMB),
		Level:     "info",
		Timestamp: time.Now().UTC(),
	})

	return &CloneResult{
		CommitSHA: head.Hash().String(),
		SizeMB:    sizeMB,
	}, nil
}

// isWithin reports whether child is the same as parent or a descendant of it.
// Returns false on any error resolving absolute paths or computing the
// relative path. Distinguishes escape (".." segment) from hidden files
// inside the parent (e.g. ".config") — the previous implementation that
// checked rel[0] != '.' conflated the two.
func isWithin(child, parent string) bool {
	absChild, err := filepath.Abs(child)
	if err != nil {
		return false
	}
	absParent, err := filepath.Abs(parent)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absParent, absChild)
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}
