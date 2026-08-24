-- Per-EVENT lookup by tag and by trace id (the correlation workflow: a request id
-- from a log line or a Grafana trace panel → the exact error it produced).
--
-- Issue-level tags cannot answer this: issues.tags keeps the LATEST event's tags, and
-- a correlation id is unique per request — by the time anyone searches for it, the
-- issue has usually moved on. The search has to look inside every event's payload,
-- and these indexes are what keep that from being a sequential scan of the biggest
-- table in the database.

-- Containment queries: payload->'tags' @> '{"correlation_id": "..."}'.
-- jsonb_path_ops: smaller and faster than the default ops class, and containment is
-- the only operator the search uses.
create index if not exists events_tags on events using gin ((payload -> 'tags') jsonb_path_ops);

-- The stock SDK puts the trace id in contexts.trace.trace_id, not in tags.
create index if not exists events_trace_id on events ((payload -> 'contexts' -> 'trace' ->> 'trace_id'))
    where payload -> 'contexts' -> 'trace' ->> 'trace_id' is not null;
