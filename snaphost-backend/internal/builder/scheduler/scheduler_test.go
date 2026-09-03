package scheduler

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"

	"snaphost/internal/builder/config"
	"snaphost/internal/builder/queue"
)

const (
	testDeployID = "b132cb0d-ce92-4009-a8f3-221d86d8607c"
	testUserID   = "a4a355f8-9769-454f-b5c0-9782acceebc0"
)

func newScheduler(limit int) (*Scheduler, *queue.Queue) {
	q := queue.NewQueue(8, zap.NewNop())
	return &Scheduler{
		Queue: q,
		Cfg:   &config.Config{MaxConcurrentBuildsPerUser: limit},
		Log:   zap.NewNop(),
	}, q
}

func validGitRequest() BuildRequest {
	return BuildRequest{
		DeployID: testDeployID,
		UserID:   testUserID,
		RepoURL:  "https://github.com/acme/example",
		Branch:   "main",
	}
}

func TestEnqueueAcceptsAValidGitBuild(t *testing.T) {
	s, q := newScheduler(1)
	jobID, err := s.Enqueue(context.Background(), validGitRequest())
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	if jobID == "" || q.Depth() != 1 {
		t.Fatalf("job id = %q, queue depth = %d", jobID, q.Depth())
	}
}

func TestEnqueueReturnsDomainErrors(t *testing.T) {
	tests := []struct {
		name string
		edit func(*BuildRequest)
		code string
	}{
		{"bad deploy id", func(r *BuildRequest) { r.DeployID = "bad" }, "invalid_deploy_id"},
		{"missing repo", func(r *BuildRequest) { r.RepoURL = "" }, "invalid_body"},
		{"unknown source", func(r *BuildRequest) { r.SourceType = "svn" }, "invalid_source_type"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newScheduler(1)
			req := validGitRequest()
			tc.edit(&req)
			_, err := s.Enqueue(context.Background(), req)
			var requestErr *RequestError
			if !errors.As(err, &requestErr) || requestErr.Code != tc.code || requestErr.Retryable {
				t.Fatalf("error = %#v, want permanent %q RequestError", err, tc.code)
			}
		})
	}
}

func TestConcurrencyLimitIsRetryable(t *testing.T) {
	s, q := newScheduler(1)
	if err := q.MarkActive(context.Background(), testUserID, "active-job"); err != nil {
		t.Fatal(err)
	}
	_, err := s.Enqueue(context.Background(), validGitRequest())
	var requestErr *RequestError
	if !errors.As(err, &requestErr) || requestErr.Code != "concurrency_limit" || !requestErr.Retryable {
		t.Fatalf("error = %#v, want retryable concurrency_limit", err)
	}
}
