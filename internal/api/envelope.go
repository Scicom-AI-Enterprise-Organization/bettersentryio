package api

// Sentry-compatible ingest (PLAN.md D14, docs/design/sentry-compat.md).
//
// A stock sentry-sdk pointed at DSN
//
//	https://<ingest_key>@<host>/<project_id>
//
// POSTs envelopes to /api/<project_id>/envelope/ and nothing else. This file is
// that endpoint: auth from X-Sentry-Auth, gzip/deflate decoding, tolerant
// newline framing, and `event` items handed to the same pipeline as the native
// endpoint — with the SDK's own bytes stored as the payload, so everything it
// captured (locals, breadcrumbs, source context) survives verbatim.
//
// Wire contract per docs/research/sentry-ingest-protocol.md. The two rules that
// matter most: never 400 a well-formed envelope over its contents, and never
// make the SDK wait.

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/events"
)

const (
	// maxEnvelopeBytes caps the decompressed envelope; maxEnvelopeEventBytes caps
	// one event item. A sentry-sdk event with locals and breadcrumbs is ~50 KB;
	// a megabyte means something is wrong.
	maxEnvelopeBytes      = 20 << 20
	maxEnvelopeEventBytes = 1 << 20
	// Attachments live in Postgres, so per-item and per-envelope caps keep one
	// chatty SDK from turning the events database into a blob store.
	maxAttachmentBytes        = 1 << 20
	maxAttachmentsPerEnvelope = 5
)

// sentryKey extracts the DSN public key the SDK presented: the X-Sentry-Auth
// header ("Sentry sentry_key=..., sentry_version=7, ..."), or ?sentry_key= for
// clients that cannot set headers. sentry_secret is legacy and ignored.
func sentryKey(r *http.Request) string {
	h := r.Header.Get("X-Sentry-Auth")
	if rest, ok := strings.CutPrefix(h, "Sentry "); ok {
		for _, part := range strings.Split(rest, ",") {
			k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
			if ok && k == "sentry_key" {
				return strings.TrimSpace(v)
			}
		}
	}
	return r.URL.Query().Get("sentry_key")
}

// envelopeItem is one item of a Sentry envelope: a type, its payload bytes, and
// the two header fields only attachments carry.
type envelopeItem struct {
	Type        string
	Payload     []byte
	Filename    string
	ContentType string
}

// parseEnvelope splits Sentry's newline framing: one envelope-header line, then
// per item a header line followed by either exactly `length` payload bytes or,
// with no length, payload up to the next newline. Unknown header fields are
// ignored; unknown item types are the caller's problem (accept-and-drop).
func parseEnvelope(body []byte) (header json.RawMessage, items []envelopeItem, err error) {
	line, rest, _ := bytes.Cut(body, []byte{'\n'})
	if len(bytes.TrimSpace(line)) == 0 {
		header = json.RawMessage("{}")
	} else if !json.Valid(line) {
		return nil, nil, errors.New("envelope header is not JSON")
	} else {
		header = json.RawMessage(line)
	}

	for len(rest) > 0 {
		var hdrLine []byte
		hdrLine, rest, _ = bytes.Cut(rest, []byte{'\n'})
		if len(bytes.TrimSpace(hdrLine)) == 0 {
			continue // tolerate blank lines between items
		}
		var ih struct {
			Type        string `json:"type"`
			Length      *int64 `json:"length"`
			Filename    string `json:"filename"`
			ContentType string `json:"content_type"`
		}
		if err := json.Unmarshal(hdrLine, &ih); err != nil {
			return nil, nil, errors.New("item header is not JSON")
		}

		var payload []byte
		if ih.Length != nil {
			n := *ih.Length
			if n < 0 || n > int64(len(rest)) {
				return nil, nil, errors.New("item length exceeds envelope")
			}
			payload = rest[:n]
			rest = rest[n:]
			if len(rest) > 0 && rest[0] == '\n' {
				rest = rest[1:]
			}
		} else {
			payload, rest, _ = bytes.Cut(rest, []byte{'\n'})
		}
		items = append(items, envelopeItem{
			Type: ih.Type, Payload: payload,
			Filename: ih.Filename, ContentType: ih.ContentType,
		})
	}
	return header, items, nil
}

var errUnsupportedEncoding = errors.New("unsupported content-encoding")

// readEnvelopeBody reads and decodes the request body. gzip is what sentry-sdk
// sends by default (gzip -9); deflate/zlib and identity round out what the
// Python SDK can emit without optional modules. brotli/zstd would need a
// dependency — callers get a 415 and every SDK falls back to gzip when told
// nothing else, so this stays stdlib-only.
func readEnvelopeBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	var rd io.Reader = http.MaxBytesReader(w, r.Body, maxEnvelopeBytes)
	switch strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding"))) {
	case "", "identity":
	case "gzip":
		gz, err := gzip.NewReader(rd)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		rd = gz
	case "deflate":
		zr, err := zlib.NewReader(rd)
		if err != nil {
			return nil, err
		}
		defer zr.Close()
		rd = zr
	default:
		return nil, errUnsupportedEncoding
	}
	data, err := io.ReadAll(io.LimitReader(rd, maxEnvelopeBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxEnvelopeBytes {
		return nil, errors.New("envelope too large")
	}
	return data, nil
}

// envelopeLimiter is a per-project token bucket: generous enough that a healthy
// service never notices it, tight enough that a crash-looping one cannot flood
// Postgres. The SDK honors the 429 + Retry-After this produces.
type envelopeLimiter struct {
	mu      sync.Mutex
	buckets map[int64]*tokenBucket
}

type tokenBucket struct {
	tokens float64
	last   time.Time
}

const (
	envelopeRatePerSec = 50
	envelopeBurst      = 200
)

func (l *envelopeLimiter) allow(projectID int64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.buckets == nil {
		l.buckets = make(map[int64]*tokenBucket)
	}
	now := time.Now()
	b, ok := l.buckets[projectID]
	if !ok {
		b = &tokenBucket{tokens: envelopeBurst, last: now}
		l.buckets[projectID] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * envelopeRatePerSec
	if b.tokens > envelopeBurst {
		b.tokens = envelopeBurst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// handleEnvelope is the Sentry envelope endpoint.
func (s *Server) handleEnvelope(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(r.PathValue("projectID"), 10, 64)
	if err != nil || projectID <= 0 {
		writeErr(w, http.StatusNotFound, "no such project")
		return
	}

	key := sentryKey(r)
	if key == "" {
		writeErr(w, http.StatusUnauthorized, "missing sentry_key (X-Sentry-Auth or ?sentry_key=)")
		return
	}
	keyProject, err := s.db.ProjectIDForKey(r.Context(), key)
	if err != nil {
		s.log.Error("envelope key lookup failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if keyProject == 0 || keyProject != projectID {
		writeErr(w, http.StatusForbidden, "key does not belong to this project")
		return
	}

	if !s.envLimit.allow(projectID) {
		ingestRejected.With("rate_limited").Inc()
		w.Header().Set("Retry-After", "5")
		w.Header().Set("X-Sentry-Rate-Limits", "5:error:project")
		writeErr(w, http.StatusTooManyRequests, "rate limited")
		return
	}

	body, err := readEnvelopeBody(w, r)
	if errors.Is(err, errUnsupportedEncoding) {
		writeErr(w, http.StatusUnsupportedMediaType, "supported content-encodings: identity, gzip, deflate")
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, "unreadable body: "+err.Error())
		return
	}

	header, items, err := parseEnvelope(body)
	if err != nil {
		// Broken framing is the one thing that earns a 400; broken *contents* never do.
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	var envHeader struct {
		EventID string `json:"event_id"`
	}
	_ = json.Unmarshal(header, &envHeader)

	dropped := map[string]int{}
	responseID := envHeader.EventID
	attachmentsSaved := 0
	for _, item := range items {
		// Non-event items each map to what they actually are: sessions to
		// release health, check-ins to the beat pipeline, attachments to
		// storage, client reports to the log. Whatever remains (transactions,
		// spans, profiles — APM products we do not carry) is counted and
		// dropped: a new SDK item type must never break error delivery.
		switch item.Type {
		case "event":
			// handled below

		case "session":
			if c := events.ParseSessionItem(item.Payload, time.Now()); c != nil {
				if err := s.events.RecordSessions(r.Context(), projectID, *c); err != nil {
					s.log.Error("session record failed", "err", err)
				}
			}
			continue

		case "sessions":
			for _, c := range events.ParseSessionsItem(item.Payload, time.Now()) {
				if err := s.events.RecordSessions(r.Context(), projectID, c); err != nil {
					s.log.Error("session record failed", "err", err)
					break
				}
			}
			continue

		case "check_in":
			s.handleCheckInItem(r.Context(), projectID, item.Payload)
			continue

		case "attachment":
			switch {
			case envHeader.EventID == "":
				dropped["attachment:no_event"]++
			case len(item.Payload) > maxAttachmentBytes:
				dropped["attachment:too_large"]++
			case attachmentsSaved >= maxAttachmentsPerEnvelope:
				dropped["attachment:too_many"]++
			default:
				name := item.Filename
				if name == "" {
					name = "attachment"
				}
				if err := s.events.SaveAttachment(r.Context(), projectID,
					envHeader.EventID, name, item.ContentType, item.Payload); err != nil {
					s.log.Error("attachment save failed", "err", err)
				} else {
					attachmentsSaved++
				}
			}
			continue

		case "client_report":
			var cr struct {
				Discarded []struct {
					Reason   string `json:"reason"`
					Category string `json:"category"`
					Quantity int64  `json:"quantity"`
				} `json:"discarded_events"`
			}
			if json.Unmarshal(item.Payload, &cr) == nil {
				for _, d := range cr.Discarded {
					s.log.Info("sdk discarded events client-side",
						"project", projectID, "reason", d.Reason,
						"category", d.Category, "n", d.Quantity)
				}
			}
			continue

		default:
			dropped[item.Type]++
			continue
		}
		if len(item.Payload) > maxEnvelopeEventBytes {
			dropped["event:too_large"]++
			continue
		}
		var e events.Event
		if err := json.Unmarshal(item.Payload, &e); err != nil {
			dropped["event:malformed"]++
			continue
		}
		if e.Message == "" && e.Logentry != nil {
			e.Message = e.Logentry.Formatted
			if e.Message == "" {
				e.Message = e.Logentry.Message
			}
		}
		if e.Exception == nil && e.Message == "" {
			dropped["event:empty"]++
			continue
		}

		res, err := s.events.IngestRaw(r.Context(), projectID, &e, item.Payload)
		if err == nil {
			ingestEvents.Inc()
		}
		if err != nil {
			// Storage down: tell the SDK to retry the envelope rather than
			// silently eating it. Duplicates on retry group into the same issue.
			s.log.Error("envelope ingest failed", "err", err)
			writeErr(w, http.StatusServiceUnavailable, "could not record the event")
			return
		}
		if responseID == "" {
			responseID = e.EventID
		}
		if res.IsNew {
			s.log.Info("new issue", "issue", res.IssueID, "culprit", res.Culprit, "via", "envelope")
		}
		s.notifyIssue(r.Context(), projectID, res)
	}

	if len(dropped) > 0 {
		s.log.Debug("envelope items dropped", "project", projectID, "dropped", dropped)
	}
	if responseID == "" {
		var b [16]byte
		_, _ = rand.Read(b[:])
		responseID = hex.EncodeToString(b[:])
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": responseID})
}
