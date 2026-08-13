package monitor

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/alert"
	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/store"
)

// The state machine lives largely in SQL, so these run against a real Postgres.
// Set BSIO_TEST_DATABASE_URL to point somewhere disposable; the tables are
// truncated before every test.
func testDSN() string {
	if v := os.Getenv("BSIO_TEST_DATABASE_URL"); v != "" {
		return v
	}
	return "postgres://localhost/bettersentryio_test?sslmode=disable"
}

// clock is the simulated time shared by the engine and the detector, so a test
// can advance minutes instantly and still have beats land at plausible times.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type env struct {
	db        *store.DB
	engine    *Engine
	detector  *Detector
	alerter   *alert.Alerter
	projectID int64
	clock     *clock
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()

	db, err := store.Open(ctx, testDSN(), 5)
	if err != nil {
		t.Skipf("no test database (%v)", err)
	}
	if err := db.Ping(ctx); err != nil {
		db.Close()
		t.Skipf("test database unreachable at %s (%v)", testDSN(), err)
	}
	t.Cleanup(db.Close)

	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.Exec(ctx, `truncate projects, ingest_keys, monitors, monitor_state,
		incidents, beat_rollups, channels, notifications restart identity cascade`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	var projectID int64
	if err := db.QueryRow(ctx,
		`insert into projects (slug, name) values ('test', 'Test') returning id`).Scan(&projectID); err != nil {
		t.Fatalf("create project: %v", err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	alerter := alert.New(db, log, 64)
	clk := &clock{t: time.Now().UTC().Truncate(time.Second)}
	engine := NewEngine(db, alerter, log, "http://test")
	engine.SetClock(clk.now)
	return &env{
		db:        db,
		engine:    engine,
		detector:  NewDetector(db, alerter, log, 15*time.Second, "http://test"),
		alerter:   alerter,
		projectID: projectID,
		clock:     clk,
	}
}

// tickNow sweeps at the simulated present.
func (e *env) tickNow(t *testing.T) Stats {
	t.Helper()
	return e.tick(t, e.clock.now())
}

func (e *env) beat(t *testing.T, slug string, progress *int64, every, grace, stall time.Duration) BeatResult {
	t.Helper()
	res, err := e.engine.Beat(context.Background(), BeatRequest{
		ProjectID: e.projectID, Slug: slug, Progress: progress,
		ExpectedEvery: every, Grace: grace, StallWindow: stall,
	})
	if err != nil {
		t.Fatalf("beat %s: %v", slug, err)
	}
	return res
}

func (e *env) status(t *testing.T, slug string) string {
	t.Helper()
	var s string
	if err := e.db.QueryRow(context.Background(), `
		select ms.status from monitor_state ms
		join monitors m on m.id = ms.monitor_id
		where m.slug = $1`, slug).Scan(&s); err != nil {
		t.Fatalf("status %s: %v", slug, err)
	}
	return s
}

func (e *env) openIncidents(t *testing.T, slug string) int {
	t.Helper()
	var n int
	if err := e.db.QueryRow(context.Background(), `
		select count(*) from incidents i join monitors m on m.id = i.monitor_id
		where m.slug = $1 and i.resolved_at is null`, slug).Scan(&n); err != nil {
		t.Fatalf("count incidents: %v", err)
	}
	return n
}

func (e *env) tick(t *testing.T, now time.Time) Stats {
	t.Helper()
	stats, err := e.detector.Tick(context.Background(), now)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	return stats
}

func ptr(n int64) *int64 { return &n }

func TestBeatCreatesMonitorAndGoesOK(t *testing.T) {
	e := newEnv(t)
	res := e.beat(t, "tts-batcher", ptr(1), 30*time.Second, 30*time.Second, 0)
	if !res.Created {
		t.Error("first beat should create the monitor")
	}
	if res.Status != StatusOK {
		t.Errorf("status = %q, want ok", res.Status)
	}
	if got := e.status(t, "tts-batcher"); got != "ok" {
		t.Errorf("stored status = %q, want ok", got)
	}
}

// The TTS incident: beats stop, nothing else changes, and the monitor must be
// found by the sweep rather than by anyone noticing.
func TestDeadLoopBecomesMissingThenRecovers(t *testing.T) {
	e := newEnv(t)
	e.beat(t, "tts-batcher", ptr(1), 30*time.Second, 30*time.Second, 0)

	// Overdue but inside the grace window: visible, not yet an incident.
	e.clock.advance(45 * time.Second)
	e.tickNow(t)
	if got := e.status(t, "tts-batcher"); got != "late" {
		t.Fatalf("status at +45s = %q, want late", got)
	}
	if n := e.openIncidents(t, "tts-batcher"); n != 0 {
		t.Fatalf("open incidents at +45s = %d, want 0", n)
	}

	// Past expected + grace, it is missing and an incident is opened.
	e.clock.advance(25 * time.Second)
	stats := e.tickNow(t)
	if stats.Missing != 1 {
		t.Errorf("missing sweep hit %d rows, want 1", stats.Missing)
	}
	if got := e.status(t, "tts-batcher"); got != "missing" {
		t.Fatalf("status at +70s = %q, want missing", got)
	}
	if n := e.openIncidents(t, "tts-batcher"); n != 1 {
		t.Fatalf("open incidents = %d, want 1", n)
	}

	// Recovery happens on arrival, not on the next tick.
	res := e.beat(t, "tts-batcher", ptr(2), 30*time.Second, 30*time.Second, 0)
	if !res.Recovered {
		t.Error("beat after missing should report recovery")
	}
	if n := e.openIncidents(t, "tts-batcher"); n != 0 {
		t.Errorf("open incidents after recovery = %d, want 0", n)
	}
}

// Repeated sweeps must not pile up incidents for one outage.
func TestMissingIsIdempotentAcrossTicks(t *testing.T) {
	e := newEnv(t)
	e.beat(t, "job", ptr(1), 10*time.Second, 10*time.Second, 0)

	for i := 0; i < 3; i++ {
		e.clock.advance(30 * time.Second)
		e.tickNow(t)
	}
	if n := e.openIncidents(t, "job"); n != 1 {
		t.Errorf("open incidents after 3 sweeps = %d, want 1", n)
	}
}

// The vLLM incident: beats keep arriving on schedule for a full minute, so
// liveness looks perfect, but the progress counter never moves.
func TestBeatingButFrozenProgressBecomesStalled(t *testing.T) {
	e := newEnv(t)
	const (
		every = 10 * time.Second
		grace = 30 * time.Second
		stall = 60 * time.Second
	)
	e.beat(t, "vllm", ptr(500), every, grace, stall)

	for elapsed := time.Duration(0); elapsed < stall; elapsed += 5 * time.Second {
		e.clock.advance(5 * time.Second)
		e.beat(t, "vllm", ptr(500), every, grace, stall)
		e.tickNow(t)
		if got := e.status(t, "vllm"); got == "missing" {
			t.Fatalf("went missing at +%s while beats were arriving on time", elapsed)
		}
	}

	e.clock.advance(5 * time.Second)
	stats := e.tickNow(t)
	if stats.Stalled != 1 {
		t.Errorf("stall sweep hit %d rows, want 1", stats.Stalled)
	}
	if stats.Missing != 0 {
		t.Errorf("missing sweep hit %d rows, want 0 — beats were on time", stats.Missing)
	}
	if got := e.status(t, "vllm"); got != "stalled" {
		t.Fatalf("status after stall window = %q, want stalled", got)
	}
}

// stallOnce drives a monitor into the stalled state with beats arriving on time
// the whole way, mirroring how the vLLM case actually presents.
func (e *env) stallOnce(t *testing.T, slug string, progress int64, every, grace, stall time.Duration) {
	t.Helper()
	e.beat(t, slug, ptr(progress), every, grace, stall)
	for elapsed := time.Duration(0); elapsed <= stall; elapsed += every / 2 {
		e.clock.advance(every / 2)
		e.beat(t, slug, ptr(progress), every, grace, stall)
	}
	e.tickNow(t)
	if got := e.status(t, slug); got != "stalled" {
		t.Fatalf("setup: status = %q, want stalled", got)
	}
}

// Regression: a beat proves the loop is alive but not that it is working. Before
// this was fixed, every heartbeat cleared the stall and the monitor flapped
// between STALLED and a false "recovered" on each beat.
func TestBeatWithFrozenProgressDoesNotClearStall(t *testing.T) {
	e := newEnv(t)
	const (
		every = 10 * time.Second
		grace = 30 * time.Second
		stall = 60 * time.Second
	)
	e.stallOnce(t, "vllm", 500, every, grace, stall)

	e.clock.advance(5 * time.Second)
	res := e.beat(t, "vllm", ptr(500), every, grace, stall)
	if res.Recovered {
		t.Error("frozen-progress beat must not report recovery")
	}
	if res.Status != StatusStalled {
		t.Errorf("beat result status = %q, want stalled", res.Status)
	}
	if got := e.status(t, "vllm"); got != "stalled" {
		t.Errorf("stored status = %q, want stalled", got)
	}
	if n := e.openIncidents(t, "vllm"); n != 1 {
		t.Errorf("open incidents = %d, want 1 (still stalled)", n)
	}

	// Moving the counter is the only thing that clears it.
	e.clock.advance(5 * time.Second)
	res = e.beat(t, "vllm", ptr(501), every, grace, stall)
	if !res.Recovered {
		t.Error("progress beat should report recovery")
	}
	if got := e.status(t, "vllm"); got != "ok" {
		t.Errorf("status after progress = %q, want ok", got)
	}
}

// A counter that goes backwards means the process restarted, which is activity.
func TestCounterResetCountsAsProgress(t *testing.T) {
	e := newEnv(t)
	e.stallOnce(t, "vllm", 500, 10*time.Second, 30*time.Second, 60*time.Second)
	e.clock.advance(5 * time.Second)
	if res := e.beat(t, "vllm", ptr(0), 10*time.Second, 30*time.Second, 60*time.Second); !res.Recovered {
		t.Error("counter reset should clear the stall")
	}
}

// A stalled loop that then stops beating is dead, and should say so.
func TestStalledThenSilentBecomesMissing(t *testing.T) {
	e := newEnv(t)
	e.stallOnce(t, "vllm", 7, 10*time.Second, 10*time.Second, 30*time.Second)

	e.clock.advance(40 * time.Second)
	e.tickNow(t)
	if got := e.status(t, "vllm"); got != "missing" {
		t.Errorf("status = %q, want missing", got)
	}
	if n := e.openIncidents(t, "vllm"); n != 1 {
		t.Errorf("open incidents = %d, want 1 — reclassifying must not open a second", n)
	}
}

// Monitors that never report progress must never be stalled, only missing.
func TestLivenessOnlyMonitorNeverStalls(t *testing.T) {
	e := newEnv(t)
	e.beat(t, "nightly", nil, 10*time.Second, 3*time.Minute, 20*time.Second)
	e.clock.advance(90 * time.Second)
	stats := e.tickNow(t)
	if stats.Stalled != 0 {
		t.Errorf("stalled = %d, want 0 — no progress counter was ever reported", stats.Stalled)
	}
	if got := e.status(t, "nightly"); got == "stalled" {
		t.Error("liveness-only monitor should not be stalled")
	}
}

// A host that slept must not produce a burst of false alarms on the first tick back.
func TestClockGapReanchorsInsteadOfAlerting(t *testing.T) {
	e := newEnv(t)
	e.beat(t, "job", ptr(1), 30*time.Second, 30*time.Second, 0)

	e.tickNow(t)
	e.clock.advance(30 * time.Minute)
	stats := e.tickNow(t)
	if stats.Missing != 0 {
		t.Errorf("missing = %d, want 0 after a clock gap", stats.Missing)
	}
	if stats.Reanchored == 0 {
		t.Error("expected schedules to be re-anchored")
	}
	if got := e.status(t, "job"); got != "ok" {
		t.Errorf("status = %q, want ok", got)
	}
}

// Regression: a chat outage must delay an alert, never lose it. Before this was
// fixed, the detector stamped last_alert_at on the first attempt and never looked
// at the incident again, so a webhook that was down at that moment meant the alert
// was gone for good — the one failure a monitoring tool may not have.
func TestUndeliveredAlertIsRetriedUntilItLands(t *testing.T) {
	e := newEnv(t)

	var mu sync.Mutex
	var delivered []string
	up := false // the webhook starts down
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		ok := up
		if ok {
			body, _ := io.ReadAll(r.Body)
			var ev map[string]any
			_ = json.Unmarshal(body, &ev)
			delivered = append(delivered, ev["monitor"].(string))
		}
		mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	blob, _ := json.Marshal(map[string]string{"url": srv.URL})
	if err := e.db.EnsureChannel(context.Background(), "flaky", "webhook", string(blob)); err != nil {
		t.Fatalf("register channel: %v", err)
	}

	// Compress both timers so this exercises the retry path in about a second.
	e.alerter.SetRetryBackoff(5 * time.Millisecond)
	e.detector.SetAlertRetry(time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); e.alerter.Run(ctx) }()
	defer func() { cancel(); <-done }()

	// Take the monitor down while the webhook is unreachable.
	e.beat(t, "job", ptr(1), 10*time.Second, 10*time.Second, 0)
	e.clock.advance(30 * time.Second)
	e.tickNow(t)
	if n := e.openIncidents(t, "job"); n != 1 {
		t.Fatalf("open incidents = %d, want 1", n)
	}
	waitFor(t, func() bool { return e.alerter.Failed() >= 1 }, "delivery to fail")

	mu.Lock()
	if len(delivered) != 0 {
		t.Fatalf("delivered %d while the webhook was down", len(delivered))
	}
	mu.Unlock()

	// The webhook comes back. Nothing about the monitor changed, so only a retry
	// can get the alert out.
	mu.Lock()
	up = true
	mu.Unlock()

	// Retries are rate-limited against the database clock, so wait it out.
	time.Sleep(1200 * time.Millisecond)
	e.clock.advance(10 * time.Second)
	e.tickNow(t)

	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(delivered) == 1
	}, "the pending alert to be delivered on retry")
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// One transition must produce exactly one delivery per channel, however many
// times it is announced.
func TestAlertDeliveryIsDedupedPerTransition(t *testing.T) {
	e := newEnv(t)

	var mu sync.Mutex
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var ev map[string]any
		_ = json.Unmarshal(body, &ev)
		mu.Lock()
		got = append(got, ev["event"].(string))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	blob, _ := json.Marshal(map[string]string{"url": srv.URL})
	if err := e.db.EnsureChannel(context.Background(), "test", "webhook", string(blob)); err != nil {
		t.Fatalf("register channel: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); e.alerter.Run(ctx) }()

	ev := alert.Event{
		Kind: "monitor.missing", DedupKey: "incident:1:open",
		Monitor: "job", Environment: "production", Text: "down",
	}
	for i := 0; i < 4; i++ {
		e.alerter.Notify(ctx, ev)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n > 0 && e.alerter.Suppressed() >= 3 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Errorf("delivered %d times, want 1 (dedup key repeated 4x)", len(got))
	}
	if e.alerter.Suppressed() != 3 {
		t.Errorf("suppressed = %d, want 3", e.alerter.Suppressed())
	}
}
