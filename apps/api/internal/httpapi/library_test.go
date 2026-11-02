package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	pdfapi "github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

type part struct {
	name    string
	content []byte
}

// send posts a multipart form of files and fields.
func (c *client) send(method, path string, files map[string]part, fields map[string]string) (int, map[string]any) {
	c.t.Helper()
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	for field, f := range files {
		fw, _ := w.CreateFormFile(field, f.name)
		_, _ = fw.Write(f.content)
	}
	for k, v := range fields {
		_ = w.WriteField(k, v)
	}
	_ = w.Close()
	rec := c.raw(method, path, w.FormDataContentType(), b.Bytes())
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		c.t.Fatalf("%s %s: invalid JSON %q", method, path, rec.Body.String())
	}
	return rec.Code, out
}

// raw sends a body as is and returns the whole answer.
func (c *client) raw(method, path, contentType string, body []byte, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.cookie != "" {
		req.Header.Set("Cookie", c.cookie)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	return rec
}

// minimalPDF is a valid PDF of blank pages, with its cross-reference table.
func minimalPDF(pages int) []byte {
	kids := make([]string, pages)
	objs := []string{"<< /Type /Catalog /Pages 2 0 R >>", ""}
	for i := range pages {
		kids[i] = fmt.Sprintf("%d 0 R", i+3)
		objs = append(objs, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] >>")
	}
	objs[1] = fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), pages)
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return b.Bytes()
}

func TestLibraryDocmosisBatchAndReleases(t *testing.T) {
	srv, setup := open(t, nil)
	h := srv.Handler()
	c := &client{t: t, h: h}
	if code, _, _ := c.do(http.MethodPost, "/api/auth/setup", map[string]string{"setupCode": setup.String(), "username": "admin", "password": "a long enough password"}); code != http.StatusOK {
		t.Fatalf("setup: %d", code)
	}
	fake := &fakeRenderer{}
	srv.SetRenderer(fake)
	tpl := odtTemplate(t, `<text:p>Bonjour &lt;&lt;client.nom&gt;&gt; de &lt;&lt;ville&gt;&gt;</text:p>`)

	// A template, read at once: the data it expects.
	code, body := c.send(http.MethodPost, "/api/templates", map[string]part{"file": {"fiche.odt", tpl}}, map[string]string{"path": `acme\contracts\fiche.odt`, "description": "Fiche"})
	tmpl, _ := body["template"].(map[string]any)
	if fields, _ := json.Marshal(body["fields"]); code != http.StatusCreated || tmpl["path"] != "acme/contracts/fiche.odt" || tmpl["published"] != float64(1) ||
		string(fields) != `[{"kind":"text","optional":false,"path":"client.nom"},{"kind":"text","optional":false,"path":"ville"}]` {
		t.Fatalf("create: %d %v", code, body)
	}
	id := int64(tmpl["id"].(float64))
	if code, _ := c.send(http.MethodPost, "/api/templates", map[string]part{"file": {"fiche.odt", tpl}}, map[string]string{"path": "acme/contracts/fiche.odt"}); code != http.StatusBadRequest {
		t.Fatalf("same path twice: %d", code)
	}
	if code, _ := c.send(http.MethodPost, "/api/templates", map[string]part{"file": {"x.odt", tpl}}, map[string]string{"path": "../x.odt"}); code != http.StatusBadRequest {
		t.Fatalf("path going up: %d", code)
	}
	if code, _ := c.send(http.MethodPost, "/api/templates", map[string]part{"file": {"cg.pdf", minimalPDF(2)}}, map[string]string{"path": "acme/cg.pdf"}); code != http.StatusCreated {
		t.Fatalf("fixed PDF: %d", code)
	}

	_, body, _ = c.do(http.MethodPost, "/api/keys", map[string]any{"name": "app"})
	secret := body["secret"].(string)
	keyID := int64(body["key"].(map[string]any)["id"].(float64))
	app := &client{t: t, h: h}
	auth := []string{"Authorization", "Bearer " + secret}

	// /v1/render by name, without the extension as Docmosis allows, with a watermark.
	form := func(fields map[string]string) (string, []byte) {
		var b bytes.Buffer
		w := multipart.NewWriter(&b)
		for k, v := range fields {
			_ = w.WriteField(k, v)
		}
		_ = w.Close()
		return w.FormDataContentType(), b.Bytes()
	}
	ct, raw := form(map[string]string{"templateName": "acme/contracts/fiche", "data": `{"client":{"nom":"Dupont"}}`, "outputName": "devis.pdf", "watermark": "COPIE"})
	rec := app.raw(http.MethodPost, "/v1/render", ct, raw, auth...)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Bonjour Dupont de") || rec.Header().Get("X-Missing-Fields") != "ville" ||
		rec.Header().Get("Content-Disposition") != "attachment; filename=devis.pdf" {
		t.Fatalf("render by name: %d %v %q", rec.Code, rec.Header(), rec.Body.String())
	}
	if !strings.Contains(fake.filter, `"Watermark":{"type":"string","value":"COPIE"}`) {
		t.Fatalf("export options: %s", fake.filter)
	}
	ct, raw = form(map[string]string{"templateName": "acme/contracts/fiche", "data": `{}`, "pdfa": "PDF/A-2b"})
	if rec := app.raw(http.MethodPost, "/v1/render", ct, raw, auth...); rec.Code != http.StatusOK || !strings.Contains(fake.filter, `"SelectPdfVersion":{"type":"long","value":"2"}`) {
		t.Fatalf("PDF/A: %d %s", rec.Code, fake.filter)
	}
	ct, raw = form(map[string]string{"templateName": "acme/contracts/fiche", "data": `{}`, "pdfa": "PDF/A-2b", "watermark": "COPIE"})
	if rec := app.raw(http.MethodPost, "/v1/render", ct, raw, auth...); rec.Code != http.StatusBadRequest {
		t.Fatalf("watermark and PDF/A: %d", rec.Code)
	}
	ct, raw = form(map[string]string{"templateName": "inconnu.docx", "data": `{}`})
	if rec := app.raw(http.MethodPost, "/v1/render", ct, raw, auth...); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown template: %d %s", rec.Code, rec.Body.String())
	}
	ct, raw = form(map[string]string{"templateName": "acme/cg.pdf", "data": `{}`})
	if rec := app.raw(http.MethodPost, "/v1/render", ct, raw, auth...); rec.Code != http.StatusBadRequest {
		t.Fatalf("a fixed PDF is not filled: %d", rec.Code)
	}

	// Strict check: holes are refused.
	if code, body, _ := c.do(http.MethodPatch, fmt.Sprintf("/api/templates/%d", id), map[string]any{"dataCheck": "strict"}); code != http.StatusOK || body["template"].(map[string]any)["dataCheck"] != "strict" {
		t.Fatalf("strict: %d %v", code, body)
	}
	ct, raw = form(map[string]string{"templateName": "acme/contracts/fiche.odt", "data": `{"client":{"nom":"Dupont"}}`})
	if rec := app.raw(http.MethodPost, "/v1/render", ct, raw, auth...); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "ville") {
		t.Fatalf("strict refusal: %d %s", rec.Code, rec.Body.String())
	}

	// The Docmosis route: same names, the key in the body.
	docmosis := func(body map[string]any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		return app.raw(http.MethodPost, "/api/render", "application/json", raw)
	}
	rec = docmosis(map[string]any{"accessKey": secret, "templateName": "acme/contracts/fiche.odt", "outputName": "fiche.pdf", "data": map[string]any{"client": map[string]any{"nom": "Durand"}, "ville": "Paris"}})
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/pdf" || !strings.Contains(rec.Body.String(), "Bonjour Durand de Paris") {
		t.Fatalf("docmosis: %d %q", rec.Code, rec.Body.String())
	}
	rec = docmosis(map[string]any{"accessKey": secret, "templateName": "acme/contracts/fiche.odt", "outputName": "fiche.pdf", "data": `{"client":{"nom":"Durand"},"ville":"Lyon"}`})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Lyon") {
		t.Fatalf("docmosis, data as text: %d", rec.Code)
	}
	vals := url.Values{"accessKey": {secret}, "templateName": {"acme/contracts/fiche.odt"}, "outputName": {"fiche.pdf"}, "data": {`{"client":{"nom":"Form"},"ville":"Nice"}`}}
	if rec := app.raw(http.MethodPost, "/api/render", "application/x-www-form-urlencoded", []byte(vals.Encode())); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Nice") {
		t.Fatalf("docmosis form: %d %s", rec.Code, rec.Body.String())
	}
	// Docmosis options, under their names.
	data := map[string]any{"client": map[string]any{"nom": "Durand"}, "ville": "Paris"}
	rec = docmosis(map[string]any{"accessKey": secret, "templateName": "acme/contracts/fiche.odt", "outputName": "fiche.pdf", "data": data, "pdfSkipEmptyPages": true, "pdfWatermark": "COPIE"})
	fake.mu.Lock()
	filter := fake.filter
	fake.mu.Unlock()
	if rec.Code != http.StatusOK || !strings.Contains(filter, "IsSkipEmptyPages") || !strings.Contains(filter, "COPIE") {
		t.Fatalf("docmosis options: %d %s", rec.Code, filter)
	}
	if rec := docmosis(map[string]any{"accessKey": secret, "templateName": "acme/contracts/fiche.odt", "outputName": "fiche.pdf", "data": data, "pdfRestrictPassword": "x", "pdfRestrictPrinting": 9}); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "pdfRestrictPrinting") {
		t.Fatalf("docmosis bad option: %d %s", rec.Code, rec.Body.String())
	}
	for _, refused := range []struct {
		body map[string]any
		code int
	}{
		{map[string]any{"accessKey": "dib_wrong", "templateName": "acme/contracts/fiche.odt", "outputName": "f.pdf"}, http.StatusUnauthorized},
		{map[string]any{"accessKey": secret, "templateName": "nope.docx", "outputName": "f.pdf", "data": map[string]any{}}, http.StatusNotFound},
	} {
		rec := docmosis(refused.body)
		var e map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &e)
		if rec.Code != refused.code || e["succeeded"] != false || e["shortMsg"] == "" {
			t.Fatalf("docmosis %v: %d %s", refused.body, rec.Code, rec.Body.String())
		}
	}

	// New version, then back to the first one.
	tpl2 := odtTemplate(t, `<text:p>Version deux &lt;&lt;client.nom&gt;&gt;</text:p>`)
	code, body = c.send(http.MethodPost, fmt.Sprintf("/api/templates/%d/versions", id), map[string]part{"file": {"fiche.odt", tpl2}}, map[string]string{"note": "texte revu"})
	if code != http.StatusCreated || body["template"].(map[string]any)["published"] != float64(2) || len(body["versions"].([]any)) != 2 {
		t.Fatalf("new version: %d %v", code, body)
	}
	if code, body, _ := c.do(http.MethodPost, fmt.Sprintf("/api/templates/%d/publish", id), map[string]any{"version": 1}); code != http.StatusOK || body["template"].(map[string]any)["published"] != float64(1) {
		t.Fatalf("back to version 1: %d %v", code, body)
	}
	if code, _, _ := c.do(http.MethodPost, fmt.Sprintf("/api/templates/%d/publish", id), map[string]any{"version": 9}); code != http.StatusBadRequest {
		t.Fatalf("unknown version: %d", code)
	}
	if rec := c.raw(http.MethodGet, fmt.Sprintf("/api/templates/%d/versions/2/file", id), "", nil); rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), tpl2) ||
		rec.Header().Get("Content-Disposition") != "attachment; filename=fiche-v2.odt" {
		t.Fatalf("download: %d %v", rec.Code, rec.Header())
	}

	// A batch: one PDF of fixed PDFs, or a zip.
	batch := func(body map[string]any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		return app.raw(http.MethodPost, "/v1/render/batch", "application/json", raw, auth...)
	}
	rec = batch(map[string]any{"documents": []map[string]any{{"pdf": "acme/cg.pdf"}, {"pdf": "acme/cg"}}, "outputName": "dossier.pdf"})
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Disposition") != "attachment; filename=dossier.pdf" {
		t.Fatalf("batch merge: %d %s", rec.Code, rec.Body.String())
	}
	if n, err := pdfapi.PageCount(context.Background(), bytes.NewReader(rec.Body.Bytes()), model.NewStatelessConfiguration()); err != nil || n != 4 {
		t.Fatalf("merged pages: %d %v", n, err)
	}
	rec = batch(map[string]any{"merge": false, "documents": []map[string]any{
		{"templateName": "acme/contracts/fiche.odt", "data": map[string]any{"client": map[string]any{"nom": "Zip"}, "ville": "Metz"}},
		{"pdf": "acme/cg.pdf"}, {"pdf": "acme/cg.pdf"},
	}})
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("batch zip: %d %s", rec.Code, rec.Body.String())
	}
	zr, _ := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	if strings.Join(names, ",") != "fiche.pdf,cg.pdf,cg-2.pdf" {
		t.Fatalf("zip entries: %v", names)
	}
	if rec := batch(map[string]any{"documents": []map[string]any{{"pdf": "acme/cg.pdf", "templateName": "x"}}}); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Document 1") {
		t.Fatalf("batch item with both: %d %s", rec.Code, rec.Body.String())
	}

	// Import: unchanged, new with its reference, read in the background.
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	for name, content := range map[string][]byte{
		"templates/acme/contracts/fiche.odt":       tpl2,
		"templates/acme/devis.odt":                 tpl,
		"templates/acme/devis.odt.reference.pdf":   minimalPDF(1),
		"templates/acme/devis.odt.data.json":       []byte(`{"client":{"nom":"Ref"},"ville":"Caen"}`),
		"templates/acme/orphan.docx.reference.pdf": minimalPDF(1),
		"__MACOSX/templates/acme/._devis.odt":      []byte("x"),
		"templates/acme/notes.txt":                 []byte("ignored"),
	} {
		w, _ := zw.Create(name)
		_, _ = w.Write(content)
	}
	_ = zw.Close()
	code, body = c.send(http.MethodPost, "/api/templates/import", map[string]part{"file": {"modeles.zip", zb.Bytes()}}, nil)
	if code != http.StatusOK || body["created"] != float64(1) || body["updated"] != float64(1) || body["references"] != float64(1) || len(body["errors"].([]any)) != 1 {
		t.Fatalf("import: %d %v", code, body)
	}
	var devis map[string]any
	for range 100 {
		_, list := c.list("/api/templates?q=devis")
		if len(list) == 1 && list[0]["hasReference"] == true {
			_, devis, _ = c.do(http.MethodGet, fmt.Sprintf("/api/templates/%d", int64(list[0]["id"].(float64))), nil)
			if len(devis["fields"].([]any)) == 2 {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if devis == nil || len(devis["fields"].([]any)) != 2 {
		t.Fatalf("imported template not read: %v", devis)
	}
	rec = c.raw(http.MethodGet, "/api/templates/export", "", nil)
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if rec.Code != http.StatusOK || err != nil {
		t.Fatalf("export: %d %v", rec.Code, err)
	}
	names = names[:0]
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	if got := strings.Join(names, ","); !strings.Contains(got, "templates/acme/devis.odt.reference.pdf") || !strings.Contains(got, "templates/acme/devis.odt.data.json") {
		t.Fatalf("export entries: %s", got)
	}

	// What the applications see.
	rec = app.raw(http.MethodGet, "/v1/templates", "", nil, auth...)
	var contracts []map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &contracts)
	if rec.Code != http.StatusOK || len(contracts) != 3 {
		t.Fatalf("published templates: %d %s", rec.Code, rec.Body.String())
	}

	// Calls per minute of a key.
	if code, body, _ := c.do(http.MethodPatch, fmt.Sprintf("/api/keys/%d", keyID), map[string]any{"ratePerMinute": 2}); code != http.StatusOK || body["ratePerMinute"] != float64(2) {
		t.Fatalf("key rate: %d %v", code, body)
	}
	for i := range 3 {
		rec := app.raw(http.MethodGet, "/v1/templates", "", nil, auth...)
		if want := map[bool]int{true: http.StatusTooManyRequests, false: http.StatusOK}[i == 2]; rec.Code != want {
			t.Fatalf("call %d: %d", i+1, rec.Code)
		}
		if i == 2 && rec.Header().Get("Retry-After") == "" {
			t.Fatalf("no Retry-After: %v", rec.Header())
		}
	}

	// Release notes.
	code, body, _ = c.do(http.MethodPost, "/api/releases", map[string]any{"version": "1.4.0", "title": "Bibliothèque", "content": "- modèles par nom"})
	if code != http.StatusCreated {
		t.Fatalf("release: %d %v", code, body)
	}
	rid := int64(body["id"].(float64))
	if code, _, _ := c.do(http.MethodPost, "/api/releases", map[string]any{"version": "", "title": "x", "content": "y"}); code != http.StatusBadRequest {
		t.Fatalf("empty version: %d", code)
	}
	if code, body, _ := c.do(http.MethodPut, fmt.Sprintf("/api/releases/%d", rid), map[string]any{"version": "1.4.1", "title": "Bibliothèque", "content": "x"}); code != http.StatusOK || body["version"] != "1.4.1" {
		t.Fatalf("release update: %d %v", code, body)
	}
	if code, body, _ := c.do(http.MethodGet, "/api/releases", nil); code != http.StatusOK || body["total"] != float64(1) {
		t.Fatalf("releases: %d %v", code, body)
	}
	if code, _, _ := c.do(http.MethodDelete, fmt.Sprintf("/api/releases/%d", rid), nil); code != http.StatusNoContent {
		t.Fatalf("release delete: %d", code)
	}

	// The journal as CSV.
	rec = c.raw(http.MethodGet, "/api/audit/export?action=template.publish", "", nil)
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Body.String(), "\ufeffdate;auteur;action") || !strings.Contains(rec.Body.String(), "template.publish") ||
		strings.Contains(rec.Body.String(), "template.create") {
		t.Fatalf("audit export: %d %q", rec.Code, rec.Body.String())
	}

	if code, _, _ := c.do(http.MethodDelete, fmt.Sprintf("/api/templates/%d", id), nil); code != http.StatusNoContent {
		t.Fatalf("delete: %d", code)
	}
}

func TestLibraryRoles(t *testing.T) {
	for _, tc := range []struct{ method, path, want string }{
		{http.MethodGet, "/api/templates", ""},
		{http.MethodPost, "/api/templates", "editor"},
		{http.MethodPost, "/api/templates/import", "editor"},
		{http.MethodGet, "/api/templates/export", "editor"},
		{http.MethodDelete, "/api/templates/3", "admin"},
		{http.MethodPost, "/api/templates/check-all", "admin"},
		{http.MethodGet, "/api/releases", ""},
		{http.MethodPost, "/api/releases", "admin"},
		{http.MethodGet, "/api/audit/export", "admin"},
		{http.MethodPatch, "/api/keys/3", "admin"},
	} {
		if got := requiredRole(tc.method, tc.path); got != tc.want {
			t.Errorf("%s %s: %q, want %q", tc.method, tc.path, got, tc.want)
		}
	}
}

func TestFilter(t *testing.T) {
	o := renderOptions{pdfa: "PDF/A-1b", accessible: true, watermark: watermark{text: "SPÉCIMEN", color: "#FF0000", size: 72, angle: 45, tiled: true}}
	var got map[string]map[string]string
	if err := json.Unmarshal([]byte(o.filter("pdf")), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"SelectPdfVersion": "1", "UseTaggedPDF": "true", "PDFUACompliance": "true", "TiledWatermark": "SPÉCIMEN",
		"WatermarkColor": "16711680", "WatermarkFontHeight": "72", "WatermarkRotateAngle": "450"}
	for k, v := range want {
		if got[k]["value"] != v {
			t.Errorf("%s: %v, want %s", k, got[k], v)
		}
	}
	if o.filter("docx") != "" || (renderOptions{pdfa: "none"}).filter("pdf") != "" {
		t.Error("no options expected")
	}
	if _, err := o.set("watermarkColor", "red"); err == nil {
		t.Error("color must be #RRGGBB")
	}

	// A watermark and PDF/A: the request wins over the settings.
	for _, tc := range []struct {
		fields             map[string]string
		watermark, pdfa    string
		accessible, refuse bool
	}{
		{map[string]string{}, "", "PDF/A-2b", false, false},
		{map[string]string{"watermark": "COPIE"}, "COPIE", "none", false, false},
		{map[string]string{"pdfa": "PDF/A-1b"}, "", "PDF/A-1b", false, false},
		{map[string]string{"watermark": "COPIE", "pdfa": "PDF/A-2b"}, "", "", false, true},
		{map[string]string{"watermark": "COPIE", "accessible": "true"}, "", "", false, true},
		{map[string]string{"watermark": "none"}, "", "PDF/A-2b", false, false},
	} {
		o := renderOptions{pdfa: "PDF/A-2b", watermark: watermark{text: "SPÉCIMEN"}}
		for k, v := range tc.fields {
			if _, err := o.set(k, v); err != nil {
				t.Fatal(err)
			}
		}
		err := o.resolve()
		if (err != nil) != tc.refuse || !tc.refuse && (o.watermark.text != tc.watermark || o.pdfa != tc.pdfa || o.accessible != tc.accessible) {
			t.Errorf("%v: %v %+v", tc.fields, err, o)
		}
	}
}

func TestDiffPermille(t *testing.T) {
	page := func(dx int) *image.Gray {
		g := image.NewGray(image.Rect(0, 0, 100, 100))
		for i := range g.Pix {
			g.Pix[i] = 255
		}
		for y := 40; y < 50; y++ {
			for x := 20 + dx; x < 60+dx; x++ {
				g.Pix[y*g.Stride+x] = 0
			}
		}
		return g
	}
	if d := diffPermille(page(0), page(0)); d != 0 {
		t.Fatalf("same page: %v", d)
	}
	if d := diffPermille(page(0), page(2)); d != 0 {
		t.Fatalf("moved by 2 px: %v", d)
	}
	if d := diffPermille(page(0), page(10)); d < 10 {
		t.Fatalf("moved by 10 px: %v", d)
	}
	blank := image.NewGray(image.Rect(0, 0, 100, 100))
	for i := range blank.Pix {
		blank.Pix[i] = 255
	}
	if d := diffPermille(page(0), blank); d != 40 {
		t.Fatalf("missing block: %v", d)
	}
}

func TestRateLimits(t *testing.T) {
	var r rateLimits
	now := time.Now()
	for i := range 60 {
		if ok, _ := r.allow(1, 60, now); !ok {
			t.Fatalf("call %d refused", i)
		}
	}
	ok, wait := r.allow(1, 60, now)
	if ok || wait < 900*time.Millisecond || wait > time.Second {
		t.Fatalf("61st call: %v %v", ok, wait)
	}
	if ok, _ := r.allow(1, 60, now.Add(time.Second)); !ok {
		t.Fatal("a second later: refused")
	}
	if ok, _ := r.allow(2, 60, now); !ok {
		t.Fatal("other key refused")
	}
}
