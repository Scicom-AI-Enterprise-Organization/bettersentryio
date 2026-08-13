// Package events is error tracking: it takes an event from an SDK, decides which
// issue it belongs to, and records it.
//
// The payload shape is Sentry's (`exception.values[].stacktrace.frames[]`), so that a
// stock sentry-sdk can be pointed at us later by adding envelope framing in front of
// this — the decision recorded as D3. Nothing here depends on the framing.
package events

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/store"
)

// Frame is one stack frame. Only the fields grouping and display need.
type Frame struct {
	Filename string `json:"filename"`
	Function string `json:"function"`
	Module   string `json:"module"`
	Lineno   int    `json:"lineno"`
	// InApp separates your code from site-packages. Grouping prefers in-app frames,
	// because two different bugs that both end inside the same library are not one bug.
	InApp   bool     `json:"in_app"`
	Context []string `json:"context_line,omitempty"`
}

type Exception struct {
	Type       string  `json:"type"`
	Value      string  `json:"value"`
	Module     string  `json:"module"`
	Stacktrace *Stack  `json:"stacktrace"`
	Mechanism  *Mech   `json:"mechanism,omitempty"`
}

type Stack struct {
	Frames []Frame `json:"frames"`
}

// Mech records how we got the exception — which hook fired. Useful when debugging why
// something was or was not captured.
type Mech struct {
	Type    string `json:"type"`
	Handled *bool  `json:"handled,omitempty"`
}

// Event is what an SDK posts.
type Event struct {
	EventID     string            `json:"event_id"`
	Timestamp   *time.Time        `json:"timestamp"`
	Level       string            `json:"level"`
	Logger      string            `json:"logger"`
	Message     string            `json:"message"`
	Environment string            `json:"environment"`
	Release     string            `json:"release"`
	ServerName  string            `json:"server_name"`
	Transaction string            `json:"transaction"`
	Tags        map[string]string `json:"tags"`
	Extra       map[string]any    `json:"extra"`
	Request     *Request          `json:"request"`
	Exception   *ExceptionValues  `json:"exception"`
}

type ExceptionValues struct {
	Values []Exception `json:"values"`
}

type Request struct {
	Method string `json:"method"`
	URL    string `json:"url"`
	Query  string `json:"query_string,omitempty"`
}

// Ingested is what the caller gets back: which issue this landed in, and whether it
// was the first sighting.
type Ingested struct {
	IssueID   int64  `json:"issue_id"`
	EventID   int64  `json:"-"`
	IsNew     bool   `json:"is_new"`
	TimesSeen int64  `json:"times_seen"`
	Culprit   string `json:"culprit"`
}

/* ---- grouping --------------------------------------------------------------
 * The whole value of error tracking is that ten thousand occurrences collapse into one
 * row. Getting that wrong in either direction is bad: group too loosely and unrelated
 * bugs merge; too tightly and one bug becomes a new issue on every deploy.
 */

// Fingerprint decides which issue an event belongs to.
//
// Line numbers are deliberately excluded: adding an import above a function must not
// split its existing issue in two. Filename, function and module are used instead, and
// only for in-app frames when there are any — an event whose in-app frames match is the
// same bug even if the library internals below it differ between versions.
func Fingerprint(e *Event) (fingerprint, kind, culprit, title string) {
	h := md5.New()

	var exc *Exception
	if e.Exception != nil && len(e.Exception.Values) > 0 {
		// The last value is the one that was actually raised; earlier ones are causes.
		exc = &e.Exception.Values[len(e.Exception.Values)-1]
	}

	if exc == nil {
		// No exception: group by the message with variable parts removed, so
		// "user 91 not found" and "user 92 not found" are one issue.
		kind = firstNonEmpty(e.Logger, "message")
		title = truncate(e.Message, 300)
		param := parameterize(e.Message)
		fmt.Fprintf(h, "message\x00%s\x00%s", kind, param)
		return hex.EncodeToString(h.Sum(nil)), kind, firstNonEmpty(e.Transaction, e.Logger), title
	}

	kind = firstNonEmpty(exc.Type, "Error")
	title = kind
	if exc.Value != "" {
		title = kind + ": " + truncate(exc.Value, 300)
	}

	fmt.Fprintf(h, "exception\x00%s\x00%s", exc.Module, kind)

	frames := relevantFrames(exc)
	for _, f := range frames {
		// filename, not lineno — see the doc comment.
		fmt.Fprintf(h, "\x00%s\x00%s\x00%s", f.Module, f.Filename, f.Function)
	}
	if len(frames) == 0 {
		// Nothing to walk: fall back to the parameterized value so at least distinct
		// messages stay distinct.
		fmt.Fprintf(h, "\x00novalue\x00%s", parameterize(exc.Value))
	}

	culprit = "?"
	if n := len(frames); n > 0 {
		last := frames[n-1] // deepest relevant frame: where it actually blew up
		culprit = strings.TrimSpace(firstNonEmpty(last.Module, last.Filename))
		if last.Function != "" {
			culprit += " in " + last.Function
		}
	} else if e.Transaction != "" {
		culprit = e.Transaction
	}

	return hex.EncodeToString(h.Sum(nil)), kind, culprit, title
}

// relevantFrames returns in-app frames if there are any, otherwise every frame. An
// exception raised entirely inside a library still has to group somehow.
func relevantFrames(exc *Exception) []Frame {
	if exc.Stacktrace == nil {
		return nil
	}
	inApp := make([]Frame, 0, len(exc.Stacktrace.Frames))
	for _, f := range exc.Stacktrace.Frames {
		if f.InApp {
			inApp = append(inApp, f)
		}
	}
	if len(inApp) > 0 {
		return inApp
	}
	return exc.Stacktrace.Frames
}

var (
	reNumber = regexp.MustCompile(`\b\d+\b`)
	reHex    = regexp.MustCompile(`\b[0-9a-fA-F]{8,}\b`)
	reUUID   = regexp.MustCompile(`\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	reQuoted = regexp.MustCompile(`'[^']*'|"[^"]*"`)
	reAddr   = regexp.MustCompile(`0x[0-9a-fA-F]+`)
)

// parameterize strips the parts of a message that vary per occurrence, so the same bug
// with different data groups together.
func parameterize(s string) string {
	s = reUUID.ReplaceAllString(s, "<uuid>")
	s = reAddr.ReplaceAllString(s, "<addr>")
	s = reHex.ReplaceAllString(s, "<hex>")
	s = reQuoted.ReplaceAllString(s, "<str>")
	s = reNumber.ReplaceAllString(s, "<n>")
	return strings.TrimSpace(s)
}

/* ---- ingest ---------------------------------------------------------------- */

type Store struct{ db *store.DB }

func New(db *store.DB) *Store { return &Store{db: db} }

// Ingest records one event against its issue, creating the issue on first sighting.
//
// One transaction, one upsert: concurrent SDKs reporting the same new bug must not race
// into two issues, which the unique index on (project, fingerprint, environment)
// prevents and ON CONFLICT resolves.
func (s *Store) Ingest(ctx context.Context, projectID int64, e *Event) (Ingested, error) {
	var out Ingested

	fingerprint, kind, culprit, title := Fingerprint(e)
	env := firstNonEmpty(e.Environment, "production")
	level := firstNonEmpty(e.Level, "error")
	seen := time.Now()
	if e.Timestamp != nil && !e.Timestamp.IsZero() {
		seen = *e.Timestamp
	}

	payload, err := json.Marshal(e)
	if err != nil {
		return out, fmt.Errorf("marshal event: %w", err)
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// `xmax = 0` is the standard way to learn whether an upsert inserted or updated.
	err = tx.QueryRow(ctx, `
		insert into issues (project_id, fingerprint, environment, kind, culprit, title,
		                    level, times_seen, first_seen, last_seen)
		values ($1, $2, $3, $4, $5, $6, $7, 1, $8, $8)
		on conflict (project_id, fingerprint, environment) do update
		   set times_seen  = issues.times_seen + 1,
		       last_seen   = greatest(issues.last_seen, excluded.last_seen),
		       -- A recurrence reopens it: something we called fixed is happening again.
		       resolved_at = null,
		       title       = excluded.title,
		       culprit     = excluded.culprit,
		       level       = excluded.level
		returning id, times_seen, (xmax = 0)`,
		projectID, fingerprint, env, kind, culprit, title, level, seen,
	).Scan(&out.IssueID, &out.TimesSeen, &out.IsNew)
	if err != nil {
		return out, fmt.Errorf("upsert issue: %w", err)
	}

	if err := tx.QueryRow(ctx, `
		insert into events (issue_id, received_at, message, payload)
		values ($1, $2, $3, $4) returning id`,
		out.IssueID, seen, truncate(firstNonEmpty(e.Message, title), 1000), payload,
	).Scan(&out.EventID); err != nil {
		return out, fmt.Errorf("insert event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return out, err
	}
	out.Culprit = culprit
	return out, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// Itoa is used by the API layer for cheap path/int conversions.
func Itoa(n int64) string { return strconv.FormatInt(n, 10) }
