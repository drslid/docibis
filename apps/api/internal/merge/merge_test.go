package merge

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"regexp"
	"strings"
	"testing"
	"time"
)

const head = `<?xml version="1.0" encoding="UTF-8"?><office:document-content xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0" xmlns:table="urn:oasis:names:tc:opendocument:xmlns:table:1.0"><office:body><office:text>`
const tail = `</office:text></office:body></office:document-content>`

func run(t *testing.T, body, data string) string {
	t.Helper()
	root, err := decode([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	tree, err := parse([]byte(head + body + tail))
	if err != nil {
		t.Fatal(err)
	}
	normalize(tree)
	m := &merger{now: time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)}
	m.process(tree, scope{root})
	out := string(tree.bytes())
	if !strings.HasPrefix(out, `<?xml version="1.0" encoding="UTF-8"?>`) || !strings.HasSuffix(out, tail) {
		t.Fatalf("document structure changed: %s", out)
	}
	return strings.TrimSuffix(strings.TrimPrefix(out, head), tail)
}

func TestFields(t *testing.T) {
	got := run(t,
		`<text:p>Nom : <text:span>&lt;&lt;customer.last</text:span>Name&gt;&gt; &lt;&lt;missing.field&gt;&gt;, le &lt;&lt;{dateFormat($nowUTC, ‘dd/MM/yy’)}&gt;&gt; (&lt;&lt; amount &gt;&gt; €)</text:p>`,
		`{"customer": {"lastName": "Dupont & fils"}, "amount": 12.50}`)
	want := `<text:p>Nom : <text:span>Dupont &amp; fils</text:span> , le 16/10/26 (12.50 €)</text:p>`
	if got != want {
		t.Fatalf("\n got %s\nwant %s", got, want)
	}
	// Data copied from Word keep its line breaks (\v): XML refuses them.
	if got := run(t, `<text:p>&lt;&lt;name&gt;&gt;</text:p>`, `{"name": "Rue\u000bdu Port\u0001"}`); got != `<text:p>Rue du Port</text:p>` {
		t.Fatalf("control characters: %q", got)
	}
}

func TestCheckboxes(t *testing.T) {
	got := run(t,
		`<text:p>&lt;&lt;isMr{renderer=boolean(‘wingdings2’)}&gt;&gt; M. &lt;&lt;isMrs{renderer=boolean('wingdings2')}&gt;&gt; Mme</text:p>`,
		`{"isMr": true, "isMrs": false}`)
	if got != `<text:p>☑ M. ☐ Mme</text:p>` {
		t.Fatal(got)
	}
}

func TestInlineSections(t *testing.T) {
	body := `<text:p>A&lt;&lt;cs_{isMinor=true}&gt;&gt; mineur&lt;&lt;else&gt;&gt; majeur&lt;&lt;es_&gt;&gt;.</text:p>`
	if got := run(t, body, `{"isMinor": true}`); got != `<text:p>A mineur.</text:p>` {
		t.Fatal(got)
	}
	if got := run(t, body, `{"isMinor": false}`); got != `<text:p>A majeur.</text:p>` {
		t.Fatal(got)
	}
}

func TestBlockSections(t *testing.T) {
	body := `<text:p>avant</text:p><text:p>&lt;&lt;cs_consent&gt;&gt;</text:p><text:p>dedans 1</text:p><text:p>dedans 2</text:p><text:p>&lt;&lt;es_consent&gt;&gt;</text:p><text:p>après</text:p>`
	if got := run(t, body, `{"consent": "yes"}`); got != `<text:p>avant</text:p><text:p>dedans 1</text:p><text:p>dedans 2</text:p><text:p>après</text:p>` {
		t.Fatal(got)
	}
	if got := run(t, body, `{"consent": false}`); got != `<text:p>avant</text:p><text:p>après</text:p>` {
		t.Fatal(got)
	}
}

// docmosisCases are sections whose markers sit inside paragraphs; want is what
// Docmosis 2.9.6 produced for them, one "style|text" per paragraph.
var docmosisCases = []struct {
	name  string
	paras []string
}{
	{"end-start", []string{"a1 before<<cs_{c1=true}>>", "a2 inside", "<<es_>>a3 after"}},
	{"alone", []string{"<<cs_{c2=true}>>", "b2 inside", "<<es_>>", "b4 after"}},
	{"mid-mid", []string{"c1 before<<cs_{c3=true}>>c1 tail", "c2 inside", "c3 head<<es_>>c3 tail"}},
	{"end-mid-space", []string{"d1 before<<cs_{c4=true}>>", "d2 inside", "d3 head<<es_>> "}},
	{"false-mid-mid", []string{"e1 before<<cs_{c5=true}>>e1 tail", "e2 inside", "e3 head<<es_>>e3 tail"}},
	{"false-end-space", []string{"f1 before<<cs_{c6=true}>>", "f2 inside", "f3 head<<es_>> "}},
	{"inline", []string{"g1 x<<cs_{c7=true}>>shown<<es_>>y"}},
	{"inline-false", []string{"h1 x<<cs_{c8=true}>>hidden<<es_>>y"}},
	{"else", []string{"i1 x<<cs_{c9=true}>>", "i2 yes", "<<else>>", "i4 no", "<<es_>>", "i6 after"}},
	{"else-false", []string{"j1 x<<cs_{c10=true}>>", "j2 yes", "<<else>>", "j4 no", "<<es_>>", "j6 after"}},
	{"alone-mid", []string{"<<cs_{c11=true}>>", "k2 inside", "k3 head<<es_>>k3 tail"}},
	{"mid-alone", []string{"l1 before<<cs_{c12=true}>>l1 tail", "l2 inside", "<<es_>>", "l4 after"}},
	{"false-mid-alone", []string{"m1 before<<cs_{c13=true}>>m1 tail", "m2 inside", "<<es_>>", "m4 after"}},
	{"else-mid", []string{"n1 x<<cs_{c14=true}>>n1 tail", "n2 yes", "n3 a<<else>>n3 b", "n4 no", "n5 c<<es_>>n5 d"}},
	{"else-mid-false", []string{"o1 x<<cs_{c15=true}>>o1 tail", "o2 yes", "o3 a<<else>>o3 b", "o4 no", "o5 c<<es_>>o5 d"}},
}

const docmosisData = `{"c1": true, "c2": true, "c3": true, "c4": true, "c5": false, "c6": false, "c7": true, "c8": false,
	"c9": true, "c10": false, "c11": true, "c12": true, "c13": false, "c14": true, "c15": false}`

var docmosisWant = []string{
	"Case|== end-start", "S1|a1 before", "S2|a2 inside", "S3|", "S1|", "S3|a3 after",
	"Case|== alone", "S5|b2 inside", "S7|b4 after",
	"Case|== mid-mid", "S8|c1 beforec1 tail", "S9|c2 inside", "S10|c3 head", "S8|", "S10|c3 tail",
	"Case|== end-mid-space", "S11|d1 before", "S12|d2 inside", "S13|d3 head", "S11|", "S13|",
	"Case|== false-mid-mid", "S14|e1 before", "S16|e3 tail",
	"Case|== false-end-space", "S17|f1 before", "S19|",
	"Case|== inline", "S20|g1 xshowny",
	"Case|== inline-false", "S21|h1 xy",
	"Case|== else", "S22|i1 x", "S23|i2 yes", "S22|", "S27|i6 after",
	"Case|== else-false", "S28|j1 x", "S31|j4 no", "S33|j6 after",
	"Case|== alone-mid", "S35|k2 inside", "S36|k3 head", "S36|k3 tail",
	"Case|== mid-alone", "S37|l1 beforel1 tail", "S38|l2 inside", "S37|", "S40|l4 after",
	"Case|== false-mid-alone", "S41|m1 before", "S44|m4 after",
	"Case|== else-mid", "S45|n1 xn1 tail", "S46|n2 yes", "S47|n3 a", "S45|", "S47|", "S49|n5 d",
	"Case|== else-mid-false", "S50|o1 x", "S52|o3 b", "S53|o4 no", "S54|o5 c", "S52|", "S54|o5 d",
}

func TestSectionsLikeDocmosis(t *testing.T) {
	var body strings.Builder
	n := 0
	for _, c := range docmosisCases {
		body.WriteString(`<text:p text:style-name="Case">== ` + c.name + `</text:p>`)
		for _, p := range c.paras {
			n++
			text := strings.NewReplacer("<", "&lt;", ">", "&gt;").Replace(strings.TrimSuffix(p, " "))
			if strings.HasSuffix(p, " ") {
				text += "<text:s/>"
			}
			fmt.Fprintf(&body, `<text:p text:style-name="S%d">%s</text:p>`, n, text)
		}
	}
	got := run(t, body.String(), docmosisData)
	var paras []string
	for _, m := range regexp.MustCompile(`<text:p text:style-name="([^"]+)">(.*?)</text:p>|<text:p text:style-name="([^"]+)"/>`).FindAllStringSubmatch(got, -1) {
		style, inner := m[1], m[2]
		if style == "" {
			style = m[3]
		}
		paras = append(paras, style+"|"+regexp.MustCompile(`<[^>]*>`).ReplaceAllString(inner, ""))
	}
	if strings.Join(paras, "\n") != strings.Join(docmosisWant, "\n") {
		t.Fatalf("\n got %q\nwant %q", paras, docmosisWant)
	}
}

func TestAnalyze(t *testing.T) {
	cases := []struct{ body, engine string }{
		// Fields at the end of their line, after a tab, checkboxes: fixed layout.
		{`<text:p>Nom : &lt;&lt;name&gt;&gt;</text:p><text:p>&lt;&lt;city&gt;&gt;<text:tab/>Pays</text:p><text:p>&lt;&lt;ok{renderer=boolean('wingdings2')}&gt;&gt; Oui</text:p>`, "overlay"},
		// A field inside a sentence: the text flows.
		{`<text:p>Bonjour &lt;&lt;name&gt;&gt;, merci.</text:p>`, "native"},
		{`<text:p>Bonjour &lt;&lt;name&gt;&gt;, merci.</text:p><draw:custom-shape><text:p>Titre</text:p></draw:custom-shape>`, "standard"},
		{`<text:p>&lt;&lt;{ordinal(amount)}&gt;&gt;</text:p>`, "standard"},
	}
	for _, c := range cases {
		a, err := Analyze(odt(t, c.body))
		if err != nil || a.Engine != c.engine {
			t.Fatalf("%s: %+v %v", c.body, a, err)
		}
	}
}

func TestRepeatedRows(t *testing.T) {
	body := `<table:table><table:table-row><table:table-cell><text:p>Nom</text:p></table:table-cell></table:table-row>` +
		`<table:table-row><table:table-cell><text:p>&lt;&lt;rr_customers&gt;&gt;&lt;&lt;name&gt;&gt; (&lt;&lt;product&gt;&gt;)&lt;&lt;er_customers&gt;&gt;</text:p></table:table-cell></table:table-row></table:table>`
	got := run(t, body, `{"product": "Obsèques", "customers": [{"name": "Ann"}, {"name": "Bob"}]}`)
	want := `<table:table><table:table-row><table:table-cell><text:p>Nom</text:p></table:table-cell></table:table-row>` +
		`<table:table-row><table:table-cell><text:p>Ann (Obsèques)</text:p></table:table-cell></table:table-row>` +
		`<table:table-row><table:table-cell><text:p>Bob (Obsèques)</text:p></table:table-cell></table:table-row></table:table>`
	if got != want {
		t.Fatalf("\n got %s\nwant %s", got, want)
	}
	if got := run(t, body, `{"customers": []}`); strings.Contains(got, "rr_") || strings.Count(got, "table-row>") != 2 {
		t.Fatalf("empty list: %s", got)
	}

	// Rows holding only a marker are not repeated.
	marked := `<table:table><table:table-row><table:table-cell><text:p>&lt;&lt;rr_customers&gt;&gt;</text:p></table:table-cell></table:table-row>` +
		`<table:table-row><table:table-cell><text:p>&lt;&lt;name&gt;&gt;</text:p></table:table-cell></table:table-row>` +
		`<table:table-row><table:table-cell><text:p>&lt;&lt;er_customers&gt;&gt;</text:p></table:table-cell></table:table-row></table:table>`
	got = run(t, marked, `{"customers": [{"name": "Ann"}, {"name": "Bob"}]}`)
	if got != `<table:table><table:table-row><table:table-cell><text:p>Ann</text:p></table:table-cell></table:table-row><table:table-row><table:table-cell><text:p>Bob</text:p></table:table-cell></table:table-row></table:table>` {
		t.Fatalf("marker rows: %s", got)
	}
}

func odt(t *testing.T, content string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	w, _ := z.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	_, _ = w.Write([]byte("application/vnd.oasis.opendocument.text"))
	w, _ = z.Create("content.xml")
	_, _ = w.Write([]byte(head + content + tail))
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestODTAndSample(t *testing.T) {
	tpl := odt(t, `<text:p>&lt;&lt;customer.name&gt;&gt; &lt;&lt;isMr{renderer=boolean('wingdings2')}&gt;&gt;&lt;&lt;cs_{consent.ok=true}&gt;&gt; oui&lt;&lt;es_&gt;&gt;</text:p>`)
	data, err := Sample(tpl)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(data)
	if string(raw) != `{"consent":{"ok":true},"customer":{"name":"Exemple name"},"isMr":true}` {
		t.Fatalf("sample: %s", raw)
	}
	merged, err := ODT(tpl, raw, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(merged), int64(len(merged)))
	if err != nil || z.File[0].Name != "mimetype" || z.File[0].Method != zip.Store {
		t.Fatalf("mimetype must stay first and stored: %v", err)
	}
	content, _ := read(z.File[1])
	if !strings.Contains(string(content), `<text:p>Exemple name ☑ oui</text:p>`) {
		t.Fatalf("merged: %s", content)
	}
}

func TestImages(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 400, 100))
	var png400 bytes.Buffer
	_ = png.Encode(&png400, img)
	uri := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png400.Bytes())
	body := `<text:p><text:bookmark-start text:name="imgfit_logo"/><draw:frame svg:x="1cm" svg:width="4cm" svg:height="2cm">` +
		`<draw:image xlink:href="Pictures/old.png" draw:mime-type="image/png"/></draw:frame><text:bookmark-end text:name="imgfit_logo"/>Logo</text:p>`
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	w, _ := z.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	_, _ = w.Write([]byte("application/vnd.oasis.opendocument.text"))
	w, _ = z.Create("content.xml")
	_, _ = w.Write([]byte(head + body + tail))
	w, _ = z.Create("META-INF/manifest.xml")
	_, _ = w.Write([]byte(`<manifest:manifest><manifest:file-entry manifest:full-path="content.xml" manifest:media-type="text/xml"/></manifest:manifest>`))
	_ = z.Close()

	sample, err := Sample(b.Bytes())
	if err != nil || !strings.HasPrefix(fmt.Sprint(sample["logo"]), "data:image/png;base64,") {
		t.Fatalf("sample: %v %v", sample, err)
	}

	merged, err := ODT(b.Bytes(), []byte(`{"logo": "`+uri+`"}`), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	zr, _ := zip.NewReader(bytes.NewReader(merged), int64(len(merged)))
	for _, f := range zr.File {
		content, _ := read(f)
		files[f.Name] = string(content)
	}
	content := strings.TrimSuffix(strings.TrimPrefix(files["content.xml"], head), tail)
	// 400 x 100 px in 4 x 2 cm: 4 x 1 cm, same corner; bookmarks removed.
	want := `<text:p><draw:frame svg:x="1cm" svg:width="4.0000cm" svg:height="1.0000cm"><draw:image xlink:href="Pictures/docibis-1.png" draw:mime-type="image/png"/></draw:frame>Logo</text:p>`
	if content != want {
		t.Fatalf("\n got %s\nwant %s", content, want)
	}
	if files["Pictures/docibis-1.png"] != png400.String() || !strings.Contains(files["META-INF/manifest.xml"], `manifest:full-path="Pictures/docibis-1.png" manifest:media-type="image/png"`) {
		t.Fatal("image not added to the package")
	}

	// No usable image: Docmosis removes the placeholder.
	for _, data := range []string{`{}`, `{"logo": "Exemple logo"}`, `{"logo": "data:image/png;base64,bm90IGFuIGltYWdl"}`, `{"logo": "https://cdn.example/logo.png"}`} {
		if got := run(t, body, data); got != `<text:p>Logo</text:p>` {
			t.Fatalf("%s: %s", data, got)
		}
	}
}

// A template or data built to exhaust the server is refused, not followed.
func TestLimits(t *testing.T) {
	deep := strings.Repeat("<text:span>", maxDepth+1) + strings.Repeat("</text:span>", maxDepth+1)
	if _, err := Fill(odt(t, `<text:p>`+deep+`</text:p>`), []byte(`{}`), Options{}); err == nil {
		t.Error("nesting beyond the limit accepted")
	}
	if _, err := parseExpr(strings.Repeat("(", maxTokens) + "1" + strings.Repeat(")", maxTokens)); err == nil {
		t.Error("expression beyond the limit accepted")
	}
	row := `<table:table><table:table-row><table:table-cell><text:p>&lt;&lt;rr_l&gt;&gt;&lt;&lt;x&gt;&gt;&lt;&lt;er_l&gt;&gt;</text:p></table:table-cell></table:table-row></table:table>`
	data := `{"l":[` + strings.TrimSuffix(strings.Repeat(`{"x":1},`, maxCopies+1), ",") + `]}`
	if _, err := Fill(odt(t, row), []byte(data), Options{}); !errors.Is(err, ErrTooLarge) {
		t.Errorf("repeats beyond the limit: %v", err)
	}
	if _, err := Fill(odt(t, row), []byte(`{"l":[{"x":1},{"x":2}]}`), Options{}); err != nil {
		t.Errorf("small repeat: %v", err)
	}
}
