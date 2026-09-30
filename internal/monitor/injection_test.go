package monitor

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// VAPT SAST #39 flagged monitor_test.go -> detector.go:397 (the open-incident alert
// query). The query binds its only input ($1, the retry interval); this drives a
// monitor whose slug is an injection payload through the whole flagged path — beat,
// incident, the alert query, delivery — and checks the payload stays data.
func TestHostileMonitorSlugStaysDataThroughTheAlertQuery(t *testing.T) {
	e := newEnv(t)
	const slug = `job'); drop table incidents; --`

	var mu sync.Mutex
	var delivered []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var ev map[string]any
		_ = json.Unmarshal(body, &ev)
		mu.Lock()
		if m, ok := ev["monitor"].(string); ok {
			delivered = append(delivered, m)
		}
		mu.Unlock()
	}))
	defer srv.Close()
	blob, _ := json.Marshal(map[string]string{"url": srv.URL})
	if err := e.db.EnsureChannel(context.Background(), "probe", "webhook", string(blob)); err != nil {
		t.Fatalf("register channel: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); e.alerter.Run(ctx) }()
	defer func() { cancel(); <-done }()

	e.beat(t, slug, ptr(1), 10*time.Second, 10*time.Second, 0)
	e.clock.advance(30 * time.Second)
	e.tickNow(t)
	if n := e.openIncidents(t, slug); n != 1 {
		t.Fatalf("open incidents = %d, want 1", n)
	}
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(delivered) == 1
	}, "the alert for the hostile slug")

	mu.Lock()
	if delivered[0] != slug {
		t.Fatalf("alert named %q, want the slug verbatim %q", delivered[0], slug)
	}
	mu.Unlock()
	var exists bool
	if err := e.db.QueryRow(context.Background(),
		`select to_regclass('public.incidents') is not null`).Scan(&exists); err != nil || !exists {
		t.Fatalf("incidents table gone (exists=%v err=%v)", exists, err)
	}
}
