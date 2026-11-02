-- +goose Up
-- Accounts: avatar and authenticator (2FA).
ALTER TABLE users ADD COLUMN avatar_icon TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN avatar_color TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN totp_secret TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN totp_enabled BOOLEAN NOT NULL DEFAULT FALSE;

-- Sessions shown in the profile: device and use.
ALTER TABLE sessions ADD COLUMN created_at TIMESTAMP;
ALTER TABLE sessions ADD COLUMN last_used_at TIMESTAMP;
ALTER TABLE sessions ADD COLUMN user_agent TEXT NOT NULL DEFAULT '';

-- Personal tokens of an account (MCP and API), kept as hashes.
CREATE TABLE user_tokens (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    prefix TEXT NOT NULL,
    hash TEXT NOT NULL UNIQUE,
    created_at TIMESTAMP NOT NULL,
    expires_at TIMESTAMP,
    last_used_at TIMESTAMP,
    revoked_at TIMESTAMP
);
CREATE INDEX user_tokens_user_id ON user_tokens(user_id);

-- MCP calls, for the Integrations page.
CREATE TABLE mcp_calls (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at TIMESTAMP NOT NULL,
    user_id INTEGER NOT NULL DEFAULT 0,
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
ALTER TABLE renders ADD COLUMN data BLOB;
ALTER TABLE renders ADD COLUMN template_sha TEXT NOT NULL DEFAULT '';
ALTER TABLE renders ADD COLUMN template_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE renders ADD COLUMN template_version INTEGER NOT NULL DEFAULT 0;
ALTER TABLE renders ADD COLUMN options TEXT NOT NULL DEFAULT '';
ALTER TABLE renders ADD COLUMN request_id TEXT NOT NULL DEFAULT '';
CREATE TABLE template_blobs (
    sha256 TEXT PRIMARY KEY,
    content BLOB NOT NULL,
    created_at TIMESTAMP NOT NULL
);

-- Alerts raised and resolved.
CREATE TABLE alert_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at TIMESTAMP NOT NULL,
    rule TEXT NOT NULL,
    state TEXT NOT NULL,
    severity TEXT NOT NULL,
    value REAL NOT NULL,
    threshold REAL NOT NULL,
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
