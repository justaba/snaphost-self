// Command snaphost is the whole platform in one process.
//
// It was seven. On a single-operator host the microservice split bought
// nothing and cost a Go runtime, a health check and a JSON round trip per
// component; the seams it existed to protect are now interfaces satisfied by
// direct calls (internal/wiring).
//
// Startup order matters and is deliberate:
//
//  1. configuration for every area, so a missing variable fails before any
//     connection is opened;
//  1. configuration for every area, so a missing variable fails before any
//     connection is opened;
//  2. the store, opened and migrated on one handle;
//  3. one Redis client, shared by everything;
//  4. components, then the adapters that join them;
//  5. one HTTP engine;
//  6. background loops last, so nothing starts working before the thing it
//     reports to exists.
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"database/sql"

	"github.com/casbin/casbin/v2"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
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
	builderevents "snaphost/internal/builder/events"
	buildergitcred "snaphost/internal/builder/gitcred"
	builderlogs "snaphost/internal/builder/logs"
	builderpipeline "snaphost/internal/builder/pipeline"
	builderqueue "snaphost/internal/builder/queue"
	builderregistry "snaphost/internal/builder/registry"
	builderscan "snaphost/internal/builder/scan"
	builderunpack "snaphost/internal/builder/unpack"
	builderupload "snaphost/internal/builder/upload"
	"snaphost/internal/control/account"
	"snaphost/internal/control/admin"
	"snaphost/internal/control/apikey"
	controlconfig "snaphost/internal/control/config"
	controldb "snaphost/internal/control/db"
	"snaphost/internal/control/deploy"
	"snaphost/internal/control/domain"
	"snaphost/internal/control/gitcred"
	"snaphost/internal/control/logs"
	"snaphost/internal/control/project"
	controlroutes "snaphost/internal/control/routes"
	"snaphost/internal/control/saga"
	"snaphost/internal/control/upload"
	gatewayconfig "snaphost/internal/gateway/config"
	"snaphost/internal/gateway/middleware"
	"snaphost/internal/gateway/webhooks"
	"snaphost/internal/gateway/wslogs"
	runtimebackend "snaphost/internal/runtime/backend"
	runtimedocker "snaphost/internal/runtime/backend/docker"
	runtimeconfig "snaphost/internal/runtime/config"
	runtimelogs "snaphost/internal/runtime/logs"
	"snaphost/internal/runtime/runner"
	"snaphost/internal/runtime/watchdog"
	"snaphost/internal/wiring"
)

func main() {
	log, err := zap.NewProduction()
	if err != nil {
		panic("failed to create logger: " + err.Error())
	}
	defer log.Sync() //nolint:errcheck // flushing on exit; nothing left to report to

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

	rdb, err := newRedis(ctx, ctlCfg.RedisURL)
	if err != nil {
		log.Fatal("redis connection failed", zap.Error(err))
	}
	defer rdb.Close()

	// 4. Components, bottom up.
	ai := buildAI(pool, aiCfg, log)
	rt := buildRuntime(pool, rtCfg, rdb, log)
	bld := buildBuilder(pool, bldCfg, aiCfg, ai, rdb, log)
	ctl := buildControl(pool, ctlCfg, bld.enqueuer, rt.service, rdb, log)

	// 5. One engine. The middleware order is the gateway's, unchanged and
	//    load-bearing: rate limiting before authentication so an IP limit
	//    applies to unauthenticated traffic too, and Enrich last so the
	//    identity headers downstream handlers read are written from a verified
	//    token and deleted when there is none.
	engine, err := buildEngine(gwCfg, ctlCfg, ctl, ai.handler, rdb, log)
	if err != nil {
		log.Fatal("failed to build HTTP engine", zap.Error(err))
	}

	// 6. Background loops, after everything they touch exists.
	startBackground(ctx, ctlCfg, rtCfg, bldCfg, ctl, bld, rt, log)

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

// newRedis connects and verifies the connection. Unlike the split services,
// Redis is not optional here: the build queue, the deploy log stream and the
// saga all need it, and a process that silently disables half of itself is
// worse than one that refuses to start. Item 7 of Task 1 removes the
// dependency entirely.
func newRedis(ctx context.Context, url string) (*redis.Client, error) {
	if url == "" {
		return nil, errors.New("REDIS_URL is required")
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	client := redis.NewClient(opts)
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		client.Close()
		return nil, err
	}
	return client, nil
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
}

func buildRuntime(pool *sql.DB, cfg *runtimeconfig.Config, rdb *redis.Client, log *zap.Logger) runtimeParts {
	publisher := runtimelogs.NewRedisPublisher(rdb, log)

	// The runtime used to reach the control plane over HTTP to check
	// ownership and record state. Same checks, no hop.
	billingClient := &wiring.BillingClient{Repo: deployRepo(pool, nil)}

	var b runtimebackend.Backend
	switch cfg.RunnerBackend {
	case "docker":
		var err error
		b, err = runtimedocker.NewDockerBackend(cfg, publisher, log)
		if err != nil {
			log.Fatal("failed to initialise docker backend", zap.Error(err))
		}
	default:
		log.Fatal("unknown RUNNER_BACKEND value", zap.String("value", cfg.RunnerBackend))
	}
	log.Info("runtime backend selected", zap.String("backend", b.Name()))

	if !cfg.StrictImageValidation && len(cfg.AllowedRegistryPrefixes) == 0 {
		log.Warn("STRICT_IMAGE_VALIDATION=false and REGISTRY_ALLOWED_PREFIXES empty: any image_ref will be accepted (dev-only safe configuration)")
	}

	return runtimeParts{
		service: runner.NewService(b, billingClient, publisher, cfg, log),
		billing: billingClient,
	}
}

type builderParts struct {
	enqueuer *builderapi.Enqueuer
	queue    *builderqueue.Queue
	runner   *builderpipeline.Runner
}

func buildBuilder(pool *sql.DB, cfg *builderconfig.Config, aiCfg *aiconfig.Config, ai aiParts, rdb *redis.Client, log *zap.Logger) builderParts {
	_ = aiCfg

	q := builderqueue.NewQueue(rdb, log)
	if err := q.EnsureGroup(context.Background()); err != nil {
		log.Fatal("failed to ensure build consumer group", zap.Error(err))
	}

	pub := builderlogs.NewRedisPublisher(rdb, log)
	eventsPub := builderevents.NewRedisEventPublisher(rdb, log)

	dockerConfigDir := os.Getenv("DOCKER_CONFIG")
	if dockerConfigDir == "" {
		dockerConfigDir = os.ExpandEnv("$HOME/.docker")
	}
	builder, err := builderbuild.NewBuilder(cfg.BuildKitHost, dockerConfigDir, cfg, pub, log)
	if err != nil {
		log.Fatal("failed to create buildkit builder", zap.Error(err))
	}

	var scanCredentials builderscan.CredentialsProvider
	if cfg.RegistryUsername != "" || cfg.RegistryPassword != "" {
		scanCredentials = func(context.Context) (string, string, error) {
			return cfg.RegistryUsername, cfg.RegistryPassword, nil
		}
	}
	scanner := builderscan.NewScanner(pub, log, cfg.RegistryInsecure, registryHost(cfg.RegistryURL), scanCredentials)

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
			Status:      &wiring.StatusReporter{Repo: deployRepo(pool, nil)},
			AIClient:    &wiring.AIClient{Service: ai.service},
			Registry:    builderregistry.NewDockerV2Client(log),
			Uploads:     builderupload.NewStore(rdb),
			Credentials: buildergitcred.NewStore(rdb),
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
	account    *account.Handler
	deploy     *deploy.Handler
	apikey     *apikey.Handler
	domain     *domain.Handler
	tls        *domain.TLSHandler
	admin      *admin.Handler
	accountRep *account.Repository
	apikeyRepo *apikey.Repository
}

func buildControl(
	pool *sql.DB,
	cfg *controlconfig.Config,
	enqueuer *builderapi.Enqueuer,
	runtimeSvc *runner.Service,
	rdb *redis.Client,
	log *zap.Logger,
) controlParts {
	accountRepo := account.NewRepository(pool)
	dRepo := deployRepo(pool, cfg)
	apikeyRepo := apikey.NewRepository(pool)
	projectRepo := project.NewRepository(pool)
	domainRepo := domain.NewRepository(pool)
	adminRepo := admin.NewRepository(pool)

	sagaQueue := saga.NewQueue(rdb, log)
	sagaRepo := saga.NewRepository(pool)
	sagaPub := logs.NewRedisPublisher(rdb, log)
	logReader := logs.NewReader(rdb)
	uploadStore := upload.NewStore(rdb)
	credStore := gitcred.NewStore(rdb)

	runnerClient := &wiring.RunnerClient{Service: runtimeSvc}

	deployHandler := deploy.NewHandler(dRepo, projectRepo, log, sagaQueue, runnerClient, logReader,
		uploadStore, int64(cfg.MaxUploadSizeMB)*1024*1024, time.Duration(cfg.UploadTTLMin)*time.Minute,
		credStore, time.Duration(cfg.GitCredTTLMin)*time.Minute)

	attachLimiter := domain.Limiter(domain.NewRedisLimiter(rdb, cfg.DomainAttachPerHour, time.Hour))
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
		Redis:            rdb,
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
		account:    account.NewHandler(accountRepo, log),
		deploy:     deployHandler,
		apikey:     apikey.NewHandler(apikeyRepo, log),
		domain:     domainHandler,
		tls:        domain.NewTLSHandler(domainRepo, log),
		admin:      admin.NewHandler(adminRepo, log),
		accountRep: accountRepo,
		apikeyRepo: apikeyRepo,
	}
}

// deployRepo builds the deploy repository. cfg may be nil for the runtime and
// builder sides, which only read and write rows by id and never consult the
// GC policy or the domain suffix.
func deployRepo(pool *sql.DB, cfg *controlconfig.Config) *deploy.Repository {
	if cfg == nil {
		return deploy.NewRepository(pool)
	}
	return deploy.NewRepository(pool,
		deploy.WithDomainSuffix(cfg.DomainSuffix),
		deploy.WithGCPolicy(deploy.GCPolicy{
			AliasIdleDays:  cfg.AliasIdleGCDays,
			KeepPerProject: cfg.ProjectDeployRetention,
		}),
	)
}

func registryHost(registryURL string) string {
	value := registryURL
	for _, prefix := range []string{"https://", "http://"} {
		if len(value) >= len(prefix) && value[:len(prefix)] == prefix {
			value = value[len(prefix):]
			break
		}
	}
	for i := 0; i < len(value); i++ {
		if value[i] == '/' {
			return value[:i]
		}
	}
	return value
}

// ---------------------------------------------------------------------------
// HTTP
// ---------------------------------------------------------------------------

func buildEngine(
	gwCfg *gatewayconfig.Config,
	ctlCfg *controlconfig.Config,
	ctl controlParts,
	aiHandler *aiapi.Handler,
	rdb *redis.Client,
	log *zap.Logger,
) (*gin.Engine, error) {
	enforcer, err := casbin.NewEnforcer(gwCfg.RBACModelPath, gwCfg.RBACPolicyPath)
	if err != nil {
		return nil, err
	}

	jwks := middleware.NewJWKSCache(gwCfg.SupabaseURL)
	fetchCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := jwks.Prefetch(fetchCtx); err != nil {
		return nil, err
	}

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()

	// Registered before all middleware, exactly as before: /health must answer
	// even when authentication is broken, and the WebSocket authenticates from
	// a query parameter because browsers cannot set headers on one.
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "service": "snaphost"})
	})
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))
	r.GET("/ws/logs/:id", wslogs.Handler(rdb, jwks, log))

	// The identity provider's signup webhook. It keeps the shared secret it
	// always had, because this caller genuinely is outside the process — and
	// it now records the account directly instead of posting to itself.
	webhooks.Register(r, webhooks.NewWebhookHandler(gwCfg, &wiring.AccountCreator{Repo: ctl.accountRep}, log))

	// Secret-authenticated routes, also before the user middleware: these
	// callers present a shared secret rather than a token, so running them
	// through JWT and Casbin would reject every one. Registering them after
	// the chain is the mistake this ordering exists to prevent.
	controlroutes.RegisterInternal(r, ctl.account, ctl.deploy, ctl.apikey, ctl.tls, ctlCfg.WebhookSecret)

	r.Use(gin.Recovery())
	r.Use(middleware.CORS())
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger(log))
	r.Use(middleware.JWT(gwCfg, jwks, &wiring.KeyVerifier{Repo: ctl.apikeyRepo, Log: log}))
	r.Use(middleware.RateLimit(rdb, gwCfg))
	r.Use(middleware.Casbin(enforcer))
	r.Use(middleware.Enrich())

	// Bound the archive body before the handler reads it. This used to live in
	// the gateway because the body was about to cross a proxy; it stays because
	// the limit is real either way.
	r.Use(uploadBodyLimit(int64(gwCfg.MaxUploadSizeMB) * 1024 * 1024))

	// The application routes, registered directly rather than proxied. They
	// read X-User-ID from the request headers Enrich just wrote, so the
	// contract between the two halves is unchanged.
	controlroutes.Register(r, ctl.deploy, ctl.apikey, ctl.domain, ctl.admin)
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
	log *zap.Logger,
) {
	go ctl.verifier.Run(ctx)

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

	consumer, _ := os.Hostname()
	if consumer == "" {
		consumer = "builder-1"
	}
	go runBuildWorker(ctx, bld, consumer, bldCfg, log)
}

// runBuildWorker consumes build jobs. The ack policy is the one the separate
// worker process had: returning nil acks the message, and a failed pipeline
// finalises the deploy as failed rather than being retried, because a retry
// budget is still Task 5b of the inherited backlog.
func runBuildWorker(ctx context.Context, bld builderParts, consumer string, cfg *builderconfig.Config, log *zap.Logger) {
	_ = cfg
	err := bld.queue.Consume(ctx, consumer, func(job builderqueue.Job) error {
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
