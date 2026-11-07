package merge

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

var seriesRanges = regexp.MustCompile(`chart:values-cell-range-address="[^"]*"`)

// A Word chart named chart_ventes takes its categories and series from the
// data; the series added look like those of the template.
func TestWordChart(t *testing.T) {
	odt, err := os.ReadFile("testdata/rapport.odt")
	if err != nil {
		t.Fatal(err)
	}
	if !HasCharts(odt) {
		t.Fatal("the chart is not found")
	}
	if a, err := Analyze(odt); err != nil || a.Engine != "standard" || !strings.Contains(a.Reason, "1 graphique") {
		t.Fatalf("analysis: %+v %v", a, err)
	}
	fields, err := Fields(odt)
	if err != nil || !hasField(fields, Field{Path: "ventes", Kind: "chart", Optional: true}) {
		t.Fatalf("fields: %+v %v", fields, err)
	}
	sample, err := Sample(odt)
	if want := `{"categories":["T1","T2","T3"],"series":[{"name":"Ventes","values":[10,12,9]}]}`; err != nil || jsonOf(t, sample["ventes"]) != want {
		t.Fatalf("sample: %s %v", jsonOf(t, sample["ventes"]), err)
	}

	data := `{"annee": 2026, "client": {"nom": "Durand"}, "ventes": {"categories": ["T1", "T2", "T3", "T4"],
		"series": [{"name": "2025", "values": [10, 12, 9, 14]}, {"name": "2026", "values": [11, "13,5", null]}]}}`
	out, err := Fill(odt, []byte(data), Options{Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	content := part(t, out, "content.xml")
	if !strings.Contains(content, "Rapport 2026 pour Durand") || strings.Contains(content, "docibis:") ||
		!strings.Contains(content, `xlink:href="./Object 2"`) || strings.Contains(content, "ObjectReplacements/Object 2") {
		t.Errorf("content.xml: %s", content[strings.Index(content, "<draw:frame"):][:400])
	}
	chart := part(t, out, "Object 2/content.xml")
	for _, want := range []string{
		`chart:values-cell-range-address="local-table.$B$2:.$B$5" chart:label-cell-address="local-table.$B$1"`,
		`chart:values-cell-range-address="local-table.$C$2:.$C$5" chart:label-cell-address="local-table.$C$1"`,
		`<chart:categories table:cell-range-address="local-table.$A$2:.$A$5"/>`,
		`<table:table-cell office:value-type="string"><text:p>2025</text:p></table:table-cell><table:table-cell office:value-type="string"><text:p>2026</text:p></table:table-cell>`,
		`<table:table-cell office:value-type="string"><text:p>T4</text:p></table:table-cell><table:table-cell office:value-type="float" office:value="14"><text:p>14</text:p></table:table-cell><table:table-cell><text:p/></table:table-cell>`,
		`office:value="13.5"`,
		`<chart:series chart:style-name="ch7-2"`,
		`<style:style style:name="ch7-2" style:family="chart" style:data-style-name="N0"><style:chart-properties chart:link-data-style-to-source="true" chart:label-position="outside"/><style:graphic-properties draw:stroke="none" draw:fill-color="#ed7d31"/>`,
	} {
		if !strings.Contains(chart, want) {
			t.Errorf("chart lacks %s", want)
		}
	}
	if n := len(seriesRanges.FindAllString(chart, -1)); n != 2 || strings.Count(chart, "<chart:data-point") != 8 {
		t.Errorf("%d series, %d points", n, strings.Count(chart, "<chart:data-point"))
	}

	// One series, given by its values only: the name of the template stays.
	out, err = Fill(odt, []byte(`{"ventes": {"categories": ["Nord", "Sud"], "values": [3, 4]}}`), Options{Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if chart := part(t, out, "Object 2/content.xml"); len(seriesRanges.FindAllString(chart, -1)) != 1 || !strings.Contains(chart, "<text:p>Ventes</text:p>") || !strings.Contains(chart, "<text:p>Sud</text:p>") {
		t.Error("single series")
	}

	// Without data, the chart goes, as an image does; dev mode keeps it.
	for _, dev := range []bool{false, true} {
		out, err = Fill(odt, []byte(`{"ventes": "rien"}`), Options{Now: time.Now(), Dev: dev})
		if err != nil {
			t.Fatal(err)
		}
		if kept := strings.Contains(part(t, out, "content.xml"), "<draw:object"); kept != dev {
			t.Errorf("dev %v: chart kept %v", dev, kept)
		}
	}
}

// A chart repeated with a section gets an object of its own in each copy.
func TestRepeatedChart(t *testing.T) {
	odt, err := os.ReadFile("testdata/rapport.odt")
	if err != nil {
		t.Fatal(err)
	}
	odt = replaceInPart(t, odt, "content.xml", func(s string) string {
		s = strings.Replace(s, "<text:p", "<text:p>&lt;&lt;rs_agences&gt;&gt;</text:p><text:p", 1)
		i := strings.LastIndex(s, "</office:text>")
		return s[:i] + "<text:p>&lt;&lt;es_agences&gt;&gt;</text:p>" + s[i:]
	})
	data := `{"agences": [{"ventes": {"categories": ["A"], "values": [1]}}, {"ventes": {"categories": ["B", "C"], "values": [2, 3]}}]}`
	out, err := Fill(odt, []byte(data), Options{Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	content := part(t, out, "content.xml")
	if !strings.Contains(content, `xlink:href="./Object 2"`) || !strings.Contains(content, `xlink:href="./Object 3"`) {
		t.Fatalf("objects: %s", regexp.MustCompile(`xlink:href="[^"]*"`).FindAllString(content, -1))
	}
	first, second := part(t, out, "Object 2/content.xml"), part(t, out, "Object 3/content.xml")
	if !strings.Contains(first, "<text:p>A</text:p>") || strings.Contains(first, "<text:p>B</text:p>") || !strings.Contains(second, "<text:p>C</text:p>") {
		t.Error("each copy shows its own data")
	}
	part(t, out, "Object 3/styles.xml")
	if manifest := part(t, out, "META-INF/manifest.xml"); !strings.Contains(manifest, `manifest:full-path="Object 3/" manifest:media-type="application/vnd.oasis.opendocument.chart"`) ||
		!strings.Contains(manifest, `manifest:full-path="Object 3/content.xml"`) {
		t.Errorf("manifest: %s", manifest)
	}
}

// chart_ventes(trimestre, montant) reads the list of a table: its items
// give the categories and the values.
func TestPresentationChart(t *testing.T) {
	odp, err := os.ReadFile("testdata/bilan.odp")
	if err != nil {
		t.Fatal(err)
	}
	sample, err := Sample(odp)
	if want := `[{"montant":10,"trimestre":"T1"},{"montant":12,"trimestre":"T2"},{"montant":9,"trimestre":"T3"}]`; err != nil || jsonOf(t, sample["ventes"]) != want {
		t.Fatalf("sample: %s %v", jsonOf(t, sample["ventes"]), err)
	}
	out, err := Fill(odp, []byte(`{"annee": 2026, "ventes": [{"trimestre": "T1", "montant": 120}, {"trimestre": "T2", "montant": 150}]}`), Options{Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	chart := part(t, out, "Object 1/content.xml")
	for _, want := range []string{"Ventes 2026", "<text:p>Ventes</text:p>", `office:value="150"`, `chart:values-cell-range-address="local-table.$B$2:.$B$3"`} {
		if !strings.Contains(chart, want) {
			t.Errorf("chart lacks %s", want)
		}
	}
	if content := part(t, out, "content.xml"); !strings.Contains(content, ">150<") {
		t.Error("the table is filled too")
	}
}

func TestColumn(t *testing.T) {
	for i, want := range map[int]string{0: "A", 1: "B", 25: "Z", 26: "AA", 27: "AB", 701: "ZZ", 702: "AAA"} {
		if got := column(i); got != want {
			t.Errorf("column(%d) = %s, want %s", i, got, want)
		}
	}
}

func hasField(fields []Field, f Field) bool {
	for _, g := range fields {
		if g == f {
			return true
		}
	}
	return false
}

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// replaceInPart returns the package with one part changed.
func replaceInPart(t *testing.T, odf []byte, name string, change func(string) string) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(odf), int64(len(odf)))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, f := range zr.File {
		content, err := read(f)
		if err != nil {
			t.Fatal(err)
		}
		if f.Name == name {
			content = []byte(change(string(content)))
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: f.Name, Method: zip.Deflate})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(content)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
