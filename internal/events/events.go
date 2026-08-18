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

// FlexTime accepts Sentry's two timestamp encodings — RFC3339 string or epoch
// float — and never fails: a timestamp we cannot parse must not sink the event,
// so it decodes to zero and ingest stamps the arrival time instead.
type FlexTime struct{ time.Time }

func (t *FlexTime) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "" || s == "null" || s == `""` {
		return nil
	}
	if s[0] == '"' {
		var str string
		if json.Unmarshal(b, &str) != nil {
			return nil
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05"} {
			if ts, err := time.Parse(layout, str); err == nil {
				t.Time = ts
				return nil
			}
		}
		return nil
	}
	var f float64
	if json.Unmarshal(b, &f) != nil {
		return nil
	}
	sec := int64(f)
	t.Time = time.Unix(sec, int64((f-float64(sec))*1e9)).UTC()
	return nil
}

func (t FlexTime) MarshalJSON() ([]byte, error) {
	if t.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(t.Time)
}

// Tags accepts Sentry's two encodings — an object map or an array of [key, value]
// pairs — and coerces non-string values, which some SDKs emit.
type Tags map[string]string

func (t *Tags) UnmarshalJSON(b []byte) error {
	out := map[string]string{}
	var m map[string]any
	if json.Unmarshal(b, &m) == nil {
		for k, v := range m {
			out[k] = coerceString(v)
		}
		*t = out
		return nil
	}
	var pairs [][]any
	if json.Unmarshal(b, &pairs) == nil {
		for _, p := range pairs {
			if len(p) == 2 {
				out[coerceString(p[0])] = coerceString(p[1])
			}
		}
		*t = out
	}
	return nil // malformed tags must not sink the event
}

func coerceString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

// Lines accepts a context line as either a bare string (stock sentry-sdk) or a
// list (the legacy vendored client sent one-element lists).
type Lines []string

func (l *Lines) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*l = Lines{s}
		return nil
	}
	var arr []string
	if json.Unmarshal(b, &arr) == nil {
		*l = Lines(arr)
	}
	return nil
}

// Frame is one stack frame. Grouping reads module/filename/function/in_app; the
// rest (source context, locals) rides along for display, stored in the payload.
type Frame struct {
	Filename string `json:"filename"`
	AbsPath  string `json:"abs_path,omitempty"`
	Function string `json:"function"`
	Module   string `json:"module"`
	Lineno   int    `json:"lineno"`
	Colno    int    `json:"colno,omitempty"`
	// InApp separates your code from site-packages. Grouping prefers in-app frames,
	// because two different bugs that both end inside the same library are not one bug.
	InApp       bool            `json:"in_app"`
	Context     Lines           `json:"context_line,omitempty"`
	PreContext  []string        `json:"pre_context,omitempty"`
	PostContext []string        `json:"post_context,omitempty"`
	Vars        json.RawMessage `json:"vars,omitempty"`
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

// Event is what an SDK posts. The named fields are what grouping and the UI read;
// pass-through blobs (contexts, breadcrumbs, user, sdk) stay raw — the stored
// payload keeps everything the SDK sent either way.
type Event struct {
	EventID     string           `json:"event_id"`
	Timestamp   *FlexTime        `json:"timestamp"`
	Platform    string           `json:"platform,omitempty"`
	Level       string           `json:"level"`
	Logger      string           `json:"logger"`
	Message     string           `json:"message"`
	Logentry    *Logentry        `json:"logentry,omitempty"`
	Environment string           `json:"environment"`
	Release     string           `json:"release"`
	ServerName  string           `json:"server_name"`
	Transaction string           `json:"transaction"`
	Fingerprint []string         `json:"fingerprint,omitempty"`
	Tags        Tags             `json:"tags"`
	Extra       map[string]any   `json:"extra"`
	Request     *Request         `json:"request"`
	Exception   *ExceptionValues `json:"exception"`
	User        json.RawMessage  `json:"user,omitempty"`
	Contexts    json.RawMessage  `json:"contexts,omitempty"`
	Breadcrumbs json.RawMessage  `json:"breadcrumbs,omitempty"`
	SDK         json.RawMessage  `json:"sdk,omitempty"`
	Modules     json.RawMessage  `json:"modules,omitempty"`
}

// Logentry is Sentry's message-with-parameters form; stock SDKs send logger
// messages here rather than in the bare `message` field.
type Logentry struct {
	Message   string          `json:"message,omitempty"`
	Formatted string          `json:"formatted,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
}

type ExceptionValues struct {
	Values []Exception `json:"values"`
}

type Request struct {
	Method  string          `json:"method"`
	URL     string          `json:"url"`
	Query   string          `json:"query_string,omitempty"`
	Headers json.RawMessage `json:"headers,omitempty"`
	Env     json.RawMessage `json:"env,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Ingested is what the caller gets back: which issue this landed in, and whether it
// was the first sighting.
type Ingested struct {
	IssueID   int64  `json:"issue_id"`
	EventID   int64  `json:"-"`
	IsNew     bool   `json:"is_new"`
	TimesSeen int64  `json:"times_seen"`
	Culprit   string `json:"culprit"`
	// For the new-issue and regression alerts, not for the SDK response.
	Title       string `json:"-"`
	Level       string `json:"-"`
	Environment string `json:"-"`
	// Reopened: this event arrived at an issue somebody had marked resolved.
	Reopened bool `json:"-"`
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
	fingerprint, kind, culprit, title = fingerprintDefault(e)

	// An explicit fingerprint from the SDK wins. "{{ default }}" splices in the
	// hash we computed, per Sentry's contract; ["{{ default }}"] alone is a no-op.
	if len(e.Fingerprint) > 0 {
		custom := false
		h := md5.New()
		for _, part := range e.Fingerprint {
			if strings.TrimSpace(part) == "{{ default }}" {
				fmt.Fprintf(h, "\x00%s", fingerprint)
				continue
			}
			custom = true
			fmt.Fprintf(h, "\x00%s", part)
		}
		if custom {
			fingerprint = hex.EncodeToString(h.Sum(nil))
		}
	}
	return fingerprint, kind, culprit, title
}

func fingerprintDefault(e *Event) (fingerprint, kind, culprit, title string) {
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

// deriveTags merges the client's tags with what the server can read off the
// event itself — the fields sentry.io promotes to searchable tags. Client tags
// win on collision: the service knows better than a heuristic.
func deriveTags(e *Event, level, env string) map[string]string {
	out := map[string]string{
		"level":       level,
		"environment": env,
	}
	if e.Release != "" {
		out["release"] = e.Release
	}
	if e.Transaction != "" {
		out["transaction"] = e.Transaction
	}
	if e.ServerName != "" {
		out["server_name"] = e.ServerName
	}
	if e.Logger != "" {
		out["logger"] = e.Logger
	}
	if e.Request != nil && e.Request.URL != "" {
		out["url"] = truncate(e.Request.URL, 200)
	}
	if e.Exception != nil && len(e.Exception.Values) > 0 {
		last := e.Exception.Values[len(e.Exception.Values)-1]
		if last.Mechanism != nil {
			if last.Mechanism.Type != "" {
				out["mechanism"] = last.Mechanism.Type
			}
			if last.Mechanism.Handled != nil {
				if *last.Mechanism.Handled {
					out["handled"] = "yes"
				} else {
					out["handled"] = "no"
				}
			}
		}
	}
	for k, v := range e.Tags {
		if k != "" && v != "" {
			out[truncate(k, 64)] = truncate(v, 256)
		}
	}
	return out
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
	return s.IngestRaw(ctx, projectID, e, nil)
}

// IngestRaw is Ingest, but stores the SDK's own bytes as the event payload when
// given — the envelope path uses this so nothing the SDK sent is lost to our
// struct's field list. raw == nil falls back to re-marshaling the struct.
func (s *Store) IngestRaw(ctx context.Context, projectID int64, e *Event, raw []byte) (Ingested, error) {
	var out Ingested

	// Stock SDKs put logger messages in logentry, not message.
	if e.Message == "" && e.Logentry != nil {
		e.Message = firstNonEmpty(e.Logentry.Formatted, e.Logentry.Message)
	}

	fingerprint, kind, culprit, title := Fingerprint(e)
	env := firstNonEmpty(e.Environment, "production")
	level := firstNonEmpty(e.Level, "error")
	now := time.Now()
	seen := now
	if e.Timestamp != nil && !e.Timestamp.IsZero() {
		seen = e.Timestamp.Time
		// Clamp instead of reject: a skewed clock must not hide the event, but it
		// must not be allowed to pin last_seen into the future or the deep past.
		if seen.After(now.Add(time.Minute)) || seen.Before(now.Add(-30*24*time.Hour)) {
			seen = now
		}
	}

	payload := raw
	if payload == nil {
		var err error
		payload, err = json.Marshal(e)
		if err != nil {
			return out, fmt.Errorf("marshal event: %w", err)
		}
	}

	tagsJSON, err := json.Marshal(deriveTags(e, level, env))
	if err != nil {
		tagsJSON = []byte("{}")
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// `xmax = 0` is the standard way to learn whether an upsert inserted or
	// updated. The `prior` CTE reads the pre-statement row, which is how a
	// recurrence at a RESOLVED issue is recognized as a regression.
	var priorResolved *time.Time
	err = tx.QueryRow(ctx, `
		with prior as (
			select resolved_at from issues
			 where project_id = $1 and fingerprint = $2 and environment = $3
		)
		insert into issues (project_id, fingerprint, environment, kind, culprit, title,
		                    level, times_seen, first_seen, last_seen, tags)
		values ($1, $2, $3, $4, $5, $6, $7, 1, $8, $8, $9::jsonb)
		on conflict (project_id, fingerprint, environment) do update
		   set times_seen  = issues.times_seen + 1,
		       last_seen   = greatest(issues.last_seen, excluded.last_seen),
		       -- A recurrence reopens it: something we called fixed is happening again.
		       resolved_at = null,
		       title       = excluded.title,
		       culprit     = excluded.culprit,
		       level       = excluded.level,
		       tags        = excluded.tags
		returning id, times_seen, (xmax = 0), (select resolved_at from prior)`,
		projectID, fingerprint, env, kind, culprit, title, level, seen, string(tagsJSON),
	).Scan(&out.IssueID, &out.TimesSeen, &out.IsNew, &priorResolved)
	if err != nil {
		return out, fmt.Errorf("upsert issue: %w", err)
	}
	out.Reopened = !out.IsNew && priorResolved != nil

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
	out.Title = title
	out.Level = level
	out.Environment = env
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
