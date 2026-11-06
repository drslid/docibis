package backup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"docibis/internal/store"
)

var secret = []byte("0123456789abcdef0123456789abcdef")

func seal(t *testing.T, plain []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := Seal(&buf, secret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(plain); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func open(raw []byte, key []byte) ([]byte, error) {
	r, err := Open(bytes.NewReader(raw), key)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

func TestSealOpen(t *testing.T) {
	for _, n := range []int{0, 1, chunkSize - 1, chunkSize, chunkSize + 1, 3*chunkSize + 7} {
		plain := bytes.Repeat([]byte{byte(n)}, n)
		raw := seal(t, plain)
		got, err := open(raw, secret)
		if err != nil || !bytes.Equal(got, plain) {
			t.Fatalf("%d bytes: %v", n, err)
		}
		if _, err := open(raw, []byte("another key of thirty-two chars!")); !errors.Is(err, ErrKey) {
			t.Fatalf("%d bytes, other key: %v", n, err)
		}
	}
	raw := seal(t, bytes.Repeat([]byte("x"), 2*chunkSize+10))
	cut := raw[:len(magic)+saltSize+chunkSize+16]
	if _, err := open(cut, secret); !errors.Is(err, ErrKey) {
		t.Fatalf("cut after a chunk: %v", err)
	}
	if _, err := open(raw[:len(raw)-1], secret); !errors.Is(err, ErrKey) {
		t.Fatalf("last byte missing: %v", err)
	}
	changed := bytes.Clone(raw)
	changed[len(magic)+saltSize+100] ^= 1
	if _, err := open(changed, secret); !errors.Is(err, ErrKey) {
		t.Fatalf("modified: %v", err)
	}
	if _, err := open([]byte("PK\x03\x04 not a backup at all, really not"), secret); err == nil {
		t.Fatal("not a backup opened")
	}
}

func TestCreateListRestore(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, store.Options{Path: filepath.Join(t.TempDir(), "docibis.db")}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close(ctx)
	if err := st.SetSetting(ctx, "instance_name", "INT"); err != nil {
		t.Fatal(err)
	}
	loc := Dir(filepath.Join(t.TempDir(), "backups"))
	if _, err := Create(ctx, st, loc, nil, Manual, "INT", t.TempDir()); !errors.Is(err, ErrNoKey) {
		t.Fatalf("backup without a key: %v", err)
	}
	info, err := Create(ctx, st, loc, secret, Manual, "INT", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !info.Verified || info.Problem != "" || info.Size == 0 || info.Summary.Rows["settings"] != 1 {
		t.Fatalf("backup: %+v", info)
	}
	all, err := List(ctx, loc)
	if err != nil || len(all) != 1 || all[0].Name != info.Name || all[0].Kind != Manual {
		t.Fatalf("list: %+v %v", all, err)
	}
	if d := Describe(ctx, loc, all[0]); !d.Verified || d.Instance != "INT" || d.Summary == nil {
		t.Fatalf("note: %+v", d)
	}
	for _, what := range []string{info.Name, "latest", info.Created.Format(time.DateOnly), info.Created.Format("20060102")} {
		if b, ok := Find(all, what); !ok || b.Name != info.Name {
			t.Fatalf("find %q: %v", what, ok)
		}
	}
	if _, ok := Find(all, "2001-01-01"); ok {
		t.Fatal("found a backup on another day")
	}

	if err := st.SetSetting(ctx, "instance_name", "REC"); err != nil {
		t.Fatal(err)
	}
	file, err := Fetch(ctx, loc, info.Name, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(ctx, st, file, []byte("another key of thirty-two chars!"), false); !errors.Is(err, ErrKey) {
		t.Fatalf("restore with another key: %v", err)
	}
	if _, err := Restore(ctx, st, file, secret, false); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := st.Setting(ctx, "instance_name"); v != "INT" {
		t.Fatalf("after restore: %q", v)
	}
	if _, err := Fetch(ctx, loc, "docibis-20010101-000000-manual.backup", t.TempDir()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing backup: %v", err)
	}
}

func TestPrune(t *testing.T) {
	ctx := context.Background()
	loc := Dir(t.TempDir())
	now := time.Date(2026, 11, 6, 2, 0, 0, 0, time.UTC)
	for _, day := range []int{40, 35, 31, 29, 1} {
		name := "docibis-" + now.AddDate(0, 0, -day).Format("20060102-150405") + "-nightly.backup"
		for _, n := range []string{name, name + ".json"} {
			if err := loc.Put(ctx, n, bytes.NewReader([]byte("x")), 1); err != nil {
				t.Fatal(err)
			}
		}
	}
	gone, err := Prune(ctx, loc, 30, now)
	if err != nil || len(gone) != 2 {
		t.Fatalf("pruned: %v %v", gone, err)
	}
	left, _ := List(ctx, loc)
	if len(left) != 3 {
		t.Fatalf("left: %+v", left)
	}
	// The three newest stay, however old.
	if gone, _ := Prune(ctx, loc, 0, now.AddDate(1, 0, 0)); len(gone) != 0 {
		t.Fatalf("newest pruned: %v", gone)
	}
	entries, _ := os.ReadDir(string(loc))
	if len(entries) != 6 {
		t.Fatalf("files left: %d", len(entries))
	}
}
