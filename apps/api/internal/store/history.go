package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/uptrace/bun"
)

// Render is one produced document: who asked, what, how long it took. The
// document is never kept; its data are, when the setting allows it, to
// produce it again.
type Render struct {
	bun.BaseModel `bun:"table:renders,alias:r"`
	ID            int64     `bun:"id,pk,autoincrement"`
	CreatedAt     time.Time `bun:"created_at,notnull"`
	Source        string    `bun:"source,notnull"` // console or api
	Actor         string    `bun:"actor,notnull"`  // account, or name of the API key
	Template      string    `bun:"template,notnull"`
	Format        string    `bun:"format,notnull"`
	Status        string    `bun:"status,notnull"` // ok or error
	Error         string    `bun:"error,notnull"`
	Size          int64     `bun:"size,notnull"`
	DurationMs    int       `bun:"duration_ms,notnull"`
	Engine        string    `bun:"engine,notnull"`
	// Data is the JSON data, gzipped; nil when not kept.
	Data            []byte `bun:"data,nullzero"`
	TemplateSHA     string `bun:"template_sha,notnull"`
	TemplateID      int64  `bun:"template_id,notnull"`
	TemplateVersion int    `bun:"template_version,notnull"`
	Options         string `bun:"options,notnull"`
	RequestID       string `bun:"request_id,notnull"`
	HasData         bool   `bun:"has_data,scanonly"`
}

// templateBlob is a template sent with a request, kept by its hash while a
// document of the history uses it.
type templateBlob struct {
	bun.BaseModel `bun:"table:template_blobs,alias:tb"`
	SHA256        string    `bun:"sha256,pk"`
	Content       []byte    `bun:"content,notnull"`
	CreatedAt     time.Time `bun:"created_at,notnull"`
}

// SaveTemplateBlob keeps a template sent with a request, once per content.
func (s *Store) SaveTemplateBlob(ctx context.Context, sha string, content []byte) error {
	_, err := s.DB.NewInsert().Model(&templateBlob{SHA256: sha, Content: content, CreatedAt: time.Now().UTC()}).
		On("CONFLICT (sha256) DO NOTHING").Exec(ctx)
	return err
}

// TemplateBlob reads a template kept by SaveTemplateBlob.
func (s *Store) TemplateBlob(ctx context.Context, sha string) ([]byte, bool, error) {
	var b templateBlob
	err := s.DB.NewSelect().Model(&b).Where("sha256 = ?", sha).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	return b.Content, err == nil, err
}

// RenderByID reads one document of the history with its data.
func (s *Store) RenderByID(ctx context.Context, id int64) (r Render, ok bool, err error) {
	err = s.DB.NewSelect().Model(&r).Where("id = ?", id).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return r, false, nil
	}
	return r, err == nil, err
}

// AuditEntry is one action of an account: sign-in, change of an account, a
// key or a setting.
type AuditEntry struct {
	bun.BaseModel `bun:"table:audit_log,alias:a"`
	ID            int64     `bun:"id,pk,autoincrement"`
	CreatedAt     time.Time `bun:"created_at,notnull"`
	Actor         string    `bun:"actor,notnull"`
	Action        string    `bun:"action,notnull"`
	Target        string    `bun:"target,notnull"`
	Details       string    `bun:"details,notnull"`
	IP            string    `bun:"ip,notnull"`
}

// like is a case-insensitive LIKE on both engines, with \ as escape.
func (s *Store) like(column string) string {
	if s.engine == Postgres {
		return column + ` ILIKE ? ESCAPE '\'`
	}
	return column + ` LIKE ? ESCAPE '\'`
}

func likePattern(text string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(strings.TrimSpace(text)) + "%"
}

// Page is a window on a list.
type Page struct {
	Limit, Offset int
}

func (p Page) apply(q *bun.SelectQuery) *bun.SelectQuery {
	limit := p.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return q.Limit(int64(limit)).Offset(int64(max(p.Offset, 0)))
}

// RecordRender adds a produced document to the history, without waiting for
// the copy.
func (s *Store) RecordRender(ctx context.Context, r *Render) error {
	r.CreatedAt = time.Now().UTC()
	_, err := s.DB.NewInsert().Model(r).Exec(ctx)
	return err
}

// RenderFilter selects the history; empty fields are ignored. TemplateIDs,
// when not nil, keeps the documents of these library templates only.
type RenderFilter struct {
	Text, Status, Source, Format string
	Since                        *time.Time
	TemplateIDs                  []int64
}

// Renders returns a page of the history, the newest first, and the count.
func (s *Store) Renders(ctx context.Context, f RenderFilter, p Page) ([]Render, int, error) {
	rows := []Render{}
	q := s.DB.NewSelect().Model(&rows).ExcludeColumn("data").ColumnExpr("data IS NOT NULL AS has_data").Order("id DESC")
	if f.Text != "" {
		pattern := likePattern(f.Text)
		q = q.WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
			return q.Where(s.like("template"), pattern).WhereOr(s.like("actor"), pattern).WhereOr(s.like("error"), pattern)
		})
	}
	switch {
	case f.TemplateIDs == nil:
	case len(f.TemplateIDs) == 0:
		q = q.Where("1 = 0")
	default:
		q = q.Where("template_id IN (?)", bun.In(f.TemplateIDs))
	}
	for column, value := range map[string]string{"status": f.Status, "source": f.Source, "format": f.Format} {
		if value != "" {
			q = q.Where("? = ?", bun.Ident(column), value)
		}
	}
	if f.Since != nil {
		q = q.Where("created_at >= ?", f.Since.UTC())
	}
	n, err := p.apply(q).ScanAndCount(ctx)
	return rows, int(n), err
}

// RenderTimes returns the time, origin, author, template, status and duration
// of the documents produced since a date, for the statistics.
func (s *Store) RenderTimes(ctx context.Context, since time.Time) ([]Render, error) {
	rows := []Render{}
	err := s.DB.NewSelect().Model(&rows).Column("created_at", "source", "actor", "template", "template_id", "status", "duration_ms").Where("created_at >= ?", since.UTC()).Order("id").Scan(ctx)
	return rows, err
}

// PurgeRenders forgets the history older than a date, and the templates sent
// with requests that no document uses any more.
func (s *Store) PurgeRenders(ctx context.Context, before time.Time) (int, error) {
	res, err := s.DB.NewDelete().Model((*Render)(nil)).Where("created_at < ?", before.UTC()).Exec(ctx)
	if err != nil {
		return 0, err
	}
	if _, err := s.DB.NewDelete().Model((*templateBlob)(nil)).
		Where("NOT EXISTS (SELECT 1 FROM renders AS r WHERE r.template_sha = tb.sha256)").Exec(ctx); err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// PurgeRenderData forgets the data of the documents produced before a date;
// their line stays in the history.
func (s *Store) PurgeRenderData(ctx context.Context, before time.Time) (int, error) {
	res, err := s.DB.NewUpdate().Model((*Render)(nil)).Set("data = NULL").
		Where("created_at < ?", before.UTC()).Where("data IS NOT NULL").Exec(ctx)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// Audit records an action in the journal, without waiting for the copy.
func (s *Store) Audit(ctx context.Context, e *AuditEntry) error {
	e.CreatedAt = time.Now().UTC()
	_, err := s.DB.NewInsert().Model(e).Exec(ctx)
	return err
}

// AuditFilter selects the journal; empty fields are ignored.
type AuditFilter struct {
	Text, Action, Actor string
}

// AuditEntries returns a page of the journal, the newest first, the count,
// and every action recorded so far (for the filter).
func (s *Store) AuditEntries(ctx context.Context, f AuditFilter, p Page) ([]AuditEntry, int, []string, error) {
	rows := []AuditEntry{}
	n, err := p.apply(s.auditQuery(&rows, f)).ScanAndCount(ctx)
	if err != nil {
		return nil, 0, nil, err
	}
	var actions []string
	if err := s.DB.NewSelect().Model((*AuditEntry)(nil)).ColumnExpr("DISTINCT action").Scan(ctx, &actions); err != nil {
		return nil, 0, nil, err
	}
	sort.Strings(actions)
	return rows, int(n), actions, nil
}

// AuditExport returns the entries of a filter, the newest first, up to max.
func (s *Store) AuditExport(ctx context.Context, f AuditFilter, max int) ([]AuditEntry, error) {
	rows := []AuditEntry{}
	return rows, s.auditQuery(&rows, f).Limit(int64(max)).Scan(ctx)
}

func (s *Store) auditQuery(rows *[]AuditEntry, f AuditFilter) *bun.SelectQuery {
	q := s.DB.NewSelect().Model(rows).Order("id DESC")
	if f.Text != "" {
		pattern := likePattern(f.Text)
		q = q.WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
			return q.Where(s.like("actor"), pattern).WhereOr(s.like("target"), pattern).WhereOr(s.like("details"), pattern).WhereOr(s.like("action"), pattern)
		})
	}
	if f.Action != "" {
		q = q.Where("action = ?", f.Action)
	}
	if f.Actor != "" {
		q = q.Where("actor = ?", f.Actor)
	}
	return q
}
