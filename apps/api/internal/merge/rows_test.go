package merge

import (
	"strings"
	"testing"
	"time"
)

func TestConditionalRows(t *testing.T) {
	row := func(text string) string {
		return `<table:table-row><table:table-cell><text:p>` + text + `</text:p></table:table-cell></table:table-row>`
	}
	simple := `<table:table>` + row("&lt;&lt;cr_show&gt;&gt;") + row("A") + row("&lt;&lt;er_show&gt;&gt;") + row("B") + `</table:table>`
	for data, want := range map[string]string{
		`{"show": true}`:  `<table:table>` + row("A") + row("B") + `</table:table>`,
		`{"show": false}`: `<table:table>` + row("B") + `</table:table>`,
	} {
		if got := run(t, simple, data); got != want {
			t.Errorf("%s\n got %s\nwant %s", data, got, want)
		}
	}
	// In repeated rows, each item decides; an anonymous end; a nested condition.
	repeated := `<table:table>` + row("&lt;&lt;rr_items&gt;&gt;") + row("&lt;&lt;cr_{stock}&gt;&gt;") + row("&lt;&lt;name&gt;&gt;") +
		row("&lt;&lt;cr_{promo}&gt;&gt;") + row("promo") + row("&lt;&lt;er_&gt;&gt;") + row("&lt;&lt;er_&gt;&gt;") + row("&lt;&lt;er_items&gt;&gt;") + `</table:table>`
	got := run(t, repeated, `{"items": [{"name": "a", "stock": true, "promo": true}, {"name": "b", "stock": false}, {"name": "c", "stock": true}]}`)
	if want := `<table:table>` + row("a") + row("promo") + row("c") + `</table:table>`; got != want {
		t.Errorf("repeated\n got %s\nwant %s", got, want)
	}
}

func TestConditionalColumns(t *testing.T) {
	doc := `<?xml version="1.0" encoding="UTF-8"?><office:document-content xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0" xmlns:style="urn:oasis:names:tc:opendocument:xmlns:style:1.0" xmlns:table="urn:oasis:names:tc:opendocument:xmlns:table:1.0">` +
		`<office:automatic-styles><style:style style:name="T.A" style:family="table-column"><style:table-column-properties style:column-width="4cm" style:rel-column-width="4000*"/></style:style>` +
		`<style:style style:name="T.C" style:family="table-column"><style:table-column-properties style:column-width="2cm" style:rel-column-width="2000*"/></style:style></office:automatic-styles>` +
		`<office:body><office:text><table:table table:name="T"><table:table-column table:style-name="T.A" table:number-columns-repeated="2"/><table:table-column table:style-name="T.C"/>` +
		`<table:table-row><table:table-cell><text:p>Nom</text:p></table:table-cell><table:table-cell><text:p>Agence&lt;&lt;cc_{agence}&gt;&gt;</text:p></table:table-cell><table:table-cell><text:p>Prime</text:p></table:table-cell></table:table-row>` +
		`<table:table-row><table:table-cell table:number-columns-spanned="2"><text:p>Total</text:p></table:table-cell><table:covered-table-cell/><table:table-cell><text:p>10</text:p></table:table-cell></table:table-row>` +
		`</table:table></office:text></office:body></office:document-content>`
	fill := func(data string) string {
		tree, err := parse([]byte(doc))
		if err != nil {
			t.Fatal(err)
		}
		normalize(tree)
		root, _ := decode([]byte(data))
		(&merger{}).process(tree, scope{root})
		return string(tree.bytes())
	}
	if got := fill(`{"agence": true}`); !strings.Contains(got, `<text:p>Agence</text:p>`) || !strings.Contains(got, `table:number-columns-spanned="2"`) {
		t.Errorf("kept: %s", got)
	}
	got := fill(`{"agence": false}`)
	for _, want := range []string{
		// One column less, the merged cell narrower, the 4 cm given to the others.
		`<table:table-column table:style-name="T.A.DocIbis1"/><table:table-column table:style-name="T.C.DocIbis2"/>`,
		`<table:table-row><table:table-cell><text:p>Nom</text:p></table:table-cell><table:table-cell><text:p>Prime</text:p></table:table-cell></table:table-row>`,
		`<table:table-row><table:table-cell><text:p>Total</text:p></table:table-cell><table:table-cell><text:p>10</text:p></table:table-cell></table:table-row>`,
		`style:name="T.A.DocIbis1" style:family="table-column"><style:table-column-properties style:column-width="6.6667cm" style:rel-column-width="6667*"/>`,
		`style:name="T.C.DocIbis2" style:family="table-column"><style:table-column-properties style:column-width="3.3333cm" style:rel-column-width="3333*"/>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s\nin %s", want, got)
		}
	}
}

func TestPageBreaks(t *testing.T) {
	doc := `<?xml version="1.0" encoding="UTF-8"?><office:document-content xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0" xmlns:style="urn:oasis:names:tc:opendocument:xmlns:style:1.0" xmlns:fo="urn:oasis:names:tc:opendocument:xmlns:xsl-fo-compatible:1.0">` +
		`<office:automatic-styles><style:style style:name="P1" style:family="paragraph" style:parent-style-name="Standard"><style:text-properties fo:font-weight="bold"/></style:style></office:automatic-styles>` +
		`<office:body><office:text><text:p>Un</text:p><text:p>&lt;&lt;pageBreak&gt;&gt;</text:p><text:p text:style-name="P1">Deux &lt;&lt;pageBreak&gt;&gt;trois</text:p>` +
		`<text:p>&lt;&lt;rs_pages&gt;&gt;</text:p><text:p>Page &lt;&lt;$itemNum&gt;&gt;&lt;&lt;pageBreakNotLast&gt;&gt;</text:p><text:p>&lt;&lt;es_pages&gt;&gt;</text:p></office:text></office:body></office:document-content>`
	tree, err := parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	normalize(tree)
	root, _ := decode([]byte(`{"pages": [1, 2]}`))
	m := &merger{now: time.Now()}
	m.process(tree, scope{root})
	got := string(tree.bytes())
	for _, want := range []string{
		// The paragraph after a lone tag starts the page, with its own style kept.
		`<style:style style:name="DocIbisBreak1" style:family="paragraph" style:parent-style-name="Standard"><style:text-properties fo:font-weight="bold"/><style:paragraph-properties fo:break-before="page"/></style:style>`,
		`<text:p>Un</text:p><text:p text:style-name="DocIbisBreak1">Deux </text:p><text:p text:style-name="DocIbisBreak2">trois</text:p>`,
		// Not after the last item of a repeat.
		`<text:p>Page 1</text:p><text:p text:style-name="DocIbisBreak3">Page 2</text:p></office:text>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s\nin %s", want, got)
		}
	}
}
