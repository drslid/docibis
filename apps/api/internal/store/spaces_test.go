package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestSpacesSQLite(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "docibis.db")}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(ctx)
	checkSpaces(t, s)
}

// checkSpaces saves a space, counts the documents of its keys and keeps a
// space that a key uses.
func checkSpaces(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	sp := Space{Name: "Habitation", Prefixes: "habitation/", DailyQuota: 10, CreatedBy: "admin"}
	if err := s.SaveSpace(ctx, &sp); err != nil || sp.ID == 0 {
		t.Fatalf("save: %v %+v", err, sp)
	}
	sp.MonthlyQuota = 100
	if err := s.SaveSpace(ctx, &sp); err != nil {
		t.Fatal(err)
	}
	k := APIKey{Name: "app", Prefix: "dib_spacetst", Hash: "space-hash", Scopes: "render", CreatedBy: "admin", CreatedAt: time.Now(), SpaceID: sp.ID}
	if err := s.CreateAPIKey(ctx, &k); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	for range 3 {
		if err := s.AddUsage(ctx, at, "key:1", "space:1"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddUsage(ctx, at.AddDate(0, 0, 1), "key:1"); err != nil {
		t.Fatal(err)
	}
	day, month, err := s.Usage(ctx, at, "key:1", "space:1", "key:2")
	if err != nil || day["key:1"] != 3 || day["space:1"] != 3 || month["key:1"] != 4 || month["space:1"] != 3 || day["key:2"] != 0 {
		t.Fatalf("usage: %v %v %v", day, month, err)
	}
	if err := s.PurgeUsage(ctx, at.AddDate(0, 1, 0)); err != nil {
		t.Fatal(err)
	}
	if day, month, _ := s.Usage(ctx, at, "key:1"); day["key:1"] != 0 || month["key:1"] != 0 {
		t.Fatalf("purged: %v %v", day, month)
	}
	if _, err := s.DeleteSpace(ctx, sp.ID); !errors.Is(err, ErrSpaceUsed) {
		t.Fatalf("delete a space in use: %v", err)
	}
	if _, err := s.RevokeAPIKey(ctx, k.ID); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.DeleteSpace(ctx, sp.ID); err != nil || !ok {
		t.Fatalf("delete: %v %v", ok, err)
	}
	if spaces, err := s.Spaces(ctx); err != nil || len(spaces) != 0 {
		t.Fatalf("spaces: %v %v", spaces, err)
	}
}
