package httpapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime/multipart"
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

// A Word template with charts is prepared and produced under a name that
// makes the worker open its charts; its embedded objects are checked.
func TestWordCharts(t *testing.T) {
	odt, err := os.ReadFile("../merge/testdata/rapport.odt")
	if err != nil {
		t.Fatal(err)
	}
	srv, setup := open(t, nil)
	c := &client{t: t, h: srv.Handler()}
	if code, _, _ := c.do(http.MethodPost, "/api/auth/setup", map[string]string{"setupCode": setup.String(), "username": "admin", "password": "a long enough password"}); code != http.StatusOK {
		t.Fatalf("setup: %d", code)
	}
	fake := &fakeRenderer{odt: odt}
	srv.SetRenderer(fake)

	docx := docxTemplate(t, "", "word/charts/chart1.xml", "<c:chartSpace/>")
	code, body, _ := c.upload("/api/try/analyze", "rapport.docx", docx, nil)
	if analysis, _ := body["analysis"].(map[string]any); code != http.StatusOK || fake.in != "template.charts.docx" || !strings.Contains(fmt.Sprint(analysis["reason"]), "graphique") {
		t.Fatalf("analyze: %d %v %s", code, body, fake.in)
	}
	data := `{"ventes": {"categories": ["T1", "T2"], "values": [3, 4]}}`
	code, body, _ = c.upload("/api/try/render", "rapport.docx", docx, map[string]string{"data": data})
	if code != http.StatusOK || fake.in != "merged.charts.odt" {
		t.Fatalf("render: %d %v %s", code, body, fake.in)
	}
	if code, _, _ := c.upload("/api/try/analyze", "simple.docx", docxTemplate(t, ""), nil); code != http.StatusOK || fake.in != "template.docx" {
		t.Fatalf("without charts: %d %s", code, fake.in)
	}

	// Without charts, the objects of a Word file stay closed: not checked.
	if code, body, _ := c.upload("/api/try/analyze", "ole.docx", docxTemplate(t, "", "word/embeddings/oleObject1.bin", "x"), nil); code != http.StatusOK {
		t.Fatalf("object without charts: %d %v", code, body)
	}
	for name, file := range map[string][]byte{
		"graphique-ole.docx": docxTemplate(t, "", "word/charts/chart1.xml", "<c:chartSpace/>", "word/embeddings/oleObject1.bin", "x"),
		"graphique-ole.odt": zipOf(t, "mimetype", "application/vnd.oasis.opendocument.text",
			"META-INF/manifest.xml", `<manifest:manifest><manifest:file-entry manifest:full-path="Object 1/" manifest:media-type="application/vnd.oasis.opendocument.chart"/></manifest:manifest>`,
			"content.xml", `<office:document-content><draw:frame><draw:object-ole xlink:href="./Object 2"/></draw:frame></office:document-content>`),
	} {
		if code, body, _ := c.upload("/api/try/analyze", name, file, nil); code != http.StatusBadRequest {
			t.Errorf("%s must be refused: %d %v", name, code, body)
		}
	}
}

// The Word add-in reads the document being written with a personal token,
// as an application would with its key; the console session is not enough.
func TestAnalyzeWithToken(t *testing.T) {
	srv, setup := open(t, nil)
	c := &client{t: t, h: srv.Handler()}
	if code, _, _ := c.do(http.MethodPost, "/api/auth/setup", map[string]string{"setupCode": setup.String(), "username": "admin", "password": "a long enough password"}); code != http.StatusOK {
		t.Fatalf("setup: %d", code)
	}
	tpl := odtTemplate(t, `<text:p>Bonjour &lt;&lt;client.nom&gt;&gt;</text:p>`)
	srv.SetRenderer(&fakeRenderer{odt: tpl})
	analyze := func(headers ...string) (int, map[string]any) {
		t.Helper()
		var b bytes.Buffer
		w := multipart.NewWriter(&b)
		fw, _ := w.CreateFormFile("template", "modele.odt")
		_, _ = fw.Write(tpl)
		_ = w.Close()
		rec := c.raw(http.MethodPost, "/v1/templates/analyze", w.FormDataContentType(), b.Bytes(), headers...)
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return rec.Code, body
	}
	if code, _ := analyze(); code != http.StatusUnauthorized {
		t.Fatalf("with the session only: %d", code)
	}
	_, created, _ := c.do(http.MethodPost, "/api/me/tokens", map[string]any{"name": "Word"})
	code, body := analyze("Authorization", "Bearer "+fmt.Sprint(created["secret"]))
	if sample, _ := json.Marshal(body["sample"]); code != http.StatusOK || string(sample) != `{"client":{"nom":"Exemple nom"}}` || body["lint"] == nil {
		t.Fatalf("with a personal token: %d %v", code, body)
	}
}
