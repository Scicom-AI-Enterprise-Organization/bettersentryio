// Package alert delivers state transitions to chat tools. Delivery is
// at-least-once and deduplicated in Postgres, so a restart mid-send cannot lose
// an alert and two replicas cannot double-send one.
package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

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

	dropped    atomic.Int64
	sent       atomic.Int64
	failed     atomic.Int64
	suppressed atomic.Int64
}

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

func (a *Alerter) Run(ctx context.Context) {
	a.log.Info("alerter started")
	for {
		select {
		case <-ctx.Done():
			a.log.Info("alerter stopped")
			return
		case ev := <-a.queue:
			a.deliver(ctx, ev)
		}
	}
}

func (a *Alerter) deliver(ctx context.Context, ev Event) {
	channels, err := a.channels(ctx)
	if err != nil {
		a.log.Error("load channels failed", "err", err)
		return
	}
	if len(channels) == 0 {
		a.log.Warn("no alert channel configured — event not delivered",
			"monitor", ev.Monitor, "text", ev.Text)
		return
	}

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
		if err := a.send(ctx, ch, ev); err != nil {
			a.failed.Add(1)
			a.log.Error("alert delivery failed", "channel", ch.name, "monitor", ev.Monitor, "err", err)
			// Release the claim so a later attempt can retry this transition.
			a.release(ctx, ev.DedupKey, ch.id)
			continue
		}
		a.sent.Add(1)
		a.log.Info("alert sent", "channel", ch.name, "kind", ev.Kind, "monitor", ev.Monitor)
	}
}

func (a *Alerter) channels(ctx context.Context) ([]channel, error) {
	rows, err := a.db.Query(ctx, `select id, name, type, config from channels where enabled order by id`)
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

func (a *Alerter) send(ctx context.Context, ch channel, ev Event) error {
	url, body, contentType, err := payload(ch, ev)
	if err != nil {
		return err
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
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
		card := map[string]any{
			"@type":      "MessageCard",
			"@context":   "https://schema.org/extensions",
			"themeColor": themeColor(ev.Severity),
			"summary":    ev.Title,
			"title":      ev.Title,
			"text":       ev.Text,
		}
		if len(ev.Fields) > 0 {
			facts := make([]map[string]string, 0, len(ev.Fields))
			for k, v := range ev.Fields {
				facts = append(facts, map[string]string{"name": k, "value": v})
			}
			card["sections"] = []map[string]any{{"facts": facts}}
		}
		if ev.URL != "" {
			card["potentialAction"] = []map[string]any{{
				"@type":   "OpenUri",
				"name":    "Open monitor",
				"targets": []map[string]string{{"os": "default", "uri": ev.URL}},
			}}
		}
		body, err = json.Marshal(card)

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
