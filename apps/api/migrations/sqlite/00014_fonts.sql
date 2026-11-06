-- +goose Up
-- Fonts added by an administrator (brand fonts, licensed fonts), installed
-- for every worker.
CREATE TABLE fonts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    file TEXT NOT NULL,
    family TEXT NOT NULL,
    style TEXT NOT NULL DEFAULT '',
    size INTEGER NOT NULL,
    sha256 TEXT NOT NULL UNIQUE,
    content BLOB NOT NULL,
    created_at TIMESTAMP NOT NULL,
    created_by TEXT NOT NULL DEFAULT ''
);

-- +goose Down
DROP TABLE fonts;
