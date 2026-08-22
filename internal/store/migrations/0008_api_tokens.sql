-- Auth tokens for the read API (docs/design/grafana-datasource.md).
--
-- Until now the only read credentials were the operator token, which lives in an env
-- var and can also delete apps, and an app's ingest key, which is minted for writing.
-- Neither is something you can hand to a dashboard and revoke later, which is exactly
-- what Sentry's auth tokens are for.

create table if not exists api_tokens (
    id           bigserial   primary key,
    name         text        not null,
    -- SHA-256 of the token, never the token. A read token exposes every issue and
    -- event in the install, so a database dump must not also be a credential dump —
    -- unlike ingest_keys.public_key, which is a write credential embedded in clients
    -- by design and has to be readable to be shown on the setup page.
    token_hash   bytea       not null unique,
    -- The visible head of the token ("bsiot_1a2b3c4d"), so the UI can identify a row
    -- without holding the secret. Shown in place of the token everywhere after minting.
    prefix       text        not null,
    created_at   timestamptz not null default now(),
    -- Answers "is this still in use?" before somebody revokes it. Updated at most once
    -- a minute per token, so a polling dashboard does not turn every read into a write.
    last_used_at timestamptz,
    revoked_at   timestamptz
);

-- Every request with a token does this lookup, and only live tokens can authenticate.
create index if not exists api_tokens_live on api_tokens (token_hash) where revoked_at is null;
