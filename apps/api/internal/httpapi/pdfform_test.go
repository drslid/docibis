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

// formPDF writes a one-page PDF form: two text fields and a check box.
func formPDF() []byte {
	off := "<< /Type /XObject /Subtype /Form /BBox [0 0 12 12] /Length 0 >>\nstream\n\nendstream"
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [4 0 R 5 0 R 6 0 R] /DA (/Helv 0 Tf 0 g) /DR << /Font << /Helv 7 0 R >> >> >> >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Annots [4 0 R 5 0 R 6 0 R] >>",
		"<< /Type /Annot /Subtype /Widget /FT /Tx /T (Nom[0]) /Rect [50 700 300 720] /P 3 0 R /DA (/Helv 12 Tf 0 g) /F 4 >>",
		"<< /Type /Annot /Subtype /Widget /FT /Tx /T (client.ville) /Rect [50 650 300 670] /P 3 0 R /DA (/Helv 12 Tf 0 g) /F 4 >>",
		"<< /Type /Annot /Subtype /Widget /FT /Btn /T (accord) /Rect [50 600 62 612] /P 3 0 R /V /Off /AS /Off /AP << /N << /Yes 8 0 R /Off 9 0 R >> >> /F 4 >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>",
		off, off,
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
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/pdf" || rec.Header().Get("Content-Disposition") != "attachment; filename=cerfa.pdf" {
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
