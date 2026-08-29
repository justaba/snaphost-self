package runner

import "errors"

// ValidationError signals that a DeployRequest failed pre-flight checks
// (image_ref shape, tag/deploy_id consistency, user-billing cross-check).
// The HTTP layer maps it to 400. Caller should not retry — the request
// itself is invalid.
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

// ErrAlreadyRunning is returned when a deploy is already in the running
// state. The HTTP layer maps it to 409 so the saga can treat the result
// as "step already complete" rather than "request invalid".
var ErrAlreadyRunning = errors.New("deploy already running")

// ProbeError is returned when the container started but nothing answered on
// the injected port. The HTTP layer maps it to 422 so the saga treats it as a
// permanent failure of this deploy — retrying the same image would fail the
// same way, and the reservation has to be refunded instead.
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

// ErrTransient marks errors originating from external systems
// (billing 5xx, network) where retry is appropriate. Local sentinel for
// runner-svc until Task 5 introduces a shared pipeline-level type.
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
