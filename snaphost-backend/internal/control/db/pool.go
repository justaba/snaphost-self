package db

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"time"

	// modernc.org/sqlite is a pure-Go translation of SQLite. The cgo driver
	// (mattn/go-sqlite3) is faster, and unusable here: every image in this
	// repository builds with CGO_ENABLED=0, and a static binary is most of
	// what makes the runtime image small.
	_ "modernc.org/sqlite"
)

// Open returns a handle to the SQLite database at path, creating it if it does
// not exist, with the pragmas this workload needs.
//
// The pool is deliberately one writer and several readers rather than the
// twenty-five connections PostgreSQL was given. SQLite serialises writes at the
// file level, so a larger writer pool does not buy throughput — it buys
// SQLITE_BUSY errors under concurrency that WAL would otherwise have absorbed.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	if path == "" {
		return nil, fmt.Errorf("database path is required")
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := ensureDir(dir); err != nil {
			return nil, err
		}
	}

	// _pragma parameters are applied per connection, which matters: SQLite
	// pragmas are connection-scoped, so setting them once after Open would
	// leave every other pooled connection on the defaults.
	//
	//   journal_mode=WAL   readers do not block the writer, and the reverse
	//   busy_timeout=5000  wait rather than fail when the writer holds the lock
	//   foreign_keys=ON    off by default in SQLite; the schema declares them
	//   synchronous=NORMAL safe under WAL, and avoids an fsync per commit
	dsn := path + "?_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(ON)" +
		"&_pragma=synchronous(NORMAL)"

	handle, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	handle.SetMaxOpenConns(8)
	handle.SetMaxIdleConns(4)
	handle.SetConnMaxLifetime(time.Hour)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := handle.PingContext(pingCtx); err != nil {
		handle.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return handle, nil
}
