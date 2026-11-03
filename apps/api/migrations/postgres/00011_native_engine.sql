-- +goose Up
-- The native engine: a Typst copy of the template written by hand for this
-- version, who attached it and when, and its check against LibreOffice as
-- JSON ('' until done).
ALTER TABLE template_versions ADD COLUMN native_source TEXT NOT NULL DEFAULT '';
ALTER TABLE template_versions ADD COLUMN native_sha256 TEXT NOT NULL DEFAULT '';
ALTER TABLE template_versions ADD COLUMN native_by TEXT NOT NULL DEFAULT '';
ALTER TABLE template_versions ADD COLUMN native_at TIMESTAMPTZ;
ALTER TABLE template_versions ADD COLUMN native TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE template_versions DROP COLUMN native;
ALTER TABLE template_versions DROP COLUMN native_at;
ALTER TABLE template_versions DROP COLUMN native_by;
ALTER TABLE template_versions DROP COLUMN native_sha256;
ALTER TABLE template_versions DROP COLUMN native_source;
