package db

import (
	"fmt"
	"time"
)

// Timestamps are stored as TEXT, and database/sql will not put a string into a
// time.Time. google/uuid implements sql.Scanner so ids need nothing; times do.
//
// The two helpers below scan straight into an existing struct field rather than
// through a local variable, so a repository keeps its `time.Time` fields and
// gains one wrapper per scan target instead of a second copy of every row
// struct.

// Into scans a non-null timestamp column into dst.
func Into(dst *time.Time) *timeScanner { return &timeScanner{dst: dst} }

type timeScanner struct{ dst *time.Time }

func (s *timeScanner) Scan(src any) error {
	if src == nil {
		*s.dst = time.Time{}
		return nil
	}
	text, err := asString(src)
	if err != nil {
		return err
	}
	parsed, err := ParseTime(text)
	if err != nil {
		return fmt.Errorf("parse timestamp %q: %w", text, err)
	}
	*s.dst = parsed
	return nil
}

// IntoNull scans a nullable timestamp column into dst, leaving it nil for NULL.
func IntoNull(dst **time.Time) *nullTimeScanner { return &nullTimeScanner{dst: dst} }

type nullTimeScanner struct{ dst **time.Time }

func (s *nullTimeScanner) Scan(src any) error {
	if src == nil {
		*s.dst = nil
		return nil
	}
	text, err := asString(src)
	if err != nil {
		return err
	}
	parsed, err := ParseTime(text)
	if err != nil {
		return fmt.Errorf("parse timestamp %q: %w", text, err)
	}
	*s.dst = &parsed
	return nil
}

func asString(src any) (string, error) {
	switch v := src.(type) {
	case string:
		return v, nil
	case []byte:
		return string(v), nil
	default:
		return "", fmt.Errorf("cannot read timestamp from %T", src)
	}
}
