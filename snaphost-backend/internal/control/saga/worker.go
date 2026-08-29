package saga

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// Worker drives two concurrent loops:
//   - a queue consumer that pulls jobs off the saga stream and runs the
//     orchestrator,
//   - a sweeper that periodically re-enqueues jobs for sagas that have
//     been stuck mid-flight (resume after crash).
type Worker struct {
	Orchestrator *Orchestrator
	Queue        *Queue
	Repo         *Repository
	Log          *zap.Logger
	// ResumeInterval controls how often the sweeper looks for stuck sagas.
	ResumeInterval time.Duration
}

// Run blocks until ctx is cancelled. Any error from the consumer loop is
// returned; the sweeper is best-effort.
func (w *Worker) Run(ctx context.Context) error {
	w.Log.Info("starting saga worker")

	// A saga left mid-build by a restart is put back to the step before the
	// build, so it is enqueued again rather than waiting out its whole build
	// timeout to discover nobody is building it.
	//
	// This is what the Redis Stream's un-acked message used to do, and it is
	// closer to correct than that was: redelivering the build request would
	// have re-run a pipeline whose uploaded archive and git credential are
	// deleted the moment they are consumed, so for two of the three source
	// types the redelivery could only fail.
	if n, err := w.Repo.RewindInterruptedBuilds(ctx); err != nil {
		w.Log.Warn("could not rewind interrupted builds", zap.Error(err))
	} else if n > 0 {
		w.Log.Info("rewound sagas interrupted mid-build", zap.Int64("sagas", n))
	}

	go w.runResumeSweeper(ctx)

	return w.Queue.Consume(ctx, func(job SagaJob) error {
		w.Log.Info("processing saga job",
			zap.String("deploy_id", job.DeployID),
			zap.String("user_id", job.UserID),
		)
		return w.Orchestrator.Run(ctx, job)
	})
}

// runResumeSweeper periodically lists sagas stuck in non-terminal states
// for >5 minutes and re-enqueues them so the orchestrator can pick up
// where it left off after a crash. Errors are logged and ignored.
func (w *Worker) runResumeSweeper(ctx context.Context) {
	interval := w.ResumeInterval
	if interval <= 0 {
		interval = 60 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.sweepOnce(ctx)
		}
	}
}

func (w *Worker) sweepOnce(ctx context.Context) {
	stuck, err := w.Repo.ListInFlight(ctx, 50)
	if err != nil {
		w.Log.Warn("resume sweeper: list in-flight failed", zap.Error(err))
		return
	}
	if len(stuck) == 0 {
		return
	}
	for _, s := range stuck {
		job := SagaJob{
			DeployID:   s.DeployID,
			UserID:     s.UserID,
			SourceType: s.SourceType,
			EnqueuedAt: time.Now().UTC(),
		}
		if s.UploadID != nil {
			job.UploadID = *s.UploadID
		}
		if s.CredentialID != nil {
			job.CredentialID = *s.CredentialID
		}
		if err := w.Queue.Enqueue(ctx, job); err != nil {
			w.Log.Warn("resume sweeper: enqueue failed",
				zap.String("deploy_id", s.DeployID), zap.Error(err))
			continue
		}
		w.Log.Info("resume sweeper: re-enqueued stuck saga",
			zap.String("deploy_id", s.DeployID),
			zap.String("step", string(s.CurrentStep)),
		)
	}
}
