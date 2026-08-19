-- SDK coverage (docs/design/sentry-compat.md): the envelope items we used to
-- accept-and-drop that carry data worth keeping.
--
-- release_health: hourly session counts per project/release/environment. The
-- Python SDK sends sessions BY DEFAULT (auto_session_tracking), so this table
-- fills itself in the moment the engine stops dropping them. Sentry's
-- crash-free rate is derived, not stored: 1 - crashed/total.
create table if not exists release_health (
    project_id  bigint not null references projects(id) on delete cascade,
    release     text not null default '',
    environment text not null default 'production',
    hour        timestamptz not null,
    exited      bigint not null default 0,
    errored     bigint not null default 0,
    crashed     bigint not null default 0,
    abnormal    bigint not null default 0,
    primary key (project_id, release, environment, hour)
);

-- attachments: files the SDK sent alongside an error event
-- (sentry_sdk.add_attachment). Small blobs in Postgres on purpose: one store,
-- one backup, and the 1 MB per-item cap is enforced at ingest.
create table if not exists attachments (
    id           bigserial primary key,
    project_id   bigint not null references projects(id) on delete cascade,
    event_uuid   text not null,
    filename     text not null,
    content_type text not null default 'application/octet-stream',
    size         bigint not null,
    data         bytea not null,
    received_at  timestamptz not null default now()
);
create index if not exists attachments_by_event on attachments (project_id, event_uuid);
