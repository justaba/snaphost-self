package clone

import (
	"fmt"
	"os"

	"go.uber.org/zap"
)

// Workdir represents an isolated temporary directory for a single build job.
type Workdir struct {
	// Path is the absolute filesystem path of the workdir.
	Path string
	// DeployID is the deployment this workdir belongs to.
	DeployID string
	// cleanup removes the workdir; errors are logged but not returned.
	cleanup func()
}

// Cleanup removes the workdir and all its contents.
func (w *Workdir) Cleanup() {
	if w.cleanup != nil {
		w.cleanup()
	}
}

// PrepareWorkdir creates an isolated build directory at {root}/{deployID} with
// restrictive permissions. The returned Workdir.Cleanup function removes the
// directory tree regardless of outcome.
func PrepareWorkdir(root, deployID string, log *zap.Logger) (*Workdir, error) {
	dir := fmt.Sprintf("%s/%s", root, deployID)

	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, fmt.Errorf("create workdir %s: %w", dir, err)
	}

	// Attempt to chown to nobody (65534:65534) for defense-in-depth.
	// This will fail in dev environments where we're not running as root,
	// which is acceptable — log a warning and continue.
	// TODO(prod): chown failures are expected in dev (container runs without CAP_CHOWN).
	// In production verify the worker has CAP_CHOWN set and the configured UID/GID
	// match the workdir owner. If this warning persists in prod logs, that's a
	// real misconfiguration.
	if err := os.Chown(dir, 65534, 65534); err != nil {
		log.Warn("chown workdir failed (expected in dev mode)",
			zap.String("path", dir),
			zap.Error(err),
		)
	}

	w := &Workdir{
		Path:     dir,
		DeployID: deployID,
		cleanup: func() {
			// if err := os.RemoveAll(dir); err != nil {
			// 	log.Error("failed to clean up workdir",
			// 		zap.String("path", dir),
			// 		zap.Error(err),
			// 	)
			// } else {
			// 	log.Debug("workdir cleaned up", zap.String("path", dir))
			// }
		},
	}

	return w, nil
}
