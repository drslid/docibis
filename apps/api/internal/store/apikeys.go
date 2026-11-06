package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/uptrace/bun"
)

// APIKey is the key of an application. Only the hash of its secret is kept;
// the prefix identifies it in the console and in the logs.
type APIKey struct {
	bun.BaseModel `bun:"table:api_keys,alias:k"`
	ID            int64      `bun:"id,pk,autoincrement"`
	Name          string     `bun:"name,notnull"`
	Prefix        string     `bun:"prefix,notnull"`
	Hash          string     `bun:"hash,notnull"`
	Scopes        string     `bun:"scopes,notnull"`
	CreatedBy     string     `bun:"created_by,notnull"`
	CreatedAt     time.Time  `bun:"created_at,notnull"`
	ExpiresAt     *time.Time `bun:"expires_at"`
	LastUsedAt    *time.Time `bun:"last_used_at"`
	RevokedAt     *time.Time `bun:"revoked_at"`
	// Calls allowed per minute; 0 takes the setting api_rate_per_minute.
	RatePerMinute int `bun:"rate_per_minute,notnull"`
	// SpaceID keeps the key to the templates of a space; 0 for the whole library.
	SpaceID int64 `bun:"space_id,notnull"`
	// Documents allowed per day and per month; 0 for no limit.
	DailyQuota   int `bun:"daily_quota,notnull"`
	MonthlyQuota int `bun:"monthly_quota,notnull"`
	// UserID is the account of a personal token seen as a key; 0 for a key.
	UserID int64 `bun:"-"`
}

// CreateAPIKey saves a key and returns once it is in the copy: the secret has
// just been shown, it must not be lost.
func (s *Store) CreateAPIKey(ctx context.Context, k *APIKey) error {
	if _, err := s.DB.NewInsert().Model(k).Exec(ctx); err != nil {
		return err
	}
	return s.Sync(ctx)
}

// APIKeys lists every key, the newest first.
func (s *Store) APIKeys(ctx context.Context) ([]APIKey, error) {
	keys := []APIKey{}
	err := s.DB.NewSelect().Model(&keys).Order("id DESC").Scan(ctx)
	return keys, err
}

// ActiveAPIKey finds a key by the hash of its secret, unless it is revoked or
// expired.
func (s *Store) ActiveAPIKey(ctx context.Context, hash string) (k APIKey, ok bool, err error) {
	err = s.DB.NewSelect().Model(&k).
		Where("hash = ?", hash).
		Where("revoked_at IS NULL").
		WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
			return q.Where("expires_at IS NULL").WhereOr("expires_at > ?", time.Now().UTC())
		}).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return k, false, nil
	}
	return k, err == nil, err
}

// RevokeAPIKey stops a key at once; ok is false when it does not exist.
func (s *Store) RevokeAPIKey(ctx context.Context, id int64) (ok bool, err error) {
	res, err := s.DB.NewUpdate().Model((*APIKey)(nil)).
		Set("revoked_at = ?", time.Now().UTC()).
		Where("id = ?", id).
		Where("revoked_at IS NULL").
		Exec(ctx)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var exists bool
		if exists, err = s.DB.NewSelect().Model((*APIKey)(nil)).Where("id = ?", id).Exists(ctx); err != nil || !exists {
			return false, err
		}
		return true, nil
	}
	return true, s.Sync(ctx)
}

// TouchAPIKey records the last use of a key, without waiting for the copy.
func (s *Store) TouchAPIKey(ctx context.Context, id int64, at time.Time) error {
	_, err := s.DB.NewUpdate().Model((*APIKey)(nil)).Set("last_used_at = ?", at.UTC()).Where("id = ?", id).Exec(ctx)
	return err
}

// APIKeyByID finds a key, active or not.
func (s *Store) APIKeyByID(ctx context.Context, id int64) (k APIKey, ok bool, err error) {
	err = s.DB.NewSelect().Model(&k).Where("id = ?", id).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return k, false, nil
	}
	return k, err == nil, err
}

// UpdateAPIKey saves the name, the calls per minute, the space and the quotas
// of a key.
func (s *Store) UpdateAPIKey(ctx context.Context, k *APIKey) error {
	if _, err := s.DB.NewUpdate().Model(k).Column("name", "rate_per_minute", "space_id", "daily_quota", "monthly_quota").WherePK().Exec(ctx); err != nil {
		return err
	}
	return s.Sync(ctx)
}
