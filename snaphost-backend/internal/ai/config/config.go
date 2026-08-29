// Package config loads and validates ai-orchestrator configuration from
// environment variables.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime configuration for the ai-orchestrator service.
type Config struct {
	// Port is the HTTP listen port (env PORT, default 8083).
	Port string
	// RunMigrations is kept only so the generator half can be told not to
	// migrate; the schema itself lives with the control plane now.
	// LogLevel controls zap log verbosity (env LOG_LEVEL, default "info").
	LogLevel string
	// WebhookSecret is the shared secret for internal service-to-service auth
	// (env WEBHOOK_SECRET, required).
	WebhookSecret string

	// AllowedBaseImagePrefixes is the list of permitted Docker base image
	// prefixes used to constrain LLM generation. Sourced from
	// ALLOWED_BASE_IMAGES env var (comma-separated). Must be identical to
	// builder-svc's list — validator (downstream) and LLM constraint
	// (upstream) use the same env value.
	AllowedBaseImagePrefixes []string

	// LLMTimeout is the per-request deadline for OpenRouter calls
	// (env LLM_TIMEOUT, default 30s). Accepts Go duration strings, e.g. "45s".
	LLMTimeout time.Duration
	// LLMMaxRetries is the number of additional attempts on transient failures
	// (env LLM_MAX_RETRIES, default 1).
	LLMMaxRetries int

	// LLMBaseURL is the OpenAI-compatible endpoint the client talks to
	// (env LLM_BASE_URL, default OpenRouter). The provider is configuration,
	// not code: OpenRouter, OpenAI, and Anthropic all refuse requests from
	// Russia outright, so being able to move without a release matters.
	LLMBaseURL string
	// LLMJSONMode asks the provider to constrain output to a JSON object
	// (env LLM_JSON_MODE, default true). `response_format` is an OpenAI
	// extension; a gateway fronting a vendor without an equivalent may reject
	// the request, and turning this off costs only a guarantee — the prompt
	// already demands JSON and the response is parsed defensively.
	LLMJSONMode bool

	// OpenRouterAPIKey is the provider API key (env OPENROUTER_API_KEY,
	// required). Named for its original provider; it authenticates to whatever
	// LLMBaseURL points at. Never logged.
	OpenRouterAPIKey string
	// OpenRouterModel is the model identifier in "vendor/model" format
	// (env OPENROUTER_MODEL, default "openai/gpt-4o-mini").
	// Examples: "anthropic/claude-3.5-sonnet", "meta-llama/llama-3.3-70b-instruct"
	OpenRouterModel string
	// OpenRouterReferer is the HTTP-Referer header sent to OpenRouter for
	// analytics and free-tier eligibility (env OPENROUTER_REFERER,
	// default "https://snaphost.app"). Leave empty to suppress the header.
	OpenRouterReferer string
	// OpenRouterAppName is the X-Title header sent to OpenRouter
	// (env OPENROUTER_APP_NAME, default "SnapHost").
	OpenRouterAppName string

	// CacheTTLDays is how long Dockerfile cache entries are retained before
	// eviction (env CACHE_TTL_DAYS, default 7).
	CacheTTLDays int
	// MaxFileSizeKB is the maximum size of a single key-file payload accepted
	// in a generate request (env MAX_FILE_SIZE_KB, default 50).
	MaxFileSizeKB int
	// MaxFilesPerRequest caps the number of key files accepted per request
	// (env MAX_FILES_PER_REQUEST, default 20).
	MaxFilesPerRequest int
	// RunMigrations controls whether DB migrations are applied at startup
	// (env RUN_MIGRATIONS, default true).
	RunMigrations bool
}

// Load reads environment variables, applies defaults, and validates required fields.
// Returns an error if any required field is missing.
func Load() (*Config, error) {
	c := &Config{
		Port:               getEnv("PORT", "8083"),
		LogLevel:           getEnv("LOG_LEVEL", "info"),
		WebhookSecret:      os.Getenv("WEBHOOK_SECRET"),
		LLMTimeout:         getEnvDuration("LLM_TIMEOUT", 30*time.Second),
		LLMMaxRetries:      getEnvInt("LLM_MAX_RETRIES", 1),
		LLMBaseURL:         getEnv("LLM_BASE_URL", "https://openrouter.ai/api/v1"),
		LLMJSONMode:        getEnvBool("LLM_JSON_MODE", true),
		OpenRouterAPIKey:   os.Getenv("OPENROUTER_API_KEY"),
		OpenRouterModel:    getEnv("OPENROUTER_MODEL", "openai/gpt-4o-mini"),
		OpenRouterReferer:  getEnv("OPENROUTER_REFERER", "https://snaphost.app"),
		OpenRouterAppName:  getEnv("OPENROUTER_APP_NAME", "SnapHost"),
		CacheTTLDays:       getEnvInt("CACHE_TTL_DAYS", 7),
		MaxFileSizeKB:      getEnvInt("MAX_FILE_SIZE_KB", 50),
		MaxFilesPerRequest: getEnvInt("MAX_FILES_PER_REQUEST", 20),
		RunMigrations:      getEnvBool("RUN_MIGRATIONS", true),
	}

	imagesStr := getEnv("ALLOWED_BASE_IMAGES", "node:,python:,golang:,ruby:,nginx:,alpine:,debian:,ubuntu:,gcr.io/distroless/,oven/bun:,denoland/deno:")
	for _, p := range strings.Split(imagesStr, ",") {
		if p = strings.TrimSpace(p); p != "" {
			c.AllowedBaseImagePrefixes = append(c.AllowedBaseImagePrefixes, p)
		}
	}
	if len(c.AllowedBaseImagePrefixes) == 0 {
		return nil, fmt.Errorf("config: ALLOWED_BASE_IMAGES resolved to empty list")
	}

	if c.WebhookSecret == "" {
		return nil, errors.New("WEBHOOK_SECRET is required")
	}
	if c.OpenRouterAPIKey == "" {
		return nil, errors.New("OPENROUTER_API_KEY is required")
	}

	return c, nil
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return defaultVal
}

func getEnvBool(key string, defaultVal bool) bool {
	if val := os.Getenv(key); val != "" {
		if b, err := strconv.ParseBool(val); err == nil {
			return b
		}
	}
	return defaultVal
}

func getEnvDuration(key string, defaultVal time.Duration) time.Duration {
	if val := os.Getenv(key); val != "" {
		if d, err := time.ParseDuration(val); err == nil {
			return d
		}
	}
	return defaultVal
}
