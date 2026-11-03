-- +goose Up
-- Documents produced in the background (POST /v1/render/async): the request
-- waits in the queue, then its document can be read until expires_at. The
-- addresses of the callback and of the upload stay in memory only; the task
-- that holds a job notes alive_at, so that another one fails it if it stops.
CREATE TABLE render_jobs (
    id TEXT PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL,
    alive_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ NOT NULL,
    key_id BIGINT NOT NULL,
    actor TEXT NOT NULL,
    status TEXT NOT NULL,
    error TEXT NOT NULL DEFAULT '',
    template TEXT NOT NULL DEFAULT '',
    format TEXT NOT NULL DEFAULT 'pdf',
    file_name TEXT NOT NULL DEFAULT '',
    size BIGINT NOT NULL DEFAULT 0,
    upload TEXT NOT NULL DEFAULT '',
    callback TEXT NOT NULL DEFAULT '',
    document BYTEA
);
CREATE INDEX render_jobs_expires_at ON render_jobs (expires_at);

-- +goose Down
DROP TABLE render_jobs;
