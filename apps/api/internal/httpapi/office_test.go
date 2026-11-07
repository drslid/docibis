package httpapi

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
)

// Excel and PowerPoint templates are prepared as OpenDocument, merged, and
// produced in a format of their kind only; those that read outside are refused.
func TestOfficeTemplates(t *testing.T) {
	ods, err := os.ReadFile("../merge/testdata/facture.ods")
	if err != nil {
		t.Fatal(err)
	}
	odp, err := os.ReadFile("../merge/testdata/bilan.odp")
	if err != nil {
		t.Fatal(err)
	}
	srv, setup := open(t, nil)
	c := &client{t: t, h: srv.Handler()}
	if code, _, _ := c.do(http.MethodPost, "/api/auth/setup", map[string]string{"setupCode": setup.String(), "username": "admin", "password": "a long enough password"}); code != http.StatusOK {
		t.Fatalf("setup: %d", code)
	}
	fake := &fakeRenderer{odt: ods}
	srv.SetRenderer(fake)
	produced := func(body map[string]any) string {
		out, _ := base64.StdEncoding.DecodeString(fmt.Sprint(body["pdf"]))
		return string(out)
	}

	xlsx := zipOf(t, "xl/workbook.xml", "<workbook/>", "xl/worksheets/sheet1.xml", `<worksheet><sheetData><row r="1"><c r="A1"><f>SUM(B1:B2)</f></c></row></sheetData></worksheet>`)
	code, body, _ := c.upload("/api/try/analyze", "facture.xlsx", xlsx, nil)
	if analysis, _ := body["analysis"].(map[string]any); code != http.StatusOK || analysis["engine"] != "standard" || !strings.Contains(fmt.Sprint(analysis["reason"]), "classeur") {
		t.Fatalf("analyze: %d %v", code, body)
	}
	data := `{"numero": "F-12", "client": {"nom": "Durand"}, "date": "2026-11-06",
		"lignes": [{"designation": "Pose", "quantite": 2, "prix": 10.5}, {"designation": "Câble", "quantite": 1, "prix": 3}]}`
	code, body, _ = c.upload("/api/try/render", "facture.xlsx", xlsx, map[string]string{"data": data, "format": "xlsx"})
	if content := produced(body); code != http.StatusOK || body["format"] != "xlsx" || fake.calls["ods"] != 1 || fake.in != "merged.ods" ||
		!strings.Contains(content, "Durand") || !strings.Contains(content, `table:formula="of:=SUM([.D5:.D6])"`) {
		t.Fatalf("Excel template: %d %v %s", code, body, fake.in)
	}
	for format, ok := range map[string]bool{"pdf": true, "ods": true, "docx": false, "pptx": false} {
		code, body, _ := c.upload("/api/try/render", "facture.xlsx", xlsx, map[string]string{"data": data, "format": format})
		if ok && code != http.StatusOK || !ok && (code != http.StatusBadRequest || !strings.Contains(fmt.Sprint(body["detail"]), "Un modèle Excel ne se produit pas en "+format)) {
			t.Fatalf("Excel template in %s: %d %v", format, code, body)
		}
	}

	fake.odt = odp
	pptx := zipOf(t, "ppt/presentation.xml", "<presentation/>")
	code, body, _ = c.upload("/api/try/render", "bilan.pptx", pptx, map[string]string{"data": `{"annee": 2026, "client": {"nom": "Durand"}, "ventes": []}`, "format": "pptx"})
	if code != http.StatusOK || body["format"] != "pptx" || fake.in != "merged.odp" || !strings.Contains(produced(body), `draw:name="Bilan 2026"`) {
		t.Fatalf("PowerPoint template: %d %v %s", code, body, fake.in)
	}
	if code, body, _ := c.upload("/api/try/render", "bilan.pptx", pptx, map[string]string{"data": `{}`, "format": "xlsx"}); code != http.StatusBadRequest || !strings.Contains(fmt.Sprint(body["detail"]), "PowerPoint") {
		t.Fatalf("PowerPoint template in xlsx: %d %v", code, body)
	}
	// The data of a PowerPoint chart is a workbook inside: checked like a template.
	chartData := zipOf(t, "xl/workbook.xml", "<workbook/>", "xl/worksheets/sheet1.xml", "<worksheet/>")
	if code, body, _ := c.upload("/api/try/analyze", "graphique.pptx", zipOf(t, "ppt/presentation.xml", "<presentation/>", "ppt/embeddings/Microsoft_Excel_Worksheet.xlsx", string(chartData)), nil); code != http.StatusOK {
		t.Fatalf("chart data: %d %v", code, body)
	}

	sheet := func(formula string) string {
		return `<worksheet><sheetData><row r="1"><c r="A1"><f>` + formula + `</f></c></row></sheetData></worksheet>`
	}
	odf := func(formula string) []byte {
		return zipOf(t, "mimetype", "application/vnd.oasis.opendocument.spreadsheet", "content.xml",
			`<office:document-content><office:body><office:spreadsheet><table:table><table:table-row><table:table-cell table:formula="`+formula+`"/></table:table-row></table:table></office:spreadsheet></office:body></office:document-content>`)
	}
	for name, file := range map[string][]byte{
		"liaison.xlsx":   zipOf(t, "xl/workbook.xml", "<workbook/>", "xl/externalLinks/externalLink1.xml", "<externalLink/>"),
		"connexion.xlsx": zipOf(t, "xl/workbook.xml", "<workbook/>", "xl/connections.xml", "<connections/>"),
		"web.xlsx":       zipOf(t, "xl/workbook.xml", "<workbook/>", "xl/worksheets/sheet1.xml", sheet(`_xlfn.WEBSERVICE("http://169.254.169.254/")`)),
		"nom.xlsx":       zipOf(t, "xl/workbook.xml", `<workbook><definedNames><definedName name="x">_xlfn.WEBSERVICE("http://169.254.169.254/")</definedName></definedNames></workbook>`),
		"dde.ods":        odf(`of:=DDE(&quot;soffice&quot;;&quot;/etc/passwd&quot;;&quot;A1&quot;)`),
		"objet.ods": zipOf(t, "mimetype", "application/vnd.oasis.opendocument.spreadsheet", "content.xml", "<office:document-content/>",
			"Object 1/content.xml", `<table:table-cell table:formula="of:=COM.MICROSOFT.WEBSERVICE(&quot;http://169.254.169.254/&quot;)"/>`),
		"image.pptx": zipOf(t, "ppt/presentation.xml", "<presentation/>", "ppt/slides/_rels/slide1.xml.rels",
			`<Relationships><Relationship Id="r1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="http://169.254.169.254/x.png" TargetMode="External"/></Relationships>`),
		"ole.pptx":     zipOf(t, "ppt/presentation.xml", "<presentation/>", "ppt/embeddings/oleObject1.bin", "x"),
		"activex.xlsx": zipOf(t, "xl/workbook.xml", "<workbook/>", "xl/activeX/activeX1.xml", "<ax:ocx/>"),
		"donnees-liees.pptx": zipOf(t, "ppt/presentation.xml", "<presentation/>", "ppt/embeddings/Microsoft_Excel_Worksheet.xlsx",
			string(zipOf(t, "xl/workbook.xml", "<workbook/>", "xl/externalLinks/externalLink1.xml", "<externalLink/>"))),
		"ole.odp": zipOf(t, "mimetype", "application/vnd.oasis.opendocument.presentation", "content.xml",
			`<office:document-content><draw:frame><draw:object-ole xlink:href="./Object 1"/></draw:frame></office:document-content>`),
		"objet-lie.ods": zipOf(t, "mimetype", "application/vnd.oasis.opendocument.spreadsheet", "content.xml", "<office:document-content/>",
			"Object 1/content.xml", `<office:document-content><text:section><text:section-source xlink:href="file:///etc/passwd"/></text:section></office:document-content>`),
	} {
		if code, body, _ := c.upload("/api/try/analyze", name, file, nil); code != http.StatusBadRequest {
			t.Errorf("%s must be refused: %d %v", name, code, body)
		}
	}
}
