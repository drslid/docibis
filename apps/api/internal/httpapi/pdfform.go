package httpapi

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	pdfapi "github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/form"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"

	"docibis/internal/merge"
)

// PDF forms (CERFA and others): a PDF with form fields, sent or kept in the
// library, is filled with the data instead of merged by LibreOffice. A field
// reads the value of the data at its name: the whole name (client.nom reads
// {"client": {"nom": …}}), else its last part without index, so that
// topmostSubform[0].Page1[0].Nom[0] reads "Nom". The fields filled are then
// locked; the others stay open, to be completed by hand (place, date,
// signature).

var errNoFormFields = errors.New("no form fields")

var (
	fieldIndex = regexp.MustCompile(`\[\d+\]$`)
	isoDate    = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})(?:[T ][\d:.]+(?:Z|[+-]\d{2}:?\d{2})?)?$`)
	frenchDate = regexp.MustCompile(`^(\d{1,2})[/.-](\d{1,2})[/.-](\d{4})$`)
	numeric    = regexp.MustCompile(`^[\d\s./-]+$`)
	separators = regexp.MustCompile(`[\s./-]`)
)

func isPDF(raw []byte) bool { return bytes.HasPrefix(raw, []byte("%PDF-")) }

// formError tells why a PDF does not fill.
func formError(name string, err error) error {
	if errors.Is(err, errNoFormFields) {
		return refuse("%q est un PDF sans champs de formulaire : il se joint aux documents (/v1/render/batch), il ne se remplit pas.", name)
	}
	return refuse("Formulaire PDF illisible : %v", err)
}

func formConfiguration() *model.Configuration {
	conf := model.NewDefaultConfiguration()
	conf.ValidationMode = model.ValidationRelaxed
	return conf
}

// readPDFForm reads the fields of a PDF form, with their values.
func readPDFForm(ctx context.Context, pdf []byte, conf *model.Configuration) (*form.Form, *form.FormGroup, error) {
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
	return &group.Forms[0], &group, nil
}

// formNames lists the names of the fields of a form.
func formNames(f *form.Form) []string {
	var names []string
	for _, x := range f.TextFields {
		names = append(names, x.Name)
	}
	for _, x := range f.DateFields {
		names = append(names, x.Name)
	}
	for _, x := range f.CheckBoxes {
		names = append(names, x.Name)
	}
	for _, x := range f.RadioButtonGroups {
		names = append(names, x.Name)
	}
	for _, x := range f.ComboBoxes {
		names = append(names, x.Name)
	}
	for _, x := range f.ListBoxes {
		names = append(names, x.Name)
	}
	return names
}

// shortName is the last part of the name of a field, without index.
func shortName(name string) string {
	return fieldIndex.ReplaceAllString(name[strings.LastIndex(name, ".")+1:], "")
}

// formKeys gives each field the key that fills it in the data: the last part
// of an indexed name when no other field ends so (topmostSubform[0].Page1[0].
// Nom[0] is "Nom"), else its whole name.
func formKeys(names []string) map[string]string {
	count := map[string]int{}
	for _, n := range names {
		count[shortName(n)]++
	}
	keys := make(map[string]string, len(names))
	for _, n := range names {
		keys[n] = n
		if s := shortName(n); s != "" && strings.Contains(n, "[") && count[s] == 1 {
			keys[n] = s
		}
	}
	return keys
}

// fitText fits a value to a field of n characters at most (any when n is 0),
// most often a row of boxes that print the separators: a date or a number
// loses them, so that 08/11/2026 or 2026-11-08 fills the boxes JJMMAAAA (or
// JJMMAA) and 123 456 789 00012 the 14 boxes of a SIRET. Others are cut.
func fitText(v string, n int) string {
	if n <= 0 || utf8.RuneCountInString(v) <= n {
		return v
	}
	if y, m, d, ok := dateParts(v); ok && n >= 6 {
		v = d + m + y
		if n < 8 {
			v = d + m + y[2:]
		}
	} else if numeric.MatchString(v) {
		v = separators.ReplaceAllString(v, "")
	}
	if r := []rune(v); len(r) > n {
		v = string(r[:n])
	}
	return v
}

// dateParts reads a date written 2026-11-08 (ISO, a time may follow) or
// 08/11/2026.
func dateParts(v string) (y, m, d string, ok bool) {
	if p := isoDate.FindStringSubmatch(v); p != nil {
		return p[1], p[2], p[3], true
	}
	if p := frenchDate.FindStringSubmatch(v); p != nil {
		two := func(s string) string { return strings.Repeat("0", 2-len(s)) + s }
		return p[3], two(p[2]), two(p[1]), true
	}
	return "", "", "", false
}

// dateIn writes a date in the format of a date field (dd/mm/yyyy…).
func dateIn(format, y, m, d string) string {
	return strings.NewReplacer("yyyy", y, "yy", y[2:], "mm", m, "dd", d).Replace(format)
}

// inDateFormat writes an ISO date (2026-11-08) in the format of a date field;
// other values stay as they are.
func inDateFormat(v, format string) string {
	if p := isoDate.FindStringSubmatch(v); p != nil && format != "" {
		return dateIn(format, p[1], p[2], p[3])
	}
	return v
}

func firstOption(options []string) string {
	if len(options) == 0 {
		return ""
	}
	return options[0]
}

// describeForm lists the data a PDF form reads, under their keys (formKeys),
// with sample data: a check box is a flag, the other fields text.
func describeForm(ctx context.Context, pdf []byte) ([]merge.Field, map[string]any, error) {
	f, _, err := readPDFForm(ctx, pdf, formConfiguration())
	if err != nil {
		return nil, nil, err
	}
	keys := formKeys(formNames(f))
	fields, sample := []merge.Field{}, map[string]any{}
	add := func(name, kind string, v any) {
		if k := keys[name]; sample[k] == nil {
			fields = append(fields, merge.Field{Path: k, Kind: kind})
			sample[k] = v
		}
	}
	for _, x := range f.TextFields {
		v := "Exemple " + shortName(x.Name)
		if x.MaxLen > 0 && utf8.RuneCountInString(v) > x.MaxLen {
			v = strings.Repeat("1234567890", x.MaxLen/10+1)[:x.MaxLen]
		}
		add(x.Name, "text", v)
	}
	for _, x := range f.DateFields {
		add(x.Name, "text", dateIn(cmp.Or(x.Format, "dd/mm/yyyy"), "2026", "12", "31"))
	}
	for _, x := range f.CheckBoxes {
		add(x.Name, "flag", true)
	}
	for _, x := range f.RadioButtonGroups {
		add(x.Name, "text", firstOption(x.Options))
	}
	for _, x := range f.ComboBoxes {
		add(x.Name, "text", firstOption(x.Options))
	}
	for _, x := range f.ListBoxes {
		add(x.Name, "text", firstOption(x.Options))
	}
	return fields, sample, nil
}

// analyzePDFForm describes a PDF form for the Try page and the API.
func analyzePDFForm(ctx context.Context, f tryForm) (TryTemplate, error) {
	fields, sample, err := describeForm(ctx, f.template)
	if err != nil {
		return TryTemplate{}, formError(f.name, err)
	}
	a := TemplateAnalysis{Engine: TemplateAnalysisEngineForm, Reason: "formulaire PDF : DocIbis remplit ses champs lui-même, sans LibreOffice"}
	for _, x := range fields {
		if x.Kind == "flag" {
			a.Checkboxes++
		} else {
			a.Fields++
		}
	}
	return TryTemplate{Name: f.name, Size: len(f.template), Sample: sample, Analysis: a, Fields: ptr(fieldList(fields))}, nil
}

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

// fillForm fills the fields of a PDF form and locks those it fills; missing
// lists the keys (formKeys) of the fields the data do not give.
func fillForm(ctx context.Context, pdf []byte, root map[string]any) (out []byte, missing []string, err error) {
	conf := formConfiguration()
	f, group, err := readPDFForm(ctx, pdf, conf)
	if err != nil {
		return nil, nil, err
	}
	keys := formKeys(formNames(f))
	filled := 0
	value := func(name string, locked *bool) (any, bool) {
		v, ok := formValue(root, name)
		if ok {
			*locked = true
			filled++
		} else {
			missing = append(missing, keys[name])
		}
		return v, ok
	}
	for _, x := range f.TextFields {
		if v, ok := value(x.Name, &x.Locked); ok {
			x.Value = fitText(formText(v), x.MaxLen)
		}
	}
	for _, x := range f.DateFields {
		if v, ok := value(x.Name, &x.Locked); ok {
			x.Value = inDateFormat(formText(v), x.Format)
		}
	}
	for _, x := range f.CheckBoxes {
		if v, ok := value(x.Name, &x.Locked); ok {
			x.Value = formChecked(v)
		}
	}
	for _, x := range f.RadioButtonGroups {
		if v, ok := value(x.Name, &x.Locked); ok {
			x.Value = formText(v)
		}
	}
	for _, x := range f.ComboBoxes {
		if v, ok := value(x.Name, &x.Locked); ok {
			x.Value = formText(v)
		}
	}
	for _, x := range f.ListBoxes {
		if v, ok := value(x.Name, &x.Locked); ok {
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
	if filled == 0 {
		return pdf, missing, nil
	}
	values, err := json.Marshal(group)
	if err != nil {
		return nil, nil, err
	}
	var buf bytes.Buffer
	if err := pdfapi.FillForm(ctx, bytes.NewReader(pdf), bytes.NewReader(values), &buf, conf); err != nil {
		if errors.Is(err, pdfapi.ErrNoFormFieldsAffected) {
			return pdf, missing, nil
		}
		return nil, nil, err
	}
	return buf.Bytes(), missing, nil
}

// produceFormPDF fills a PDF form with the data of a request.
func (s *Server) produceFormPDF(ctx context.Context, f tryForm, format string) (document, error) {
	doc := document{name: f.name, format: "pdf", outputName: f.outputName, libID: f.libID, libVersion: f.libVersion, opts: f.opts, engine: "form"}
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
	if err != nil {
		return doc, formError(doc.name, err)
	}
	mode := f.opts.dataCheck
	if mode == "" {
		mode = f.check
	}
	if len(missing) > 0 && mode == "strict" {
		list := strings.Join(missing[:min(len(missing), 20)], ", ")
		if len(missing) > 20 {
			list += fmt.Sprintf("… (%d en tout)", len(missing))
		}
		return doc, refuse("Données manquantes pour ce formulaire : %s.", list)
	}
	if mode != "off" {
		doc.missing = missing
	}
	doc.merge, doc.content = time.Since(start), content
	s.logger.Debug("document: form filled", "request_id", requestID(ctx), "template", doc.name, "fields_missing", len(missing), "merge_ms", ms(doc.merge))
	return doc, nil
}
