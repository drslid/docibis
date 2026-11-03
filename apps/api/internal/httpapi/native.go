package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"docibis/internal/render"
	"docibis/internal/store"
	"docibis/internal/version"
)

// The native engine produces a document from a Typst copy of its template,
// written by hand for one version of the Word file: no LibreOffice, about
// thirty times faster. The copy serves only once it gives, pixel for pixel
// and word for word, the document LibreOffice makes with the sample and
// reference data and their variants. A new version of the Word file starts
// without a copy: LibreOffice produces it until a new copy is attached and
// passes the same comparison.

const (
	nativeVersionKey = "native.version"
	maxNativeMB      = 1
)

// NativeCompiler makes PDFs from Typst sources (render.Pool of native.py).
type NativeCompiler interface {
	Compile(ctx context.Context, in, out string, inputs map[string]string) (time.Duration, error)
}

type nativeBox struct{ NativeCompiler }

// SetNative opens the native engine once its workers are warm.
func (s *Server) SetNative(c NativeCompiler) { s.native.Store(&nativeBox{c}) }

// nativeFiles keeps the copies on disk for the workers, each alone in a
// folder named after its hash: the only files it can read.
type nativeFiles struct {
	mu  sync.Mutex
	dir string
}

// nativeEnabled tells whether qualified copies produce the documents
// (setting native_engine) and the workers run.
func (s *Server) nativeEnabled() bool {
	cfg := s.cfg.Load()
	return cfg != nil && cfg.String("native_engine") == "true" && s.native.Load() != nil
}

// nativeStatus is what the console shows of the copy of a version: nil
// without one, pending until compared.
func nativeStatus(raw, sha string, size int, by string, at *time.Time) *NativeStatus {
	if sha == "" {
		return nil
	}
	n := NativeStatus{State: NativeStatusStatePending}
	if raw != "" && json.Unmarshal([]byte(raw), &n) != nil {
		n = NativeStatus{State: NativeStatusStateError, Reason: ptr("Résultat de la comparaison illisible.")}
	}
	n.SourceSha256, n.AttachedAt = sha, at
	if size > 0 {
		n.SourceBytes = ptr(size)
	}
	if by != "" {
		n.By = ptr(by)
	}
	return &n
}

func nativeQualified(raw string) bool {
	var n NativeStatus
	return raw != "" && json.Unmarshal([]byte(raw), &n) == nil && n.State == NativeStatusStateQualified
}

func hashText(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// nativeFile writes the copy of a version in its folder, once, and returns
// its path; source is read from the database when empty.
func (s *Server) nativeFile(ctx context.Context, versionID int64, sha, source string) (string, error) {
	if _, err := hex.DecodeString(sha); err != nil || len(sha) != 64 {
		return "", errors.New("hash of the Typst copy unreadable")
	}
	s.nativeFiles.mu.Lock()
	defer s.nativeFiles.mu.Unlock()
	if s.nativeFiles.dir == "" {
		dir, err := os.MkdirTemp("", "docibis-native-")
		if err != nil {
			return "", err
		}
		s.nativeFiles.dir = dir
	}
	file := filepath.Join(s.nativeFiles.dir, sha, "main.typ")
	if _, err := os.Stat(file); err == nil {
		return file, nil
	}
	if source == "" {
		var err error
		if source, err = s.db.Load().NativeSource(ctx, versionID); err != nil {
			return "", err
		}
	}
	if hashText(source) != sha {
		return "", errors.New("the Typst copy changed meanwhile")
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return "", err
	}
	return file, os.WriteFile(file, []byte(source), 0o600)
}

// nativeInputs are what a copy reads: the data, and the date of the
// document as the merge writes $nowUTC.
func nativeInputs(data []byte, now time.Time) map[string]string {
	utc := now.UTC()
	return map[string]string{"data": string(data), "today": utc.Format("2006-01-02"), "now": utc.Format(time.RFC3339)}
}

// nativeText tells whether every text of the data uses the characters the
// copies are compared with: Latin letters and their accents, digits, plain
// punctuation and the euro sign. Others (Greek, emoji, typographic quotes
// and dashes, non-breaking spaces…) go to LibreOffice: fonts and line
// breaks may differ around them.
func nativeText(data []byte) bool {
	var v any
	return json.Unmarshal(data, &v) == nil && nativeValue(v)
}

func nativeValue(v any) bool {
	switch x := v.(type) {
	case string:
		for _, r := range x {
			if !nativeRune(r) {
				return false
			}
		}
	case map[string]any:
		for _, e := range x {
			if !nativeValue(e) {
				return false
			}
		}
	case []any:
		for _, e := range x {
			if !nativeValue(e) {
				return false
			}
		}
	}
	return true
}

func nativeRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	case r >= 0xc0 && r <= 0x17f && r != 0xd7 && r != 0xf7:
		// Accented letters, Œ œ ß.
		return true
	}
	return strings.ContainsRune(" \n\t.,;:!?-/()@&+%€_#*='°", r)
}

// renderNative produces a document of the library from the Typst copy of its
// version; it reports false when LibreOffice has to produce it.
func (s *Server) renderNative(ctx context.Context, f tryForm, doc *document, dir string) bool {
	if doc.format != "pdf" || f.libVersionID == 0 || f.nativeSHA == "" || f.opts.filter("pdf") != "" || len(f.opts.meta) > 0 ||
		f.opts.devMode || !nativeQualified(f.native) || !s.nativeEnabled() {
		return false
	}
	declined := func(reason string) bool {
		s.logger.Info("native engine declined: LibreOffice produces the document", "request_id", requestID(ctx), "template", doc.name, "reason", reason)
		return false
	}
	if !nativeText(f.data) {
		return declined("characters outside those of the comparison")
	}
	file, err := s.nativeFile(ctx, f.libVersionID, f.nativeSHA, "")
	if err != nil {
		return declined(err.Error())
	}
	out := filepath.Join(dir, "native.pdf")
	t := time.Now()
	if _, err := s.native.Load().Compile(ctx, file, out, nativeInputs(f.data, t)); err != nil {
		return declined(err.Error())
	}
	content, err := os.ReadFile(out)
	if err != nil {
		return declined(err.Error())
	}
	doc.content, doc.render, doc.engine = content, time.Since(t), "native"
	return true
}

// nativeData lists the data a copy is compared with: those of the fast
// engine, then the sample and the reference data with every list five times
// longer (page breaks) and with long texts (line breaks in narrow cells).
func nativeData(v store.TemplateVersion) [][]byte {
	out := qualificationData(v)
	seen := map[string]bool{}
	for _, d := range out {
		seen[string(d)] = true
	}
	add := func(data any) {
		raw, _ := json.Marshal(data)
		if !seen[string(raw)] {
			seen[string(raw)] = true
			out = append(out, raw)
		}
	}
	for _, base := range []string{v.Sample, v.ReferenceData} {
		var root map[string]any
		if base == "" || json.Unmarshal([]byte(base), &root) != nil {
			continue
		}
		long := lengthenTexts(root)
		add(lengthenLists(root, 5))
		add(long)
		add(lengthenLists(long, 5))
	}
	return out
}

// longText is wider than most cells, with hyphens, accents and an
// apostrophe, as long names and addresses are.
const longText = "Éléa-Marie Dupont-Durand de la Fontaine-aux-Prés, résidence Les Œillets 12 bis, l'été"

// lengthenTexts makes every text of the data long, except those that look
// like numbers, amounts, dates or codes.
func lengthenTexts(v any) any {
	switch x := v.(type) {
	case string:
		if strings.ContainsAny(x, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") && !strings.ContainsAny(x, "/") && utf8.RuneCountInString(x) > 3 {
			if strings.Contains(x, "@") {
				return "prenom.nom-de-famille.tres-long@exemple-de-domaine-tres-long.fr"
			}
			return longText
		}
		return x
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			out[k] = lengthenTexts(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = lengthenTexts(e)
		}
		return out
	}
	return v
}

// lengthenLists repeats the items of every list n times.
func lengthenLists(v any, n int) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			out[k] = lengthenLists(e, n)
		}
		return out
	case []any:
		out := make([]any, 0, len(x)*n)
		for range n {
			for _, e := range x {
				out = append(out, lengthenLists(e, n))
			}
		}
		return out
	}
	return v
}

// checkNative compares the documents of the copy of a version with those of
// LibreOffice for each set of data: same pages, same text, pixels within
// the tolerance.
func (s *Server) checkNative(ctx context.Context, t store.Template, v store.TemplateVersion) (NativeStatus, error) {
	status := NativeStatus{State: NativeStatusStateRefused, At: ptr(time.Now().UTC())}
	because := func(state NativeStatusState, format string, args ...any) (NativeStatus, error) {
		status.State, status.Reason = state, ptr(fmt.Sprintf(format, args...))
		return status, nil
	}
	if t.Kind != "template" {
		return because(NativeStatusStateError, "Un PDF fixe ne se remplit pas.")
	}
	box := s.native.Load()
	if box == nil {
		return because(NativeStatusStateError, "Le moteur natif n'est pas démarré : vérifiez le paquet typst de l'image.")
	}
	if s.renderer.Load() == nil || len(v.Prepared) == 0 {
		return status, notReady
	}
	file, err := s.nativeFile(ctx, v.ID, v.NativeSHA256, v.NativeSource)
	if err != nil {
		return status, err
	}
	dir, err := os.MkdirTemp("", "docibis-native-check-")
	if err != nil {
		return status, err
	}
	defer os.RemoveAll(dir)
	threshold := float64(s.cfg.Load().Int("native_engine_pixel_threshold"))
	worst, tests, declined := 0.0, 0, 0
	var nativeTime, standardTime time.Duration
	for i, data := range nativeData(v) {
		if !nativeText(data) {
			continue
		}
		out := filepath.Join(dir, fmt.Sprintf("native%d.pdf", i))
		start := time.Now()
		_, err := box.Compile(ctx, file, out, nativeInputs(data, start))
		took := time.Since(start)
		if err != nil {
			if errors.Is(err, render.ErrBusy) || ctx.Err() != nil {
				return status, err
			}
			if declines(err) {
				// The copy sends these data to LibreOffice, as in production.
				declined++
				continue
			}
			return because(NativeStatusStateRefused, "La copie Typst ne se compile pas : %s", firstLine(err.Error()))
		}
		drawn, err := os.ReadFile(out)
		if err != nil {
			return status, err
		}
		start = time.Now()
		doc, err := s.produceForm(ctx, tryForm{
			name: path.Base(t.Path), template: v.Content, prepared: v.Prepared, data: data, hasData: true, format: "pdf",
			opts: renderOptions{pdfa: "none", dataCheck: "off"},
		}, "pdf")
		if err != nil {
			if errors.Is(err, render.ErrBusy) || ctx.Err() != nil {
				return status, err
			}
			// LibreOffice cannot produce it either: these data do not fit.
			continue
		}
		standardTime += time.Since(start)
		nativeTime += took
		want, err := rasterize(ctx, doc.content, dir, fmt.Sprintf("standard%d", i))
		if err != nil {
			return status, err
		}
		got, err := rasterize(ctx, drawn, dir, fmt.Sprintf("native%d", i))
		if err != nil {
			return because(NativeStatusStateError, "Le PDF de la copie est illisible : %v", err)
		}
		if len(got) != len(want) {
			return because(NativeStatusStateRefused, "La copie donne %d pages, LibreOffice %d (essai %d).", len(got), len(want), i+1)
		}
		for p := range got {
			worst = math.Max(worst, diffPermille(got[p], want[p]))
		}
		if where, differ, err := pdfTextDiffers(ctx, doc.content, drawn, dir, i); err != nil {
			return because(NativeStatusStateError, "Texte des PDF illisible : %v", err)
		} else if differ {
			return because(NativeStatusStateRefused, "Le texte de la copie diffère de celui de LibreOffice (essai %d : %s).", i+1, where)
		}
		tests++
	}
	status.Tests = ptr(tests)
	if tests == 0 {
		if declined > 0 {
			return because(NativeStatusStateRefused, "La copie renvoie tous les jeux de données à LibreOffice (docibis-decline).")
		}
		return because(NativeStatusStateRefused, "Aucune donnée d'essai à comparer : ajoutez des données de référence.")
	}
	if declined > 0 {
		status.Reason = ptr(fmt.Sprintf("%d jeu(x) de données renvoyé(s) à LibreOffice par la copie, comme en production.", declined))
	}
	status.Permille = ptr(float32(math.Round(worst*100) / 100))
	status.NativeMs = ptr(float32(math.Round(float64(nativeTime.Microseconds())/float64(tests)) / 1000))
	status.StandardMs = ptr(ms(standardTime / time.Duration(tests)))
	if worst > threshold {
		return because(NativeStatusStateRefused, "Écart de %.2f ‰ avec LibreOffice (toléré : %.0f ‰).", worst, threshold)
	}
	status.State = NativeStatusStateQualified
	return status, nil
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

// declines tells whether a copy sent the data back to LibreOffice on
// purpose: panic("docibis-decline: …") in the Typst source, for data it
// cannot lay out as LibreOffice does.
func declines(err error) bool {
	return strings.Contains(err.Error(), "docibis-decline")
}

// pdfTextDiffers compares the text of two PDFs read by position (PDFium),
// page by page, spaces aside: the PDFs of Typst and of LibreOffice are
// built differently, the text they show must be the same.
func pdfTextDiffers(ctx context.Context, standard, native []byte, dir string, i int) (where string, differ bool, err error) {
	read := func(pdf []byte, name string) ([]string, error) {
		file := filepath.Join(dir, fmt.Sprintf("%s%d-text.pdf", name, i))
		if err := os.WriteFile(file, pdf, 0o600); err != nil {
			return nil, err
		}
		out, err := pdfTools(ctx, "layout", fmt.Sprint(checkMaxPages), file).Output()
		if err != nil {
			return nil, fmt.Errorf("pdftools: %w", err)
		}
		pages := strings.Split(strings.TrimRight(string(out), "\f"), "\f")
		for p, text := range pages {
			pages[p] = strings.Join(strings.Fields(text), "")
		}
		return pages, nil
	}
	want, err := read(standard, "standard")
	if err != nil {
		return "", false, err
	}
	got, err := read(native, "native")
	if err != nil {
		return "", false, err
	}
	for p := range max(len(want), len(got)) {
		var a, b string
		if p < len(want) {
			a = want[p]
		}
		if p < len(got) {
			b = got[p]
		}
		if a == b {
			continue
		}
		ra, rb := []rune(a), []rune(b)
		c := 0
		for c < len(ra) && c < len(rb) && ra[c] == rb[c] {
			c++
		}
		around := func(t []rune) string { return string(t[max(0, c-15):min(len(t), c+15)]) }
		return fmt.Sprintf("page %d, LibreOffice « %s », copie « %s »", p+1, around(ra), around(rb)), true, nil
	}
	return "", false, nil
}

// checkNativeVersion compares the copy of a version and keeps the result.
func (s *Server) checkNativeVersion(ctx context.Context, t store.Template, v store.TemplateVersion) (NativeStatus, error) {
	status, err := s.checkNative(ctx, t, v)
	if err != nil {
		if errors.Is(err, notReady) || errors.Is(err, render.ErrBusy) || ctx.Err() != nil {
			return status, err
		}
		_, pb := s.failure(err)
		status.State, status.Reason = NativeStatusStateError, ptr(trimmed(pb.Detail))
	}
	raw, _ := json.Marshal(status)
	if err := s.db.Load().SetNative(ctx, v.ID, string(raw)); err != nil {
		return status, err
	}
	s.logger.Info("native engine comparison", "template", t.Path, "version", v.Number, "state", status.State, "reason", trimmed(status.Reason),
		"permille", status.Permille, "tests", status.Tests)
	out := nativeStatus(string(raw), v.NativeSHA256, len(v.NativeSource), v.NativeBy, v.NativeAt)
	return *out, nil
}

// checkNativePending compares the copies not compared yet.
func (s *Server) checkNativePending(ctx context.Context) {
	st := s.db.Load()
	if s.native.Load() == nil || s.renderer.Load() == nil {
		return
	}
	pending, err := st.UncheckedNativeVersions(ctx)
	if err != nil {
		s.logger.Warn("cannot list the Typst copies to compare", "error", err)
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
		if _, err := s.checkNativeVersion(ctx, t, v); err != nil {
			s.logger.Warn("native engine comparison stopped", "template", t.Path, "error", err)
			break
		}
		done++
	}
	if done > 0 {
		if err := st.Sync(ctx); err != nil {
			s.logger.Warn("database copy not updated", "error", err)
		}
	}
}

// resetNative compares every copy again after a new version of DocIbis:
// LibreOffice or the fonts may have changed.
func (s *Server) resetNative(ctx context.Context, st *store.Store) {
	last, _, err := st.Setting(ctx, nativeVersionKey)
	if err != nil || last == version.Version {
		return
	}
	if err := st.ResetNative(ctx); err != nil {
		s.logger.Warn("native engine comparisons not reset", "error", err)
		return
	}
	if err := st.SetSetting(ctx, nativeVersionKey, version.Version); err != nil {
		s.logger.Warn("cannot record the version compared", "error", err)
	}
}

// checkNativeSource refuses what cannot be a Typst copy DocIbis can run.
func checkNativeSource(raw []byte) error {
	switch {
	case len(raw) == 0:
		return refuse("La copie Typst est vide.")
	case !utf8.Valid(raw) || strings.ContainsRune(string(raw), 0):
		return refuse("La copie Typst doit être un fichier texte UTF-8 (.typ).")
	case strings.Contains(string(raw), "@preview/") || strings.Contains(string(raw), "@local/"):
		return refuse("La copie Typst ne peut pas importer de paquet (@preview, @local) : DocIbis la compile sans réseau ni autre fichier.")
	}
	return nil
}

// nativeVersion finds the version a route names.
func (s *Server) nativeVersion(ctx context.Context, id int64, number int) (store.Template, store.TemplateVersion, bool, error) {
	st := s.db.Load()
	t, ok, err := st.TemplateByID(ctx, id)
	if err != nil || !ok {
		return t, store.TemplateVersion{}, false, err
	}
	v, ok, err := st.TemplateVersion(ctx, t.ID, number)
	return t, v, ok, err
}

func (s *Server) GetTemplateNative(ctx context.Context, req GetTemplateNativeRequestObject) (GetTemplateNativeResponseObject, error) {
	_, v, ok, err := s.nativeVersion(ctx, req.Id, req.Number)
	if err != nil || !ok || v.NativeSource == "" {
		return GetTemplateNative404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Cette version n'a pas de copie Typst."))}, err
	}
	return GetTemplateNative200TextResponse(v.NativeSource), nil
}

func (s *Server) SetTemplateNative(ctx context.Context, req SetTemplateNativeRequestObject) (SetTemplateNativeResponseObject, error) {
	st := s.db.Load()
	t, v, ok, err := s.nativeVersion(ctx, req.Id, req.Number)
	if err != nil || !ok {
		return SetTemplateNative404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Version inconnue."))}, err
	}
	if t.Kind != "template" {
		return SetTemplateNative400ApplicationProblemPlusJSONResponse{badRequest("Un PDF fixe n'a pas de copie Typst.")}, nil
	}
	f, err := readLibraryForm(req.Body, map[string]int{"source": maxNativeMB})
	if err == nil {
		err = checkNativeSource(f.files["source"])
	}
	if err != nil {
		status, pb := s.failure(err)
		if status == http.StatusRequestEntityTooLarge {
			return SetTemplateNative413ApplicationProblemPlusJSONResponse{TooLargeApplicationProblemPlusJSONResponse(pb)}, nil
		}
		return SetTemplateNative400ApplicationProblemPlusJSONResponse{BadRequestApplicationProblemPlusJSONResponse(pb)}, nil
	}
	me, _ := userFrom(ctx)
	source := string(f.files["source"])
	sha := hashText(source)
	if err := st.SetNativeSource(ctx, v.ID, source, sha, me.Username); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	v.NativeSource, v.NativeSHA256, v.NativeBy, v.NativeAt, v.Native = source, sha, me.Username, &now, ""
	s.audit(ctx, me.Username, "template.native", "template:"+t.Path, fmt.Sprintf("version %d : copie Typst %s jointe", v.Number, sha[:12]))
	status, err := s.checkNativeVersion(ctx, t, v)
	if err != nil {
		if code, _ := s.failure(err); code == http.StatusServiceUnavailable {
			// Compared in the background once the engine is free.
			s.describeLater(context.WithoutCancel(ctx))
			return SetTemplateNative200JSONResponse(*nativeStatus("", sha, len(source), me.Username, &now)), nil
		}
		return nil, err
	}
	if err := st.Sync(ctx); err != nil {
		s.logger.Warn("database copy not updated", "error", err)
	}
	return SetTemplateNative200JSONResponse(status), nil
}

func (s *Server) CheckTemplateNative(ctx context.Context, req CheckTemplateNativeRequestObject) (CheckTemplateNativeResponseObject, error) {
	t, v, ok, err := s.nativeVersion(ctx, req.Id, req.Number)
	if err != nil || !ok || v.NativeSource == "" {
		return CheckTemplateNative404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Cette version n'a pas de copie Typst."))}, err
	}
	status, err := s.checkNativeVersion(ctx, t, v)
	if err != nil {
		if code, pb := s.failure(err); code == http.StatusServiceUnavailable {
			return CheckTemplateNative503ApplicationProblemPlusJSONResponse{busy(pb)}, nil
		}
		return nil, err
	}
	if err := s.db.Load().Sync(ctx); err != nil {
		s.logger.Warn("database copy not updated", "error", err)
	}
	me, _ := userFrom(ctx)
	s.audit(ctx, me.Username, "template.native", "template:"+t.Path, fmt.Sprintf("version %d : comparaison %s", v.Number, status.State))
	return CheckTemplateNative200JSONResponse(status), nil
}

func (s *Server) DeleteTemplateNative(ctx context.Context, req DeleteTemplateNativeRequestObject) (DeleteTemplateNativeResponseObject, error) {
	t, v, ok, err := s.nativeVersion(ctx, req.Id, req.Number)
	if err != nil || !ok || v.NativeSource == "" {
		return DeleteTemplateNative404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Cette version n'a pas de copie Typst."))}, err
	}
	if err := s.db.Load().SetNativeSource(ctx, v.ID, "", "", ""); err != nil {
		return nil, err
	}
	me, _ := userFrom(ctx)
	s.audit(ctx, me.Username, "template.native", "template:"+t.Path, fmt.Sprintf("version %d : copie Typst retirée", v.Number))
	return DeleteTemplateNative204Response{}, nil
}

// importNative attaches the copy of an imported template (X.docx.native.typ)
// to the version served; it is compared in the background.
func (s *Server) importNative(ctx context.Context, t store.Template, source []byte, actor string) (bool, error) {
	if t.Kind != "template" {
		return false, refuse("un PDF fixe n'a pas de copie Typst.")
	}
	if len(source) > maxNativeMB<<20 {
		return false, refuse("la copie Typst dépasse %d Mo.", maxNativeMB)
	}
	if err := checkNativeSource(source); err != nil {
		return false, err
	}
	st := s.db.Load()
	number := t.Published
	if number == 0 {
		number = t.Versions
	}
	v, ok, err := st.ServedVersion(ctx, t.ID, number)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, refuse("version introuvable.")
	}
	sha := hashText(string(source))
	if v.NativeSHA256 == sha {
		return false, nil
	}
	return true, st.SetNativeSource(ctx, v.ID, string(source), sha, actor)
}
