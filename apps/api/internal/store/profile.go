package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/uptrace/bun"
)

// UserToken is a personal token of an account, for its MCP client or its
// scripts. Only the hash of its secret is kept.
type UserToken struct {
	bun.BaseModel `bun:"table:user_tokens,alias:ut"`
	ID            int64      `bun:"id,pk,autoincrement"`
	UserID        int64      `bun:"user_id,notnull"`
	Name          string     `bun:"name,notnull"`
	Prefix        string     `bun:"prefix,notnull"`
	Hash          string     `bun:"hash,notnull"`
	CreatedAt     time.Time  `bun:"created_at,notnull"`
	ExpiresAt     *time.Time `bun:"expires_at"`
	LastUsedAt    *time.Time `bun:"last_used_at"`
	RevokedAt     *time.Time `bun:"revoked_at"`
}

// CreateUserToken saves a token and returns once it is in the copy.
func (s *Store) CreateUserToken(ctx context.Context, t *UserToken) error {
	t.CreatedAt = time.Now().UTC()
	if _, err := s.DB.NewInsert().Model(t).Exec(ctx); err != nil {
		return err
	}
	return s.Sync(ctx)
}

// UserTokens lists the tokens of an account, the newest first.
func (s *Store) UserTokens(ctx context.Context, userID int64) ([]UserToken, error) {
	rows := []UserToken{}
	err := s.DB.NewSelect().Model(&rows).Where("user_id = ?", userID).Order("id DESC").Scan(ctx)
	return rows, err
}

// ActiveUserToken finds the token of a hash with its active account.
func (s *Store) ActiveUserToken(ctx context.Context, hash string) (t UserToken, u User, ok bool, err error) {
	err = s.DB.NewSelect().Model(&t).Where("hash = ?", hash).Where("revoked_at IS NULL").
		WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
			return q.Where("expires_at IS NULL").WhereOr("expires_at > ?", time.Now().UTC())
		}).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return t, u, false, nil
	}
	if err != nil {
		return t, u, false, err
	}
	u, ok, err = s.UserByID(ctx, t.UserID)
	return t, u, ok && u.Active, err
}

// TouchUserToken notes the use of a token, without waiting for the copy.
func (s *Store) TouchUserToken(ctx context.Context, id int64, at time.Time) error {
	_, err := s.DB.NewUpdate().Model((*UserToken)(nil)).Set("last_used_at = ?", at.UTC()).Where("id = ?", id).Exec(ctx)
	return err
}

// RevokeUserToken stops a token of an account at once.
func (s *Store) RevokeUserToken(ctx context.Context, userID, id int64) (bool, error) {
	res, err := s.DB.NewUpdate().Model((*UserToken)(nil)).Set("revoked_at = ?", time.Now().UTC()).
		Where("id = ?", id).Where("user_id = ?", userID).Where("revoked_at IS NULL").Exec(ctx)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return false, nil
	}
	return true, s.Sync(ctx)
}

// MCPCall is one call of an MCP client: who, which tool, how it went. Neither
// the arguments nor the answer are kept.
type MCPCall struct {
	bun.BaseModel `bun:"table:mcp_calls,alias:mc"`
	ID            int64     `bun:"id,pk,autoincrement"`
	CreatedAt     time.Time `bun:"created_at,notnull"`
	UserID        int64     `bun:"user_id,notnull"`
	Actor         string    `bun:"actor,notnull"`
	Via           string    `bun:"via,notnull"`
	Method        string    `bun:"method,notnull"`
	Tool          string    `bun:"tool,notnull"`
	Status        string    `bun:"status,notnull"`
	Error         string    `bun:"error,notnull"`
	DurationMs    int       `bun:"duration_ms,notnull"`
}

// RecordMCPCall adds a call to the journal, without waiting for the copy.
func (s *Store) RecordMCPCall(ctx context.Context, c *MCPCall) error {
	c.CreatedAt = time.Now().UTC()
	_, err := s.DB.NewInsert().Model(c).Exec(ctx)
	return err
}

// MCPCalls returns a page of the calls, the newest first; userID 0 lists all.
func (s *Store) MCPCalls(ctx context.Context, userID int64, p Page) ([]MCPCall, int, error) {
	rows := []MCPCall{}
	q := s.DB.NewSelect().Model(&rows).Order("id DESC")
	if userID != 0 {
		q = q.Where("user_id = ?", userID)
	}
	n, err := p.apply(q).ScanAndCount(ctx)
	return rows, int(n), err
}

// PurgeMCPCalls forgets the calls older than a date.
func (s *Store) PurgeMCPCalls(ctx context.Context, before time.Time) error {
	_, err := s.DB.NewDelete().Model((*MCPCall)(nil)).Where("created_at < ?", before.UTC()).Exec(ctx)
	return err
}
