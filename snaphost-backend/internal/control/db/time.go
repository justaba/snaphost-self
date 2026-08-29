package db

import "time"

// TimeLayout is how every timestamp in this database is written.
//
// SQLite has no timestamp type, so ordering and range comparisons are string
// operations. They are correct only because the format is fixed-width, RFC 3339
// and always UTC — a local time or a different precision written here sorts
// wrongly and reports nothing.
//
// The schema's column defaults use strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), which
// produces exactly this.
const TimeLayout = "2006-01-02T15:04:05.000Z"

// Now is the current time in the stored format.
func Now() string { return FormatTime(time.Now()) }

// FormatTime renders t for storage, converting to UTC first.
func FormatTime(t time.Time) string { return t.UTC().Format(TimeLayout) }

// ParseTime reads a stored timestamp.
func ParseTime(s string) (time.Time, error) { return time.Parse(TimeLayout, s) }

// NullTime renders an optional timestamp, returning nil for the zero value so
// the column stays NULL rather than becoming a year-1 date that sorts first.
func NullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return FormatTime(t)
}
