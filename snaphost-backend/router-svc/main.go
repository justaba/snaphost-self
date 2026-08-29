package main

import (
	"context"
	"net/http"
	"time"

	ycsdk "github.com/yandex-cloud/go-sdk"
	"go.uber.org/zap"

	"snaphost/router-svc/config"
	"snaphost/router-svc/internal/router"
	"snaphost/shared/yandexauth"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}

	log, err := zap.NewProduction()
	if err != nil {
		panic(err)
	}
	defer log.Sync() //nolint:errcheck

	ctx := context.Background()
	var sdk *ycsdk.SDK
	if cfg.YandexAuthMode == "metadata" {
		sdk, err = yandexauth.NewSDKFromMetadata(ctx)
	} else {
		sdk, err = yandexauth.NewSDK(ctx, cfg.YandexSAKeyPath)
	}
	if err != nil {
		log.Fatal("failed to build Yandex SDK", zap.Error(err))
	}

	httpClient := &http.Client{Timeout: cfg.ProxyTimeout}
	lookup := &router.LookupClient{
		BaseURL:    cfg.UserBillingURL,
		Secret:     cfg.WebhookSecret,
		HTTPClient: httpClient,
	}
	proxy := &router.Proxy{
		DomainSuffix: cfg.DomainSuffix,
		Lookup:       lookup,
		Resolver:     &router.YandexResolver{SDK: sdk},
		Tokens:       &router.CachedTokenSource{SDK: sdk, TTL: cfg.TokenCacheTTL},
		Client:       httpClient,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	// On-demand TLS gate for the custom-domain edge (ADR 0007). Only the
	// on-VDS instance serves this: it is published on loopback, so the
	// unauthenticated surface never leaves the host. The Yandex instance of
	// this same image sits behind a public API Gateway and must not register
	// it, which is why the default is off.
	if cfg.TLSAskEnabled {
		mux.Handle("/internal/tls/authorize", &router.TLSAskHandler{
			Client: lookup,
			Suffix: cfg.DomainSuffix,
		})
		log.Info("on-demand TLS ask endpoint enabled")
	}
	mux.Handle("/", proxy)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Info("starting router-svc", zap.String("port", cfg.Port))
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal("router-svc failed", zap.Error(err))
	}
}
