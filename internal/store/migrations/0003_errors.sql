-- Error tracking (M2). Two tables, because the useful unit is not the event: ten
-- thousand occurrences of one bug are one thing to fix, so events group into issues
-- and the issue is what a person reads.

create table if not exists issues (
    id          bigserial   primary key,
    project_id  bigint      not null references projects (id) on delete cascade,
    -- Stable across line-number shifts and across occurrences; see internal/events.
    fingerprint text        not null,
    environment text        not null default 'production',
    kind        text        not null,          -- exception type, e.g. RuntimeError
    culprit     text        not null,          -- where it happened: module in function
    title       text        not null,          -- the line shown in the list
    level       text        not null default 'error',
    times_seen  bigint      not null default 0,
    first_seen  timestamptz not null default now(),
    last_seen   timestamptz not null default now(),
    resolved_at timestamptz,
    -- One issue per fingerprint per environment: staging noise must not merge into
    -- production's count, or "is this happening in prod?" becomes unanswerable.
    unique (project_id, fingerprint, environment)
);

-- The issue list is "what is unresolved, most recent first", so index exactly that.
create index if not exists issues_unresolved_recent
    on issues (project_id, last_seen desc)
    where resolved_at is null;

create table if not exists events (
    id          bigserial   primary key,
    issue_id    bigint      not null references issues (id) on delete cascade,
    received_at timestamptz not null default now(),
    message     text        not null default '',
    -- The whole event: stacktrace, request context, tags. Kept as jsonb so the shape
    -- can grow with the SDKs without a migration per field.
    payload     jsonb       not null
);

create index if not exists events_issue_recent on events (issue_id, received_at desc);
