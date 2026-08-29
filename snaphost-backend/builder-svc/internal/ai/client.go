package ai

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

// Typed sentinel errors returned by GenerateDockerfile. Callers use
// errors.Is to classify failures as transient vs permanent — see
// pipeline/errors.go.
//
//	ErrAIRefused      — model semantic refusal (HTTP 422) or other 4xx
//	                    indicating our request is wrong. Permanent.
//	                    Also returned for marshal / decode failures because
//	                    those signal a contract bug, not a flaky upstream.
//	ErrAIUnavailable  — upstream issues: HTTP 5xx, 429 rate-limit, or any
//	                    network-level error reaching the AI orchestrator.
//	                    Transient.
var (
	ErrAIRefused     = errors.New("ai refused to generate")
	ErrAIUnavailable = errors.New("ai service unavailable")
)

type Client interface {
	GenerateDockerfile(ctx context.Context, req GenerateRequest) (*GenerateResponse, error)
}

type ProjectInfo struct {
	Language   string `json:"language"`
	Framework  string `json:"framework"`
	BuildTool  string `json:"build_tool"`
	EntryPoint string `json:"entry_point"`
}

type GenerateRequest struct {
	DeployID    string            `json:"deploy_id"`
	UserID      string            `json:"user_id"`
	ProjectInfo interface{}       `json:"project_info"`
	FileTree    []string          `json:"file_tree"`
	KeyFiles    map[string]string `json:"key_files"`
}

type Metadata struct {
	Source     string `json:"source"`
	TemplateID string `json:"template_id"`
	DurationMs int    `json:"duration_ms"`
	TokensUsed int    `json:"tokens_used"`
}

type GenerateResponse struct {
	Dockerfile   string            `json:"dockerfile"`
	ExposePort   int               `json:"expose_port"`
	BuildArgs    map[string]string `json:"build_args"`
	SupportFiles map[string]string `json:"support_files"`
	CacheHit     bool              `json:"cache_hit"`
	Metadata     Metadata          `json:"metadata"`
}

type HTTPClient struct {
	baseURL       string
	webhookSecret string
	client        *http.Client
	log           *zap.Logger
}

func NewHTTPClient(baseURL, webhookSecret string, log *zap.Logger) *HTTPClient {
	return &HTTPClient{
		baseURL:       baseURL,
		webhookSecret: webhookSecret,
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
		log: log,
	}
}

func (c *HTTPClient) GenerateDockerfile(ctx context.Context, req GenerateRequest) (*GenerateResponse, error) {
	b, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("%w: marshal req: %v", ErrAIRefused, err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/internal/ai/generate-dockerfile", bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("%w: new http request: %v", ErrAIRefused, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Webhook-Secret", c.webhookSecret)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAIUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		var genResp GenerateResponse
		if err := json.NewDecoder(resp.Body).Decode(&genResp); err != nil {
			return nil, fmt.Errorf("%w: decode json body: %v", ErrAIRefused, err)
		}
		return &genResp, nil
	}

	body, _ := io.ReadAll(resp.Body)
	switch {
	case resp.StatusCode == 422:
		return nil, fmt.Errorf("%w: %s", ErrAIRefused, string(body))
	case resp.StatusCode == 429:
		return nil, fmt.Errorf("%w: rate limited: %s", ErrAIUnavailable, string(body))
	case resp.StatusCode >= 500:
		return nil, fmt.Errorf("%w: status %d: %s", ErrAIUnavailable, resp.StatusCode, string(body))
	default:
		// Other 4xx (400, 401, 403, 404): our request is wrong, retry won't help.
		return nil, fmt.Errorf("%w: status %d: %s", ErrAIRefused, resp.StatusCode, string(body))
	}
}
