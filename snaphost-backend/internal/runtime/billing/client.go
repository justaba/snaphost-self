// Package billing provides an HTTP client for calling the user-billing service's
// internal API. runner-svc does not access user-billing's database directly —
// all deploy status updates go through these HTTP endpoints.
package billing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// ErrDeployNotFound is returned by GetDeploy when user-billing responds with
// 404. Callers use errors.Is(err, ErrDeployNotFound) to distinguish a
// permanent validation failure (deploy gone) from transient failures
// (billing down — retry).
var ErrDeployNotFound = errors.New("deploy not found")

// Client is an HTTP client for the user-billing internal API.
type Client struct {
	baseURL    string
	secret     string
	httpClient *http.Client
	log        *zap.Logger
}

// NewClient creates a new billing HTTP client.
func NewClient(baseURL, webhookSecret string, log *zap.Logger) *Client {
	return &Client{
		baseURL: baseURL,
		secret:  webhookSecret,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		log: log,
	}
}

// SetRunningRequest is the request body for SetDeployRunning.
type SetRunningRequest struct {
	// ImageRef is the Docker image reference that was deployed.
	ImageRef string `json:"image_ref"`
	// EndpointURL is the public URL where the deploy is accessible.
	EndpointURL string `json:"endpoint_url"`
	// Subdomain is the hostname prefix assigned to this deploy.
	Subdomain string `json:"subdomain"`
	// ContainerID is the backend-specific handle for the running container.
	ContainerID string `json:"container_id"`
	// TTLExpiresAt is when the deploy should be auto-stopped.
	TTLExpiresAt time.Time `json:"ttl_expires_at"`
}

// DeployInfo mirrors user-billing's GET /internal/deploys/:id response.
// Fields are always populated by the server (no omitempty on the wire);
// empty ImageRef or ContainerID values mean the deploy has not reached that
// lifecycle stage yet, not that the field is missing.
type DeployInfo struct {
	DeployID    string `json:"deploy_id"`
	UserID      string `json:"user_id"`
	Status      string `json:"status"`
	ImageRef    string `json:"image_ref"`
	ContainerID string `json:"container_id"`
}

// ExpiredDeploy is a deploy that has exceeded its TTL and needs to be stopped.
type ExpiredDeploy struct {
	// ID is the deploy's UUID.
	ID string `json:"id"`
	// UserID is the owning user's UUID.
	UserID string `json:"user_id"`
	// ContainerID is the backend handle for the running container.
	ContainerID string `json:"container_id"`
}

// ImageCleanup is a terminal deploy artifact waiting to be removed locally.
type ImageCleanup struct {
	ID       string `json:"id"`
	ImageRef string `json:"image_ref"`
}

// UpdateDeployStatus updates the status and optional failure reason for a deploy.
func (c *Client) UpdateDeployStatus(ctx context.Context, deployID string, status string, failureReason *string) error {
	body := map[string]interface{}{
		"status": status,
	}
	if failureReason != nil {
		body["failure_reason"] = *failureReason
	}

	url := fmt.Sprintf("%s/internal/deploys/%s/status", c.baseURL, deployID)
	if err := c.doPost(ctx, url, body); err != nil {
		return fmt.Errorf("update deploy status: %w", err)
	}
	return nil
}

// SetDeployRunning transitions a deploy to running status with all runtime details.
func (c *Client) SetDeployRunning(ctx context.Context, deployID string, req SetRunningRequest) error {
	url := fmt.Sprintf("%s/internal/deploys/%s/running", c.baseURL, deployID)
	if err := c.doPost(ctx, url, req); err != nil {
		return fmt.Errorf("set deploy running: %w", err)
	}
	return nil
}

// MarkDeployImageDeleted stores the durable completion marker for image GC.
func (c *Client) MarkDeployImageDeleted(ctx context.Context, deployID string) error {
	url := fmt.Sprintf("%s/internal/deploys/%s/image-deleted", c.baseURL, deployID)
	if err := c.doPost(ctx, url, struct{}{}); err != nil {
		return fmt.Errorf("mark deploy image deleted: %w", err)
	}
	return nil
}

// GetDeploy fetches the minimal DeployInfo for a deploy. Returns
// ErrDeployNotFound on 404; other non-200 statuses are returned as
// transient-looking errors so callers can decide whether to retry.
func (c *Client) GetDeploy(ctx context.Context, deployID string) (*DeployInfo, error) {
	url := fmt.Sprintf("%s/internal/deploys/%s", c.baseURL, deployID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("X-Webhook-Secret", c.secret)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get deploy: %w", err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrDeployNotFound
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, fmt.Errorf("billing auth failed: check webhook secret")
	case resp.StatusCode >= 500:
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("billing returned %d: %s", resp.StatusCode, string(body))
	case resp.StatusCode != http.StatusOK:
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("billing returned unexpected %d: %s", resp.StatusCode, string(body))
	}

	var info DeployInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("decode billing response: %w", err)
	}
	return &info, nil
}

// ListExpiredDeploys returns deploys whose TTL has expired and need to be stopped.
func (c *Client) ListExpiredDeploys(ctx context.Context, limit int) ([]ExpiredDeploy, error) {
	url := fmt.Sprintf("%s/internal/deploys/expired?limit=%d", c.baseURL, limit)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("X-Webhook-Secret", c.secret)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list expired deploys: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list expired deploys: status %d, body: %s", resp.StatusCode, string(body))
	}

	var deploys []ExpiredDeploy
	if err := json.NewDecoder(resp.Body).Decode(&deploys); err != nil {
		return nil, fmt.Errorf("decode expired deploys: %w", err)
	}

	return deploys, nil
}

// ListImagesPendingCleanup returns terminal images without a cleanup marker.
func (c *Client) ListImagesPendingCleanup(ctx context.Context, limit int) ([]ImageCleanup, error) {
	url := fmt.Sprintf("%s/internal/deploy-images/pending?limit=%d", c.baseURL, limit)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("X-Webhook-Secret", c.secret)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list images pending cleanup: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list images pending cleanup: status %d, body: %s", resp.StatusCode, string(body))
	}

	var images []ImageCleanup
	if err := json.NewDecoder(resp.Body).Decode(&images); err != nil {
		return nil, fmt.Errorf("decode images pending cleanup: %w", err)
	}
	return images, nil
}

// doPost performs a POST request with JSON body and the webhook secret header.
func (c *Client) doPost(ctx context.Context, url string, body interface{}) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Secret", c.secret)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("billing API error: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	return nil
}
