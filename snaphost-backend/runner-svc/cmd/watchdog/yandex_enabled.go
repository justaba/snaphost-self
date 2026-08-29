//go:build yandex
// +build yandex

package main

import (
	"go.uber.org/zap"

	"snaphost/runner-svc/config"
	"snaphost/runner-svc/internal/backend"
	"snaphost/runner-svc/internal/backend/yandex"
	"snaphost/runner-svc/internal/logs"
)

func newYandexBackend(cfg *config.Config, pub logs.Publisher, log *zap.Logger) (backend.Backend, error) {
	yb, err := yandex.NewYandexBackend(cfg, pub, log)
	if err != nil {
		return nil, err
	}
	yandex.SetMemoryAccessor(func(*yandex.YandexBackend) int64 { return cfg.ContainerMemoryMB })
	return yb, nil
}
