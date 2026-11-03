package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"docibis/internal/merge"
	"docibis/internal/store"
)

// The library keeps the templates the applications name (templateName), as
// with Docmosis, with their versions, the data they read and the PDF Docmosis
// made with them (the reference).

const (
	maxReferenceMB = 50
	maxImportMB    = 300
	importPrefix   = "templates/"
)

func libraryTemplate(t store.Template) LibraryTemplate {
	out := LibraryTemplate{
		Id: t.ID, Path: t.Path, Kind: LibraryTemplateKind(t.Kind), Description: t.Description, Published: t.Published,
		DataCheck: LibraryTemplateDataCheck(t.DataCheck), Versions: t.Versions,
		CreatedAt: t.CreatedAt, CreatedBy: t.CreatedBy, UpdatedAt: t.UpdatedAt,
	}
	if t.Published > 0 {
		engine, has := t.Engine, t.HasReference
		out.Engine, out.HasReference, out.Check = &engine, &has, checkResult(t.CheckResult)
		out.Fast = fastStatus(t.Fast)
		out.Native = nativeStatus(t.Native, t.NativeSHA256, 0, "", nil)
	}
	if t.Kind == "template" {
		out.Lint = lintSummary(t.Lint)
	}
	if t.PreviousPublished > 0 && t.PreviousPublished != t.Published {
		out.PreviousPublished = ptr(t.PreviousPublished)
	}
	if t.ScheduledVersion > 0 && t.ScheduledAt != nil {
		out.Scheduled = &Scheduled{Version: t.ScheduledVersion, At: *t.ScheduledAt, By: ptr(t.ScheduledBy)}
	}
	return out
}

func checkResult(raw string) *CheckResult {
	var c CheckResult
	if raw == "" || json.Unmarshal([]byte(raw), &c) != nil {
		return nil
	}
	return &c
}

func libraryVersion(v store.TemplateVersion) LibraryVersion {
	return LibraryVersion{
		Number: v.Number, Size: v.Size, Sha256: v.SHA256, Engine: v.Engine, Note: v.Note, HasReference: v.HasReference,
		Check: checkResult(v.CheckResult), CreatedAt: v.CreatedAt, CreatedBy: v.CreatedBy, Lint: lintList(v.Lint),
		Fast: fastStatus(v.Fast), Native: nativeStatus(v.Native, v.NativeSHA256, len(v.NativeSource), v.NativeBy, v.NativeAt),
	}
}

func templateFields(raw string) []TemplateField {
	var fields []merge.Field
	_ = json.Unmarshal([]byte(raw), &fields)
	out := make([]TemplateField, 0, len(fields))
	for _, f := range fields {
		out = append(out, TemplateField{Path: f.Path, Kind: TemplateFieldKind(f.Kind), Optional: f.Optional})
	}
	return out
}

func sampleData(raw string) *map[string]any {
	var sample map[string]any
	if json.Unmarshal([]byte(raw), &sample) != nil || sample == nil {
		return nil
	}
	return &sample
}

// detail returns a template, its versions and the data its served version
// reads (the newest one while none is published).
func (s *Server) detail(ctx context.Context, id int64) (TemplateDetail, bool, error) {
	st := s.db.Load()
	t, ok, err := st.TemplateByID(ctx, id)
	if err != nil || !ok {
		return TemplateDetail{}, ok, err
	}
	versions, err := st.TemplateVersions(ctx, id)
	if err != nil {
		return TemplateDetail{}, false, err
	}
	out := TemplateDetail{Template: libraryTemplate(t), Versions: make([]LibraryVersion, 0, len(versions)), Fields: []TemplateField{}}
	for i, v := range versions {
		out.Versions = append(out.Versions, libraryVersion(v))
		if v.Number == t.Published || t.Published == 0 && i == 0 {
			out.Fields, out.Sample = templateFields(v.Fields), sampleData(v.Sample)
		}
	}
	return out, true, nil
}

// libraryForm is a form of the library: its files and its small fields.
type libraryForm struct {
	files  map[string][]byte
	names  map[string]string
	values map[string]string
}

// readLibraryForm reads a form; big lists the parts read whole, with their
// limit in MB. The other fields are cut at 4 KB.
func readLibraryForm(body *multipart.Reader, big map[string]int) (libraryForm, error) {
	f := libraryForm{files: map[string][]byte{}, names: map[string]string{}, values: map[string]string{}}
	for {
		part, err := body.NextPart()
		if errors.Is(err, io.EOF) {
			return f, nil
		}
		if err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				return f, err
			}
			return f, refuse("Formulaire illisible.")
		}
		name := part.FormName()
		if mb, ok := big[name]; ok {
			f.names[name] = filepath.Base(part.FileName())
			f.files[name], err = readAtMost(part, mb, "Le fichier")
		} else {
			var b []byte
			b, err = io.ReadAll(io.LimitReader(part, 4<<10))
			f.values[name] = strings.TrimSpace(string(b))
		}
		_ = part.Close()
		if err != nil {
			return f, err
		}
	}
}

// yes reads a boolean field of a form.
func yes(value string, otherwise bool) bool {
	switch strings.ToLower(value) {
	case "true", "1", "on", "y", "yes", "oui":
		return true
	case "false", "0", "off", "n", "no", "non":
		return false
	}
	return otherwise
}

// newVersion checks a file for the library: a Word or OpenDocument template,
// or a fixed PDF. A template is described at once when the engine runs;
// otherwise, and for bulk imports, it is described in the background.
func (s *Server) newVersion(ctx context.Context, p string, raw []byte, describe bool) (string, store.TemplateVersion, error) {
	sum := sha256.Sum256(raw)
	v := store.TemplateVersion{Content: raw, Size: int64(len(raw)), SHA256: hex.EncodeToString(sum[:])}
	if strings.EqualFold(path.Ext(p), ".pdf") {
		if !bytes.HasPrefix(raw, []byte("%PDF-")) {
			return "", v, refuse("%s n'est pas un PDF.", p)
		}
		v.Fields, v.Sample, v.Lint = "[]", "{}", "[]"
		return "pdf", v, nil
	}
	if _, err := checkTemplate(path.Base(p), raw); err != nil {
		return "", v, err
	}
	if describe && s.renderer.Load() != nil {
		if err := s.describe(ctx, &v, path.Base(p)); err != nil {
			return "", v, err
		}
	}
	return "template", v, nil
}

// describe reads the data a template expects, sample data, its engine and
// its checks.
func (s *Server) describe(ctx context.Context, v *store.TemplateVersion, name string) error {
	box := s.renderer.Load()
	if box == nil {
		return notReady
	}
	kind, err := checkTemplate(name, v.Content)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "docibis-library-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	odt, _, err := s.prepare(ctx, box.Renderer, tryForm{template: v.Content, prepared: v.Prepared}, kind, dir)
	if err != nil {
		return err
	}
	fields, err := merge.Fields(odt)
	if err != nil {
		return refuse("Le modèle est illisible : %v", err)
	}
	if fields == nil {
		fields = []merge.Field{}
	}
	sample, _ := merge.Sample(odt)
	a, _ := merge.Analyze(odt)
	f, _ := json.Marshal(fields)
	sm, _ := json.Marshal(sample)
	v.Fields, v.Sample, v.Engine, v.Prepared, v.Lint = string(f), string(sm), a.Engine, odt, lintTemplate(odt)
	return nil
}

// DescribeLater reads in the background the templates imported in bulk.
func (s *Server) describeLater(ctx context.Context) {
	s.describeQueued.Store(true)
	if !s.describing.CompareAndSwap(false, true) {
		return
	}
	go func() {
		for {
			s.describeQueued.Store(false)
			s.describePending(ctx)
			s.qualifyPending(ctx)
			s.checkNativePending(ctx)
			s.describing.Store(false)
			if !s.describeQueued.Load() || !s.describing.CompareAndSwap(false, true) {
				return
			}
		}
	}()
}

func (s *Server) describePending(ctx context.Context) {
	st := s.db.Load()
	pending, err := st.UndescribedVersions(ctx)
	if err != nil {
		s.logger.Warn("cannot list the templates to read", "error", err)
		return
	}
	done := 0
	for _, p := range pending {
		t, ok, err := st.TemplateByID(ctx, p.TemplateID)
		if err != nil || !ok {
			continue
		}
		v, ok, err := st.TemplateVersion(ctx, p.TemplateID, p.Number)
		if err != nil || !ok {
			continue
		}
		if err := s.describe(ctx, &v, path.Base(t.Path)); err != nil {
			if ctx.Err() != nil || errors.Is(err, notReady) {
				return
			}
			s.logger.Warn("template not readable", "template", t.Path, "version", v.Number, "error", err)
			_, pb := s.failure(err)
			issue, _ := json.Marshal([]merge.Issue{{Level: "error", Code: "unreadable", Message: trimmed(pb.Detail)}})
			v.Fields, v.Sample, v.Engine, v.Prepared, v.Lint = "[]", "{}", "", nil, string(issue)
		}
		if err := st.DescribeVersion(ctx, v.ID, v.Fields, v.Sample, v.Engine, v.Lint, v.Prepared); err != nil {
			s.logger.Warn("template description not saved", "template", t.Path, "error", err)
			return
		}
		done++
	}
	if done > 0 {
		if err := st.Sync(ctx); err != nil {
			s.logger.Warn("database copy not updated", "error", err)
		}
		s.logger.Info("library templates read", "templates", done)
	}
}

func (s *Server) ListTemplates(ctx context.Context, req ListTemplatesRequestObject) (ListTemplatesResponseObject, error) {
	q := ""
	if req.Params.Q != nil {
		q = strings.TrimSpace(*req.Params.Q)
	}
	rows, err := s.db.Load().Templates(ctx, q)
	if err != nil {
		return nil, err
	}
	out := make(ListTemplates200JSONResponse, 0, len(rows))
	for _, t := range rows {
		out = append(out, libraryTemplate(t))
	}
	return out, nil
}

func (s *Server) CreateTemplate(ctx context.Context, req CreateTemplateRequestObject) (CreateTemplateResponseObject, error) {
	cfg := s.cfg.Load()
	f, err := readLibraryForm(req.Body, map[string]int{"file": max(cfg.Int("max_template_size_mb"), maxReferenceMB)})
	if err == nil && len(f.files["file"]) == 0 {
		err = refuse("Choisissez le fichier du modèle (.docx, .odt) ou le PDF fixe.")
	}
	var p string
	if err == nil {
		p = f.values["path"]
		if p == "" {
			p = f.names["file"]
		}
		p, err = cleanPath(p)
	}
	if err == nil && !slices.Contains([]string{".docx", ".odt", ".pdf"}, strings.ToLower(path.Ext(p))) {
		err = refuse("Le chemin doit finir par .docx, .odt ou .pdf : %q.", p)
	}
	var t store.Template
	if err == nil {
		var exists bool
		if _, exists, err = s.db.Load().TemplateByPath(ctx, p); err != nil {
			return nil, err
		} else if exists {
			err = refuse("%q est déjà dans la bibliothèque : ajoutez-lui une version.", p)
		}
	}
	var v store.TemplateVersion
	if err == nil {
		t.Kind, v, err = s.newVersion(ctx, p, f.files["file"], true)
	}
	if err != nil {
		status, pb := s.failure(err)
		if status == http.StatusRequestEntityTooLarge {
			return CreateTemplate413ApplicationProblemPlusJSONResponse{TooLargeApplicationProblemPlusJSONResponse(pb)}, nil
		}
		return CreateTemplate400ApplicationProblemPlusJSONResponse{BadRequestApplicationProblemPlusJSONResponse(pb)}, nil
	}
	me, _ := userFrom(ctx)
	t.Path, t.Description, t.CreatedBy = p, trimmed(ptr(f.values["description"])), me.Username
	v.Note, v.CreatedBy = f.values["note"], me.Username
	if err := s.db.Load().AddTemplateVersion(ctx, &t, &v, yes(f.values["publish"], true)); err != nil {
		return nil, err
	}
	// Reads the template when it could not be read now, and qualifies it for
	// the fast engine.
	s.describeLater(context.WithoutCancel(ctx))
	s.audit(ctx, me.Username, "template.create", "template:"+p, fmt.Sprintf("version 1, %d octets", v.Size))
	out, _, err := s.detail(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	return CreateTemplate201JSONResponse(out), nil
}

func ptr[T any](v T) *T { return &v }

// valueOf reads an optional value, its zero when absent.
func valueOf[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func (s *Server) GetTemplate(ctx context.Context, req GetTemplateRequestObject) (GetTemplateResponseObject, error) {
	out, ok, err := s.detail(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return GetTemplate404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Modèle inconnu."))}, nil
	}
	return GetTemplate200JSONResponse(out), nil
}

func (s *Server) UpdateTemplate(ctx context.Context, req UpdateTemplateRequestObject) (UpdateTemplateResponseObject, error) {
	st := s.db.Load()
	t, ok, err := st.TemplateByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return UpdateTemplate404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Modèle inconnu."))}, nil
	}
	if req.Body == nil {
		return UpdateTemplate400ApplicationProblemPlusJSONResponse{badRequest("Rien à changer.")}, nil
	}
	if req.Body.Description != nil {
		t.Description = trimmed(req.Body.Description)
		if len([]rune(t.Description)) > 500 {
			return UpdateTemplate400ApplicationProblemPlusJSONResponse{badRequest("Description : 500 caractères au plus.")}, nil
		}
	}
	if req.Body.DataCheck != nil {
		t.DataCheck = string(*req.Body.DataCheck)
		if !slices.Contains([]string{"off", "warn", "strict"}, t.DataCheck) {
			return UpdateTemplate400ApplicationProblemPlusJSONResponse{badRequest("Contrôle des données : off, warn ou strict.")}, nil
		}
	}
	if err := st.UpdateTemplate(ctx, &t); err != nil {
		return nil, err
	}
	me, _ := userFrom(ctx)
	s.audit(ctx, me.Username, "template.update", "template:"+t.Path, "contrôle des données : "+t.DataCheck)
	out, _, err := s.detail(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	return UpdateTemplate200JSONResponse(out), nil
}

func (s *Server) DeleteTemplate(ctx context.Context, req DeleteTemplateRequestObject) (DeleteTemplateResponseObject, error) {
	st := s.db.Load()
	t, ok, err := st.TemplateByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return DeleteTemplate404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Modèle inconnu."))}, nil
	}
	if err := st.DeleteTemplate(ctx, t.ID); err != nil {
		return nil, err
	}
	me, _ := userFrom(ctx)
	s.audit(ctx, me.Username, "template.delete", "template:"+t.Path, fmt.Sprintf("%d versions", t.Versions))
	return DeleteTemplate204Response{}, nil
}

func (s *Server) AddTemplateVersion(ctx context.Context, req AddTemplateVersionRequestObject) (AddTemplateVersionResponseObject, error) {
	st := s.db.Load()
	t, ok, err := st.TemplateByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return AddTemplateVersion404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Modèle inconnu."))}, nil
	}
	cfg := s.cfg.Load()
	f, err := readLibraryForm(req.Body, map[string]int{"file": max(cfg.Int("max_template_size_mb"), maxReferenceMB)})
	if err == nil && len(f.files["file"]) == 0 {
		err = refuse("Choisissez le fichier de la nouvelle version.")
	}
	var v store.TemplateVersion
	if err == nil {
		var kind string
		if kind, v, err = s.newVersion(ctx, t.Path, f.files["file"], true); err == nil && kind != t.Kind {
			err = refuse("La nouvelle version doit être du même type que le modèle (%s).", t.Kind)
		}
	}
	if err != nil {
		status, pb := s.failure(err)
		if status == http.StatusRequestEntityTooLarge {
			return AddTemplateVersion413ApplicationProblemPlusJSONResponse{TooLargeApplicationProblemPlusJSONResponse(pb)}, nil
		}
		return AddTemplateVersion400ApplicationProblemPlusJSONResponse{BadRequestApplicationProblemPlusJSONResponse(pb)}, nil
	}
	me, _ := userFrom(ctx)
	v.Note, v.CreatedBy = f.values["note"], me.Username
	publish := yes(f.values["publish"], true)
	if err := st.AddTemplateVersion(ctx, &t, &v, publish); err != nil {
		return nil, err
	}
	s.describeLater(context.WithoutCancel(ctx))
	details := fmt.Sprintf("version %d, %d octets", v.Number, v.Size)
	if publish {
		details += ", publiée"
	}
	s.audit(ctx, me.Username, "template.version", "template:"+t.Path, details)
	out, _, err := s.detail(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	return AddTemplateVersion201JSONResponse(out), nil
}

func (s *Server) PublishTemplate(ctx context.Context, req PublishTemplateRequestObject) (PublishTemplateResponseObject, error) {
	st := s.db.Load()
	t, ok, err := st.TemplateByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return PublishTemplate404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Modèle inconnu."))}, nil
	}
	if req.Body == nil || req.Body.Version < 1 || req.Body.Version > t.Versions {
		return PublishTemplate400ApplicationProblemPlusJSONResponse{badRequest(fmt.Sprintf("Version à publier : de 1 à %d.", t.Versions))}, nil
	}
	if err := st.PublishTemplate(ctx, t.ID, req.Body.Version); err != nil {
		return nil, err
	}
	s.describeLater(context.WithoutCancel(ctx))
	me, _ := userFrom(ctx)
	s.audit(ctx, me.Username, "template.publish", "template:"+t.Path, fmt.Sprintf("version %d (avant : %d)", req.Body.Version, t.Published))
	out, _, err := s.detail(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	return PublishTemplate200JSONResponse(out), nil
}

func (s *Server) DownloadTemplateVersion(ctx context.Context, req DownloadTemplateVersionRequestObject) (DownloadTemplateVersionResponseObject, error) {
	st := s.db.Load()
	notFound := DownloadTemplateVersion404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Version inconnue."))}
	t, ok, err := st.TemplateByID(ctx, req.Id)
	if err != nil || !ok {
		return notFound, err
	}
	v, ok, err := st.TemplateVersion(ctx, t.ID, req.Number)
	if err != nil || !ok {
		return notFound, err
	}
	ext := path.Ext(t.Path)
	name := fmt.Sprintf("%s-v%d%s", strings.TrimSuffix(path.Base(t.Path), ext), v.Number, ext)
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": name})
	return DownloadTemplateVersion200ApplicationoctetStreamResponse{
		Body: bytes.NewReader(v.Content), ContentLength: int64(len(v.Content)),
		Headers: DownloadTemplateVersion200ResponseHeaders{ContentDisposition: &disposition},
	}, nil
}

// checkReference reads the reference PDF of a version and its data.
func checkReference(pdf []byte, data string) error {
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		return refuse("La référence doit être le PDF produit par Docmosis.")
	}
	var root map[string]any
	if json.Unmarshal([]byte(data), &root) != nil {
		return refuse("Les données de la référence doivent être un objet JSON.")
	}
	return nil
}

func (s *Server) SetTemplateReference(ctx context.Context, req SetTemplateReferenceRequestObject) (SetTemplateReferenceResponseObject, error) {
	st := s.db.Load()
	notFound := SetTemplateReference404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Version inconnue."))}
	t, ok, err := st.TemplateByID(ctx, req.Id)
	if err != nil || !ok {
		return notFound, err
	}
	v, ok, err := st.TemplateVersion(ctx, t.ID, req.Number)
	if err != nil || !ok {
		return notFound, err
	}
	if t.Kind != "template" {
		return SetTemplateReference400ApplicationProblemPlusJSONResponse{badRequest("Un PDF fixe n'a pas de référence.")}, nil
	}
	cfg := s.cfg.Load()
	f, err := readLibraryForm(req.Body, map[string]int{"reference": maxReferenceMB, "data": cfg.Int("max_data_size_mb")})
	if err == nil {
		err = checkReference(f.files["reference"], string(f.files["data"]))
	}
	if err != nil {
		status, pb := s.failure(err)
		if status == http.StatusRequestEntityTooLarge {
			return SetTemplateReference413ApplicationProblemPlusJSONResponse{TooLargeApplicationProblemPlusJSONResponse(pb)}, nil
		}
		return SetTemplateReference400ApplicationProblemPlusJSONResponse{BadRequestApplicationProblemPlusJSONResponse(pb)}, nil
	}
	if err := st.SetReference(ctx, v.ID, f.files["reference"], string(f.files["data"])); err != nil {
		return nil, err
	}
	s.forgetOverlay(v.ID)
	s.describeLater(context.WithoutCancel(ctx))
	me, _ := userFrom(ctx)
	s.audit(ctx, me.Username, "template.reference", "template:"+t.Path, fmt.Sprintf("version %d", v.Number))
	out, _, err := s.detail(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	return SetTemplateReference200JSONResponse(out), nil
}

func (s *Server) CheckTemplateVersion(ctx context.Context, req CheckTemplateVersionRequestObject) (CheckTemplateVersionResponseObject, error) {
	st := s.db.Load()
	t, ok, err := st.TemplateByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return CheckTemplateVersion404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Modèle inconnu."))}, nil
	}
	res, err := s.checkVersion(ctx, t, req.Number)
	if err != nil {
		switch status, pb := s.failure(err); status {
		case http.StatusNotFound:
			return CheckTemplateVersion404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(pb)}, nil
		case http.StatusServiceUnavailable:
			return CheckTemplateVersion503ApplicationProblemPlusJSONResponse{busy(pb)}, nil
		default:
			return CheckTemplateVersion400ApplicationProblemPlusJSONResponse{BadRequestApplicationProblemPlusJSONResponse(pb)}, nil
		}
	}
	me, _ := userFrom(ctx)
	s.audit(ctx, me.Username, "template.check", "template:"+t.Path, fmt.Sprintf("version %d : %s", req.Number, verdict(res)))
	return CheckTemplateVersion200JSONResponse(res), nil
}

func verdict(r CheckResult) string {
	switch {
	case r.Error != nil:
		return "erreur"
	case r.Ok:
		return "conforme"
	}
	return "écart"
}

func (s *Server) CheckAllTemplates(ctx context.Context, _ CheckAllTemplatesRequestObject) (CheckAllTemplatesResponseObject, error) {
	n, err := s.checkAll(context.WithoutCancel(ctx))
	if err != nil {
		return nil, err
	}
	me, _ := userFrom(ctx)
	s.audit(ctx, me.Username, "template.check_all", "", fmt.Sprintf("%d modèles", n))
	return CheckAllTemplates202JSONResponse{Templates: n}, nil
}

// importEntry is a template of an import with what goes with it. Its parts
// stay packed in the zip until the template is imported, one at a time: the
// zip may unpack to 1 GB, more than the memory of the task.
type importEntry struct {
	file, reference, data, native []byte
	packed                        map[*[]byte]*zip.File
}

// unpack reads the parts of the entry from the zip.
func (e *importEntry) unpack() error {
	for target, zf := range e.packed {
		var err error
		if *target, err = readZip(zf, maxUnpacked); err != nil {
			return refuse("Le zip est abîmé (%s).", zf.Name)
		}
	}
	return nil
}

func (s *Server) ImportTemplates(ctx context.Context, req ImportTemplatesRequestObject) (ImportTemplatesResponseObject, error) {
	f, err := readLibraryForm(req.Body, map[string]int{"file": maxImportMB})
	var entries map[string]*importEntry
	if err == nil {
		entries, err = readImport(f.files["file"])
	}
	if err != nil {
		status, pb := s.failure(err)
		if status == http.StatusRequestEntityTooLarge {
			return ImportTemplates413ApplicationProblemPlusJSONResponse{TooLargeApplicationProblemPlusJSONResponse(pb)}, nil
		}
		return ImportTemplates400ApplicationProblemPlusJSONResponse{BadRequestApplicationProblemPlusJSONResponse(pb)}, nil
	}
	me, _ := userFrom(ctx)
	report := s.importTemplates(ctx, entries, me.Username, yes(f.values["publish"], true))
	s.describeLater(context.WithoutCancel(ctx))
	s.audit(ctx, me.Username, "template.import", "", fmt.Sprintf("%d créés, %d mis à jour, %d inchangés, %d références, %d copies Typst, %d erreurs",
		report.Created, report.Updated, report.Unchanged, report.References, valueOf(report.Natives), len(report.Errors)))
	return ImportTemplates200JSONResponse(report), nil
}

// readImport reads a zip of templates: each X.docx (or .odt, .pdf) may come
// with X.reference.pdf, the PDF Docmosis made, and X.data.json, its data,
// and X.native.typ, its Typst copy for the native engine. A leading
// templates/ folder is dropped.
func readImport(raw []byte) (map[string]*importEntry, error) {
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, refuse("Le fichier n'est pas un zip.")
	}
	entries := map[string]*importEntry{}
	entry := func(p string) *importEntry {
		if entries[p] == nil {
			entries[p] = &importEntry{packed: map[*[]byte]*zip.File{}}
		}
		return entries[p]
	}
	var total uint64
	for _, zf := range zr.File {
		name := zf.Name
		base := path.Base(name)
		if zf.FileInfo().IsDir() || strings.HasPrefix(name, "__MACOSX/") || strings.HasPrefix(base, ".") || strings.HasPrefix(base, "~$") {
			continue
		}
		if total += zf.UncompressedSize64; total > maxUnpacked*4 {
			return nil, &tryError{http.StatusRequestEntityTooLarge, "Le zip dépasse 1 Go une fois décompressé."}
		}
		p, err := cleanPath(strings.TrimPrefix(name, importPrefix))
		if err != nil {
			return nil, err
		}
		lower := strings.ToLower(p)
		var e *importEntry
		var target *[]byte
		switch {
		case strings.HasSuffix(lower, ".reference.pdf"):
			e = entry(p[:len(p)-len(".reference.pdf")])
			target = &e.reference
		case strings.HasSuffix(lower, ".data.json"):
			e = entry(p[:len(p)-len(".data.json")])
			target = &e.data
		case strings.HasSuffix(lower, ".native.typ"):
			e = entry(p[:len(p)-len(".native.typ")])
			target = &e.native
		case strings.HasSuffix(lower, ".docx") || strings.HasSuffix(lower, ".odt") || strings.HasSuffix(lower, ".pdf"):
			e = entry(p)
			target = &e.file
		default:
			continue
		}
		if zf.UncompressedSize64 > 0 {
			e.packed[target] = zf
		}
	}
	if len(entries) == 0 {
		return nil, refuse("Le zip ne contient aucun modèle (.docx, .odt) ni PDF.")
	}
	return entries, nil
}

// importTemplates adds what changed: a new template, or a new version when
// the file differs from the latest one. References go to the version served.
func (s *Server) importTemplates(ctx context.Context, entries map[string]*importEntry, actor string, publish bool) ImportReport {
	report := ImportReport{Errors: []string{}}
	paths := make([]string, 0, len(entries))
	for p := range entries {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	for _, p := range paths {
		e := entries[p]
		if err := e.unpack(); err != nil {
			_, pb := s.failure(err)
			report.Errors = append(report.Errors, p+" : "+trimmed(pb.Detail))
			continue
		}
		s.importOne(ctx, &report, p, e, actor, publish)
		*e = importEntry{}
	}
	return report
}

// importOne imports a template of the zip and what goes with it.
func (s *Server) importOne(ctx context.Context, report *ImportReport, p string, e *importEntry, actor string, publish bool) {
	st := s.db.Load()
	t, exists, err := st.TemplateByPath(ctx, p)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("%s : %v", p, err))
		return
	}
	if len(e.file) == 0 && !exists {
		report.Errors = append(report.Errors, p+" : référence sans modèle.")
		return
	}
	if len(e.file) > 0 {
		changed, err := s.importFile(ctx, &t, exists, p, e.file, actor, publish)
		switch {
		case err != nil:
			_, pb := s.failure(err)
			report.Errors = append(report.Errors, p+" : "+trimmed(pb.Detail))
			return
		case !exists:
			report.Created++
		case changed:
			report.Updated++
		default:
			report.Unchanged++
		}
	}
	if len(e.reference) > 0 {
		if err := s.importReference(ctx, t, e); err != nil {
			_, pb := s.failure(err)
			report.Errors = append(report.Errors, p+" (référence) : "+trimmed(pb.Detail))
		} else {
			report.References++
		}
	}
	if len(e.native) > 0 {
		if changed, err := s.importNative(ctx, t, e.native, actor); err != nil {
			_, pb := s.failure(err)
			report.Errors = append(report.Errors, p+" (copie Typst) : "+trimmed(pb.Detail))
		} else if changed {
			report.Natives = ptr(valueOf(report.Natives) + 1)
		}
	}
}

func (s *Server) importFile(ctx context.Context, t *store.Template, exists bool, p string, raw []byte, actor string, publish bool) (bool, error) {
	st := s.db.Load()
	kind, v, err := s.newVersion(ctx, p, raw, false)
	if err != nil {
		return false, err
	}
	if exists {
		if kind != t.Kind {
			return false, refuse("le type change (%s → %s).", t.Kind, kind)
		}
		last, ok, err := st.TemplateVersion(ctx, t.ID, t.Versions)
		if err != nil {
			return false, err
		}
		if ok && last.SHA256 == v.SHA256 {
			if publish && t.Published != last.Number {
				return true, st.PublishTemplate(ctx, t.ID, last.Number)
			}
			return false, nil
		}
	} else {
		t.Path, t.Kind, t.CreatedBy = p, kind, actor
	}
	v.Note, v.CreatedBy = "import", actor
	if err := st.AddTemplateVersion(ctx, t, &v, publish); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Server) importReference(ctx context.Context, t store.Template, e *importEntry) error {
	st := s.db.Load()
	if t.Kind != "template" {
		return refuse("un PDF fixe n'a pas de référence.")
	}
	data := string(e.data)
	if data == "" {
		return refuse("X.reference.pdf doit venir avec X.data.json, les données utilisées.")
	}
	if err := checkReference(e.reference, data); err != nil {
		return err
	}
	number := t.Published
	if number == 0 {
		number = t.Versions
	}
	v, ok, err := st.TemplateVersion(ctx, t.ID, number)
	if err != nil {
		return err
	}
	if !ok {
		return refuse("version introuvable.")
	}
	if bytes.Equal(v.Reference, e.reference) && v.ReferenceData == data {
		return nil
	}
	return st.SetReference(ctx, v.ID, e.reference, data)
}

func (s *Server) ExportTemplates(ctx context.Context, _ ExportTemplatesRequestObject) (ExportTemplatesResponseObject, error) {
	st := s.db.Load()
	rows, err := st.Templates(ctx, "")
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name string, content []byte) error {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: importPrefix + name, Method: zip.Deflate, Modified: time.Now()})
		if err == nil {
			_, err = w.Write(content)
		}
		return err
	}
	for _, t := range rows {
		number := t.Published
		if number == 0 {
			number = t.Versions
		}
		v, ok, err := st.TemplateVersion(ctx, t.ID, number)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if err := add(t.Path, v.Content); err != nil {
			return nil, err
		}
		if len(v.Reference) > 0 {
			if err := add(t.Path+".reference.pdf", v.Reference); err != nil {
				return nil, err
			}
			if err := add(t.Path+".data.json", []byte(v.ReferenceData)); err != nil {
				return nil, err
			}
		}
		if v.NativeSource != "" {
			if err := add(t.Path+".native.typ", []byte(v.NativeSource)); err != nil {
				return nil, err
			}
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	me, _ := userFrom(ctx)
	s.audit(ctx, me.Username, "template.export", "", fmt.Sprintf("%d modèles", len(rows)))
	name := fmt.Sprintf("docibis-modeles-%s-%s.zip", strings.ToLower(s.cfg.Load().String("instance_name")), time.Now().Format("2006-01-02"))
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": name})
	return ExportTemplates200ApplicationzipResponse{
		Body: bytes.NewReader(buf.Bytes()), ContentLength: int64(buf.Len()),
		Headers: ExportTemplates200ResponseHeaders{ContentDisposition: &disposition},
	}, nil
}

// ListPublishedTemplates lists, for the applications, the templates served
// and the data each one reads.
func (s *Server) ListPublishedTemplates(ctx context.Context, req ListPublishedTemplatesRequestObject) (ListPublishedTemplatesResponseObject, error) {
	q := ""
	if req.Params.Q != nil {
		q = strings.TrimSpace(*req.Params.Q)
	}
	st := s.db.Load()
	rows, err := st.Templates(ctx, q)
	if err != nil {
		return nil, err
	}
	out := ListPublishedTemplates200JSONResponse{}
	for _, t := range rows {
		if t.Published == 0 {
			continue
		}
		c := TemplateContract{Name: t.Path, Kind: TemplateContractKind(t.Kind), Version: t.Published, Fields: []TemplateField{}}
		if t.Description != "" {
			c.Description = ptr(t.Description)
		}
		versions, err := st.TemplateVersions(ctx, t.ID)
		if err != nil {
			return nil, err
		}
		for _, v := range versions {
			if v.Number == t.Published {
				c.Fields, c.Sample = templateFields(v.Fields), sampleData(v.Sample)
			}
		}
		out = append(out, c)
	}
	return out, nil
}
