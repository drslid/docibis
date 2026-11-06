package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/uptrace/bun"
)

// Font is a font file added by an administrator (a brand font, a licensed
// font), installed for every worker.
type Font struct {
	bun.BaseModel `bun:"table:fonts,alias:f"`
	ID            int64     `bun:"id,pk,autoincrement"`
	File          string    `bun:"file,notnull"`
	Family        string    `bun:"family,notnull"`
	Style         string    `bun:"style,notnull"`
	Size          int64     `bun:"size,notnull"`
	SHA256        string    `bun:"sha256,notnull"`
	Content       []byte    `bun:"content,notnull"`
	CreatedAt     time.Time `bun:"created_at,notnull"`
	CreatedBy     string    `bun:"created_by,notnull"`
}

// Fonts lists the fonts added, without their files.
func (s *Store) Fonts(ctx context.Context) ([]Font, error) {
	rows := []Font{}
	err := s.DB.NewSelect().Model(&rows).ExcludeColumn("content").Order("family", "style", "id").Scan(ctx)
	return rows, err
}

// FontFiles lists the fonts added with their files.
func (s *Store) FontFiles(ctx context.Context) ([]Font, error) {
	rows := []Font{}
	err := s.DB.NewSelect().Model(&rows).Order("id").Scan(ctx)
	return rows, err
}

// FontSums lists the hashes of the fonts added: a change shows there.
func (s *Store) FontSums(ctx context.Context) ([]string, error) {
	sums := []string{}
	err := s.DB.NewSelect().Model((*Font)(nil)).Column("sha256").Order("sha256").Scan(ctx, &sums)
	return sums, err
}

// AddFont keeps a font; a file already added is not added twice: it says
// false and f becomes the font kept.
func (s *Store) AddFont(ctx context.Context, f *Font) (bool, error) {
	f.CreatedAt = time.Now().UTC()
	res, err := s.DB.NewInsert().Model(f).On("CONFLICT (sha256) DO NOTHING").Returning("id").Exec(ctx)
	if err != nil {
		return false, err
	}
	if n, err := res.RowsAffected(); err == nil && n > 0 {
		return true, nil
	}
	return false, s.DB.NewSelect().Model(f).ExcludeColumn("content").Where("sha256 = ?", f.SHA256).Scan(ctx)
}

// DeleteFont removes a font added and returns it, without its file; false
// when there is none.
func (s *Store) DeleteFont(ctx context.Context, id int64) (Font, bool, error) {
	var f Font
	err := s.DB.NewSelect().Model(&f).ExcludeColumn("content").Where("id = ?", id).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return f, false, nil
	}
	if err != nil {
		return f, false, err
	}
	_, err = s.DB.NewDelete().Model((*Font)(nil)).Where("id = ?", id).Exec(ctx)
	return f, err == nil, err
}

// ResetLint has every version of the Word templates checked again (the
// fonts changed): they are read again in the background, which also
// qualifies them again for the fast and native engines.
func (s *Store) ResetLint(ctx context.Context) error {
	_, err := s.DB.NewUpdate().Model((*TemplateVersion)(nil)).Set("lint = ''").
		Where("template_id IN (SELECT id FROM templates WHERE kind = 'template')").Exec(ctx)
	return err
}

// PublishedLint gives the checks of the published version of each Word
// template, by path.
func (s *Store) PublishedLint(ctx context.Context) (map[string]string, error) {
	var rows []struct {
		Path string `bun:"path"`
		Lint string `bun:"lint"`
	}
	err := s.DB.NewSelect().TableExpr("templates AS t").ColumnExpr("t.path, v.lint").
		Join("JOIN template_versions AS v ON v.template_id = t.id AND v.number = t.published").
		Where("t.kind = 'template'").Scan(ctx, &rows)
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.Path] = r.Lint
	}
	return out, err
}
