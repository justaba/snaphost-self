package db

import (
	"fmt"
	"os"
)

// ensureDir creates the database's parent directory. A fresh install points
// DATABASE_PATH at a volume that exists but is empty, and a first run that
// fails on a missing directory is a confusing way to say "nothing is wrong
// yet".
func ensureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create database directory %s: %w", dir, err)
	}
	return nil
}
