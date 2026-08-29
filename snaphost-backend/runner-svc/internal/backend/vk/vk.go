// Package vk is a placeholder backend for VK Cloud.
//
// TODO: implement when VK Cloud support is needed. Until then, every Run call
// returns ErrNotImplemented and the saga marks the deployment as failed,
// which is the desired behavior for an explicitly-configured-but-unbuilt
// backend.
package vk

import (
	"context"
	"errors"

	"go.uber.org/zap"

	"snaphost/runner-svc/internal/backend"
)

// ErrNotImplemented is returned by every method on the VK backend stub.
var ErrNotImplemented = errors.New("vk backend not implemented yet")

// VKBackend is the stub implementation of backend.Backend for VK Cloud.
//
// TODO: implement when VK Cloud support is needed.
type VKBackend struct {
	log *zap.Logger
}

// NewVKBackend constructs the stub. It performs no I/O and never fails.
func NewVKBackend(log *zap.Logger) *VKBackend {
	return &VKBackend{log: log}
}

// Run always returns ErrNotImplemented.
func (b *VKBackend) Run(ctx context.Context, req backend.RunRequest) (*backend.RunResult, error) {
	return nil, ErrNotImplemented
}

// Stop always returns ErrNotImplemented.
func (b *VKBackend) Stop(ctx context.Context, deployID, containerID string) error {
	return ErrNotImplemented
}

// HealthCheck always returns ErrNotImplemented.
func (b *VKBackend) HealthCheck(ctx context.Context, containerID string) (*backend.HealthStatus, error) {
	return nil, ErrNotImplemented
}

// StreamLogs always returns ErrNotImplemented.
func (b *VKBackend) StreamLogs(ctx context.Context, containerID string) (<-chan string, error) {
	return nil, ErrNotImplemented
}

// Name returns "vk-stub" so logs make the placeholder status obvious.
func (b *VKBackend) Name() string { return "vk-stub" }

// Compile-time assertion that the stub satisfies backend.Backend.
var _ backend.Backend = (*VKBackend)(nil)
