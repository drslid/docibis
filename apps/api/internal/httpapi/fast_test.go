package httpapi

import (
	"encoding/json"
	"testing"

	"docibis/internal/store"
)

func TestQualificationData(t *testing.T) {
	v := store.TemplateVersion{
		Sample:        `{"nom":"Exemple nom","ok":true,"date":"2026-01-01","lignes":[{"coche":false}]}`,
		ReferenceData: `{"nom":"Dupont","ok":false}`,
	}
	got := qualificationData(v)
	if len(got) != 5 {
		t.Fatalf("%d data sets, want sample, reference, short, short flipped and accented", len(got))
	}
	var short, flipped, accented map[string]any
	for i, into := range []*map[string]any{&short, &flipped, &accented} {
		if err := json.Unmarshal(got[i+2], into); err != nil {
			t.Fatal(err)
		}
	}
	if short["nom"] != "Éa 12" || short["ok"] != true || short["date"] != "2026-01-01" {
		t.Errorf("short = %v", short)
	}
	if flipped["ok"] != false || flipped["lignes"].([]any)[0].(map[string]any)["coche"] != true || flipped["nom"] != "Éa 12" {
		t.Errorf("flipped = %v", flipped)
	}
	if accented["nom"] == "Exemple nom" || accented["date"] != "2026-01-01" || accented["ok"] != true {
		t.Errorf("accented = %v", accented)
	}
	if n := len(qualificationData(store.TemplateVersion{Sample: `{"nom":"Dupont"}`})); n != 1 {
		t.Errorf("without yes/no nor sample text: %d data sets, want 1", n)
	}
}

func TestSetSample(t *testing.T) {
	root := map[string]any{"client": map[string]any{"nom": "Exemple nom", "cp": "75002"}, "date": "2026-01-01"}
	if !setSample(root, []string{"client", "nom"}, "Éa") || root["client"].(map[string]any)["nom"] != "Éa" {
		t.Fatalf("sample text not replaced: %v", root)
	}
	for _, path := range [][]string{{"client", "cp"}, {"date"}, {"absent", "x"}, {"client", "nom", "x"}} {
		if setSample(root, path, "Éa") {
			t.Errorf("%v replaced", path)
		}
	}
	if !plainTag.MatchString("client.adresse.codePostal") || plainTag.MatchString("{dateFormat(d, 'dd/MM')}") || plainTag.MatchString("x{renderer=boolean}") {
		t.Error("plain tags misread")
	}
}

func TestFastStatusRoundTrip(t *testing.T) {
	if fastStatus("") != nil || fastStatus("{") != nil {
		t.Fatal("no status expected")
	}
	raw, _ := json.Marshal(FastStatus{State: FastStatusStateQualified, Permille: ptr(float32(0.04)), Tests: ptr(3)})
	s := fastStatus(string(raw))
	if s == nil || s.State != FastStatusStateQualified || *s.Tests != 3 {
		t.Fatalf("status = %+v", s)
	}
}

// The overlay only serves plain PDFs of a qualified version.
func TestRenderFastDeclines(t *testing.T) {
	s := &Server{}
	qualified, _ := json.Marshal(FastStatus{State: FastStatusStateQualified})
	for name, f := range map[string]tryForm{
		"not in the library": {fast: string(qualified)},
		"not qualified":      {libVersionID: 1, fast: `{"state":"refused"}`},
		"PDF/A":              {libVersionID: 1, fast: string(qualified), opts: renderOptions{pdfa: "PDF/A-2b"}},
		"watermark":          {libVersionID: 1, fast: string(qualified), opts: renderOptions{watermark: watermark{text: "COPIE"}}},
	} {
		doc := document{format: "pdf"}
		if s.renderFast(t.Context(), f, &doc) || doc.engine != "" {
			t.Errorf("%s: the overlay served the document", name)
		}
	}
	if s.renderFast(t.Context(), tryForm{libVersionID: 1, fast: string(qualified)}, &document{format: "docx"}) {
		t.Error("the overlay served a Word document")
	}
}
