package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// TokenPrefix marks a string as one of our auth tokens. It exists so a credential can
// be routed to the right lookup by shape rather than by trying every table: an ingest
// key and an auth token both arrive as a bearer token, and only one of them may read
// across every project.
const TokenPrefix = "bsiot_"

// APIToken is a token as it can safely be described: everything except the secret.
type APIToken struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

// CreateAPIToken mints a token and returns the plaintext, which is the only time it
// exists anywhere but the caller's screen: the row keeps a hash. Losing it means
// minting another — the same trade Sentry makes, and the reason the UI says so loudly.
func (db *DB) CreateAPIToken(ctx context.Context, name string) (plaintext string, out APIToken, err error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", out, errors.New("a token needs a name")
	}
	if len(name) > 128 {
		return "", out, errors.New("name must be 128 characters or fewer")
	}

	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", out, fmt.Errorf("generate token: %w", err)
	}
	plaintext = TokenPrefix + hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(plaintext))
	// Long enough to identify a row at a glance, short enough to be useless alone.
	prefix := plaintext[:len(TokenPrefix)+8]

	err = db.QueryRow(ctx, `
		insert into api_tokens (name, token_hash, prefix)
		values ($1, $2, $3)
		returning id, name, prefix, created_at, last_used_at, revoked_at`,
		name, sum[:], prefix,
	).Scan(&out.ID, &out.Name, &out.Prefix, &out.CreatedAt, &out.LastUsedAt, &out.RevokedAt)
	if err != nil {
		return "", out, err
	}
	return plaintext, out, nil
}

// ListAPITokens returns live tokens first, then revoked ones — a revoked row is kept
// so that "who had access, and when did it stop" survives the revocation.
func (db *DB) ListAPITokens(ctx context.Context) ([]APIToken, error) {
	rows, err := db.Query(ctx, `
		select id, name, prefix, created_at, last_used_at, revoked_at
		from api_tokens
		order by revoked_at nulls first, created_at desc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []APIToken{}
	for rows.Next() {
		var t APIToken
		if err := rows.Scan(&t.ID, &t.Name, &t.Prefix, &t.CreatedAt, &t.LastUsedAt, &t.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeAPIToken stamps a token dead. Not a delete: the row is the record that the
// token existed. Revoking an already-revoked token is not an error worth raising, so
// the original stamp is left alone and the caller still hears "found".
func (db *DB) RevokeAPIToken(ctx context.Context, id int64) (found bool, err error) {
	tag, err := db.Exec(ctx,
		`update api_tokens set revoked_at = now() where id = $1 and revoked_at is null`, id)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() > 0 {
		return true, nil
	}
	// Tell "no such token" apart from "already revoked", so the API can 404 honestly.
	var exists bool
	err = db.QueryRow(ctx, `select true from api_tokens where id = $1`, id).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return exists, err
}

// APITokenValid reports whether a presented token is live, and records the use.
//
// The hash is the lookup key, so authentication is an index probe rather than a string
// comparison: there is no timing signal to leak, and the token itself is not in the
// database to be compared against in the first place.
func (db *DB) APITokenValid(ctx context.Context, plaintext string) (bool, error) {
	if !strings.HasPrefix(plaintext, TokenPrefix) {
		return false, nil
	}
	sum := sha256.Sum256([]byte(plaintext))
	var id int64
	err := db.QueryRow(ctx,
		`select id from api_tokens where token_hash = $1 and revoked_at is null`, sum[:]).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	// Throttled inside the statement: a dashboard polling every ten seconds must not
	// turn every read into a write, and doing the throttling in the predicate leaves
	// no read-then-write race between replicas.
	if _, err := db.Exec(ctx, `
		update api_tokens set last_used_at = now()
		where id = $1 and (last_used_at is null or last_used_at < now() - interval '1 minute')`,
		id); err != nil {
		// Failing to record the use must not fail the request it was recording.
		return true, nil //nolint:nilerr
	}
	return true, nil
}
