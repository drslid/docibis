package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	pdfapi "github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/form"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// PDF forms (CERFA and others): a PDF with form fields, sent or kept in the
// library, is filled with the data instead of merged by LibreOffice. A field
// reads the value of the data at its name: the whole name (client.nom reads
// {"client": {"nom": …}}), else its last part without index, so that
// topmostSubform[0].Page1[0].Nom[0] reads "Nom". The fields filled are then
// locked: the document is no longer editable.

var errNoFormFields = errors.New("no form fields")

var fieldIndex = regexp.MustCompile(`\[\d+\]$`)

func isPDF(raw []byte) bool { return bytes.HasPrefix(raw, []byte("%PDF-")) }

// formValue finds the data of a field.
func formValue(root map[string]any, name string) (any, bool) {
	if v, ok := root[name]; ok {
		return v, true
	}
	var cur any = root
	for _, part := range strings.Split(name, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			cur = nil
			break
		}
		if cur, ok = m[part]; !ok {
			break
		}
	}
	if cur != nil {
		return cur, true
	}
	last := name[strings.LastIndex(name, ".")+1:]
	if v, ok := root[fieldIndex.ReplaceAllString(last, "")]; ok {
		return v, true
	}
	return nil, false
}

func formText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return map[bool]string{true: "Oui", false: "Non"}[x]
	}
	raw, _ := json.Marshal(v)
	return string(raw)
}

func formChecked(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case float64:
		return x != 0
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "true", "1", "oui", "yes", "x", "on":
			return true
		}
	}
	return false
}

// fillForm fills and locks the fields of a PDF form; missing lists the
// fields the data do not give.
func fillForm(ctx context.Context, pdf []byte, root map[string]any) (out []byte, missing []string, err error) {
	conf := model.NewDefaultConfiguration()
	conf.ValidationMode = model.ValidationRelaxed
	fields, err := pdfapi.FormFields(ctx, bytes.NewReader(pdf), conf)
	if err != nil || len(fields) == 0 {
		return nil, nil, errors.Join(errNoFormFields, err)
	}
	var exported bytes.Buffer
	if err := pdfapi.ExportFormJSON(ctx, bytes.NewReader(pdf), &exported, "form.pdf", conf); err != nil {
		return nil, nil, err
	}
	var group form.FormGroup
	if err := json.Unmarshal(exported.Bytes(), &group); err != nil || len(group.Forms) == 0 {
		return nil, nil, errors.Join(errNoFormFields, err)
	}
	f := &group.Forms[0]
	value := func(name string) (any, bool) {
		v, ok := formValue(root, name)
		if !ok {
			missing = append(missing, name)
		}
		return v, ok
	}
	for _, x := range f.TextFields {
		if v, ok := value(x.Name); ok {
			x.Value = formText(v)
			if x.MaxLen > 0 && len([]rune(x.Value)) > x.MaxLen {
				x.Value = string([]rune(x.Value)[:x.MaxLen])
			}
		}
	}
	for _, x := range f.DateFields {
		if v, ok := value(x.Name); ok {
			x.Value = formText(v)
		}
	}
	for _, x := range f.CheckBoxes {
		if v, ok := value(x.Name); ok {
			x.Value = formChecked(v)
		}
	}
	for _, x := range f.RadioButtonGroups {
		if v, ok := value(x.Name); ok {
			x.Value = formText(v)
		}
	}
	for _, x := range f.ComboBoxes {
		if v, ok := value(x.Name); ok {
			x.Value = formText(v)
		}
	}
	for _, x := range f.ListBoxes {
		if v, ok := value(x.Name); ok {
			if list, isList := v.([]any); isList {
				x.Values = x.Values[:0]
				for _, item := range list {
					x.Values = append(x.Values, formText(item))
				}
			} else {
				x.Values = []string{formText(v)}
			}
		}
	}
	filled, err := json.Marshal(group)
	if err != nil {
		return nil, nil, err
	}
	var buf bytes.Buffer
	if err := pdfapi.FillForm(ctx, bytes.NewReader(pdf), bytes.NewReader(filled), &buf, conf); err != nil {
		return nil, nil, err
	}
	var locked bytes.Buffer
	if err := pdfapi.LockFormFields(ctx, bytes.NewReader(buf.Bytes()), &locked, nil, conf); err != nil {
		return nil, nil, err
	}
	return locked.Bytes(), missing, nil
}

// produceFormPDF fills a PDF form with the data of a request.
func (s *Server) produceFormPDF(ctx context.Context, f tryForm, format string) (document, error) {
	doc := document{name: f.name, format: "pdf", outputName: f.outputName, libID: f.libID, libVersion: f.libVersion, opts: f.opts}
	if f.hasData {
		doc.data = f.data
	}
	if f.libID == 0 {
		doc.template = f.template
	}
	if doc.name == "" {
		doc.name = path.Base(f.templateName)
	}
	if format == "" {
		format = f.format
	}
	if format != "" && format != "pdf" {
		return doc, refuse("Un formulaire PDF se produit en PDF.")
	}
	var root map[string]any
	if !f.hasData || json.Unmarshal(f.data, &root) != nil {
		return doc, refuse("Les données doivent être un objet JSON valide, par exemple { \"nom\": \"Dupont\" }.")
	}
	if err := f.opts.resolve(); err != nil {
		return doc, err
	}
	start := time.Now()
	content, missing, err := fillForm(ctx, f.template, root)
	switch {
	case errors.Is(err, errNoFormFields):
		return doc, refuse("%q est un PDF sans champs de formulaire : il se joint aux documents (/v1/render/batch), il ne se remplit pas.", doc.name)
	case err != nil:
		return doc, refuse("Formulaire PDF illisible : %v", err)
	}
	mode := f.opts.dataCheck
	if mode == "" {
		mode = f.check
	}
	if len(missing) > 0 && mode == "strict" {
		return doc, refuse("Données manquantes pour ce formulaire : %s.", strings.Join(missing, ", "))
	}
	if mode != "off" {
		doc.missing = missing
	}
	doc.merge, doc.content = time.Since(start), content
	s.logger.Debug("document: form filled", "request_id", requestID(ctx), "template", doc.name, "fields_missing", len(missing), "merge_ms", ms(doc.merge))
	return doc, nil
}
