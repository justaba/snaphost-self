// Package main is the entry point for the control-plane service. It owns
// accounts, projects, deploys, and custom domains, and runs the saga
// orchestrator worker.
//
// The directory is still called user-billing and there is no billing in it:
// renaming it is deferred to the module merge, where every import path
// changes once instead of twice.
package main

import (
	"context"
	"errors"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"snaphost/user-billing/config"
	"snaphost/user-billing/db"
	"snaphost/user-billing/internal/account"
	"snaphost/user-billing/internal/admin"
	"snaphost/user-billing/internal/apikey"
	"snaphost/user-billing/internal/deploy"
	"snaphost/user-billing/internal/domain"
	"snaphost/user-billing/internal/gitcred"
	"snaphost/user-billing/internal/logs"
	"snaphost/user-billing/internal/project"
	"snaphost/user-billing/internal/saga"
	"snaphost/user-billing/internal/upload"
	"snaphost/user-billing/routes"
)

func main() {
	// 1. Load configuration.
	cfg, err := config.Load()
	if err != nil {
		panic("failed to load config: " + err.Error())
	}

	// 2. Initialize logger.
	var log *zap.Logger
	if cfg.LogLevel == "debug" {
		log, err = zap.NewDevelopment()
	} else {
		log, err = zap.NewProduction()
	}
	if err != nil {
		panic("failed to create logger: " + err.Error())
	}
	defer log.Sync() //nolint:errcheck

	// 3. Run embedded migrations (must succeed before pool opens).
	if cfg.RunMigrations {
		log.Info("running database migrations")
		if err := db.RunMigrations(cfg.DatabaseURL, log); err != nil {
			log.Fatal("migrations failed", zap.Error(err))
		}
	}

	// 4. Create context for graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 5. Open database connection pool.
	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal("failed to create database pool", zap.Error(err))
	}
	defer pool.Close()

	// 6. Optionally connect to Redis (enables saga worker + log publisher + log history reader).
	var (
		rdb         *redis.Client
		sagaQueue   *saga.Queue
		sagaRepo    *saga.Repository
		sagaPub     logs.Publisher
		logReader   *logs.Reader
		uploadStore *upload.Store
		credStore   *gitcred.Store
	)
	if cfg.RedisURL != "" {
		opts, err := redis.ParseURL(cfg.RedisURL)
		if err != nil {
			log.Fatal("failed to parse REDIS_URL", zap.Error(err))
		}
		rdb = redis.NewClient(opts)
		defer rdb.Close()

		pingCtx, pingCancel := context.WithTimeout(ctx, 5*time.Second)
		if err := rdb.Ping(pingCtx).Err(); err != nil {
			pingCancel()
			log.Fatal("failed to ping redis", zap.Error(err))
		}
		pingCancel()

		sagaQueue = saga.NewQueue(rdb, log)
		sagaRepo = saga.NewRepository(pool)
		sagaPub = logs.NewRedisPublisher(rdb, log)
		logReader = logs.NewReader(rdb)
		uploadStore = upload.NewStore(rdb)
		credStore = gitcred.NewStore(rdb)
	} else {
		log.Warn("REDIS_URL not set; saga worker and saga-driven deploys will be disabled")
	}

	// 7. Wire repositories.
	accountRepo := account.NewRepository(pool)
	deployRepo := deploy.NewRepository(pool,
		deploy.WithDomainSuffix(cfg.DomainSuffix),
		deploy.WithGCPolicy(deploy.GCPolicy{
			AliasIdleDays:  cfg.AliasIdleGCDays,
			KeepPerProject: cfg.ProjectDeployRetention,
		}),
	)
	apikeyRepo := apikey.NewRepository(pool)
	projectRepo := project.NewRepository(pool)
	domainRepo := domain.NewRepository(pool)
	adminRepo := admin.NewRepository(pool)

	// 8. Wire services.
	runnerClient := saga.NewHTTPRunnerClient(cfg.RunnerSvcURL, cfg.WebhookSecret)

	// 9. Wire HTTP handlers.
	accountHandler := account.NewHandler(accountRepo, log)
	deployHandler := deploy.NewHandler(deployRepo, projectRepo, log, sagaQueue, runnerClient, logReader,
		uploadStore, int64(cfg.MaxUploadSizeMB)*1024*1024, time.Duration(cfg.UploadTTLMin)*time.Minute,
		credStore, time.Duration(cfg.GitCredTTLMin)*time.Minute)
	apikeyHandler := apikey.NewHandler(apikeyRepo, log)
	adminHandler := admin.NewHandler(adminRepo, log)

	var attachLimiter domain.Limiter
	if rdb != nil {
		attachLimiter = domain.NewRedisLimiter(rdb, cfg.DomainAttachPerHour, time.Hour)
	}
	domainHandler := domain.NewHandler(domainRepo, attachLimiter, domain.Config{
		PlatformSuffix:  cfg.DomainSuffix,
		ReservedDomains: cfg.ReservedDomains,
		MaxPerUser:      cfg.MaxDomainsPerUser,
		RequireIdentity: cfg.DomainAttachRequireIdentity,
		CNAMETarget:     cfg.DomainCNAMETarget,
		ARecordTarget:   cfg.DomainARecordTarget,
	}, log)
	// Gate for the custom-domain TLS edge (ADR 0007): it authorises an ACME
	// issuance only for a hostname whose ownership has been proven.
	tlsHandler := domain.NewTLSHandler(domainRepo, log)

	// Ownership verification. Without it a domain never leaves pending, so
	// nothing routes — which is the intended failure mode.
	domainVerifier := domain.NewVerifier(domainRepo, log,
		time.Duration(cfg.DomainVerifyIntervalSec)*time.Second,
		time.Duration(cfg.DomainReverifyHours)*time.Hour,
		time.Duration(cfg.DomainVerifyGraceHours)*time.Hour,
		50)
	go domainVerifier.Run(ctx)

	// 10. Optionally wire and start the saga worker in-process.
	if cfg.SagaWorkerEnabled && rdb != nil {
		orch := &saga.Orchestrator{
			Repo: sagaRepo,

			DeployRepo:       deployRepo,
			Builder:          saga.NewHTTPBuilderClient(cfg.BuilderSvcURL, cfg.WebhookSecret),
			Runner:           runnerClient,
			Redis:            rdb,
			Publisher:        sagaPub,
			Log:              log,
			BuildTimeout:     time.Duration(cfg.SagaBuildTimeoutMin) * time.Minute,
			DeployPort:       cfg.DeployDefaultPort,
			DeployTTLMinutes: cfg.DeployTTLMin,
			MaxTTLMinutes:    cfg.DeployTTLMaxMin,
			// Publishing after a successful deploy (Task 16a item 4).
			Aliases: domainRepo,
		}
		worker := &saga.Worker{
			Orchestrator:   orch,
			Queue:          sagaQueue,
			Repo:           sagaRepo,
			Log:            log,
			ResumeInterval: time.Duration(cfg.SagaResumeIntervalSec) * time.Second,
		}
		go func() {
			if err := worker.Run(ctx); err != nil {
				log.Error("saga worker exited", zap.Error(err))
			}
		}()
	}

	// 11. Create Gin engine (recovery only — auth is handled by api-gateway).
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	// 12. Health check and metrics.
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"service": "user-billing",
		})
	})
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// 13. Register application routes.
	routes.Register(r, accountHandler, deployHandler, apikeyHandler, domainHandler, tlsHandler, adminHandler, cfg.WebhookSecret)

	// 14. Start HTTP server with graceful shutdown.
	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: r,
	}

	go func() {
		log.Info("starting user-billing service", zap.String("port", cfg.Port))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("server failed", zap.Error(err))
		}
	}()

	// Block until shutdown signal.
	<-ctx.Done()
	log.Info("shutting down gracefully")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("forced shutdown", zap.Error(err))
	}

	log.Info("user-billing service stopped")
}
