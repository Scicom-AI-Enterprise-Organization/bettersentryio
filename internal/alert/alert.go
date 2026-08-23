// Package alert delivers state transitions to chat tools. Delivery is
// at-least-once and deduplicated in Postgres, so a restart mid-send cannot lose
// an alert and two replicas cannot double-send one.
package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/store"
)

const (
	SeverityCritical = "critical"
	SeverityWarning  = "warning"
	SeverityOK       = "ok"
)

type Event struct {
	Kind        string            `json:"event"`
	Monitor     string            `json:"monitor"`
	Environment string            `json:"environment"`
	Status      string            `json:"status"`
	Severity    string            `json:"severity"`
	Title       string            `json:"title"`
	Text        string            `json:"text"`
	URL         string            `json:"url,omitempty"`
	Fields      map[string]string `json:"fields,omitempty"`

	// DedupKey identifies the transition, not the event instance. Two attempts to
	// announce the same transition collapse to one delivery per channel.
	DedupKey string `json:"-"`

	// ProjectID scopes delivery: an alert goes to the project's own channels plus
	// the global channels that project imported. Zero means the caller could not
	// resolve a project, and the alert falls back to every enabled global channel —
	// a missing scope must not turn into silence.
	ProjectID int64 `json:"-"`
}

type channel struct {
	id     int64
	name   string
	kind   string
	config map[string]string
}

type Alerter struct {
	db      *store.DB
	log     *slog.Logger
	http    *http.Client
	queue   chan Event
	backoff time.Duration
	baseURL string

	dropped    atomic.Int64
	sent       atomic.Int64
	failed     atomic.Int64
	suppressed atomic.Int64
	digested   atomic.Int64
}

// Patience: the window in which a burst collapses into one digest.
const (
	// defaultPatience applies to alerts that resolve to no project.
	defaultPatience = 10 * time.Minute
	// flushEvery is how often closed windows are swept. Well under the shortest
	// useful patience, so a digest lands within seconds of its window closing.
	flushEvery = 5 * time.Second
	// digestCap bounds one digest card. Past this the count carries the news.
	digestCap = 20
)

func New(db *store.DB, log *slog.Logger, buffer int) *Alerter {
	if buffer <= 0 {
		buffer = 256
	}
	return &Alerter{
		db:      db,
		log:     log,
		http:    &http.Client{Timeout: 10 * time.Second},
		queue:   make(chan Event, buffer),
		backoff: time.Second,
	}
}

// SetRetryBackoff shortens the per-attempt backoff so tests do not spend seconds
// waiting for a delivery to give up.
func (a *Alerter) SetRetryBackoff(d time.Duration) { a.backoff = d }

// SetBaseURL gives digest cards somewhere to link. Individual alerts carry their
// own URL from whoever raised them; a digest spans many, so it links to the project.
func (a *Alerter) SetBaseURL(u string) { a.baseURL = strings.TrimRight(u, "/") }

// Notify enqueues without blocking. Dropping under pressure is deliberate: an
// alerter that blocks the detector would turn a chat outage into a monitoring
// outage. Drops are counted and surfaced on /-/health.
func (a *Alerter) Notify(_ context.Context, ev Event) {
	select {
	case a.queue <- ev:
	default:
		a.dropped.Add(1)
		a.log.Error("alert queue full, dropped event", "monitor", ev.Monitor, "kind", ev.Kind)
	}
}

func (a *Alerter) QueueDepth() int   { return len(a.queue) }
func (a *Alerter) Dropped() int64    { return a.dropped.Load() }
func (a *Alerter) Sent() int64       { return a.sent.Load() }
func (a *Alerter) Failed() int64     { return a.failed.Load() }
func (a *Alerter) Suppressed() int64 { return a.suppressed.Load() }

// Digested counts alerts folded into a digest instead of sent on their own —
// the size of the flood that did not reach anybody's phone.
func (a *Alerter) Digested() int64 { return a.digested.Load() }

func (a *Alerter) Run(ctx context.Context) {
	a.log.Info("alerter started")
	flush := time.NewTicker(flushEvery)
	defer flush.Stop()
	for {
		select {
		case <-ctx.Done():
			a.log.Info("alerter stopped")
			return
		case ev := <-a.queue:
			a.deliver(ctx, ev)
		case <-flush.C:
			a.flushDigests(ctx)
		}
	}
}

func (a *Alerter) deliver(ctx context.Context, ev Event) {
	channels, err := a.channels(ctx, ev.ProjectID)
	if err != nil {
		a.log.Error("load channels failed", "err", err)
		return
	}
	if len(channels) == 0 {
		a.log.Warn("no alert channel routes this project — event not delivered",
			"monitor", ev.Monitor, "project_id", ev.ProjectID, "text", ev.Text)
		return
	}

	patience := a.patience(ctx, ev.ProjectID)

	for _, ch := range channels {
		claimed, err := a.claim(ctx, ev.DedupKey, ch.id)
		if err != nil {
			a.log.Error("dedup claim failed", "channel", ch.name, "err", err)
			continue
		}
		if !claimed {
			a.suppressed.Add(1)
			continue
		}

		// The first alert of a quiet spell goes out now; the rest of the burst waits
		// for the window to close and arrives as one card. openWindow decides which
		// this is, atomically, so two replicas cannot both call themselves first.
		opened := false
		if patience > 0 && ev.ProjectID != 0 {
			first, err := a.openWindow(ctx, ev.ProjectID, ch.id, patience)
			if err != nil {
				a.log.Error("patience window failed, sending immediately",
					"channel", ch.name, "err", err)
			} else if !first {
				if err := a.appendPending(ctx, ev.ProjectID, ch.id, ev); err != nil {
					a.log.Error("digest append failed", "channel", ch.name, "err", err)
					a.release(ctx, ev.DedupKey, ch.id)
					continue
				}
				a.digested.Add(1)
				continue
			} else {
				opened = true
			}
		}

		if err := a.send(ctx, ch, ev); err != nil {
			a.failed.Add(1)
			a.log.Error("alert delivery failed", "channel", ch.name, "monitor", ev.Monitor, "err", err)
			// Release the claim so a later attempt can retry this transition.
			a.release(ctx, ev.DedupKey, ch.id)
			// And close the window this attempt opened. Patience rate-limits how often
			// a channel is *interrupted*; nothing was delivered, so there is nothing to
			// rate-limit. Leaving it open would fold the retry into a digest and turn a
			// chat outage from "the alert is seconds late" into "the alert arrives as a
			// summary line up to a whole window later" — which is the retry path's
			// entire reason for existing, defeated.
			if opened {
				a.closeWindow(ctx, ev.ProjectID, ch.id)
			}
			continue
		}
		a.sent.Add(1)
		a.log.Info("alert sent", "channel", ch.name, "kind", ev.Kind, "monitor", ev.Monitor)
	}
}

// channels resolves which channels an alert reaches: the project's own, plus the
// global definitions that project imported. A zero projectID means the scope could
// not be resolved, and every enabled global channel is used instead — the loud
// failure mode beats the silent one.
func (a *Alerter) channels(ctx context.Context, projectID int64) ([]channel, error) {
	const scoped = `
		select id, name, type, config from channels
		where enabled and (project_id = $1 or exists (
			select 1 from project_channels pc
			where pc.channel_id = channels.id and pc.project_id = $1))
		order by id`
	const global = `
		select id, name, type, config from channels
		where enabled and project_id is null order by id`

	query, args := global, []any(nil)
	if projectID != 0 {
		query, args = scoped, []any{projectID}
	}
	rows, err := a.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []channel
	for rows.Next() {
		var (
			ch  channel
			raw []byte
		)
		if err := rows.Scan(&ch.id, &ch.name, &ch.kind, &raw); err != nil {
			return nil, err
		}
		ch.config = map[string]string{}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &ch.config); err != nil {
				return nil, fmt.Errorf("decode channel %s config: %w", ch.name, err)
			}
		}
		out = append(out, ch)
	}
	return out, rows.Err()
}

// patience reads the project's window. A lookup failure falls back to the default
// rather than to zero: a database hiccup must not turn into an alert flood.
func (a *Alerter) patience(ctx context.Context, projectID int64) time.Duration {
	if projectID == 0 {
		return defaultPatience
	}
	secs, err := a.db.AlertPatience(ctx, projectID)
	if err != nil {
		a.log.Error("read alert patience failed", "project_id", projectID, "err", err)
		return defaultPatience
	}
	return time.Duration(secs) * time.Second
}

// openWindow reports whether this alert is the first in a quiet window for the
// channel. The row's existence is the window, so the insert is both the test and
// the claim — one statement, safe across replicas.
func (a *Alerter) openWindow(ctx context.Context, projectID, channelID int64, patience time.Duration) (bool, error) {
	tag, err := a.db.Exec(ctx, `
		insert into alert_digests (project_id, channel_id, window_ends_at)
		values ($1, $2, now() + make_interval(secs => $3))
		on conflict (project_id, channel_id) do nothing`,
		projectID, channelID, patience.Seconds())
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// closeWindow undoes an openWindow whose delivery then failed, so the next attempt is
// immediate again.
//
// Only when the window is still empty: another replica may have appended to it between
// our insert and our failure, and deleting the row would throw those events away.
func (a *Alerter) closeWindow(ctx context.Context, projectID, channelID int64) {
	if _, err := a.db.Exec(ctx, `
		delete from alert_digests
		where project_id = $1 and channel_id = $2 and jsonb_array_length(pending) = 0`,
		projectID, channelID,
	); err != nil {
		a.log.Error("close patience window failed", "channel_id", channelID, "err", err)
	}
}

// digestEntry is one line of a digest card. Kept small on purpose: the card
// summarises, the UI has the detail.
type digestEntry struct {
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Text     string `json:"text"`
	Severity string `json:"severity"`
	URL      string `json:"url,omitempty"`
}

func (a *Alerter) appendPending(ctx context.Context, projectID, channelID int64, ev Event) error {
	entry, err := json.Marshal(digestEntry{
		Kind: ev.Kind, Title: ev.Title, Text: ev.Text, Severity: ev.Severity, URL: ev.URL,
	})
	if err != nil {
		return err
	}
	// Past the cap the card stops growing and the overflow becomes a count. A
	// thousand-issue storm must not become a thousand-line Teams message.
	_, err = a.db.Exec(ctx, `
		update alert_digests set
			pending = case when jsonb_array_length(pending) < $3
			               then pending || jsonb_build_array($4::jsonb) else pending end,
			dropped = case when jsonb_array_length(pending) < $3 then dropped else dropped + 1 end
		where project_id = $1 and channel_id = $2`,
		projectID, channelID, digestCap, string(entry))
	return err
}

func (a *Alerter) claim(ctx context.Context, dedupKey string, channelID int64) (bool, error) {
	tag, err := a.db.Exec(ctx, `
		insert into notifications (dedup_key, channel_id) values ($1, $2)
		on conflict (dedup_key, channel_id) do nothing`, dedupKey, channelID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (a *Alerter) release(ctx context.Context, dedupKey string, channelID int64) {
	if _, err := a.db.Exec(ctx,
		`delete from notifications where dedup_key = $1 and channel_id = $2`, dedupKey, channelID,
	); err != nil {
		a.log.Error("release dedup claim failed", "err", err)
	}
}

// send delivers with the live retry policy: three attempts with backoff, because a
// real alert is worth waiting for.
func (a *Alerter) send(ctx context.Context, ch channel, ev Event) error {
	return a.sendWithin(ctx, ch, ev, 3)
}

// TestChannel delivers a probe to a channel configuration that has not been saved,
// so an operator can find out whether a webhook works before committing to it.
//
// It goes through payload() and the same HTTP client a live alert uses: a test that
// travels different code proves nothing about the thing you are about to save.
//
// One attempt, deliberately — not the live path's three. The caller is a human
// staring at a button, and a test that sits for six seconds before reporting
// "connection refused" teaches them to distrust it. Pressing it again is cheaper
// than waiting out a backoff.
func (a *Alerter) TestChannel(ctx context.Context, kind, url string) error {
	ch := channel{name: "test", kind: kind, config: map[string]string{"url": url}}
	return a.sendWithin(ctx, ch, testEvent(a.baseURL), 1)
}

// testEvent is what a probe looks like on the other end: unmistakably a test, and
// severity OK so a Teams card renders it green rather than paging whoever sees it.
func testEvent(baseURL string) Event {
	return Event{
		Kind:     "channel.test",
		Monitor:  "bettersentryio",
		Status:   "test",
		Severity: SeverityOK,
		Title:    "bettersentryio test alert",
		Text: "If you can read this, the webhook works. " +
			"Nothing is broken — somebody pressed Send test while configuring this channel.",
		URL:    baseURL,
		Fields: map[string]string{"kind": "test"},
	}
}

func (a *Alerter) sendWithin(ctx context.Context, ch channel, ev Event, attempts int) error {
	url, body, contentType, err := payload(ch, ev)
	if err != nil {
		return err
	}

	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			delay := a.backoff * (1 << attempt)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("User-Agent", "bettersentryio")

		resp, err := a.http.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		drained, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
		lastErr = fmt.Errorf("%s returned %d: %s", ch.kind, resp.StatusCode, bytes.TrimSpace(drained))
		// 4xx other than 429 will not fix themselves; stop burning retries.
		if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
			return lastErr
		}
	}
	return lastErr
}

func payload(ch channel, ev Event) (url string, body []byte, contentType string, err error) {
	contentType = "application/json"
	switch ch.kind {
	case "webhook":
		url = ch.config["url"]
		body, err = json.Marshal(ev)

	case "slack":
		url = ch.config["url"]
		text := ev.Text
		if ev.URL != "" {
			text = fmt.Sprintf("%s\n<%s|open monitor>", text, ev.URL)
		}
		body, err = json.Marshal(map[string]any{"text": text})

	case "teams":
		url = ch.config["url"]
		// Adaptive Card in the Workflows envelope. The classic Office 365
		// connector (webhook.office.com, MessageCard) was disabled by Microsoft
		// in May 2026; the replacement — a Power Automate flow with the "when a
		// Teams webhook request is received" trigger — expects
		// {"type":"message","attachments":[<adaptive card>]} and, unlike its
		// MessageCard compatibility mode, renders the button.
		cardBody := []map[string]any{
			{"type": "TextBlock", "text": ev.Title, "weight": "Bolder", "size": "Medium",
				"wrap": true, "color": adaptiveColor(ev.Severity)},
		}
		if ev.Text != "" {
			cardBody = append(cardBody, map[string]any{"type": "TextBlock", "text": ev.Text, "wrap": true})
		}
		if len(ev.Fields) > 0 {
			facts := make([]map[string]string, 0, len(ev.Fields))
			for k, v := range ev.Fields {
				facts = append(facts, map[string]string{"title": k, "value": v})
			}
			cardBody = append(cardBody, map[string]any{"type": "FactSet", "facts": facts})
		}
		card := map[string]any{
			"type":    "AdaptiveCard",
			"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
			"version": "1.4",
			"body":    cardBody,
			"msteams": map[string]string{"width": "Full"},
		}
		if ev.URL != "" {
			card["actions"] = []map[string]any{{
				"type": "Action.OpenUrl", "title": linkLabel(ev), "url": ev.URL,
			}}
		}
		body, err = json.Marshal(map[string]any{
			"type": "message",
			"attachments": []map[string]any{{
				"contentType": "application/vnd.microsoft.card.adaptive",
				"content":     card,
			}},
		})

	case "telegram":
		token, chatID := ch.config["bot_token"], ch.config["chat_id"]
		if token == "" || chatID == "" {
			return "", nil, "", fmt.Errorf("telegram channel %s missing bot_token or chat_id", ch.name)
		}
		url = "https://api.telegram.org/bot" + token + "/sendMessage"
		body, err = json.Marshal(map[string]any{"chat_id": chatID, "text": ev.Text})

	default:
		return "", nil, "", fmt.Errorf("unknown channel type %q", ch.kind)
	}

	if err != nil {
		return "", nil, "", err
	}
	if url == "" {
		return "", nil, "", fmt.Errorf("channel %s has no url configured", ch.name)
	}
	return url, body, contentType, nil
}

// linkLabel names the card's button for what it opens: monitor alerts predate
// issue alerts, and "Open monitor" on an error issue reads wrong.
func linkLabel(ev Event) string {
	if strings.HasPrefix(ev.Kind, "issue") {
		return "Open issue"
	}
	return "Open monitor"
}

// adaptiveColor maps severity onto Adaptive Card TextBlock colors.
func adaptiveColor(severity string) string {
	switch severity {
	case SeverityOK:
		return "Good"
	case SeverityWarning:
		return "Warning"
	default:
		return "Attention"
	}
}

func themeColor(severity string) string {
	switch severity {
	case SeverityOK:
		return "2EB67D"
	case SeverityWarning:
		return "ECB22E"
	default:
		return "E01E5A"
	}
}

/* ---- digest flush -------------------------------------------------------------
 * A window closes on a tick, not on an alert, so a burst that stops does not leave
 * its tail undelivered. Two outcomes per due row:
 *
 *   pending non-empty → send one digest card, reopen the window (the burst is
 *                       still hot, so the next alerts keep collapsing)
 *   pending empty     → delete the row, ending the quiet period; the next alert
 *                       for that channel is immediate again
 *
 * Digests are best-effort by design: the individual alerts they summarise already
 * claimed their dedup keys, so a lost digest costs a summary, never a first alert.
 */

type dueDigest struct {
	projectID int64
	channelID int64
	entries   []digestEntry
	dropped   int
}

func (a *Alerter) flushDigests(ctx context.Context) {
	due, err := a.claimDue(ctx)
	if err != nil {
		a.log.Error("digest flush failed", "err", err)
		return
	}
	for _, d := range due {
		ch, ok, err := a.channelByID(ctx, d.channelID)
		if err != nil {
			a.log.Error("digest channel lookup failed", "channel_id", d.channelID, "err", err)
			continue
		}
		if !ok {
			continue // deleted or disabled while the window was open
		}
		ev := a.digestEvent(ctx, d)
		if err := a.send(ctx, ch, ev); err != nil {
			a.failed.Add(1)
			a.log.Error("digest delivery failed", "channel", ch.name, "err", err)
			continue
		}
		a.sent.Add(1)
		a.log.Info("alert digest sent", "channel", ch.name,
			"alerts", len(d.entries)+d.dropped, "project_id", d.projectID)
	}
	if _, err := a.db.Exec(ctx, `
		delete from alert_digests
		where window_ends_at <= now() and jsonb_array_length(pending) = 0`,
	); err != nil {
		a.log.Error("close quiet digest windows failed", "err", err)
	}
}

// claimDue takes the pending batches off every window that has closed and reopens
// those windows in the same transaction, so a second replica sweeping at the same
// moment finds nothing to send.
func (a *Alerter) claimDue(ctx context.Context) ([]dueDigest, error) {
	tx, err := a.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	rows, err := tx.Query(ctx, `
		select d.project_id, d.channel_id, d.pending, d.dropped, p.alert_patience_seconds
		from alert_digests d join projects p on p.id = d.project_id
		where d.window_ends_at <= now() and jsonb_array_length(d.pending) > 0
		order by d.window_ends_at
		limit 200
		for update of d skip locked`)
	if err != nil {
		return nil, err
	}
	type claim struct {
		dueDigest
		patience int
	}
	var claims []claim
	for rows.Next() {
		var (
			c   claim
			raw []byte
		)
		if err := rows.Scan(&c.projectID, &c.channelID, &raw, &c.dropped, &c.patience); err != nil {
			rows.Close()
			return nil, err
		}
		if err := json.Unmarshal(raw, &c.entries); err != nil {
			a.log.Error("decode digest failed", "project_id", c.projectID, "err", err)
			continue
		}
		claims = append(claims, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]dueDigest, 0, len(claims))
	for _, c := range claims {
		patience := c.patience
		if patience <= 0 {
			// Patience was switched off mid-window: drain what accumulated, then let
			// the empty-window sweep remove the row so alerting goes back to instant.
			patience = 1
		}
		if _, err := tx.Exec(ctx, `
			update alert_digests
			set pending = '[]', dropped = 0, window_ends_at = now() + make_interval(secs => $3)
			where project_id = $1 and channel_id = $2`,
			c.projectID, c.channelID, patience,
		); err != nil {
			return nil, err
		}
		out = append(out, c.dueDigest)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func (a *Alerter) channelByID(ctx context.Context, id int64) (channel, bool, error) {
	var (
		ch  channel
		raw []byte
	)
	err := a.db.QueryRow(ctx,
		`select id, name, type, config from channels where id = $1 and enabled`, id,
	).Scan(&ch.id, &ch.name, &ch.kind, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return channel{}, false, nil
	}
	if err != nil {
		return channel{}, false, err
	}
	ch.config = map[string]string{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &ch.config); err != nil {
			return channel{}, false, err
		}
	}
	return ch, true, nil
}

// digestEvent turns a batch into the one card that stands in for it.
func (a *Alerter) digestEvent(ctx context.Context, d dueDigest) Event {
	total := len(d.entries) + d.dropped
	slug, name, err := a.db.ProjectMeta(ctx, d.projectID)
	if err != nil || name == "" {
		name = slug
	}
	if name == "" {
		name = fmt.Sprintf("project %d", d.projectID)
	}

	var b strings.Builder
	worst := SeverityOK
	for _, e := range d.entries {
		fmt.Fprintf(&b, "• %s\n", e.Title)
		worst = worseOf(worst, e.Severity)
	}
	if d.dropped > 0 {
		fmt.Fprintf(&b, "…and %d more\n", d.dropped)
	}

	return Event{
		Kind:      "alert.digest",
		Monitor:   slug,
		Status:    "digest",
		Severity:  worst,
		Title:     fmt.Sprintf("%d more alerts in %s", total, name),
		Text:      strings.TrimRight(b.String(), "\n"),
		URL:       a.projectURL(slug),
		ProjectID: d.projectID,
		Fields: map[string]string{
			"app":    slug,
			"alerts": fmt.Sprint(total),
		},
	}
}

// projectURL is derived from the first entry that carried one, so a digest links
// somewhere useful without the alerter needing to know the UI's base URL.
func (a *Alerter) projectURL(slug string) string {
	if a.baseURL == "" || slug == "" {
		return ""
	}
	return fmt.Sprintf("%s/apps/%s/issues/outages", a.baseURL, slug)
}

func worseOf(a, b string) string {
	rank := map[string]int{SeverityOK: 0, SeverityWarning: 1, SeverityCritical: 2}
	if rank[b] > rank[a] {
		return b
	}
	return a
}
