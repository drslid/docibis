-- +goose Up
-- The template converted once to OpenDocument: no document converts it again,
-- even after a restart.
ALTER TABLE template_versions ADD COLUMN prepared BYTEA;

-- +goose Down
ALTER TABLE template_versions DROP COLUMN prepared;
