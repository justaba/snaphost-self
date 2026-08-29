package pipeline

import (
	"errors"
	"fmt"
)

// ErrTransient marks errors that originate from external systems where the
// failure is plausibly temporary (network blip, upstream service restart,
// DB timeout). Returning a transient error from the worker is the signal
// for retry — wired in Task 5b. In Task 5a the worker still finalizes
// every error as failed; the classification is preserved through the
// chain so 5b can act on it.
//
// Use Transient(err) to wrap external-call errors. Do not return
// ErrTransient directly without context.
var ErrTransient = errors.New("transient")

// ErrPermanent marks errors caused by the input itself (bad URL, invalid
// Dockerfile, CVE found, hallucinated AI output, missing repo, auth
// failure) where retry would just produce the same failure.
//
// Use Permanent(err) to wrap.
var ErrPermanent = errors.New("permanent")

// Transient wraps err so callers can detect it via errors.Is(err, ErrTransient).
// nil → nil. Already-transient err returned unchanged (no double-wrap).
func Transient(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrTransient) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrTransient, err)
}

// Permanent wraps err so callers can detect it via errors.Is(err, ErrPermanent).
// nil → nil. Already-permanent err returned unchanged.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrPermanent) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrPermanent, err)
}

// IsTransient reports whether err (or anything in its chain) carries the
// transient tag.
func IsTransient(err error) bool { return errors.Is(err, ErrTransient) }

// IsPermanent reports whether err (or anything in its chain) carries the
// permanent tag.
func IsPermanent(err error) bool { return errors.Is(err, ErrPermanent) }
