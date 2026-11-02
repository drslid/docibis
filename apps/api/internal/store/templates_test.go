package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestTemplatesSQLite(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "docibis.db")}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(ctx)
	checkTemplates(t, s)
}

// checkTemplates runs on every database engine: the library, its versions,
// references and release notes.
func checkTemplates(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	tpl := &Template{Path: "acme/fiche.docx", Kind: "template", CreatedBy: "admin"}
	if err := s.AddTemplateVersion(ctx, tpl, &TemplateVersion{Content: []byte("v1"), Size: 2, SHA256: "a", CreatedBy: "admin"}, true); err != nil {
		t.Fatal(err)
	}
	if err := s.AddTemplateVersion(ctx, tpl, &TemplateVersion{Content: []byte("v2"), Size: 2, SHA256: "b", CreatedBy: "eve"}, false); err != nil {
		t.Fatal(err)
	}
	list, err := s.Templates(ctx, "FICHE")
	if err != nil || len(list) != 1 || list[0].Published != 1 || list[0].Versions != 2 || list[0].DataCheck != "warn" || list[0].HasReference {
		t.Fatalf("list: %+v %v", list, err)
	}
	pending, err := s.UndescribedVersions(ctx)
	if err != nil || len(pending) != 2 {
		t.Fatalf("pending: %+v %v", pending, err)
	}
	v1, ok, err := s.TemplateVersion(ctx, tpl.ID, 1)
	if err != nil || !ok || string(v1.Content) != "v1" || v1.HasReference {
		t.Fatalf("version 1: %+v %v %v", v1, ok, err)
	}
	if err := s.DescribeVersion(ctx, v1.ID, `[{"path":"nom"}]`, `{"nom":"x"}`, "overlay", `[]`, []byte("odt")); err != nil {
		t.Fatal(err)
	}
	if pending, _ := s.UndescribedVersions(ctx); len(pending) != 1 || pending[0].Number != 2 {
		t.Fatalf("pending after reading version 1: %+v", pending)
	}
	if served, ok, err := s.ServedVersion(ctx, tpl.ID, 1); err != nil || !ok || string(served.Prepared) != "odt" || string(served.Content) != "v1" {
		t.Fatalf("served version: %+v %v %v", served, ok, err)
	}
	if err := s.SetReference(ctx, v1.ID, []byte("%PDF-ref"), `{"nom":"Ref"}`); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCheckResult(ctx, v1.ID, `{"ok":true}`); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.TemplateByPath(ctx, "acme/fiche.docx")
	if err != nil || !ok || got.Engine != "overlay" || !got.HasReference || got.CheckResult != `{"ok":true}` {
		t.Fatalf("published version facts: %+v %v %v", got, ok, err)
	}
	refs, err := s.ReferenceVersions(ctx)
	if err != nil || len(refs) != 1 || refs[0].Number != 1 {
		t.Fatalf("references: %+v %v", refs, err)
	}
	versions, err := s.TemplateVersions(ctx, tpl.ID)
	if err != nil || len(versions) != 2 || versions[0].Number != 2 || versions[1].HasReference != true || versions[0].Content != nil {
		t.Fatalf("versions: %+v %v", versions, err)
	}
	if err := s.PublishTemplate(ctx, tpl.ID, 2); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := s.TemplateByID(ctx, tpl.ID); got.Published != 2 || got.HasReference || got.PreviousPublished != 1 {
		t.Fatalf("after publishing 2: %+v", got)
	}
	v2, _, _ := s.TemplateVersion(ctx, tpl.ID, 2)
	if err := s.DescribeVersion(ctx, v2.ID, `[]`, `{}`, "standard", `[{"level":"error","code":"not-closed","message":"x"}]`, nil); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := s.TemplateByID(ctx, tpl.ID); got.Lint == "" {
		t.Fatalf("lint of the newest version: %+v", got)
	}

	// A planned publication: not due yet, then due and done once.
	at := time.Now().Add(time.Hour)
	if err := s.ScheduleTemplate(ctx, tpl.ID, 1, &at, "eve"); err != nil {
		t.Fatal(err)
	}
	if due, err := s.DueSchedules(ctx, time.Now()); err != nil || len(due) != 0 {
		t.Fatalf("due too early: %+v %v", due, err)
	}
	due, err := s.DueSchedules(ctx, time.Now().Add(2*time.Hour))
	if err != nil || len(due) != 1 || due[0].ScheduledVersion != 1 || due[0].ScheduledBy != "eve" {
		t.Fatalf("due: %+v %v", due, err)
	}
	if done, err := s.PublishDue(ctx, tpl.ID, 1); err != nil || !done {
		t.Fatalf("publish due: %v %v", done, err)
	}
	if done, err := s.PublishDue(ctx, tpl.ID, 1); err != nil || done {
		t.Fatalf("published twice: %v %v", done, err)
	}
	if got, _, _ := s.TemplateByID(ctx, tpl.ID); got.Published != 1 || got.PreviousPublished != 2 || got.ScheduledVersion != 0 || got.ScheduledAt != nil {
		t.Fatalf("after the planned publication: %+v", got)
	}
	if err := s.ScheduleTemplate(ctx, tpl.ID, 2, &at, "eve"); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishTemplate(ctx, tpl.ID, 2); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := s.TemplateByID(ctx, tpl.ID); got.Published != 2 || got.ScheduledVersion != 0 {
		t.Fatalf("publishing the planned version by hand ends the plan: %+v", got)
	}
	checkUses(t, s, tpl.ID)
	got.Description, got.DataCheck = "Fiche conseil", "strict"
	if err := s.UpdateTemplate(ctx, &got); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := s.TemplateByID(ctx, tpl.ID); got.Description != "Fiche conseil" || got.DataCheck != "strict" {
		t.Fatalf("after update: %+v", got)
	}
	if err := s.DeleteTemplate(ctx, tpl.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.Templates(ctx, ""); len(list) != 0 {
		t.Fatalf("after delete: %+v", list)
	}

	r := &Release{Version: "1.4.0", Title: "Bibliothèque", Content: "- modèles", CreatedBy: "admin"}
	if err := s.SaveRelease(ctx, r); err != nil {
		t.Fatal(err)
	}
	r.Title = "Bibliothèque de modèles"
	if err := s.SaveRelease(ctx, r); err != nil {
		t.Fatal(err)
	}
	rows, total, err := s.Releases(ctx, Page{Limit: 10})
	if err != nil || total != 1 || rows[0].Title != "Bibliothèque de modèles" {
		t.Fatalf("releases: %+v %d %v", rows, total, err)
	}
	if err := s.DeleteRelease(ctx, r.ID); err != nil {
		t.Fatal(err)
	}

	k := &APIKey{Name: "app", Prefix: "dib_rate0001", Hash: "h-rate", Scopes: "render", CreatedBy: "admin", CreatedAt: r.CreatedAt}
	if err := s.CreateAPIKey(ctx, k); err != nil {
		t.Fatal(err)
	}
	k.RatePerMinute = 120
	if err := s.UpdateAPIKey(ctx, k); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := s.APIKeyByID(ctx, k.ID); err != nil || !ok || got.RatePerMinute != 120 {
		t.Fatalf("key rate: %+v %v %v", got, ok, err)
	}
	checkBundles(t, s)
}

// checkUses counts the documents of a template.
func checkUses(t *testing.T, s *Store, id int64) {
	t.Helper()
	ctx := context.Background()
	for i, status := range []string{"ok", "ok", "error", "refused"} {
		if err := s.RecordRender(ctx, &Render{Source: "api", Actor: "crm", Template: "fiche.docx", Format: "pdf", Status: status,
			DurationMs: 100 * (i + 1), TemplateID: id, TemplateVersion: 2}); err != nil {
			t.Fatal(err)
		}
	}
	uses, err := s.TemplateUses(ctx, time.Now().Add(-time.Hour))
	if err != nil || uses[id].Count != 4 || uses[id].Errors != 1 || uses[id].Refused != 1 || uses[id].LastUsedAt == nil {
		t.Fatalf("uses: %+v %v", uses, err)
	}
	if time.Since(*uses[id].LastUsedAt) > time.Minute || time.Since(*uses[id].LastUsedAt) < -time.Minute {
		t.Fatalf("last use read wrong: %v", uses[id].LastUsedAt)
	}
	renders, err := s.TemplateRenders(ctx, id, time.Now().Add(-time.Hour))
	if err != nil || len(renders) != 4 || renders[0].DurationMs != 100 || renders[0].TemplateVersion != 2 {
		t.Fatalf("renders: %+v %v", renders, err)
	}
}

// checkBundles saves, finds and removes bundles and promotions.
func checkBundles(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	b := &Bundle{Name: "souscription", Items: `[{"template":"a.docx"}]`, Output: "pdf", UpdatedBy: "eve"}
	if err := s.SaveBundle(ctx, b); err != nil || b.ID == 0 || b.CreatedBy != "eve" {
		t.Fatalf("create bundle: %+v %v", b, err)
	}
	b.Name, b.Output = "souscription-habitation", "zip"
	if err := s.SaveBundle(ctx, b); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := s.BundleByName(ctx, "souscription-habitation"); err != nil || !ok || got.Output != "zip" || got.ID != b.ID {
		t.Fatalf("bundle by name: %+v %v %v", got, ok, err)
	}
	if list, err := s.Bundles(ctx); err != nil || len(list) != 1 {
		t.Fatalf("bundles: %+v %v", list, err)
	}
	if ok, err := s.DeleteBundle(ctx, b.ID); err != nil || !ok {
		t.Fatalf("delete bundle: %v %v", ok, err)
	}
	if _, ok, _ := s.BundleByID(ctx, b.ID); ok {
		t.Fatal("bundle still there")
	}
	for _, status := range []string{"error", "ok"} {
		if err := s.RecordPromotion(ctx, &Promotion{TemplateID: 7, Version: 3, Environment: "REC", Status: status, Actor: "eve"}); err != nil {
			t.Fatal(err)
		}
	}
	if list, err := s.Promotions(ctx, 7); err != nil || len(list) != 2 || list[0].Status != "ok" {
		t.Fatalf("promotions: %+v %v", list, err)
	}
}
