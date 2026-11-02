package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestAPIKeysSQLite(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "docibis.db")}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(ctx)
	checkAPIKeys(t, s)
}

// checkAPIKeys runs on every database engine.
func checkAPIKeys(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	past, future := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	keys := []*APIKey{
		{Name: "billing-app", Prefix: "dib_aaaa", Hash: "h1", Scopes: "render", CreatedBy: "admin", CreatedAt: time.Now().UTC(), ExpiresAt: &future},
		{Name: "expired", Prefix: "dib_bbbb", Hash: "h2", Scopes: "render", CreatedBy: "admin", CreatedAt: time.Now().UTC(), ExpiresAt: &past},
		{Name: "forever", Prefix: "dib_cccc", Hash: "h3", Scopes: "render", CreatedBy: "admin", CreatedAt: time.Now().UTC()},
	}
	for _, k := range keys {
		if err := s.CreateAPIKey(ctx, k); err != nil {
			t.Fatal(err)
		}
	}
	for hash, want := range map[string]bool{"h1": true, "h2": false, "h3": true, "nope": false} {
		if _, ok, err := s.ActiveAPIKey(ctx, hash); ok != want || err != nil {
			t.Fatalf("%s: %v %v", hash, ok, err)
		}
	}
	if err := s.TouchAPIKey(ctx, keys[0].ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.RevokeAPIKey(ctx, keys[0].ID); !ok || err != nil {
		t.Fatalf("revoke: %v %v", ok, err)
	}
	if ok, err := s.RevokeAPIKey(ctx, keys[0].ID); !ok || err != nil {
		t.Fatalf("revoke twice: %v %v", ok, err)
	}
	if ok, err := s.RevokeAPIKey(ctx, 999); ok || err != nil {
		t.Fatalf("revoke a missing key: %v %v", ok, err)
	}
	if _, ok, _ := s.ActiveAPIKey(ctx, "h1"); ok {
		t.Fatal("a revoked key must be refused")
	}
	list, err := s.APIKeys(ctx)
	if err != nil || len(list) != 3 || list[0].Name != "forever" || list[2].LastUsedAt == nil || list[2].RevokedAt == nil {
		t.Fatalf("list: %+v %v", list, err)
	}
}
