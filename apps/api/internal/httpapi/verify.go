package httpapi

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"docibis/internal/render"
	"docibis/internal/store"
	"docibis/internal/version"
)

// A version is compared with its reference, the PDF Docmosis made with the
// same data: both are drawn at 100 dpi, and a page differs by the share of
// dark pixels of one that have no dark pixel of the other within 2 pixels.
// The same text moved by a pixel, or drawn with another hinting, does not
// count; a missing word, a moved block or another page break does.
const (
	checkDPI        = 100
	checkRadius     = 2
	checkMaxPages   = 200
	checkVersionKey = "check.version"
)

// checkVersion produces a version with the data of its reference, compares
// both and keeps the result.
func (s *Server) checkVersion(ctx context.Context, t store.Template, number int) (CheckResult, error) {
	st := s.db.Load()
	v, ok, err := st.TemplateVersion(ctx, t.ID, number)
	if err != nil {
		return CheckResult{}, err
	}
	if !ok {
		return CheckResult{}, &tryError{http.StatusNotFound, "Version inconnue."}
	}
	if len(v.Reference) == 0 {
		return CheckResult{}, refuse("Cette version n'a pas de PDF de référence : ajoutez celui produit par Docmosis.")
	}
	res := CheckResult{At: time.Now().UTC(), Version: version.Version, Threshold: float32(s.cfg.Load().Int("reference_threshold_permille"))}
	pages, refPages, permille, err := s.compare(ctx, t, v)
	switch {
	case err == nil:
		res.Pages, res.ReferencePages, res.Permille = pages, &refPages, &permille
		res.Ok = pages == refPages && (len(permille) == 0 || slices.Max(permille) <= res.Threshold)
	case err == notReady || errors.Is(err, render.ErrBusy) || ctx.Err() != nil:
		return res, err
	default:
		_, p := s.failure(err)
		res.Error = p.Detail
	}
	raw, _ := json.Marshal(res)
	if err := st.SetCheckResult(ctx, v.ID, string(raw)); err != nil {
		return res, err
	}
	return res, nil
}

func (s *Server) compare(ctx context.Context, t store.Template, v store.TemplateVersion) (pages, refPages int, permille []float32, err error) {
	f := tryForm{
		name: path.Base(t.Path), template: v.Content, data: []byte(v.ReferenceData), hasData: true, format: "pdf",
		opts: renderOptions{pdfa: "none", dataCheck: "off"},
	}
	doc, err := s.produceForm(ctx, f, "pdf")
	if err != nil {
		return 0, 0, nil, err
	}
	dir, err := os.MkdirTemp("", "docibis-check-")
	if err != nil {
		return 0, 0, nil, err
	}
	defer os.RemoveAll(dir)
	got, err := rasterize(ctx, doc.content, dir, "docibis")
	if err != nil {
		return 0, 0, nil, err
	}
	want, err := rasterize(ctx, v.Reference, dir, "reference")
	if err != nil {
		return 0, 0, nil, refuse("Le PDF de référence est illisible.")
	}
	permille = make([]float32, min(len(got), len(want)))
	for i := range permille {
		permille[i] = float32(math.Round(diffPermille(got[i], want[i])*100) / 100)
	}
	return len(got), len(want), permille, nil
}

// rasterize draws the pages of a PDF in grey with PDFium (pdftools.py).
func rasterize(ctx context.Context, pdf []byte, dir, name string) ([]*image.Gray, error) {
	return rasterizeAt(ctx, pdf, dir, name, checkDPI, checkMaxPages)
}

// pdfTools runs pdftools.py, which draws and reads PDFs with PDFium (the PDF
// engine of Chrome): DOCIBIS_PDFTOOLS when set, otherwise the one of the
// image, run by the Python bundled with LibreOffice.
func pdfTools(ctx context.Context, args ...string) *exec.Cmd {
	command := strings.Fields(os.Getenv("DOCIBIS_PDFTOOLS"))
	if len(command) == 0 {
		program := cmp.Or(os.Getenv("LO_PATH"), "/opt/libreoffice26.8/program")
		command = []string{filepath.Join(program, "python"), "/usr/local/lib/docibis/pdftools.py"}
	}
	cmd := exec.CommandContext(ctx, command[0], append(command[1:], args...)...)
	if os.Getenv("PYTHONPATH") == "" {
		cmd.Env = append(os.Environ(), "PYTHONPATH=/usr/local/lib/docibis/python")
	}
	return cmd
}

func rasterizeAt(ctx context.Context, pdf []byte, dir, name string, dpi, most int) ([]*image.Gray, error) {
	in := filepath.Join(dir, name+".pdf")
	if err := os.WriteFile(in, pdf, 0o600); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := pdfTools(ctx, "raster", strconv.Itoa(dpi), strconv.Itoa(most), in, filepath.Join(dir, name))
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("pdftools: %w: %s", err, bytes.TrimSpace(out))
	}
	files, err := filepath.Glob(filepath.Join(dir, name+"-*.png"))
	if err != nil {
		return nil, err
	}
	number := func(file string) int {
		n, _ := strconv.Atoi(strings.TrimSuffix(file[strings.LastIndexByte(file, '-')+1:], ".png"))
		return n
	}
	slices.SortFunc(files, func(a, b string) int { return number(a) - number(b) })
	pages := make([]*image.Gray, 0, len(files))
	for _, file := range files {
		img, err := readPNG(file)
		if err != nil {
			return nil, err
		}
		pages = append(pages, img)
		_ = os.Remove(file)
	}
	return pages, nil
}

func readPNG(file string) (*image.Gray, error) {
	fh, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	img, err := png.Decode(fh)
	if err != nil {
		return nil, err
	}
	if g, ok := img.(*image.Gray); ok {
		return g, nil
	}
	g := image.NewGray(img.Bounds())
	draw.Draw(g, g.Bounds(), img, img.Bounds().Min, draw.Src)
	return g, nil
}

// diffPermille is the share of the page, in thousandths, of the dark pixels
// of each image with no dark pixel of the other within checkRadius. What one
// page has beyond the size of the other counts too.
func diffPermille(a, b *image.Gray) float64 {
	w, h := max(a.Rect.Dx(), b.Rect.Dx()), max(a.Rect.Dy(), b.Rect.Dy())
	if w == 0 || h == 0 {
		return 0
	}
	da, db := darkMask(a, w, h), darkMask(b, w, h)
	na, nb := dilate(da, w, h, checkRadius), dilate(db, w, h, checkRadius)
	changed := 0
	for i := range da {
		if da[i] && !nb[i] || db[i] && !na[i] {
			changed++
		}
	}
	return float64(changed) * 1000 / float64(w*h)
}

// darkMask marks the dark pixels of an image on a w×h canvas.
func darkMask(img *image.Gray, w, h int) []bool {
	m := make([]bool, w*h)
	for y := 0; y < img.Rect.Dy(); y++ {
		row := img.Pix[y*img.Stride : y*img.Stride+img.Rect.Dx()]
		for x, p := range row {
			m[y*w+x] = p < 128
		}
	}
	return m
}

// dilate widens the marked pixels by r in each direction (a square).
func dilate(m []bool, w, h, r int) []bool {
	tmp, out := make([]bool, len(m)), make([]bool, len(m))
	for y := 0; y < h; y++ {
		count := 0
		for x := 0; x < min(r, w); x++ {
			if m[y*w+x] {
				count++
			}
		}
		for x := 0; x < w; x++ {
			if x+r < w && m[y*w+x+r] {
				count++
			}
			if x-r-1 >= 0 && m[y*w+x-r-1] {
				count--
			}
			tmp[y*w+x] = count > 0
		}
	}
	for x := 0; x < w; x++ {
		count := 0
		for y := 0; y < min(r, h); y++ {
			if tmp[y*w+x] {
				count++
			}
		}
		for y := 0; y < h; y++ {
			if y+r < h && tmp[(y+r)*w+x] {
				count++
			}
			if y-r-1 >= 0 && tmp[(y-r-1)*w+x] {
				count--
			}
			out[y*w+x] = count > 0
		}
	}
	return out
}

// checkAll compares again, in the background, every published version that
// has a reference. It returns how many there are.
func (s *Server) checkAll(ctx context.Context) (int, error) {
	st := s.db.Load()
	versions, err := st.ReferenceVersions(ctx)
	if err != nil {
		return 0, err
	}
	if len(versions) == 0 || !s.checking.CompareAndSwap(false, true) {
		return len(versions), nil
	}
	go func() {
		defer s.checking.Store(false)
		start, ok, ko := time.Now(), 0, 0
		for _, v := range versions {
			t, found, err := st.TemplateByID(ctx, v.TemplateID)
			if err != nil || !found {
				continue
			}
			res, err := s.checkVersion(ctx, t, v.Number)
			switch {
			case ctx.Err() != nil:
				return
			case err != nil:
				s.logger.Warn("reference check failed", "template", t.Path, "error", err)
				ko++
			case res.Ok:
				ok++
			default:
				s.logger.Warn("template differs from its reference", "template", t.Path, "version", v.Number)
				ko++
			}
		}
		if err := st.Sync(ctx); err != nil {
			s.logger.Warn("database copy not updated", "error", err)
		}
		s.logger.Info("reference check done", "templates", len(versions), "ok", ok, "differ", ko, "seconds", int(time.Since(start).Seconds()))
	}()
	return len(versions), nil
}

// Maintain runs once the engine is ready: it reads the templates imported
// in bulk, then compares every reference again after a new version of
// DocIbis.
func (s *Server) Maintain(ctx context.Context) {
	st := s.db.Load()
	if st == nil {
		return
	}
	s.recordRelease(ctx)
	if version.Version != "dev" {
		s.resetFast(ctx, st)
		s.resetNative(ctx, st)
	}
	s.describeLater(ctx)
	if version.Version == "dev" {
		return
	}
	last, _, err := st.Setting(ctx, checkVersionKey)
	if err != nil || last == version.Version {
		return
	}
	if err := st.SetSetting(ctx, checkVersionKey, version.Version); err != nil {
		s.logger.Warn("cannot record the version checked", "error", err)
		return
	}
	if n, err := s.checkAll(ctx); err != nil {
		s.logger.Warn("reference check not started", "error", err)
	} else if n > 0 {
		s.logger.Info("new version: comparing the templates with their references", "templates", n, "previous", last)
	}
}
