package admin

import "github.com/google/uuid"

// SQLite's placeholders are positional, so a filter that mentions the same
// value more than once needs that value bound once per mention. PostgreSQL's
// $1 could be reused, and losing that means the argument list stops being
// obvious — hence these builders, which keep each filter's SQL and its bindings
// defined next to each other.
//
// Getting one of these wrong shifts every later argument by a position, which
// is a class of bug that produces wrong results rather than an error.

// userSearchArgs binds userSearch, which mentions the query three times.
func userSearchArgs(query string) []any {
	return []any{query, query, query}
}

// deployFilterArgs binds deployFilter: status twice, the user id twice, and the
// free-text query five times.
func deployFilterArgs(status string, userID *uuid.UUID, query string) []any {
	id := nullableID(userID)
	return []any{status, status, id, id, query, query, query, query, query}
}

// domainFilterArgs binds the domain filter: status twice, the user id twice,
// and the query three times.
func domainFilterArgs(status string, userID *uuid.UUID, query string) []any {
	id := nullableID(userID)
	return []any{status, status, id, id, query, query, query}
}

// nullableID renders an optional filter id, which has to reach the driver as an
// untyped nil to make `? IS NULL` true.
func nullableID(id *uuid.UUID) any {
	if id == nil {
		return nil
	}
	return id.String()
}

// withPage appends the LIMIT and OFFSET bindings after a filter's own.
func withPage(args []any, limit, offset int) []any {
	return append(append([]any{}, args...), limit, offset)
}
