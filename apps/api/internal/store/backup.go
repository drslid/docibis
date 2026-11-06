package store

import (
	"context"
	"database/sql"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"

	"docibis/internal/version"
)

// A backup holds every table of DocIbis in one stream that SQLite and
// PostgreSQL both read back: a backup of either engine restores into the
// other.

const dumpFormat = 1

// ErrUnknownTables means the database holds tables this version does not
// know, such as those of a later version: Restore drops them only when forced.
var ErrUnknownTables = errors.New("tables unknown to this version of DocIbis")

// Summary describes a backup.
type Summary struct {
	Format  int              `json:"format"`
	Version string           `json:"version"`
	Engine  Engine           `json:"engine"`
	Schema  int64            `json:"schema"`
	Created time.Time        `json:"created"`
	Rows    map[string]int64 `json:"rows"`
}

type dumpHeader struct {
	Format  int
	Version string
	Engine  Engine
	Schema  int64
	Created time.Time
	Tables  []string
}

type dumpTable struct {
	Name    string
	Columns []string
}

type dumpEnd struct {
	Rows map[string]int64
}

// dumpRecord is one message of the stream: the header, the start of a table,
// one of its rows, and at last the counts.
type dumpRecord struct {
	Header *dumpHeader
	Table  *dumpTable
	Row    []cell
	End    *dumpEnd
}

type cellKind uint8

const (
	cellNull cellKind = iota
	cellInt
	cellFloat
	cellText
	cellBytes
	cellBool
	cellTime
)

// cell is one value, typed so that each engine gets it back its own way.
type cell struct {
	K cellKind
	I int64
	F float64
	S string
	B []byte
	T time.Time
}

func toCell(v any) (cell, error) {
	switch x := v.(type) {
	case nil:
		return cell{}, nil
	case int64:
		return cell{K: cellInt, I: x}, nil
	case float64:
		return cell{K: cellFloat, F: x}, nil
	case string:
		return cell{K: cellText, S: x}, nil
	case []byte:
		return cell{K: cellBytes, B: x}, nil
	case bool:
		if x {
			return cell{K: cellBool, I: 1}, nil
		}
		return cell{K: cellBool}, nil
	case time.Time:
		return cell{K: cellTime, T: x.UTC()}, nil
	}
	return cell{}, fmt.Errorf("unexpected type %T", v)
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Dump writes every table of DocIbis to w, as seen at one instant.
func (s *Store) Dump(ctx context.Context, w io.Writer) (Summary, error) {
	opts := &sql.TxOptions{ReadOnly: true}
	if s.engine == Postgres {
		opts.Isolation = sql.LevelRepeatableRead
	}
	tx, err := s.DB.DB.BeginTx(ctx, opts)
	if err != nil {
		return Summary{}, err
	}
	defer tx.Rollback()
	var schema sql.NullInt64
	if err := tx.QueryRowContext(ctx, "SELECT MAX(version_id) FROM goose_db_version WHERE is_applied").Scan(&schema); err != nil {
		return Summary{}, fmt.Errorf("schema version: %w", err)
	}
	present, err := s.tables(ctx, tx)
	if err != nil {
		return Summary{}, err
	}
	known, err := knownTables(ctx)
	if err != nil {
		return Summary{}, err
	}
	present = slices.DeleteFunc(present, func(t string) bool { return !known[t] })
	tables, err := s.parentsFirst(ctx, tx, present)
	if err != nil {
		return Summary{}, err
	}
	sum := Summary{Format: dumpFormat, Version: version.Version, Engine: s.engine, Schema: schema.Int64, Created: time.Now().UTC(), Rows: map[string]int64{}}
	enc := gob.NewEncoder(w)
	head := dumpHeader{Format: sum.Format, Version: sum.Version, Engine: sum.Engine, Schema: sum.Schema, Created: sum.Created, Tables: tables}
	if err := enc.Encode(dumpRecord{Header: &head}); err != nil {
		return Summary{}, err
	}
	for _, t := range tables {
		n, err := writeTable(ctx, tx, enc, t)
		if err != nil {
			return Summary{}, fmt.Errorf("table %s: %w", t, err)
		}
		sum.Rows[t] = n
	}
	if err := enc.Encode(dumpRecord{End: &dumpEnd{Rows: sum.Rows}}); err != nil {
		return Summary{}, err
	}
	return sum, nil
}

func writeTable(ctx context.Context, tx *sql.Tx, enc *gob.Encoder, table string) (int64, error) {
	rows, err := tx.QueryContext(ctx, "SELECT * FROM "+quote(table))
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return 0, err
	}
	if err := enc.Encode(dumpRecord{Table: &dumpTable{Name: table, Columns: cols}}); err != nil {
		return 0, err
	}
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	var n int64
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return n, err
		}
		row := make([]cell, len(cols))
		for i, v := range vals {
			if row[i], err = toCell(v); err != nil {
				return n, fmt.Errorf("column %s: %w", cols[i], err)
			}
		}
		if err := enc.Encode(dumpRecord{Row: row}); err != nil {
			return n, err
		}
		n++
	}
	return n, rows.Err()
}

// Restore replaces the tables with a backup made by Dump, whatever the engine
// that made it, then applies the migrations made since. When the database is
// at the schema of the backup, it happens in one transaction. Otherwise the
// tables are dropped, made again at that schema, filled, then migrated; force
// also drops the tables this version does not know (ErrUnknownTables).
func (s *Store) Restore(ctx context.Context, r io.Reader, force bool) (Summary, error) {
	dec := gob.NewDecoder(r)
	var first dumpRecord
	if err := dec.Decode(&first); err != nil {
		return Summary{}, fmt.Errorf("not a DocIbis backup: %w", err)
	}
	if first.Header == nil {
		return Summary{}, errors.New("not a DocIbis backup")
	}
	h := first.Header
	if h.Format != dumpFormat {
		return Summary{}, fmt.Errorf("backup format %d is not read by this version", h.Format)
	}
	p, err := s.provider()
	if err != nil {
		return Summary{}, err
	}
	sources := p.ListSources()
	latest := sources[len(sources)-1].Version
	if h.Schema > latest {
		return Summary{}, fmt.Errorf("backup made by DocIbis %s (schema %d), newer than this version (schema %d): update DocIbis first", h.Version, h.Schema, latest)
	}
	current, err := p.GetDBVersion(ctx)
	if err != nil {
		return Summary{}, err
	}
	present, err := s.tables(ctx, s.DB.DB)
	if err != nil {
		return Summary{}, err
	}
	known, err := knownTables(ctx)
	if err != nil {
		return Summary{}, err
	}
	var unknown []string
	for _, t := range present {
		if !known[t] {
			unknown = append(unknown, t)
		}
	}
	inPlace := current == h.Schema && len(unknown) == 0
	if !inPlace {
		if len(unknown) > 0 && !force {
			return Summary{}, fmt.Errorf("%w: %s", ErrUnknownTables, strings.Join(unknown, ", "))
		}
		if err := s.dropTables(ctx, present); err != nil {
			return Summary{}, fmt.Errorf("drop the tables: %w", err)
		}
		// A new provider: the previous one knows its table existed.
		if p, err = s.provider(); err != nil {
			return Summary{}, err
		}
		if _, err := p.UpTo(ctx, h.Schema); err != nil {
			return Summary{}, fmt.Errorf("make the tables at schema %d: %w", h.Schema, err)
		}
		present = nil
	}
	sum, err := s.load(ctx, dec, h, present)
	if err != nil {
		return sum, err
	}
	if _, err := p.Up(ctx); err != nil {
		return sum, fmt.Errorf("apply migrations: %w", err)
	}
	return sum, s.Sync(ctx)
}

// load empties the tables in clear, then fills them from the stream, in one
// transaction.
func (s *Store) load(ctx context.Context, dec *gob.Decoder, h *dumpHeader, clear []string) (Summary, error) {
	sum := Summary{Format: h.Format, Version: h.Version, Engine: h.Engine, Schema: h.Schema, Created: h.Created, Rows: map[string]int64{}}
	tx, err := s.DB.DB.BeginTx(ctx, nil)
	if err != nil {
		return sum, err
	}
	defer tx.Rollback()
	order, err := s.parentsFirst(ctx, tx, clear)
	if err != nil {
		return sum, err
	}
	for _, t := range slices.Backward(order) {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+quote(t)); err != nil {
			return sum, fmt.Errorf("empty %s: %w", t, err)
		}
	}
	var (
		ins   *sql.Stmt
		kinds []colType
		table string
	)
	defer func() {
		if ins != nil {
			ins.Close()
		}
	}()
	for {
		var rec dumpRecord
		if err := dec.Decode(&rec); err != nil {
			if errors.Is(err, io.EOF) {
				return sum, errors.New("backup cut short")
			}
			return sum, err
		}
		switch {
		case rec.Table != nil:
			if ins != nil {
				ins.Close()
			}
			table = rec.Table.Name
			if ins, kinds, err = s.prepareInsert(ctx, tx, rec.Table); err != nil {
				return sum, fmt.Errorf("table %s: %w", table, err)
			}
			sum.Rows[table] = 0
		case rec.Row != nil:
			if ins == nil || len(rec.Row) != len(kinds) {
				return sum, errors.New("backup damaged: row outside its table")
			}
			args := make([]any, len(kinds))
			for i, c := range rec.Row {
				args[i] = s.value(c, kinds[i])
			}
			if _, err := ins.ExecContext(ctx, args...); err != nil {
				return sum, fmt.Errorf("table %s, row %d: %w", table, sum.Rows[table]+1, err)
			}
			sum.Rows[table]++
		case rec.End != nil:
			if !maps.Equal(rec.End.Rows, sum.Rows) {
				return sum, errors.New("backup damaged: rows missing")
			}
			if err := s.resetSequences(ctx, tx); err != nil {
				return sum, err
			}
			return sum, tx.Commit()
		}
	}
}

type colType int

const (
	colText colType = iota
	colInt
	colFloat
	colBool
	colTime
	colBytes
)

func typeOf(declared string) colType {
	t := strings.ToUpper(declared)
	switch {
	case strings.Contains(t, "BOOL"):
		return colBool
	case strings.Contains(t, "TIMESTAMP"), strings.Contains(t, "DATE"):
		return colTime
	case strings.Contains(t, "BYTEA"), strings.Contains(t, "BLOB"):
		return colBytes
	case strings.Contains(t, "INT"):
		return colInt
	case strings.Contains(t, "DOUBLE"), strings.Contains(t, "REAL"), strings.Contains(t, "FLOA"), strings.Contains(t, "NUMERIC"):
		return colFloat
	}
	return colText
}

func (s *Store) prepareInsert(ctx context.Context, tx *sql.Tx, t *dumpTable) (*sql.Stmt, []colType, error) {
	types, identity, err := s.columns(ctx, tx, t.Name)
	if err != nil {
		return nil, nil, err
	}
	if len(types) == 0 {
		return nil, nil, errors.New("absent from the database")
	}
	kinds := make([]colType, len(t.Columns))
	names := make([]string, len(t.Columns))
	marks := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		k, ok := types[c]
		if !ok {
			return nil, nil, fmt.Errorf("column %s absent from the database", c)
		}
		kinds[i], names[i], marks[i] = k, quote(c), "?"
		if s.engine == Postgres {
			marks[i] = "$" + strconv.Itoa(i+1)
		}
	}
	q := "INSERT INTO " + quote(t.Name) + " (" + strings.Join(names, ", ") + ")"
	if identity {
		q += " OVERRIDING SYSTEM VALUE"
	}
	stmt, err := tx.PrepareContext(ctx, q+" VALUES ("+strings.Join(marks, ", ")+")")
	return stmt, kinds, err
}

// columns gives the type of each column of a table, and whether one of them
// is an identity (PostgreSQL).
func (s *Store) columns(ctx context.Context, q querier, table string) (map[string]colType, bool, error) {
	query := `SELECT name, type, 'NO' FROM pragma_table_info(?)`
	if s.engine == Postgres {
		query = `SELECT column_name, data_type, is_identity FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = $1`
	}
	rows, err := q.QueryContext(ctx, query, table)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := map[string]colType{}
	identity := false
	for rows.Next() {
		var name, declared, ident string
		if err := rows.Scan(&name, &declared, &ident); err != nil {
			return nil, false, err
		}
		out[name] = typeOf(declared)
		identity = identity || ident == "YES"
	}
	return out, identity, rows.Err()
}

// sqliteTime is how bun writes dates in SQLite: kept, so that dates still
// compare as text.
const sqliteTime = "2006-01-02 15:04:05.999999-07:00"

// value converts a cell for a column of this engine.
func (s *Store) value(c cell, k colType) any {
	pg := s.engine == Postgres
	switch c.K {
	case cellBool, cellInt:
		switch {
		case k == colBool && pg:
			return c.I != 0
		case k == colFloat:
			return float64(c.I)
		case k == colText:
			return strconv.FormatInt(c.I, 10)
		}
		return c.I
	case cellFloat:
		if k == colInt && c.F == math.Trunc(c.F) {
			return int64(c.F)
		}
		return c.F
	case cellText:
		switch k {
		case colTime:
			if t := parseTime(c.S); t != nil {
				return s.timeValue(*t)
			}
		case colBytes:
			return []byte(c.S)
		case colBool:
			if pg {
				return c.S == "1" || strings.EqualFold(c.S, "true") || strings.EqualFold(c.S, "t")
			}
		case colInt:
			if n, err := strconv.ParseInt(c.S, 10, 64); err == nil {
				return n
			}
		}
		return c.S
	case cellBytes:
		if k == colText {
			return string(c.B)
		}
		if c.B == nil {
			return []byte{}
		}
		return c.B
	case cellTime:
		if k == colText {
			return c.T.UTC().Format(time.RFC3339Nano)
		}
		return s.timeValue(c.T)
	}
	return nil
}

func (s *Store) timeValue(t time.Time) any {
	if s.engine == SQLite {
		return t.UTC().Format(sqliteTime)
	}
	return t
}

// resetSequences moves the identity counters of PostgreSQL past the rows
// restored with their own numbers.
func (s *Store) resetSequences(ctx context.Context, tx *sql.Tx) error {
	if s.engine != Postgres {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT table_name, column_name FROM information_schema.columns WHERE table_schema = current_schema() AND is_identity = 'YES'`)
	if err != nil {
		return err
	}
	var pairs [][2]string
	for rows.Next() {
		var t, c string
		if err := rows.Scan(&t, &c); err != nil {
			rows.Close()
			return err
		}
		pairs = append(pairs, [2]string{t, c})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, p := range pairs {
		q := "SELECT setval(pg_get_serial_sequence($1, $2), m) FROM (SELECT MAX(" + quote(p[1]) + ") AS m FROM " + quote(p[0]) + ") x WHERE m IS NOT NULL"
		if _, err := tx.ExecContext(ctx, q, quote(p[0]), p[1]); err != nil {
			return fmt.Errorf("counter of %s: %w", p[0], err)
		}
	}
	return nil
}

func (s *Store) dropTables(ctx context.Context, tables []string) error {
	order, err := s.parentsFirst(ctx, s.DB.DB, tables)
	if err != nil {
		return err
	}
	tx, err := s.DB.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	cascade := ""
	if s.engine == Postgres {
		cascade = " CASCADE"
	}
	for _, t := range slices.Backward(append(order, "goose_db_version")) {
		if _, err := tx.ExecContext(ctx, "DROP TABLE IF EXISTS "+quote(t)+cascade); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// tables lists the tables of the database, without those of goose, SQLite
// and Litestream.
func (s *Store) tables(ctx context.Context, q querier) ([]string, error) {
	query := `SELECT name FROM sqlite_master WHERE type = 'table'`
	if s.engine == Postgres {
		query = `SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema() AND table_type = 'BASE TABLE'`
	}
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		if name != "goose_db_version" && !strings.HasPrefix(name, "sqlite_") && !strings.HasPrefix(name, "_litestream") {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out, rows.Err()
}

// parentsFirst orders tables so that a table comes after those its foreign
// keys point to.
func (s *Store) parentsFirst(ctx context.Context, q querier, tables []string) ([]string, error) {
	parents := map[string][]string{}
	if s.engine == Postgres {
		rows, err := q.QueryContext(ctx, `SELECT c.relname, p.relname FROM pg_constraint k
			JOIN pg_class c ON c.oid = k.conrelid
			JOIN pg_class p ON p.oid = k.confrelid
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE k.contype = 'f' AND n.nspname = current_schema()`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var child, parent string
			if err := rows.Scan(&child, &parent); err != nil {
				rows.Close()
				return nil, err
			}
			parents[child] = append(parents[child], parent)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	} else {
		for _, t := range tables {
			rows, err := q.QueryContext(ctx, `SELECT "table" FROM pragma_foreign_key_list(?)`, t)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var parent string
				if err := rows.Scan(&parent); err != nil {
					rows.Close()
					return nil, err
				}
				parents[t] = append(parents[t], parent)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return nil, err
			}
		}
	}
	in := map[string]bool{}
	for _, t := range tables {
		in[t] = true
	}
	done := map[string]bool{}
	out := make([]string, 0, len(tables))
	for len(out) < len(tables) {
		progress := false
		for _, t := range tables {
			if done[t] || slices.ContainsFunc(parents[t], func(p string) bool { return p != t && in[p] && !done[p] }) {
				continue
			}
			done[t] = true
			out = append(out, t)
			progress = true
		}
		if !progress {
			return nil, errors.New("foreign keys in a circle")
		}
	}
	return out, nil
}

var known struct {
	once   sync.Once
	tables map[string]bool
	err    error
}

// knownTables lists the tables of this version, from its SQLite migrations
// applied in memory (the PostgreSQL ones make the same tables).
func knownTables(ctx context.Context) (map[string]bool, error) {
	known.once.Do(func() {
		db, err := sql.Open("sqlite", ":memory:")
		if err != nil {
			known.err = err
			return
		}
		defer db.Close()
		db.SetMaxOpenConns(1)
		s := &Store{DB: bun.NewDB(db, sqlitedialect.New()), engine: SQLite}
		if known.err = s.migrate(ctx); known.err != nil {
			return
		}
		names, err := s.tables(ctx, db)
		if err != nil {
			known.err = err
			return
		}
		known.tables = map[string]bool{}
		for _, n := range names {
			known.tables[n] = true
		}
	})
	return known.tables, known.err
}

func quote(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
