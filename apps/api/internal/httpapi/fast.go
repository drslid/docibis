package httpapi

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"docibis/internal/fast"
	"docibis/internal/merge"
	"docibis/internal/render"
	"docibis/internal/store"
	"docibis/internal/version"
)

// The fast engine draws the documents of a fixed layout without LibreOffice:
// the template is rendered once with every tag empty (the background), and
// the values are written over it with the fonts LibreOffice used. A version
// is qualified when the documents drawn this way do not differ, pixel-wise,
// from those of LibreOffice for its sample and reference data; otherwise
// LibreOffice keeps producing them.

const fastVersionKey = "fast.version"

// fastEnabled tells whether the qualified templates go through the fast
// engine (setting fast_engine).
func (s *Server) fastEnabled() bool {
	cfg := s.cfg.Load()
	return cfg != nil && cfg.String("fast_engine") == "true"
}

func fastStatus(raw string) *FastStatus {
	var f FastStatus
	if raw == "" || json.Unmarshal([]byte(raw), &f) != nil {
		return nil
	}
	return &f
}

// overlayCache keeps the overlays read from the database, by version.
type overlayCache struct {
	mu    sync.Mutex
	items map[int64]*fast.Overlay
}

func (s *Server) overlay(ctx context.Context, versionID int64) (*fast.Overlay, error) {
	s.overlays.mu.Lock()
	o, ok := s.overlays.items[versionID]
	s.overlays.mu.Unlock()
	if ok {
		return o, nil
	}
	raw, err := s.db.Load().FastOverlay(ctx, versionID)
	if err != nil || len(raw) == 0 {
		return nil, err
	}
	o = new(fast.Overlay)
	if err := gob.NewDecoder(bytes.NewReader(raw)).Decode(o); err != nil {
		return nil, err
	}
	s.overlays.mu.Lock()
	if s.overlays.items == nil || len(s.overlays.items) >= 64 {
		s.overlays.items = map[int64]*fast.Overlay{}
	}
	s.overlays.items[versionID] = o
	s.overlays.mu.Unlock()
	return o, nil
}

func (s *Server) forgetOverlay(versionID int64) {
	s.overlays.mu.Lock()
	delete(s.overlays.items, versionID)
	s.overlays.mu.Unlock()
}

// renderFast draws a document of the library with the fast engine; it
// reports false when LibreOffice has to produce it.
func (s *Server) renderFast(ctx context.Context, f tryForm, doc *document) bool {
	if doc.format != "pdf" || f.libVersionID == 0 || f.opts.filter("pdf") != "" || len(f.opts.meta) > 0 || f.opts.devMode || !s.fastEnabled() {
		return false
	}
	if st := fastStatus(f.fast); st == nil || st.State != FastStatusStateQualified {
		return false
	}
	o, err := s.overlay(ctx, f.libVersionID)
	if err != nil || o == nil {
		s.logger.Warn("fast engine not available", "template", doc.name, "error", err)
		return false
	}
	t := time.Now()
	values, err := merge.Values(o.Tags, f.data, t)
	if err == nil {
		doc.content, err = o.Render(values)
	}
	if err != nil {
		s.logger.Info("fast engine declined: LibreOffice produces the document", "request_id", requestID(ctx), "template", doc.name, "reason", err.Error())
		return false
	}
	doc.merge, doc.engine = time.Since(t), "overlay"
	return true
}

// qualify prepares the overlay of a version and compares what it draws with
// what LibreOffice produces. It returns the status and, when qualified, the
// overlay to keep.
func (s *Server) qualify(ctx context.Context, t store.Template, v store.TemplateVersion) (FastStatus, []byte, error) {
	status := FastStatus{State: FastStatusStateStandard, At: time.Now().UTC()}
	because := func(state FastStatusState, format string, args ...any) (FastStatus, []byte, error) {
		status.State, status.Reason = state, ptr(fmt.Sprintf(format, args...))
		return status, nil, nil
	}
	if t.Kind != "template" {
		return because(FastStatusStateStandard, "Un PDF fixe ne se remplit pas.")
	}
	if len(v.Prepared) == 0 {
		return because(FastStatusStateStandard, "Le modèle n'est pas encore lu.")
	}
	a, err := merge.Analyze(v.Prepared)
	switch {
	case err != nil:
		return because(FastStatusStateError, "Modèle illisible : %v", err)
	case a.Engine != "overlay":
		return because(FastStatusStateStandard, "Mise en page qui bouge avec les données (%s) : LibreOffice la produit.", a.Reason)
	case a.Images > 0:
		return because(FastStatusStateStandard, "Le modèle dessine des images ou des codes : pas encore pris en charge par le calque.")
	case a.Sections > 0:
		return because(FastStatusStateStandard, "Le modèle a des sections conditionnelles : pas encore prises en charge par le calque.")
	case a.Fields+a.Checkboxes == 0:
		return because(FastStatusStateStandard, "Le modèle n'a pas de champ à remplir.")
	}
	box := s.renderer.Load()
	if box == nil {
		return status, nil, notReady
	}
	dir, err := os.MkdirTemp("", "docibis-fast-")
	if err != nil {
		return status, nil, err
	}
	defer os.RemoveAll(dir)
	var tags []string
	var boxes []bool
	variant := func(name string, mark func(i int, tag string) string) ([]byte, error) {
		merged, err := merge.Fill(v.Prepared, []byte("{}"), merge.Options{Now: time.Now(), Mark: mark})
		if err != nil {
			return nil, err
		}
		in, out := filepath.Join(dir, name+".odt"), filepath.Join(dir, name+".pdf")
		if err := os.WriteFile(in, merged, 0o600); err != nil {
			return nil, err
		}
		if _, err := box.Convert(ctx, in, out, "pdf", ""); err != nil {
			return nil, err
		}
		return os.ReadFile(out)
	}
	var variants fast.Variants
	// Mark goes through the tags of a part from its end: each tag is kept at
	// its index.
	if variants.Background, err = variant("background", func(i int, tag string) string {
		for len(tags) <= i {
			tags, boxes = append(tags, ""), append(boxes, false)
		}
		tags[i], boxes[i] = tag, merge.Boxed(tag)
		if boxes[i] {
			return merge.Unchecked
		}
		return ""
	}); err != nil {
		return status, nil, err
	}
	if _, ok := fast.Marker(len(tags) - 1); !ok {
		return because(FastStatusStateStandard, "Trop de balises (%d) pour le calque.", len(tags))
	}
	// Each tag becomes its marker and a suffix; a box stays as it is drawn,
	// so that it moves nothing, and is ticked in the donor.
	marked := func(suffix, box string) func(i int, tag string) string {
		return func(i int, _ string) string {
			if boxes[i] {
				return box
			}
			m, _ := fast.Marker(i)
			return m + suffix
		}
	}
	for _, step := range []struct {
		name string
		out  *[]byte
		mark func(i int, tag string) string
	}{
		{"markers", &variants.Markers, marked("", merge.Unchecked)},
		{"dots", &variants.Dots, marked(".", merge.Unchecked)},
		{"long", &variants.Long, marked(fast.Long, merge.Unchecked)},
		// The donor lends the fonts of every character.
		{"donor", &variants.Donor, marked(fast.Charset(), merge.Checked)},
	} {
		if *step.out, err = variant(step.name, step.mark); err != nil {
			return status, nil, err
		}
	}
	if slices.Contains(boxes, true) {
		// Where each box is: its marker, the other tags empty.
		if variants.BoxMarks, err = variant("boxes", func(i int, _ string) string {
			if !boxes[i] {
				return ""
			}
			m, _ := fast.Marker(i)
			return m
		}); err != nil {
			return status, nil, err
		}
	}
	o, err := fast.Prepare(tags, boxes, variants)
	var unfit *fast.Unfit
	if errors.As(err, &unfit) {
		return because(FastStatusStateStandard, "%s", unfit.Reason)
	}
	if err != nil {
		return because(FastStatusStateError, "Calque non préparé : %v", err)
	}

	// The pixel check, with each set of data.
	threshold := float64(s.cfg.Load().Int("fast_engine_pixel_threshold"))
	worst, tests := 0.0, 0
	var refusals []string
	note := func(reason string) {
		if !slices.Contains(refusals, reason) {
			refusals = append(refusals, reason)
		}
	}
	var overlayTime, standardTime time.Duration
	datasets := append(qualificationData(v), fittedData(o, tags, boxes, v.Sample)...)
	for i, data := range datasets {
		values, err := merge.Values(tags, data, time.Now())
		if err != nil {
			note(err.Error())
			continue
		}
		start := time.Now()
		drawn, err := o.Render(values)
		took := time.Since(start)
		var r *fast.Refusal
		if errors.As(err, &r) {
			note(r.Reason)
			continue
		}
		if err != nil {
			return because(FastStatusStateError, "Rendu par le calque impossible : %v", err)
		}
		start = time.Now()
		doc, err := s.produceForm(ctx, tryForm{
			name: path.Base(t.Path), template: v.Content, prepared: v.Prepared, data: data, hasData: true, format: "pdf",
			opts: renderOptions{pdfa: "none", dataCheck: "off"},
		}, "pdf")
		if err != nil {
			if errors.Is(err, render.ErrBusy) || ctx.Err() != nil {
				return status, nil, err
			}
			continue
		}
		standardTime += time.Since(start)
		overlayTime += took
		want, err := rasterize(ctx, doc.content, dir, fmt.Sprintf("standard%d", i))
		if err != nil {
			return status, nil, err
		}
		got, err := rasterize(ctx, drawn, dir, fmt.Sprintf("overlay%d", i))
		if err != nil {
			return because(FastStatusStateError, "Le PDF du calque est illisible : %v", err)
		}
		if len(got) != len(want) {
			return because(FastStatusStateRefused, "Le calque donne %d pages, LibreOffice %d.", len(got), len(want))
		}
		for p := range got {
			worst = math.Max(worst, diffPermille(got[p], want[p]))
		}
		// The pixels forgive a shift of 2 pixels: a tick in the wrong box
		// or a value in the wrong cell would pass; the text would not.
		if where, differ := textDiffers(doc.content, drawn); differ {
			return because(FastStatusStateRefused, "Le texte du calque diffère de celui de LibreOffice (%s).", where)
		}
		tests++
	}
	status.Tests = ptr(tests)
	if tests == 0 {
		if len(refusals) == 0 {
			refusals = []string{"aucune donnée d'essai"}
		}
		return because(FastStatusStateRefused, "Aucun essai ne passe par le calque : %s.", strings.Join(refusals[:min(3, len(refusals))], " ; "))
	}
	status.Permille = ptr(float32(math.Round(worst*100) / 100))
	status.OverlayMs = ptr(float32(math.Round(float64(overlayTime.Microseconds())/float64(tests)) / 1000))
	status.StandardMs = ptr(ms(standardTime / time.Duration(tests)))
	if worst > threshold {
		return because(FastStatusStateRefused, "Écart de %.2f ‰ avec LibreOffice (toléré : %.0f ‰).", worst, threshold)
	}
	var blob bytes.Buffer
	if err := gob.NewEncoder(&blob).Encode(o); err != nil {
		return status, nil, err
	}
	status.State = FastStatusStateQualified
	return status, blob.Bytes(), nil
}

// qualificationData lists the data the overlay is compared with: the sample,
// the reference data, the sample with short texts (as a code or an amount,
// which fit narrow places), the same with each yes/no turned over, and the
// sample with accented texts.
func qualificationData(v store.TemplateVersion) [][]byte {
	out := [][]byte{}
	seen := map[string]bool{}
	add := func(data any) {
		raw, ok := data.(string)
		if !ok {
			b, _ := json.Marshal(data)
			raw = string(b)
		}
		var root map[string]any
		if raw == "" || seen[raw] || json.Unmarshal([]byte(raw), &root) != nil {
			return
		}
		seen[raw] = true
		out = append(out, []byte(raw))
	}
	add(v.Sample)
	add(v.ReferenceData)
	var root map[string]any
	if json.Unmarshal([]byte(v.Sample), &root) != nil {
		return out
	}
	short, changed := replaceSample(root, "Éa 12")
	if changed {
		add(short)
	}
	if flipped, changed := flip(short); changed {
		add(flipped)
	}
	if accented, changed := replaceSample(root, "Éléa Œuvré çà 12,50 €"); changed {
		add(accented)
	}
	return out
}

// replaceSample writes text in place of the sample texts ("Exemple …");
// dates, numbers and yes/no stay.
func replaceSample(v any, text string) (any, bool) {
	switch x := v.(type) {
	case string:
		if strings.HasPrefix(x, "Exemple ") {
			return text, true
		}
	case map[string]any:
		out, changed := map[string]any{}, false
		for k, e := range x {
			var c bool
			out[k], c = replaceSample(e, text)
			changed = changed || c
		}
		return out, changed
	case []any:
		out, changed := make([]any, len(x)), false
		for i, e := range x {
			var c bool
			out[i], c = replaceSample(e, text)
			changed = changed || c
		}
		return out, changed
	}
	return v, false
}

// textDiffers compares what two PDFs read, line by line; where tells the
// first difference.
func textDiffers(a, b []byte) (where string, differ bool) {
	ta, errA := fast.Text(a)
	tb, errB := fast.Text(b)
	if errA != nil || errB != nil {
		return "texte illisible", true
	}
	if ta == tb {
		return "", false
	}
	ra, rb := []rune(ta), []rune(tb)
	i := 0
	for i < len(ra) && i < len(rb) && ra[i] == rb[i] {
		i++
	}
	around := func(t []rune) string {
		return strings.NewReplacer("\n", " ", "\f", " ").Replace(string(t[max(0, i-15):min(len(t), i+15)]))
	}
	return fmt.Sprintf("LibreOffice « %s », calque « %s »", around(ra), around(rb)), true
}

var plainTag = regexp.MustCompile(`^[\pL_][\pL\pN_]*(\.[\pL_][\pL\pN_]*)*$`)

// fittedData writes in each sample text what fits the places of its field on
// the page, with accents and the euro sign, then the same with each yes/no
// turned over: the sample texts are often too long for a postal code or a
// key, and every place is tried this way.
func fittedData(o *fast.Overlay, tags []string, boxes []bool, sample string) [][]byte {
	var root map[string]any
	if json.Unmarshal([]byte(sample), &root) != nil {
		return nil
	}
	fit := map[string]string{}
	for i, tag := range tags {
		if boxes[i] || !plainTag.MatchString(tag) {
			continue
		}
		text := o.Fit(i, "Éa12 Œç3,50 €")
		if old, ok := fit[tag]; !ok || len([]rune(text)) < len([]rune(old)) {
			fit[tag] = text
		}
	}
	changed := false
	for path, text := range fit {
		changed = setSample(root, strings.Split(path, "."), text) || changed
	}
	if !changed {
		return nil
	}
	raw, _ := json.Marshal(root)
	out := [][]byte{raw}
	if flipped, changed := flip(root); changed {
		raw, _ := json.Marshal(flipped)
		out = append(out, raw)
	}
	return out
}

// setSample writes value at path when a sample text is there.
func setSample(m map[string]any, path []string, value string) bool {
	for i, key := range path {
		if i == len(path)-1 {
			if old, ok := m[key].(string); ok && strings.HasPrefix(old, "Exemple ") {
				m[key] = value
				return true
			}
			return false
		}
		next, ok := m[key].(map[string]any)
		if !ok {
			return false
		}
		m = next
	}
	return false
}

// flip turns over the yes/no values of data.
func flip(v any) (any, bool) {
	switch x := v.(type) {
	case bool:
		return !x, true
	case map[string]any:
		out, changed := map[string]any{}, false
		for k, e := range x {
			var c bool
			out[k], c = flip(e)
			changed = changed || c
		}
		return out, changed
	case []any:
		out, changed := make([]any, len(x)), false
		for i, e := range x {
			var c bool
			out[i], c = flip(e)
			changed = changed || c
		}
		return out, changed
	}
	return v, false
}

// qualifyVersion qualifies a version and keeps the result.
func (s *Server) qualifyVersion(ctx context.Context, t store.Template, v store.TemplateVersion) (FastStatus, error) {
	status, overlay, err := s.qualify(ctx, t, v)
	if err != nil {
		if errors.Is(err, notReady) || errors.Is(err, render.ErrBusy) || ctx.Err() != nil {
			return status, err
		}
		_, pb := s.failure(err)
		status.State, status.Reason = FastStatusStateError, ptr(trimmed(pb.Detail))
	}
	raw, _ := json.Marshal(status)
	if err := s.db.Load().SetFast(ctx, v.ID, string(raw), overlay); err != nil {
		return status, err
	}
	s.forgetOverlay(v.ID)
	reason := ""
	if status.Reason != nil {
		reason = *status.Reason
	}
	s.logger.Info("fast engine qualification", "template", t.Path, "version", v.Number, "state", status.State, "reason", reason,
		"permille", status.Permille, "tests", status.Tests)
	return status, nil
}

// qualifyPending qualifies the published versions not qualified yet.
func (s *Server) qualifyPending(ctx context.Context) {
	st := s.db.Load()
	if s.renderer.Load() == nil {
		return
	}
	pending, err := st.UnqualifiedVersions(ctx)
	if err != nil {
		s.logger.Warn("cannot list the templates to qualify", "error", err)
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
		if _, err := s.qualifyVersion(ctx, t, v); err != nil {
			s.logger.Warn("fast engine qualification stopped", "template", t.Path, "error", err)
			return
		}
		done++
	}
	if done > 0 {
		if err := st.Sync(ctx); err != nil {
			s.logger.Warn("database copy not updated", "error", err)
		}
	}
}

// resetFast forgets the qualifications made by another version of DocIbis:
// the background and the fonts may have changed with LibreOffice.
func (s *Server) resetFast(ctx context.Context, st *store.Store) {
	last, _, err := st.Setting(ctx, fastVersionKey)
	if err != nil || last == version.Version {
		return
	}
	if err := st.ResetFast(ctx); err != nil {
		s.logger.Warn("fast engine qualifications not reset", "error", err)
		return
	}
	if err := st.SetSetting(ctx, fastVersionKey, version.Version); err != nil {
		s.logger.Warn("cannot record the version qualified", "error", err)
	}
}

func (s *Server) QualifyTemplateVersion(ctx context.Context, req QualifyTemplateVersionRequestObject) (QualifyTemplateVersionResponseObject, error) {
	st := s.db.Load()
	notFound := QualifyTemplateVersion404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Version inconnue."))}
	t, ok, err := st.TemplateByID(ctx, req.Id)
	if err != nil || !ok {
		return notFound, err
	}
	v, ok, err := st.TemplateVersion(ctx, t.ID, req.Number)
	if err != nil || !ok {
		return notFound, err
	}
	status, err := s.qualifyVersion(ctx, t, v)
	if err != nil {
		if code, pb := s.failure(err); code == http.StatusServiceUnavailable {
			return QualifyTemplateVersion503ApplicationProblemPlusJSONResponse{busy(pb)}, nil
		}
		return nil, err
	}
	if err := st.Sync(ctx); err != nil {
		s.logger.Warn("database copy not updated", "error", err)
	}
	me, _ := userFrom(ctx)
	s.audit(ctx, me.Username, "template.fast", "template:"+t.Path, fmt.Sprintf("version %d : %s", v.Number, status.State))
	return QualifyTemplateVersion200JSONResponse(status), nil
}
