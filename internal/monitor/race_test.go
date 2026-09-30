package monitor

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// VAPT SAST #46 (race condition across cmd/bettersentryio/main.go and detector.go:
// 116-206). The detector's loop writes its state while /-/health and the metrics
// gauges read it from request goroutines, and beats land from ingest goroutines. This
// runs all of that at once; under `go test -race` (CI's test.yml) any unsynchronised
// access fails the test.
func TestDetectorStateIsSafeUnderConcurrentReaders(t *testing.T) {
	e := newEnv(t)
	d := NewDetector(e.db, e.alerter, slog.New(slog.NewTextHandler(io.Discard, nil)),
		20*time.Millisecond, "http://test")

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); d.Run(ctx) }()

	// What /-/health and the gauges read, from several request goroutines.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				_ = d.Leading()
				_ = d.Ticks()
				_ = d.Failures()
				_ = d.LastError()
				_ = d.LastTickAge()
				_ = d.Interval()
			}
		}()
	}
	// Ingest, concurrently with the sweeps.
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; ctx.Err() == nil; j++ {
				_, _ = e.engine.Beat(ctx, BeatRequest{
					ProjectID: e.projectID, Slug: fmt.Sprintf("race-%d-%d", n, j%5),
					ExpectedEvery: time.Second,
				})
			}
		}(i)
	}

	waitFor(t, func() bool { return d.Leading() && d.Ticks() >= 5 }, "the detector to lead and sweep")
	cancel()
	wg.Wait()

	if f := d.Failures(); f != 0 {
		t.Fatalf("detector reported %d consecutive failures: %s", f, d.LastError())
	}
	if d.Leading() {
		t.Fatal("detector still claims the lock after Run returned")
	}
}
