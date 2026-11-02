package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/uptrace/bun"
)

// Release is a version note shown on the "Nouveautés" page, in Markdown.
type Release struct {
	bun.BaseModel `bun:"table:releases,alias:r"`
	ID            int64     `bun:"id,pk,autoincrement"`
	Version       string    `bun:"version,notnull"`
	Title         string    `bun:"title,notnull"`
	Content       string    `bun:"content,notnull"`
	CreatedBy     string    `bun:"created_by,notnull"`
	CreatedAt     time.Time `bun:"created_at,notnull"`
	UpdatedAt     time.Time `bun:"updated_at,notnull"`
}

// Releases lists the notes, the newest first.
func (s *Store) Releases(ctx context.Context, p Page) ([]Release, int, error) {
	rows := []Release{}
	n, err := p.apply(s.DB.NewSelect().Model(&rows).Order("created_at DESC", "id DESC")).ScanAndCount(ctx)
	return rows, int(n), err
}

// ReleaseByID finds a note.
func (s *Store) ReleaseByID(ctx context.Context, id int64) (r Release, ok bool, err error) {
	err = s.DB.NewSelect().Model(&r).Where("id = ?", id).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return r, false, nil
	}
	return r, err == nil, err
}

// SaveRelease creates or updates a note.
func (s *Store) SaveRelease(ctx context.Context, r *Release) error {
	now := time.Now().UTC()
	r.UpdatedAt = now
	var err error
	if r.ID == 0 {
		r.CreatedAt = now
		_, err = s.DB.NewInsert().Model(r).Exec(ctx)
	} else {
		_, err = s.DB.NewUpdate().Model(r).Column("version", "title", "content", "updated_at").WherePK().Exec(ctx)
	}
	if err != nil {
		return err
	}
	return s.Sync(ctx)
}

// DeleteRelease removes a note.
func (s *Store) DeleteRelease(ctx context.Context, id int64) error {
	if _, err := s.DB.NewDelete().Model((*Release)(nil)).Where("id = ?", id).Exec(ctx); err != nil {
		return err
	}
	return s.Sync(ctx)
}
