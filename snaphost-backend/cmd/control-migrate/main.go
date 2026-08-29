// Command control-migrate applies the schema and exits.
//
// The service migrates itself at startup, so this exists for the one case that
// needs the schema applied as its own step: a deployment that wants the
// migration to succeed or fail before the thing serving traffic is allowed to
// start.
package main

import (
	"context"
	"log"
	"os"

	"go.uber.org/zap"

	"snaphost/internal/control/db"
)

func main() {
	path := os.Getenv("DATABASE_PATH")
	if path == "" {
		path = "/var/snaphost/data/snaphost.db"
	}

	logger, err := zap.NewProduction()
	if err != nil {
		log.Fatal("initialize logger")
	}
	defer logger.Sync() //nolint:errcheck

	handle, err := db.Open(context.Background(), path)
	if err != nil {
		logger.Fatal("database open failed", zap.Error(err))
	}
	defer handle.Close()

	if err := db.RunMigrations(handle, logger); err != nil {
		logger.Fatal("migrations failed", zap.Error(err))
	}
}
