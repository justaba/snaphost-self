package db

import (
	"embed"
	"errors"
	"fmt"
	"net/url"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrationsTable isolates ai-orchestrator's migration ledger from other
// services that share the same Postgres database (e.g. user-billing).
const migrationsTable = "ai_orchestrator_schema_migrations"

// Migrate runs pending database migrations.
func Migrate(databaseURL string) error {
	srcDriver, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("failed to create migration source: %w", err)
	}

	migrateURL, err := withMigrationsTable(databaseURL, migrationsTable)
	if err != nil {
		return fmt.Errorf("failed to build migrate URL: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", srcDriver, migrateURL)
	if err != nil {
		return fmt.Errorf("failed to initialize migrate: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	return nil
}

func withMigrationsTable(databaseURL, table string) (string, error) {
	u, err := url.Parse(databaseURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("x-migrations-table", table)
	u.RawQuery = q.Encode()
	return u.String(), nil
}
