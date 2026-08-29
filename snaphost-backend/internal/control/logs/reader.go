package logs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// Reader fetches buffered log history from Redis Streams. It is the
// counterpart to RedisPublisher's XADD — every line that goes to
// logs-history:{deploy_id} is recoverable here for catch-up reads.
type Reader struct {
	rdb *redis.Client
}

// NewReader constructs a Reader over the given Redis client.
func NewReader(rdb *redis.Client) *Reader {
	return &Reader{rdb: rdb}
}

// HistoryEntry is one stream entry annotated with its Redis Stream ID.
// The ID can be passed back as the `since` cursor on the next request.
type HistoryEntry struct {
	ID   string  `json:"id"`
	Line LogLine `json:"line"`
}

// Read returns up to limit entries from logs-history:{deploy_id} starting
// strictly after the given since cursor. Pass since="" (or "0") for the
// full history.
func (r *Reader) Read(ctx context.Context, deployID string, since string, limit int) ([]HistoryEntry, string, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	stream := "logs-history:" + deployID
	start := exclusiveStart(since)

	msgs, err := r.rdb.XRangeN(ctx, stream, start, "+", int64(limit)).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, "", nil
		}
		return nil, "", fmt.Errorf("xrange %s: %w", stream, err)
	}

	out := make([]HistoryEntry, 0, len(msgs))
	for _, m := range msgs {
		raw, ok := m.Values["line"].(string)
		if !ok {
			continue
		}
		var ll LogLine
		if err := json.Unmarshal([]byte(raw), &ll); err != nil {
			continue
		}
		out = append(out, HistoryEntry{ID: m.ID, Line: ll})
	}
	next := ""
	if n := len(out); n > 0 {
		next = out[n-1].ID
	}
	return out, next, nil
}

// exclusiveStart turns a "last seen ID" cursor into the XRANGE start
// argument for "strictly after". An empty cursor yields "-" (full history).
func exclusiveStart(since string) string {
	if since == "" || since == "-" || since == "0" || since == "0-0" {
		return "-"
	}
	// Redis Stream supports "(" prefix to mean exclusive lower bound.
	return "(" + since
}
