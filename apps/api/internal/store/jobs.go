package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/uptrace/bun"
)

// RenderJob is a document produced in the background: queued, running, done
// or failed. Only the key that asked for it reads it.
type RenderJob struct {
	bun.BaseModel `bun:"table:render_jobs,alias:j"`
	ID            string    `bun:"id,pk"`
	CreatedAt     time.Time `bun:"created_at,notnull"`
	// AliveAt is the last sign of the task that holds the job in memory.
	AliveAt    time.Time  `bun:"alive_at,notnull"`
	FinishedAt *time.Time `bun:"finished_at"`
	ExpiresAt  time.Time  `bun:"expires_at,notnull"`
	KeyID      int64      `bun:"key_id,notnull"`
	Actor      string     `bun:"actor,notnull"`
	Status     string     `bun:"status,notnull"`
	Error      string     `bun:"error,notnull"`
	Template   string     `bun:"template,notnull"`
	Format     string     `bun:"format,notnull"`
	FileName   string     `bun:"file_name,notnull"`
	Size       int64      `bun:"size,notnull"`
	// Upload and Callback: "" when not asked, else pending, done or failed.
	Upload   string `bun:"upload,notnull"`
	Callback string `bun:"callback,notnull"`
	Document []byte `bun:"document,nullzero"`
}

// CreateRenderJob records a job waiting in the queue.
func (s *Store) CreateRenderJob(ctx context.Context, j *RenderJob) error {
	now := time.Now().UTC()
	j.CreatedAt, j.AliveAt, j.ExpiresAt = now, now, j.ExpiresAt.UTC()
	_, err := s.DB.NewInsert().Model(j).Exec(ctx)
	return err
}

// UpdateRenderJob writes the given columns of a job, all of them (its
// document included) when none is given.
func (s *Store) UpdateRenderJob(ctx context.Context, j *RenderJob, columns ...string) error {
	j.AliveAt, j.ExpiresAt = time.Now().UTC(), j.ExpiresAt.UTC()
	if j.FinishedAt != nil {
		finished := j.FinishedAt.UTC()
		j.FinishedAt = &finished
	}
	q := s.DB.NewUpdate().Model(j).WherePK()
	if len(columns) > 0 {
		q = q.Column(columns...).Column("alive_at")
	}
	_, err := q.Exec(ctx)
	return err
}

// RenderJob reads a job of a key, without its document; expired jobs are
// gone.
func (s *Store) RenderJob(ctx context.Context, id string, keyID int64) (j RenderJob, ok bool, err error) {
	err = s.DB.NewSelect().Model(&j).ExcludeColumn("document").
		Where("id = ?", id).Where("key_id = ?", keyID).Where("expires_at > ?", time.Now().UTC()).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return j, false, nil
	}
	return j, err == nil, err
}

// RenderJobDocument reads the document of a finished job of a key.
func (s *Store) RenderJobDocument(ctx context.Context, id string, keyID int64) (j RenderJob, ok bool, err error) {
	err = s.DB.NewSelect().Model(&j).
		Where("id = ?", id).Where("key_id = ?", keyID).Where("expires_at > ?", time.Now().UTC()).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return j, false, nil
	}
	return j, err == nil, err
}

var unfinished = []string{"queued", "running"}

// TouchRenderJobs notes that this task still holds these jobs, and keeps them
// until retention from now.
func (s *Store) TouchRenderJobs(ctx context.Context, ids []string, retention time.Duration) error {
	if len(ids) == 0 {
		return nil
	}
	now := time.Now().UTC()
	_, err := s.DB.NewUpdate().Model((*RenderJob)(nil)).
		Set("alive_at = ?", now).Set("expires_at = ?", now.Add(retention)).
		Where("id IN (?)", bun.In(ids)).Where("status IN (?)", bun.In(unfinished)).Exec(ctx)
	return err
}

// FailStaleRenderJobs ends the jobs of a task that stopped before finishing
// them (no sign since before): their request was only in its memory.
func (s *Store) FailStaleRenderJobs(ctx context.Context, before time.Time, reason string, retention time.Duration) (int, error) {
	now := time.Now().UTC()
	res, err := s.DB.NewUpdate().Model((*RenderJob)(nil)).
		Set("status = ?", "failed").Set("error = ?", reason).Set("finished_at = ?", now).Set("expires_at = ?", now.Add(retention)).
		Set("upload = CASE WHEN upload = 'pending' THEN 'failed' ELSE upload END").
		Set("callback = CASE WHEN callback = 'pending' THEN 'failed' ELSE callback END").
		Where("status IN (?)", bun.In(unfinished)).Where("alive_at < ?", before.UTC()).Exec(ctx)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// PurgeRenderJobs forgets the jobs, and their documents, past their expiry.
func (s *Store) PurgeRenderJobs(ctx context.Context) (int, error) {
	res, err := s.DB.NewDelete().Model((*RenderJob)(nil)).Where("expires_at <= ?", time.Now().UTC()).Exec(ctx)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
