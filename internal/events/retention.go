// Retention: the sweep that keeps the events table from being the thing that
// eventually takes the platform down.
//
// Per project and OFF by default — retention_days = 0 means keep forever. Deleting
// someone's error history is a decision, so it is made per project in its settings
// and lands in the audit log like any other admin action; the sweep only ever
// enforces what a person configured.
package events

import (
	"context"
	"time"

	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/store"
)

// sweepBatch bounds one DELETE. Retention on a table with months of backlog must not
// take a lock for minutes; many small deletes let vacuum and everyone else breathe.
const sweepBatch = 5000

// maxBatchesPerProject bounds one sweep's work per project. A project that just
// turned retention on over a huge history is drained across successive sweeps
// rather than in one marathon transaction chain.
const maxBatchesPerProject = 20

// Swept is what one sweep removed, for the log line and the metrics.
type Swept struct {
	Events      int64
	Attachments int64
	Issues      int64
	// Truncated: at least one project hit maxBatchesPerProject and still has
	// expired rows; the next sweep continues from where this one stopped.
	Truncated bool
}

// SweepRetention enforces every project's retention, under an advisory lock so
// concurrent replicas do not race each other into deadlocks on the same rows.
func (s *Store) SweepRetention(ctx context.Context) (Swept, error) {
	var out Swept
	lock, err := s.db.TryAdvisoryLock(ctx, store.LockRetention)
	if err != nil {
		return out, err
	}
	if lock == nil {
		return out, nil // another replica is sweeping; nothing to do is success
	}
	defer lock.Release()

	rows, err := s.db.Query(ctx,
		`select id, retention_days from projects where retention_days > 0`)
	if err != nil {
		return out, err
	}
	type target struct {
		id   int64
		days int
	}
	var targets []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.id, &t.days); err != nil {
			rows.Close()
			return out, err
		}
		targets = append(targets, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}

	for _, t := range targets {
		cutoff := time.Now().Add(-time.Duration(t.days) * 24 * time.Hour)

		// Events first, in batches. The join goes through issues because events has
		// no project_id of its own; events_received keeps the age filter off a
		// sequential scan.
		for i := 0; i < maxBatchesPerProject; i++ {
			tag, err := s.db.Exec(ctx, `
				delete from events where id in (
				    select e.id from events e
				    join issues i on i.id = e.issue_id
				    where i.project_id = $1 and e.received_at < $2
				    limit $3)`, t.id, cutoff, sweepBatch)
			if err != nil {
				return out, err
			}
			out.Events += tag.RowsAffected()
			if tag.RowsAffected() < sweepBatch {
				break
			}
			if i == maxBatchesPerProject-1 {
				out.Truncated = true
			}
		}

		tag, err := s.db.Exec(ctx,
			`delete from attachments where project_id = $1 and received_at < $2`, t.id, cutoff)
		if err != nil {
			return out, err
		}
		out.Attachments += tag.RowsAffected()

		// An issue whose last sighting is past the cutoff has no events left by now;
		// the row itself is the final thing retention removes. An issue still being
		// seen keeps its lifetime times_seen — history of the count survives even
		// where the event bodies do not, which is Sentry's semantics too.
		tag, err = s.db.Exec(ctx,
			`delete from issues where project_id = $1 and last_seen < $2`, t.id, cutoff)
		if err != nil {
			return out, err
		}
		out.Issues += tag.RowsAffected()
	}
	return out, nil
}
