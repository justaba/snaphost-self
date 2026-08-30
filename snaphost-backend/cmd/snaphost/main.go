// Command snaphost is the whole platform in one process.
//
// It was seven. On a single-operator host the microservice split bought
// nothing and cost a Go runtime, a health check and a JSON round trip per
// component; the seams it existed to protect are now interfaces satisfied by
// direct calls (internal/wiring).
//
// Startup order matters and is deliberate:
//
//  0. the memory ceiling, before anything allocates against it;
//  1. configuration for every area, so a missing variable fails before any
//     connection is opened;
//  2. the store, opened and migrated on one handle;
//  3. the operator account, so the platform can be logged into before it can
//     be asked to authenticate anyone;
//  4. the in-process buses and stores that used to be Redis;
//  5. components, then the adapters that join them;
//  6. one HTTP engine;
//  7. background loops last, so nothing starts working before the thing it
//     reports to exists.
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"syscall"
	"time"

	"database/sql"

	"github.com/casbin/casbin/v2"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	aiapi "snaphost/internal/ai/api"
	aicache "snaphost/internal/ai/cache"
	aiconfig "snaphost/internal/ai/config"
	aillm "snaphost/internal/ai/llm"
	aiservice "snaphost/internal/ai/service"
	aiusage "snaphost/internal/ai/usage"
	builderapi "snaphost/internal/builder/api"
	builderbuild "snaphost/internal/builder/build"
	builderclone "snaphost/internal/builder/clone"
	builderconfig "snaphost/internal/builder/config"
	builderpipeline "snaphost/internal/builder/pipeline"
	builderqueue "snaphost/internal/builder/queue"
	builderscan "snaphost/internal/builder/scan"
	builderunpack "snaphost/internal/builder/unpack"
	"snaphost/internal/buildevents"
	"snaphost/internal/control/admin"
	"snaphost/internal/control/apikey"
	"snaphost/internal/control/auth"
	controlconfig "snaphost/internal/control/config"
	controldb "snaphost/internal/control/db"
	"snaphost/internal/control/deploy"
	"snaphost/internal/control/domain"
	"snaphost/internal/control/logs"
	"snaphost/internal/control/project"
	controlroutes "snaphost/internal/control/routes"
	"snaphost/internal/control/saga"
	gatewayconfig "snaphost/internal/gateway/config"
	"snaphost/internal/gateway/middleware"
	"snaphost/internal/gateway/wslogs"
	"snaphost/internal/gitcreds"
	"snaphost/internal/logbus"
	"snaphost/internal/memlimit"
	"snaphost/internal/panel"
	runtimebackend "snaphost/internal/runtime/backend"
	runtimedocker "snaphost/internal/runtime/backend/docker"
	runtimeconfig "snaphost/internal/runtime/config"
	"snaphost/internal/runtime/runner"
	"snaphost/internal/runtime/watchdog"
	"snaphost/internal/uploads"
	"snaphost/internal/wiring"
)

func main() {
	log, err := zap.NewProduction()
	if err != nil {
		panic("failed to create logger: " + err.Error())
	}
	defer log.Sync() //nolint:errcheck // flushing on exit; nothing left to report to

	// 0. Tell the runtime about the ceiling, before anything allocates against
	//    it. Nothing set GOMEMLIMIT while the production manifest did set a
	//    container limit, so the heap grew against the machine's memory and
	//    found out about the container's when the kernel killed the process.
	applyMemoryLimit(log)

	// 1. Configuration. Every area is loaded up front so that a missing or
	//    malformed variable stops the process before it opens a connection,
	//    starts a container, or half-registers a route table.
	gwCfg, err := gatewayconfig.Load()
	if err != nil {
		log.Fatal("gateway config", zap.Error(err))
	}
	ctlCfg, err := controlconfig.Load()
	if err != nil {
		log.Fatal("control config", zap.Error(err))
	}
	bldCfg, err := builderconfig.Load()
	if err != nil {
		log.Fatal("builder config", zap.Error(err))
	}
	rtCfg, err := runtimeconfig.Load()
	if err != nil {
		log.Fatal("runtime config", zap.Error(err))
	}
	aiCfg, err := aiconfig.Load()
	if err != nil {
		log.Fatal("ai config", zap.Error(err))
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 2. One store, opened before anything reads it, then migrated on the same
	//    handle. Migrating through a connection of its own would apply the
	//    schema with foreign keys off, since SQLite's pragmas are per
	//    connection — the constraints would be created and not enforced.
	//
	//    The generator's two tables live here too. They were a separate
	//    migration history against the same database only because the
	//    generator was a separate service.
	pool, err := controldb.Open(ctx, ctlCfg.DatabasePath)
	if err != nil {
		log.Fatal("database open failed", zap.Error(err))
	}
	defer pool.Close()

	if ctlCfg.RunMigrations {
		if err := controldb.RunMigrations(pool, log); err != nil {
			log.Fatal("migrations failed", zap.Error(err))
		}
	}

	// 3. The operator account, before anything can be asked to authenticate.
	//    On first start this generates a password and prints it once; on every
	//    start after that it does nothing. It is fatal because a platform
	//    nobody can log into is not a platform that has started.
	if err := auth.Bootstrap(ctx, auth.NewRepository(pool), ctlCfg.OperatorEmail, log); err != nil {
		log.Fatal("operator bootstrap failed", zap.Error(err))
	}

	// The in-process log bus. Deploy output used to meet on a Redis channel
	// because four processes produced it; one process produces it now.
	bus := logbus.New(0, 0)

	// Uploaded archives live in a directory beside the build workspaces, which
	// the image already creates with the right owner. They were Redis values:
	// a 50 MB tar.gz held in the API process, copied into Redis, and read back
	// into the builder — four copies of the same bytes, on a box picked for
	// having a gigabyte.
	uploadsStore, err := uploads.NewStore(
		filepath.Join(bldCfg.WorkdirRoot, "uploads"),
		time.Duration(ctlCfg.UploadTTLMin)*time.Minute,
	)
	if err != nil {
		log.Fatal("upload store", zap.Error(err))
	}

	// Git credentials for private clones. In memory and nowhere else: Redis
	// held these with appendonly persistence, so every token was appended to a
	// file on disk for no reason — nothing ever read it back.
	credsStore := gitcreds.NewStore()

	// Build outcomes. A separate bus from the log one on purpose: a log line
	// may be dropped for a slow reader, and a build outcome is a state
	// transition the saga acts on.
	events := buildevents.New(0)

	// 5. Components, bottom up.
	ai := buildAI(pool, aiCfg, log)
	rt := buildRuntime(pool, rtCfg, ctlCfg, bus, log)
	bld := buildBuilder(pool, bldCfg, aiCfg, ai, bus, events, uploadsStore, credsStore, rt.loader, log)
	ctl := buildControl(pool, ctlCfg, bld.enqueuer, rt.service, bus, events, uploadsStore, credsStore, log)

	// 6. One engine. The middleware order is the gateway's and still
	//    load-bearing, though for one reason rather than two now that the
	//    sliding-window limiter is gone: Enrich runs last, so the identity
	//    headers downstream handlers read are written from a verified
	//    credential and deleted when there is none.
	engine, err := buildEngine(gwCfg, ctlCfg, ctl, ai.handler, pool, bus, log)
	if err != nil {
		log.Fatal("failed to build HTTP engine", zap.Error(err))
	}

	// 7. Background loops, after everything they touch exists.
	startBackground(ctx, ctlCfg, rtCfg, bldCfg, ctl, bld, rt, uploadsStore, credsStore, log)

	srv := &http.Server{
		Addr:              ":" + gwCfg.Port,
		Handler:           engine,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("http server failed", zap.Error(err))
		}
	}()
	log.Info("snaphost started", zap.String("port", gwCfg.Port))

	<-ctx.Done()
	log.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed", zap.Error(err))
	}
	log.Info("stopped")
}

// ---------------------------------------------------------------------------
// components
// ---------------------------------------------------------------------------

type aiParts struct {
	service *aiservice.Service
	handler *aiapi.Handler
}

func buildAI(pool *sql.DB, cfg *aiconfig.Config, log *zap.Logger) aiParts {
	inner := aillm.NewClient(aillm.Config{
		APIKey:   cfg.OpenRouterAPIKey,
		BaseURL:  cfg.LLMBaseURL,
		Model:    cfg.OpenRouterModel,
		Referer:  cfg.OpenRouterReferer,
		AppName:  cfg.OpenRouterAppName,
		Timeout:  cfg.LLMTimeout,
		JSONMode: cfg.LLMJSONMode,
	}, log)
	circuit := aillm.NewCircuitClient(inner, 10, 60*time.Second)
	svc := aiservice.New(aicache.NewRepository(pool), aiusage.NewRepository(pool), circuit, cfg, log)
	return aiParts{service: svc, handler: aiapi.NewHandler(svc)}
}

type runtimeParts struct {
	service *runner.Service
	billing *wiring.BillingClient
	// loader hands a built image to the Docker daemon. It lives on the runtime
	// side because that package is the only one allowed to import the Docker
	// SDK. The builder takes it as two narrow interfaces — one to load a built
	// image, one to remove a rejected one — but it is passed around concretely so
	// that a nil backend cannot hide inside a non-nil interface value.
	loader *runtimedocker.DockerBackend
}

func buildRuntime(pool *sql.DB, cfg *runtimeconfig.Config, ctlCfg *controlconfig.Config, bus *logbus.Bus, log *zap.Logger) runtimeParts {
	publisher := &wiring.RuntimeLogPublisher{Bus: bus}

	// The runtime used to reach the control plane over HTTP to check
	// ownership and record state. Same checks, no hop.
	//
	// This one takes the control config rather than nil: it is the repository
	// the watchdog sweeps with, and the sweep is the only reader of the GC
	// policy. Without it ALIAS_IDLE_GC_DAYS and PROJECT_DEPLOY_RETENTION are
	// set, documented, and ignored.
	billingClient := &wiring.BillingClient{Repo: deployRepo(pool, bus, ctlCfg)}

	var b runtimebackend.Backend
	var loader *runtimedocker.DockerBackend
	switch cfg.RunnerBackend {
	case "docker":
		var err error
		loader, err = runtimedocker.NewDockerBackend(cfg, publisher, log)
		if err != nil {
			log.Fatal("failed to initialise docker backend", zap.Error(err))
		}
		b = loader
	default:
		log.Fatal("unknown RUNNER_BACKEND value", zap.String("value", cfg.RunnerBackend))
	}
	log.Info("runtime backend selected", zap.String("backend", b.Name()))

	if !cfg.StrictImageValidation && len(cfg.AllowedImagePrefixes) == 0 {
		log.Warn("STRICT_IMAGE_VALIDATION=false and ALLOWED_IMAGE_PREFIXES empty: any image_ref will be accepted (dev-only safe configuration)")
	}

	return runtimeParts{
		service: runner.NewService(b, billingClient, publisher, cfg, log),
		billing: billingClient,
		loader:  loader,
	}
}

type builderParts struct {
	enqueuer *builderapi.Enqueuer
	queue    *builderqueue.Queue
	runner   *builderpipeline.Runner
}

func buildBuilder(pool *sql.DB, cfg *builderconfig.Config, aiCfg *aiconfig.Config, ai aiParts, bus *logbus.Bus, events *buildevents.Bus, uploadsStore *uploads.Store, credsStore *gitcreds.Store, loader *runtimedocker.DockerBackend, log *zap.Logger) builderParts {
	_ = aiCfg

	q := builderqueue.NewQueue(0, log)
	pub := &wiring.BuilderLogPublisher{Bus: bus}
	eventsPub := &wiring.BuildEventPublisher{Bus: events}

	builder, err := builderbuild.NewBuilder(cfg.BuildKitHost, loader, pub, log)
	if err != nil {
		log.Fatal("failed to create buildkit builder", zap.Error(err))
	}

	scanner := builderscan.NewScanner(pub, log)

	return builderParts{
		enqueuer: &builderapi.Enqueuer{Queue: q, Cfg: cfg, Log: log},
		queue:    q,
		runner: &builderpipeline.Runner{
			Cfg:       cfg,
			Queue:     q,
			Publisher: pub,
			Events:    eventsPub,
			Cloner:    builderclone.NewCloner(pub, log),
			Builder:   builder,
			Scanner:   scanner,
			// Was an HTTP POST to the control plane; now the same repository
			// write, so a status update cannot be lost to a network blip.
			Status:      &wiring.StatusReporter{Repo: deployRepo(pool, bus, nil)},
			AIClient:    &wiring.AIClient{Service: ai.service},
			Images:      loader,
			Uploads:     uploadsStore,
			Credentials: credsStore,
			UnpackLimits: builderunpack.Limits{
				MaxFiles:      cfg.MaxArchiveFiles,
				MaxFileBytes:  int64(cfg.MaxArchiveFileMB) * 1024 * 1024,
				MaxTotalBytes: int64(cfg.MaxArchiveTotalMB) * 1024 * 1024,
			},
			Log: log,
		},
	}
}

type controlParts struct {
	deployRepo *deploy.Repository
	domainRepo *domain.Repository
	sagaRepo   *saga.Repository
	sagaQueue  *saga.Queue
	sagaPub    logs.Publisher
	orch       *saga.Orchestrator
	verifier   *domain.Verifier
	auth       *auth.Handler
	authSvc    *auth.Service
	deploy     *deploy.Handler
	apikey     *apikey.Handler
	domain     *domain.Handler
	tls        *domain.TLSHandler
	admin      *admin.Handler
	apikeyRepo *apikey.Repository
}

func buildControl(
	pool *sql.DB,
	cfg *controlconfig.Config,
	enqueuer *builderapi.Enqueuer,
	runtimeSvc *runner.Service,
	bus *logbus.Bus,
	events *buildevents.Bus,
	uploadsStore *uploads.Store,
	credsStore *gitcreds.Store,
	log *zap.Logger,
) controlParts {
	authSvc := auth.NewService(auth.NewRepository(pool), time.Duration(cfg.SessionTTLHours)*time.Hour)
	dRepo := deployRepo(pool, bus, cfg)
	apikeyRepo := apikey.NewRepository(pool)
	projectRepo := project.NewRepository(pool)
	domainRepo := domain.NewRepository(pool)
	adminRepo := admin.NewRepository(pool)

	sagaQueue := saga.NewQueue(0, log)
	sagaRepo := saga.NewRepository(pool)
	sagaPub := &wiring.ControlLogPublisher{Bus: bus}
	logReader := logs.NewReader(bus, dRepo)
	uploadStore := uploadsStore
	credStore := credsStore

	runnerClient := &wiring.RunnerClient{Service: runtimeSvc}

	deployHandler := deploy.NewHandler(dRepo, projectRepo, log, sagaQueue, runnerClient, logReader,
		uploadStore, int64(cfg.MaxUploadSizeMB)*1024*1024, time.Duration(cfg.UploadTTLMin)*time.Minute,
		credStore, time.Duration(cfg.GitCredTTLMin)*time.Minute)

	attachLimiter := domain.Limiter(domain.NewMemoryLimiter(cfg.DomainAttachPerHour, time.Hour))
	domainHandler := domain.NewHandler(domainRepo, attachLimiter, domain.Config{
		PlatformSuffix:  cfg.DomainSuffix,
		ReservedDomains: cfg.ReservedDomains,
		MaxPerUser:      cfg.MaxDomainsPerUser,
		RequireIdentity: cfg.DomainAttachRequireIdentity,
		CNAMETarget:     cfg.DomainCNAMETarget,
		ARecordTarget:   cfg.DomainARecordTarget,
	}, log)

	orch := &saga.Orchestrator{
		Repo:             sagaRepo,
		DeployRepo:       dRepo,
		Builder:          &wiring.BuilderClient{Enqueuer: enqueuer},
		Runner:           runnerClient,
		BuildEvents:      &wiring.BuildEventWaiter{Bus: events},
		Publisher:        sagaPub,
		Log:              log,
		BuildTimeout:     time.Duration(cfg.SagaBuildTimeoutMin) * time.Minute,
		DeployPort:       cfg.DeployDefaultPort,
		DeployTTLMinutes: cfg.DeployTTLMin,
		MaxTTLMinutes:    cfg.DeployTTLMaxMin,
		Aliases:          domainRepo,
	}

	return controlParts{
		deployRepo: dRepo,
		domainRepo: domainRepo,
		sagaRepo:   sagaRepo,
		sagaQueue:  sagaQueue,
		sagaPub:    sagaPub,
		orch:       orch,
		verifier: domain.NewVerifier(domainRepo, log,
			time.Duration(cfg.DomainVerifyIntervalSec)*time.Second,
			time.Duration(cfg.DomainReverifyHours)*time.Hour,
			time.Duration(cfg.DomainVerifyGraceHours)*time.Hour,
			50),
		auth:       auth.NewHandler(authSvc, auth.CookieOptions{Secure: cfg.SessionCookieSecure}, log),
		authSvc:    authSvc,
		deploy:     deployHandler,
		apikey:     apikey.NewHandler(apikeyRepo, log),
		domain:     domainHandler,
		tls:        domain.NewTLSHandler(domainRepo, log),
		admin:      admin.NewHandler(adminRepo, log),
		apikeyRepo: apikeyRepo,
	}
}

// deployRepo builds the deploy repository. cfg may be nil for the runtime and
// builder sides, which only read and write rows by id and never consult the
// GC policy or the domain suffix.
// The log archiver is passed to every repository, including the ones built
// with a nil config: the build pipeline and the runtime are the two components
// that mark a deploy failed most often, and they hold those repositories. A
// repository without it would silently drop the output of exactly the deploys
// worth keeping it for.
func deployRepo(pool *sql.DB, bus *logbus.Bus, cfg *controlconfig.Config) *deploy.Repository {
	opts := []deploy.Option{deploy.WithLogArchiver(&wiring.LogArchiver{Bus: bus})}
	if cfg == nil {
		return deploy.NewRepository(pool, opts...)
	}
	return deploy.NewRepository(pool, append(opts,
		deploy.WithDomainSuffix(cfg.DomainSuffix),
		deploy.WithGCPolicy(deploy.GCPolicy{
			AliasIdleDays:  cfg.AliasIdleGCDays,
			KeepPerProject: cfg.ProjectDeployRetention,
		}),
	)...)
}

// ---------------------------------------------------------------------------
// HTTP
// ---------------------------------------------------------------------------

func buildEngine(
	gwCfg *gatewayconfig.Config,
	ctlCfg *controlconfig.Config,
	ctl controlParts,
	aiHandler *aiapi.Handler,
	pool *sql.DB,
	bus *logbus.Bus,
	log *zap.Logger,
) (*gin.Engine, error) {
	enforcer, err := casbin.NewEnforcer(gwCfg.RBACModelPath, gwCfg.RBACPolicyPath)
	if err != nil {
		return nil, err
	}

	sessions := &wiring.SessionVerifier{Service: ctl.authSvc}

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()

	// Registered before all middleware, exactly as before: /health must answer
	// even when authentication is broken, and the WebSocket authenticates
	// itself because a browser cannot put a header on a handshake.
	//
	// The startup JWKS prefetch that used to sit here is gone. It fetched
	// Supabase's public keys and was fatal on failure, so this process refused
	// to start whenever an external SaaS was unreachable — for a self-hosted
	// platform that is a dependency on somebody else's uptime to run at all.
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "service": "snaphost"})
	})
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))
	r.GET("/ws/logs/:id", wslogs.Handler(bus, sessions,
		&wiring.DeployOwnership{Repo: deployRepo(pool, bus, nil)}, middleware.AllowedOrigins(), log))

	// Secret-authenticated routes, also before the user middleware: these
	// callers present a shared secret rather than a session, so running them
	// through Auth and Casbin would reject every one. Registering them after
	// the chain is the mistake this ordering exists to prevent.
	controlroutes.RegisterInternal(r, ctl.deploy, ctl.apikey, ctl.tls, ctlCfg.WebhookSecret)

	r.Use(gin.Recovery())
	r.Use(middleware.CORS())
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger(log))

	// The panel, before authentication and before every API route. It only
	// answers GET and HEAD for paths outside the API prefixes, so nothing below
	// is shadowed — and serving the login page cannot require a session.
	//
	// A binary built without running the frontend build carries no panel. That
	// is a supported state rather than a broken one: the API is unaffected, and
	// this says so once at startup instead of refusing to start.
	switch assets, err := panel.Assets(); {
	case err == nil:
		r.Use(panel.Middleware(assets))
		log.Info("panel served from this binary")
	case errors.Is(err, panel.ErrNotBuilt):
		log.Warn("no panel in this binary; the API is unaffected", zap.Error(err))
	default:
		return nil, err
	}

	r.Use(middleware.Auth(sessions, &wiring.KeyVerifier{Repo: ctl.apikeyRepo, Log: log}))
	r.Use(middleware.Casbin(enforcer))
	r.Use(middleware.Enrich())

	// Bound the archive body before the handler reads it. This used to live in
	// the gateway because the body was about to cross a proxy; it stays because
	// the limit is real either way.
	r.Use(uploadBodyLimit(int64(gwCfg.MaxUploadSizeMB) * 1024 * 1024))

	// The application routes, registered directly rather than proxied. They
	// read X-User-ID from the request headers Enrich just wrote, so the
	// contract between the two halves is unchanged.
	controlroutes.Register(r, ctl.auth, ctl.deploy, ctl.apikey, ctl.domain, ctl.admin)
	aiHandler.Register(r.Group("/api/v1/ai"))

	return r, nil
}

// uploadBodyLimit rejects an oversized Content-Length outright and truncates a
// chunked or lying body mid-read.
func uploadBodyLimit(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodPost && c.Request.URL.Path == "/api/v1/deploys/upload" {
			if c.Request.ContentLength > maxBytes {
				c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{
					"error":   "upload_too_large",
					"message": "archive exceeds the maximum upload size",
				})
				return
			}
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}

// ---------------------------------------------------------------------------
// background
// ---------------------------------------------------------------------------

func startBackground(
	ctx context.Context,
	ctlCfg *controlconfig.Config,
	rtCfg *runtimeconfig.Config,
	bldCfg *builderconfig.Config,
	ctl controlParts,
	bld builderParts,
	rt runtimeParts,
	uploadsStore *uploads.Store,
	credsStore *gitcreds.Store,
	log *zap.Logger,
) {
	go ctl.verifier.Run(ctx)

	// Expiry sweepers. Redis freed a key when its TTL passed; a directory and
	// a map need someone to do it, and an expired credential sitting in memory
	// is a secret with no reason to still be there.
	go uploadsStore.Run(ctx, time.Minute)
	go credsStore.Run(ctx, time.Minute)

	if ctlCfg.SagaWorkerEnabled {
		worker := &saga.Worker{
			Orchestrator:   ctl.orch,
			Queue:          ctl.sagaQueue,
			Repo:           ctl.sagaRepo,
			Log:            log,
			ResumeInterval: time.Duration(ctlCfg.SagaResumeIntervalSec) * time.Second,
		}
		go func() {
			if err := worker.Run(ctx); err != nil && ctx.Err() == nil {
				log.Error("saga worker exited", zap.Error(err))
			}
		}()
	}

	go watchdog.NewWatchdog(rt.service, rt.billing, rtCfg, log).Run(ctx)

	go runBuildWorker(ctx, bld, bldCfg, log)
}

// runBuildWorker consumes build jobs. The failure policy is the one the
// separate worker process had: a failed pipeline finalises the deploy as
// failed rather than being retried, because a retry budget is still Task 5b of
// the inherited backlog.
func runBuildWorker(ctx context.Context, bld builderParts, cfg *builderconfig.Config, log *zap.Logger) {
	_ = cfg
	err := bld.queue.Consume(ctx, func(job builderqueue.Job) error {
		log.Info("processing build job",
			zap.String("deploy_id", job.DeployID),
			zap.String("user_id", job.UserID),
		)
		result, err := bld.runner.Run(ctx, job)
		if err != nil {
			log.Error("build pipeline failed",
				zap.String("deploy_id", job.DeployID),
				zap.Bool("transient", builderpipeline.IsTransient(err)),
				zap.Bool("permanent", builderpipeline.IsPermanent(err)),
				zap.Error(err),
			)
			bld.runner.FinalizeAsFailed(ctx, job.DeployID, err)
			return nil
		}
		log.Info("build pipeline completed",
			zap.String("deploy_id", job.DeployID),
			zap.String("image_ref", result.ImageRef),
		)
		bld.runner.FinalizeAsSucceeded(ctx, job.DeployID, result)
		return nil
	})
	if err != nil && ctx.Err() == nil {
		log.Error("build consumer exited unexpectedly", zap.Error(err))
	}
}

// ---------------------------------------------------------------------------
// memory
// ---------------------------------------------------------------------------

// applyMemoryLimit derives GOMEMLIMIT from the cgroup and sets it.
//
// It is deliberately not fatal on any path. A platform that refuses to start
// because it could not read a limit file is worse than one running without a
// soft ceiling, and the two interesting outcomes — no limit, or a limit too
// small for what this container runs — are both worth saying out loud rather
// than dying over.
//
// An explicit GOMEMLIMIT in the environment wins. The runtime has already
// applied it by the time this runs, and an operator who set one by hand is
// making a decision this should not quietly overrule.
func applyMemoryLimit(log *zap.Logger) {
	if os.Getenv("GOMEMLIMIT") != "" {
		log.Info("GOMEMLIMIT set in the environment; leaving it alone",
			zap.String("gomemlimit", os.Getenv("GOMEMLIMIT")))
		return
	}

	reserve := int64(memlimit.DefaultReserve)
	if raw := os.Getenv("GOMEMLIMIT_RESERVE_MB"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			log.Warn("GOMEMLIMIT_RESERVE_MB is not a non-negative integer; using the default",
				zap.String("value", raw))
		} else {
			reserve = parsed << 20
		}
	}

	cgroupLimit, err := memlimit.Detect("/sys/fs/cgroup")
	if err != nil {
		// Outside a container, or a container started without a limit. Not a
		// problem in itself; it is only worth knowing when someone is trying
		// to work out why the heap grew.
		log.Info("no cgroup memory limit found; GOMEMLIMIT left unset", zap.Error(err))
		return
	}

	derived, err := memlimit.Derive(cgroupLimit, reserve)
	if err != nil {
		log.Warn("container memory limit is too small to derive a GOMEMLIMIT from; leaving it unset",
			zap.Int64("cgroup_limit_bytes", cgroupLimit),
			zap.Int64("reserve_bytes", reserve),
			zap.Error(err),
		)
		return
	}

	debug.SetMemoryLimit(derived)
	log.Info("GOMEMLIMIT derived from the container limit",
		zap.Int64("cgroup_limit_bytes", cgroupLimit),
		zap.Int64("reserve_bytes", reserve),
		zap.Int64("gomemlimit_bytes", derived),
	)
}
