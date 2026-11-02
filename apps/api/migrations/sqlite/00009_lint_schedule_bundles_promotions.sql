-- +goose Up
-- Checks of each template version, as a JSON list ('' until checked).
ALTER TABLE template_versions ADD COLUMN lint TEXT NOT NULL DEFAULT '';

-- A publication planned at a date, and the version published before it.
ALTER TABLE templates ADD COLUMN scheduled_version INTEGER NOT NULL DEFAULT 0;
ALTER TABLE templates ADD COLUMN scheduled_at TIMESTAMP;
ALTER TABLE templates ADD COLUMN scheduled_by TEXT NOT NULL DEFAULT '';
ALTER TABLE templates ADD COLUMN previous_published INTEGER NOT NULL DEFAULT 0;

-- Statistics per template.
CREATE INDEX renders_template_id ON renders(template_id, created_at);

-- Bundles: documents produced together in one call.
CREATE TABLE bundles (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    items TEXT NOT NULL,
    output TEXT NOT NULL DEFAULT 'pdf',
    created_at TIMESTAMP NOT NULL,
    created_by TEXT NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    updated_by TEXT NOT NULL
);

-- Templates sent to another environment (INT -> REC -> PROD).
CREATE TABLE promotions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at TIMESTAMP NOT NULL,
    template_id INTEGER NOT NULL,
    version INTEGER NOT NULL,
    environment TEXT NOT NULL,
    status TEXT NOT NULL,
    detail TEXT NOT NULL DEFAULT '',
    actor TEXT NOT NULL
);
CREATE INDEX promotions_template_id ON promotions(template_id, id);

-- +goose Down
DROP TABLE promotions;
DROP TABLE bundles;
DROP INDEX renders_template_id;
ALTER TABLE templates DROP COLUMN previous_published;
ALTER TABLE templates DROP COLUMN scheduled_by;
ALTER TABLE templates DROP COLUMN scheduled_at;
ALTER TABLE templates DROP COLUMN scheduled_version;
ALTER TABLE template_versions DROP COLUMN lint;
