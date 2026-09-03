// Package config loads and validates Dockerfile-generator configuration.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime configuration for Dockerfile generation.
type Config struct {
	// AllowedBaseImagePrefixes is the list of permitted Docker base image
	// prefixes used to constrain LLM generation. Sourced from
	// ALLOWED_BASE_IMAGES env var (comma-separated). Must be identical to
	// the build validator's list, so generation and validation use the same
	// environment value.
	AllowedBaseImagePrefixes []string

	// LLMTimeout is the per-request deadline for OpenRouter calls
	// (env LLM_TIMEOUT, default 30s). Accepts Go duration strings, e.g. "45s".
	LLMTimeout time.Duration
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
}

// Load reads environment variables, applies defaults, and validates required fields.
// Returns an error if any required field is missing.
func Load() (*Config, error) {
	c := &Config{
		LLMTimeout:        getEnvDuration("LLM_TIMEOUT", 30*time.Second),
		LLMBaseURL:        getEnv("LLM_BASE_URL", "https://openrouter.ai/api/v1"),
		LLMJSONMode:       getEnvBool("LLM_JSON_MODE", true),
		OpenRouterAPIKey:  os.Getenv("OPENROUTER_API_KEY"),
		OpenRouterModel:   getEnv("OPENROUTER_MODEL", "openai/gpt-4o-mini"),
		OpenRouterReferer: getEnv("OPENROUTER_REFERER", "https://snaphost.app"),
		OpenRouterAppName: getEnv("OPENROUTER_APP_NAME", "SnapHost"),
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
