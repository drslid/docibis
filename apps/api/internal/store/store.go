// Package store opens the database: SQLite copied continuously by Litestream
// (default), or PostgreSQL. Both run the same queries through bun and their
// own migrations.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // "pgx" driver
	"github.com/pressly/goose/v3"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	_ "modernc.org/sqlite" // "sqlite" driver, required by Litestream

	"docibis/migrations"
)

// Engine is the database engine.
type Engine string

const (
	SQLite   Engine = "sqlite"
	Postgres Engine = "postgres"
)

// Options says where the database lives.
type Options struct {
	// Path is the SQLite file, used when URL is empty.
	Path string
	// URL is a PostgreSQL address; it replaces SQLite and its copy.
	URL string
	// ReplicaURL is the Litestream copy (s3://… or file://…); empty: no copy.
	ReplicaURL string
}

// Store is the open database.
type Store struct {
	DB      *bun.DB
	engine  Engine
	replica *replica
}

// Open restores the SQLite file from its copy when it is missing, starts the
// copy, opens the database and applies pending migrations. With a URL, it
// connects to PostgreSQL instead.
func Open(ctx context.Context, o Options, logger *slog.Logger) (*Store, error) {
	if o.URL != "" {
		return openPostgres(ctx, o.URL)
	}
	if o.Path == "" {
		return nil, errors.New("database path is required")
	}
	if err := os.MkdirAll(filepath.Dir(o.Path), 0o750); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	var rep *replica
	if o.ReplicaURL != "" {
		var err error
		if rep, err = startReplica(ctx, o.Path, o.ReplicaURL, logger); err != nil {
			return nil, err
		}
	} else {
		logger.Warn("database copy disabled: data is lost with the container")
	}
	dsn := "file:" + o.Path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(wal)" +
		"&_pragma=foreign_keys(1)&_pragma=synchronous(normal)&_txlock=immediate"
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		if rep != nil {
			_ = rep.close(ctx)
		}
		return nil, err
	}
	s := &Store{DB: bun.NewDB(sqldb, sqlitedialect.New()), engine: SQLite, replica: rep}
	if err := s.migrate(ctx); err != nil {
		_ = s.Close(ctx)
		return nil, err
	}
	return s, nil
}

func openPostgres(ctx context.Context, url string) (*Store, error) {
	sqldb, err := sql.Open("pgx", url)
	if err != nil {
		return nil, err
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := sqldb.PingContext(pingCtx); err != nil {
		_ = sqldb.Close()
		return nil, fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	s := &Store{DB: bun.NewDB(sqldb, pgdialect.New()), engine: Postgres}
	if err := s.migrate(ctx); err != nil {
		_ = s.DB.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate(ctx context.Context) error {
	p, err := s.provider()
	if err != nil {
		return err
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

func (s *Store) provider() (*goose.Provider, error) {
	dir, dialect := "sqlite", goose.DialectSQLite3
	if s.engine == Postgres {
		dir, dialect = "postgres", goose.DialectPostgres
	}
	fsys, err := fs.Sub(migrations.FS, dir)
	if err != nil {
		return nil, err
	}
	p, err := goose.NewProvider(dialect, s.DB.DB, fsys)
	if err != nil {
		return nil, fmt.Errorf("migrations: %w", err)
	}
	return p, nil
}

// Engine is the database engine.
func (s *Store) Engine() Engine { return s.engine }

// Ping checks the connection.
func (s *Store) Ping(ctx context.Context) error { return s.DB.PingContext(ctx) }

// Sync waits until every committed write is in the copy. Console writes call
// it before answering, so an acknowledged change survives a crash.
func (s *Store) Sync(ctx context.Context) error {
	if s.replica == nil {
		return nil
	}
	return s.replica.sync(ctx)
}

// Replication describes the copy, for the console and /api/info.
type Replication struct {
	Enabled bool   `json:"enabled"`
	Target  string `json:"target,omitempty"`
	// Locked is true when a lease on S3 guarantees a single writer.
	Locked bool `json:"locked"`
	// LeaseLost means another task took the database: this one must stop.
	LeaseLost bool `json:"leaseLost,omitempty"`
}

// Replication reports the state of the copy.
func (s *Store) Replication() Replication {
	if s.replica == nil {
		return Replication{}
	}
	return s.replica.status()
}

// Close closes the database, then flushes and stops the copy.
func (s *Store) Close(ctx context.Context) error {
	err := s.DB.Close()
	if s.replica != nil {
		err = errors.Join(err, s.replica.close(ctx))
	}
	return err
}

type settingRow struct {
	bun.BaseModel `bun:"table:settings"`
	Key           string    `bun:"key,pk"`
	Value         string    `bun:"value,notnull"`
	UpdatedAt     time.Time `bun:"updated_at,notnull"`
}

// Setting reads a setting; ok is false when it was never saved.
func (s *Store) Setting(ctx context.Context, key string) (value string, ok bool, err error) {
	var row settingRow
	err = s.DB.NewSelect().Model(&row).Where("key = ?", key).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return row.Value, err == nil, err
}

// SetSetting saves a setting and returns once it is in the copy.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	row := settingRow{Key: key, Value: value, UpdatedAt: time.Now().UTC()}
	_, err := s.DB.NewInsert().Model(&row).
		On("CONFLICT (key) DO UPDATE").
		Set("value = EXCLUDED.value").
		Set("updated_at = EXCLUDED.updated_at").
		Exec(ctx)
	if err != nil {
		return err
	}
	return s.Sync(ctx)
}

// ClaimDay records under key that a daily job runs on day (2026-11-06): false
// when another task already did, so that only one task runs it.
func (s *Store) ClaimDay(ctx context.Context, key, day string) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
		WHERE settings.value < excluded.value`, key, day, time.Now().UTC())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, s.Sync(ctx)
}
