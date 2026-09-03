package saga

import (
	"context"
	"fmt"
)

const defaultDeployPort = 3000

type BuildRequest struct {
	DeployID     string
	UserID       string
	SourceType   string
	RepoURL      string
	Branch       string
	UploadID     string
	CredentialID string
}

type DeployRequest struct {
	DeployID   string
	UserID     string
	ImageRef   string
	Port       int
	TTLMinutes int
}

type DeployResponse struct {
	ContainerID string
	EndpointURL string
}

// BuildScheduler accepts asynchronous build work. Completion is reported
// separately through BuildEvents.
type BuildScheduler interface {
	EnqueueBuild(ctx context.Context, req BuildRequest) error
}

// Runtime owns the container lifecycle used by the saga.
type Runtime interface {
	Deploy(ctx context.Context, req DeployRequest) (*DeployResponse, error)
	Stop(ctx context.Context, deployID, containerID string) error
}

// OperationError communicates whether repeating a failed component operation
// can help. It deliberately carries no transport status: all components run
// in the same process and the saga needs retry semantics, not HTTP semantics.
type OperationError struct {
	Code      string
	Message   string
	Retryable bool
}

func (e *OperationError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	if e.Code != "" {
		return e.Code
	}
	return "component operation failed"
}

func (e *OperationError) Permanent() bool { return !e.Retryable }

func (e *OperationError) UserReason() string {
	if e.Message != "" {
		return e.Message
	}
	return "the runtime rejected the deploy"
}
