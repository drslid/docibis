package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"

	"github.com/uptrace/bun"
)

// TemplateAlias is a former name of a template: renamed, or merged into
// another one, it still answers to the names the applications call.
type TemplateAlias struct {
	bun.BaseModel `bun:"table:template_aliases,alias:ta"`
	Alias         string    `bun:"alias,pk"`
	TemplateID    int64     `bun:"template_id,notnull"`
	CreatedAt     time.Time `bun:"created_at,notnull"`
	CreatedBy     string    `bun:"created_by,notnull"`
}

// ErrNameTaken is a name already used by another template, as its path or
// as a former name.
var ErrNameTaken = errors.New("name used by another template")

// TemplateByName finds a template by its path, else by a former name.
func (s *Store) TemplateByName(ctx context.Context, name string) (Template, bool, error) {
	t, ok, err := s.TemplateByPath(ctx, name)
	if err != nil || ok {
		return t, ok, err
	}
	return s.template(ctx, "t.id = (SELECT a.template_id FROM template_aliases AS a WHERE a.alias = ?)", name)
}

// NameOwner tells which template answers to a name, as its path or a former
// name; 0 for none.
func (s *Store) NameOwner(ctx context.Context, name string) (int64, error) {
	return nameOwner(ctx, s.DB, name)
}

func nameOwner(ctx context.Context, db bun.IDB, name string) (int64, error) {
	var id int64
	err := db.NewSelect().Model((*Template)(nil)).Column("id").Where("path = ?", name).Scan(ctx, &id)
	if errors.Is(err, sql.ErrNoRows) {
		err = db.NewSelect().Model((*TemplateAlias)(nil)).Column("template_id").Where("alias = ?", name).Scan(ctx, &id)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// TemplateAliases lists the former names of a template.
func (s *Store) TemplateAliases(ctx context.Context, templateID int64) ([]TemplateAlias, error) {
	rows := []TemplateAlias{}
	err := s.DB.NewSelect().Model(&rows).Where("template_id = ?", templateID).Order("alias").Scan(ctx)
	return rows, err
}

// RenameTemplate gives a template a new path; the old one becomes a former
// name. Going back to a former name makes it the path again.
func (s *Store) RenameTemplate(ctx context.Context, id int64, path, by string) error {
	err := s.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var old string
		if err := tx.NewSelect().Model((*Template)(nil)).Column("path").Where("id = ?", id).Scan(ctx, &old); err != nil {
			return err
		}
		if old == path {
			return nil
		}
		if owner, err := nameOwner(ctx, tx, path); err != nil {
			return err
		} else if owner != 0 && owner != id {
			return ErrNameTaken
		}
		if _, err := tx.NewDelete().Model((*TemplateAlias)(nil)).Where("alias = ?", path).Exec(ctx); err != nil {
			return err
		}
		now := time.Now().UTC()
		if _, err := tx.NewInsert().Model(&TemplateAlias{Alias: old, TemplateID: id, CreatedAt: now, CreatedBy: by}).Exec(ctx); err != nil {
			return err
		}
		_, err := tx.NewUpdate().Model((*Template)(nil)).Set("path = ?", path).Set("updated_at = ?", now).Where("id = ?", id).Exec(ctx)
		return err
	})
	if err != nil {
		return err
	}
	return s.Sync(ctx)
}

// MergeTemplate moves into a template the versions of another one whose file
// it does not have yet, in their order, then deletes the other one: its
// path and former names become former names of the template, and its
// history counts for the template. The publication of the template does not
// change. It returns the number of versions added.
func (s *Store) MergeTemplate(ctx context.Context, sourceID, targetID int64, by string) (int, error) {
	added := 0
	err := s.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var source Template
		if err := tx.NewSelect().Model(&source).Where("id = ?", sourceID).Scan(ctx); err != nil {
			return err
		}
		var have []string
		if err := tx.NewSelect().Model((*TemplateVersion)(nil)).Column("sha256").Where("template_id = ?", targetID).Scan(ctx, &have); err != nil {
			return err
		}
		var last int
		if err := tx.NewSelect().Model((*TemplateVersion)(nil)).ColumnExpr("COALESCE(MAX(number), 0)").
			Where("template_id = ?", targetID).Scan(ctx, &last); err != nil {
			return err
		}
		var versions []TemplateVersion
		if err := tx.NewSelect().Model(&versions).Where("template_id = ?", sourceID).Order("number").Scan(ctx); err != nil {
			return err
		}
		for _, v := range versions {
			if slices.Contains(have, v.SHA256) {
				continue
			}
			last++
			v.ID, v.TemplateID, v.Number = 0, targetID, last
			if _, err := tx.NewInsert().Model(&v).Exec(ctx); err != nil {
				return err
			}
			have = append(have, v.SHA256)
			added++
		}
		for _, q := range []*bun.UpdateQuery{
			tx.NewUpdate().Model((*TemplateAlias)(nil)).Set("template_id = ?", targetID).Where("template_id = ?", sourceID),
			tx.NewUpdate().Table("renders").Set("template_id = ?", targetID).Where("template_id = ?", sourceID),
			tx.NewUpdate().Table("promotions").Set("template_id = ?", targetID).Where("template_id = ?", sourceID),
		} {
			if _, err := q.Exec(ctx); err != nil {
				return err
			}
		}
		if _, err := tx.NewDelete().Model((*TemplateVersion)(nil)).Where("template_id = ?", sourceID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewDelete().Model((*Template)(nil)).Where("id = ?", sourceID).Exec(ctx); err != nil {
			return err
		}
		now := time.Now().UTC()
		if _, err := tx.NewInsert().Model(&TemplateAlias{Alias: source.Path, TemplateID: targetID, CreatedAt: now, CreatedBy: by}).Exec(ctx); err != nil {
			return err
		}
		_, err := tx.NewUpdate().Model((*Template)(nil)).Set("updated_at = ?", now).Where("id = ?", targetID).Exec(ctx)
		return err
	})
	if err != nil {
		return 0, err
	}
	return added, s.Sync(ctx)
}

// DeleteTemplateAlias removes a former name of a template: the applications
// still calling it get an error. false when the template has no such name.
func (s *Store) DeleteTemplateAlias(ctx context.Context, templateID int64, alias string) (bool, error) {
	res, err := s.DB.NewDelete().Model((*TemplateAlias)(nil)).Where("alias = ?", alias).Where("template_id = ?", templateID).Exec(ctx)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, nil
	}
	return true, s.Sync(ctx)
}
