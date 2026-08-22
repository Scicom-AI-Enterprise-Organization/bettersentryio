-- Project-level alerting (Sentry's shape): /admin/alerts becomes a catalogue of
-- channel definitions, and each project chooses which of them it alerts to.
--
-- Two things change:
--   1. A channel has a scope. project_id null = global (a shared definition that
--      any project may import); project_id set = a channel owned by one project.
--   2. A global channel fires for a project only where project_channels says so.
--      Every existing (project, global channel) pair is backfilled below, so an
--      upgrade keeps alerting exactly as it did — the routing is merely explicit
--      now, and revocable per project.

alter table channels add column if not exists project_id bigint references projects (id) on delete cascade;

-- Names identify a channel to a human, and a human only ever sees one scope at a
-- time. Postgres treats nulls as distinct, so the global scope needs its own
-- partial index rather than a plain unique (project_id, name).
alter table channels drop constraint if exists channels_name_key;
create unique index if not exists channels_global_name on channels (name) where project_id is null;
create unique index if not exists channels_project_name on channels (project_id, name) where project_id is not null;
create index if not exists channels_project on channels (project_id) where project_id is not null;

-- Which projects have imported which global channel. The definition stays in one
-- place: editing the global URL changes it for every importer, which is the whole
-- point of importing rather than copying.
create table if not exists project_channels (
    project_id bigint      not null references projects (id) on delete cascade,
    channel_id bigint      not null references channels (id) on delete cascade,
    created_at timestamptz not null default now(),
    primary key (project_id, channel_id)
);

insert into project_channels (project_id, channel_id)
select p.id, c.id from projects p cross join channels c where c.project_id is null
on conflict do nothing;

-- Alert patience, Sentry's action interval by another name. The first alert in a
-- quiet window goes out immediately; everything that arrives while the window is
-- open is collected and delivered as one digest when it closes. 0 disables it and
-- restores send-everything-immediately.
alter table projects add column if not exists alert_patience_seconds integer not null default 600;
do $$ begin
    alter table projects add constraint projects_alert_patience_sane
        check (alert_patience_seconds >= 0 and alert_patience_seconds <= 86400);
exception when duplicate_object then null; end $$;

-- One row per (project, channel) with an open patience window. Its existence IS
-- the window: inserting it is how a sender learns it is the first alert and may
-- send now, and the row's pending array is where the rest of the burst lands.
-- Kept in Postgres so two replicas cannot both send the digest (D2a: no cache tier).
create table if not exists alert_digests (
    project_id     bigint      not null references projects (id) on delete cascade,
    channel_id     bigint      not null references channels (id) on delete cascade,
    window_ends_at timestamptz not null,
    -- [{kind,title,text,severity,url}, …], capped by the alerter.
    pending        jsonb       not null default '[]',
    dropped        integer     not null default 0,
    primary key (project_id, channel_id)
);

create index if not exists alert_digests_due on alert_digests (window_ends_at);
