package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/uptrace/bun"
)

// User is a console account. Roles: admin, editor, viewer.
type User struct {
	bun.BaseModel `bun:"table:users,alias:u"`
	ID            int64      `bun:"id,pk,autoincrement"`
	Username      string     `bun:"username,notnull"`
	PasswordHash  string     `bun:"password_hash,notnull"`
	Role          string     `bun:"role,notnull"`
	CreatedAt     time.Time  `bun:"created_at,notnull"`
	DisplayName   string     `bun:"display_name,notnull"`
	FirstName     string     `bun:"first_name,notnull"`
	LastName      string     `bun:"last_name,notnull"`
	Email         string     `bun:"email,notnull"`
	Active        bool       `bun:"active,notnull"`
	Language      string     `bun:"language,notnull"`
	LastLoginAt   *time.Time `bun:"last_login_at"`
	AvatarIcon    string     `bun:"avatar_icon,notnull"`
	AvatarColor   string     `bun:"avatar_color,notnull"`
	TOTPSecret    string     `bun:"totp_secret,notnull"`
	TOTPEnabled   bool       `bun:"totp_enabled,notnull"`
}

type sessionRow struct {
	bun.BaseModel `bun:"table:sessions,alias:s"`
	ID            string     `bun:"id,pk"`
	UserID        int64      `bun:"user_id,notnull"`
	ExpiresAt     time.Time  `bun:"expires_at,notnull"`
	CreatedAt     *time.Time `bun:"created_at"`
	LastUsedAt    *time.Time `bun:"last_used_at"`
	UserAgent     string     `bun:"user_agent,notnull"`
}

// Session is an open session of an account, as its profile shows it.
type Session struct {
	ID         string
	CreatedAt  *time.Time
	LastUsedAt *time.Time
	ExpiresAt  time.Time
	UserAgent  string
}

// CountUsers tells whether the first administrator still has to be created.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	n, err := s.DB.NewSelect().Model((*User)(nil)).Count(ctx)
	return int(n), err
}

// CreateUser saves an account and returns once it is in the copy.
func (s *Store) CreateUser(ctx context.Context, username, passwordHash, role string) error {
	return s.CreateAccount(ctx, &User{Username: username, PasswordHash: passwordHash, Role: role})
}

// CreateAccount saves an account with all its fields and returns once it is in
// the copy.
func (s *Store) CreateAccount(ctx context.Context, u *User) error {
	u.CreatedAt, u.Active = time.Now().UTC(), true
	if u.Language == "" {
		u.Language = "fr"
	}
	if _, err := s.DB.NewInsert().Model(u).Exec(ctx); err != nil {
		return err
	}
	return s.Sync(ctx)
}

// Users lists the accounts, filtered by a text found in the username, the name
// or the e-mail, by role and by state; empty filters are ignored.
func (s *Store) Users(ctx context.Context, text, role string, active *bool) ([]User, error) {
	users := []User{}
	q := s.DB.NewSelect().Model(&users).Order("username")
	if text != "" {
		pattern := likePattern(text)
		q = q.WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
			return q.Where(s.like("username"), pattern).WhereOr(s.like("display_name"), pattern).
				WhereOr(s.like("first_name"), pattern).WhereOr(s.like("last_name"), pattern).WhereOr(s.like("email"), pattern)
		})
	}
	if role != "" {
		q = q.Where("role = ?", role)
	}
	if active != nil {
		q = q.Where("active = ?", *active)
	}
	return users, q.Scan(ctx)
}

// UpdateUser saves the names, e-mail, role, state and language of an account.
func (s *Store) UpdateUser(ctx context.Context, u *User) error {
	if _, err := s.DB.NewUpdate().Model(u).Column("display_name", "first_name", "last_name", "email", "role", "active", "language").WherePK().Exec(ctx); err != nil {
		return err
	}
	return s.Sync(ctx)
}

// DeleteUser removes an account and its sessions.
func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	if _, err := s.DB.NewDelete().Model((*sessionRow)(nil)).Where("user_id = ?", id).Exec(ctx); err != nil {
		return err
	}
	if _, err := s.DB.NewDelete().Model((*User)(nil)).Where("id = ?", id).Exec(ctx); err != nil {
		return err
	}
	return s.Sync(ctx)
}

// CountActiveAdmins guards against removing the last administrator.
func (s *Store) CountActiveAdmins(ctx context.Context) (int, error) {
	n, err := s.DB.NewSelect().Model((*User)(nil)).Where("role = ?", "admin").Where("active = ?", true).Count(ctx)
	return int(n), err
}

// RecordLogin notes the time of a successful sign-in.
func (s *Store) RecordLogin(ctx context.Context, id int64) error {
	_, err := s.DB.NewUpdate().Model((*User)(nil)).Set("last_login_at = ?", time.Now().UTC()).Where("id = ?", id).Exec(ctx)
	return err
}

// DeleteSessions signs an account out everywhere, except the session given.
func (s *Store) DeleteSessions(ctx context.Context, userID int64, except string) (int, error) {
	res, err := s.DB.NewDelete().Model((*sessionRow)(nil)).Where("user_id = ?", userID).Where("id <> ?", except).Exec(ctx)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// UserByUsername finds an account; ok is false when there is none.
func (s *Store) UserByUsername(ctx context.Context, username string) (u User, ok bool, err error) {
	err = s.DB.NewSelect().Model(&u).Where("username = ?", username).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return u, false, nil
	}
	return u, err == nil, err
}

// UserByLogin finds an account by its username, or else by its e-mail when
// a single account has it.
func (s *Store) UserByLogin(ctx context.Context, login string) (User, bool, error) {
	u, ok, err := s.UserByUsername(ctx, login)
	if err != nil || ok || !strings.Contains(login, "@") {
		return u, ok, err
	}
	var users []User
	if err := s.DB.NewSelect().Model(&users).Where("LOWER(email) = ?", strings.ToLower(login)).Limit(2).Scan(ctx); err != nil {
		return User{}, false, err
	}
	if len(users) != 1 {
		return User{}, false, nil
	}
	return users[0], true, nil
}

// UserByID finds an account by its number.
func (s *Store) UserByID(ctx context.Context, id int64) (u User, ok bool, err error) {
	err = s.DB.NewSelect().Model(&u).Where("id = ?", id).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return u, false, nil
	}
	return u, err == nil, err
}

// SetPassword replaces the password of an account.
func (s *Store) SetPassword(ctx context.Context, userID int64, passwordHash string) error {
	if _, err := s.DB.NewUpdate().Model((*User)(nil)).Set("password_hash = ?", passwordHash).Where("id = ?", userID).Exec(ctx); err != nil {
		return err
	}
	return s.Sync(ctx)
}

// CreateSession records a session by the hash of its token. Sessions are not
// awaited in the copy: losing one only means signing in again.
func (s *Store) CreateSession(ctx context.Context, tokenHash string, userID int64, expires time.Time, userAgent string) error {
	now := time.Now().UTC()
	if _, err := s.DB.NewDelete().Model((*sessionRow)(nil)).Where("expires_at < ?", now).Exec(ctx); err != nil {
		return err
	}
	if len(userAgent) > 300 {
		userAgent = userAgent[:300]
	}
	_, err := s.DB.NewInsert().Model(&sessionRow{ID: tokenHash, UserID: userID, ExpiresAt: expires.UTC(), CreatedAt: &now, LastUsedAt: &now, UserAgent: userAgent}).Exec(ctx)
	return err
}

// UserSessions lists the open sessions of an account, the latest used first.
func (s *Store) UserSessions(ctx context.Context, userID int64) ([]Session, error) {
	var rows []sessionRow
	if err := s.DB.NewSelect().Model(&rows).Where("user_id = ?", userID).Where("expires_at > ?", time.Now().UTC()).
		OrderExpr("last_used_at DESC").Scan(ctx); err != nil {
		return nil, err
	}
	out := make([]Session, 0, len(rows))
	for _, r := range rows {
		out = append(out, Session{ID: r.ID, CreatedAt: r.CreatedAt, LastUsedAt: r.LastUsedAt, ExpiresAt: r.ExpiresAt, UserAgent: r.UserAgent})
	}
	return out, nil
}

// TouchSession notes the use of a session, without waiting for the copy.
func (s *Store) TouchSession(ctx context.Context, tokenHash string, at time.Time) error {
	_, err := s.DB.NewUpdate().Model((*sessionRow)(nil)).Set("last_used_at = ?", at.UTC()).Where("id = ?", tokenHash).Exec(ctx)
	return err
}

// DeleteUserSession closes one session of an account.
func (s *Store) DeleteUserSession(ctx context.Context, userID int64, id string) (bool, error) {
	res, err := s.DB.NewDelete().Model((*sessionRow)(nil)).Where("user_id = ?", userID).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// SaveAvatar keeps the icon and colour an account chose.
func (s *Store) SaveAvatar(ctx context.Context, userID int64, icon, color string) error {
	if _, err := s.DB.NewUpdate().Model((*User)(nil)).Set("avatar_icon = ?", icon).Set("avatar_color = ?", color).Where("id = ?", userID).Exec(ctx); err != nil {
		return err
	}
	return s.Sync(ctx)
}

// SetTOTP saves or removes the authenticator of an account.
func (s *Store) SetTOTP(ctx context.Context, userID int64, secret string, enabled bool) error {
	if _, err := s.DB.NewUpdate().Model((*User)(nil)).Set("totp_secret = ?", secret).Set("totp_enabled = ?", enabled).Where("id = ?", userID).Exec(ctx); err != nil {
		return err
	}
	return s.Sync(ctx)
}

// SessionUser returns the active account of a valid session.
func (s *Store) SessionUser(ctx context.Context, tokenHash string, idle time.Duration) (u User, ok bool, err error) {
	now := time.Now().UTC()
	q := s.DB.NewSelect().Model(&u).
		Join("JOIN sessions AS s ON s.user_id = u.id").
		Where("s.id = ?", tokenHash).
		Where("s.expires_at > ?", now).
		Where("u.active = ?", true)
	if idle > 0 {
		q = q.Where("s.last_used_at IS NULL OR s.last_used_at > ?", now.Add(-idle))
	}
	err = q.Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return u, false, nil
	}
	return u, err == nil, err
}

// DeleteSession signs a session out.
func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.DB.NewDelete().Model((*sessionRow)(nil)).Where("id = ?", tokenHash).Exec(ctx)
	return err
}

// Settings returns every setting of the configuration page. Keys with a dot
// (metrics.token, branding.logo…) and the logo state are kept apart.
func (s *Store) Settings(ctx context.Context) (map[string]string, error) {
	var rows []settingRow
	if err := s.DB.NewSelect().Model(&rows).Where("key NOT LIKE '%.%'").Where("key <> 'branding'").Scan(ctx); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.Key] = r.Value
	}
	return out, nil
}

// SaveSettings writes several settings at once and returns once they are in
// the copy. An empty value removes the setting (back to its default).
func (s *Store) SaveSettings(ctx context.Context, values map[string]string) error {
	now := time.Now().UTC()
	err := s.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		for k, v := range values {
			if v == "" {
				if _, err := tx.NewDelete().Model((*settingRow)(nil)).Where("key = ?", k).Exec(ctx); err != nil {
					return err
				}
				continue
			}
			row := settingRow{Key: k, Value: v, UpdatedAt: now}
			if _, err := tx.NewInsert().Model(&row).
				On("CONFLICT (key) DO UPDATE").
				Set("value = EXCLUDED.value").
				Set("updated_at = EXCLUDED.updated_at").
				Exec(ctx); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return s.Sync(ctx)
}
