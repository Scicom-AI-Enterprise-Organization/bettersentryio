-- Issue-level tags: the client's tags merged with what the server derives from
-- the event (level, environment, release, transaction, url, mechanism, ...).
-- Kept on the issue (latest event wins) because that is what the list filters
-- by; the full per-event history stays inside events.payload.
alter table issues add column if not exists tags jsonb not null default '{}'::jsonb;
create index if not exists issues_tags on issues using gin (tags);
