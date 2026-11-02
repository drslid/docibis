package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"docibis/internal/render"
	"docibis/internal/store"
)

// Two versions of a template are produced with the same data and compared
// page by page (drawn at 72 dpi, the differences in red on the new one) and
// line by line (the text of the PDF).
const (
	compareDPI      = 72
	compareMaxPages = 20
	compareMaxLines = 400
)

func (s *Server) CompareTemplateVersions(ctx context.Context, req CompareTemplateVersionsRequestObject) (CompareTemplateVersionsResponseObject, error) {
	st := s.db.Load()
	t, ok, err := st.TemplateByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return CompareTemplateVersions404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Modèle inconnu."))}, nil
	}
	if req.Body == nil || req.Body.From < 1 || req.Body.To < 1 || req.Body.From > t.Versions || req.Body.To > t.Versions || req.Body.From == req.Body.To {
		return CompareTemplateVersions400ApplicationProblemPlusJSONResponse{badRequest(fmt.Sprintf("Choisissez deux versions différentes, de 1 à %d.", t.Versions))}, nil
	}
	out, err := s.compareVersions(ctx, t, req.Body.From, req.Body.To, req.Body.Data)
	if err != nil {
		switch status, pb := s.failure(err); status {
		case http.StatusServiceUnavailable:
			return CompareTemplateVersions503ApplicationProblemPlusJSONResponse{busy(pb)}, nil
		default:
			return CompareTemplateVersions400ApplicationProblemPlusJSONResponse{BadRequestApplicationProblemPlusJSONResponse(pb)}, nil
		}
	}
	return CompareTemplateVersions200JSONResponse(out), nil
}

func (s *Server) compareVersions(ctx context.Context, t store.Template, from, to int, data *map[string]any) (TemplateComparison, error) {
	st := s.db.Load()
	out := TemplateComparison{From: from, To: to, Pages: []ComparedPage{}, Text: []TextChange{}}
	a, ok, err := st.ServedVersion(ctx, t.ID, from)
	if err != nil || !ok {
		return out, errors.Join(err, refuse("Version %d introuvable.", from))
	}
	b, ok, err := st.ServedVersion(ctx, t.ID, to)
	if err != nil || !ok {
		return out, errors.Join(err, refuse("Version %d introuvable.", to))
	}
	raw := []byte(b.Sample)
	if data != nil {
		raw, _ = json.Marshal(*data)
	} else if len(raw) == 0 {
		raw = []byte("{}")
	}
	pdfA, pdfB := a.Content, b.Content
	if t.Kind == "template" {
		if pdfA, err = s.compareRender(ctx, t, a, raw); err != nil {
			return out, err
		}
		if pdfB, err = s.compareRender(ctx, t, b, raw); err != nil {
			return out, err
		}
	}
	dir, err := os.MkdirTemp("", "docibis-compare-")
	if err != nil {
		return out, err
	}
	defer os.RemoveAll(dir)
	pagesA, err := rasterizeAt(ctx, pdfA, dir, "from", compareDPI, compareMaxPages)
	if err != nil {
		return out, err
	}
	pagesB, err := rasterizeAt(ctx, pdfB, dir, "to", compareDPI, compareMaxPages)
	if err != nil {
		return out, err
	}
	out.Identical = len(pagesA) == len(pagesB)
	for i := range max(len(pagesA), len(pagesB)) {
		p := ComparedPage{Number: i + 1, Permille: -1}
		switch {
		case i < len(pagesA) && i < len(pagesB):
			permille, overlay := highlight(pagesA[i], pagesB[i])
			p.Permille = float32(math.Round(permille*100) / 100)
			p.Before, p.After = pngURL(pagesA[i]), pngURL(overlay)
			if p.Permille > 0 {
				out.Identical = false
			}
		case i < len(pagesA):
			p.Before = pngURL(pagesA[i])
		default:
			p.After = pngURL(pagesB[i])
		}
		out.Pages = append(out.Pages, p)
	}
	textA, errA := pdfText(ctx, pdfA, dir, "from")
	textB, errB := pdfText(ctx, pdfB, dir, "to")
	if errA == nil && errB == nil {
		out.Text = diffLines(textA, textB)
		for _, l := range out.Text {
			if l.Op != Same {
				out.Identical = false
				break
			}
		}
	}
	return out, nil
}

// compareRender produces a version with the data, without the options of
// the settings (watermark, PDF/A) that would hide the differences.
func (s *Server) compareRender(ctx context.Context, t store.Template, v store.TemplateVersion, data []byte) ([]byte, error) {
	f := tryForm{
		name: path.Base(t.Path), template: v.Content, prepared: v.Prepared, data: data, hasData: true, format: "pdf",
		opts: renderOptions{pdfa: "none", dataCheck: "off"},
	}
	doc, err := s.produceForm(ctx, f, "pdf")
	if err != nil {
		var te *tryError
		if errors.As(err, &te) && te.status < http.StatusInternalServerError {
			return nil, refuse("Version %d : %s", v.Number, te.msg)
		}
		if errors.Is(err, render.ErrBusy) || err == notReady {
			return nil, err
		}
		return nil, refuse("Version %d : LibreOffice n'a pas pu produire le document.", v.Number)
	}
	return doc.content, nil
}

// highlight compares two pages as the check against the reference does and
// returns the new page in light grey with what changed in color: red what
// appears, blue what went away, on a tinted zone around each change so that
// a small change shows on a small image.
func highlight(a, b *image.Gray) (float64, *image.RGBA) {
	w, h := max(a.Rect.Dx(), b.Rect.Dx()), max(a.Rect.Dy(), b.Rect.Dy())
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	if w == 0 || h == 0 {
		return 0, out
	}
	da, db := darkMask(a, w, h), darkMask(b, w, h)
	na, nb := dilate(da, w, h, checkRadius), dilate(db, w, h, checkRadius)
	added, removed := make([]bool, w*h), make([]bool, w*h)
	changed := 0
	for i := range da {
		added[i], removed[i] = db[i] && !na[i], da[i] && !nb[i]
		if added[i] || removed[i] {
			changed++
		}
	}
	zone := dilate(added, w, h, 6)
	gone := dilate(removed, w, h, 6)
	for y := range h {
		for x := range w {
			i := y*w + x
			grey := uint8(255)
			if x < b.Rect.Dx() && y < b.Rect.Dy() {
				grey = b.Pix[y*b.Stride+x]
			}
			// The page itself, lightened, so that the colors stand out.
			l := uint8(150 + int(grey)*105/255)
			c := color.RGBA{l, l, l, 255}
			switch {
			case added[i]:
				c = color.RGBA{220, 30, 30, 255}
			case removed[i]:
				c = color.RGBA{40, 110, 230, 255}
			case zone[i]:
				c = color.RGBA{l, uint8(int(l) * 82 / 100), uint8(int(l) * 82 / 100), 255}
			case gone[i]:
				c = color.RGBA{uint8(int(l) * 85 / 100), uint8(int(l) * 90 / 100), l, 255}
			}
			out.Pix[y*out.Stride+x*4], out.Pix[y*out.Stride+x*4+1], out.Pix[y*out.Stride+x*4+2], out.Pix[y*out.Stride+x*4+3] = c.R, c.G, c.B, c.A
		}
	}
	return float64(changed) * 1000 / float64(w*h), out
}

func pngURL(img image.Image) *string {
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, img); err != nil {
		return nil
	}
	u := "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
	return &u
}

// pdfText reads the lines of text of a PDF (pdftotext, poppler), without
// the empty ones.
func pdfText(ctx context.Context, pdf []byte, dir, name string) ([]string, error) {
	in := filepath.Join(dir, name+"-text.pdf")
	if err := os.WriteFile(in, pdf, 0o600); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	raw, err := exec.CommandContext(ctx, "pdftotext", "-enc", "UTF-8", "-l", fmt.Sprint(checkMaxPages), in, "-").Output()
	if err != nil {
		return nil, fmt.Errorf("pdftotext: %w", err)
	}
	var lines []string
	for _, l := range strings.Split(string(raw), "\n") {
		if l = strings.Join(strings.Fields(strings.ReplaceAll(l, "\f", " ")), " "); l != "" {
			lines = append(lines, l)
		}
	}
	return lines, nil
}

// diffLines lists the lines removed and added from a to b, with two lines
// of context around each change ("…" where lines are skipped).
func diffLines(a, b []string) []TextChange {
	prefix := 0
	for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(a)-prefix && suffix < len(b)-prefix && a[len(a)-1-suffix] == b[len(b)-1-suffix] {
		suffix++
	}
	midA, midB := a[prefix:len(a)-suffix], b[prefix:len(b)-suffix]
	var ops []TextChange
	for _, l := range a[:prefix] {
		ops = append(ops, TextChange{Op: Same, Line: l})
	}
	if len(midA)*len(midB) > 4_000_000 {
		// Too far apart to align: everything in the middle changed.
		for _, l := range midA {
			ops = append(ops, TextChange{Op: Removed, Line: l})
		}
		for _, l := range midB {
			ops = append(ops, TextChange{Op: Added, Line: l})
		}
	} else {
		ops = append(ops, lcsDiff(midA, midB)...)
	}
	for _, l := range a[len(a)-suffix:] {
		ops = append(ops, TextChange{Op: Same, Line: l})
	}
	// Keep the changes and two lines around them.
	keep := make([]bool, len(ops))
	for i, op := range ops {
		if op.Op != Same {
			for j := max(0, i-2); j <= min(len(ops)-1, i+2); j++ {
				keep[j] = true
			}
		}
	}
	out := []TextChange{}
	skipped := false
	for i, op := range ops {
		if !keep[i] {
			skipped = true
			continue
		}
		if skipped && len(out) > 0 {
			out = append(out, TextChange{Op: Same, Line: "…"})
		}
		skipped = false
		out = append(out, op)
		if len(out) >= compareMaxLines {
			out = append(out, TextChange{Op: Same, Line: "…"})
			break
		}
	}
	return out
}

// lcsDiff aligns two lists of lines on their longest common subsequence.
func lcsDiff(a, b []string) []TextChange {
	n, m := len(a), len(b)
	table := make([]int32, (n+1)*(m+1))
	at := func(i, j int) int32 { return table[i*(m+1)+j] }
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				table[i*(m+1)+j] = at(i+1, j+1) + 1
			} else {
				table[i*(m+1)+j] = max(at(i+1, j), at(i, j+1))
			}
		}
	}
	var out []TextChange
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			out = append(out, TextChange{Op: Same, Line: a[i]})
			i, j = i+1, j+1
		case at(i+1, j) >= at(i, j+1):
			out = append(out, TextChange{Op: Removed, Line: a[i]})
			i++
		default:
			out = append(out, TextChange{Op: Added, Line: b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, TextChange{Op: Removed, Line: a[i]})
	}
	for ; j < m; j++ {
		out = append(out, TextChange{Op: Added, Line: b[j]})
	}
	return out
}
