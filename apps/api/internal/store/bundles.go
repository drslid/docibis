package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/uptrace/bun"
)

// Bundle is a set of documents produced together in one call: templates of
// the library and fixed PDFs, each under an optional condition on the data.
type Bundle struct {
	bun.BaseModel `bun:"table:bundles,alias:b"`
	ID            int64     `bun:"id,pk,autoincrement"`
	Name          string    `bun:"name,notnull"`
	Description   string    `bun:"description,notnull"`
	Items         string    `bun:"items,notnull"` // JSON list
	Output        string    `bun:"output,notnull"`
	CreatedAt     time.Time `bun:"created_at,notnull"`
	CreatedBy     string    `bun:"created_by,notnull"`
	UpdatedAt     time.Time `bun:"updated_at,notnull"`
	UpdatedBy     string    `bun:"updated_by,notnull"`
}

// Bundles lists the bundles by name.
func (s *Store) Bundles(ctx context.Context) ([]Bundle, error) {
	rows := []Bundle{}
	err := s.DB.NewSelect().Model(&rows).Order("name").Scan(ctx)
	return rows, err
}

func (s *Store) bundle(ctx context.Context, where string, arg any) (b Bundle, ok bool, err error) {
	err = s.DB.NewSelect().Model(&b).Where(where, arg).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return b, false, nil
	}
	return b, err == nil, err
}

// BundleByID finds a bundle by its number.
func (s *Store) BundleByID(ctx context.Context, id int64) (Bundle, bool, error) {
	return s.bundle(ctx, "id = ?", id)
}

// BundleByName finds a bundle by the name applications call.
func (s *Store) BundleByName(ctx context.Context, name string) (Bundle, bool, error) {
	return s.bundle(ctx, "name = ?", name)
}

// SaveBundle creates or updates a bundle and returns once it is in the copy.
func (s *Store) SaveBundle(ctx context.Context, b *Bundle) error {
	now := time.Now().UTC()
	b.UpdatedAt = now
	var err error
	if b.ID == 0 {
		b.CreatedAt, b.CreatedBy = now, b.UpdatedBy
		_, err = s.DB.NewInsert().Model(b).Exec(ctx)
	} else {
		_, err = s.DB.NewUpdate().Model(b).Column("name", "description", "items", "output", "updated_at", "updated_by").WherePK().Exec(ctx)
	}
	if err != nil {
		return err
	}
	return s.Sync(ctx)
}

// DeleteBundle removes a bundle.
func (s *Store) DeleteBundle(ctx context.Context, id int64) (bool, error) {
	res, err := s.DB.NewDelete().Model((*Bundle)(nil)).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return false, nil
	}
	return true, s.Sync(ctx)
}

// Promotion is a template version sent to another environment.
type Promotion struct {
	bun.BaseModel `bun:"table:promotions,alias:pr"`
	ID            int64     `bun:"id,pk,autoincrement"`
	CreatedAt     time.Time `bun:"created_at,notnull"`
	TemplateID    int64     `bun:"template_id,notnull"`
	Version       int       `bun:"version,notnull"`
	Environment   string    `bun:"environment,notnull"`
	Status        string    `bun:"status,notnull"`
	Detail        string    `bun:"detail,notnull"`
	Actor         string    `bun:"actor,notnull"`
}

// RecordPromotion adds a promotion to the history of a template.
func (s *Store) RecordPromotion(ctx context.Context, p *Promotion) error {
	p.CreatedAt = time.Now().UTC()
	if _, err := s.DB.NewInsert().Model(p).Exec(ctx); err != nil {
		return err
	}
	return s.Sync(ctx)
}

// Promotions lists the promotions of a template, the newest first.
func (s *Store) Promotions(ctx context.Context, templateID int64) ([]Promotion, error) {
	rows := []Promotion{}
	err := s.DB.NewSelect().Model(&rows).Where("template_id = ?", templateID).Order("id DESC").Limit(50).Scan(ctx)
	return rows, err
}

// TemplateUse sums up the documents produced with a template.
type TemplateUse struct {
	TemplateID int64      `bun:"template_id"`
	Count      int        `bun:"count"`
	Errors     int        `bun:"errors"`
	Refused    int        `bun:"refused"`
	LastUsedAt *time.Time `bun:"-"`
	Last       string     `bun:"last"`
}

// TemplateUses sums up, for each template of the library, the documents
// produced since a date.
func (s *Store) TemplateUses(ctx context.Context, since time.Time) (map[int64]TemplateUse, error) {
	var rows []TemplateUse
	err := s.DB.NewSelect().TableExpr("renders AS r").
		ColumnExpr("r.template_id").ColumnExpr("COUNT(*) AS count").
		ColumnExpr("SUM(CASE WHEN r.status = 'error' THEN 1 ELSE 0 END) AS errors").
		ColumnExpr("SUM(CASE WHEN r.status = 'refused' THEN 1 ELSE 0 END) AS refused").
		ColumnExpr("CAST(MAX(r.created_at) AS TEXT) AS last").
		Where("r.template_id <> 0").Where("r.created_at >= ?", since.UTC()).
		GroupExpr("r.template_id").Scan(ctx, &rows)
	out := make(map[int64]TemplateUse, len(rows))
	for _, r := range rows {
		r.LastUsedAt = parseTime(r.Last)
		out[r.TemplateID] = r
	}
	return out, err
}

// parseTime reads a date written by the database as text.
func parseTime(s string) *time.Time {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999-07", "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05.999999999"} {
		if t, err := time.Parse(layout, s); err == nil {
			return &t
		}
	}
	return nil
}

// TemplateRenders returns the documents produced with a template since a
// date, without their data, the oldest first.
func (s *Store) TemplateRenders(ctx context.Context, templateID int64, since time.Time) ([]Render, error) {
	rows := []Render{}
	err := s.DB.NewSelect().Model(&rows).Column("id", "created_at", "source", "actor", "status", "duration_ms", "template_version", "error").
		Where("template_id = ?", templateID).Where("created_at >= ?", since.UTC()).Order("created_at").Limit(100000).Scan(ctx)
	return rows, err
}
