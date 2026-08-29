// Package db provides the SQLite store and embedded migration support.
package db

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"go.uber.org/zap"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// RunMigrations applies all pending migrations on the given handle.
//
// It takes an open *sql.DB rather than a URL for two reasons. The obvious one
// is that golang-migrate's sqlite:// URL is parsed with net/url, and a Windows
// path puts a colon after the drive letter, which that parser reads as a port.
// The better one is that a URL makes the migrator open a second connection of
// its own — without the pragmas Open sets, so foreign keys would be off while
// the schema that declares them was being created.
func RunMigrations(handle *sql.DB, log *zap.Logger) error {
	sourceDriver, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("create migration source: %w", err)
	}

	dbDriver, err := migratesqlite.WithInstance(handle, &migratesqlite.Config{})
	if err != nil {
		return fmt.Errorf("create migration driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", sourceDriver, "sqlite", dbDriver)
	if err != nil {
		return fmt.Errorf("create migrator: %w", err)
	}
	// Deliberately not m.Close(): that would close the handle the caller owns
	// and is about to serve every request from.

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
