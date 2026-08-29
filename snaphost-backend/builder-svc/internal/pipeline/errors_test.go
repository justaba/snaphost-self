package pipeline

import (
	"errors"
	"testing"
)

func TestTransient_NilReturnsNil(t *testing.T) {
	if got := Transient(nil); got != nil {
		t.Fatalf("Transient(nil) = %v, want nil", got)
	}
}

func TestPermanent_NilReturnsNil(t *testing.T) {
	if got := Permanent(nil); got != nil {
		t.Fatalf("Permanent(nil) = %v, want nil", got)
	}
}

func TestTransient_WrapsRegularErrorAndIsDetected(t *testing.T) {
	base := errors.New("network unreachable")
	wrapped := Transient(base)
	if !IsTransient(wrapped) {
		t.Errorf("IsTransient should be true for Transient-wrapped err")
	}
	if IsPermanent(wrapped) {
		t.Errorf("IsPermanent should be false for transient err")
	}
	if !errors.Is(wrapped, base) {
		t.Errorf("errors.Is should still find the base error in the chain")
	}
}

func TestPermanent_WrapsRegularErrorAndIsDetected(t *testing.T) {
	base := errors.New("invalid dockerfile")
	wrapped := Permanent(base)
	if !IsPermanent(wrapped) {
		t.Errorf("IsPermanent should be true for Permanent-wrapped err")
	}
	if IsTransient(wrapped) {
		t.Errorf("IsTransient should be false for permanent err")
	}
	if !errors.Is(wrapped, base) {
		t.Errorf("errors.Is should still find the base error in the chain")
	}
}

func TestTransient_DoubleWrapReturnsSameError(t *testing.T) {
	base := errors.New("flaky")
	once := Transient(base)
	twice := Transient(once)
	// Same error value — short-circuit returned the existing wrapper.
	if once != twice {
		t.Errorf("double-wrap produced a new error: once=%p twice=%p", once, twice)
	}
	// Base error still reachable.
	if !errors.Is(twice, base) {
		t.Error("errors.Is should still find base in doubly-wrapped err")
	}
	if !IsTransient(twice) {
		t.Error("doubly-wrapped err should still be transient")
	}
}

func TestPermanent_DoubleWrapReturnsSameError(t *testing.T) {
	base := errors.New("bad")
	once := Permanent(base)
	twice := Permanent(once)
	if once != twice {
		t.Errorf("double-wrap produced a new error: once=%p twice=%p", once, twice)
	}
	if !errors.Is(twice, base) {
		t.Error("errors.Is should still find base in doubly-wrapped err")
	}
	if !IsPermanent(twice) {
		t.Error("doubly-wrapped err should still be permanent")
	}
}

func TestTransient_UnwrapReachesBase(t *testing.T) {
	// fmt.Errorf("%w: %w", ...) creates a multi-error whose Unwrap()
	// returns []error; errors.Is walks both branches.
	base := errors.New("real cause")
	wrapped := Transient(base)
	if !errors.Is(wrapped, base) {
		t.Error("base error not reachable via errors.Is from transient-wrapped")
	}
	if !errors.Is(wrapped, ErrTransient) {
		t.Error("ErrTransient tag should still be reachable")
	}
}

func TestTransientAndPermanent_Disjoint(t *testing.T) {
	// A bare error is neither.
	plain := errors.New("plain")
	if IsTransient(plain) {
		t.Error("plain err should not be transient")
	}
	if IsPermanent(plain) {
		t.Error("plain err should not be permanent")
	}
}
