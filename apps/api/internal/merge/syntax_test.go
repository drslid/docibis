package merge

import (
	"strings"
	"testing"
)

func TestDevMode(t *testing.T) {
	tree, err := parse([]byte(head + `<text:p>&lt;&lt;nom&gt;&gt; &lt;&lt;absent&gt;&gt; &lt;&lt;{inconnue(nom)}&gt;&gt;</text:p><text:p>&lt;&lt;op:vide&gt;&gt;</text:p>` + tail))
	if err != nil {
		t.Fatal(err)
	}
	normalize(tree)
	root, _ := decode([]byte(`{"nom": "Dupont"}`))
	(&merger{dev: true}).process(tree, scope{root})
	got := string(tree.bytes())
	for _, want := range []string{
		`<text:p>Dupont <text:span text:style-name="DocIbisDev">[&lt;&lt;absent&gt;&gt; : donnée absente]</text:span> <text:span text:style-name="DocIbisDev">[&lt;&lt;{inconnue(nom)}&gt;&gt; : fonction inconnue : inconnue]</text:span>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s\nin %s", want, got)
		}
	}
	// The style is declared once in each part.
	styled, _ := parse([]byte(`<?xml version="1.0" encoding="UTF-8"?><office:document-content xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0"><office:body/></office:document-content>`))
	addDevStyle(styled)
	if got := string(styled.bytes()); !strings.Contains(got, `<office:automatic-styles><style:style style:name="DocIbisDev" style:family="text">`) {
		t.Error(got)
	}
}

func TestDocumentMetadata(t *testing.T) {
	raw := []byte(`<?xml version="1.0" encoding="UTF-8"?><office:document-meta xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:meta="urn:oasis:names:tc:opendocument:xmlns:meta:1.0"><office:meta><dc:title>Modèle</dc:title><meta:generator>LibreOffice</meta:generator></office:meta></office:document-meta>`)
	out, err := withMeta(raw, map[string]string{"title": "Contrat & avenant", "keywords": "assurance, obsèques"})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, want := range []string{"<dc:title>Contrat &amp; avenant</dc:title>", "<meta:keyword>assurance</meta:keyword><meta:keyword>obsèques</meta:keyword>", "<meta:generator>LibreOffice</meta:generator>"} {
		if !strings.Contains(got, want) {
			t.Errorf("%s lacks %s", got, want)
		}
	}
	if strings.Contains(got, "Modèle") {
		t.Error("old title kept:", got)
	}
}

func TestOptionalParagraphsAndLinks(t *testing.T) {
	body := `<text:p>A</text:p><text:p>Adresse 2 : &lt;&lt;op:adresse2&gt;&gt;</text:p><text:p>Site : &lt;&lt;link:site&gt;&gt;.</text:p>`
	for _, tc := range []struct{ data, want string }{
		{`{"adresse2": " ", "site": "ACME|https://www.example.com"}`,
			`<text:p>A</text:p><text:p>Site : <text:a xlink:type="simple" xlink:href="https://www.example.com" text:style-name="Internet_20_link" text:visited-style-name="Visited_20_Internet_20_Link">ACME</text:a>.</text:p>`},
		{`{"adresse2": "Bât C", "site": "mailto:contact@exemple.fr"}`,
			`<text:p>A</text:p><text:p>Adresse 2 : Bât C</text:p><text:p>Site : <text:a xlink:type="simple" xlink:href="mailto:contact@exemple.fr" text:style-name="Internet_20_link" text:visited-style-name="Visited_20_Internet_20_Link">mailto:contact@exemple.fr</text:a>.</text:p>`},
		{`{"site": "Piège|javascript:alert(1)"}`, `<text:p>A</text:p><text:p>Site : Piège.</text:p>`},
	} {
		if got := run(t, body, tc.data); got != tc.want {
			t.Errorf("%s\n got %s\nwant %s", tc.data, got, tc.want)
		}
	}
	// The last paragraph of a cell stays, emptied.
	cell := `<table:table-cell><text:p>&lt;&lt;op:x&gt;&gt; €</text:p></table:table-cell>`
	if got := run(t, cell, `{}`); got != `<table:table-cell><text:p></text:p></table:table-cell>` {
		t.Error(got)
	}
}

func TestElseIf(t *testing.T) {
	inline := `<text:p>Taille : &lt;&lt;cs_{n&gt;8}&gt;&gt;grande&lt;&lt;else_{n&gt;3}&gt;&gt;moyenne&lt;&lt;else&gt;&gt;petite&lt;&lt;es_&gt;&gt;.</text:p>`
	blocks := `<text:p>&lt;&lt;cs_{n&gt;8}&gt;&gt;</text:p><text:p>Grand</text:p><text:p>&lt;&lt;else_{n&gt;3}&gt;&gt;</text:p><text:p>Moyen</text:p>` +
		`<text:p>&lt;&lt;else_big&gt;&gt;</text:p><text:p>Gros</text:p><text:p>&lt;&lt;else&gt;&gt;</text:p><text:p>Petit</text:p><text:p>&lt;&lt;es_&gt;&gt;</text:p><text:p>Fin</text:p>`
	noElse := `<text:p>&lt;&lt;cs_{n&gt;8}&gt;&gt;</text:p><text:p>Grand</text:p><text:p>&lt;&lt;else_{n&gt;3}&gt;&gt;</text:p><text:p>Moyen</text:p><text:p>&lt;&lt;es_&gt;&gt;</text:p><text:p>Fin</text:p>`
	for _, tc := range []struct{ body, data, want string }{
		{inline, `{"n": 9}`, `<text:p>Taille : grande.</text:p>`},
		{inline, `{"n": 5}`, `<text:p>Taille : moyenne.</text:p>`},
		{inline, `{"n": 2}`, `<text:p>Taille : petite.</text:p>`},
		{blocks, `{"n": 9}`, `<text:p>Grand</text:p><text:p>Fin</text:p>`},
		{blocks, `{"n": 5}`, `<text:p>Moyen</text:p><text:p>Fin</text:p>`},
		{blocks, `{"n": 2, "big": true}`, `<text:p>Gros</text:p><text:p>Fin</text:p>`},
		{blocks, `{"n": 2}`, `<text:p>Petit</text:p><text:p>Fin</text:p>`},
		{noElse, `{"n": 1}`, `<text:p>Fin</text:p>`},
		{noElse, `{"n": 4}`, `<text:p>Moyen</text:p><text:p>Fin</text:p>`},
	} {
		if got := run(t, tc.body, tc.data); got != tc.want {
			t.Errorf("%s with %s\n got %s\nwant %s", tc.body, tc.data, got, tc.want)
		}
	}
}
