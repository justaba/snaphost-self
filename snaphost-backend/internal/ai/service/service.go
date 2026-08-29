// Package service implements the top-level Dockerfile generation orchestrator.
// It follows a deterministic-first approach: template library → LLM fallback.
package service

import (
	"context"
	"time"

	"snaphost/internal/ai/cache"
	"snaphost/internal/ai/config"
	"snaphost/internal/ai/detector"
	"snaphost/internal/ai/llm"
	"snaphost/internal/ai/templates"
	"snaphost/internal/ai/usage"

	"go.uber.org/zap"
)

// Service orchestrates Dockerfile generation: cache lookup → template match → LLM fallback.
type Service struct {
	cache *cache.Repository
	usage *usage.Repository
	llm   *llm.CircuitClient
	cfg   *config.Config
	log   *zap.Logger
}

// New constructs a Service with all required dependencies.
func New(
	cacheRepo *cache.Repository,
	usageRepo *usage.Repository,
	llmClient *llm.CircuitClient,
	cfg *config.Config,
	log *zap.Logger,
) *Service {
	return &Service{
		cache: cacheRepo,
		usage: usageRepo,
		llm:   llmClient,
		cfg:   cfg,
		log:   log,
	}
}

// Metadata is included in every response for observability.
type Metadata struct {
	// Source is one of "cache", "template", or "llm".
	Source string `json:"source"`
	// TemplateID is set when Source == "template".
	TemplateID string `json:"template_id,omitempty"`
	// Model is the OpenRouter model used when Source == "llm".
	Model string `json:"model,omitempty"`
	// DurationMs is wall-clock time for the full generation.
	DurationMs int `json:"duration_ms"`
	// TokensUsed is the total token count consumed (0 for template/cache hits).
	TokensUsed int `json:"tokens_used"`
}

// ServiceResponse is the HTTP response body for the generate-dockerfile endpoint.
type ServiceResponse struct {
	Dockerfile   string            `json:"dockerfile"`
	ExposePort   int               `json:"expose_port"`
	BuildArgs    map[string]string `json:"build_args"`
	SupportFiles map[string]string `json:"support_files"`
	// Source mirrors Metadata.Source for quick inspection without unpacking Metadata.
	Source   string   `json:"source"`
	CacheHit bool     `json:"cache_hit"`
	Metadata Metadata `json:"metadata"`
}

// GenerateDockerfile is the main entry point. It applies the deterministic-first
// strategy and falls back to the LLM only when no template matches.
func (s *Service) GenerateDockerfile(ctx context.Context, req llm.GenerateRequest) (*ServiceResponse, error) {
	start := time.Now()

	// 1. Enrich raw signals into structured form for template matching.
	signals := templates.ProjectSignals{
		FileTree: req.FileTree,
		KeyFiles: req.KeyFiles,
	}
	signals = detector.Enrich(signals)

	// 2. Compute cache key before any expensive work.
	signature := s.cache.ComputeSignature(req)

	// 3. Cache lookup — fast path.
	if cached, err := s.cache.Get(ctx, signature); err == nil {
		_ = s.cache.IncrementUsage(ctx, signature)
		s.recordUsage(ctx, req, "cache", cached.Source, "", 0, 0, int(time.Since(start).Milliseconds()), true, nil, true)
		return &ServiceResponse{
			Dockerfile: cached.Dockerfile,
			ExposePort: cached.ExposePort,
			BuildArgs:  map[string]string{},
			Source:     cached.Source,
			CacheHit:   true,
			Metadata: Metadata{
				Source:     cached.Source,
				DurationMs: int(time.Since(start).Milliseconds()),
			},
		}, nil
	}

	// 4. Template library — deterministic, no LLM call.
	if match, ok := templates.FindMatch(signals); ok {
		rendered, err := templates.Render(match.Template, match.Vars)
		if err != nil {
			s.log.Warn("template render failed, falling through to LLM",
				zap.String("template", match.Template.ID),
				zap.Error(err),
			)
		} else {
			_ = s.cache.Store(ctx, signature, rendered, match.Template.ExposePort, match.Template.ID, "template")
			s.recordUsage(ctx, req, "template", "template", "", 0, 0, int(time.Since(start).Milliseconds()), true, nil, false)
			return &ServiceResponse{
				Dockerfile: rendered,
				ExposePort: match.Template.ExposePort,
				BuildArgs:  map[string]string{},
				Source:     "template",
				CacheHit:   false,
				Metadata: Metadata{
					Source:     "template",
					TemplateID: match.Template.ID,
					DurationMs: int(time.Since(start).Milliseconds()),
				},
			}, nil
		}
	}

	// 5. LLM fallback — enforce standard security constraints.
	// Allow-list is sourced from config (same env var as builder-svc's
	// validator). Single source of truth across both services.
	req.Constraints = s.buildLLMConstraints()

	resp, err := s.llm.Generate(ctx, req)
	if err != nil {
		errMsg := err.Error()
		s.recordUsage(ctx, req, "openrouter", s.cfg.OpenRouterModel, "", 0, 0, int(time.Since(start).Milliseconds()), false, &errMsg, false)
		return nil, err
	}

	// Skip cache for responses that ship support_files — the cache row only
	// stores the Dockerfile, so a hit would silently drop the auxiliary files
	// and break the next build. Re-running the LLM for these is acceptable;
	// they're rare relative to plain server projects.
	if len(resp.SupportFiles) == 0 {
		_ = s.cache.Store(ctx, signature, resp.Dockerfile, resp.ExposePort, "llm-generated", "llm")
	}
	s.recordUsage(ctx, req, "openrouter", resp.Model, resp.Model, resp.InputTokens, resp.OutputTokens, resp.DurationMs, true, nil, false)

	return &ServiceResponse{
		Dockerfile:   resp.Dockerfile,
		ExposePort:   resp.ExposePort,
		BuildArgs:    resp.BuildArgs,
		SupportFiles: resp.SupportFiles,
		Source:       "llm",
		CacheHit:     false,
		Metadata: Metadata{
			Source:     "llm",
			Model:      resp.Model,
			DurationMs: resp.DurationMs,
			TokensUsed: resp.InputTokens + resp.OutputTokens,
		},
	}, nil
}

// buildLLMConstraints assembles the constraint object passed to the LLM
// client. Extracted into a method so a regression test can pin that the
// allow-list comes from config, not a hardcoded literal.
func (s *Service) buildLLMConstraints() llm.Constraints {
	return llm.Constraints{
		AllowedBaseImagePrefixes: s.cfg.AllowedBaseImagePrefixes,
		RequireNonRootUser:       true,
	}
}

// recordUsage persists an audit row to ai_usage_log. Errors are logged but never
// propagated — usage tracking must not fail the primary request.
func (s *Service) recordUsage(
	ctx context.Context,
	req llm.GenerateRequest,
	provider, model, _ string,
	inTokens, outTokens, durMs int,
	success bool,
	errClass *string,
	cacheHit bool,
) {
	entry := usage.UsageEntry{
		DeployID:     req.DeployID,
		UserID:       req.UserID,
		Provider:     provider,
		Model:        model,
		Operation:    "generate_dockerfile",
		InputTokens:  inTokens,
		OutputTokens: outTokens,
		// Micro-USD cost: rough estimate (input cheaper than output).
		CostUSDMicro: int64(inTokens*10 + outTokens*30),
		DurationMs:   durMs,
		Success:      success,
		ErrorClass:   errClass,
		CacheHit:     cacheHit,
	}
	if err := s.usage.Record(ctx, entry); err != nil {
		s.log.Warn("failed to record usage entry", zap.Error(err))
	}
}
