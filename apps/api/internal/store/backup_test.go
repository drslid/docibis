package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
)

func openSQLite(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()
	s, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "docibis.db")}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(ctx) })
	return s
}

// fill writes rows with every kind of value: text, numbers, booleans, dates,
// files with any byte, empty and NULL values.
func fill(t *testing.T, s *Store, full bool) {
	t.Helper()
	ctx := context.Background()
	if err := s.SetSetting(ctx, "instance_name", "INT"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateUser(ctx, "admin", "hash", "admin"); err != nil {
		t.Fatal(err)
	}
	users, err := s.Users(ctx, "", "", nil)
	if err != nil || len(users) == 0 {
		t.Fatalf("users: %v %v", users, err)
	}
	if err := s.SetTOTP(ctx, users[0].ID, "sealed:abc", true); err != nil {
		t.Fatal(err)
	}
	every := make([]byte, 256)
	for i := range every {
		every[i] = byte(i)
	}
	tpl := &Template{Path: "habitation/contrat.docx", Kind: "template", CreatedBy: "admin"}
	if err := s.AddTemplateVersion(ctx, tpl, &TemplateVersion{Content: every, Size: 256, SHA256: "a", CreatedBy: "admin"}, true); err != nil {
		t.Fatal(err)
	}
	if !full {
		return
	}
	future := time.Now().Add(time.Hour).UTC()
	if err := s.CreateAPIKey(ctx, &APIKey{Name: "crm", Prefix: "dib_aaaa", Hash: "h1", Scopes: "render", CreatedBy: "admin", CreatedAt: time.Now().UTC(), ExpiresAt: &future}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordRender(ctx, &Render{CreatedAt: time.Now().UTC(), Source: "api", Actor: "crm", Template: tpl.Path, Format: "pdf", Status: "ok", Size: 1234, DurationMs: 87, Engine: "standard", Data: []byte{0x1f, 0x8b, 0}, TemplateID: tpl.ID, TemplateVersion: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddFont(ctx, &Font{File: "a.ttf", Family: "A", Style: "Regular", Size: 3, SHA256: "f", Content: []byte{}, CreatedAt: time.Now().UTC(), CreatedBy: "admin"}); err != nil {
		t.Fatal(err)
	}
}

// snapshot reads every table as text, the same way whatever the engine.
func snapshot(t *testing.T, s *Store) map[string][]string {
	t.Helper()
	ctx := context.Background()
	known, err := knownTables(ctx)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	for table := range known {
		rows, err := s.DB.DB.QueryContext(ctx, "SELECT * FROM "+quote(table))
		if err != nil {
			t.Fatalf("%s: %v", table, err)
		}
		cols, _ := rows.Columns()
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		for rows.Next() {
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			fields := make([]string, len(cols))
			for i, v := range vals {
				fields[i] = cols[i] + "=" + text(v)
			}
			slices.Sort(fields)
			out[table] = append(out[table], strings.Join(fields, " "))
		}
		rows.Close()
		slices.Sort(out[table])
	}
	return out
}

func text(v any) string {
	switch x := v.(type) {
	case nil:
		return "NULL"
	case bool:
		if x {
			return "1"
		}
		return "0"
	case int64:
		return strconv.FormatInt(x, 10)
	case []byte:
		return "0x" + hex.EncodeToString(x)
	case time.Time:
		return x.UTC().Format(time.RFC3339Nano)
	}
	return fmt.Sprint(v)
}

func dump(t *testing.T, s *Store) []byte {
	t.Helper()
	var buf bytes.Buffer
	if _, err := s.Dump(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sameTables(t *testing.T, got, want map[string][]string) {
	t.Helper()
	for table := range want {
		if !slices.Equal(got[table], want[table]) {
			t.Fatalf("table %s:\n got  %q\n want %q", table, got[table], want[table])
		}
	}
}

func TestBackupSQLite(t *testing.T) {
	ctx := context.Background()
	a := openSQLite(t)
	fill(t, a, true)
	want := snapshot(t, a)
	raw := dump(t, a)

	// Same schema: replaced in one transaction.
	b := openSQLite(t)
	fill(t, b, false)
	if err := b.SetSetting(ctx, "instance_name", "REC"); err != nil {
		t.Fatal(err)
	}
	sum, err := b.Restore(ctx, bytes.NewReader(raw), false)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Rows["users"] != 1 || sum.Rows["template_versions"] != 1 || sum.Engine != SQLite {
		t.Fatalf("summary: %+v", sum)
	}
	sameTables(t, snapshot(t, b), want)
	if v, _, _ := b.Setting(ctx, "instance_name"); v != "INT" {
		t.Fatalf("setting after restore: %q", v)
	}
	if err := b.CreateAPIKey(ctx, &APIKey{Name: "after", Prefix: "dib_bbbb", Hash: "h2", Scopes: "render", CreatedBy: "admin", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("new row after a restore: %v", err)
	}

	// A cut backup changes nothing.
	if _, err := b.Restore(ctx, bytes.NewReader(raw[:len(raw)/2]), false); err == nil {
		t.Fatal("cut backup restored")
	}
	if keys, _ := b.APIKeys(ctx); len(keys) != 2 {
		t.Fatalf("keys after a failed restore: %d", len(keys))
	}

	// Tables of another version: only dropped when forced.
	c := openSQLite(t)
	if _, err := c.DB.ExecContext(ctx, "CREATE TABLE later_feature (x INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Restore(ctx, bytes.NewReader(raw), false); !errors.Is(err, ErrUnknownTables) {
		t.Fatalf("unknown table kept silently: %v", err)
	}
	if _, err := c.Restore(ctx, bytes.NewReader(raw), true); err != nil {
		t.Fatal(err)
	}
	sameTables(t, snapshot(t, c), want)
	if tables, _ := c.tables(ctx, c.DB.DB); slices.Contains(tables, "later_feature") {
		t.Fatal("unknown table left after a forced restore")
	}
}

// TestBackupOlderSchema restores a backup made before the last migrations:
// the tables are made at its schema, filled, then migrated.
func TestBackupOlderSchema(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "old.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	old := &Store{DB: bun.NewDB(db, sqlitedialect.New()), engine: SQLite}
	p, err := old.provider()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 14); err != nil {
		t.Fatal(err)
	}
	fill(t, old, false)
	raw := dump(t, old)

	s := openSQLite(t)
	sum, err := s.Restore(ctx, bytes.NewReader(raw), false)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Schema != 14 {
		t.Fatalf("schema of the backup: %d", sum.Schema)
	}
	sp, _ := s.provider()
	sources := sp.ListSources()
	if v, err := sp.GetDBVersion(ctx); err != nil || v != sources[len(sources)-1].Version {
		t.Fatalf("not migrated after the restore: %d %v", v, err)
	}
	if users, _ := s.Users(ctx, "", "", nil); len(users) != 1 || !users[0].TOTPEnabled {
		t.Fatalf("users: %+v", users)
	}
	if err := s.SaveSpace(ctx, &Space{Name: "habitation", Prefixes: "habitation/"}); err != nil {
		t.Fatalf("table of a later migration: %v", err)
	}
}

// checkBackups restores the PostgreSQL database into SQLite and back into
// another PostgreSQL schema: nothing changes on the way.
func checkBackups(t *testing.T, s *Store, other *Store) {
	t.Helper()
	ctx := context.Background()
	fill(t, s, true)
	want := snapshot(t, s)

	lite := openSQLite(t)
	if _, err := lite.Restore(ctx, bytes.NewReader(dump(t, s)), false); err != nil {
		t.Fatalf("PostgreSQL to SQLite: %v", err)
	}
	sameTables(t, snapshot(t, lite), want)

	if _, err := other.Restore(ctx, bytes.NewReader(dump(t, lite)), false); err != nil {
		t.Fatalf("SQLite to PostgreSQL: %v", err)
	}
	sameTables(t, snapshot(t, other), want)
	keys, _ := other.APIKeys(ctx)
	k := &APIKey{Name: "after", Prefix: "dib_zzzz", Hash: "hz", Scopes: "render", CreatedBy: "admin", CreatedAt: time.Now().UTC()}
	if err := other.CreateAPIKey(ctx, k); err != nil {
		t.Fatalf("new row after a restore: %v", err)
	}
	for _, old := range keys {
		if k.ID <= old.ID {
			t.Fatalf("identity counter not moved: new %d, restored %d", k.ID, old.ID)
		}
	}

	// Both engines have the same tables and columns.
	for table := range want {
		pg, _, err := s.columns(ctx, s.DB.DB, table)
		if err != nil {
			t.Fatal(err)
		}
		sq, _, err := lite.columns(ctx, lite.DB.DB, table)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(slices.Sorted(maps.Keys(pg)), slices.Sorted(maps.Keys(sq))) {
			t.Fatalf("table %s: PostgreSQL %v, SQLite %v", table, slices.Sorted(maps.Keys(pg)), slices.Sorted(maps.Keys(sq)))
		}
	}
}
