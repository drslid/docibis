package merge

import (
	"archive/zip"
	"bytes"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

func part(t *testing.T, odf []byte, name string) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(odf), int64(len(odf)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if f.Name == name {
			b, err := read(f)
			if err != nil {
				t.Fatal(err)
			}
			return string(b)
		}
	}
	t.Fatalf("no %s", name)
	return ""
}

// cells lists the value type, value and formula of each cell of a sheet row
// that holds something.
var cellAttrs = regexp.MustCompile(`<table:table-cell\b[^>]*>`)

func TestSpreadsheet(t *testing.T) {
	ods, err := os.ReadFile("testdata/facture.ods")
	if err != nil {
		t.Fatal(err)
	}
	if Family(ods) != "spreadsheet" {
		t.Fatalf("family %q", Family(ods))
	}
	a, err := Analyze(ods)
	if err != nil || a.Engine != "standard" || a.Repeats != 1 {
		t.Fatalf("analysis: %+v %v", a, err)
	}
	data := `{"numero": "F-12", "client": {"nom": "Durand"}, "date": "2026-11-06",
		"lignes": [{"designation": "Pose", "quantite": 2, "prix": 10.5}, {"designation": "Câble", "quantite": 1, "prix": 3}, {"designation": "Prise", "quantite": 4, "prix": 2.25}]}`
	out, err := Fill(ods, []byte(data), Options{Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	content := part(t, out, "content.xml")
	for _, want := range []string{
		"Facture F-12", "Durand", "Émise le 06/11/2026",
		`office:value-type="float" calcext:value-type="float" office:value="2"`,
		`office:value="10.5"`, `office:value="2.25"`,
		`table:formula="of:=[.B5]*[.C5]"`, `table:formula="of:=[.B6]*[.C6]"`, `table:formula="of:=[.B7]*[.C7]"`,
		`table:formula="of:=SUM([.D5:.D7])"`,
		`draw:notify-on-update-of-ranges="Facture.A5:Facture.A7 Facture.D4:Facture.D4 Facture.D5:Facture.D7"`,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("content.xml lacks %s", want)
		}
	}
	if strings.Contains(content, "docibis:") || strings.Contains(content, "&lt;&lt;") {
		t.Error("marks or tags left")
	}
	if !strings.Contains(content, `<text:p>Pose</text:p>`) || strings.Count(content, `office:value-type="string" calcext:value-type="string"><text:p>Pose`) != 1 {
		t.Error("a text field must stay text")
	}
	chart := part(t, out, "Object 1/content.xml")
	for _, want := range []string{`table:cell-range-address="Facture.A5:Facture.A7"`, `chart:values-cell-range-address="Facture.D5:Facture.D7"`, `chart:label-cell-address="Facture.D4:Facture.D4"`} {
		if !strings.Contains(chart, want) {
			t.Errorf("chart lacks %s", want)
		}
	}

	// No line: the total reads an empty cell, the chart an empty range.
	out, err = Fill(ods, []byte(`{"numero": "F-13", "lignes": []}`), Options{Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	content = part(t, out, "content.xml")
	if !strings.Contains(content, `table:formula="of:=SUM([.D1048576:.D1048576])"`) || strings.Contains(content, "[.B5]*") {
		t.Errorf("empty list: %s", cellAttrs.FindAllString(content, -1))
	}
	if chart := part(t, out, "Object 1/content.xml"); !strings.Contains(chart, `chart:values-cell-range-address="Facture.D1048576:Facture.D1048576"`) {
		t.Error("empty list: chart range")
	}
}

func TestMoveAddresses(t *testing.T) {
	s := &sheetRows{starts: []int{1, 2, 3, 4}, counts: map[int]int{1: 1, 2: 1, 3: 1, 4: 10}, copies: map[int][]int{1: {0}, 2: {1, 2}, 3: {3}, 4: {4}}, runs: []rowRun{
		{orig: 1, count: 1, at: 1}, {orig: 2, count: 1, at: 2, item: "1"}, {orig: 2, count: 1, at: 3, item: "2"}, {orig: 3, count: 1, at: 4}, {orig: 4, count: 10, at: 5},
	}}
	maps := map[string]*sheetRows{"Ma feuille": s}
	for _, c := range []struct{ in, sheet, item, want string }{
		{"[.B2]*[.C2]", "Ma feuille", "2", "[.B3]*[.C3]"},
		{"SUM([.D2:.D2])+[.D3]", "Ma feuille", "", "SUM([.D2:.D3])+[.D4]"},
		{"['Ma feuille'.$A$2:.$A$3]", "Autre", "", "['Ma feuille'.$A$2:.$A$4]"},
		{"$'Ma feuille'.A1:'Ma feuille'.B2 'Ma feuille'.C7", "", "", "$'Ma feuille'.A1:'Ma feuille'.B3 'Ma feuille'.C8"},
		{"Autre.A2:Autre.A9", "", "", "Autre.A2:Autre.A9"},
		{"local-table.$B$2:.$B$4", "", "", "local-table.$B$2:.$B$4"},
	} {
		if got := moveAddresses(c.in, c.sheet, c.item, maps); got != c.want {
			t.Errorf("%s: got %s, want %s", c.in, got, c.want)
		}
	}
}

func TestPresentation(t *testing.T) {
	odp, err := os.ReadFile("testdata/bilan.odp")
	if err != nil {
		t.Fatal(err)
	}
	if a, err := Analyze(odp); err != nil || a.Engine != "standard" || a.Repeats != 1 {
		t.Fatalf("analysis: %+v %v", a, err)
	}
	out, err := Fill(odp, []byte(`{"annee": 2026, "client": {"nom": "Durand"}, "ventes": [{"trimestre": "T1", "montant": 120}, {"trimestre": "T2", "montant": 150}]}`), Options{Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	content := part(t, out, "content.xml")
	for _, want := range []string{`draw:name="Bilan 2026"`, ">Bilan 2026<", "Client : Durand", ">T1<", ">150<"} {
		if !strings.Contains(content, want) {
			t.Errorf("content.xml lacks %s", want)
		}
	}
	if strings.Contains(content, "&lt;&lt;") {
		t.Error("tags left")
	}
}
