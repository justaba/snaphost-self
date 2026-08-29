// Package db provides database connection pooling and embedded migration support.
package db

import (
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"go.uber.org/zap"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// RunMigrations applies all pending database migrations embedded in the binary.
// It logs the current and target versions and returns an error if the database
// is in a dirty state or if migration application fails.
func RunMigrations(databaseURL string, log *zap.Logger) error {
	sourceDriver, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("create migration source: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", sourceDriver, databaseURL)
	if err != nil {
		return fmt.Errorf("create migrator: %w", err)
	}
	defer func() {
		sourceErr, dbErr := m.Close()
		if sourceErr != nil {
			log.Warn("failed to close migration source", zap.Error(sourceErr))
		}
		if dbErr != nil {
			log.Warn("failed to close migration db connection", zap.Error(dbErr))
		}
	}()

	version, dirty, err := m.Version()
	if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		return fmt.Errorf("check migration version: %w", err)
	}
	if errors.Is(err, migrate.ErrNilVersion) {
		version = 0
	}

	if dirty {
		return fmt.Errorf("database is in dirty state at version %d — manual intervention required (use `migrate force`)", version)
	}

	log.Info("running migrations", zap.Uint("current_version", uint(version)))

	if err := m.Up(); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			log.Info("no new migrations to apply")
			return nil
		}
		return fmt.Errorf("apply migrations: %w", err)
	}

	newVersion, _, err := m.Version()
	if err != nil {
		return fmt.Errorf("check new migration version: %w", err)
	}

	log.Info("migrations applied successfully",
		zap.Uint("from", uint(version)),
		zap.Uint("to", uint(newVersion)),
	)

	return nil
}
