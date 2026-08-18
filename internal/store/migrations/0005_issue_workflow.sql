-- Issue workflow: archive (with expiry or until-recurrence), priority, and the
-- columns the triage UI acts on. Delete needs no schema; events cascade.
alter table issues add column if not exists archived_at    timestamptz;
alter table issues add column if not exists archived_until timestamptz; -- null while archived_at set = forever
alter table issues add column if not exists archive_recur  boolean not null default false; -- unarchive on next event
alter table issues add column if not exists priority       text not null default '';
