package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pdfapi "github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/form"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// formPDF writes a one-page PDF form: two text fields, a check box, then the
// other fields given (the entries of their dictionaries).
func formPDF(extra ...string) []byte {
	fields := append([]string{
		"/FT /Tx /T (Nom[0]) /Rect [50 700 300 720] /DA (/Helv 12 Tf 0 g)",
		"/FT /Tx /T (client.ville) /Rect [50 650 300 670] /DA (/Helv 12 Tf 0 g)",
		"/FT /Btn /T (accord) /Rect [50 600 62 612] /V /Off /AS /Off /AP << /N << /Yes 5 0 R /Off 5 0 R >> >>",
	}, extra...)
	refs := make([]string, len(fields))
	for i := range fields {
		refs[i] = fmt.Sprintf("%d 0 R", i+6)
	}
	list := strings.Join(refs, " ")
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [" + list + "] /DA (/Helv 0 Tf 0 g) /DR << /Font << /Helv 4 0 R >> >> >> >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Annots [" + list + "] >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>",
		"<< /Type /XObject /Subtype /Form /BBox [0 0 12 12] /Length 0 >>\nstream\n\nendstream",
	}
	for _, f := range fields {
		objects = append(objects, "<< /Type /Annot /Subtype /Widget "+f+" /P 3 0 R /F 4 >>")
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.7\n")
	offsets := make([]int, len(objects))
	for i, o := range objects {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, o := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return b.Bytes()
}

// formValues reads back the values of a filled form.
func formValues(t *testing.T, pdf []byte) form.Form {
	t.Helper()
	var out bytes.Buffer
	if err := pdfapi.ExportFormJSON(context.Background(), bytes.NewReader(pdf), &out, "x.pdf", model.NewDefaultConfiguration()); err != nil {
		t.Fatal(err)
	}
	var group form.FormGroup
	if err := json.Unmarshal(out.Bytes(), &group); err != nil || len(group.Forms) != 1 {
		t.Fatalf("export: %v %s", err, out.String())
	}
	return group.Forms[0]
}

func TestFillForm(t *testing.T) {
	ctx := context.Background()
	data := map[string]any{"Nom": "Dupont", "client": map[string]any{"ville": "Paris"}, "accord": "oui"}
	out, missing, err := fillForm(ctx, formPDF(), data)
	if err != nil || len(missing) != 0 {
		t.Fatalf("fill: %v %v", err, missing)
	}
	f := formValues(t, out)
	values := map[string]string{}
	for _, x := range f.TextFields {
		values[x.Name] = x.Value
		if !x.Locked {
			t.Errorf("%s not locked", x.Name)
		}
	}
	if values["Nom[0]"] != "Dupont" || values["client.ville"] != "Paris" || len(f.CheckBoxes) != 1 || !f.CheckBoxes[0].Value {
		t.Fatalf("values: %v %+v", values, f.CheckBoxes)
	}

	if _, missing, err := fillForm(ctx, formPDF(), map[string]any{"Nom": 12.5}); err != nil || strings.Join(missing, ",") != "client.ville,accord" {
		t.Fatalf("missing: %v %v", missing, err)
	}
	if _, _, err := fillForm(ctx, taggedPDF("/Standard /P", "Standard"), data); !errors.Is(err, errNoFormFields) {
		t.Fatalf("a PDF without fields: %v", err)
	}
	if out, missing, err := fillForm(ctx, formPDF(), map[string]any{}); err != nil || !bytes.Equal(out, formPDF()) || len(missing) != 3 {
		t.Fatalf("no data: the form as it is: %v %v", err, missing)
	}
}

func TestFitText(t *testing.T) {
	for _, c := range []struct {
		in   string
		n    int
		want string
	}{
		{"08/11/2026", 8, "08112026"},
		{"2026-11-08", 8, "08112026"},
		{"2026-11-08T10:30:00Z", 8, "08112026"},
		{"8/11/2026", 8, "08112026"},
		{"2026-11-08", 6, "081126"},
		{"123 456 789 00012", 14, "12345678900012"},
		{"75 001", 5, "75001"},
		{"Dupont-Durand", 6, "Dupont"},
		{"Évelyne", 3, "Éve"},
		{"2026-11-08", 10, "2026-11-08"},
		{"2026-11-08", 0, "2026-11-08"},
	} {
		if got := fitText(c.in, c.n); got != c.want {
			t.Errorf("fitText(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
	if got := inDateFormat("2026-11-08", "dd/mm/yyyy"); got != "08/11/2026" {
		t.Errorf("date field: %q", got)
	}
	if got := inDateFormat("08/11/2026", "yyyy-mm-dd"); got != "08/11/2026" {
		t.Errorf("a date not ISO stays: %q", got)
	}
}

// A CERFA names its fields topmostSubform[0].Page1[0].Nom[0]: the data give
// them their last part when it is the only one, else the whole name. Only
// the fields filled are locked.
func TestPDFFormKeys(t *testing.T) {
	ctx := context.Background()
	pdf := formPDF(
		"/FT /Tx /T (topmostSubform[0].Page1[0].dateNaissance[0]) /Rect [50 550 150 570] /DA (/Helv 12 Tf 0 g) /MaxLen 8 /Ff 16777216",
		"/FT /Tx /T (a[0].Commune[0]) /Rect [50 500 300 520] /DA (/Helv 12 Tf 0 g)",
		"/FT /Tx /T (b[0].Commune[0]) /Rect [50 450 300 470] /DA (/Helv 12 Tf 0 g)",
	)
	fields, sample, err := describeForm(ctx, pdf)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(fields)
	want := `[{"path":"Nom","kind":"text","optional":false},{"path":"client.ville","kind":"text","optional":false},` +
		`{"path":"dateNaissance","kind":"text","optional":false},{"path":"a[0].Commune[0]","kind":"text","optional":false},` +
		`{"path":"b[0].Commune[0]","kind":"text","optional":false},{"path":"accord","kind":"flag","optional":false}]`
	if string(got) != want || sample["Nom"] != "Exemple Nom" || sample["dateNaissance"] != "12345678" || sample["accord"] != true {
		t.Fatalf("fields: %s %v", got, sample)
	}

	out, missing, err := fillForm(ctx, pdf, map[string]any{"Nom": "Dupont", "dateNaissance": "2026-11-08", "a[0].Commune[0]": "Paris"})
	if err != nil || strings.Join(missing, ",") != "client.ville,b[0].Commune[0],accord" {
		t.Fatalf("fill: %v %v", err, missing)
	}
	values, locked := map[string]string{}, map[string]bool{}
	for _, x := range formValues(t, out).TextFields {
		values[x.Name], locked[x.Name] = x.Value, x.Locked
	}
	if values["topmostSubform[0].Page1[0].dateNaissance[0]"] != "08112026" || values["a[0].Commune[0]"] != "Paris" || values["b[0].Commune[0]"] != "" {
		t.Fatalf("values: %v", values)
	}
	if !locked["Nom[0]"] || !locked["a[0].Commune[0]"] || locked["client.ville"] || locked["b[0].Commune[0]"] {
		t.Fatalf("only the fields filled are locked: %v", locked)
	}
}

// The Try page reads a PDF form as a template; the library keeps its fields.
func TestPDFFormInConsole(t *testing.T) {
	srv, setup := open(t, nil)
	c := &client{t: t, h: srv.Handler()}
	if code, _, _ := c.do(http.MethodPost, "/api/auth/setup", map[string]string{"setupCode": setup.String(), "username": "admin", "password": "a long enough password"}); code != http.StatusOK {
		t.Fatalf("setup: %d", code)
	}
	srv.SetRenderer(&fakeRenderer{})
	code, body, _ := c.upload("/api/try/analyze", "cerfa.pdf", formPDF(), nil)
	analysis, _ := body["analysis"].(map[string]any)
	if sample, _ := body["sample"].(map[string]any); code != http.StatusOK || analysis["engine"] != "form" || analysis["fields"] != float64(2) ||
		analysis["checkboxes"] != float64(1) || sample["Nom"] != "Exemple Nom" || len(body["fields"].([]any)) != 3 {
		t.Fatalf("analyze: %d %v", code, body)
	}
	if code, body, _ := c.upload("/api/try/analyze", "cg.pdf", minimalPDF(1), nil); code != http.StatusBadRequest || !strings.Contains(fmt.Sprint(body["detail"]), "sans champs") {
		t.Fatalf("a fixed PDF: %d %v", code, body)
	}

	code, body = c.send(http.MethodPost, "/api/templates", map[string]part{"file": {"cerfa.pdf", formPDF()}}, map[string]string{"path": "cerfa/13753.pdf"})
	if fields, _ := body["fields"].([]any); code != http.StatusCreated || len(fields) != 3 {
		t.Fatalf("library: %d %v", code, body)
	}
	if rec := c.raw(http.MethodGet, "/api/templates", "", nil); !strings.Contains(rec.Body.String(), `"engine":"form"`) {
		t.Fatalf("a form in the library: %s", rec.Body.String())
	}
	code, body, _ = c.upload("/api/try/render", "", nil, map[string]string{"templateName": "cerfa/13753.pdf", "data": `{"Nom": "Dupont"}`})
	if code != http.StatusOK || body["engine"] != "form" {
		t.Fatalf("render: %d %v", code, body)
	}
}

func TestRenderPDFForm(t *testing.T) {
	srv, _ := open(t, nil)
	srv.SetRenderer(&fakeRenderer{})
	h, key := srv.Handler(), testKey(t, srv, "cerfa")
	post := func(fields map[string]string) *httptest.ResponseRecorder {
		var b bytes.Buffer
		w := multipart.NewWriter(&b)
		fw, _ := w.CreateFormFile("template", "cerfa.pdf")
		_, _ = fw.Write(formPDF())
		for k, v := range fields {
			_ = w.WriteField(k, v)
		}
		_ = w.Close()
		req := httptest.NewRequest(http.MethodPost, "/v1/render", &b)
		req.Header.Set("Content-Type", w.FormDataContentType())
		req.Header.Set("Authorization", "Bearer "+key)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	rec := post(map[string]string{"data": `{"Nom": "Dupont", "client": {"ville": "Paris"}, "accord": true}`})
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/pdf" || rec.Header().Get("Content-Disposition") != "attachment; filename=cerfa.pdf" ||
		rec.Header().Get("X-Render-Engine") != "form" {
		t.Fatalf("render: %d %v %s", rec.Code, rec.Header(), rec.Body.String())
	}
	if f := formValues(t, rec.Body.Bytes()); f.TextFields[0].Value != "Dupont" && f.TextFields[1].Value != "Dupont" {
		t.Fatalf("filled: %+v", f.TextFields)
	}
	if rec := post(map[string]string{"data": `{"Nom": "Dupont"}`}); rec.Code != http.StatusOK || rec.Header().Get("X-Missing-Fields") != "client.ville,accord" {
		t.Fatalf("warn: %d %v", rec.Code, rec.Header())
	}
	for _, fields := range []map[string]string{
		{"data": `{"Nom": "Dupont"}`, "dataCheck": "strict"},
		{"data": `{}`, "format": "docx"},
		{"data": "pas du JSON"},
	} {
		if rec := post(fields); rec.Code != http.StatusBadRequest {
			t.Fatalf("%v: %d %s", fields, rec.Code, rec.Body.String())
		}
	}
}
