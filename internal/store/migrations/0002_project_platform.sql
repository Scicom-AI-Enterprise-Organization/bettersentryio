-- The platform an app reports from ("fastapi", "python", "shell", …). Purely
-- presentational: it selects the logo and which integration snippet the setup page
-- opens on. Empty means "not stated", which is what every pre-existing app gets.
alter table projects add column if not exists platform text not null default '';
