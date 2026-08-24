// Event lookup: from an identifier somebody is holding — a correlation id out of a
// log line, a trace id out of a Grafana trace panel — to the exact events it
// produced. This is a row search, not an aggregation, which is why it lives beside
// discover.go rather than in it.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// FoundEvent is one hit, with enough context to render a row and build the deep
// link to the issue page that explains it.
type FoundEvent struct {
	// ID is our row id; EventID is the SDK's uuid when it sent one. Both travel
	// because the issue page's event browser is keyed by row id.
	ID          int64     `json:"id"`
	EventID     string    `json:"event_id"`
	ReceivedAt  time.Time `json:"received_at"`
	Message     string    `json:"message"`
	IssueID     int64     `json:"issue_id"`
	IssueTitle  string    `json:"issue_title"`
	Level       string    `json:"level"`
	Environment string    `json:"environment"`
	Project     string    `json:"project"`
}

type LookupQuery struct {
	// ProjectID narrows to one app; 0 searches every project, because the person
	// holding a correlation id often does not know which service produced the error.
	ProjectID int64
	// Tags: every pair must match the event's own tags (not the issue's — an issue
	// keeps only its latest event's tags, and a correlation id is unique per request).
	Tags map[string]string
	// TraceID matches contexts.trace.trace_id, where the stock SDK puts it.
	TraceID  string
	From, To time.Time
	Limit    int
}

// LookupEvents finds events by per-event identity. At least one of Tags/TraceID is
// required — an unfiltered dump is not a search, and would be a payload-sized table
// scan besides.
func (s *Store) LookupEvents(ctx context.Context, q LookupQuery) ([]FoundEvent, error) {
	if len(q.Tags) == 0 && q.TraceID == "" {
		return nil, fmt.Errorf("a tag or a trace id is required")
	}
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 100
	}

	args := []any{}
	where := "e.received_at >= " + arg(&args, q.From) + " and e.received_at < " + arg(&args, q.To)
	if q.ProjectID != 0 {
		where += " and i.project_id = " + arg(&args, q.ProjectID)
	}
	if len(q.Tags) > 0 {
		// One containment test for all pairs: @> is what the jsonb_path_ops GIN
		// index answers, where per-key ->> extraction would not use it.
		blob, err := json.Marshal(q.Tags)
		if err != nil {
			return nil, err
		}
		where += " and e.payload -> 'tags' @> " + arg(&args, string(blob)) + "::jsonb"
	}
	if q.TraceID != "" {
		where += " and e.payload -> 'contexts' -> 'trace' ->> 'trace_id' = " + arg(&args, q.TraceID)
	}

	rows, err := s.db.Query(ctx, `
		select e.id, coalesce(e.payload ->> 'event_id', ''), e.received_at, e.message,
		       i.id, i.title, i.level, i.environment, p.slug
		from events e
		join issues i on i.id = e.issue_id
		join projects p on p.id = i.project_id
		where `+where+`
		order by e.received_at desc
		limit `+arg(&args, q.Limit), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []FoundEvent{}
	for rows.Next() {
		var f FoundEvent
		if err := rows.Scan(&f.ID, &f.EventID, &f.ReceivedAt, &f.Message,
			&f.IssueID, &f.IssueTitle, &f.Level, &f.Environment, &f.Project); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
