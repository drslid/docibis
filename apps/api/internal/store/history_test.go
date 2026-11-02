package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestHistorySQLite(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "docibis.db")}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(ctx)
	checkHistory(t, s)
}

// checkHistory runs on every database engine: accounts, documents produced
// and audit journal, with their searches.
func checkHistory(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	for _, u := range []*User{
		{Username: "marie", DisplayName: "Marie Dupont", Email: "marie@example.fr", Role: "admin"},
		{Username: "paul", DisplayName: "Paul Martin", Role: "viewer"},
	} {
		if err := s.CreateAccount(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	if users, err := s.Users(ctx, "DUPONT", "", nil); err != nil || len(users) != 1 || users[0].Username != "marie" || !users[0].Active || users[0].Language != "fr" {
		t.Fatalf("search by name: %+v %v", users, err)
	}
	paul, _, _ := s.UserByUsername(ctx, "paul")
	paul.Active, paul.Role = false, "editor"
	if err := s.UpdateUser(ctx, &paul); err != nil {
		t.Fatal(err)
	}
	inactive := false
	if users, err := s.Users(ctx, "", "editor", &inactive); err != nil || len(users) != 1 || users[0].Username != "paul" {
		t.Fatalf("filter: %+v %v", users, err)
	}
	if err := s.CreateSession(ctx, "token", paul.ID, time.Now().Add(time.Hour), "Firefox"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.SessionUser(ctx, "token", 0); ok {
		t.Fatal("a disabled account must not keep its session")
	}
	if n, err := s.CountActiveAdmins(ctx); n != 1 || err != nil {
		t.Fatalf("admins: %d %v", n, err)
	}
	if err := s.RecordLogin(ctx, paul.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUser(ctx, paul.ID); err != nil {
		t.Fatal(err)
	}

	for _, r := range []*Render{
		{Source: "api", Actor: "billing-app", Template: "Mandat_SEPA.docx", Format: "pdf", Status: "ok", Size: 1000, DurationMs: 200, Engine: "standard"},
		{Source: "console", Actor: "marie", Template: "100%_tarif.docx", Format: "pdf", Status: "error", Error: "JSON invalide", Engine: "standard"},
	} {
		if err := s.RecordRender(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if rows, n, err := s.Renders(ctx, RenderFilter{Text: "mandat"}, Page{}); err != nil || n != 1 || rows[0].Actor != "billing-app" {
		t.Fatalf("search: %+v %d %v", rows, n, err)
	}
	if _, n, _ := s.Renders(ctx, RenderFilter{Text: "100%"}, Page{}); n != 1 {
		t.Fatalf("%% must be searched as text: %d", n)
	}
	if rows, n, err := s.Renders(ctx, RenderFilter{Status: "error", Source: "console"}, Page{Limit: 1}); err != nil || n != 1 || len(rows) != 1 {
		t.Fatalf("filter: %d %v", n, err)
	}
	if times, err := s.RenderTimes(ctx, time.Now().Add(-time.Minute)); err != nil || len(times) != 2 || times[0].DurationMs != 200 {
		t.Fatalf("times: %+v %v", times, err)
	}
	if n, err := s.PurgeRenders(ctx, time.Now().Add(time.Minute)); err != nil || n != 2 {
		t.Fatalf("purge: %d %v", n, err)
	}

	for _, e := range []*AuditEntry{
		{Actor: "marie", Action: "login"},
		{Actor: "marie", Action: "apikey.create", Target: "key:dib_aaaa", Details: "billing-app"},
		{Actor: "", Action: "login.failed", Target: "user:intrus"},
	} {
		if err := s.Audit(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	rows, n, actions, err := s.AuditEntries(ctx, AuditFilter{Text: "billing"}, Page{})
	if err != nil || n != 1 || rows[0].Action != "apikey.create" || len(actions) != 3 || actions[0] != "apikey.create" {
		t.Fatalf("audit: %+v %d %v %v", rows, n, actions, err)
	}
	if _, n, _, _ := s.AuditEntries(ctx, AuditFilter{Actor: "marie", Action: "login"}, Page{}); n != 1 {
		t.Fatalf("audit filter: %d", n)
	}
}
