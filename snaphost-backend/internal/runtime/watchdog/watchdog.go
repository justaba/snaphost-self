// Package watchdog implements a background loop that periodically checks for
// expired deployments and stops them.
package watchdog

import (
	"context"
	"time"

	"go.uber.org/zap"

	"snaphost/internal/runtime/billing"
	"snaphost/internal/runtime/config"
	"snaphost/internal/runtime/runner"
)

// ExpiredLister is the one thing the watchdog needs from the control plane.
// An interface rather than a concrete client so the sweep can read the deploy
// repository directly in-process, without a round trip to an HTTP endpoint
// that exists only to serve this caller.
type ExpiredLister interface {
	ListExpiredDeploys(ctx context.Context, limit int) ([]billing.ExpiredDeploy, error)
}

// Watchdog periodically sweeps for expired deploys and stops them.
type Watchdog struct {
	runner  *runner.Service
	billing ExpiredLister
	cfg     *config.Config
	log     *zap.Logger
}

// NewWatchdog creates a new Watchdog instance.
func NewWatchdog(r *runner.Service, b ExpiredLister, cfg *config.Config, log *zap.Logger) *Watchdog {
	return &Watchdog{
		runner:  r,
		billing: b,
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

// sweep performs a single watchdog cycle: fetch expired deploys and stop them.
func (w *Watchdog) sweep(ctx context.Context) {
	expired, err := w.billing.ListExpiredDeploys(ctx, 50)
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
