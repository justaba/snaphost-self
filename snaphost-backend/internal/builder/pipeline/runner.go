// Package pipeline orchestrates the full build pipeline: clone → detect →
// validate → build → scan → report. Each stage has explicit timeouts and
// structured logging.
package pipeline

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"snaphost/internal/builder/ai"
	"snaphost/internal/builder/build"
	"snaphost/internal/builder/clone"
	"snaphost/internal/builder/config"
	"snaphost/internal/builder/detect"
	"snaphost/internal/builder/events"
	"snaphost/internal/builder/logs"
	"snaphost/internal/builder/queue"
	"snaphost/internal/builder/registry"
	"snaphost/internal/builder/scan"
	"snaphost/internal/builder/unpack"
	"snaphost/internal/gitcreds"
	"snaphost/internal/shared/validator"
	"snaphost/internal/uploads"
)

// StatusReporter is the interface for reporting deploy status back to the
// user-billing service. The build-success path uses Redis BuildEvent
// (consumed by the saga) for image_ref / commit_sha / port — there is no
// HTTP "built" callback because "built" is not a valid value of
// deploys.status. See chk_deploys_status constraint and the saga
// orchestrator's WaitForBuildEvent.
type StatusReporter interface {
	// ReportBuilding notifies that the build has started.
	ReportBuilding(ctx context.Context, deployID string) error
	// ReportFailed notifies that the build has failed with the given reason.
	ReportFailed(ctx context.Context, deployID, reason string) error
}

// AIClient interface moved to internal/ai

// Runner orchestrates the full build pipeline for each job.
type Runner struct {
	// Cfg is the service configuration.
	Cfg *config.Config
	// Queue is the Redis Streams job queue.
	Queue *queue.Queue
	// Publisher is the build log publisher.
	Publisher logs.Publisher
	// Events publishes coarse-grained build lifecycle events
	// (started/completed/failed) for the saga orchestrator to consume.
	// Optional; nil disables event publishing.
	Events events.Publisher
	// Cloner performs Git clone operations.
	Cloner *clone.Cloner
	// Builder performs BuildKit builds.
	Builder *build.Builder
	// Scanner performs vulnerability scanning.
	Scanner *scan.Scanner
	// Status reports deploy status to user-billing.
	Status StatusReporter
	// AI generates Dockerfiles when none is provided.
	AIClient ai.Client
	// Registry cleans up images that fail the post-push scan. Optional;
	// nil disables cleanup (used by tests that don't exercise that path).
	Registry registry.Client
	// Uploads reads archive blobs for source_type=archive jobs (14b-2).
	// Optional; nil makes archive jobs fail permanently.
	Uploads *uploads.Store
	// Credentials reads short-lived git credentials for git_private jobs
	// (14b-3). Optional; nil makes git_private jobs fail permanently.
	Credentials *gitcreds.Store
	// UnpackLimits bounds untrusted archive extraction. Zero fields fall
	// back to the unpack package defaults.
	UnpackLimits unpack.Limits
	// Log is the structured logger.
	Log *zap.Logger
}

// publishEvent is a best-effort wrapper around the events publisher.
// Failures are logged but never propagated to the pipeline.
func (r *Runner) publishEvent(ctx context.Context, ev events.BuildEvent) {
	if r.Events == nil {
		return
	}
	if err := r.Events.Publish(ctx, ev); err != nil {
		r.Log.Warn("publish build event failed",
			zap.String("deploy_id", ev.DeployID),
			zap.String("type", string(ev.Type)),
			zap.Error(err))
	}
}

// Result is the success output of Runner.Run. The worker passes it to
// FinalizeAsSucceeded to publish the BuildCompleted lifecycle event.
type Result struct {
	ImageRef  string
	CommitSHA string
	Port      int
}

// Run executes the full build pipeline for a single job.
//
//   - On error: returns nil, tagged err (transient/permanent per classify.go).
//     Caller (worker) decides whether to retry (Task 5b) or call
//     FinalizeAsFailed to surface the failure to the saga.
//   - On success: returns *Result, nil. Caller is responsible for calling
//     FinalizeAsSucceeded to publish BuildCompleted.
//
// Run does NOT publish BuildFailed / BuildCompleted or call ReportFailed
// on its own — that path moved to FinalizeAsFailed / FinalizeAsSucceeded
// so the worker can defer event publication until after retry exhaustion.
// Today's worker calls the finalizers immediately, preserving end-to-end
// behaviour.
func (r *Runner) Run(ctx context.Context, job queue.Job) (*Result, error) {
	log := r.Log.With(
		zap.String("deploy_id", job.DeployID),
		zap.String("user_id", job.UserID),
		zap.String("stage", "pipeline"),
	)

	// 1. Publish pipeline started.
	_ = r.Publisher.Publish(job.DeployID, logs.LogLine{
		Stage:     "pipeline",
		Text:      "pipeline started",
		Level:     "info",
		Timestamp: time.Now().UTC(),
	})

	// 2. Mark job active.
	if err := r.Queue.MarkActive(ctx, job.UserID, job.ID); err != nil {
		log.Error("failed to mark job active", zap.Error(err))
	}

	// 3. Defer mark done (always, regardless of success).
	defer func() {
		if err := r.Queue.MarkDone(ctx, job.UserID, job.ID); err != nil {
			log.Error("failed to mark job done", zap.Error(err))
		}
	}()

	// 4. Report "building" status + publish lifecycle event.
	if err := r.Status.ReportBuilding(ctx, job.DeployID); err != nil {
		log.Warn("failed to report building status", zap.Error(err))
	}
	r.publishEvent(ctx, events.BuildEvent{
		Type:      events.BuildStarted,
		DeployID:  job.DeployID,
		Timestamp: time.Now().UTC(),
	})

	imageRef, commitSHA, appPort, err := r.executePipeline(ctx, job, log)
	if err != nil {
		return nil, err
	}
	return &Result{
		ImageRef:  imageRef,
		CommitSHA: commitSHA,
		Port:      appPort,
	}, nil
}

// FinalizeAsSucceeded publishes the BuildCompleted lifecycle event with
// the success metadata, emits a final "pipeline complete" log line, and
// logs a structured success record. Symmetric to FinalizeAsFailed —
// both event publications are owned by the worker, not the pipeline
// itself, so Task 5b can defer publishing for transient errors that
// may resolve on retry.
//
// Best-effort: publish failures are logged but not returned.
func (r *Runner) FinalizeAsSucceeded(ctx context.Context, deployID string, result *Result) {
	if result == nil {
		r.Log.Warn("FinalizeAsSucceeded called with nil result", zap.String("deploy_id", deployID))
		return
	}

	_ = r.Publisher.Publish(deployID, logs.LogLine{
		Stage:     "pipeline",
		Text:      "pipeline complete",
		Level:     "info",
		Timestamp: time.Now().UTC(),
	})

	r.publishEvent(ctx, events.BuildEvent{
		Type:      events.BuildCompleted,
		DeployID:  deployID,
		ImageRef:  result.ImageRef,
		Port:      result.Port,
		CommitSHA: result.CommitSHA,
		Timestamp: time.Now().UTC(),
	})

	r.Log.Info("deploy finalized as succeeded",
		zap.String("deploy_id", deployID),
		zap.String("image_ref", result.ImageRef),
		zap.String("commit_sha", result.CommitSHA),
	)
}

// FinalizeAsFailed publishes the BuildFailed lifecycle event, calls
// Status.ReportFailed, and emits a final "pipeline failed" log line for
// the given deploy. It is called by the worker after Runner.Run returns
// a non-nil error and the worker has decided no further retry is
// appropriate (today: always; in 5b: only after exhausting the retry
// budget on transient errors, or immediately on permanent ones).
//
// Best-effort: failure of the publish or status-report calls is logged
// but does not propagate. The caller has already classified this as
// terminal for the deploy.
func (r *Runner) FinalizeAsFailed(ctx context.Context, deployID string, cause error) {
	log := r.Log.With(
		zap.String("deploy_id", deployID),
		zap.String("stage", "finalize"),
		zap.Bool("transient", IsTransient(cause)),
		zap.Bool("permanent", IsPermanent(cause)),
	)

	reason := "unknown failure"
	if cause != nil {
		reason = cause.Error()
	}

	_ = r.Publisher.Publish(deployID, logs.LogLine{
		Stage:     "pipeline",
		Text:      fmt.Sprintf("pipeline failed: %s", reason),
		Level:     "error",
		Timestamp: time.Now().UTC(),
	})

	if r.Status != nil {
		if reportErr := r.Status.ReportFailed(ctx, deployID, reason); reportErr != nil {
			log.Error("failed to report failure status", zap.Error(reportErr))
		}
	}

	r.publishEvent(ctx, events.BuildEvent{
		Type:      events.BuildFailed,
		DeployID:  deployID,
		Reason:    reason,
		Timestamp: time.Now().UTC(),
	})

	log.Info("deploy finalized as failed")
}

// executePipeline runs the core pipeline stages and returns the image ref,
// commit SHA, and resolved application port on success. The port is taken
// from EXPOSE in the Dockerfile, falling back to a language heuristic; 0
// means the consumer should use its own default.
func (r *Runner) executePipeline(ctx context.Context, job queue.Job, log *zap.Logger) (string, string, int, error) {
	// Source dispatch (Task 14b): git_public/git_private clone (private
	// with a short-lived credential), archive unpacks an uploaded Redis
	// blob. The API gates unknown types upstream, so hitting the default
	// arm is a defence-in-depth check against stale or hand-crafted jobs.
	switch job.SourceType {
	case "", queue.SourceGitPublic, queue.SourceGitPrivate, queue.SourceArchive:
	default:
		return "", "", 0, Permanent(fmt.Errorf("source_type %q is not supported by this worker", job.SourceType))
	}

	// 5. URL validation (git sources only).
	var validated *clone.ValidatedURL
	if job.SourceType != queue.SourceArchive {
		log.Info("validating repo URL", zap.String("stage", "validate"))
		v, err := clone.ValidateRepoURL(ctx, job.RepoURL, r.Cfg.AllowedGitHosts, nil)
		if err != nil {
			return "", "", 0, classifyValidationError(fmt.Errorf("url validation: %w", err))
		}
		validated = v
	}

	// 6. Prepare workdir.
	workdir, err := clone.PrepareWorkdir(r.Cfg.WorkdirRoot, job.DeployID, log)
	if err != nil {
		return "", "", 0, Transient(fmt.Errorf("prepare workdir: %w", err))
	}
	defer workdir.Cleanup()

	// 7. Materialize the source: clone (git) or unpack (archive).
	var commitSHA string
	if job.SourceType == queue.SourceArchive {
		if err := r.unpackUpload(ctx, job, workdir.Path, log); err != nil {
			return "", "", 0, err
		}
	} else {
		cloneOpts := clone.CloneOptions{
			Validated:   validated,
			Branch:      job.Branch,
			DeployID:    job.DeployID,
			WorkdirPath: workdir.Path,
			Timeout:     5 * time.Minute,
			MaxSizeMB:   r.Cfg.MaxRepoSizeMB,
		}
		// git_private: resolve the short-lived credential right before the
		// clone and delete it right after — success or failure — so the
		// secret's life is one clone attempt (TTL is the backstop).
		if job.SourceType == queue.SourceGitPrivate {
			if r.Credentials == nil {
				return "", "", 0, Permanent(fmt.Errorf("git_private source: credential store is not configured"))
			}
			cred, err := r.Credentials.Get(ctx, job.CredentialID)
			if err != nil {
				if errors.Is(err, gitcreds.ErrNotFound) {
					return "", "", 0, Permanent(fmt.Errorf("git_private source: credential %s not found or expired", job.CredentialID))
				}
				return "", "", 0, Transient(fmt.Errorf("git_private source: fetch credential: %w", err))
			}
			// Single-use by design: deleted after the first clone attempt.
			// If Task 5b adds transient-retry to the worker, a retried
			// git_private job will find the credential gone and must fail
			// permanently — revisit this delete when 5b lands.
			defer func() {
				if err := r.Credentials.Delete(context.WithoutCancel(ctx), job.CredentialID); err != nil {
					log.Warn("delete git credential failed", zap.String("credential_id", job.CredentialID), zap.Error(err))
				}
			}()
			if cred.UserID != job.UserID {
				return "", "", 0, Permanent(fmt.Errorf("git_private source: credential %s does not belong to the requesting user", job.CredentialID))
			}
			cloneOpts.AuthToken = cred.Token
			cloneOpts.AuthUsername = cred.Username
		}

		log.Info("cloning repository", zap.String("stage", "clone"))
		cloneResult, err := r.Cloner.Clone(ctx, cloneOpts)
		if err != nil {
			return "", "", 0, classifyGitError(fmt.Errorf("clone: %w", err))
		}
		commitSHA = cloneResult.CommitSHA
	}

	// 8. Detect project type.
	log.Info("detecting project type", zap.String("stage", "detect"))
	project, err := detect.Detect(workdir.Path)
	if err != nil {
		return "", "", 0, Permanent(fmt.Errorf("detect: %w", err))
	}
	log.Info("project detected",
		zap.String("stage", "detect"),
		zap.String("language", project.Language),
		zap.String("framework", project.Framework),
		zap.String("dockerfile", project.DockerfilePath),
	)

	// Bail out early when the project class is structurally non-deployable
	// (mobile, desktop). Skipped if the repo ships its own Dockerfile —
	// in that case the user has explicitly opted in to whatever they wrote.
	if project.DockerfilePath == "" && project.UnsupportedReason != "" {
		_ = r.Publisher.Publish(job.DeployID, logs.LogLine{
			Stage:     "detect",
			Text:      project.UnsupportedReason,
			Level:     "error",
			Timestamp: time.Now().UTC(),
		})
		return "", "", 0, Permanent(fmt.Errorf("unsupported project type: %s", project.UnsupportedReason))
	}

	// 9. If no Dockerfile found, generate one via AI Orchestrator.
	if project.DockerfilePath == "" {
		_ = r.Publisher.Publish(job.DeployID, logs.LogLine{
			Stage:     "detect",
			Text:      "no Dockerfile found, requesting AI-generated Dockerfile",
			Level:     "info",
			Timestamp: time.Now().UTC(),
		})

		aiResp, err := r.AIClient.GenerateDockerfile(ctx, ai.GenerateRequest{
			DeployID:    job.DeployID,
			UserID:      job.UserID,
			ProjectInfo: project,
			FileTree:    buildFileTree(workdir.Path, 3, 50),
			KeyFiles:    readKeyFiles(workdir.Path),
		})
		if err != nil {
			return "", "", 0, classifyAIError(fmt.Errorf("AI generation failed: %w", err), log)
		}

		_ = r.Publisher.Publish(job.DeployID, logs.LogLine{
			Stage:     "detect",
			Text:      fmt.Sprintf("Dockerfile generated (source: %s)", aiResp.Metadata.Source),
			Level:     "info",
			Timestamp: time.Now().UTC(),
		})

		dockerfilePath := filepath.Join(workdir.Path, "Dockerfile.snaphost")
		if err := os.WriteFile(dockerfilePath, []byte(aiResp.Dockerfile), 0644); err != nil {
			return "", "", 0, Transient(fmt.Errorf("write generated Dockerfile: %w", err))
		}
		project.DockerfilePath = "Dockerfile.snaphost"

		// Materialize any auxiliary files the model emitted alongside the
		// Dockerfile (nginx.conf, supervisord.conf, etc.) into the build
		// context so subsequent COPY validation and BuildKit can find them.
		// Permanent: writeSupportFiles errors are dominated by AI emitting
		// bad paths (escape attempt, invalid relpath). The minority of IO
		// errors gets misclassified — accepted trade-off until concrete
		// observability data justifies splitting.
		if err := writeSupportFiles(workdir.Path, aiResp.SupportFiles); err != nil {
			return "", "", 0, Permanent(fmt.Errorf("write AI support files: %w", err))
		}
	}

	// 10. Validate Dockerfile. Strict policy for our generated Dockerfiles;
	// permissive policy for user-shipped ones. Tag rules (no :latest, no
	// missing tag) apply in both modes.
	log.Info("validating Dockerfile", zap.String("stage", "validate"))
	dockerfilePath := filepath.Join(workdir.Path, project.DockerfilePath)

	mode := validator.ModePermissive
	if project.DockerfilePath == "Dockerfile.snaphost" {
		mode = validator.ModeStrict
	}
	issues, err := validator.ValidateDockerfileWithOptions(dockerfilePath, validator.Options{
		Mode:                               mode,
		AllowedBaseImagePrefixesStrict:     r.Cfg.AllowedBaseImagePrefixes,
		AllowedBaseImagePrefixesPermissive: r.Cfg.AllowedBaseImagePrefixesPermissive,
	})
	if err != nil {
		return "", "", 0, Permanent(fmt.Errorf("validate dockerfile: %w", err))
	}

	// Remediation hint when user-provided Dockerfile is rejected on base
	// image. Permissive mode = user authored the Dockerfile so they can fix
	// it; strict mode = we authored it so no user action is meaningful.
	if mode == validator.ModePermissive {
		for _, issue := range issues {
			if issue.Code == "base_image_not_allowed" {
				_ = r.Publisher.Publish(job.DeployID, logs.LogLine{
					Stage: "validate",
					Text: "your Dockerfile uses a base image we don't allow. " +
						"Either change the FROM line to one of our allowed " +
						"images, or remove the Dockerfile from your repo and " +
						"we will generate one for you.",
					Level:     "error",
					Timestamp: time.Now().UTC(),
				})
				break
			}
		}
	}

	// Check for blocking issues (everything except secret_build_arg warnings).
	for _, issue := range issues {
		_ = r.Publisher.Publish(job.DeployID, logs.LogLine{
			Stage:     "validate",
			Text:      fmt.Sprintf("line %d [%s]: %s", issue.Line, issue.Code, issue.Message),
			Level:     issueLevel(issue.Code),
			Timestamp: time.Now().UTC(),
		})
		if issue.Code != "secret_build_arg" {
			return "", "", 0, Permanent(fmt.Errorf("dockerfile validation failed: line %d: %s", issue.Line, issue.Message))
		}
	}

	// Port contract (Task 15a). A user-shipped Dockerfile that exposes a fixed
	// port and never mentions PORT usually means the server is hard-bound to
	// that port — it will build, push, start, and then never receive a request,
	// because the runtime invokes the container on the port it injects. The
	// check cannot see what the app reads at runtime, so this is a warning and
	// never blocks the build.
	if mode == validator.ModePermissive {
		if port, risky := detect.FixedPortRisk(dockerfilePath); risky {
			_ = r.Publisher.Publish(job.DeployID, logs.LogLine{
				Stage: "validate",
				Text: fmt.Sprintf("your Dockerfile exposes port %d and never mentions PORT. "+
					"The runtime injects a PORT environment variable and sends traffic there, "+
					"so a server hard-bound to %d will deploy but never receive a request. "+
					"Make the server listen on $PORT (falling back to %d when it is unset). "+
					"Ignore this if your application already reads PORT at runtime.",
					port, port, port),
				Level:     "warn",
				Timestamp: time.Now().UTC(),
			})
		}
	}

	// Catch hallucinated COPY/ADD sources (e.g. AI-invented nginx.conf) before
	// they explode inside BuildKit with an opaque "not found" message.
	copyIssues, err := validator.ValidateCopySources(dockerfilePath, workdir.Path)
	if err != nil {
		return "", "", 0, Permanent(fmt.Errorf("validate copy sources: %w", err))
	}
	for _, issue := range copyIssues {
		_ = r.Publisher.Publish(job.DeployID, logs.LogLine{
			Stage:     "validate",
			Text:      fmt.Sprintf("line %d [%s]: %s", issue.Line, issue.Code, issue.Message),
			Level:     "error",
			Timestamp: time.Now().UTC(),
		})
	}
	if len(copyIssues) > 0 {
		return "", "", 0, Permanent(fmt.Errorf("dockerfile validation failed: line %d: %s", copyIssues[0].Line, copyIssues[0].Message))
	}

	// 11. Compute image ref.
	imageRef := imageRefForJob(r.Cfg.RegistryURL, job.UserID, job.DeployID)

	// 12. Build image and push to registry.
	log.Info("building image", zap.String("stage", "build"), zap.String("image_ref", imageRef))
	buildTimeout := time.Duration(r.Cfg.MaxBuildTimeMin) * time.Minute
	_, err = r.Builder.Build(ctx, build.BuildOptions{
		DeployID:       job.DeployID,
		ContextDir:     workdir.Path,
		DockerfileName: project.DockerfilePath,
		ImageName:      imageRef,
		BuildArgs:      job.BuildArgs,
		Timeout:        buildTimeout,
	})
	if err != nil {
		return "", "", 0, classifyBuildKitError(fmt.Errorf("build: %w", err))
	}

	// 13. Scan pushed image via Trivy. On critical-vulnerability failure
	// with the gate enabled, delete the image from the registry so
	// vulnerable artifacts don't accumulate. Cleanup failure is logged
	// but doesn't change the user-visible outcome — scan failure is the
	// primary error.
	log.Info("scanning image for vulnerabilities", zap.String("stage", "scan"))
	scanResult, err := r.Scanner.Scan(ctx, imageRef)
	if err != nil {
		if errors.Is(err, scan.ErrCriticalVulnerability) {
			if r.Cfg.ScanFailOnCritical {
				if r.Registry != nil {
					if delErr := r.Registry.DeleteImage(ctx, imageRef); delErr != nil {
						log.Error("failed to delete vulnerable image from registry",
							zap.String("image_ref", imageRef),
							zap.Error(delErr),
						)
					}
				}
				return "", "", 0, Permanent(fmt.Errorf("scan: critical vulnerabilities found (CRITICAL=%d, HIGH=%d)",
					scanResult.CriticalCount, scanResult.HighCount))
			}
			// Gate disabled (typically dev): log + publish a warning so the
			// finding is visible, but let the build proceed.
			log.Warn("scan found critical vulnerabilities; gate disabled, proceeding",
				zap.Int("critical", scanResult.CriticalCount),
				zap.Int("high", scanResult.HighCount),
			)
			_ = r.Publisher.Publish(job.DeployID, logs.LogLine{
				Stage:     "scan",
				Text:      fmt.Sprintf("found %d CRITICAL, %d HIGH vulnerabilities — gate disabled, deploy continuing", scanResult.CriticalCount, scanResult.HighCount),
				Level:     "warn",
				Timestamp: time.Now().UTC(),
			})
		} else {
			// Trivy process error (binary missing, exec failed, etc.) — transient infra issue.
			return "", "", 0, Transient(fmt.Errorf("scan: %w", err))
		}
	}

	// Resolve the application port: prefer EXPOSE in the Dockerfile, then
	// fall back to a heuristic by detected language. 0 means "unknown" —
	// the saga consumer will fall back to its DEPLOY_DEFAULT_PORT env.
	dockerfileAbsPath := filepath.Join(workdir.Path, project.DockerfilePath)
	appPort := detect.PortFromDockerfile(dockerfileAbsPath)
	if appPort == 0 {
		lang := project.Language
		if lang == "" || lang == "unknown" {
			lang = detect.DetectLanguage(workdir.Path)
		}
		appPort = detect.DefaultPortForLanguage(lang)
	}

	return imageRef, commitSHA, appPort, nil
}

// unpackUpload fetches the uploaded archive blob for an archive-source
// job, verifies its ownership, extracts it into the workdir under the
// unpack security limits, and deletes the blob (TTL is the backstop if
// the delete fails).
func (r *Runner) unpackUpload(ctx context.Context, job queue.Job, workdirPath string, log *zap.Logger) error {
	if r.Uploads == nil {
		return Permanent(fmt.Errorf("archive source: upload store is not configured"))
	}
	log.Info("fetching uploaded archive", zap.String("stage", "unpack"), zap.String("upload_id", job.UploadID))

	archive, owner, size, err := r.Uploads.Open(ctx, job.UploadID)
	if err != nil {
		if errors.Is(err, uploads.ErrNotFound) {
			return Permanent(fmt.Errorf("archive source: upload %s not found or expired", job.UploadID))
		}
		return Transient(fmt.Errorf("archive source: fetch upload: %w", err))
	}
	defer archive.Close()
	// The archive is single-use: delete it whether unpack succeeds or fails,
	// so a failed build does not leave user source on disk until the TTL.
	defer func() {
		if err := r.Uploads.Delete(context.WithoutCancel(ctx), job.UploadID); err != nil {
			log.Warn("delete upload failed", zap.String("upload_id", job.UploadID), zap.Error(err))
		}
	}()

	if owner != job.UserID {
		return Permanent(fmt.Errorf("archive source: upload %s does not belong to the requesting user", job.UploadID))
	}

	// Streamed from the file rather than read into memory first: this is the
	// path a 50 MB tar.gz takes, and the point of moving it off Redis.
	res, err := unpack.TarGzFrom(archive, size, workdirPath, r.UnpackLimits)
	if err != nil {
		// Every unpack failure is attacker-controllable input — permanent,
		// never retried.
		return Permanent(fmt.Errorf("archive source: %w", err))
	}

	_ = r.Publisher.Publish(job.DeployID, logs.LogLine{
		Stage:     "unpack",
		Text:      fmt.Sprintf("archive unpacked: %d files, %d bytes", res.Files, res.TotalBytes),
		Level:     "info",
		Timestamp: time.Now().UTC(),
	})
	return nil
}

func imageRefForJob(registryURL, userID, deployID string) string {
	userHash := fmt.Sprintf("%x", sha256.Sum256([]byte(userID)))
	registryURL = strings.TrimRight(registryURL, "/")
	return fmt.Sprintf("%s/proj-%s:%s", registryURL, userHash[:8], deployID)
}

// issueLevel returns the log level for a validation issue code.
func issueLevel(code string) string {
	if code == "secret_build_arg" {
		return "warn"
	}
	return "error"
}

// writeSupportFiles materializes AI-emitted auxiliary files into the build
// context. Paths must be relative and stay inside the context dir — anything
// trying to escape (absolute path, traversal) is rejected.
func writeSupportFiles(contextDir string, files map[string]string) error {
	if len(files) == 0 {
		return nil
	}
	absRoot, err := filepath.Abs(contextDir)
	if err != nil {
		return fmt.Errorf("resolve context dir: %w", err)
	}
	for relPath, content := range files {
		if relPath == "" || filepath.IsAbs(relPath) {
			return fmt.Errorf("invalid support_files path %q", relPath)
		}
		joined := filepath.Join(absRoot, relPath)
		absJoined, err := filepath.Abs(joined)
		if err != nil {
			return fmt.Errorf("resolve support_files path %q: %w", relPath, err)
		}
		rel, err := filepath.Rel(absRoot, absJoined)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("support_files path %q escapes build context", relPath)
		}
		if err := os.MkdirAll(filepath.Dir(absJoined), 0755); err != nil {
			return fmt.Errorf("mkdir for %q: %w", relPath, err)
		}
		if err := os.WriteFile(absJoined, []byte(content), 0644); err != nil {
			return fmt.Errorf("write %q: %w", relPath, err)
		}
	}
	return nil
}

func buildFileTree(root string, maxDepth, maxEntries int) []string {
	var tree []string
	var walk func(path string, depth int)
	walk = func(path string, depth int) {
		if depth > maxDepth || len(tree) >= maxEntries {
			return
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return
		}
		for _, e := range entries {
			if len(tree) >= maxEntries {
				break
			}
			if e.Name() == ".git" || e.Name() == "node_modules" || e.Name() == "vendor" {
				continue
			}
			rel, _ := filepath.Rel(root, filepath.Join(path, e.Name()))
			if e.IsDir() {
				walk(filepath.Join(path, e.Name()), depth+1)
			} else {
				tree = append(tree, rel)
			}
		}
	}
	walk(root, 1)
	return tree
}

func readKeyFiles(root string) map[string]string {
	keys := []string{
		// JS/TS ecosystem
		"package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock",
		".nvmrc", ".node-version",
		// Go
		"go.mod", "go.sum",
		// Python
		"requirements.txt", "pyproject.toml", "Pipfile", ".python-version",
		// Misc
		"Gemfile", "composer.json", "README.md",
	}
	res := make(map[string]string)
	for _, k := range keys {
		content, err := os.ReadFile(filepath.Join(root, k))
		if err == nil {
			if len(content) > 50000 {
				res[k] = string(content[:50000]) + "\n...[truncated]"
			} else {
				res[k] = string(content)
			}
		}
	}
	return res
}
