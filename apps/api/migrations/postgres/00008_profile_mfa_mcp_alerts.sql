-- +goose Up
-- Accounts: avatar and authenticator (2FA).
ALTER TABLE users ADD COLUMN avatar_icon TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN avatar_color TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN totp_secret TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN totp_enabled BOOLEAN NOT NULL DEFAULT FALSE;

-- Sessions shown in the profile: device and use.
ALTER TABLE sessions ADD COLUMN created_at TIMESTAMPTZ;
ALTER TABLE sessions ADD COLUMN last_used_at TIMESTAMPTZ;
ALTER TABLE sessions ADD COLUMN user_agent TEXT NOT NULL DEFAULT '';

-- Personal tokens of an account (MCP and API), kept as hashes.
CREATE TABLE user_tokens (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    prefix TEXT NOT NULL,
    hash TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ
);
CREATE INDEX user_tokens_user_id ON user_tokens(user_id);

-- MCP calls, for the Integrations page.
CREATE TABLE mcp_calls (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL,
    user_id BIGINT NOT NULL DEFAULT 0,
    actor TEXT NOT NULL,
    via TEXT NOT NULL DEFAULT '',
    method TEXT NOT NULL,
    tool TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    error TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX mcp_calls_created_at ON mcp_calls(created_at);

-- What producing a document again needs: its data, its template, its options.
ALTER TABLE renders ADD COLUMN data BYTEA;
ALTER TABLE renders ADD COLUMN template_sha TEXT NOT NULL DEFAULT '';
ALTER TABLE renders ADD COLUMN template_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE renders ADD COLUMN template_version INTEGER NOT NULL DEFAULT 0;
ALTER TABLE renders ADD COLUMN options TEXT NOT NULL DEFAULT '';
ALTER TABLE renders ADD COLUMN request_id TEXT NOT NULL DEFAULT '';
CREATE TABLE template_blobs (
    sha256 TEXT PRIMARY KEY,
    content BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

-- Alerts raised and resolved.
CREATE TABLE alert_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL,
    rule TEXT NOT NULL,
    state TEXT NOT NULL,
    severity TEXT NOT NULL,
    value DOUBLE PRECISION NOT NULL,
    threshold DOUBLE PRECISION NOT NULL,
    unit TEXT NOT NULL,
    notified TEXT NOT NULL DEFAULT ''
);
CREATE INDEX alert_events_created_at ON alert_events(created_at);

-- +goose Down
DROP TABLE alert_events;
DROP TABLE template_blobs;
ALTER TABLE renders DROP COLUMN request_id;
ALTER TABLE renders DROP COLUMN options;
ALTER TABLE renders DROP COLUMN template_version;
ALTER TABLE renders DROP COLUMN template_id;
ALTER TABLE renders DROP COLUMN template_sha;
ALTER TABLE renders DROP COLUMN data;
DROP TABLE mcp_calls;
DROP TABLE user_tokens;
ALTER TABLE sessions DROP COLUMN user_agent;
ALTER TABLE sessions DROP COLUMN last_used_at;
ALTER TABLE sessions DROP COLUMN created_at;
ALTER TABLE users DROP COLUMN totp_enabled;
ALTER TABLE users DROP COLUMN totp_secret;
ALTER TABLE users DROP COLUMN avatar_color;
ALTER TABLE users DROP COLUMN avatar_icon;
