-- +goose Up
-- Spaces: the templates of a team, by path prefix, with quotas of documents;
-- an API key may be kept to one space and have its own quotas.
CREATE TABLE spaces (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    prefixes TEXT NOT NULL DEFAULT '',
    daily_quota INTEGER NOT NULL DEFAULT 0,
    monthly_quota INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL,
    created_by TEXT NOT NULL DEFAULT ''
);
ALTER TABLE api_keys ADD COLUMN space_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE api_keys ADD COLUMN daily_quota INTEGER NOT NULL DEFAULT 0;
ALTER TABLE api_keys ADD COLUMN monthly_quota INTEGER NOT NULL DEFAULT 0;
-- Documents produced, by key or space (key:12, space:3) and by day or month.
CREATE TABLE usage_counts (
    scope TEXT NOT NULL,
    period TEXT NOT NULL,
    count BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (scope, period)
);

-- +goose Down
DROP TABLE usage_counts;
ALTER TABLE api_keys DROP COLUMN monthly_quota;
ALTER TABLE api_keys DROP COLUMN daily_quota;
ALTER TABLE api_keys DROP COLUMN space_id;
DROP TABLE spaces;
