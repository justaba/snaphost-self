package logs

import (
	"context"
	"strconv"

	"github.com/google/uuid"

	"snaphost/internal/logbus"
)

// HistoryEntry is one history line with the cursor identifying it. The cursor
// is an opaque string the client passes back as `since`; it was a Redis stream
// id and is now a sequence number, and no caller is supposed to parse it.
type HistoryEntry struct {
	ID   string  `json:"id"`
	Line LogLine `json:"line"`
}

// Archive reads the log tail stored against a deploy that failed.
// *deploy.Repository satisfies it.
type Archive interface {
	LogTail(ctx context.Context, deployID uuid.UUID) ([]byte, error)
}

// Reader serves log history from two places, and which one it uses is not a
// fallback so much as a consequence of what each holds.
//
// While a deploy is live its lines are in memory, bounded, and that is the
// only complete copy. Once it has failed the last of them are in the database,
// which is what survives a restart. Reading the bus first is therefore reading
// the more complete source; the archive answers when the bus has forgotten.
type Reader struct {
	bus     *logbus.Bus
	archive Archive
}

// NewReader constructs a Reader. archive may be nil, in which case history
// stops being available once the bus has dropped a deploy.
func NewReader(bus *logbus.Bus, archive Archive) *Reader {
	return &Reader{bus: bus, archive: archive}
}

// Read returns up to limit entries recorded strictly after since.
func (r *Reader) Read(ctx context.Context, deployID string, since string, limit int) ([]HistoryEntry, string, error) {
	if entries, next := r.bus.History(deployID, since, limit); len(entries) > 0 {
		return convert(entries), next, nil
	}

	// Nothing live. Either the deploy never logged anything, or it ended and
	// its topic was released — in which case the archive is what is left.
	if r.archive == nil {
		return nil, "", nil
	}
	id, err := uuid.Parse(deployID)
	if err != nil {
		return nil, "", nil
	}
	blob, err := r.archive.LogTail(ctx, id)
	if err != nil || len(blob) == 0 {
		return nil, "", err
	}

	return page(logbus.DecodeLines(blob), since, limit)
}

// page applies the same cursor contract to an archived tail that the bus
// applies to live history, so a client polling across the moment a deploy ends
// does not have to know which source answered.
func page(lines []logbus.Line, since string, limit int) ([]HistoryEntry, string, error) {
	if limit <= 0 || limit > logbus.DefaultHistory {
		limit = 200
	}

	start := 0
	if since != "" {
		if seq, err := strconv.Atoi(since); err == nil && seq >= 0 {
			start = seq + 1
		}
	}
	if start >= len(lines) {
		return nil, since, nil
	}
	end := start + limit
	if end > len(lines) {
		end = len(lines)
	}

	out := make([]HistoryEntry, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, HistoryEntry{ID: strconv.Itoa(i), Line: fromBus(lines[i])})
	}
	return out, out[len(out)-1].ID, nil
}

func convert(entries []logbus.Entry) []HistoryEntry {
	out := make([]HistoryEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, HistoryEntry{ID: e.ID, Line: fromBus(e.Line)})
	}
	return out
}

func fromBus(l logbus.Line) LogLine {
	return LogLine{
		DeployID:  l.DeployID,
		Stage:     l.Stage,
		Text:      l.Text,
		Level:     l.Level,
		Timestamp: l.Timestamp,
	}
}
