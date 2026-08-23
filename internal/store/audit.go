package store

import (
	"context"
	"encoding/json"
	"time"
)

// AuditEntry is one control-plane action: who did what, how they were
// authenticated, and what the engine answered.
type AuditEntry struct {
	ID         int64           `json:"id"`
	At         time.Time       `json:"at"`
	Actor      string          `json:"actor"`
	Via        string          `json:"via"`
	Action     string          `json:"action"`
	Status     int             `json:"status"`
	RemoteAddr string          `json:"remote_addr"`
	Detail     json.RawMessage `json:"detail"`
}

func (db *DB) InsertAudit(ctx context.Context, e AuditEntry) error {
	if len(e.Detail) == 0 {
		e.Detail = json.RawMessage(`{}`)
	}
	_, err := db.Exec(ctx, `
		insert into audit_log (actor, via, action, status, remote_addr, detail)
		values ($1, $2, $3, $4, $5, $6)`,
		e.Actor, e.Via, e.Action, e.Status, e.RemoteAddr, e.Detail)
	return err
}

// Cursor is a position in the log: the sort key of a row, both halves of it.
//
// Both halves are needed because the order is (at desc, id desc). An id alone is only
// a valid cursor while id order and `at` order agree, which holds for rows InsertAudit
// writes — it takes `at` from the column default — but silently stops holding the moment
// anything backfills history with explicit timestamps. Paging then repeats or skips
// rows, which in an audit log is the one bug that matters, because the whole value of
// the thing is being able to say you saw everything.
type Cursor struct {
	At time.Time
	ID int64
}

// AuditQuery narrows the log. Every field is optional; the zero value is "the most
// recent page of everything".
type AuditQuery struct {
	Actor  string
	Action string
	// Since/Until bound `at`. A log grows without limit, so callers page a window
	// rather than the whole table.
	Since *time.Time
	Until *time.Time
	Limit int
	// Keyset cursors, at most one set. Before walks toward older rows, After back
	// toward newer ones.
	//
	// Keyset rather than offset because this table is appended to while somebody is
	// reading it: with OFFSET, a row inserted at the top shifts page 2 down and the
	// reader silently re-reads a row they already saw, or skips one they did not.
	Before *Cursor
	After  *Cursor
}

// Page is a window onto the log plus whether there is more in either direction.
type Page struct {
	Entries  []AuditEntry `json:"entries"`
	HasOlder bool         `json:"has_older"`
	HasNewer bool         `json:"has_newer"`
}

// ListAudit returns one page, newest first. actor and action filter when non-empty;
// action matches as a prefix so "DELETE" finds every deletion and "POST /api/0/tokens"
// finds exactly token minting.
//
// Both directions fetch one row more than asked for: whether that extra row exists is
// the answer to "is there another page", measured rather than guessed.
func (db *DB) ListAudit(ctx context.Context, q AuditQuery) (Page, error) {
	limit := q.Limit
	if limit <= 0 || limit > 1000 {
		limit = 100
	}

	// Walking toward newer rows reads ascending to find the *nearest* newer rows
	// rather than the newest ones, then flips back to display order.
	ascending := q.After != nil
	order := "desc"
	if ascending {
		order = "asc"
	}

	// Row-value comparison against the whole sort key, so the cursor means the same
	// thing the ORDER BY does no matter how `at` and id relate.
	var (
		beforeAt, afterAt *time.Time
		beforeID, afterID int64
	)
	if q.Before != nil {
		beforeAt, beforeID = &q.Before.At, q.Before.ID
	}
	if q.After != nil {
		afterAt, afterID = &q.After.At, q.After.ID
	}

	sql := `
		select id, at, actor, via, action, status, remote_addr, detail
		from audit_log
		where ($1 = '' or actor = $1)
		  and ($2 = '' or action like $2 || '%')
		  and ($3::timestamptz is null or at >= $3)
		  and ($4::timestamptz is null or at <= $4)
		  and ($5::timestamptz is null or (at, id) < ($5, $6))
		  and ($7::timestamptz is null or (at, id) > ($7, $8))
		order by at ` + order + `, id ` + order + `
		limit $9`

	rows, err := db.Query(ctx, sql,
		q.Actor, q.Action, q.Since, q.Until,
		beforeAt, beforeID, afterAt, afterID, limit+1)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()

	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.At, &e.Actor, &e.Via, &e.Action, &e.Status, &e.RemoteAddr, &e.Detail); err != nil {
			return Page{}, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return Page{}, err
	}

	more := len(out) > limit
	if more {
		out = out[:limit]
	}

	page := Page{Entries: out}
	if ascending {
		// Reverse into newest-first for display. The extra row proved there are still
		// newer rows beyond this page; older ones exist because we came from there.
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
		page.HasNewer = more
		page.HasOlder = true
	} else {
		page.HasOlder = more
		// Descending with a cursor means somebody paged forward to get here, so newer
		// rows exist by construction. Without a cursor this is the first page.
		page.HasNewer = q.Before != nil
	}
	return page, nil
}
