// Package queue carries build jobs from the API to the worker.
package queue

import "time"

// Source types carried in Job.SourceType. Mirrors the deploy source
// abstraction owned by the deploy API (Task 14b).
const (
	SourceGitPublic  = "git_public"
	SourceGitPrivate = "git_private"
	SourceArchive    = "archive"
)

// Job represents a single build request enqueued by the API process and
// consumed by a worker process.
type Job struct {
	// ID identifies this job in logs and in the in-flight set. Assigned on
	// enqueue; it was a Redis Stream entry id and is a UUID now.
	ID string `json:"id"`
	// DeployID is the unique identifier for the deployment.
	DeployID string `json:"deploy_id"`
	// UserID is the unique identifier for the requesting user.
	UserID string `json:"user_id"`
	// SourceType selects how the worker obtains the project source:
	// git_public/git_private clone RepoURL, archive unpacks the stored
	// archive referenced by UploadID. Empty means git_public (jobs enqueued
	// before 14b-1 have no field).
	SourceType string `json:"source_type,omitempty"`
	// RepoURL is the Git repository URL to clone. Set for git sources only.
	RepoURL string `json:"repo_url,omitempty"`
	// Branch is the Git branch to build. Set for git sources only.
	Branch string `json:"branch,omitempty"`
	// UploadID references the uploaded archive
	// for source_type=archive.
	UploadID string `json:"upload_id,omitempty"`
	// CredentialID references the short-lived git credential
	// for source_type=git_private. Opaque id
	// only — the secret never enters the queue.
	CredentialID string `json:"credential_id,omitempty"`
	// BuildArgs is an optional set of Docker build arguments.
	BuildArgs map[string]string `json:"build_args,omitempty"`
	// QueuedAt is the time the job was enqueued.
	QueuedAt time.Time `json:"queued_at"`
}
