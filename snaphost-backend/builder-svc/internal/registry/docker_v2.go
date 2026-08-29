package registry

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

// DockerV2Client deletes images from a Docker Registry v2 instance via the
// HTTP API. Delete is a two-step process per the v2 spec:
//  1. GET /v2/<name>/manifests/<tag> to resolve the manifest digest.
//  2. DELETE /v2/<name>/manifests/<digest> using the resolved digest.
//
// Tag-based DELETE is not supported by the spec. The registry must be
// configured with REGISTRY_STORAGE_DELETE_ENABLED=true; otherwise DELETE
// returns 405.
type DockerV2Client struct {
	httpClient *http.Client
	log        *zap.Logger
}

// NewDockerV2Client constructs a Docker Registry v2 cleanup client.
func NewDockerV2Client(log *zap.Logger) *DockerV2Client {
	return &DockerV2Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		log:        log,
	}
}

// DeleteImage removes the manifest for the given image ref from the
// registry. The ref must be in the form "<host>/<name>:<tag>", e.g.
// "registry:5000/snaphost/proj-abc:deploy-xyz". 404 on either step is
// treated as success (idempotent — image already gone).
func (c *DockerV2Client) DeleteImage(ctx context.Context, imageRef string) error {
	host, name, tag, err := parseImageRef(imageRef)
	if err != nil {
		return fmt.Errorf("parse image ref %q: %w", imageRef, err)
	}

	digest, err := c.resolveDigest(ctx, host, name, tag)
	if err != nil {
		return fmt.Errorf("resolve digest for %s:%s: %w", name, tag, err)
	}
	if digest == "" {
		c.log.Info("image already absent from registry, nothing to delete",
			zap.String("image_ref", imageRef))
		return nil
	}

	deleteURL := fmt.Sprintf("http://%s/v2/%s/manifests/%s", host, name, digest)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, deleteURL, nil)
	if err != nil {
		return fmt.Errorf("create DELETE request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("DELETE request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNotFound {
		c.log.Info("image deleted from registry",
			zap.String("image_ref", imageRef),
			zap.String("digest", digest),
			zap.Int("status", resp.StatusCode))
		return nil
	}
	return fmt.Errorf("DELETE returned unexpected status %d", resp.StatusCode)
}

// resolveDigest performs a GET against the v2 manifests endpoint with the
// distribution manifest Accept header and returns the Docker-Content-Digest
// response header value. Returns ("", nil) if the manifest is absent (404).
func (c *DockerV2Client) resolveDigest(ctx context.Context, host, name, tag string) (string, error) {
	manifestURL := fmt.Sprintf("http://%s/v2/%s/manifests/%s", host, name, tag)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return "", fmt.Errorf("create GET request: %w", err)
	}
	// Required for the registry to return the v2 manifest (and the digest header).
	req.Header.Set("Accept", "application/vnd.docker.distribution.manifest.v2+json")
	req.Header.Add("Accept", "application/vnd.oci.image.manifest.v1+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("GET request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET returned unexpected status %d", resp.StatusCode)
	}

	digest := resp.Header.Get("Docker-Content-Digest")
	if digest == "" {
		return "", fmt.Errorf("registry did not return Docker-Content-Digest header")
	}
	return digest, nil
}

// parseImageRef splits "<host>/<name>:<tag>" into its three components.
// The host portion may itself contain a colon (e.g. "registry:5000").
// Host/name split is at the FIRST forward slash; name/tag split is at the
// LAST colon.
func parseImageRef(ref string) (host, name, tag string, err error) {
	slashIdx := strings.Index(ref, "/")
	if slashIdx < 0 {
		return "", "", "", fmt.Errorf("missing host: %q", ref)
	}
	host = ref[:slashIdx]
	rest := ref[slashIdx+1:]

	colonIdx := strings.LastIndex(rest, ":")
	if colonIdx < 0 {
		return "", "", "", fmt.Errorf("missing tag: %q", ref)
	}
	name = rest[:colonIdx]
	tag = rest[colonIdx+1:]
	if name == "" || tag == "" {
		return "", "", "", fmt.Errorf("empty name or tag: %q", ref)
	}
	return host, name, tag, nil
}
