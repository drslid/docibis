package merge

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
)

// Shared blocks, as Docmosis: a paragraph that holds only <<ref:name>> is
// replaced by the body of the template name (a legal clause, a signature
// block…), which the merge then fills with the same data. The automatic
// styles of the block come along, renamed so as not to meet those of the
// document, with its images; its common styles are those of the document.

var refTag = regexp.MustCompile(`^\s*<<ref:([^<>{}]+)>>\s*$`)

const (
	// maxRefDepth bounds blocks within blocks (and loops).
	maxRefDepth = 4
	maxRefs     = 50
)

// ErrBlock is a block that cannot be included; the message says which.
type ErrBlock struct{ Msg string }

func (e *ErrBlock) Error() string { return e.Msg }

func refName(p *node) string {
	return strings.TrimSpace(refTag.FindStringSubmatch(paragraphText(p))[1])
}

func refParagraphs(root *node) []*node {
	var out []*node
	root.walk(func(n *node) bool {
		if isParagraph(n) {
			if refTag.MatchString(paragraphText(n)) {
				out = append(out, n)
			}
			return false
		}
		return true
	})
	return out
}

func contentOf(odt []byte) (*node, *zip.Reader, error) {
	zr, err := zip.NewReader(bytes.NewReader(odt), int64(len(odt)))
	if err != nil {
		return nil, nil, fmt.Errorf("template is not an ODT: %w", err)
	}
	for _, f := range zr.File {
		if f.Name == "content.xml" {
			raw, err := read(f)
			if err != nil {
				return nil, nil, err
			}
			tree, err := parse(raw)
			return tree, zr, err
		}
	}
	return nil, nil, errors.New("template without content.xml")
}

func firstElement(root *node, name string) *node {
	var found *node
	root.walk(func(n *node) bool {
		if n.name == name {
			found = n
		}
		return found == nil
	})
	return found
}

// Declarations of the body of a block that the document already has.
var bodyDeclarations = map[string]bool{
	"text:sequence-decls": true, "text:variable-decls": true, "text:user-field-decls": true,
	"text:dde-connection-decls": true, "office:forms": true, "table:calculation-settings": true,
}

// Include replaces the paragraphs <<ref:name>> with the body of the
// templates load returns (an ODT each), blocks within blocks included.
func Include(odt []byte, load func(name string) ([]byte, error)) ([]byte, error) {
	tree, zr, err := contentOf(odt)
	if err != nil {
		return nil, err
	}
	if len(refParagraphs(tree)) == 0 {
		return odt, nil
	}
	auto := firstElement(tree, "office:automatic-styles")
	if auto == nil {
		body := firstElement(tree, "office:body")
		if body == nil {
			return nil, errors.New("template without body")
		}
		auto = &node{kind: element, name: "office:automatic-styles"}
		parent, i := body.parent, body.index()
		parent.children = append(parent.children[:i], append([]*node{auto}, parent.children[i:]...)...)
		auto.parent = parent
	}
	var pictures []picture
	count := 0
	for depth := 0; ; depth++ {
		refs := refParagraphs(tree)
		if len(refs) == 0 {
			break
		}
		if depth == maxRefDepth {
			return nil, &ErrBlock{fmt.Sprintf("blocs imbriqués sur plus de %d niveaux (ou qui s'incluent eux-mêmes) : %s", maxRefDepth, refName(refs[0]))}
		}
		for _, p := range refs {
			if count++; count > maxRefs {
				return nil, &ErrBlock{fmt.Sprintf("plus de %d blocs inclus", maxRefs)}
			}
			name := refName(p)
			block, err := load(name)
			if err != nil {
				return nil, err
			}
			body, styles, pics, err := blockBody(block, fmt.Sprintf("ref%d_", count))
			if err != nil {
				return nil, &ErrBlock{fmt.Sprintf("bloc %s illisible : %v", name, err)}
			}
			for _, s := range styles {
				s.parent = auto
				auto.children = append(auto.children, s)
			}
			pictures = append(pictures, pics...)
			if len(body) > 0 {
				p.insertAfter(body...)
			}
			p.remove()
		}
	}

	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	now := time.Now()
	for _, f := range zr.File {
		var content []byte
		switch {
		case strings.HasSuffix(f.Name, "/"):
			continue
		case f.Name == "content.xml":
			content = tree.bytes()
		case f.Name == "META-INF/manifest.xml" && len(pictures) > 0:
			raw, err := read(f)
			if err != nil {
				return nil, err
			}
			content = manifest(raw, pictures)
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
	for _, p := range pictures {
		if err := write(zw, p.path, zip.Deflate, p.data, now); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// blockBody returns the body of a block, its automatic styles renamed with
// prefix, and its images under new names.
func blockBody(block []byte, prefix string) (body, styles []*node, pictures []picture, err error) {
	tree, zr, err := contentOf(block)
	if err != nil {
		return nil, nil, nil, err
	}
	text := firstElement(tree, "office:text")
	if text == nil {
		return nil, nil, nil, errors.New("no text body")
	}
	renamed := map[string]string{}
	if auto := firstElement(tree, "office:automatic-styles"); auto != nil {
		for _, s := range auto.children {
			if s.kind != element {
				continue
			}
			if name := s.attr("style:name"); name != "" {
				renamed[name] = prefix + name
				s.setAttr("style:name", prefix+name)
			}
			styles = append(styles, s)
		}
	}
	rename := func(n *node) {
		for i, a := range n.attrs {
			if strings.HasSuffix(a.Name.Local, "style-name") {
				if to, ok := renamed[a.Value]; ok {
					n.attrs[i].Value = to
				}
			}
		}
	}
	for _, s := range styles {
		rename(s)
		s.walk(func(n *node) bool { rename(n); return true })
	}
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		files[f.Name] = f
	}
	for _, c := range text.children {
		if c.kind == element && bodyDeclarations[c.name] {
			continue
		}
		body = append(body, c)
	}
	for _, c := range body {
		if c.kind != element {
			continue
		}
		rename(c)
		var walkErr error
		c.walk(func(n *node) bool {
			rename(n)
			if n.name == "draw:image" {
				href := n.attr("xlink:href")
				if f, ok := files[href]; ok && strings.HasPrefix(href, "Pictures/") {
					data, err := read(f)
					if err != nil {
						walkErr = err
						return false
					}
					to := "Pictures/" + prefix + path.Base(href)
					n.setAttr("xlink:href", to)
					pictures = append(pictures, picture{path: to, mime: imageMime(href), data: data})
				}
			}
			return true
		})
		if walkErr != nil {
			return nil, nil, nil, walkErr
		}
	}
	return body, styles, pictures, nil
}

func imageMime(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".svg":
		return "image/svg+xml"
	}
	return "application/octet-stream"
}
