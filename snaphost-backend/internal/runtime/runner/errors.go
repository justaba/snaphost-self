package runner

import "errors"

// ValidationError signals that a DeployRequest failed pre-flight checks
// (image_ref shape, tag/deploy_id consistency, durable-state cross-check).
// Callers should not retry because the request itself is invalid.
type ValidationError struct {
	Err error
}

func (e *ValidationError) Error() string {
	if e == nil || e.Err == nil {
		return "validation error"
	}
	return "validation error: " + e.Err.Error()
}

func (e *ValidationError) Unwrap() error { return e.Err }

// ErrAlreadyRunning is returned when a deploy is already running.
var ErrAlreadyRunning = errors.New("deploy already running")

// ProbeError is returned when the container started but nothing answered on
// the injected port. It is permanent for this image because retrying it would
// fail in the same way.
type ProbeError struct {
	Err error
}

func (e *ProbeError) Error() string {
	if e == nil || e.Err == nil {
		return probeFailureReason
	}
	return probeFailureReason + " (" + e.Err.Error() + ")"
}

func (e *ProbeError) Unwrap() error { return e.Err }

// ErrTransient marks state-store or container-engine failures where retrying
// may help.
var ErrTransient = errors.New("transient error")

// wrapTransient tags err as transient so callers / tests can detect it
// via errors.Is(err, ErrTransient).
func wrapTransient(err error) error {
	if err == nil {
		return nil
	}
	return transientError{err: err}
}

type transientError struct{ err error }

func (e transientError) Error() string { return "transient: " + e.err.Error() }
func (e transientError) Unwrap() error { return e.err }
func (e transientError) Is(target error) bool {
	return target == ErrTransient
}
