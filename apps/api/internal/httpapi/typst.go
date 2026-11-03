package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"docibis/internal/render"
)

// Typst templates, without Word: a .typ file, sent or kept in the library,
// is compiled by the native engine with the data (sys.inputs.data, as for
// the Typst copies), in a few milliseconds. No LibreOffice: the PDF options
// of LibreOffice (PDF/A, accessible, watermark) do not apply; password and
// seal do.

func isTypst(name string) bool { return strings.EqualFold(path.Ext(name), ".typ") }

func (s *Server) produceTypst(ctx context.Context, f tryForm, format string) (document, error) {
	doc := document{name: f.name, format: "pdf", outputName: f.outputName, libID: f.libID, libVersion: f.libVersion, opts: f.opts, engine: "native"}
	if f.hasData {
		doc.data = f.data
	}
	if f.libID == 0 {
		doc.template = f.template
	}
	if format == "" {
		format = f.format
	}
	if format != "" && format != "pdf" {
		return doc, refuse("Un modèle Typst se produit en PDF.")
	}
	if err := checkNativeSource(f.template); err != nil {
		return doc, err
	}
	if err := f.opts.resolve(); err != nil {
		return doc, err
	}
	var root map[string]any
	if !f.hasData || json.Unmarshal(f.data, &root) != nil {
		return doc, refuse("Les données doivent être un objet JSON valide, par exemple { \"nom\": \"Dupont\" }.")
	}
	box := s.native.Load()
	if box == nil {
		return doc, &tryError{http.StatusServiceUnavailable, "Le moteur Typst démarre : réessayez dans un instant."}
	}
	source := string(f.template)
	file, err := s.nativeFile(ctx, f.libVersionID, hashText(source), source)
	if err != nil {
		return doc, err
	}
	dir, err := os.MkdirTemp("", "docibis-typst-")
	if err != nil {
		return doc, err
	}
	defer os.RemoveAll(dir)
	out := filepath.Join(dir, "document.pdf")
	start := time.Now()
	if _, err := box.Compile(ctx, file, out, nativeInputs(f.data, start)); err != nil {
		switch {
		case errors.Is(err, render.ErrBusy) || ctx.Err() != nil:
			return doc, err
		case declines(err):
			return doc, refuse("Le modèle Typst refuse ces données : %s", shorten(err.Error(), 500))
		}
		return doc, refuse("Le modèle Typst ne se compile pas : %s", shorten(err.Error(), 1000))
	}
	doc.render = time.Since(start)
	doc.content, err = os.ReadFile(out)
	return doc, err
}
