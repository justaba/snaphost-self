package cache

// cacheSchemaVersion is mixed into ComputeSignature so that cache entries
// from older versions of the matcher/detector/template logic become
// unreachable when this constant is bumped. Old rows are eventually purged
// by DeleteExpired (7-day TTL) without explicit migration.
//
// BUMP THIS when ANY of the following change in a way that affects the
// generated Dockerfile:
//
//   - internal/ai/detector/*       (signal extraction)
//   - internal/ai/templates/*      (matcher logic OR .tmpl files)
//   - internal/ai/llm/prompt.go    (prompt / few-shot changes)
//
// History:
//
//	v1  initial scheme (Task 9 follow-up).
//	    Captures every prior change implicitly — bumping to v1 invalidates
//	    every pre-existing cache row, including stale Task-9 misses.
const cacheSchemaVersion = "v2"
