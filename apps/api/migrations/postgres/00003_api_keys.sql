-- +goose Up
CREATE TABLE api_keys (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name TEXT NOT NULL,
    prefix TEXT NOT NULL UNIQUE,
    hash TEXT NOT NULL UNIQUE,
    scopes TEXT NOT NULL,
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ
);

-- +goose Down
DROP TABLE api_keys;
