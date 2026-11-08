package merge

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
	"time"
)

var xmlText = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

func TestExpressions(t *testing.T) {
	data := `{"prime": 1234.567, "taux": "12,5", "eu": "1.250,50", "us": "1,250.50", "nom": "dupont", "date": "2026-10-19", "lignes": [{"total": 10}, {"total": 2.5}], "age": 17, "statut": "ok", "vide": ""}`
	for _, tc := range []struct{ tag, want string }{
		{`{numFormat(prime, '#,##0.00')}`, "1\u00a0234,57"},
		{`{numFormat(eu, '#,##0.00')}`, "1\u00a0250,50"},
		{`{numFormat(us, '#,##0.00')}`, "1\u00a0250,50"},
		{`{numFormat(prime, '#,##0.00', 'en')}`, "1,234.57"},
		{`{numFormat(2.5, '0')}`, "2"},
		{`{numFormat(3.5, '0')}`, "4"},
		{`{numFormat(taux, '0.00 %')}`, "1250,00 %"},
		{`{numFormat(0.5, '#.##')}`, ",5"},
		{`{numFormat(prime, '#,##0.00 €')}`, "1\u00a0234,57 €"},
		{`{dateFormat(date, 'EEEE d MMMM yyyy')}`, "lundi 19 octobre 2026"},
		{`{dateFormat(date, 'dd MMM yyyy', 'en')}`, "19 Oct 2026"},
		{`{dateFormat($nowUTC, 'dd/MM/yyyy HH:mm')}`, "16/10/2026 09:00"},
		{`{toUpper(nom)}`, "DUPONT"},
		{`{capitalize(nom) + ' & fils'}`, "Dupont &amp; fils"},
		{`{sum(lignes, 'total')}`, "12.5"},
		{`{0.1 + 0.2}`, "0.3"},
		{`{if(age >= 18, 'majeur', 'mineur')}`, "mineur"},
		{`{length(lignes)}`, "2"},
		{`{substring(nom, 0, 3)}`, "dup"},
		{`{inconnu(nom)}`, ""},
	} {
		got := run(t, `<text:p>&lt;&lt;`+xmlText.Replace(tc.tag)+`&gt;&gt;</text:p>`, data)
		if want := `<text:p>` + tc.want + `</text:p>`; got != want {
			t.Errorf("%s\n got %s\nwant %s", tc.tag, got, want)
		}
	}
	for expr, want := range map[string]bool{
		"age >= 18": false, "age < 18 && statut = 'ok'": true, "statut=ok": true, "statut != ok": false,
		"!vide": true, "isEmpty(vide)": true, "prime > 1000 || age > 99": true, "not (age > 10)": false, "absent": false,
	} {
		if got := Condition(expr, map[string]any{"age": 17, "statut": "ok", "vide": "", "prime": 1234.5}); got != want {
			t.Errorf("condition %q = %v, want %v", expr, got, want)
		}
	}
	if got := run(t, `<text:p>&lt;&lt;prime{numFormat='#,##0.0'}&gt;&gt; &lt;&lt;date{dateFormat='dd/MM/yyyy'}&gt;&gt;</text:p>`, data); got != "<text:p>1\u00a0234,6 19/10/2026</text:p>" {
		t.Fatal(got)
	}
}

func TestRepeatedSections(t *testing.T) {
	data := `{"garanties": [{"libelle": "Décès", "capital": 100}, {"libelle": "Invalidité", "capital": 50}], "pieces": ["Identité", "RIB"], "vide": []}`
	got := run(t, `<text:p>Garanties :</text:p><text:p>&lt;&lt;rs_garanties&gt;&gt;</text:p><text:p>&lt;&lt;$itemNum&gt;&gt;. &lt;&lt;libelle&gt;&gt;</text:p>`+
		`<text:p>&lt;&lt;cs_{capital&gt;60}&gt;&gt;</text:p><text:p>Capital élevé</text:p><text:p>&lt;&lt;es_&gt;&gt;</text:p><text:p>&lt;&lt;es_garanties&gt;&gt;</text:p><text:p>Fin</text:p>`, data)
	want := `<text:p>Garanties :</text:p><text:p>1. Décès</text:p><text:p>Capital élevé</text:p><text:p>2. Invalidité</text:p><text:p>Fin</text:p>`
	if got != want {
		t.Fatalf("blocks\n got %s\nwant %s", got, want)
	}
	if got := run(t, `<text:p>Pièces : &lt;&lt;rs_pieces&gt;&gt;&lt;&lt;$item&gt;&gt;, &lt;&lt;es_pieces&gt;&gt;fin.</text:p>`, data); got != `<text:p>Pièces : Identité, RIB, fin.</text:p>` {
		t.Fatalf("inline: %s", got)
	}
	list := `<text:list><text:list-item><text:p>&lt;&lt;rs_garanties&gt;&gt;</text:p></text:list-item><text:list-item><text:p>&lt;&lt;libelle&gt;&gt;</text:p></text:list-item><text:list-item><text:p>&lt;&lt;es_garanties&gt;&gt;</text:p></text:list-item></text:list>`
	if got := run(t, list, data); got != `<text:list><text:list-item><text:p>Décès</text:p></text:list-item><text:list-item><text:p>Invalidité</text:p></text:list-item></text:list>` {
		t.Fatalf("list items: %s", got)
	}
	if got := run(t, `<text:p>A</text:p><text:p>&lt;&lt;rs_vide&gt;&gt;</text:p><text:p>x</text:p><text:p>&lt;&lt;es_vide&gt;&gt;</text:p><text:p>B</text:p>`, data); got != `<text:p>A</text:p><text:p>B</text:p>` {
		t.Fatalf("empty list: %s", got)
	}
	// A table of rows inside a repeated section reads the item's list.
	nested := `<text:p>&lt;&lt;rs_contrats&gt;&gt;</text:p><text:p>&lt;&lt;numero&gt;&gt;</text:p><table:table><table:table-row><table:table-cell><text:p>&lt;&lt;rr_lignes&gt;&gt;&lt;&lt;x&gt;&gt;&lt;&lt;er_lignes&gt;&gt;</text:p></table:table-cell></table:table-row></table:table><text:p>&lt;&lt;es_contrats&gt;&gt;</text:p>`
	got = run(t, nested, `{"contrats": [{"numero": "C1", "lignes": [{"x": "a"}, {"x": "b"}]}, {"numero": "C2", "lignes": [{"x": "c"}]}]}`)
	if strings.Count(got, "<table:table-row>") != 3 || !strings.Contains(got, "C1") || !strings.Contains(got, ">c<") {
		t.Fatalf("nested: %s", got)
	}
	a, err := Analyze(odt(t, `<text:p>&lt;&lt;rs_garanties&gt;&gt;</text:p><text:p>&lt;&lt;libelle&gt;&gt; &lt;&lt;{numFormat(capital, '0.00')}&gt;&gt; &lt;&lt;{qrcode(url)}&gt;&gt;</text:p><text:p>&lt;&lt;es_garanties&gt;&gt;</text:p>`))
	if err != nil || len(a.Unsupported) != 0 || a.Repeats != 1 || a.Images != 1 {
		t.Fatalf("analysis: %+v %v", a, err)
	}
	fields, _ := Fields(odt(t, `<text:p>&lt;&lt;rs_garanties&gt;&gt;</text:p><text:p>&lt;&lt;{numFormat(capital, '0.00')}&gt;&gt;</text:p><text:p>&lt;&lt;es_garanties&gt;&gt;</text:p><text:p>&lt;&lt;nom&gt;&gt;</text:p>`))
	if len(fields) != 3 || fields[0].Path != "garanties" || fields[1].Path != "garanties[].capital" || fields[2].Path != "nom" {
		t.Fatalf("fields: %+v", fields)
	}
	sample, _ := Sample(odt(t, `<text:p>&lt;&lt;rs_garanties&gt;&gt;</text:p><text:p>&lt;&lt;{numFormat(capital, '0.00')}&gt;&gt;</text:p><text:p>&lt;&lt;es_garanties&gt;&gt;</text:p>`))
	if items, ok := sample["garanties"].([]any); !ok || len(items) != 2 {
		t.Fatalf("sample: %v", sample)
	}
}

func TestLint(t *testing.T) {
	body := `<text:p>&lt;&lt;client.nom&gt;&gt; &lt;&lt;client.pre</text:p><text:p>&lt;&lt;cs_{age &gt;}&gt;&gt; x &lt;&lt;{toto(a)}&gt;&gt;</text:p><text:p>&lt;&lt;rs_garanties&gt;&gt;</text:p>` +
		`<text:p>&lt;&lt;else&gt;&gt;</text:p><text:p>&lt;&lt;nom{format=x}&gt;&gt;</text:p><text:p><text:bookmark-start text:name="imgfit_logo"/><text:bookmark-end text:name="imgfit_logo"/></text:p>` +
		`<table:table><table:table-row><table:table-cell><text:p>&lt;&lt;rr_lignes&gt;&gt;</text:p></table:table-cell></table:table-row></table:table>`
	issues, err := Lint(odt(t, body), LintOptions{})
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]int{}
	for _, i := range issues {
		codes[i.Code]++
	}
	for _, want := range []string{"tag-open", "expression", "not-closed", "else-alone", "option", "image-empty", "row-open"} {
		if codes[want] == 0 {
			t.Errorf("missing %s in %+v", want, issues)
		}
	}
	if issues[0].Level != "error" {
		t.Fatalf("errors first: %+v", issues)
	}
	clean, _ := Lint(odt(t, `<text:p>&lt;&lt;rs_g&gt;&gt;&lt;&lt;x&gt;&gt;&lt;&lt;es_g&gt;&gt; &lt;&lt;cs_{a &gt; 2}&gt;&gt;b&lt;&lt;es_&gt;&gt;</text:p>`), LintOptions{Font: func(string) bool { return true }})
	if len(clean) != 0 {
		t.Fatalf("clean template: %+v", clean)
	}
}

func TestCodesAndImageAddresses(t *testing.T) {
	tree, err := parse([]byte(head + `<text:p>Vérifier : &lt;&lt;{qrcode(url, 2.5)}&gt;&gt; colis &lt;&lt;{barcode(colis, 'code128', 6, 1.2)}&gt;&gt; ean &lt;&lt;{barcode(nom, 'ean13')}&gt;&gt;.</text:p>` + tail))
	if err != nil {
		t.Fatal(err)
	}
	root, _ := decode([]byte(`{"url": "https://exemple.fr/v/1", "colis": "DOC-123", "nom": "lettres"}`))
	m := &merger{now: time.Now()}
	m.process(tree, scope{root})
	out := string(tree.bytes())
	if strings.Count(out, "<draw:frame") != 2 || !strings.Contains(out, `svg:width="2.500cm"`) || !strings.Contains(out, `svg:width="6.000cm" svg:height="1.200cm"`) ||
		!strings.Contains(out, "Vérifier : <draw:frame") || !strings.Contains(out, "ean .") || len(m.pictures) != 2 {
		t.Fatalf("codes: %s", out)
	}
	if cfg, err := png.DecodeConfig(bytes.NewReader(m.pictures[0].data)); err != nil || cfg.Width < 250 {
		t.Fatalf("QR image: %+v %v", cfg, err)
	}

	img := image.NewRGBA(image.Rect(0, 0, 40, 20))
	img.Set(1, 1, color.Black)
	var pic bytes.Buffer
	_ = png.Encode(&pic, img)
	frame := `<text:p><text:bookmark-start text:name="imgfit_logo"/><draw:frame svg:width="4cm" svg:height="4cm"><draw:image xlink:href="x.png"/></draw:frame><text:bookmark-end text:name="imgfit_logo"/></text:p>`
	for _, tc := range []struct {
		fetch  func(string) ([]byte, error)
		frames int
	}{
		{nil, 0},
		{func(url string) ([]byte, error) {
			if url != "https://cdn.exemple.fr/logo.png" {
				t.Fatalf("url %s", url)
			}
			return pic.Bytes(), nil
		}, 1},
	} {
		tree, _ := parse([]byte(head + frame + tail))
		root, _ := decode([]byte(`{"logo": "https://cdn.exemple.fr/logo.png"}`))
		m := &merger{now: time.Now(), fetch: tc.fetch}
		m.process(tree, scope{root})
		if got := strings.Count(string(tree.bytes()), "<draw:frame"); got != tc.frames {
			t.Fatalf("fetch %v: %d frames", tc.fetch != nil, got)
		}
	}
	_ = base64.StdEncoding
}
