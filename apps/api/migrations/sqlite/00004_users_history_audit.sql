-- +goose Up
ALTER TABLE users ADD COLUMN display_name TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN email TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN active BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE users ADD COLUMN language TEXT NOT NULL DEFAULT 'fr';
ALTER TABLE users ADD COLUMN last_login_at TIMESTAMP;

CREATE TABLE renders (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at TIMESTAMP NOT NULL,
    source TEXT NOT NULL,
    actor TEXT NOT NULL,
    template TEXT NOT NULL,
    format TEXT NOT NULL,
    status TEXT NOT NULL,
    error TEXT NOT NULL DEFAULT '',
    size INTEGER NOT NULL DEFAULT 0,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    engine TEXT NOT NULL DEFAULT 'standard'
);

CREATE INDEX renders_created_at ON renders(created_at);

CREATE TABLE audit_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at TIMESTAMP NOT NULL,
    actor TEXT NOT NULL,
    action TEXT NOT NULL,
    target TEXT NOT NULL DEFAULT '',
    details TEXT NOT NULL DEFAULT '',
    ip TEXT NOT NULL DEFAULT ''
);

CREATE INDEX audit_log_created_at ON audit_log(created_at);

-- +goose Down
DROP TABLE audit_log;
DROP TABLE renders;
ALTER TABLE users DROP COLUMN last_login_at;
ALTER TABLE users DROP COLUMN language;
ALTER TABLE users DROP COLUMN active;
ALTER TABLE users DROP COLUMN email;
ALTER TABLE users DROP COLUMN display_name;
