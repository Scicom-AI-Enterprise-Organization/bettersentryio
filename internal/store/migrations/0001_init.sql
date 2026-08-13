-- M0/M1 schema: projects, ingest keys, loop monitors, incidents, alert channels.
-- Error tracking tables (groups, group_hashes, events) arrive with M2.

create table projects (
    id         bigserial primary key,
    slug       text        not null unique,
    name       text        not null,
    created_at timestamptz not null default now()
);

create table ingest_keys (
    id         bigserial primary key,
    project_id bigint      not null references projects (id) on delete cascade,
    public_key text        not null unique,
    created_at timestamptz not null default now(),
    revoked_at timestamptz
);

create table monitors (
    id         bigserial primary key,
    project_id bigint      not null references projects (id) on delete cascade,
    slug       text        not null,
    kind       text        not null check (kind in ('cron', 'loop')),
    name       text        not null,
    config     jsonb       not null default '{}',
    disabled   boolean     not null default false,
    muted      boolean     not null default false,
    created_at timestamptz not null default now(),
    unique (project_id, slug)
);

-- One row per (monitor, environment). This table is the detector's whole working set.
create table monitor_state (
    monitor_id               bigint      not null references monitors (id) on delete cascade,
    environment              text        not null default 'production',
    status                   text        not null check (status in ('waiting', 'ok', 'late', 'missing', 'stalled')),
    last_beat_at             timestamptz,
    last_progress            bigint,
    -- Anchor for stall detection: only advances when the progress counter advances,
    -- so `now() - window_started_at` is the age of the last real unit of work.
    progress_at_window_start bigint,
    window_started_at        timestamptz,
    next_expected_at         timestamptz,
    next_expected_latest     timestamptz,
    updated_at               timestamptz not null default now(),
    primary key (monitor_id, environment)
);

-- Partial indexes: only rows the detector can still act on are indexed at all.
create index monitor_state_miss_scan on monitor_state (next_expected_latest)
    where status in ('ok', 'late');
create index monitor_state_stall_scan on monitor_state (window_started_at)
    where status in ('ok', 'late');

create table incidents (
    monitor_id    bigint      not null references monitors (id) on delete cascade,
    id            bigserial primary key,
    environment   text        not null,
    kind          text        not null check (kind in ('missing', 'stalled', 'failed_checkins')),
    opened_at     timestamptz not null default now(),
    resolved_at   timestamptz,
    last_alert_at timestamptz
);

-- At most one open incident per monitor environment, enforced by the DB.
create unique index incidents_one_open on incidents (monitor_id, environment)
    where resolved_at is null;

create table beat_rollups (
    monitor_id     bigint      not null references monitors (id) on delete cascade,
    environment    text        not null,
    window_start   timestamptz not null,
    beats          integer     not null default 0,
    progress_delta bigint      not null default 0,
    primary key (monitor_id, environment, window_start)
);

create table channels (
    id      bigserial primary key,
    name    text    not null unique,
    type    text    not null check (type in ('webhook', 'slack', 'teams', 'telegram')),
    config  jsonb   not null,
    enabled boolean not null default true
);

-- The dedup ledger: one delivery per (transition, channel), enforced by the unique index.
-- This is what makes alerting idempotent across restarts and concurrent replicas.
create table notifications (
    id         bigserial primary key,
    dedup_key  text        not null,
    channel_id bigint      not null references channels (id) on delete cascade,
    sent_at    timestamptz not null default now(),
    unique (dedup_key, channel_id)
);
