-- +goose Up
CREATE TABLE templates (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    path TEXT NOT NULL UNIQUE,
    kind TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    published INTEGER NOT NULL DEFAULT 0,
    data_check TEXT NOT NULL DEFAULT 'warn',
    created_at TIMESTAMPTZ NOT NULL,
    created_by TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE template_versions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    template_id BIGINT NOT NULL REFERENCES templates(id) ON DELETE CASCADE,
    number INTEGER NOT NULL,
    content BYTEA NOT NULL,
    size BIGINT NOT NULL,
    sha256 TEXT NOT NULL,
    fields TEXT NOT NULL DEFAULT '',
    sample TEXT NOT NULL DEFAULT '',
    engine TEXT NOT NULL DEFAULT '',
    note TEXT NOT NULL DEFAULT '',
    reference BYTEA,
    reference_data TEXT NOT NULL DEFAULT '',
    check_result TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    created_by TEXT NOT NULL,
    UNIQUE (template_id, number)
);

CREATE TABLE releases (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    version TEXT NOT NULL,
    title TEXT NOT NULL,
    content TEXT NOT NULL,
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

ALTER TABLE api_keys ADD COLUMN rate_per_minute INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE api_keys DROP COLUMN rate_per_minute;
DROP TABLE releases;
DROP TABLE template_versions;
DROP TABLE templates;
