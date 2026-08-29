//go:build !yandex
// +build !yandex

package main

import (
	"errors"

	"go.uber.org/zap"

	"snaphost/runner-svc/config"
	"snaphost/runner-svc/internal/backend"
	"snaphost/runner-svc/internal/logs"
)

func newYandexBackend(_ *config.Config, _ logs.Publisher, _ *zap.Logger) (backend.Backend, error) {
	return nil, errors.New("yandex backend not compiled in: rebuild with -tags yandex")
}
