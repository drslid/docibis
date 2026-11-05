-- +goose Up
-- Former names of a template: renamed or merged, it still answers to the
-- names the applications call.
CREATE TABLE template_aliases (
    alias TEXT PRIMARY KEY,
    template_id INTEGER NOT NULL REFERENCES templates(id) ON DELETE CASCADE,
    created_at TIMESTAMP NOT NULL,
    created_by TEXT NOT NULL DEFAULT ''
);
CREATE INDEX template_aliases_template_id ON template_aliases (template_id);

-- +goose Down
DROP TABLE template_aliases;
