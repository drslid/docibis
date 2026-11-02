// Package fast is the overlay engine ("calque"): for a template whose layout
// never moves with the data, the PDF is the page of LibreOffice without the
// values, on which the values are written directly, in the fonts and at the
// places LibreOffice uses. A document then takes well under a millisecond.
package fast

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// document is a PDF made by LibreOffice, read once.
type document struct {
	raw   []byte
	ctx   *model.Context
	pages []*page
}

type page struct {
	ref       int // object number of the page
	dict      types.Dict
	resources types.Dict
	fonts     map[string]*font // by resource name
	content   []byte
}

// font is a simple font of a page: what its codes draw and how wide they are.
type font struct {
	ref       int // object number in its document
	first     int
	widths    []float64 // in thousandths of the size
	unicode   map[byte]string
	ascent    float64
	descent   float64
	composite bool
}

func (f *font) width(code byte) float64 {
	if i := int(code) - f.first; i >= 0 && i < len(f.widths) {
		return f.widths[i]
	}
	return 0
}

// code is the lowest code the font draws a text with.
func (f *font) code(text string) (byte, bool) {
	for c := 0; c < 256; c++ {
		if t, ok := f.unicode[byte(c)]; ok && t == text {
			return byte(c), true
		}
	}
	return 0, false
}

func (f *font) has(text string) bool {
	_, ok := f.code(text)
	return ok
}

func load(raw []byte) (*document, error) {
	conf := model.NewDefaultConfiguration()
	conf.ValidationMode = model.ValidationRelaxed
	ctx, err := api.ReadContext(context.Background(), bytes.NewReader(raw), conf)
	if err != nil {
		return nil, fmt.Errorf("pdf: %w", err)
	}
	if err := ctx.EnsurePageCount(); err != nil {
		return nil, fmt.Errorf("pdf: %w", err)
	}
	doc := &document{raw: raw, ctx: ctx}
	fonts := map[int]*font{}
	for n := 1; n <= ctx.PageCount; n++ {
		d, ref, inherited, err := ctx.PageDict(context.Background(), n, false)
		if err != nil || d == nil || ref == nil {
			return nil, fmt.Errorf("pdf page %d: %v", n, err)
		}
		p := &page{ref: ref.ObjectNumber.Value(), dict: d, fonts: map[string]*font{}}
		if o, found := d.Find("Resources"); found {
			p.resources, err = ctx.DereferenceDict(o)
		} else if inherited != nil {
			p.resources = inherited.Resources
		}
		if err != nil {
			return nil, err
		}
		if p.content, err = ctx.PageContent(d, n); err != nil && err != model.ErrNoContent {
			return nil, err
		}
		if fd, ok := p.resources.Find("Font"); ok {
			named, err := ctx.DereferenceDict(fd)
			if err != nil {
				return nil, err
			}
			for name, o := range named {
				r, ok := o.(types.IndirectRef)
				if !ok {
					continue
				}
				f := fonts[r.ObjectNumber.Value()]
				if f == nil {
					if f, err = doc.font(r); err != nil {
						return nil, err
					}
					fonts[r.ObjectNumber.Value()] = f
				}
				p.fonts[name] = f
			}
		}
		doc.pages = append(doc.pages, p)
	}
	return doc, nil
}

func number(o types.Object) float64 {
	switch v := o.(type) {
	case types.Integer:
		return float64(v.Value())
	case types.Float:
		return v.Value()
	}
	return 0
}

func (doc *document) font(r types.IndirectRef) (*font, error) {
	ctx := doc.ctx
	d, err := ctx.DereferenceDict(r)
	if err != nil {
		return nil, err
	}
	f := &font{ref: r.ObjectNumber.Value(), unicode: map[byte]string{}}
	if sub := d.NameEntry("Subtype"); sub != nil && *sub == "Type0" {
		f.composite = true
		return f, nil
	}
	if first := d.IntEntry("FirstChar"); first != nil {
		f.first = *first
	}
	if w, ok := d.Find("Widths"); ok {
		a, err := ctx.DereferenceArray(w)
		if err != nil {
			return nil, err
		}
		for _, o := range a {
			o, _ = ctx.Dereference(o)
			f.widths = append(f.widths, number(o))
		}
	}
	if fd, ok := d.Find("FontDescriptor"); ok {
		if desc, err := ctx.DereferenceDict(fd); err == nil {
			if o, ok := desc.Find("Ascent"); ok {
				f.ascent = number(o)
			}
			if o, ok := desc.Find("Descent"); ok {
				f.descent = number(o)
			}
		}
	}
	if tu, ok := d.Find("ToUnicode"); ok {
		sd, _, err := ctx.DereferenceStreamDict(tu)
		if err != nil || sd == nil {
			return nil, fmt.Errorf("font %d: no ToUnicode", f.ref)
		}
		if err := sd.Decode(); err != nil {
			return nil, err
		}
		readCMap(sd.Content, f.unicode)
	}
	return f, nil
}

var (
	bfchar  = regexp.MustCompile(`(?s)beginbfchar(.*?)endbfchar`)
	bfrange = regexp.MustCompile(`(?s)beginbfrange(.*?)endbfrange`)
	hexes   = regexp.MustCompile(`<([0-9A-Fa-f]*)>|\[([^\]]*)\]`)
)

func hexText(h string) string {
	var r []rune
	for i := 0; i+4 <= len(h); i += 4 {
		v, _ := strconv.ParseUint(h[i:i+4], 16, 32)
		r = append(r, rune(v))
	}
	return string(r)
}

// readCMap reads the codes of a ToUnicode CMap (one byte codes).
func readCMap(cmap []byte, into map[byte]string) {
	for _, block := range bfchar.FindAllSubmatch(cmap, -1) {
		parts := hexes.FindAllStringSubmatch(string(block[1]), -1)
		for i := 0; i+1 < len(parts); i += 2 {
			code, err := strconv.ParseUint(parts[i][1], 16, 32)
			if err == nil && code < 256 {
				into[byte(code)] = hexText(parts[i+1][1])
			}
		}
	}
	for _, block := range bfrange.FindAllSubmatch(cmap, -1) {
		parts := hexes.FindAllStringSubmatch(string(block[1]), -1)
		for i := 0; i+2 < len(parts); i += 3 {
			lo, err1 := strconv.ParseUint(parts[i][1], 16, 32)
			hi, err2 := strconv.ParseUint(parts[i+1][1], 16, 32)
			if err1 != nil || err2 != nil || hi > 255 || lo > hi {
				continue
			}
			if parts[i+2][2] != "" {
				for k, h := range strings.Fields(strings.NewReplacer("<", " ", ">", " ").Replace(parts[i+2][2])) {
					if c := lo + uint64(k); c <= hi {
						into[byte(c)] = hexText(h)
					}
				}
				continue
			}
			base := []rune(hexText(parts[i+2][1]))
			if len(base) == 0 {
				continue
			}
			for c := lo; c <= hi; c++ {
				r := append([]rune{}, base...)
				r[len(r)-1] += rune(c - lo)
				into[byte(c)] = string(r)
			}
		}
	}
}
