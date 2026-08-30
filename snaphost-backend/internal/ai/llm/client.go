// Package llm provides the LLM client for AI Dockerfile generation.
//
// The client speaks the OpenAI Chat Completions protocol against a configurable
// base URL, so the provider is a matter of configuration rather than code. That
// stopped being academic on 2026-08-06, when OpenRouter began refusing requests
// from the production host's country outright — the base URL had been hardcoded,
// and swapping providers meant a release rather than an env change.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/sashabaranov/go-openai"
	"go.uber.org/zap"
)

// defaultBaseURL keeps existing deployments working when only the older
// OPENROUTER_* variables are set.
const defaultBaseURL = "https://openrouter.ai/api/v1"

// Sentinel errors used by the service layer for typed error handling.
var (
	// ErrTimeout is returned when the LLM request exceeds the configured timeout.
	ErrTimeout = errors.New("llm: request timed out")
	// ErrRateLimited is returned when the provider responds with HTTP 429.
	ErrRateLimited = errors.New("llm: rate limited")
	// ErrCircuitOpen is returned by CircuitClient when the breaker is open.
	ErrCircuitOpen = errors.New("llm: circuit breaker open")
	// ErrInvalidOutput is returned when the model response cannot be parsed or
	// fails structural validation (missing dockerfile / expose_port fields).
	ErrInvalidOutput = errors.New("llm: model returned invalid output")
	// ErrUpstream is returned for all other upstream errors (5xx, auth failures, etc.).
	ErrUpstream = errors.New("llm: upstream error")
)

// GenerateRequest is the input to the LLM.
type GenerateRequest struct {
	DeployID    string            `json:"deploy_id"`
	UserID      string            `json:"user_id"`
	ProjectInfo ProjectInfo       `json:"project_info"`
	FileTree    []string          `json:"file_tree"`
	KeyFiles    map[string]string `json:"key_files"`
	Constraints Constraints       `json:"constraints"`
}

// GenerateResponse is the validated structured output from the LLM.
type GenerateResponse struct {
	Dockerfile   string            `json:"dockerfile"`
	ExposePort   int               `json:"expose_port"`
	BuildArgs    map[string]string `json:"build_args"`
	SupportFiles map[string]string `json:"support_files"`
	Reasoning    string            `json:"reasoning"`

	// Metrics filled by the client after a successful call.
	InputTokens  int    `json:"-"`
	OutputTokens int    `json:"-"`
	DurationMs   int    `json:"-"`
	Model        string `json:"-"`
}

// ProjectInfo describes detected project characteristics passed through from the pipeline.
type ProjectInfo struct {
	Language  string `json:"language"`
	Framework string `json:"framework"`
	BuildTool string `json:"build_tool"`
}

// Constraints are the security rules every generated Dockerfile must satisfy.
type Constraints struct {
	AllowedBaseImagePrefixes []string `json:"allowed_base_image_prefixes"`
	RequireNonRootUser       bool     `json:"require_non_root_user"`
}

// Config holds construction parameters for the client.
type Config struct {
	// APIKey authenticates to the provider. Never logged.
	APIKey string
	// BaseURL is the OpenAI-compatible endpoint, without a trailing slash,
	// e.g. "https://openrouter.ai/api/v1" or "https://api.claudexia.tech/v1".
	// Empty falls back to OpenRouter for compatibility with older config.
	BaseURL string
	// Model is whatever identifier the provider uses. Aggregators tend to want
	// "vendor/model" ("openai/gpt-4o-mini"); single-vendor gateways tend to want
	// a bare name ("claude-opus-5"). Deliberately unvalidated — guessing the
	// shape here would reject providers that are perfectly fine.
	Model string
	// Referer is the HTTP-Referer header value. OpenRouter uses it for usage
	// attribution and free-tier eligibility; other providers ignore it.
	// Optional — omit to send no header.
	Referer string
	// AppName is the X-Title header value, same story as Referer. Optional.
	AppName string
	// Timeout is the per-request deadline applied via context.
	Timeout time.Duration
	// JSONMode asks the provider to constrain output to a JSON object.
	//
	// Not every provider supports it: the field is an OpenAI extension, and a
	// gateway that translates to a vendor without an equivalent may reject the
	// request outright. Turning it off is safe — the prompt already demands
	// JSON and the response is parsed defensively, fences and all — it only
	// removes a guarantee.
	JSONMode bool
}

// Client calls an OpenAI-compatible Chat Completions API.
// Use NewClient to construct; do not create directly.
type Client struct {
	api      *openai.Client
	model    string
	timeout  time.Duration
	jsonMode bool
	log      *zap.Logger
}

// NewClient builds a fully configured LLM client.
func NewClient(cfg Config, log *zap.Logger) *Client {
	oaiCfg := openai.DefaultConfig(cfg.APIKey)
	oaiCfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if oaiCfg.BaseURL == "" {
		oaiCfg.BaseURL = defaultBaseURL
	}

	// Attach custom HTTP client with timeout and optional analytics headers.
	if cfg.Referer != "" || cfg.AppName != "" {
		oaiCfg.HTTPClient = &http.Client{
			Timeout: cfg.Timeout,
			Transport: &headerTransport{
				base:    http.DefaultTransport,
				referer: cfg.Referer,
				appName: cfg.AppName,
			},
		}
	} else {
		oaiCfg.HTTPClient = &http.Client{Timeout: cfg.Timeout}
	}

	return &Client{
		api:      openai.NewClientWithConfig(oaiCfg),
		model:    cfg.Model,
		timeout:  cfg.Timeout,
		jsonMode: cfg.JSONMode,
		log:      log,
	}
}

// headerTransport injects the optional attribution headers on every request.
// HTTP-Referer and X-Title are what OpenRouter uses for usage attribution and
// to qualify certain models for free-tier rate limits.
type headerTransport struct {
	base    http.RoundTripper
	referer string
	appName string
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.referer != "" {
		req.Header.Set("HTTP-Referer", t.referer)
	}
	if t.appName != "" {
		req.Header.Set("X-Title", t.appName)
	}
	return t.base.RoundTrip(req)
}

// Generate calls OpenRouter and returns a validated, parsed response.
// The timeout in Config is applied via context; the caller's ctx
// deadline (if shorter) takes precedence — whichever fires first wins.
func (c *Client) Generate(ctx context.Context, req GenerateRequest) (*GenerateResponse, error) {
	start := time.Now()

	// Derive a child context with the configured timeout so that either the
	// caller or our own deadline can cancel the HTTP request.
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	messages := BuildMessages(req)

	chatReq := openai.ChatCompletionRequest{
		Model:    c.model,
		Messages: messages,
		// Low temperature → deterministic, reproducible Dockerfiles.
		Temperature: 0.2,
		MaxTokens:   2000,
	}
	if c.jsonMode {
		chatReq.ResponseFormat = &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONObject,
		}
	}

	resp, err := c.api.CreateChatCompletion(ctx, chatReq)
	if err != nil {
		return nil, c.classifyError(err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("%w: no choices returned by model", ErrUpstream)
	}

	content := strings.TrimSpace(resp.Choices[0].Message.Content)
	if content == "" {
		return nil, fmt.Errorf("%w: model returned empty content", ErrInvalidOutput)
	}

	// Some models wrap JSON in markdown fences despite the ResponseFormat hint — strip defensively.
	content = stripMarkdownFences(content)

	var out GenerateResponse
	if err := json.Unmarshal([]byte(content), &out); err != nil {
		// Include a truncated snippet of the bad output to aid debugging without
		// bloating structured logs when the model produces very large broken blobs.
		snippet := content
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return nil, fmt.Errorf("%w: %v (got: %.200q)", ErrInvalidOutput, err, snippet)
	}

	if out.Dockerfile == "" {
		return nil, fmt.Errorf("%w: missing dockerfile field in model response", ErrInvalidOutput)
	}
	if out.ExposePort == 0 {
		return nil, fmt.Errorf("%w: missing or zero expose_port in model response", ErrInvalidOutput)
	}
	if out.BuildArgs == nil {
		out.BuildArgs = map[string]string{}
	}
	if out.SupportFiles == nil {
		out.SupportFiles = map[string]string{}
	}

	out.InputTokens = resp.Usage.PromptTokens
	out.OutputTokens = resp.Usage.CompletionTokens
	out.DurationMs = int(time.Since(start).Milliseconds())
	out.Model = c.model

	return &out, nil
}

// Name returns a human-readable identifier used in /health and usage logs.
func (c *Client) Name() string { return "openrouter:" + c.model }

// classifyError maps raw HTTP / context errors to our typed sentinel errors so
// that the service layer can apply appropriate retry or circuit-breaker policy.
func (c *Client) classifyError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return fmt.Errorf("%w: %v", ErrTimeout, err)
	}

	// The client library reports a failure two ways. It parses the provider's
	// JSON error body into an APIError when it can, and falls back to a bare
	// RequestError when it cannot — which is what a provider returns for a
	// rejected key, since there is nothing to say about the request.
	//
	// Only APIError used to be classified. A 403 therefore skipped the auth
	// branch below and surfaced as "upstream error: error, status code: 403,
	// message:" — the provider's empty body, verbatim, telling the operator
	// nothing about the one thing that was wrong.
	var status int
	var detail string

	var apiErr *openai.APIError
	var reqErr *openai.RequestError
	switch {
	case errors.As(err, &apiErr):
		status, detail = apiErr.HTTPStatusCode, apiErr.Message
	case errors.As(err, &reqErr):
		status = reqErr.HTTPStatusCode
	}

	switch {
	case status == http.StatusTooManyRequests:
		return fmt.Errorf("%w: %s", ErrRateLimited, detail)
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		// Named for what the operator has to do. This platform is self-hosted:
		// nobody else can look at the account, and the provider's own body is
		// usually empty here.
		return fmt.Errorf("%w: the model provider rejected the API key (%d) — check OPENROUTER_API_KEY", ErrUpstream, status)
	case status >= 500:
		return fmt.Errorf("%w: server error %d: %s", ErrUpstream, status, detail)
	}

	return fmt.Errorf("%w: %v", ErrUpstream, err)
}

// stripMarkdownFences removes leading ```json / ``` wrappers that some models
// insert even when ResponseFormat: json_object is requested.
func stripMarkdownFences(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	// Discard the opening fence line (```json, ```JSON, ``` etc.)
	nl := strings.IndexByte(s, '\n')
	if nl == -1 {
		return s
	}
	s = s[nl+1:]
	// Discard trailing closing fence if present.
	s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	return strings.TrimSpace(s)
}
