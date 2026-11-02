-- +goose Up
-- Qualification of each version for the fast engine, as JSON ('' until done),
-- and what the engine draws with once qualified.
ALTER TABLE template_versions ADD COLUMN fast TEXT NOT NULL DEFAULT '';
ALTER TABLE template_versions ADD COLUMN fast_overlay BYTEA;

-- +goose Down
ALTER TABLE template_versions DROP COLUMN fast_overlay;
ALTER TABLE template_versions DROP COLUMN fast;
