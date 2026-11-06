package backup

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"docibis/internal/store"
)

// Kinds of backups.
const (
	Nightly       = "nightly"
	Manual        = "manual"
	Update        = "update"
	BeforeRestore = "restore"
)

// ErrNoKey means APP_SECRET_KEY is missing: backups are always encrypted.
var ErrNoKey = errors.New("APP_SECRET_KEY is not set (32 characters at least): backups are encrypted with it")

// Secret is the key material of the backups: APP_SECRET_KEY, nil without it.
func Secret() []byte {
	k := os.Getenv("APP_SECRET_KEY")
	if len(k) < 32 {
		return nil
	}
	return []byte(k)
}

// Info describes a backup; it is also written next to it, as name.json.
type Info struct {
	Name     string         `json:"name"`
	Kind     string         `json:"kind"`
	Created  time.Time      `json:"created"`
	Size     int64          `json:"size"`
	Instance string         `json:"instance,omitempty"`
	Summary  *store.Summary `json:"summary,omitempty"`
	// Verified: read back into a new database, every row found.
	Verified bool   `json:"verified"`
	Problem  string `json:"problem,omitempty"`
}

var namePattern = regexp.MustCompile(`^docibis-(\d{8}-\d{6})-([a-z]+)\.backup$`)

// Create backs up the database to loc. The file is made in tmpDir, read back
// into a new SQLite database to check it, then sent with its note.
func Create(ctx context.Context, st *store.Store, loc Location, secret []byte, kind, instance, tmpDir string) (Info, error) {
	if len(secret) == 0 {
		return Info{}, ErrNoKey
	}
	now := time.Now().UTC()
	info := Info{Name: "docibis-" + now.Format("20060102-150405") + "-" + kind + ".backup", Kind: kind, Created: now, Instance: instance}
	work, err := os.MkdirTemp(tmpDir, ".backup-")
	if err != nil {
		return info, err
	}
	defer os.RemoveAll(work)
	file := filepath.Join(work, info.Name)
	sum, err := write(ctx, st, file, secret)
	if err != nil {
		return info, err
	}
	info.Summary = &sum
	if err := verify(ctx, file, secret, sum, work); err != nil {
		info.Problem = "La relecture de la sauvegarde a échoué : " + err.Error()
	} else {
		info.Verified = true
	}
	f, err := os.Open(file)
	if err != nil {
		return info, err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return info, err
	}
	info.Size = stat.Size()
	if err := loc.Put(ctx, info.Name, f, info.Size); err != nil {
		return info, err
	}
	note, _ := json.Marshal(info)
	return info, loc.Put(ctx, info.Name+".json", bytes.NewReader(note), int64(len(note)))
}

func write(ctx context.Context, st *store.Store, file string, secret []byte) (store.Summary, error) {
	f, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return store.Summary{}, err
	}
	defer f.Close()
	sealed, err := Seal(f, secret)
	if err != nil {
		return store.Summary{}, err
	}
	zw, err := gzip.NewWriterLevel(sealed, gzip.BestSpeed)
	if err != nil {
		return store.Summary{}, err
	}
	sum, err := st.Dump(ctx, zw)
	if err != nil {
		return sum, err
	}
	if err := zw.Close(); err != nil {
		return sum, err
	}
	if err := sealed.Close(); err != nil {
		return sum, err
	}
	if err := f.Sync(); err != nil {
		return sum, err
	}
	return sum, f.Close()
}

// verify restores the file into a new SQLite database and counts its rows.
func verify(ctx context.Context, file string, secret []byte, want store.Summary, work string) error {
	db, err := store.Open(ctx, store.Options{Path: filepath.Join(work, "check.db")}, slog.New(slog.DiscardHandler))
	if err != nil {
		return err
	}
	defer db.Close(context.WithoutCancel(ctx))
	got, err := Restore(ctx, db, file, secret, false)
	if err != nil {
		return err
	}
	if !maps.Equal(got.Rows, want.Rows) {
		return errors.New("rows differ")
	}
	return nil
}

// Restore replaces the database with a backup file (see store.Restore).
func Restore(ctx context.Context, st *store.Store, file string, secret []byte, force bool) (store.Summary, error) {
	f, err := os.Open(file)
	if err != nil {
		return store.Summary{}, err
	}
	defer f.Close()
	plain, err := Open(f, secret)
	if err != nil {
		return store.Summary{}, err
	}
	zr, err := gzip.NewReader(plain)
	if err != nil {
		return store.Summary{}, err
	}
	defer zr.Close()
	return st.Restore(ctx, zr, force)
}

// List returns the backups of a location, newest first. Only the name is
// read: Describe adds the note.
func List(ctx context.Context, loc Location) ([]Info, error) {
	items, err := loc.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []Info
	for _, it := range items {
		m := namePattern.FindStringSubmatch(it.Name)
		if m == nil {
			continue
		}
		created, _ := time.Parse("20060102-150405", m[1])
		out = append(out, Info{Name: it.Name, Kind: m[2], Created: created, Size: it.Size})
	}
	slices.SortFunc(out, func(a, b Info) int { return strings.Compare(b.Name, a.Name) })
	return out, nil
}

// Describe adds what the note next to a backup says.
func Describe(ctx context.Context, loc Location, info Info) Info {
	r, err := loc.Get(ctx, info.Name+".json")
	if err != nil {
		return info
	}
	defer r.Close()
	var note Info
	if json.NewDecoder(io.LimitReader(r, 1<<20)).Decode(&note) != nil || note.Name != info.Name {
		return info
	}
	note.Size = info.Size
	return note
}

// Find picks a backup by its name, or "latest", or a day (2026-11-06) for the
// newest of that day; the copies made before a restore only by their name.
func Find(all []Info, what string) (Info, bool) {
	day := strings.ReplaceAll(what, "-", "")
	for _, b := range all {
		switch {
		case b.Name == what || b.Name == what+".backup":
			return b, true
		case b.Kind == BeforeRestore:
		case what == "latest", len(day) == 8 && b.Created.Format("20060102") == day:
			return b, true
		}
	}
	return Info{}, false
}

// Prune deletes the backups older than days, always keeping the three newest.
func Prune(ctx context.Context, loc Location, days int, now time.Time) ([]string, error) {
	all, err := List(ctx, loc)
	if err != nil {
		return nil, err
	}
	limit := now.AddDate(0, 0, -days)
	var gone []string
	for i, b := range all {
		if i < 3 || !b.Created.Before(limit) {
			continue
		}
		if err := loc.Delete(ctx, b.Name); err != nil {
			return gone, err
		}
		if err := loc.Delete(ctx, b.Name+".json"); err != nil {
			return gone, err
		}
		gone = append(gone, b.Name)
	}
	return gone, nil
}

// Fetch copies a backup into dir and returns the path of the copy.
func Fetch(ctx context.Context, loc Location, name, dir string) (string, error) {
	r, err := loc.Get(ctx, name)
	if err != nil {
		return "", err
	}
	defer r.Close()
	p := filepath.Join(dir, filepath.Base(name))
	f, err := os.OpenFile(p, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return "", fmt.Errorf("download %s: %w", name, err)
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return p, nil
}
