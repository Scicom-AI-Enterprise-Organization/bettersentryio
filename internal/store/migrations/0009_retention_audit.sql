-- Retention and the audit log: the two tables an operator asks about the day this
-- carries someone else's production traffic.

-- Retention is per project and OFF by default (0 = keep forever). Off by default
-- because silently deleting somebody's error history is worse than a growing table —
-- turning it on is a decision, made in the project's settings, and recorded in the
-- audit log like any other admin action.
alter table projects add column if not exists retention_days integer not null default 0;
do $$ begin
    alter table projects add constraint projects_retention_sane
        check (retention_days >= 0 and retention_days <= 3650);
exception when duplicate_object then null; end $$;

-- The sweeper deletes by received_at per project; events joins through issues, so the
-- per-issue index (issue_id, received_at) does not help. This partial-free index is
-- what keeps an hourly sweep from being a sequential scan of the whole table.
create index if not exists events_received on events (received_at);
create index if not exists attachments_received on attachments (project_id, received_at);

-- Control-plane actions only: who created the app, who deleted the issue, who revoked
-- the token. Data-plane traffic (beats, envelopes) never lands here — at 80 rps it
-- would be the biggest table in the database by the end of the week.
create table if not exists audit_log (
    id          bigserial   primary key,
    at          timestamptz not null default now(),
    -- Who: an email when the UI forwarded the acting user, "operator" for the bare
    -- operator token, "token:<prefix>" for an API token, "key:<project>" for an
    -- ingest key on the dev-mode fallback.
    actor       text        not null,
    -- How they authenticated: session | operator | token | key.
    via         text        not null,
    -- What: "POST /api/0/apps", "DELETE /api/0/tokens/3" — method + concrete path.
    -- Derived from the request rather than hand-written per handler, so an endpoint
    -- added next month is audited without anyone remembering to say so.
    action      text        not null,
    status      integer     not null,
    remote_addr text        not null default '',
    detail      jsonb       not null default '{}'
);

-- The reading pattern is "most recent first, sometimes filtered by actor".
create index if not exists audit_log_recent on audit_log (at desc);
create index if not exists audit_log_actor on audit_log (actor, at desc);
