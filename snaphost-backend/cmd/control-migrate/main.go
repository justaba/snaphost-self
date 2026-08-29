package main

import (
	"log"
	"os"

	"go.uber.org/zap"

	"snaphost/internal/control/db"
)

func main() {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}
	logger, err := zap.NewProduction()
	if err != nil {
		log.Fatal("initialize logger")
	}
	defer logger.Sync() //nolint:errcheck

	if err := db.RunMigrations(databaseURL, logger); err != nil {
		logger.Fatal("migrations failed", zap.Error(err))
	}
}
