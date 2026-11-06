-- +goose Up
-- Fonts added by an administrator (brand fonts, licensed fonts), installed
-- for every worker.
CREATE TABLE fonts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    file TEXT NOT NULL,
    family TEXT NOT NULL,
    style TEXT NOT NULL DEFAULT '',
    size BIGINT NOT NULL,
    sha256 TEXT NOT NULL UNIQUE,
    content BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    created_by TEXT NOT NULL DEFAULT ''
);

-- +goose Down
DROP TABLE fonts;
