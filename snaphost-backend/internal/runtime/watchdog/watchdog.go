// Package watchdog implements a background loop that periodically checks for
// expired deployments and stops them.
package watchdog

import (
	"context"
	"time"

	"go.uber.org/zap"

	"snaphost/internal/runtime/config"
	"snaphost/internal/runtime/deployments"
)

// ExpiredLister is the one thing the watchdog needs from the control plane.
// An interface rather than a concrete client so the sweep can read the deploy
// repository directly in-process, without a round trip to an HTTP endpoint
// that exists only to serve this caller.
type DeploymentCatalog interface {
	ListExpiredDeploys(ctx context.Context, limit int) ([]deployments.Expired, error)
	ListImagesPendingCleanup(ctx context.Context, limit int) ([]deployments.ImageCleanup, error)
	ReclaimStoppedDeploys(ctx context.Context, limit int) (int, error)
}

type RuntimeCleaner interface {
	StopExpired(ctx context.Context, deployID, containerID string) error
	RemoveImage(ctx context.Context, deployID, imageRef string) error
}

// Watchdog periodically sweeps for expired deploys and stops them.
type Watchdog struct {
	runner  RuntimeCleaner
	catalog DeploymentCatalog
	cfg     *config.Config
	log     *zap.Logger
}

// NewWatchdog creates a new Watchdog instance.
func NewWatchdog(r RuntimeCleaner, catalog DeploymentCatalog, cfg *config.Config, log *zap.Logger) *Watchdog {
	return &Watchdog{
		runner:  r,
		catalog: catalog,
		cfg:     cfg,
		log:     log,
	}
}

// Run blocks until the context is cancelled, sweeping for expired deploys on
// every tick. Each cycle fetches up to 50 expired deploys and stops them.
func (w *Watchdog) Run(ctx context.Context) {
	interval := time.Duration(w.cfg.WatchdogIntervalSec) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	w.log.Info("watchdog started",
		zap.Duration("interval", interval),
	)

	for {
		select {
		case <-ctx.Done():
			w.log.Info("watchdog shutting down")
			return
		case <-ticker.C:
			w.sweep(ctx)
		}
	}
}

// sweep performs a single watchdog cycle.
//
// The two halves are independent on purpose. They read different queries and
// fix different problems — one stops containers past their TTL, the other
// releases the disk their images still hold — and letting the first failure
// skip the second is how a broken expiry query would silently take image
// reclamation with it. This repository has already had a reclaim statement
// that never once executed.
//
// Order matters within a tick, and it is the pipeline the three sweeps form:
// expiry stops a deploy, reclamation eventually moves a long-stopped one to
// deleted, and the image sweep releases the disk of whatever reached a dead
// status. Running them in this order means work started at the top of a tick
// finishes in the same one rather than waiting for the next.
func (w *Watchdog) sweep(ctx context.Context) {
	w.sweepExpired(ctx)
	w.sweepStopped(ctx)
	w.sweepImages(ctx)
}

// sweepStopped is the only automatic way out of 'stopped'.
//
// Stopping keeps the image so the deploy can be started again without a
// rebuild, and the image sweep skips 'stopped' for that reason. Those two
// facts together are a disk leak with no ceiling unless something eventually
// gives up on the favour — every TTL expiry would otherwise keep a container
// image forever. This is that something.
func (w *Watchdog) sweepStopped(ctx context.Context) {
	reclaimed, err := w.catalog.ReclaimStoppedDeploys(ctx, 50)
	if err != nil {
		w.log.Error("watchdog: failed to reclaim long-stopped deploys", zap.Error(err))
		return
	}
	if reclaimed > 0 {
		w.log.Info("watchdog reclaimed long-stopped deploys",
			zap.Int("deleted", reclaimed),
		)
	}
}

func (w *Watchdog) sweepExpired(ctx context.Context) {
	expired, err := w.catalog.ListExpiredDeploys(ctx, 50)
	if err != nil {
		w.log.Error("watchdog: failed to list expired deploys", zap.Error(err))
		return
	}

	stopped := 0
	for _, deploy := range expired {
		if err := w.runner.StopExpired(ctx, deploy.ID, deploy.ContainerID); err != nil {
			w.log.Error("watchdog: failed to stop expired deploy",
				zap.String("deploy_id", deploy.ID),
				zap.String("container_id", deploy.ContainerID),
				zap.Error(err),
			)
			continue
		}
		stopped++
	}

	w.log.Info("watchdog cycle complete",
		zap.Int("checked", len(expired)),
		zap.Int("stopped", stopped),
	)
}

// sweepImages releases the local Docker images of deploys that have reached a
// terminal status. With no registry, the disk that fills is the one the
// platform runs on, and stopping a container does not reclaim any of it.
//
// A failure on one image is skipped rather than fatal: the row keeps its
// image_deleted_at NULL, so the next tick asks for it again.
func (w *Watchdog) sweepImages(ctx context.Context) {
	images, err := w.catalog.ListImagesPendingCleanup(ctx, 50)
	if err != nil {
		w.log.Error("watchdog: failed to list images pending cleanup", zap.Error(err))
		return
	}
	removed := 0
	for _, image := range images {
		if err := w.runner.RemoveImage(ctx, image.ID, image.ImageRef); err != nil {
			w.log.Error("watchdog: failed to remove deploy image",
				zap.String("deploy_id", image.ID),
				zap.String("image_ref", image.ImageRef),
				zap.Error(err),
			)
			continue
		}
		removed++
	}
	w.log.Info("watchdog image cleanup complete",
		zap.Int("checked", len(images)),
		zap.Int("removed", removed),
	)
}
