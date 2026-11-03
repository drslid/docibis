package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/uptrace/bun"
)

// Template is a Word template, or a fixed PDF to join to documents, kept in
// the library under the path the applications ask for.
type Template struct {
	bun.BaseModel `bun:"table:templates,alias:t"`
	ID            int64     `bun:"id,pk,autoincrement"`
	Path          string    `bun:"path,notnull"`
	Kind          string    `bun:"kind,notnull"`
	Description   string    `bun:"description,notnull"`
	Published     int       `bun:"published,notnull"`
	DataCheck     string    `bun:"data_check,notnull"`
	CreatedAt     time.Time `bun:"created_at,notnull"`
	CreatedBy     string    `bun:"created_by,notnull"`
	UpdatedAt     time.Time `bun:"updated_at,notnull"`
	// A publication planned at a date, and the version published before.
	ScheduledVersion  int        `bun:"scheduled_version,notnull"`
	ScheduledAt       *time.Time `bun:"scheduled_at"`
	ScheduledBy       string     `bun:"scheduled_by,notnull"`
	PreviousPublished int        `bun:"previous_published,notnull"`
	Versions          int        `bun:"versions,scanonly"`
	// Of the published version.
	Engine       string `bun:"engine,scanonly"`
	CheckResult  string `bun:"check_result,scanonly"`
	HasReference bool   `bun:"has_reference,scanonly"`
	// Checks of the newest version.
	Lint string `bun:"lint,scanonly"`
	// Fast engine of the published version.
	Fast string `bun:"fast,scanonly"`
	// Native engine of the published version: its check, and the hash of
	// its Typst copy ('' without one).
	Native       string `bun:"native,scanonly"`
	NativeSHA256 string `bun:"native_sha256,scanonly"`
}

// TemplateVersion is one upload of a template. Versions are never changed:
// publishing an older one is the way back.
type TemplateVersion struct {
	bun.BaseModel `bun:"table:template_versions,alias:v"`
	ID            int64  `bun:"id,pk,autoincrement"`
	TemplateID    int64  `bun:"template_id,notnull"`
	Number        int    `bun:"number,notnull"`
	Content       []byte `bun:"content,notnull"`
	Size          int64  `bun:"size,notnull"`
	SHA256        string `bun:"sha256,notnull"`
	Fields        string `bun:"fields,notnull"`
	Sample        string `bun:"sample,notnull"`
	Engine        string `bun:"engine,notnull"`
	Note          string `bun:"note,notnull"`
	Reference     []byte `bun:"reference,nullzero"`
	ReferenceData string `bun:"reference_data,notnull"`
	CheckResult   string `bun:"check_result,notnull"`
	// Lint is the JSON list of the checks of the template ('' until done).
	Lint string `bun:"lint,notnull"`
	// Prepared is the template converted to OpenDocument, done once.
	Prepared []byte `bun:"prepared,nullzero"`
	// Fast is the qualification of the fast engine as JSON ('' until done)
	// and FastOverlay what it draws with, when qualified.
	Fast        string `bun:"fast,notnull"`
	FastOverlay []byte `bun:"fast_overlay,nullzero"`
	// NativeSource is the Typst copy of the template, written by hand for
	// this version, and Native its check against LibreOffice as JSON ('' until
	// done).
	NativeSource string     `bun:"native_source,notnull"`
	NativeSHA256 string     `bun:"native_sha256,notnull"`
	NativeBy     string     `bun:"native_by,notnull"`
	NativeAt     *time.Time `bun:"native_at"`
	Native       string     `bun:"native,notnull"`
	CreatedAt    time.Time  `bun:"created_at,notnull"`
	CreatedBy    string     `bun:"created_by,notnull"`
	HasReference bool       `bun:"has_reference,scanonly"`
}

func (s *Store) templates() *bun.SelectQuery {
	return s.DB.NewSelect().Model((*Template)(nil)).ColumnExpr("t.*").
		ColumnExpr("(SELECT COUNT(*) FROM template_versions AS c WHERE c.template_id = t.id) AS versions").
		ColumnExpr("COALESCE(p.engine, '') AS engine").
		ColumnExpr("COALESCE(p.check_result, '') AS check_result").
		ColumnExpr("p.reference IS NOT NULL AS has_reference").
		ColumnExpr("COALESCE(p.fast, '') AS fast").
		ColumnExpr("COALESCE(p.native, '') AS native").
		ColumnExpr("COALESCE(p.native_sha256, '') AS native_sha256").
		ColumnExpr("COALESCE((SELECT l.lint FROM template_versions AS l WHERE l.template_id = t.id ORDER BY l.number DESC LIMIT 1), '') AS lint").
		Join("LEFT JOIN template_versions AS p ON p.template_id = t.id AND p.number = t.published")
}

// Templates lists the library, by path; text filters the path and description.
func (s *Store) Templates(ctx context.Context, text string) ([]Template, error) {
	rows := []Template{}
	q := s.templates().Order("t.path")
	if text != "" {
		pattern := likePattern(text)
		q = q.WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
			return q.Where(s.like("t.path"), pattern).WhereOr(s.like("t.description"), pattern)
		})
	}
	return rows, q.Scan(ctx, &rows)
}

func (s *Store) template(ctx context.Context, where string, arg any) (t Template, ok bool, err error) {
	err = s.templates().Where(where, arg).Scan(ctx, &t)
	if errors.Is(err, sql.ErrNoRows) {
		return t, false, nil
	}
	return t, err == nil, err
}

// TemplateByID finds a template of the library.
func (s *Store) TemplateByID(ctx context.Context, id int64) (Template, bool, error) {
	return s.template(ctx, "t.id = ?", id)
}

// TemplateByPath finds a template by the path the applications use.
func (s *Store) TemplateByPath(ctx context.Context, path string) (Template, bool, error) {
	return s.template(ctx, "t.path = ?", path)
}

// TemplateVersions lists the versions of a template, the newest first,
// without their files.
func (s *Store) TemplateVersions(ctx context.Context, templateID int64) ([]TemplateVersion, error) {
	rows := []TemplateVersion{}
	err := s.DB.NewSelect().Model(&rows).ExcludeColumn("content", "reference", "prepared", "fast_overlay", "native_source").
		ColumnExpr("reference IS NOT NULL AS has_reference").
		Where("template_id = ?", templateID).Order("number DESC").Scan(ctx)
	return rows, err
}

// TemplateVersion reads one version with its files.
func (s *Store) TemplateVersion(ctx context.Context, templateID int64, number int) (v TemplateVersion, ok bool, err error) {
	err = s.DB.NewSelect().Model(&v).ColumnExpr("v.*").ColumnExpr("reference IS NOT NULL AS has_reference").
		Where("template_id = ?", templateID).Where("number = ?", number).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return v, false, nil
	}
	return v, err == nil, err
}

// ServedVersion reads what producing a document needs: the template, its
// OpenDocument conversion and what it reads, not the reference.
func (s *Store) ServedVersion(ctx context.Context, templateID int64, number int) (v TemplateVersion, ok bool, err error) {
	err = s.DB.NewSelect().Model(&v).ExcludeColumn("reference", "reference_data", "check_result", "fast_overlay", "native_source").
		Where("template_id = ?", templateID).Where("number = ?", number).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return v, false, nil
	}
	return v, err == nil, err
}

// AddTemplateVersion saves a new version, creating the template the first
// time, and publishes it when asked. It returns once the copy has it.
func (s *Store) AddTemplateVersion(ctx context.Context, t *Template, v *TemplateVersion, publish bool) error {
	now := time.Now().UTC()
	err := s.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if t.ID == 0 {
			t.CreatedAt, t.UpdatedAt = now, now
			if t.DataCheck == "" {
				t.DataCheck = "warn"
			}
			if _, err := tx.NewInsert().Model(t).Exec(ctx); err != nil {
				return err
			}
		}
		var last int
		if err := tx.NewSelect().Model((*TemplateVersion)(nil)).ColumnExpr("COALESCE(MAX(number), 0)").
			Where("template_id = ?", t.ID).Scan(ctx, &last); err != nil {
			return err
		}
		v.TemplateID, v.Number, v.CreatedAt = t.ID, last+1, now
		if _, err := tx.NewInsert().Model(v).Exec(ctx); err != nil {
			return err
		}
		q := tx.NewUpdate().Model((*Template)(nil)).Set("updated_at = ?", now).Where("id = ?", t.ID)
		if publish {
			q = q.Set("published = ?", v.Number)
			t.Published = v.Number
		}
		t.UpdatedAt = now
		t.Versions = v.Number
		_, err := q.Exec(ctx)
		return err
	})
	if err != nil {
		return err
	}
	return s.Sync(ctx)
}

// PublishTemplate makes a version the one served: a new one, or an older one
// to go back. The version served before is kept for the way back, and a
// publication planned for this version is done.
func (s *Store) PublishTemplate(ctx context.Context, id int64, number int) error {
	if _, err := s.DB.NewUpdate().Model((*Template)(nil)).
		Set("previous_published = CASE WHEN published <> ? THEN published ELSE previous_published END", number).
		Set("scheduled_version = CASE WHEN scheduled_version = ? THEN 0 ELSE scheduled_version END", number).
		Set("published = ?", number).Set("updated_at = ?", time.Now().UTC()).
		Where("id = ?", id).Exec(ctx); err != nil {
		return err
	}
	return s.Sync(ctx)
}

// ScheduleTemplate plans the publication of a version at a date; version 0
// cancels it.
func (s *Store) ScheduleTemplate(ctx context.Context, id int64, version int, at *time.Time, by string) error {
	if at != nil {
		utc := at.UTC()
		at = &utc
	}
	if _, err := s.DB.NewUpdate().Model((*Template)(nil)).Set("scheduled_version = ?", version).Set("scheduled_at = ?", at).
		Set("scheduled_by = ?", by).Where("id = ?", id).Exec(ctx); err != nil {
		return err
	}
	return s.Sync(ctx)
}

// DueSchedules lists the templates whose planned publication has come.
func (s *Store) DueSchedules(ctx context.Context, now time.Time) ([]Template, error) {
	rows := []Template{}
	err := s.DB.NewSelect().Model(&rows).Where("scheduled_version > 0").Where("scheduled_at <= ?", now.UTC()).Scan(ctx)
	return rows, err
}

// PublishDue publishes a planned version if it is still planned; false
// when it no longer is (cancelled, or published by another task).
func (s *Store) PublishDue(ctx context.Context, id int64, number int) (bool, error) {
	res, err := s.DB.NewUpdate().Model((*Template)(nil)).
		Set("previous_published = CASE WHEN published <> ? THEN published ELSE previous_published END", number).
		Set("published = ?", number).Set("scheduled_version = 0").Set("scheduled_at = NULL").
		Set("updated_at = ?", time.Now().UTC()).Where("id = ?", id).Where("scheduled_version = ?", number).Exec(ctx)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, nil
	}
	return true, s.Sync(ctx)
}

// UpdateTemplate saves the description and the data check of a template.
func (s *Store) UpdateTemplate(ctx context.Context, t *Template) error {
	t.UpdatedAt = time.Now().UTC()
	if _, err := s.DB.NewUpdate().Model(t).Column("description", "data_check", "updated_at").WherePK().Exec(ctx); err != nil {
		return err
	}
	return s.Sync(ctx)
}

// DeleteTemplate removes a template and all its versions.
func (s *Store) DeleteTemplate(ctx context.Context, id int64) error {
	err := s.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewDelete().Model((*TemplateVersion)(nil)).Where("template_id = ?", id).Exec(ctx); err != nil {
			return err
		}
		_, err := tx.NewDelete().Model((*Template)(nil)).Where("id = ?", id).Exec(ctx)
		return err
	})
	if err != nil {
		return err
	}
	return s.Sync(ctx)
}

// SetReference attaches the reference PDF of a version (Docmosis' rendering)
// and the data it was made with; the last check no longer applies, and the
// fast and native engines are checked again with these data.
func (s *Store) SetReference(ctx context.Context, versionID int64, pdf []byte, data string) error {
	if _, err := s.DB.NewUpdate().Model((*TemplateVersion)(nil)).Set("reference = ?", pdf).Set("reference_data = ?", data).
		Set("check_result = ''").Set("fast = ''").Set("fast_overlay = NULL").Set("native = ''").Where("id = ?", versionID).Exec(ctx); err != nil {
		return err
	}
	return s.Sync(ctx)
}

// SetCheckResult keeps the result of the last comparison with the reference.
func (s *Store) SetCheckResult(ctx context.Context, versionID int64, result string) error {
	_, err := s.DB.NewUpdate().Model((*TemplateVersion)(nil)).Set("check_result = ?", result).Where("id = ?", versionID).Exec(ctx)
	return err
}

// ReferenceVersions lists the published versions that have a reference, for
// the comparison after each new version of DocIbis.
func (s *Store) ReferenceVersions(ctx context.Context) ([]TemplateVersion, error) {
	rows := []TemplateVersion{}
	err := s.DB.NewSelect().Model(&rows).Column("v.id", "v.template_id", "v.number", "v.check_result").
		Join("JOIN templates AS t ON t.id = v.template_id AND t.published = v.number").
		Where("v.reference IS NOT NULL").Order("v.template_id").Scan(ctx)
	return rows, err
}

// UndescribedVersions lists the versions of Word templates not read yet
// (imported in bulk) or read before their conversion was kept, without their
// files. A template that could not be read has fields but no engine.
func (s *Store) UndescribedVersions(ctx context.Context) ([]TemplateVersion, error) {
	rows := []TemplateVersion{}
	err := s.DB.NewSelect().Model(&rows).Column("v.id", "v.template_id", "v.number").
		Join("JOIN templates AS t ON t.id = v.template_id AND t.kind = 'template'").
		WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
			return q.Where("v.fields = ''").WhereOr("v.prepared IS NULL AND v.engine <> ''").WhereOr("v.lint = ''")
		}).Order("v.id").Scan(ctx)
	return rows, err
}

// DescribeVersion saves what a template reads, its sample data, its engine,
// its checks and its conversion to OpenDocument.
func (s *Store) DescribeVersion(ctx context.Context, id int64, fields, sample, engine, lint string, prepared []byte) error {
	q := s.DB.NewUpdate().Model((*TemplateVersion)(nil)).Set("fields = ?", fields).Set("sample = ?", sample).
		Set("engine = ?", engine).Set("lint = ?", lint).Set("fast = ''").Set("fast_overlay = NULL").Set("native = ''").Where("id = ?", id)
	if len(prepared) > 0 {
		q = q.Set("prepared = ?", prepared)
	}
	_, err := q.Exec(ctx)
	return err
}

// UnqualifiedVersions lists the published versions of Word templates read
// but not yet qualified for the fast engine, without their files.
func (s *Store) UnqualifiedVersions(ctx context.Context) ([]TemplateVersion, error) {
	rows := []TemplateVersion{}
	err := s.DB.NewSelect().Model(&rows).Column("v.id", "v.template_id", "v.number").
		Join("JOIN templates AS t ON t.id = v.template_id AND t.kind = 'template' AND t.published = v.number").
		Where("v.fast = ''").Where("v.prepared IS NOT NULL").Where("v.lint <> ''").Order("v.id").Scan(ctx)
	return rows, err
}

// SetFast keeps the qualification of a version for the fast engine and what
// it draws with (nil when not qualified).
func (s *Store) SetFast(ctx context.Context, versionID int64, status string, overlay []byte) error {
	_, err := s.DB.NewUpdate().Model((*TemplateVersion)(nil)).Set("fast = ?", status).Set("fast_overlay = ?", overlay).
		Where("id = ?", versionID).Exec(ctx)
	return err
}

// FastOverlay reads what the fast engine draws a version with.
func (s *Store) FastOverlay(ctx context.Context, versionID int64) ([]byte, error) {
	var overlay []byte
	err := s.DB.NewSelect().Model((*TemplateVersion)(nil)).Column("fast_overlay").Where("id = ?", versionID).Scan(ctx, &overlay)
	return overlay, err
}

// ResetFast forgets every qualification, for a new version of DocIbis.
func (s *Store) ResetFast(ctx context.Context) error {
	_, err := s.DB.NewUpdate().Model((*TemplateVersion)(nil)).Set("fast = ''").Set("fast_overlay = NULL").
		Where("fast <> ''").Exec(ctx)
	return err
}

// SetNativeSource attaches the Typst copy of a version, to be checked; an
// empty source removes it.
func (s *Store) SetNativeSource(ctx context.Context, versionID int64, source, sha256, by string) error {
	var at *time.Time
	if source != "" {
		now := time.Now().UTC()
		at = &now
	} else {
		sha256, by = "", ""
	}
	if _, err := s.DB.NewUpdate().Model((*TemplateVersion)(nil)).Set("native_source = ?", source).Set("native_sha256 = ?", sha256).
		Set("native_by = ?", by).Set("native_at = ?", at).Set("native = ''").Where("id = ?", versionID).Exec(ctx); err != nil {
		return err
	}
	return s.Sync(ctx)
}

// NativeSource reads the Typst copy of a version (” without one).
func (s *Store) NativeSource(ctx context.Context, versionID int64) (string, error) {
	var source string
	err := s.DB.NewSelect().Model((*TemplateVersion)(nil)).Column("native_source").Where("id = ?", versionID).Scan(ctx, &source)
	return source, err
}

// SetNative keeps the check of a Typst copy against LibreOffice.
func (s *Store) SetNative(ctx context.Context, versionID int64, status string) error {
	_, err := s.DB.NewUpdate().Model((*TemplateVersion)(nil)).Set("native = ?", status).Where("id = ?", versionID).Exec(ctx)
	return err
}

// UncheckedNativeVersions lists the versions read whose Typst copy is not
// checked yet, the published ones first, without their files.
func (s *Store) UncheckedNativeVersions(ctx context.Context) ([]TemplateVersion, error) {
	rows := []TemplateVersion{}
	err := s.DB.NewSelect().Model(&rows).Column("v.id", "v.template_id", "v.number").
		Join("JOIN templates AS t ON t.id = v.template_id AND t.kind = 'template'").
		Where("v.native_source <> ''").Where("v.native = ''").Where("v.prepared IS NOT NULL").
		OrderExpr("CASE WHEN t.published = v.number THEN 0 ELSE 1 END, v.id").Scan(ctx)
	return rows, err
}

// ResetNative asks every Typst copy to be checked again, for a new version of
// DocIbis.
func (s *Store) ResetNative(ctx context.Context) error {
	_, err := s.DB.NewUpdate().Model((*TemplateVersion)(nil)).Set("native = ''").Where("native <> ''").Exec(ctx)
	return err
}
