package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"docibis/internal/render"
)

// fakeRenderer stands for LibreOffice: a Word file becomes odt, and the "PDF"
// carries the merged content.xml so that the test sees the data.
type fakeRenderer struct {
	mu    sync.Mutex
	calls map[string]int
	odt   []byte
	err   error
	// filter is the last PDF export options received.
	filter string
}

func (f *fakeRenderer) Convert(_ context.Context, in, out, format, filter string) (time.Duration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[format]++
	if format == "pdf" {
		f.filter = filter
	}
	if f.err != nil {
		return 0, f.err
	}
	if format == "odt" {
		return time.Millisecond, os.WriteFile(out, f.odt, 0o600)
	}
	merged, err := os.ReadFile(in)
	if err != nil {
		return 0, err
	}
	zr, err := zip.NewReader(bytes.NewReader(merged), int64(len(merged)))
	if err != nil {
		return 0, err
	}
	for _, zf := range zr.File {
		if zf.Name == "content.xml" {
			content, _ := readZip(zf, 1<<20)
			return time.Millisecond, os.WriteFile(out, append([]byte("%PDF-fake\n"), content...), 0o600)
		}
	}
	return 0, io.ErrUnexpectedEOF
}

func zipOf(t *testing.T, files ...string) []byte {
	t.Helper()
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

func odtTemplate(t *testing.T, body string) []byte {
	return zipOf(t, "mimetype", "application/vnd.oasis.opendocument.text", "content.xml",
		`<?xml version="1.0" encoding="UTF-8"?><office:document-content xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0"><office:body><office:text>`+
			body+`</office:text></office:body></office:document-content>`)
}

func docxTemplate(t *testing.T, rels string, extra ...string) []byte {
	return zipOf(t, append([]string{"word/document.xml", "<w:document/>", "word/_rels/document.xml.rels", "<Relationships>" + rels + "</Relationships>"}, extra...)...)
}

// upload posts a try form: the template file and the other fields.
func (c *client) upload(path, name string, template []byte, fields map[string]string) (int, map[string]any, http.Header) {
	c.t.Helper()
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	if template != nil {
		fw, _ := w.CreateFormFile("template", name)
		_, _ = fw.Write(template)
	}
	for k, v := range fields {
		_ = w.WriteField(k, v)
	}
	_ = w.Close()
	req := httptest.NewRequest(http.MethodPost, path, &b)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if c.cookie != "" {
		req.Header.Set("Cookie", c.cookie)
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		c.t.Fatalf("%s: invalid JSON %q", path, rec.Body.String())
	}
	return rec.Code, out, rec.Header()
}

func TestTry(t *testing.T) {
	srv, setup := open(t, nil)
	c := &client{t: t, h: srv.Handler()}
	tpl := odtTemplate(t, `<text:p>Bonjour &lt;&lt;name&gt;&gt;</text:p>`)

	if code, _, _ := c.upload("/api/try/analyze", "modele.odt", tpl, nil); code != http.StatusUnauthorized {
		t.Fatalf("without a session: %d", code)
	}
	if code, _, _ := c.do(http.MethodPost, "/api/auth/setup", map[string]string{"setupCode": setup.String(), "username": "admin", "password": "a long enough password"}); code != http.StatusOK {
		t.Fatalf("setup: %d", code)
	}
	if code, _, h := c.upload("/api/try/analyze", "modele.odt", tpl, nil); code != http.StatusServiceUnavailable || h.Get("Retry-After") != "2" {
		t.Fatalf("before the workers start: %d %v", code, h)
	}

	fake := &fakeRenderer{odt: tpl}
	srv.SetRenderer(fake)
	code, body, _ := c.upload("/api/try/analyze", "modele.odt", tpl, nil)
	analysis, _ := body["analysis"].(map[string]any)
	if code != http.StatusOK || body["name"] != "modele.odt" || analysis["engine"] != "overlay" || analysis["fields"] != float64(1) {
		t.Fatalf("analyze: %d %v", code, body)
	}
	if sample, _ := json.Marshal(body["sample"]); string(sample) != `{"name":"Exemple name"}` {
		t.Fatalf("sample: %s", sample)
	}

	code, body, _ = c.upload("/api/try/render", "modele.odt", tpl, map[string]string{"data": `{"name": "Ann & Bob"}`})
	pdf, _ := base64.StdEncoding.DecodeString(body["pdf"].(string))
	if code != http.StatusOK || body["engine"] != "standard" || body["size"] != float64(len(pdf)) ||
		!bytes.HasPrefix(pdf, []byte("%PDF")) || !bytes.Contains(pdf, []byte("Bonjour Ann &amp; Bob")) {
		t.Fatalf("render: %d %v %q", code, body, pdf)
	}
	for _, data := range []string{`[1, 2]`, `{oops`, ``} {
		if code, _, _ := c.upload("/api/try/render", "modele.odt", tpl, map[string]string{"data": data}); code != http.StatusBadRequest {
			t.Fatalf("data %q: %d", data, code)
		}
	}
	if code, _, _ := c.upload("/api/try/render", "modele.odt", tpl, nil); code != http.StatusBadRequest {
		t.Fatalf("without data: %d", code)
	}

	// A Word template is converted once, then served from memory.
	link := `<Relationship Id="r1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink" Target="https://example.com" TargetMode="External"/>`
	docx := docxTemplate(t, link)
	for i := 0; i < 2; i++ {
		if code, body, _ := c.upload("/api/try/analyze", "modele.docx", docx, nil); code != http.StatusOK {
			t.Fatalf("Word template: %d %v", code, body)
		}
	}
	if fake.calls["odt"] != 1 {
		t.Fatalf("Word template converted %d times", fake.calls["odt"])
	}

	image := `<Relationship Id="r2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="http://169.254.169.254/x.png" TargetMode="External"/>`
	for name, file := range map[string][]byte{
		"pas-un-zip.docx":  []byte("hello"),
		"~$modele.docx":    docx,
		"macros.docm":      docxTemplate(t, "", "word/vbaProject.bin", "x"),
		"image-liee.docx":  docxTemplate(t, link+image),
		"autre-chose.xlsx": zipOf(t, "xl/workbook.xml", "<workbook/>"),
	} {
		if code, body, _ := c.upload("/api/try/analyze", name, file, nil); code != http.StatusBadRequest {
			t.Fatalf("%s must be refused: %d %v", name, code, body)
		}
	}
	if code, _, _ := c.upload("/api/try/analyze", "", nil, nil); code != http.StatusBadRequest {
		t.Fatalf("without a template: %d", code)
	}

	if code, _, _ := c.do(http.MethodPut, "/api/settings", map[string]any{"values": map[string]string{"max_template_size_mb": "1"}}); code != http.StatusOK {
		t.Fatalf("settings: %d", code)
	}
	big := odtTemplate(t, `<text:p>`+strings.Repeat("x", 1<<20)+`</text:p>`)
	big = append(big, bytes.Repeat([]byte{0}, 1<<20)...)
	if code, body, _ := c.upload("/api/try/analyze", "gros.odt", big, nil); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("too large: %d %v", code, body)
	}

	fake.err = render.ErrBusy
	if code, _, h := c.upload("/api/try/analyze", "autre.docx", docxTemplate(t, ""), nil); code != http.StatusServiceUnavailable || h.Get("Retry-After") == "" {
		t.Fatalf("busy: %d", code)
	}
}

// Templates that make LibreOffice read a file or an address are refused,
// whatever the splitting of the field code; ordinary fields and links stay.
func TestTemplatesReadingOutsideRefused(t *testing.T) {
	docx := func(body string) []byte {
		return docxTemplate(t, "", "word/document.xml", `<w:document><w:body><w:p>`+body+`</w:p></w:body></w:document>`)
	}
	refused := map[string][]byte{
		"INCLUDEPICTURE split over runs": docx(`<w:r><w:fldChar w:fldCharType="begin"/></w:r><w:r><w:instrText xml:space="preserve"> INCLUDE</w:instrText></w:r><w:r><w:instrText>PICTURE "http://169.254.169.254/x" \d</w:instrText></w:r><w:r><w:fldChar w:fldCharType="separate"/></w:r><w:r><w:fldChar w:fldCharType="end"/></w:r>`),
		"DDEAUTO field":                  docx(`<w:fldSimple w:instr=" DDEAUTO c:\\windows\\system32\\cmd.exe "/>`),
		"INCLUDETEXT in a nested field":  docx(`<w:r><w:fldChar w:fldCharType="begin"/></w:r><w:r><w:instrText> IF 1 = 1 </w:instrText></w:r><w:r><w:fldChar w:fldCharType="begin"/></w:r><w:r><w:instrText> INCLUDETEXT "file:///etc/passwd" </w:instrText></w:r><w:r><w:fldChar w:fldCharType="end"/></w:r><w:r><w:fldChar w:fldCharType="end"/></w:r>`),
		"linked image":                   odtTemplate(t, `<draw:frame><draw:image xlink:href="http://127.0.0.1:8080/x.png"/></draw:frame>`),
		"linked file section":            odtTemplate(t, `<text:section><text:section-source xlink:href="file:///etc/passwd"/></text:section>`),
		"DDE connection":                 odtTemplate(t, `<text:dde-connection-decls><text:dde-connection-decl office:name="x"/></text:dde-connection-decls>`),
	}
	for name, raw := range refused {
		if _, err := checkTemplate("modele.docx", raw); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	accepted := map[string][]byte{
		"page, hyperlink and property fields": docx(`<w:fldSimple w:instr=" PAGE "/><w:r><w:fldChar w:fldCharType="begin"/></w:r><w:r><w:instrText> HYPERLINK "https://example.com" </w:instrText></w:r><w:r><w:fldChar w:fldCharType="end"/></w:r><w:fldSimple w:instr=" DOCPROPERTY Import "/>`),
		"embedded image and a link":           odtTemplate(t, `<draw:frame><draw:image xlink:href="Pictures/1.png"/></draw:frame><text:p><text:a xlink:href="https://example.com">x</text:a></text:p>`),
	}
	for name, raw := range accepted {
		if _, err := checkTemplate("modele.docx", raw); err != nil {
			t.Errorf("%s refused: %v", name, err)
		}
	}
}
