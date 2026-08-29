package saga

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// defaultDeployPort is sent to runner-svc when the caller does not set
// DeployRequest.Port. Most templates today expose 3000.
const defaultDeployPort = 3000

// BuildRequest is the body sent to builder-svc to enqueue a build.
// Mirrors the JSON shape expected by builder-svc/api Handler.Build at
// POST /api/v1/build.
type BuildRequest struct {
	DeployID       string `json:"deploy_id"`
	UserID         string `json:"user_id"`
	SourceType     string `json:"source_type,omitempty"`
	RepoURL        string `json:"repo_url,omitempty"`
	Branch         string `json:"branch,omitempty"`
	UploadID       string `json:"upload_id,omitempty"`
	CredentialID   string `json:"credential_id,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// DeployRequest is the body sent to runner-svc to start a container.
// Port is required by runner-svc; default to 3000 unless set by the caller.
type DeployRequest struct {
	DeployID       string `json:"deploy_id"`
	UserID         string `json:"user_id"`
	ImageRef       string `json:"image_ref"`
	Port           int    `json:"port"`
	TTLMinutes     int    `json:"ttl_minutes,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// DeployResponse is what runner-svc returns after a successful deploy.
// runner-svc itself calls user-billing's /internal/deploys/:id/running to
// fill in subdomain/ttl/etc, so we only need the basics here.
type DeployResponse struct {
	ContainerID string `json:"container_id"`
	EndpointURL string `json:"endpoint_url"`
}

// BuilderClient triggers builds on builder-svc. EnqueueBuild is fire-and-
// forget: builder-svc accepts the job, runs it asynchronously, and signals
// completion via Redis pub/sub on build-events:{deploy_id}.
type BuilderClient interface {
	EnqueueBuild(ctx context.Context, req BuildRequest) error
}

// RunnerClient deploys and stops containers on runner-svc. Both methods
// must be idempotent on the runner-svc side via IdempotencyKey or via
// runner-svc looking up by deploy_id.
type RunnerClient interface {
	Deploy(ctx context.Context, req DeployRequest) (*DeployResponse, error)
	Stop(ctx context.Context, deployID, containerID string) error
}

// HTTPBuilderClient is a BuilderClient over HTTP+JSON.
type HTTPBuilderClient struct {
	BaseURL       string
	WebhookSecret string
	Client        *http.Client
}

// NewHTTPBuilderClient constructs a client with a 30-second default timeout.
// The actual build runs much longer; only the trigger call uses this timeout.
func NewHTTPBuilderClient(baseURL, secret string) *HTTPBuilderClient {
	return &HTTPBuilderClient{
		BaseURL:       baseURL,
		WebhookSecret: secret,
		Client:        &http.Client{Timeout: 30 * time.Second},
	}
}

// EnqueueBuild posts the build request to POST /api/v1/build.
func (c *HTTPBuilderClient) EnqueueBuild(ctx context.Context, req BuildRequest) error {
	return doPost(ctx, c.Client, c.BaseURL+"/api/v1/build", c.WebhookSecret, req, nil)
}

// HTTPRunnerClient is a RunnerClient over HTTP+JSON.
type HTTPRunnerClient struct {
	BaseURL       string
	WebhookSecret string
	Client        *http.Client
}

// NewHTTPRunnerClient constructs a client. Deploy is a *synchronous* call
// inside runner-svc (it pulls the image and starts the container before
// returning) so the timeout has to cover image-pull cold-start. 5 minutes
// is enough for typical node/python images on first-time pull.
func NewHTTPRunnerClient(baseURL, secret string) *HTTPRunnerClient {
	return &HTTPRunnerClient{
		BaseURL:       baseURL,
		WebhookSecret: secret,
		Client:        &http.Client{Timeout: 5 * time.Minute},
	}
}

// Deploy calls /internal/deploys to start a container for the built image.
// The IdempotencyKey is sent both in the body and as a header so that
// runner-svc can dedupe at either layer.
func (c *HTTPRunnerClient) Deploy(ctx context.Context, req DeployRequest) (*DeployResponse, error) {
	var resp DeployResponse
	if err := doPostWithIdemHeader(ctx, c.Client, c.BaseURL+"/internal/deploys", c.WebhookSecret, req.IdempotencyKey, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// Stop calls DELETE /internal/deploys/{id} with the container_id in the
// JSON body, as required by runner-svc.
func (c *HTTPRunnerClient) Stop(ctx context.Context, deployID, containerID string) error {
	body, err := json.Marshal(map[string]string{"container_id": containerID})
	if err != nil {
		return fmt.Errorf("marshal stop body: %w", err)
	}
	url := fmt.Sprintf("%s/internal/deploys/%s", c.BaseURL, deployID)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create stop request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Webhook-Secret", c.WebhookSecret)
	resp, err := c.Client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("send stop request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("stop returned %d", resp.StatusCode)
	}
	return nil
}

// statusErrorFrom builds a StatusError from a failed response, reading the
// standard {error, message} body when present. The body is bounded: it is a
// service-to-service error, not a payload.
func statusErrorFrom(resp *http.Response) error {
	statusErr := &StatusError{StatusCode: resp.StatusCode}
	var body struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<10)).Decode(&body); err == nil {
		statusErr.Code = body.Error
		statusErr.Message = body.Message
	}
	return statusErr
}

// StatusError carries a downstream service's HTTP status so the orchestrator
// can tell "this will never work" from "try again". Without it every runner
// refusal looked transient and was requeued, which for a deploy that cannot
// serve means retrying a build that is already correct.
type StatusError struct {
	StatusCode int
	// Code and Message come from the standard {error, message} body when the
	// downstream service sends one.
	Code    string
	Message string
}

func (e *StatusError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("server returned %d (%s): %s", e.StatusCode, e.Code, e.Message)
	}
	return fmt.Sprintf("server returned %d", e.StatusCode)
}

// Permanent reports whether retrying the same request is pointless. 429 is
// excluded: rate limiting is exactly the case where waiting helps.
func (e *StatusError) Permanent() bool {
	return e.StatusCode >= 400 && e.StatusCode < 500 && e.StatusCode != http.StatusTooManyRequests
}

// UserReason is the text worth showing a deploy owner, falling back to
// something honest when the downstream service sent no message.
func (e *StatusError) UserReason() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("runtime rejected the deploy (HTTP %d)", e.StatusCode)
}

// doPost is a tiny helper for JSON POST requests with X-Webhook-Secret.
func doPost(ctx context.Context, client *http.Client, url, secret string, body, out any) error {
	return doPostWithIdemHeader(ctx, client, url, secret, "", body, out)
}

func doPostWithIdemHeader(ctx context.Context, client *http.Client, url, secret, idemKey string, body, out any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Secret", secret)
	if idemKey != "" {
		req.Header.Set("Idempotency-Key", idemKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return statusErrorFrom(resp)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
