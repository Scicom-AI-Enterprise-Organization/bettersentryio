package alert

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/store"
)

// The digest flush is the one part of alerting that reads shared mutable state on a
// timer in *every* replica — the alerter runs everywhere, not only on the detector
// leader. Now that one-detector-per-database is a shipped configuration rather than a
// plan, "two replicas cannot both send the same digest" needs a test rather than a
// comment. A double flush means duplicate cards in somebody's chat, which is the exact
// flood the digest exists to prevent.
func digestDSN() string {
	if v := os.Getenv("BSIO_TEST_DATABASE_URL"); v != "" {
		return v
	}
	return "postgres://localhost/bettersentryio_test?sslmode=disable"
}

func openPool(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), digestDSN(), 5)
	if err != nil {
		t.Skipf("no test database (%v)", err)
	}
	if err := db.Ping(context.Background()); err != nil {
		db.Close()
		t.Skipf("test database unreachable at %s (%v)", digestDSN(), err)
	}
	t.Cleanup(db.Close)
	return db
}

// fixture returns two pools onto the same database with one project, one enabled
// project-scoped channel, and one due digest window holding two entries.
//
// Two *pools*, not two handles on one: pgx would hand both alerters connections from
// the same pool and the row lock would be uncontended, so the test would pass without
// proving anything about two processes.
func fixture(t *testing.T, sinkURL string) (a, b *store.DB, projectID, channelID int64) {
	t.Helper()
	ctx := context.Background()
	a = openPool(t)
	b = openPool(t)

	if _, err := a.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := a.Exec(ctx, `truncate projects, channels, notifications, alert_digests
		restart identity cascade`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if err := a.QueryRow(ctx,
		`insert into projects (slug, name) values ('two-replicas', 'Two Replicas') returning id`,
	).Scan(&projectID); err != nil {
		t.Fatalf("project: %v", err)
	}
	cfg, _ := json.Marshal(map[string]string{"url": sinkURL})
	if err := a.QueryRow(ctx, `
		insert into channels (name, type, config, enabled, project_id)
		values ('sink', 'webhook', $1::jsonb, true, $2) returning id`,
		string(cfg), projectID,
	).Scan(&channelID); err != nil {
		t.Fatalf("channel: %v", err)
	}
	// A window that closed a second ago, with a burst waiting in it.
	if _, err := a.Exec(ctx, `
		insert into alert_digests (project_id, channel_id, window_ends_at, pending, dropped)
		values ($1, $2, now() - interval '1 second',
		        '[{"kind":"issue.new","title":"one","text":"t","severity":"critical"},
		          {"kind":"issue.new","title":"two","text":"t","severity":"warning"}]'::jsonb, 0)`,
		projectID, channelID); err != nil {
		t.Fatalf("digest row: %v", err)
	}
	return a, b, projectID, channelID
}

func TestTwoReplicasFlushOneDigestOnce(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dbA, dbB, projectID, channelID := fixture(t, srv.URL)

	one := New(dbA, quiet(), 4)
	two := New(dbB, quiet(), 4)

	// Both sweep at the same instant, which is the case a shared ticker produces.
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, a := range []*Alerter{one, two} {
		wg.Add(1)
		go func(al *Alerter) {
			defer wg.Done()
			<-start
			al.flushDigests(context.Background())
		}(a)
	}
	close(start)
	wg.Wait()

	if got := hits.Load(); got != 1 {
		t.Fatalf("digest delivered %d times, want exactly 1 — two replicas double-sent", got)
	}
	// Between them they should count one send, not two.
	if sent := one.Sent() + two.Sent(); sent != 1 {
		t.Errorf("Sent() across replicas = %d, want 1", sent)
	}

	// The window must have been reopened empty, not deleted: the burst may still be hot.
	var (
		pending int
		ends    time.Time
	)
	if err := dbA.QueryRow(context.Background(), `
		select jsonb_array_length(pending), window_ends_at from alert_digests
		where project_id = $1 and channel_id = $2`, projectID, channelID,
	).Scan(&pending, &ends); err != nil {
		t.Fatalf("read window back: %v", err)
	}
	if pending != 0 {
		t.Errorf("pending = %d after the flush, want 0", pending)
	}
	if !ends.After(time.Now()) {
		t.Errorf("window_ends_at = %v, want a reopened window in the future", ends)
	}
}

// A second sweep after a flush must send nothing: the batch was claimed, and an empty
// window is closed rather than delivered as a card with no lines in it.
func TestFlushIsIdempotentOnAnEmptyWindow(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dbA, _, projectID, channelID := fixture(t, srv.URL)
	a := New(dbA, quiet(), 4)
	ctx := context.Background()

	a.flushDigests(ctx)
	if got := hits.Load(); got != 1 {
		t.Fatalf("first flush sent %d, want 1", got)
	}

	// Force the reopened window due again; there is nothing in it, so nothing to send.
	if _, err := dbA.Exec(ctx, `
		update alert_digests set window_ends_at = now() - interval '1 second'
		where project_id = $1 and channel_id = $2`, projectID, channelID); err != nil {
		t.Fatal(err)
	}
	a.flushDigests(ctx)
	if got := hits.Load(); got != 1 {
		t.Fatalf("an empty window sent a card: hits = %d, want still 1", got)
	}

	// And that sweep should have closed the window, so the next alert is immediate.
	var rows int
	if err := dbA.QueryRow(ctx,
		`select count(*) from alert_digests where project_id = $1 and channel_id = $2`,
		projectID, channelID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Errorf("quiet window still open (%d rows); the next alert would be digested", rows)
	}
}
