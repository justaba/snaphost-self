// Package registry provides cleanup operations for container registries.
// Used by the pipeline to remove vulnerable images that failed Trivy
// scanning, preventing them from accumulating in the registry.
package registry

import "context"

// Client is the registry cleanup interface. Each backend (Docker v2,
// Yandex CR, etc.) implements this. The pipeline uses it without knowing
// which registry is configured — selection happens at worker startup
// based on cfg.
type Client interface {
	// DeleteImage removes the image at the given ref from the registry.
	// imageRef is the full reference, e.g.
	// "registry:5000/snaphost/proj-abc:deploy-xyz". Returns nil if
	// successful or if the image is already gone (404 treated as success).
	DeleteImage(ctx context.Context, imageRef string) error
}
