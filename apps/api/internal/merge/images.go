package merge

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"math"
	"strconv"
	"strings"

	// Formats accepted for image fields, with PNG.
	_ "image/gif"
	_ "image/jpeg"
)

// maxImage bounds the size of one image, decoded.
const maxImage = 10 << 20

// picture is an image added to the package.
type picture struct {
	path string
	mime string
	data []byte
}

// images fills the image placeholders: the frames between the bookmarks
// imgfit_x get the image of field x, scaled to fit the frame with its ratio
// kept, from the top left corner. Without a usable image (field missing,
// not an image), the placeholder is removed, as Docmosis does. Images come
// as data URIs; addresses (URLs) are not followed yet.
func (m *merger) images(root *node, s scope) {
	var starts []*node
	root.walk(func(n *node) bool {
		if n.name == "text:bookmark-start" && strings.HasPrefix(n.attr("text:name"), "imgfit_") {
			starts = append(starts, n)
		}
		return true
	})
	for _, start := range starts {
		name := start.attr("text:name")
		para := start.parent
		for para != nil && !isParagraph(para) {
			para = para.parent
		}
		if para == nil {
			continue
		}
		frames, end := placeholder(para, start, name)
		var pic picture
		var width, height int
		ok := false
		if value, found := s.lookup(strings.TrimPrefix(name, "imgfit_")); found {
			if ref, isText := value.(string); isText {
				pic, width, height, ok = m.load(ref)
			}
		}
		for _, f := range frames {
			if !ok {
				f.remove()
				continue
			}
			fit(f, width, height)
			f.walk(func(n *node) bool {
				if n.name == "draw:image" {
					n.setAttr("xlink:href", pic.path)
					n.setAttr("draw:mime-type", pic.mime)
					n.children = nil
				}
				return true
			})
		}
		start.remove()
		if end != nil {
			end.remove()
		}
	}
}

// placeholder returns the frames between a bookmark start and its end.
func placeholder(para, start *node, name string) (frames []*node, end *node) {
	inside := false
	var visit func(n *node) bool
	visit = func(n *node) bool {
		for _, c := range n.children {
			switch {
			case c == start:
				inside = true
			case c.name == "text:bookmark-end" && c.attr("text:name") == name:
				end = c
				return false
			case inside && c.name == "draw:frame":
				frames = append(frames, c)
			case c.kind == element && !opaque[c.name]:
				if !visit(c) {
					return false
				}
			}
		}
		return true
	}
	visit(para)
	return frames, end
}

// load decodes a data URI, or reads an address through the fetcher, and
// checks that it holds an image.
func (m *merger) load(ref string) (picture, int, int, bool) {
	var data []byte
	if strings.HasPrefix(ref, "https://") || strings.HasPrefix(ref, "http://") {
		if m.fetch == nil {
			return picture{}, 0, 0, false
		}
		var err error
		if data, err = m.fetch(ref); err != nil || len(data) > maxImage {
			return picture{}, 0, 0, false
		}
	} else {
		meta, payload, found := strings.Cut(strings.TrimPrefix(ref, "data:"), ",")
		if !strings.HasPrefix(ref, "data:") || !found || !strings.HasSuffix(meta, ";base64") ||
			base64.StdEncoding.DecodedLen(len(payload)) > maxImage {
			return picture{}, 0, 0, false
		}
		var err error
		if data, err = base64.StdEncoding.DecodeString(payload); err != nil {
			return picture{}, 0, 0, false
		}
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return picture{}, 0, 0, false
	}
	p := picture{path: fmt.Sprintf("Pictures/docibis-%d.%s", len(m.pictures)+1, format), mime: "image/" + format, data: data}
	m.pictures = append(m.pictures, p)
	return p, cfg.Width, cfg.Height, true
}

// fit sets the size of frame f to the largest that keeps the ratio of an
// image of w x h pixels inside its current size.
func fit(f *node, w, h int) {
	fw, unit, ok1 := length(f.attr("svg:width"))
	fh, unit2, ok2 := length(f.attr("svg:height"))
	if !ok1 || !ok2 || unit != unit2 || fw <= 0 || fh <= 0 {
		return
	}
	scale := math.Min(fw/float64(w), fh/float64(h))
	f.setAttr("svg:width", strconv.FormatFloat(float64(w)*scale, 'f', 4, 64)+unit)
	f.setAttr("svg:height", strconv.FormatFloat(float64(h)*scale, 'f', 4, 64)+unit)
}

// length splits an ODF length such as "3.895cm".
func length(s string) (float64, string, bool) {
	i := strings.IndexFunc(s, func(r rune) bool { return (r < '0' || r > '9') && r != '.' && r != '-' })
	if i <= 0 {
		return 0, "", false
	}
	v, err := strconv.ParseFloat(s[:i], 64)
	return v, s[i:], err == nil
}

// exampleImage is the image Sample gives to image fields: a grey band.
func exampleImage() string {
	img := image.NewGray(image.Rect(0, 0, 300, 100))
	for i := range img.Pix {
		img.Pix[i] = 0xd0
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
}
