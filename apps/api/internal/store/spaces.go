package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/uptrace/bun"
)

// Space is the templates of a team: those whose path starts with one of its
// prefixes (one per line; none for the whole library), with quotas of
// documents for the keys kept to it.
type Space struct {
	bun.BaseModel `bun:"table:spaces,alias:sp"`
	ID            int64     `bun:"id,pk,autoincrement"`
	Name          string    `bun:"name,notnull"`
	Prefixes      string    `bun:"prefixes,notnull"`
	DailyQuota    int       `bun:"daily_quota,notnull"`
	MonthlyQuota  int       `bun:"monthly_quota,notnull"`
	CreatedAt     time.Time `bun:"created_at,notnull"`
	CreatedBy     string    `bun:"created_by,notnull"`
}

// ErrSpaceUsed is a space that keys still use.
var ErrSpaceUsed = errors.New("space used by keys")

// Spaces lists the spaces by name.
func (s *Store) Spaces(ctx context.Context) ([]Space, error) {
	rows := []Space{}
	err := s.DB.NewSelect().Model(&rows).Order("name").Scan(ctx)
	return rows, err
}

// SpaceByID finds a space.
func (s *Store) SpaceByID(ctx context.Context, id int64) (sp Space, ok bool, err error) {
	err = s.DB.NewSelect().Model(&sp).Where("id = ?", id).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return sp, false, nil
	}
	return sp, err == nil, err
}

// SaveSpace creates a space, or updates it when it has an ID.
func (s *Store) SaveSpace(ctx context.Context, sp *Space) error {
	var err error
	if sp.ID == 0 {
		sp.CreatedAt = time.Now().UTC()
		_, err = s.DB.NewInsert().Model(sp).Exec(ctx)
	} else {
		_, err = s.DB.NewUpdate().Model(sp).Column("name", "prefixes", "daily_quota", "monthly_quota").WherePK().Exec(ctx)
	}
	if err != nil {
		return err
	}
	return s.Sync(ctx)
}

// DeleteSpace removes a space that no active key uses; false when there is
// none.
func (s *Store) DeleteSpace(ctx context.Context, id int64) (bool, error) {
	used, err := s.DB.NewSelect().Model((*APIKey)(nil)).Where("space_id = ?", id).Where("revoked_at IS NULL").Exists(ctx)
	if err != nil {
		return false, err
	}
	if used {
		return false, ErrSpaceUsed
	}
	res, err := s.DB.NewDelete().Model((*Space)(nil)).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, nil
	}
	return true, s.Sync(ctx)
}

type usageCount struct {
	bun.BaseModel `bun:"table:usage_counts,alias:u"`
	Scope         string `bun:"scope,pk"`
	Period        string `bun:"period,pk"`
	Count         int64  `bun:"count,notnull"`
}

// Periods are the day and the month a moment counts in.
func Periods(at time.Time) (day, month string) {
	return at.Format("2006-01-02"), at.Format("2006-01")
}

// AddUsage counts one document for each scope (key:12, space:3), in its day
// and its month.
func (s *Store) AddUsage(ctx context.Context, at time.Time, scopes ...string) error {
	day, month := Periods(at)
	rows := make([]usageCount, 0, 2*len(scopes))
	for _, scope := range scopes {
		rows = append(rows, usageCount{Scope: scope, Period: day, Count: 1}, usageCount{Scope: scope, Period: month, Count: 1})
	}
	if len(rows) == 0 {
		return nil
	}
	_, err := s.DB.NewInsert().Model(&rows).On("CONFLICT (scope, period) DO UPDATE").Set("count = u.count + 1").Exec(ctx)
	return err
}

// Usage gives the documents of each scope in the day and the month of at.
func (s *Store) Usage(ctx context.Context, at time.Time, scopes ...string) (day, month map[string]int64, err error) {
	d, m := Periods(at)
	day, month = map[string]int64{}, map[string]int64{}
	if len(scopes) == 0 {
		return day, month, nil
	}
	var rows []usageCount
	err = s.DB.NewSelect().Model(&rows).Where("scope IN (?)", bun.In(scopes)).Where("period IN (?)", bun.In([]string{d, m})).Scan(ctx)
	for _, r := range rows {
		if r.Period == d {
			day[r.Scope] = r.Count
		} else {
			month[r.Scope] = r.Count
		}
	}
	return day, month, err
}

// PurgeUsage forgets the counts of the days before a date, and of the months
// before its month.
func (s *Store) PurgeUsage(ctx context.Context, before time.Time) error {
	day, month := Periods(before)
	_, err := s.DB.NewDelete().Model((*usageCount)(nil)).
		WhereGroup(" AND ", func(q *bun.DeleteQuery) *bun.DeleteQuery {
			return q.Where("length(period) = 10 AND period < ?", day).WhereOr("length(period) = 7 AND period < ?", month)
		}).Exec(ctx)
	return err
}
