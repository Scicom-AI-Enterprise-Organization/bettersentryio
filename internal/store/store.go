// Package store owns the Postgres connection pool, schema migrations and
// first-boot bootstrap. Every other package talks to Postgres through here.
package store

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Shared advisory-lock keys. Concurrent replicas coordinate on these rather than
// on any external lock service.
const (
	lockMigrate  int64 = 8930_0001
	LockDetector int64 = 8930_0002
)

type DB struct {
	*pgxpool.Pool
}

func Open(ctx context.Context, url string, maxConns int32) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	cfg.MaxConns = maxConns
	cfg.MaxConnLifetime = time.Hour
	cfg.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}
	return &DB{Pool: pool}, nil
}

// WaitReady blocks until Postgres answers or the deadline passes. A database that
// is not up yet is a normal startup condition, not a reason to crash-loop.
func (db *DB) WaitReady(ctx context.Context, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	delay := 250 * time.Millisecond
	for {
		err := db.Ping(ctx)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			return fmt.Errorf("database not ready after %s: %w", limit, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		if delay < 2*time.Second {
			delay *= 2
		}
	}
}

// Migrate applies every embedded migration that has not run yet, holding an
// advisory lock so that concurrent replicas starting together cannot race.
func (db *DB) Migrate(ctx context.Context) ([]string, error) {
	conn, err := db.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `select pg_advisory_lock($1)`, lockMigrate); err != nil {
		return nil, fmt.Errorf("acquire migrate lock: %w", err)
	}
	defer conn.Exec(ctx, `select pg_advisory_unlock($1)`, lockMigrate) //nolint:errcheck

	if _, err := conn.Exec(ctx, `
		create table if not exists schema_migrations (
			name       text primary key,
			applied_at timestamptz not null default now()
		)`); err != nil {
		return nil, fmt.Errorf("create schema_migrations: %w", err)
	}

	applied := map[string]bool{}
	rows, err := conn.Query(ctx, `select name from schema_migrations`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return nil, err
		}
		applied[name] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	var ran []string
	for _, name := range names {
		if applied[name] {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return nil, err
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, string(body)); err != nil {
			tx.Rollback(ctx) //nolint:errcheck
			return nil, fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, `insert into schema_migrations (name) values ($1)`, name); err != nil {
			tx.Rollback(ctx) //nolint:errcheck
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit migration %s: %w", name, err)
		}
		ran = append(ran, name)
	}
	return ran, nil
}

type Bootstrap struct {
	ProjectSlug string
	PublicKey   string
	Created     bool
}

// EnsureDefaultProject creates the `default` project and an ingest key on first
// boot. The key is returned only when freshly generated, so startup logs print a
// usable DSN exactly once instead of leaking it on every restart.
func (db *DB) EnsureDefaultProject(ctx context.Context) (Bootstrap, error) {
	b := Bootstrap{ProjectSlug: "default"}

	var projectID int64
	err := db.QueryRow(ctx, `select id from projects where slug = 'default'`).Scan(&projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := db.QueryRow(ctx,
			`insert into projects (slug, name) values ('default', 'Default') returning id`,
		).Scan(&projectID); err != nil {
			return b, err
		}
	} else if err != nil {
		return b, err
	}

	var existing int
	if err := db.QueryRow(ctx,
		`select count(*) from ingest_keys where project_id = $1 and revoked_at is null`, projectID,
	).Scan(&existing); err != nil {
		return b, err
	}
	if existing > 0 {
		return b, nil
	}

	key, err := newKey()
	if err != nil {
		return b, err
	}
	if _, err := db.Exec(ctx,
		`insert into ingest_keys (project_id, public_key) values ($1, $2)`, projectID, key,
	); err != nil {
		return b, err
	}
	b.PublicKey, b.Created = key, true
	return b, nil
}

// ErrSlugTaken is returned when an app slug already exists.
var ErrSlugTaken = errors.New("slug already taken")

// CreateProject registers an app and mints its first ingest key. Each app gets its
// own key so a leaked one can be revoked without taking every other service down.
func (db *DB) CreateProject(ctx context.Context, name, platform string) (Bootstrap, error) {
	b := Bootstrap{}
	slug := Slugify(name)
	if slug == "" {
		return b, fmt.Errorf("name must contain at least one letter or digit")
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return b, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var projectID int64
	err = tx.QueryRow(ctx,
		`insert into projects (slug, name, platform) values ($1, $2, $3)
		 on conflict (slug) do nothing returning id`, slug, name, platform,
	).Scan(&projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return b, ErrSlugTaken
	}
	if err != nil {
		return b, err
	}

	key, err := newKey()
	if err != nil {
		return b, err
	}
	if _, err := tx.Exec(ctx,
		`insert into ingest_keys (project_id, public_key) values ($1, $2)`, projectID, key,
	); err != nil {
		return b, err
	}
	if err := tx.Commit(ctx); err != nil {
		return b, err
	}
	return Bootstrap{ProjectSlug: slug, PublicKey: key, Created: true}, nil
}

// Slugify is the single definition of how a display name becomes a slug, so the UI
// never has to guess what the engine will store.
func Slugify(name string) string {
	var b []rune
	lastDash := true // trims leading dashes
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b = append(b, r)
			lastDash = false
		case r == ' ' || r == '-' || r == '_' || r == '.' || r == '/':
			if !lastDash {
				b = append(b, '-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(string(b), "-")
	if len(out) > 64 {
		out = strings.Trim(out[:64], "-")
	}
	return out
}

// DeleteProject removes an app and everything under it, returning how many monitors
// went with it. The FKs cascade (monitors, keys, state, incidents, rollups), so this
// is one statement — but it is genuinely destructive, which is why the count comes
// back for the caller to show before and confirm after.
//
// Note it does not stop the app coming back: --project bootstraps a default on every
// start, so deleting that one is a reset, not a removal.
func (db *DB) DeleteProject(ctx context.Context, slug string) (monitors int64, found bool, err error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var projectID int64
	err = tx.QueryRow(ctx, `select id from projects where slug = $1`, slug).Scan(&projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}

	if err := tx.QueryRow(ctx,
		`select count(*) from monitors where project_id = $1`, projectID,
	).Scan(&monitors); err != nil {
		return 0, false, err
	}
	if _, err := tx.Exec(ctx, `delete from projects where id = $1`, projectID); err != nil {
		return 0, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, false, err
	}
	return monitors, true, nil
}

// EnsureChannel upserts a named alert channel. Used by --alert-webhook so a
// single flag is enough to get alerts flowing on a fresh install.
func (db *DB) EnsureChannel(ctx context.Context, name, kind, configJSON string) error {
	_, err := db.Exec(ctx, `
		insert into channels (name, type, config, enabled)
		values ($1, $2, $3::jsonb, true)
		on conflict (name) do update set type = excluded.type, config = excluded.config, enabled = true`,
		name, kind, configJSON)
	return err
}

// ProjectMeta returns a project's slug and display name, or empty strings if
// the id is unknown.
func (db *DB) ProjectMeta(ctx context.Context, id int64) (slug, name string, err error) {
	err = db.QueryRow(ctx, `select slug, name from projects where id = $1`, id).Scan(&slug, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", nil
	}
	return slug, name, err
}

// ChannelByName fetches one alert channel's config, for the settings UI.
func (db *DB) ChannelByName(ctx context.Context, name string) (kind string, config map[string]string, enabled, found bool, err error) {
	var raw []byte
	err = db.QueryRow(ctx,
		`select type, config, enabled from channels where name = $1`, name,
	).Scan(&kind, &raw, &enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, false, false, nil
	}
	if err != nil {
		return "", nil, false, false, err
	}
	config = map[string]string{}
	_ = json.Unmarshal(raw, &config)
	return kind, config, enabled, true, nil
}

// SetChannelEnabled flips a channel without losing its config. Disabling a
// missing channel is a no-op, not an error.
func (db *DB) SetChannelEnabled(ctx context.Context, name string, enabled bool) error {
	_, err := db.Exec(ctx, `update channels set enabled = $2 where name = $1`, name, enabled)
	return err
}

// ErrChannelNameTaken is returned when an alert channel name already exists.
var ErrChannelNameTaken = errors.New("channel name already taken")

// ChannelInfo is one row on the alert-channels settings page.
type ChannelInfo struct {
	ID      int64
	Name    string
	Kind    string
	URL     string
	Enabled bool
}

// ListChannels returns every alert channel, name order, URL pulled out of the
// config for display.
func (db *DB) ListChannels(ctx context.Context) ([]ChannelInfo, error) {
	rows, err := db.Query(ctx,
		`select id, name, type, coalesce(config->>'url', ''), enabled from channels order by name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ChannelInfo{}
	for rows.Next() {
		var c ChannelInfo
		if err := rows.Scan(&c.ID, &c.Name, &c.Kind, &c.URL, &c.Enabled); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CreateChannel adds a named channel, enabled. Names are the identity a human
// deletes by, so a duplicate is an error, not an upsert — EnsureChannel is the
// boot-flag path that wants upsert semantics.
func (db *DB) CreateChannel(ctx context.Context, name, kind, url string) (int64, error) {
	cfg, _ := json.Marshal(map[string]string{"url": url})
	var id int64
	err := db.QueryRow(ctx, `
		insert into channels (name, type, config, enabled)
		values ($1, $2, $3::jsonb, true)
		on conflict (name) do nothing
		returning id`, name, kind, string(cfg)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrChannelNameTaken
	}
	return id, err
}

// UpdateChannel patches a channel: nil fields keep their value. A rename onto
// an existing name reports ErrChannelNameTaken.
func (db *DB) UpdateChannel(ctx context.Context, id int64, name, url *string, enabled *bool) (bool, error) {
	tag, err := db.Exec(ctx, `
		update channels set
			name    = coalesce($2, name),
			config  = case when $3::text is null then config
			               else jsonb_set(config, '{url}', to_jsonb($3::text)) end,
			enabled = coalesce($4, enabled)
		where id = $1`, id, name, url, enabled)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return false, ErrChannelNameTaken
		}
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// DeleteChannel removes a channel and its delivery ledger (cascade).
func (db *DB) DeleteChannel(ctx context.Context, id int64) (bool, error) {
	tag, err := db.Exec(ctx, `delete from channels where id = $1`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ProjectIDForKey resolves an ingest key to its project, or 0 if unknown.
func (db *DB) ProjectIDForKey(ctx context.Context, key string) (int64, error) {
	var id int64
	err := db.QueryRow(ctx,
		`select project_id from ingest_keys where public_key = $1 and revoked_at is null`, key,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

func newKey() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
