package merge

import (
	"archive/zip"
	"bytes"
	"errors"
	"strings"
	"testing"
)

// odtFile builds an OpenDocument text from its automatic styles and body.
func odtFile(t *testing.T, styles, body string, extra ...string) []byte {
	t.Helper()
	content := `<?xml version="1.0" encoding="UTF-8"?><office:document-content xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" xmlns:style="urn:oasis:names:tc:opendocument:xmlns:style:1.0" xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0" xmlns:draw="urn:oasis:names:tc:opendocument:xmlns:drawing:1.0" xmlns:xlink="http://www.w3.org/1999/xlink" xmlns:fo="urn:oasis:names:tc:opendocument:xmlns:xsl-fo-compatible:1.0">` +
		`<office:automatic-styles>` + styles + `</office:automatic-styles><office:body><office:text><text:sequence-decls/>` + body + `</office:text></office:body></office:document-content>`
	files := append([]string{"mimetype", "application/vnd.oasis.opendocument.text", "content.xml", content,
		"META-INF/manifest.xml", `<?xml version="1.0" encoding="UTF-8"?><manifest:manifest xmlns:manifest="urn:oasis:names:tc:opendocument:xmlns:manifest:1.0"></manifest:manifest>`}, extra...)
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for i := 0; i+1 < len(files); i += 2 {
		w, _ := z.Create(files[i])
		_, _ = w.Write([]byte(files[i+1]))
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func zipEntry(t *testing.T, odt []byte, name string) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(odt), int64(len(odt)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if f.Name == name {
			raw, _ := read(f)
			return string(raw)
		}
	}
	return ""
}

func TestIncludeSharedBlocks(t *testing.T) {
	main := odtFile(t, `<style:style style:name="P1" style:family="paragraph"><style:text-properties fo:font-weight="bold"/></style:style>`,
		`<text:p text:style-name="P1">Contrat de <text:span>&lt;&lt;client.nom&gt;&gt;</text:span></text:p>`+
			`<text:p><text:span>&lt;&lt;ref:</text:span>clauses/rgpd&gt;&gt;</text:p><text:p>Fin</text:p>`)
	blocks := map[string][]byte{
		"clauses/rgpd": odtFile(t, `<style:style style:name="P1" style:family="paragraph"><style:text-properties fo:font-style="italic"/></style:style>`,
			`<text:p text:style-name="P1">Vos données, &lt;&lt;client.nom&gt;&gt;, restent chez nous.</text:p><text:p>&lt;&lt;ref:clauses/signature&gt;&gt;</text:p>`),
		"clauses/signature": odtFile(t, ``,
			`<text:p>Signature <draw:frame><draw:image xlink:href="Pictures/sig.png"/></draw:frame></text:p>`, "Pictures/sig.png", "PNG"),
		"boucle": odtFile(t, ``, `<text:p>&lt;&lt;ref:boucle&gt;&gt;</text:p>`),
	}
	read := func(name string) ([]byte, error) {
		if b, ok := blocks[name]; ok {
			return b, nil
		}
		return nil, errors.New("unknown " + name)
	}
	out, err := Include(main, read)
	if err != nil {
		t.Fatal(err)
	}
	content := zipEntry(t, out, "content.xml")
	for _, want := range []string{
		`<style:style style:name="P1" style:family="paragraph"><style:text-properties fo:font-weight="bold"/>`,
		`<style:style style:name="ref1_P1" style:family="paragraph"><style:text-properties fo:font-style="italic"/>`,
		`<text:p text:style-name="ref1_P1">Vos données, &lt;&lt;client.nom&gt;&gt;, restent chez nous.</text:p><text:p>Signature <draw:frame><draw:image xlink:href="Pictures/ref2_sig.png"/></draw:frame></text:p><text:p>Fin</text:p>`,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("missing %s\nin %s", want, content)
		}
	}
	if strings.Contains(content, "ref:") || strings.Count(content, "sequence-decls") != 1 {
		t.Errorf("tags or declarations left: %s", content)
	}
	if zipEntry(t, out, "Pictures/ref2_sig.png") != "PNG" || !strings.Contains(zipEntry(t, out, "META-INF/manifest.xml"), `manifest:full-path="Pictures/ref2_sig.png" manifest:media-type="image/png"`) {
		t.Error("image of the block not copied")
	}

	// The merge then fills the blocks with the same data.
	filled, err := Fill(out, []byte(`{"client": {"nom": "Dupont"}}`), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := zipEntry(t, filled, "content.xml"); !strings.Contains(got, "Vos données, Dupont, restent chez nous.") || !strings.Contains(got, "Contrat de <text:span>Dupont</text:span>") {
		t.Errorf("filled: %s", got)
	}

	if same, err := Include(blocks["clauses/signature"], read); err != nil || !bytes.Equal(same, blocks["clauses/signature"]) {
		t.Error("a template without blocks is returned as is")
	}
	var be *ErrBlock
	if _, err := Include(odtFile(t, ``, `<text:p>&lt;&lt;ref:boucle&gt;&gt;</text:p>`), read); !errors.As(err, &be) || !strings.Contains(err.Error(), "niveaux") {
		t.Errorf("loop: %v", err)
	}
	if _, err := Include(odtFile(t, ``, `<text:p>&lt;&lt;ref:absent&gt;&gt;</text:p>`), read); err == nil || !strings.Contains(err.Error(), "unknown absent") {
		t.Errorf("unknown block: %v", err)
	}
}
