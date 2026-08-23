package store

import (
	"context"
	"os"
	"testing"
	"time"
)

// The window and both keyset directions live in SQL, so these run against a real
// Postgres. Set BSIO_TEST_DATABASE_URL to somewhere disposable; audit_log is
// truncated before each test.
func testDSN() string {
	if v := os.Getenv("BSIO_TEST_DATABASE_URL"); v != "" {
		return v
	}
	return "postgres://localhost/bettersentryio_test?sslmode=disable"
}

func auditDB(t *testing.T) *DB {
	t.Helper()
	ctx := context.Background()
	db, err := Open(ctx, testDSN(), 5)
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
	if _, err := db.Exec(ctx, `truncate audit_log restart identity`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return db
}

// seed writes n rows one hour apart, oldest first, so ids ascend with time the way
// they do in production.
func seed(t *testing.T, db *DB, n int, base time.Time) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		if _, err := db.Exec(ctx, `
			insert into audit_log (at, actor, via, action, status)
			values ($1, $2, 'session', $3, 200)`,
			base.Add(time.Duration(i)*time.Hour),
			"user@example.com",
			"POST /api/0/thing",
		); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
}

// cursorFor builds the page cursor for a row the way the API layer does: both halves
// of the sort key, read back from the row itself.
func cursorFor(t *testing.T, db *DB, id int64) *Cursor {
	t.Helper()
	var c Cursor
	if err := db.QueryRow(context.Background(),
		`select at, id from audit_log where id = $1`, id).Scan(&c.At, &c.ID); err != nil {
		t.Fatalf("cursor for id %d: %v", id, err)
	}
	return &c
}

func ids(p Page) []int64 {
	out := make([]int64, 0, len(p.Entries))
	for _, e := range p.Entries {
		out = append(out, e.ID)
	}
	return out
}

func TestListAuditNewestFirstAndPagesOlder(t *testing.T) {
	db := auditDB(t)
	ctx := context.Background()
	seed(t, db, 10, time.Now().UTC().Add(-20*time.Hour))

	first, err := db.ListAudit(ctx, AuditQuery{Limit: 4})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(first); len(got) != 4 || got[0] != 10 || got[3] != 7 {
		t.Fatalf("first page = %v, want newest four 10..7", got)
	}
	if !first.HasOlder {
		t.Error("HasOlder should be true with 10 rows and a page of 4")
	}
	if first.HasNewer {
		t.Error("the first page has nothing newer")
	}

	// Walk older using the last id on the page as the cursor.
	second, err := db.ListAudit(ctx, AuditQuery{Limit: 4, Before: cursorFor(t, db, 7)})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(second); len(got) != 4 || got[0] != 6 || got[3] != 3 {
		t.Fatalf("second page = %v, want 6..3", got)
	}
	if !second.HasNewer {
		t.Error("a page reached by a cursor has newer rows behind it")
	}

	last, err := db.ListAudit(ctx, AuditQuery{Limit: 4, Before: cursorFor(t, db, 3)})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(last); len(got) != 2 || got[0] != 2 {
		t.Fatalf("last page = %v, want the final two 2,1", got)
	}
	if last.HasOlder {
		t.Error("nothing is older than the last page")
	}
}

// Walking back must land on the rows immediately newer than the cursor, in display
// order — not on the newest rows in the table.
func TestListAuditPagesNewer(t *testing.T) {
	db := auditDB(t)
	ctx := context.Background()
	seed(t, db, 10, time.Now().UTC().Add(-20*time.Hour))

	back, err := db.ListAudit(ctx, AuditQuery{Limit: 4, After: cursorFor(t, db, 3)})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(back); len(got) != 4 || got[0] != 7 || got[3] != 4 {
		t.Fatalf("after=3 gave %v, want the four nearest-newer 7..4 in display order", got)
	}
	if !back.HasNewer {
		t.Error("rows 8..10 are still newer")
	}
	if !back.HasOlder {
		t.Error("rows 1..3 are still older")
	}

	// Arriving at the top: fewer rows than asked for, so nothing newer remains.
	top, err := db.ListAudit(ctx, AuditQuery{Limit: 4, After: cursorFor(t, db, 8)})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(top); len(got) != 2 || got[0] != 10 || got[1] != 9 {
		t.Fatalf("after=8 gave %v, want 10,9", got)
	}
	if top.HasNewer {
		t.Error("nothing is newer than the newest row")
	}
}

func TestListAuditWindow(t *testing.T) {
	db := auditDB(t)
	ctx := context.Background()
	base := time.Now().UTC().Add(-10 * time.Hour)
	seed(t, db, 10, base) // rows at base+0h … base+9h

	since := base.Add(4*time.Hour - time.Minute)
	page, err := db.ListAudit(ctx, AuditQuery{Limit: 100, Since: &since})
	if err != nil {
		t.Fatal(err)
	}
	// Rows 5..10 are at base+4h..base+9h.
	if len(page.Entries) != 6 {
		t.Fatalf("since base+4h gave %d rows, want 6: %v", len(page.Entries), ids(page))
	}

	until := base.Add(2*time.Hour + time.Minute)
	page, err = db.ListAudit(ctx, AuditQuery{Limit: 100, Since: &since, Until: &until})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 0 {
		t.Fatalf("a window with since > until must be empty, got %v", ids(page))
	}

	only := base.Add(-time.Hour)
	page, err = db.ListAudit(ctx, AuditQuery{Limit: 100, Until: &only})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 0 {
		t.Fatalf("until before every row must be empty, got %v", ids(page))
	}
}

// The filters and the window have to compose, or a filtered view silently ignores
// the window the URL is carrying.
func TestListAuditFiltersComposeWithWindow(t *testing.T) {
	db := auditDB(t)
	ctx := context.Background()
	base := time.Now().UTC().Add(-5 * time.Hour)
	seed(t, db, 4, base)
	if _, err := db.Exec(ctx, `
		insert into audit_log (at, actor, via, action, status)
		values ($1, 'other@example.com', 'session', 'DELETE /api/0/apps/x', 403)`,
		base.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	since := base.Add(30 * time.Minute)
	page, err := db.ListAudit(ctx, AuditQuery{
		Limit: 100, Actor: "other@example.com", Action: "DELETE", Since: &since,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 || page.Entries[0].Status != 403 {
		t.Fatalf("actor+action+window gave %v, want the single denied delete", ids(page))
	}

	// The same actor outside the window must not come back.
	future := base.Add(4 * time.Hour)
	page, err = db.ListAudit(ctx, AuditQuery{
		Limit: 100, Actor: "other@example.com", Since: &future,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 0 {
		t.Fatalf("window must still apply with an actor filter, got %v", ids(page))
	}
}

func TestListAuditDefaultsAndCaps(t *testing.T) {
	db := auditDB(t)
	ctx := context.Background()
	seed(t, db, 3, time.Now().UTC().Add(-3*time.Hour))

	for _, limit := range []int{0, -5, 5000} {
		page, err := db.ListAudit(ctx, AuditQuery{Limit: limit})
		if err != nil {
			t.Fatalf("limit %d: %v", limit, err)
		}
		if len(page.Entries) != 3 {
			t.Errorf("limit %d returned %d rows, want all 3", limit, len(page.Entries))
		}
	}
}

// The bug this design exists to prevent.
//
// Rows written newest-first — a backfill, an import, anything that sets `at` explicitly
// — make id order disagree with `at` order. An id-only cursor then means a different
// position than the ORDER BY does, and paging repeats rows: measured at 19 rows
// collected across two pages of which only 10 were distinct, before the cursor carried
// both halves of the sort key.
func TestListAuditPagesCorrectlyWhenIDOrderFightsTimeOrder(t *testing.T) {
	db := auditDB(t)
	ctx := context.Background()

	// Inserted newest-first, so id 1 is the newest row and id 12 the oldest.
	base := time.Now().UTC()
	for i := 0; i < 12; i++ {
		if _, err := db.Exec(ctx, `
			insert into audit_log (at, actor, via, action, status)
			values ($1, 'backfill@example.com', 'session', 'POST /api/0/thing', 200)`,
			base.Add(-time.Duration(i)*time.Hour)); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	var collected []int64
	page, err := db.ListAudit(ctx, AuditQuery{Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	for guard := 0; ; guard++ {
		if guard > 10 {
			t.Fatal("paging did not terminate")
		}
		got := ids(page)
		if len(got) == 0 {
			t.Fatal("a page with has_older true returned nothing")
		}
		collected = append(collected, got...)
		if !page.HasOlder {
			break
		}
		last := page.Entries[len(page.Entries)-1]
		page, err = db.ListAudit(ctx, AuditQuery{
			Limit:  5,
			Before: &Cursor{At: last.At, ID: last.ID},
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	if len(collected) != 12 {
		t.Fatalf("collected %d rows across all pages, want each of the 12 exactly once: %v",
			len(collected), collected)
	}
	seen := map[int64]bool{}
	for _, id := range collected {
		if seen[id] {
			t.Fatalf("row %d appeared twice; paging is repeating rows: %v", id, collected)
		}
		seen[id] = true
	}
	// Newest first means ascending id here, because the seed inverted the two.
	for i := 1; i < len(collected); i++ {
		if collected[i] < collected[i-1] {
			t.Fatalf("out of chronological order at %d: %v", i, collected)
		}
	}
}
