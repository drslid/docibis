package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"

	pdfapi "github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"

	"docibis/internal/store"
)

// renderOptions are the choices of a request for the document: PDF/A,
// accessibility, watermark, protection, metadata and data check. Unset ones
// take the settings.
type renderOptions struct {
	pdfa       string
	accessible bool
	watermark  watermark
	dataCheck  string
	// Set by the request rather than by the settings.
	pdfaSet, accessibleSet bool
	// Opening password and restrictions, under the Docmosis names; never
	// written to the history nor to the logs.
	password string
	restrict restrictions
	// skipEmptyPages leaves out the blank pages added for odd/even breaks.
	skipEmptyPages bool
	// meta are the title, author, subject and keywords of the document.
	meta map[string]string
	// devMode shows in the document what cannot be filled, highlighted.
	devMode bool
	// seal certifies the PDF with the electronic seal of the organization.
	seal bool
}

// restrictions of a PDF, under an owner password. Printing (0-2) and
// editing (0-4) are the LibreOffice scales; empty means allowed.
type restrictions struct {
	password, printing, editing string
	noCopy, noAccessibility     bool
}

func (r restrictions) asked() bool {
	return r.printing != "" || r.editing != "" || r.noCopy || r.noAccessibility
}

// flags are the permissions of the PDF, on the scales of LibreOffice and
// Docmosis: printing 0 none, 1 low resolution, 2 full; editing 0 none,
// 1 pages, 2 forms, 3 forms and comments, 4 all but extracting pages.
func (r restrictions) flags() model.PermissionFlags {
	if r.password == "" {
		return model.PermissionsAll
	}
	f := model.PermissionsNone
	switch r.printing {
	case "", "2":
		f |= model.PermissionPrintRev2 | model.PermissionPrintRev3
	case "1":
		f |= model.PermissionPrintRev2
	}
	switch r.editing {
	case "", "4":
		f |= model.PermissionModify | model.PermissionModAnnFillForm | model.PermissionFillRev3 | model.PermissionAssembleRev3
	case "3":
		f |= model.PermissionModAnnFillForm | model.PermissionFillRev3
	case "2":
		f |= model.PermissionFillRev3
	case "1":
		f |= model.PermissionAssembleRev3
	}
	if !r.noCopy {
		f |= model.PermissionExtract
	}
	if !r.noAccessibility {
		f |= model.PermissionExtractRev3
	}
	return f
}

// protected tells whether the PDF gets a password.
func (o renderOptions) protected() bool { return o.password != "" || o.restrict.password != "" }

// protect encrypts a PDF with AES-256 (LibreOffice only offers RC4): the
// opening password, and the restrictions under the owner password.
func (o renderOptions) protect(ctx context.Context, pdf []byte) ([]byte, error) {
	if !o.protected() {
		return pdf, nil
	}
	owner := o.restrict.password
	if owner == "" {
		owner = rand.Text()
	}
	conf := model.NewAESConfiguration(o.password, owner, 256)
	conf.Permissions = o.restrict.flags()
	var out bytes.Buffer
	if err := pdfapi.Encrypt(ctx, bytes.NewReader(pdf), &out, conf); err != nil {
		return nil, fmt.Errorf("protection du PDF : %w", err)
	}
	return out.Bytes(), nil
}

type watermark struct {
	text, color  string
	size, angle  int
	tiled, isSet bool
}

var (
	pdfaVersions = map[string]string{"PDF/A-1b": "1", "PDF/A-2b": "2", "PDF/A-3b": "3"}
	hexColor     = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
)

// defaultOptions reads the settings of the documents.
func (s *Server) defaultOptions() renderOptions {
	o := renderOptions{pdfa: "none"}
	cfg := s.cfg.Load()
	if cfg == nil {
		return o
	}
	o.pdfa = cfg.String("default_pdfa")
	o.accessible = cfg.String("default_accessible") == "true"
	o.watermark = watermark{
		text: strings.TrimSpace(cfg.String("watermark_text")), color: cfg.String("watermark_color"),
		size: cfg.Int("watermark_size"), angle: cfg.Int("watermark_angle"), tiled: cfg.String("watermark_tiled") == "true",
	}
	return o
}

// set applies one field of a form; it reports false for other fields.
func (o *renderOptions) set(name, value string) (bool, error) {
	value = strings.TrimSpace(value)
	switch name {
	case "pdfa":
		if _, ok := pdfaVersions[value]; !ok && value != "none" && value != "" {
			return true, refuse("pdfa : %q inconnu ; valeurs possibles : none, PDF/A-1b, PDF/A-2b, PDF/A-3b.", value)
		}
		if value != "" {
			o.pdfa, o.pdfaSet = value, true
		}
	case "accessible":
		o.accessible, o.accessibleSet = value == "true" || value == "1" || value == "on", true
	case "watermark":
		if len([]rune(value)) > 100 {
			return true, refuse("watermark : 100 caractères au plus.")
		}
		o.watermark.text, o.watermark.isSet = value, true
		if strings.EqualFold(value, "none") {
			o.watermark.text = ""
		}
	case "watermarkColor":
		if !hexColor.MatchString(value) {
			return true, refuse("watermarkColor : couleur au format #RRGGBB attendue.")
		}
		o.watermark.color = value
	case "watermarkSize", "watermarkAngle":
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 || n > 360 {
			return true, refuse("%s : nombre entre 0 et 360 attendu.", name)
		}
		if name == "watermarkSize" {
			o.watermark.size = n
		} else {
			o.watermark.angle = n
		}
	case "watermarkTiled":
		o.watermark.tiled = value == "true" || value == "1" || value == "on"
	case "dataCheck":
		if value != "off" && value != "warn" && value != "strict" {
			return true, refuse("dataCheck : off, warn ou strict.")
		}
		o.dataCheck = value
	case "passwordProtect", "pdfRestrictPassword":
		if len(value) > 128 {
			return true, refuse("%s : 128 caractères au plus.", name)
		}
		if name == "passwordProtect" {
			o.password = value
		} else {
			o.restrict.password = value
		}
	case "pdfRestrictPrinting":
		if value != "0" && value != "1" && value != "2" {
			return true, refuse("pdfRestrictPrinting : 0 (pas d'impression), 1 (basse résolution) ou 2 (pleine qualité).")
		}
		o.restrict.printing = value
	case "pdfRestrictEditing":
		if len(value) != 1 || value[0] < '0' || value[0] > '4' {
			return true, refuse("pdfRestrictEditing : de 0 (aucune modification) à 4 (tout sauf l'extraction de pages).")
		}
		o.restrict.editing = value
	case "pdfRestrictCopy":
		o.restrict.noCopy = yes(value, false)
	case "pdfRestrictAllowAccessibilty", "pdfRestrictAllowAccessibility":
		o.restrict.noAccessibility = yes(value, false)
	case "pdfSkipEmptyPages":
		o.skipEmptyPages = yes(value, false)
	case "devMode":
		o.devMode = yes(value, false)
	case "seal":
		o.seal = yes(value, false)
	case "title", "author", "subject", "keywords":
		if len([]rune(value)) > 500 {
			return true, refuse("%s : 500 caractères au plus.", name)
		}
		if o.meta == nil {
			o.meta = map[string]string{}
		}
		o.meta[name] = value
		if value == "" {
			delete(o.meta, name)
		}
	default:
		return false, nil
	}
	return true, nil
}

// apply takes the options of a JSON request (batch).
func (o *renderOptions) apply(in *RenderOptions) error {
	if in == nil {
		return nil
	}
	fields := map[string]string{}
	if in.Pdfa != nil {
		fields["pdfa"] = string(*in.Pdfa)
	}
	if in.Accessible != nil {
		fields["accessible"] = strconv.FormatBool(*in.Accessible)
	}
	if in.DataCheck != nil {
		fields["dataCheck"] = string(*in.DataCheck)
	}
	texts := map[string]*string{"passwordProtect": in.PasswordProtect, "pdfRestrictPassword": in.PdfRestrictPassword,
		"title": in.Title, "author": in.Author, "subject": in.Subject, "keywords": in.Keywords}
	for name, v := range texts {
		if v != nil {
			fields[name] = *v
		}
	}
	if in.PdfRestrictPrinting != nil {
		fields["pdfRestrictPrinting"] = strconv.Itoa(int(*in.PdfRestrictPrinting))
	}
	if in.PdfRestrictEditing != nil {
		fields["pdfRestrictEditing"] = strconv.Itoa(int(*in.PdfRestrictEditing))
	}
	flags := map[string]*bool{"pdfRestrictCopy": in.PdfRestrictCopy, "pdfRestrictAllowAccessibilty": in.PdfRestrictAllowAccessibilty,
		"pdfSkipEmptyPages": in.PdfSkipEmptyPages, "devMode": in.DevMode}
	for name, v := range flags {
		if v != nil {
			fields[name] = strconv.FormatBool(*v)
		}
	}
	if w := in.Watermark; w != nil {
		if w.Text != nil {
			fields["watermark"] = *w.Text
		}
		if w.Color != nil {
			fields["watermarkColor"] = *w.Color
		}
		if w.Size != nil {
			fields["watermarkSize"] = strconv.Itoa(*w.Size)
		}
		if w.Angle != nil {
			fields["watermarkAngle"] = strconv.Itoa(*w.Angle)
		}
		if w.Tiled != nil {
			fields["watermarkTiled"] = strconv.FormatBool(*w.Tiled)
		}
	}
	for name, value := range fields {
		if _, err := o.set(name, value); err != nil {
			return err
		}
	}
	return nil
}

// resolve settles a watermark asked with PDF/A or an accessible PDF:
// LibreOffice hangs on them (seen with 26.2), or breaks the next document. What the
// request asks wins over the settings; asking both is refused.
func (o *renderOptions) resolve() error {
	archive := o.pdfa != "none" && o.pdfa != ""
	switch {
	case o.restrict.asked() && o.restrict.password == "":
		return refuse("Les restrictions (pdfRestrictPrinting, pdfRestrictEditing, pdfRestrictCopy…) demandent un mot de passe pdfRestrictPassword.")
	case archive && (o.password != "" || o.restrict.password != ""):
		return refuse("Un PDF/A ne peut pas être protégé par un mot de passe : choisissez l'un ou l'autre.")
	case o.seal && (o.password != "" || o.restrict.password != ""):
		return refuse("Un PDF scellé (seal) ne peut pas être protégé par un mot de passe : choisissez l'un ou l'autre.")
	}
	if o.watermark.text == "" || !archive && !o.accessible {
		return nil
	}
	switch {
	case o.watermark.isSet && (archive && o.pdfaSet || o.accessible && o.accessibleSet):
		return refuse("Le filigrane ne se combine pas avec PDF/A ni avec un PDF accessible : choisissez l'un ou l'autre.")
	case o.watermark.isSet:
		o.pdfa, o.accessible = "none", false
	default:
		o.watermark.text = ""
	}
	return nil
}

// filter writes the LibreOffice PDF export options of these choices.
func (o renderOptions) filter(format string) string {
	if format != "pdf" {
		return ""
	}
	options := map[string]map[string]string{}
	add := func(name, kind, value string) { options[name] = map[string]string{"type": kind, "value": value} }
	if v, ok := pdfaVersions[o.pdfa]; ok {
		add("SelectPdfVersion", "long", v)
	}
	if o.accessible {
		add("UseTaggedPDF", "boolean", "true")
		add("PDFUACompliance", "boolean", "true")
	}
	if w := o.watermark; w.text != "" {
		if w.tiled {
			add("TiledWatermark", "string", w.text)
		} else {
			add("Watermark", "string", w.text)
		}
		if hexColor.MatchString(w.color) {
			rgb, _ := strconv.ParseInt(w.color[1:], 16, 32)
			add("WatermarkColor", "long", strconv.FormatInt(rgb, 10))
		}
		if w.size > 0 {
			add("WatermarkFontHeight", "long", strconv.Itoa(w.size))
		}
		add("WatermarkRotateAngle", "long", strconv.Itoa(w.angle*10))
	}
	if o.skipEmptyPages {
		add("IsSkipEmptyPages", "boolean", "true")
	}
	if len(options) == 0 {
		return ""
	}
	raw, _ := json.Marshal(options)
	return string(raw)
}

// fields are the options that differ from a plain document, as form fields:
// set again one by one, they produce the same document.
func (o renderOptions) fields() map[string]string {
	m := map[string]string{}
	if _, ok := pdfaVersions[o.pdfa]; ok {
		m["pdfa"] = o.pdfa
	}
	if o.accessible {
		m["accessible"] = "true"
	}
	if w := o.watermark; w.text != "" {
		m["watermark"] = w.text
		if hexColor.MatchString(w.color) {
			m["watermarkColor"] = w.color
		}
		m["watermarkSize"], m["watermarkAngle"] = strconv.Itoa(w.size), strconv.Itoa(w.angle)
		if w.tiled {
			m["watermarkTiled"] = "true"
		}
	}
	if o.dataCheck != "" {
		m["dataCheck"] = o.dataCheck
	}
	if o.skipEmptyPages {
		m["pdfSkipEmptyPages"] = "true"
	}
	if o.devMode {
		m["devMode"] = "true"
	}
	if o.seal {
		m["seal"] = "true"
	}
	for k, v := range o.meta {
		m[k] = v
	}
	return m
}

// describe writes the options as JSON for the history and the logs; empty
// for a plain document. A protected document only says so: its passwords
// are not kept, so producing it again gives it without protection.
func (o renderOptions) describe() string {
	m := o.fields()
	if o.password != "" || o.restrict.password != "" {
		m["protected"] = "true"
	}
	if len(m) == 0 {
		return ""
	}
	raw, _ := json.Marshal(m)
	return string(raw)
}

// cleanPath turns a template name into the path of the library: forward
// slashes, no leading slash, no way up.
func cleanPath(name string) (string, error) {
	p := strings.TrimSpace(strings.ReplaceAll(name, `\`, "/"))
	p = strings.TrimLeft(p, "/")
	if p == "" || len(p) > 300 || strings.ContainsAny(p, "\x00\r\n") {
		return "", refuse("Chemin de modèle invalide.")
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." || part == "." || part == "" {
			return "", refuse("Chemin de modèle invalide : %q.", name)
		}
	}
	return path.Clean(p), nil
}

var errNoTemplate = errors.New("no template")

// findTemplate returns the published version of a template of the library,
// also found without its extension, as Docmosis does, or by a former name.
func (s *Server) findTemplate(ctx context.Context, name string) (store.Template, store.TemplateVersion, error) {
	p, err := cleanPath(name)
	if err != nil {
		return store.Template{}, store.TemplateVersion{}, err
	}
	st := s.db.Load()
	candidates := []string{p}
	if path.Ext(p) == "" {
		for _, ext := range templateExts {
			candidates = append(candidates, p+ext)
		}
	}
	for _, c := range candidates {
		t, ok, err := st.TemplateByName(ctx, c)
		if err != nil {
			return t, store.TemplateVersion{}, err
		}
		if !ok {
			continue
		}
		if err := s.checkSpace(ctx, t.Path); err != nil {
			return t, store.TemplateVersion{}, err
		}
		if t.Published == 0 {
			return t, store.TemplateVersion{}, &tryError{http.StatusNotFound, fmt.Sprintf("Le modèle %q n'a pas de version publiée.", t.Path)}
		}
		v, ok, err := st.ServedVersion(ctx, t.ID, t.Published)
		if err == nil && !ok {
			err = &tryError{http.StatusNotFound, fmt.Sprintf("La version publiée de %q est introuvable.", t.Path)}
		}
		return t, v, err
	}
	return store.Template{}, store.TemplateVersion{}, &tryError{http.StatusNotFound, fmt.Sprintf("Modèle inconnu dans la bibliothèque : %q.", p)}
}
