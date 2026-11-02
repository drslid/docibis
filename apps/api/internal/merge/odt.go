// Package merge fills Docmosis-style templates (<<field>>, <<cs_x>>…<<es_x>>,
// <<rr_x>>…<<er_x>>, checkboxes, dateFormat) prepared as ODT by LibreOffice.
package merge

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// Parts of an ODT that hold text with tags: the body, and the headers and
// footers kept in the styles.
var parts = map[string]bool{"content.xml": true, "styles.xml": true}

// Options change how a document is filled.
type Options struct {
	// Now is the date shown by $nowUTC.
	Now time.Time
	// Fetch reads an image given by its address (imgfit_ fields); nil leaves
	// such images out.
	Fetch func(url string) ([]byte, error)
	// Mark writes, instead of its value, what it returns for each tag, in the
	// order of the document: the overlay engine finds the fields this way.
	Mark func(i int, tag string) string
	// Meta replaces the title, author, subject or keywords of the document,
	// which LibreOffice writes in the PDF.
	Meta map[string]string
	// Dev shows, highlighted in the document, what cannot be filled (data
	// absent, expression unreadable) instead of leaving it blank, as the
	// devMode of Docmosis does.
	Dev bool
}

// ODT fills a template prepared as ODT with JSON data and returns the
// merged ODT. now is the date shown by $nowUTC.
func ODT(template []byte, data []byte, now time.Time) ([]byte, error) {
	return Fill(template, data, Options{Now: now})
}

// Fill is ODT with options.
func Fill(template []byte, data []byte, o Options) ([]byte, error) {
	root, err := decode(data)
	if err != nil {
		return nil, err
	}
	zr, err := zip.NewReader(bytes.NewReader(template), int64(len(template)))
	if err != nil {
		return nil, fmt.Errorf("template is not an ODT: %w", err)
	}
	now := o.Now
	m := &merger{now: now, fetch: o.Fetch, mark: o.Mark, dev: o.Dev}
	merged := map[string][]byte{}
	for _, f := range zr.File {
		if !parts[f.Name] {
			continue
		}
		raw, err := read(f)
		if err != nil {
			return nil, err
		}
		tree, err := parse(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.Name, err)
		}
		normalize(tree)
		if o.Dev {
			addDevStyle(tree)
		}
		m.process(tree, scope{root})
		if m.err != nil {
			return nil, m.err
		}
		dropMarkerStyles(tree)
		merged[f.Name] = tree.bytes()
	}

	// Same files in the same order (mimetype first), the merged parts, the
	// manifest with the added images, then the images.
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, f := range zr.File {
		var content []byte
		switch {
		case strings.HasSuffix(f.Name, "/"):
			continue // folders are implied by the paths of the files
		case merged[f.Name] != nil:
			content = merged[f.Name]
		case f.Name == "meta.xml" && len(o.Meta) > 0:
			raw, err := read(f)
			if err != nil {
				return nil, err
			}
			if content, err = withMeta(raw, o.Meta); err != nil {
				return nil, fmt.Errorf("meta.xml: %w", err)
			}
		case f.Name == "META-INF/manifest.xml" && len(m.pictures) > 0:
			raw, err := read(f)
			if err != nil {
				return nil, err
			}
			content = manifest(raw, m.pictures)
		default:
			if err := zw.Copy(f); err != nil {
				return nil, err
			}
			continue
		}
		if err := write(zw, f.Name, zip.Deflate, content, f.Modified); err != nil {
			return nil, err
		}
	}
	// Deflated: LibreOffice refuses stored entries followed by a data
	// descriptor, which zip.Writer writes when it streams.
	for _, p := range m.pictures {
		if err := write(zw, p.path, zip.Deflate, p.data, now); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func write(zw *zip.Writer, name string, method uint16, content []byte, modified time.Time) error {
	w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: method, Modified: modified})
	if err != nil {
		return err
	}
	_, err = w.Write(content)
	return err
}

// manifest lists the added images in META-INF/manifest.xml.
func manifest(raw []byte, pictures []picture) []byte {
	var entries bytes.Buffer
	for _, p := range pictures {
		fmt.Fprintf(&entries, `<manifest:file-entry manifest:full-path="%s" manifest:media-type="%s"/>`, p.path, p.mime)
	}
	end := bytes.LastIndex(raw, []byte("</manifest:manifest>"))
	if end < 0 {
		return raw
	}
	return append(append(append([]byte(nil), raw[:end]...), entries.Bytes()...), raw[end:]...)
}

// metaElements are the elements of meta.xml for the title, the author, the
// subject and the keywords.
var metaElements = map[string][]string{
	"title": {"dc:title"}, "author": {"meta:initial-creator", "dc:creator"}, "subject": {"dc:subject"}, "keywords": {"meta:keyword"},
}

// withMeta replaces the title, author, subject or keywords of meta.xml.
func withMeta(raw []byte, values map[string]string) ([]byte, error) {
	tree, err := parse(raw)
	if err != nil {
		return nil, err
	}
	var meta *node
	tree.walk(func(n *node) bool {
		if n.name == "office:meta" {
			meta = n
		}
		return meta == nil
	})
	if meta == nil {
		return raw, nil
	}
	for key, names := range metaElements {
		value, ok := values[key]
		if !ok {
			continue
		}
		for _, name := range names {
			for _, c := range append([]*node(nil), meta.children...) {
				if c.name == name {
					c.remove()
				}
			}
		}
		items := []string{value}
		if key == "keywords" {
			items = strings.Split(value, ",")
		}
		for _, name := range names {
			for _, item := range items {
				if item = strings.TrimSpace(item); item == "" {
					continue
				}
				e := &node{kind: element, name: name, parent: meta}
				e.children = []*node{{kind: textNode, text: item, parent: e}}
				meta.children = append(meta.children, e)
			}
		}
	}
	return tree.bytes(), nil
}

// dropMarkerStyles removes the formatting of the paragraph marks, which
// LibreOffice reads from Word files: Docmosis documents have none, and it
// changes the height of empty paragraphs.
func dropMarkerStyles(root *node) {
	root.walk(func(n *node) bool {
		if isParagraph(n) {
			kept := n.attrs[:0]
			for _, a := range n.attrs {
				if a.Name.Local != "marker-style-name" {
					kept = append(kept, a)
				}
			}
			n.attrs = kept
		}
		return true
	})
}

// decode reads JSON data, keeping numbers as they are written.
func decode(data []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, fmt.Errorf("data: %w", err)
	}
	if _, ok := v.(map[string]any); !ok {
		return nil, fmt.Errorf("data: a JSON object is expected")
	}
	return v, nil
}

func read(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}
