package monitor

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/alert"
	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/store"
)

// awaitCond polls until the condition holds or the deadline passes. The election is
// tick-driven, so assertions have to wait for a tick rather than for wall time.
// (monitor_test.go already owns the name waitFor with a different shape.)
func awaitCond(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// Two detectors, one database: exactly one may sweep, and the standby takes over
// when the leader goes away. This is the invariant ARCHITECTURE promises for a
// two-replica install, and it was a lie until LockDetector was actually used —
// the constant existed, nothing acquired it.
func TestDetectorLeaderElection(t *testing.T) {
	env := newEnv(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	// A second, independent pool: two replicas of the engine are two processes with
	// two pools, and a session advisory lock is per connection — one shared pool
	// would let both "replicas" ride the same session.
	db2, err := store.Open(context.Background(), testDSN(), 5)
	if err != nil {
		t.Skipf("no second pool (%v)", err)
	}
	t.Cleanup(db2.Close)

	a := env.alerter
	b := alert.New(db2, log, 64)

	d1 := NewDetector(env.db, a, log, 50*time.Millisecond, "http://one")
	d2 := NewDetector(db2, b, log, 50*time.Millisecond, "http://two")

	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel1()
	defer cancel2()

	go d1.Run(ctx1)
	// d1 gets a head start so the election is deterministic; the property under
	// test is exclusivity and failover, not who wins a coin toss.
	awaitCond(t, 5*time.Second, "d1 to lead", d1.Leading)

	go d2.Run(ctx2)
	awaitCond(t, 5*time.Second, "d2 to reach standby", func() bool { return d2.Ticks() >= 0 })
	// Give d2 several ticks' worth of chances to (wrongly) grab the lock.
	time.Sleep(300 * time.Millisecond)

	if d2.Leading() {
		t.Fatal("both replicas lead: the lock is not exclusive")
	}
	if d2.Ticks() != 0 {
		t.Fatalf("standby swept %d times; a standby must not sweep", d2.Ticks())
	}
	if !d1.Leading() {
		t.Fatal("d1 lost the lock without dying")
	}

	// Graceful failover: the leader stops, Run releases the lock on the way out,
	// and the standby must take over within a couple of ticks.
	cancel1()
	awaitCond(t, 5*time.Second, "d2 to take over after d1 stopped", d2.Leading)
	awaitCond(t, 5*time.Second, "d2 to sweep as the new leader", func() bool { return d2.Ticks() > 0 })

	if d1.Leading() {
		t.Fatal("a stopped detector still claims leadership")
	}
}
