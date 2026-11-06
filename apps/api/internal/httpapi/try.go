package httpapi

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"mime/multipart"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v5"

	"docibis/internal/merge"
	"docibis/internal/render"
	"docibis/internal/store"
)

// Renderer converts files with the LibreOffice workers (render.Pool).
type Renderer interface {
	Convert(ctx context.Context, in, out, format, filter string) (time.Duration, error)
}

type rendererBox struct{ Renderer }

// SetRenderer opens the try routes once the workers are warm.
func (s *Server) SetRenderer(r Renderer) { s.renderer.Store(&rendererBox{r}) }

// limitBody bounds what the routes that receive files read: the template and
// the data at their configured maximum, plus room for the form itself.
func (s *Server) limitBody(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		r := c.Request()
		if cfg := s.cfg.Load(); cfg != nil {
			if mb := bodyLimit(cfg.Int("max_template_size_mb"), cfg.Int("max_data_size_mb"), r.Method, r.URL.Path); mb > 0 {
				r.Body = http.MaxBytesReader(c.Response(), r.Body, int64(mb)<<20)
			}
		}
		return next(c)
	}
}

// bodyLimit is the most a request may send, in MB; 0 leaves the default.
func bodyLimit(template, data int, method, p string) int {
	switch {
	case strings.HasPrefix(p, "/api/try/"), p == "/v1/render", p == "/v1/render/async", p == "/v1/render/batch":
		return template + data + 1
	case p == mcpPath:
		// The template travels in base64 inside JSON.
		return (template+data+1)*3/2 + 1
	case p == docmosisPath:
		return data + 1
	case p == "/api/templates/import", p == "/v1/templates/import":
		return maxImportMB + 1
	case p == "/api/fonts" && method == http.MethodPost:
		return maxFontMB + 1
	case strings.HasPrefix(p, "/v1/bundles/"), strings.HasPrefix(p, "/api/bundles"):
		return data + 1
	case strings.HasPrefix(p, "/api/templates") && method != http.MethodGet && method != http.MethodHead:
		return max(template, maxReferenceMB) + data + 1
	}
	return 0
}

// tryError is a refusal explained to the user, with its HTTP status.
type tryError struct {
	status int
	msg    string
}

func (e *tryError) Error() string { return e.msg }

func refuse(format string, args ...any) error {
	return &tryError{http.StatusBadRequest, fmt.Sprintf(format, args...)}
}

// failure turns an error of the try routes into a status and a problem.
func (s *Server) failure(err error) (int, Problem) {
	var te *tryError
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &te):
		return te.status, problem(te.status, te.msg)
	case errors.As(err, &tooBig):
		return http.StatusRequestEntityTooLarge, problem(http.StatusRequestEntityTooLarge, "La demande dépasse la taille permise par la configuration.")
	case errors.Is(err, render.ErrBusy):
		return http.StatusServiceUnavailable, problem(http.StatusServiceUnavailable, "Tous les workers LibreOffice sont occupés : réessayez dans un instant.")
	}
	s.logger.Warn("try refused", "error", err)
	return http.StatusBadRequest, problem(http.StatusBadRequest, "LibreOffice n'a pas pu traiter ce document.")
}

func busy(p Problem) BusyApplicationProblemPlusJSONResponse {
	wait := 2
	return BusyApplicationProblemPlusJSONResponse{Body: p, Headers: BusyResponseHeaders{RetryAfter: &wait}}
}

var notReady = &tryError{http.StatusServiceUnavailable, "Le moteur de rendu démarre : réessayez dans un instant."}

type tryForm struct {
	name     string
	template []byte
	data     []byte
	hasData  bool
	format   string
	// templateName names a template of the library instead of the file.
	templateName string
	outputName   string
	opts         renderOptions
	// fields and check come from the library, when the template is one of it,
	// with prepared, its conversion to OpenDocument.
	fields   []merge.Field
	check    string
	prepared []byte
	// libID and libVersion name the version of the library used.
	libID      int64
	libVersion int
	// libVersionID and fast tell its overlay for the fast engine, native and
	// nativeSHA its Typst copy for the native engine.
	libVersionID int64
	fast         string
	native       string
	nativeSHA    string
	// callbackURL and uploadURL serve the documents produced in the
	// background (/v1/render/async).
	callbackURL, uploadURL string
}

// readForm reads the template file and the JSON data of a try form.
func (s *Server) readForm(body *multipart.Reader) (tryForm, error) {
	cfg := s.cfg.Load()
	f := tryForm{opts: s.defaultOptions()}
	for {
		part, err := body.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				return f, err
			}
			return f, refuse("Formulaire illisible.")
		}
		switch part.FormName() {
		case "template":
			f.name = filepath.Base(part.FileName())
			f.template, err = readAtMost(part, cfg.Int("max_template_size_mb"), "Le modèle")
		case "data":
			f.data, err = readAtMost(part, cfg.Int("max_data_size_mb"), "Les données")
			f.hasData = true
		case "format":
			var b []byte
			b, err = io.ReadAll(io.LimitReader(part, 20))
			f.format = strings.ToLower(strings.TrimSpace(string(b)))
		case "callbackUrl", "uploadUrl":
			var b []byte
			if b, err = io.ReadAll(io.LimitReader(part, maxHookURL+1)); err == nil {
				err = f.setHook(part.FormName(), string(b))
			}
		default:
			var b []byte
			if b, err = io.ReadAll(io.LimitReader(part, 400)); err == nil {
				err = f.setField(part.FormName(), string(b))
			}
		}
		_ = part.Close()
		if err != nil {
			return f, err
		}
	}
	if len(f.template) == 0 && f.templateName == "" {
		return f, refuse("Choisissez un modèle Word (.docx) ou le nom d'un modèle de la bibliothèque (templateName).")
	}
	return f, nil
}

// setField reads the small fields of a form; unknown ones are ignored.
func (f *tryForm) setField(name, value string) error {
	switch name {
	case "templateName":
		f.templateName = strings.TrimSpace(value)
	case "outputName":
		f.outputName = filepath.Base(strings.TrimSpace(value))
	default:
		_, err := f.opts.set(name, value)
		return err
	}
	return nil
}

func readAtMost(r io.Reader, mb int, what string) ([]byte, error) {
	limit := int64(mb) << 20
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, &tryError{http.StatusRequestEntityTooLarge, fmt.Sprintf("%s dépasse %d Mo (réglage de la configuration).", what, mb)}
	}
	return b, nil
}

const maxUnpacked = 256 << 20

var externalRelation = regexp.MustCompile(`<Relationship\b[^>]*\bTargetMode="External"[^>]*>`)

// checkTemplate accepts a Word document or an OpenDocument text without
// macros, without links to outside files (LibreOffice would fetch them) and
// of reasonable size once unpacked. It returns "docx" or "odt".
func checkTemplate(name string, raw []byte) (string, error) {
	if strings.HasPrefix(name, "~$") {
		return "", refuse("Ce fichier est un fichier temporaire de Word (~$) : choisissez le vrai modèle.")
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return "", refuse("Ce fichier n'est pas un document Word (.docx) ni OpenDocument (.odt).")
	}
	var total uint64
	kind := ""
	for _, f := range zr.File {
		total += f.UncompressedSize64
		name := strings.ToLower(f.Name)
		switch {
		case name == "word/document.xml":
			kind = "docx"
		case name == "mimetype":
			if m, err := readZip(f, 100); err == nil && strings.HasPrefix(string(m), "application/vnd.oasis.opendocument.text") {
				kind = "odt"
			}
		case strings.HasSuffix(name, "vbaproject.bin") || strings.HasPrefix(name, "basic/") || strings.HasPrefix(name, "scripts/"):
			return "", refuse("Le modèle contient des macros : il est refusé.")
		}
	}
	if total > maxUnpacked {
		return "", &tryError{http.StatusRequestEntityTooLarge, "Le modèle dépasse 256 Mo une fois décompressé."}
	}
	if kind == "" {
		return "", refuse("Ce fichier n'est pas un document Word (.docx) ni OpenDocument (.odt).")
	}
	for _, f := range zr.File {
		if kind != "docx" || !strings.HasSuffix(f.Name, ".rels") {
			continue
		}
		rels, err := readZip(f, 10<<20)
		if err != nil {
			return "", refuse("Le modèle est abîmé (%s).", f.Name)
		}
		for _, rel := range externalRelation.FindAll(rels, -1) {
			// Links in the text and the Word template it came from are never opened.
			if !bytes.Contains(rel, []byte(`relationships/hyperlink"`)) && !bytes.Contains(rel, []byte(`relationships/attachedTemplate"`)) {
				return "", refuse("Le modèle dépend d'un fichier extérieur (image ou objet lié) : il est refusé.")
			}
		}
	}
	for _, f := range zr.File {
		name := strings.ToLower(f.Name)
		docxPart := kind == "docx" && strings.HasPrefix(name, "word/") && strings.HasSuffix(name, ".xml")
		odtPart := kind == "odt" && (name == "content.xml" || name == "styles.xml")
		if !docxPart && !odtPart {
			continue
		}
		part, err := readZip(f, maxUnpacked)
		if err != nil {
			return "", refuse("Le modèle est abîmé (%s).", f.Name)
		}
		if docxPart && readsOutside(part) || odtPart && linksOutside(part) {
			return "", refuse("Le modèle lit un fichier ou une adresse extérieure (image ou texte lié, champ INCLUDEPICTURE, INCLUDETEXT, LINK ou DDE) : il est refusé.")
		}
	}
	return kind, nil
}

var (
	fieldToken  = regexp.MustCompile(`<w:fldChar\b[^>]*\bw:fldCharType="(begin|separate|end)"|<w:instrText\b[^>]*>([^<]*)</w:instrText>|\bw:instr="([^"]*)"`)
	outsideCode = regexp.MustCompile(`(?i)^\s*(INCLUDEPICTURE|INCLUDETEXT|INCLUDE|LINK|DDEAUTO|DDE|IMPORT)\b`)
	odtLinked   = regexp.MustCompile(`<(?:draw:image|draw:object|draw:object-ole|draw:plugin|draw:applet|draw:floating-frame|text:section-source|table:table-source)\b[^>]*\bxlink:href="([^"]*)"`)
	odtDDE      = regexp.MustCompile(`<(?:office:dde-source|text:dde-connection-decl|table:dde-link|text:dde-connection)\b`)
)

// readsOutside tells a Word part with a field that reads a file or an
// address; the code of a field may be split over several runs.
func readsOutside(part []byte) bool {
	var codes []string
	for _, m := range fieldToken.FindAllSubmatch(part, -1) {
		switch {
		case string(m[1]) == "begin":
			codes = append(codes, "")
		case len(m[1]) > 0:
			if n := len(codes); n > 0 {
				if outsideCode.MatchString(codes[n-1]) {
					return true
				}
				if string(m[1]) == "end" {
					codes = codes[:n-1]
				}
			}
		case len(m[3]) > 0:
			if outsideCode.Match(m[3]) {
				return true
			}
		case len(codes) > 0:
			codes[len(codes)-1] += string(m[2])
		}
	}
	return false
}

// linksOutside tells an OpenDocument part that links an image, an object or
// a text outside the file, or reads another application (DDE).
func linksOutside(part []byte) bool {
	if odtDDE.Match(part) {
		return true
	}
	for _, m := range odtLinked.FindAllSubmatch(part, -1) {
		href := string(m[1])
		if strings.Contains(href, ":") || strings.HasPrefix(href, "/") || strings.HasPrefix(href, "../") || strings.HasPrefix(href, "\\") {
			return true
		}
	}
	return false
}

func readZip(f *zip.File, limit int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, limit))
}

// preparedCache keeps the last Word templates converted to ODT, by content,
// so that each new try does not convert them again.
type preparedCache struct {
	mu    sync.Mutex
	items map[string]*preparedODT
}

type preparedODT struct {
	odt  []byte
	used time.Time
}

const preparedMax = 64

func (c *preparedCache) get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.items[key]
	if ok {
		p.used = time.Now()
		return p.odt, true
	}
	return nil, false
}

func (c *preparedCache) put(key string, odt []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil {
		c.items = map[string]*preparedODT{}
	}
	if len(c.items) >= preparedMax {
		oldest := ""
		for k, p := range c.items {
			if oldest == "" || p.used.Before(c.items[oldest].used) {
				oldest = k
			}
		}
		delete(c.items, oldest)
	}
	c.items[key] = &preparedODT{odt: odt, used: time.Now()}
}

// prepare returns the template as an ODT, converting a Word file once.
func (s *Server) prepare(ctx context.Context, r Renderer, f tryForm, kind, dir string) ([]byte, time.Duration, error) {
	if kind == "odt" {
		return f.template, 0, nil
	}
	if len(f.prepared) > 0 {
		return f.prepared, 0, nil
	}
	sum := sha256.Sum256(f.template)
	key := hex.EncodeToString(sum[:])
	if odt, ok := s.prepared.get(key); ok {
		return odt, 0, nil
	}
	in, out := filepath.Join(dir, "template.docx"), filepath.Join(dir, "template.odt")
	if err := os.WriteFile(in, f.template, 0o600); err != nil {
		return nil, 0, err
	}
	start := time.Now()
	if _, err := r.Convert(ctx, in, out, "odt", ""); err != nil {
		return nil, 0, err
	}
	odt, err := os.ReadFile(out)
	if err != nil {
		return nil, 0, err
	}
	s.prepared.put(key, odt)
	return odt, time.Since(start), nil
}

// open checks the form and returns the template prepared as ODT, in a
// working folder that the caller removes.
func (s *Server) open(ctx context.Context, body *multipart.Reader) (tryForm, []byte, time.Duration, string, error) {
	if s.renderer.Load() == nil {
		return tryForm{}, nil, 0, "", notReady
	}
	f, err := s.readForm(body)
	if err != nil {
		return f, nil, 0, "", err
	}
	odt, prep, dir, err := s.openForm(ctx, &f)
	return f, odt, prep, dir, err
}

// openForm checks a template already read and returns it prepared as ODT.
func (s *Server) openForm(ctx context.Context, f *tryForm) ([]byte, time.Duration, string, error) {
	box := s.renderer.Load()
	if box == nil {
		return nil, 0, "", notReady
	}
	if err := s.resolve(ctx, f); err != nil {
		return nil, 0, "", err
	}
	kind, err := checkTemplate(f.name, f.template)
	if err != nil {
		return nil, 0, "", err
	}
	dir, err := os.MkdirTemp("", "docibis-try-")
	if err != nil {
		return nil, 0, "", err
	}
	odt, prep, err := s.prepare(ctx, box.Renderer, *f, kind, dir)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, 0, "", err
	}
	return odt, prep, dir, nil
}

// resolve loads the published version of a template named in the library.
func (s *Server) resolve(ctx context.Context, f *tryForm) error {
	if f.templateName == "" || len(f.template) > 0 {
		return nil
	}
	t, v, err := s.findTemplate(ctx, f.templateName)
	if err != nil {
		return err
	}
	if t.Kind == "pdf" && !bytes.Contains(v.Content, []byte("/AcroForm")) {
		return refuse("%q est un PDF fixe : il se joint aux documents (/v1/render/batch), il ne se remplit pas.", t.Path)
	}
	f.name, f.template, f.check, f.prepared = path.Base(t.Path), v.Content, t.DataCheck, v.Prepared
	f.libID, f.libVersion = t.ID, v.Number
	f.libVersionID, f.fast = v.ID, v.Fast
	f.native, f.nativeSHA = v.Native, v.NativeSHA256
	if v.Fields != "" {
		_ = json.Unmarshal([]byte(v.Fields), &f.fields)
	}
	return nil
}

func ms(d time.Duration) int { return int((d + 500*time.Microsecond) / time.Millisecond) }

// analyzeForm reads the tags of a template and proposes sample data.
func (s *Server) analyzeForm(ctx context.Context, f tryForm) (TryTemplate, error) {
	odt, prep, dir, err := s.openForm(ctx, &f)
	if err != nil {
		return TryTemplate{}, err
	}
	defer os.RemoveAll(dir)
	var sample map[string]any
	a, err := merge.Analyze(odt)
	if err == nil {
		sample, err = merge.Sample(odt)
	}
	if err != nil {
		return TryTemplate{}, refuse("Le modèle est illisible : %v", err)
	}
	out := TryTemplate{Name: f.name, Size: len(f.template), PrepareMs: ms(prep), Sample: sample, Analysis: TemplateAnalysis{
		Fields: a.Fields, FlowFields: a.FlowFields, Checkboxes: a.Checkboxes, Sections: a.Sections, BlockSections: a.BlockSections,
		Repeats: a.Repeats, Images: a.Images, Shapes: a.Shapes, Tables: a.Tables, Engine: TemplateAnalysisEngine(a.Engine), Reason: a.Reason,
	}}
	if len(a.Unsupported) > 0 {
		out.Analysis.Unsupported = &a.Unsupported
	}
	if issues, err := merge.Lint(odt, lintOptions()); err == nil {
		out.Lint = ptr(lintIssues(issues))
	}
	if fields, err := merge.Fields(odt); err == nil {
		out.Fields = ptr(fieldList(fields))
	}
	return out, nil
}

func (s *Server) TryAnalyze(ctx context.Context, req TryAnalyzeRequestObject) (TryAnalyzeResponseObject, error) {
	var out TryTemplate
	err := error(notReady)
	if s.renderer.Load() != nil {
		var f tryForm
		if f, err = s.readForm(req.Body); err == nil {
			out, err = s.analyzeForm(ctx, f)
		}
	}
	if err == nil {
		return TryAnalyze200JSONResponse(out), nil
	}
	switch status, p := s.failure(err); status {
	case http.StatusRequestEntityTooLarge:
		return TryAnalyze413ApplicationProblemPlusJSONResponse{TooLargeApplicationProblemPlusJSONResponse(p)}, nil
	case http.StatusServiceUnavailable:
		return TryAnalyze503ApplicationProblemPlusJSONResponse{busy(p)}, nil
	default:
		return TryAnalyze400ApplicationProblemPlusJSONResponse{BadRequestApplicationProblemPlusJSONResponse(p)}, nil
	}
}

func (s *Server) TryRender(ctx context.Context, req TryRenderRequestObject) (TryRenderResponseObject, error) {
	start := time.Now()
	doc, err := s.produce(ctx, req.Body, "")
	me, _ := userFrom(ctx)
	s.record(ctx, "console", me.Username, doc, err, time.Since(start))
	if err == nil {
		out := TryResult{
			Pdf: doc.content, Format: doc.format, Size: len(doc.content), Engine: TryResultEngine(doc.engineName()),
			PrepareMs: ms(doc.prepare), MergeMs: ms(doc.merge), RenderMs: ms(doc.render), TotalMs: ms(time.Since(start)),
		}
		return TryRender200JSONResponse(out), nil
	}
	switch status, p := s.failure(err); status {
	case http.StatusRequestEntityTooLarge:
		return TryRender413ApplicationProblemPlusJSONResponse{TooLargeApplicationProblemPlusJSONResponse(p)}, nil
	case http.StatusServiceUnavailable:
		return TryRender503ApplicationProblemPlusJSONResponse{busy(p)}, nil
	default:
		return TryRender400ApplicationProblemPlusJSONResponse{BadRequestApplicationProblemPlusJSONResponse(p)}, nil
	}
}

// document is a produced document and the time spent on each step.
type document struct {
	content                []byte
	name, format           string
	outputName             string
	prepare, merge, render time.Duration
	// missing lists the data the template expects but did not get (check warn).
	missing []string
	// What produced it, to log it and to produce it again: the data, the
	// template sent with the request, or the version of the library.
	data       []byte
	template   []byte
	libID      int64
	libVersion int
	opts       renderOptions
	// engine is "overlay" when the fast engine drew it, "native" when the
	// Typst copy of the template made it, else "standard".
	engine string
}

func (d document) engineName() string {
	if d.engine == "" {
		return "standard"
	}
	return d.engine
}

// fileName is the name to give the document.
func (d document) fileName() string {
	if d.outputName != "" {
		return d.outputName
	}
	name := strings.TrimSuffix(d.name, filepath.Ext(d.name))
	if name == "" || name == "." {
		name = "document"
	}
	return name + "." + d.format
}

// produce fills the template of a form with its data and converts the result:
// to format when given, otherwise to the format of the form (PDF by default).
func (s *Server) produce(ctx context.Context, body *multipart.Reader, format string) (document, error) {
	if s.renderer.Load() == nil {
		return document{format: format}, notReady
	}
	f, err := s.readForm(body)
	if err != nil {
		return document{name: f.name, format: format}, err
	}
	return s.produceForm(ctx, f, format)
}

// produceForm produces the document of a template and data already read;
// a PDF gets its passwords last, whatever the engine.
func (s *Server) produceForm(ctx context.Context, f tryForm, format string) (document, error) {
	doc, err := s.produceDocument(ctx, f, format)
	if err == nil && doc.format == "pdf" {
		doc.content, err = f.opts.protect(ctx, doc.content)
	}
	if err == nil && f.opts.seal {
		doc.content, err = s.sealDocument(doc)
	}
	return doc, err
}

func (s *Server) produceDocument(ctx context.Context, f tryForm, format string) (document, error) {
	// A PDF form is filled and a Typst template compiled, not merged.
	if err := s.resolve(ctx, &f); err == nil && isPDF(f.template) {
		return s.produceFormPDF(ctx, f, format)
	} else if err == nil && isTypst(f.name) {
		return s.produceTypst(ctx, f, format)
	}
	odt, prep, dir, err := s.openForm(ctx, &f)
	doc := document{name: f.name, format: format, outputName: f.outputName, libID: f.libID, libVersion: f.libVersion, opts: f.opts}
	if f.hasData {
		doc.data = f.data
	}
	if f.libID == 0 {
		doc.template = f.template
	}
	if doc.name == "" {
		doc.name = path.Base(f.templateName)
	}
	if err != nil {
		return doc, err
	}
	defer os.RemoveAll(dir)
	if doc.format == "" {
		doc.format = f.format
	}
	if doc.format == "" {
		doc.format = "pdf"
	}
	if doc.format, err = outputFormat(doc.format); err != nil {
		return doc, err
	}
	if err := f.opts.resolve(); err != nil {
		return doc, err
	}
	doc.opts = f.opts
	var root map[string]any
	if !f.hasData || json.Unmarshal(f.data, &root) != nil {
		return doc, refuse("Les données doivent être un objet JSON valide, par exemple { \"nom\": \"Dupont\" }.")
	}
	if odt, err = s.includeBlocks(ctx, odt, root); err != nil {
		return doc, err
	}
	if doc.missing, err = s.checkData(f, odt, root); err != nil {
		return doc, err
	}
	s.logger.Debug("document: template ready", "request_id", requestID(ctx), "template", doc.name, "template_bytes", len(f.template),
		"odt_bytes", len(odt), "prepare_ms", ms(prep), "data_bytes", len(f.data), "missing", len(doc.missing))
	if s.renderFast(ctx, f, &doc) || s.renderNative(ctx, f, &doc, dir) {
		doc.prepare = prep
		return doc, nil
	}
	t := time.Now()
	merged, err := merge.Fill(odt, f.data, merge.Options{Now: time.Now(), Fetch: s.imageFetcher(ctx), Meta: accessibleMeta(f.opts, odt, doc), Dev: f.opts.devMode})
	if err != nil {
		return doc, refuse("Fusion impossible : %v", err)
	}
	doc.prepare, doc.merge = prep, time.Since(t)
	s.logger.Debug("document: merged", "request_id", requestID(ctx), "template", doc.name, "merged_bytes", len(merged), "merge_ms", ms(doc.merge))
	in, out := filepath.Join(dir, "merged.odt"), filepath.Join(dir, "document."+doc.format)
	if err := os.WriteFile(in, merged, 0o600); err != nil {
		return doc, err
	}
	t = time.Now()
	filter := f.opts.filter(doc.format)
	if _, err := s.renderer.Load().Convert(ctx, in, out, doc.format, filter); err != nil {
		return doc, err
	}
	doc.render = time.Since(t)
	s.logger.Debug("document: converted", "request_id", requestID(ctx), "template", doc.name, "format", doc.format, "options", f.opts.describe(), "render_ms", ms(doc.render))
	if doc.content, err = os.ReadFile(out); err != nil || doc.format != "pdf" || !f.opts.accessible {
		return doc, err
	}
	if fixed, err := unmapStandardTypes(ctx, doc.content); err != nil {
		s.logger.Warn("accessible PDF: tags not checked", "request_id", requestID(ctx), "template", doc.name, "error", err)
	} else {
		doc.content = fixed
	}
	return doc, nil
}

// accessibleMeta gives an accessible PDF the title it needs: the one asked,
// that of the template, or else the name of the document.
func accessibleMeta(o renderOptions, odt []byte, doc document) map[string]string {
	if !o.accessible || doc.format != "pdf" || o.meta["title"] != "" || merge.Title(odt) != "" {
		return o.meta
	}
	meta := maps.Clone(o.meta)
	if meta == nil {
		meta = map[string]string{}
	}
	name := doc.fileName()
	meta["title"] = strings.TrimSuffix(name, filepath.Ext(name))
	return meta
}

// checkData compares the data with the fields the template reads: "strict"
// refuses a document with holes, "warn" (the default) only reports them.
func (s *Server) checkData(f tryForm, odt []byte, root map[string]any) ([]string, error) {
	mode := f.opts.dataCheck
	if mode == "" {
		mode = f.check
	}
	if mode == "off" {
		return nil, nil
	}
	fields := f.fields
	if fields == nil {
		var err error
		if fields, err = merge.Fields(odt); err != nil {
			return nil, nil
		}
	}
	missing := merge.Missing(fields, root)
	if len(missing) > 0 && mode == "strict" {
		return nil, refuse("Données manquantes pour ce modèle : %s.", strings.Join(missing, ", "))
	}
	return missing, nil
}

// record adds a production to the history, never its content: its data and
// template only when the setting keep_render_data allows it, to produce it
// again. It also writes the log line of the document.
func (s *Server) record(ctx context.Context, source, actor string, doc document, err error, took time.Duration) {
	status, detail := s.logDocument(ctx, source, actor, doc, err, took)
	st := s.db.Load()
	if st == nil || errors.Is(err, notReady) {
		return
	}
	r := &store.Render{Source: source, Actor: actor, Template: doc.name, Format: doc.format, Status: status, Error: detail,
		Size: int64(len(doc.content)), DurationMs: ms(took), Engine: doc.engineName(), RequestID: requestID(ctx),
		TemplateID: doc.libID, TemplateVersion: doc.libVersion, Options: doc.opts.describe()}
	if r.Format == "" {
		r.Format = "pdf"
	}
	ctx = context.WithoutCancel(ctx)
	if s.cfg.Load().String("keep_render_data") == "true" && len(doc.data) > 0 && (doc.libID != 0 || len(doc.template) > 0) {
		if r.Data, err = gzipped(doc.data); err != nil {
			s.logger.Warn("render data not kept", "error", err)
			r.Data = nil
		}
		if r.Data != nil && doc.libID == 0 {
			sum := sha256.Sum256(doc.template)
			r.TemplateSHA = hex.EncodeToString(sum[:])
			if err := st.SaveTemplateBlob(ctx, r.TemplateSHA, doc.template); err != nil {
				s.logger.Warn("render template not kept", "error", err)
				r.Data, r.TemplateSHA = nil, ""
			}
		}
	}
	if err := st.RecordRender(ctx, r); err != nil {
		s.logger.Warn("render not recorded in the history", "error", err)
	}
	if r.Status == "ok" {
		s.countDocument(ctx)
	}
	s.metrics.document(source, r.Status, took)
}

// logDocument writes one line per document, at the level of its outcome:
// who asked, which template and options, the time of each step and the
// cause of a failure. Never the data nor the document.
func (s *Server) logDocument(ctx context.Context, source, actor string, doc document, err error, took time.Duration) (status, detail string) {
	status, level := "ok", slog.LevelInfo
	if err != nil {
		_, p := s.failure(err)
		status, detail, level = "error", trimmed(p.Detail), slog.LevelError
		if refusal(err) {
			status, level = "refused", slog.LevelWarn
		}
	}
	format := doc.format
	if format == "" {
		format = "pdf"
	}
	attrs := []slog.Attr{
		slog.String("request_id", requestID(ctx)), slog.String("source", source), slog.String("actor", actor),
		slog.String("template", doc.name), slog.String("format", format), slog.String("status", status),
		slog.Int("size", len(doc.content)), slog.Int("data_bytes", len(doc.data)),
		slog.Int("prepare_ms", ms(doc.prepare)), slog.Int("merge_ms", ms(doc.merge)), slog.Int("render_ms", ms(doc.render)), slog.Int("total_ms", ms(took)),
	}
	if doc.libID != 0 {
		attrs = append(attrs, slog.Int64("template_id", doc.libID), slog.Int("template_version", doc.libVersion))
	} else if len(doc.template) > 0 {
		attrs = append(attrs, slog.Int("template_bytes", len(doc.template)))
	}
	if k, ok := keyFrom(ctx); ok {
		attrs = append(attrs, slog.String("key", k.Prefix))
	}
	if o := doc.opts.describe(); o != "" {
		attrs = append(attrs, slog.String("options", o))
	}
	if doc.outputName != "" {
		attrs = append(attrs, slog.String("output_name", doc.outputName))
	}
	if len(doc.missing) > 0 {
		attrs = append(attrs, slog.String("missing", shorten(strings.Join(doc.missing, ", "), 500)))
	}
	if err != nil {
		attrs = append(attrs, slog.String("error", detail))
		if cause := err.Error(); cause != detail {
			attrs = append(attrs, slog.String("cause", shorten(cause, 1000)))
		}
	}
	s.logger.LogAttrs(ctx, level, "document", attrs...)
	return status, detail
}

func gzipped(raw []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(raw); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func gunzipped(raw []byte, limit int64) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(io.LimitReader(r, limit))
}

// refusal tells a template or data refused (the caller's to fix) from a
// failure of the service (the engine, or all workers busy).
func refusal(err error) bool {
	var te *tryError
	var tooBig *http.MaxBytesError
	return errors.As(err, &tooBig) || errors.As(err, &te) && te.status < http.StatusInternalServerError
}
