// Package queue provides a Redis Streams-backed build job queue with consumer
// group support for horizontal scaling of worker replicas.
package queue

import "time"

// StreamName is the Redis Stream key used for build jobs.
const StreamName = "snaphost:builds"

// ConsumerGroup is the default consumer group for builder workers.
const ConsumerGroup = "builder-workers"

// Source types carried in Job.SourceType. Mirrors the deploy source
// abstraction owned by user-billing (Task 14b).
const (
	SourceGitPublic  = "git_public"
	SourceGitPrivate = "git_private"
	SourceArchive    = "archive"
)

// Job represents a single build request enqueued by the API process and
// consumed by a worker process.
type Job struct {
	// ID is the Redis Stream entry ID assigned on enqueue.
	ID string `json:"id"`
	// DeployID is the unique identifier for the deployment.
	DeployID string `json:"deploy_id"`
	// UserID is the unique identifier for the requesting user.
	UserID string `json:"user_id"`
	// SourceType selects how the worker obtains the project source:
	// git_public/git_private clone RepoURL, archive unpacks the Redis
	// blob referenced by UploadID. Empty means git_public (jobs enqueued
	// before 14b-1 have no field).
	SourceType string `json:"source_type,omitempty"`
	// RepoURL is the Git repository URL to clone. Set for git sources only.
	RepoURL string `json:"repo_url,omitempty"`
	// Branch is the Git branch to build. Set for git sources only.
	Branch string `json:"branch,omitempty"`
	// UploadID references the uploaded archive blob (upload:<upload_id>)
	// for source_type=archive.
	UploadID string `json:"upload_id,omitempty"`
	// CredentialID references the short-lived git credential
	// (gitcred:<credential_id>) for source_type=git_private. Opaque id
	// only — the secret never enters the queue.
	CredentialID string `json:"credential_id,omitempty"`
	// BuildArgs is an optional set of Docker build arguments.
	BuildArgs map[string]string `json:"build_args,omitempty"`
	// QueuedAt is the time the job was enqueued.
	QueuedAt time.Time `json:"queued_at"`
}
