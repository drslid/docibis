package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestAliasesSQLite(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "docibis.db")}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(ctx)
	checkAliases(t, s)
}

// checkAliases renames and merges templates: the former names keep finding
// them, and a merge keeps the versions the template did not have.
func checkAliases(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	add := func(path, content string) Template {
		t.Helper()
		tpl := Template{Path: path, Kind: "template", CreatedBy: "admin"}
		v := TemplateVersion{Content: []byte(content), Size: int64(len(content)), SHA256: "sha-" + content, CreatedBy: "admin"}
		if err := s.AddTemplateVersion(ctx, &tpl, &v, true); err != nil {
			t.Fatal(err)
		}
		return tpl
	}
	find := func(name string) int64 {
		t.Helper()
		got, ok, err := s.TemplateByName(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			return 0
		}
		return got.ID
	}
	names := func(id int64) []string {
		t.Helper()
		aliases, err := s.TemplateAliases(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, a := range aliases {
			out = append(out, a.Alias)
		}
		return out
	}

	direct := add("aliases/acme/default/monthly/Fiche_conseil.docx", "fiche")
	broker := add("aliases/acme/broker/monthly/Fiche_conseil.docx", "fiche")
	old := add("aliases/acme/tarif_v1.docx", "tarif-2025")
	current := add("aliases/acme/tarif.docx", "tarif-2026")

	if err := s.RenameTemplate(ctx, direct.ID, "habitation/fiche-conseil.docx", "admin"); err != nil {
		t.Fatal(err)
	}
	if find("habitation/fiche-conseil.docx") != direct.ID || find(direct.Path) != direct.ID {
		t.Fatal("renamed template not found by its new and former names")
	}
	if _, ok, _ := s.TemplateByPath(ctx, direct.Path); ok {
		t.Fatal("the former name is still a path")
	}
	if got := names(direct.ID); len(got) != 1 || got[0] != direct.Path {
		t.Fatalf("aliases %v", got)
	}
	// Back to the former name, then to the new one again.
	if err := s.RenameTemplate(ctx, direct.ID, direct.Path, "admin"); err != nil {
		t.Fatal(err)
	}
	if got := names(direct.ID); len(got) != 1 || got[0] != "habitation/fiche-conseil.docx" {
		t.Fatalf("aliases after going back %v", got)
	}
	if err := s.RenameTemplate(ctx, direct.ID, "habitation/fiche-conseil.docx", "admin"); err != nil {
		t.Fatal(err)
	}
	for _, taken := range []string{"habitation/fiche-conseil.docx", direct.Path, current.Path} {
		if err := s.RenameTemplate(ctx, broker.ID, taken, "admin"); !errors.Is(err, ErrNameTaken) {
			t.Fatalf("rename to %s: %v", taken, err)
		}
	}
	if owner, _ := s.NameOwner(ctx, direct.Path); owner != direct.ID {
		t.Fatalf("owner of the former name: %d", owner)
	}

	// The same file under another name: no version added.
	if n, err := s.MergeTemplate(ctx, broker.ID, direct.ID, "admin"); err != nil || n != 0 {
		t.Fatalf("merge identical: %d %v", n, err)
	}
	if find(broker.Path) != direct.ID {
		t.Fatal("merged template not found by its name")
	}
	if _, ok, _ := s.TemplateByID(ctx, broker.ID); ok {
		t.Fatal("merged template still there")
	}
	if got := names(direct.ID); len(got) != 2 {
		t.Fatalf("aliases after merge %v", got)
	}
	if found, err := s.Templates(ctx, "broker/monthly"); err != nil || len(found) != 1 || found[0].ID != direct.ID {
		t.Fatalf("search by former name: %+v %v", found, err)
	}

	// An older copy and the current one: one template, two versions in order.
	if err := s.RenameTemplate(ctx, old.ID, "habitation/proposition-tarifaire.docx", "admin"); err != nil {
		t.Fatal(err)
	}
	if n, err := s.MergeTemplate(ctx, current.ID, old.ID, "admin"); err != nil || n != 1 {
		t.Fatalf("merge newer copy: %d %v", n, err)
	}
	merged, _, _ := s.TemplateByID(ctx, old.ID)
	newest, ok, err := s.TemplateVersion(ctx, old.ID, 2)
	if err != nil || !ok || string(newest.Content) != "tarif-2026" || merged.Versions != 2 || merged.Published != 1 {
		t.Fatalf("merged versions: %+v %+v %v", merged, newest, err)
	}
	if find(current.Path) != old.ID || find(old.Path) != old.ID {
		t.Fatal("former names of the merged template")
	}

	if ok, err := s.DeleteTemplateAlias(ctx, direct.ID, broker.Path); err != nil || !ok {
		t.Fatalf("remove alias: %v %v", ok, err)
	}
	if ok, _ := s.DeleteTemplateAlias(ctx, direct.ID, broker.Path); ok {
		t.Fatal("alias removed twice")
	}
	if find(broker.Path) != 0 {
		t.Fatal("removed alias still found")
	}
	if err := s.DeleteTemplate(ctx, direct.ID); err != nil {
		t.Fatal(err)
	}
	if owner, _ := s.NameOwner(ctx, direct.Path); owner != 0 {
		t.Fatal("aliases of a deleted template remain")
	}
	if err := s.DeleteTemplate(ctx, old.ID); err != nil {
		t.Fatal(err)
	}
}
