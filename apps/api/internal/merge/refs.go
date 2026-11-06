package merge

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Shared blocks, as Docmosis: a paragraph that holds only <<ref:name>> is
// replaced by the body of the template name (a legal clause, a signature
// block…), which the merge then fills with the same data. The automatic
// styles of the block come along, renamed so as not to meet those of the
// document, with its images; its common styles are those of the document.

var refTag = regexp.MustCompile(`^\s*<<ref:([^<>{}]+)>>\s*$`)

// <<refLookup:field>> takes the name of the block from the data, as Docmosis
// does: a block for each value of field, which the merge keeps where field
// has that value (in the data or in each item of a repeat).
// <<refLookupOp:field>> may find no name; refLookup then refuses.
var lookupTag = regexp.MustCompile(`^\s*<<refLookup(Op)?:([^<>{}]+)>>\s*$`)

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
			if t := paragraphText(n); refTag.MatchString(t) || lookupTag.MatchString(t) {
				out = append(out, n)
			}
			return false
		}
		return true
	})
	return out
}

// lookupContexts gives, for each paragraph <<refLookup:…>>, the names of the
// repeats it is in, outermost first.
func lookupContexts(root *node) map[*node][]string {
	out := map[*node][]string{}
	var lists, blocks, rows []string
	root.walk(func(n *node) bool {
		if !isParagraph(n) {
			return true
		}
		text := paragraphText(n)
		if lookupTag.MatchString(text) {
			out[n] = slices.Clone(lists)
			return false
		}
		for _, m := range tagPattern.FindAllStringSubmatch(text, -1) {
			switch tag := strings.TrimSpace(quotes.Replace(m[1])); {
			case strings.HasPrefix(tag, "rr_"):
				lists, rows = append(lists, tag[3:]), append(rows, "list")
			case strings.HasPrefix(tag, "rs_"):
				lists, blocks = append(lists, tag[3:]), append(blocks, "list")
			case strings.HasPrefix(tag, "cs_"):
				blocks = append(blocks, "section")
			case strings.HasPrefix(tag, "cr_"):
				rows = append(rows, "condition")
			case strings.HasPrefix(tag, "er_") && len(rows) > 0:
				if rows[len(rows)-1] == "list" && len(lists) > 0 {
					lists = lists[:len(lists)-1]
				}
				rows = rows[:len(rows)-1]
			case strings.HasPrefix(tag, "es_") && len(blocks) > 0:
				if blocks[len(blocks)-1] == "list" && len(lists) > 0 {
					lists = lists[:len(lists)-1]
				}
				blocks = blocks[:len(blocks)-1]
			}
		}
		return false
	})
	return out
}

// lookupNames gives the blocks <<refLookup:field>> may insert: the values
// of field in the data, or in the items of the repeats around it.
func lookupNames(data map[string]any, lists []string, field string) []string {
	scopes := [][]map[string]any{{data}} // innermost first
	group := ""
	for _, l := range lists {
		path, mods := listName(l)
		if slices.ContainsFunc(mods, func(m modifier) bool { return m.kind == "group" }) {
			// A group reads the data around it; its items come with $groupItems.
			group = path
			continue
		}
		if path == groupItems {
			path = group
		}
		var next [][]map[string]any
		for _, s := range scopes {
			items, _ := find(s, path).([]any)
			for _, it := range items {
				if m, ok := it.(map[string]any); ok {
					next = append(next, append([]map[string]any{m}, s...))
				}
			}
		}
		scopes = next
	}
	var names []string
	for _, s := range scopes {
		if name, ok := find(s, field).(string); ok && strings.TrimSpace(name) != "" && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names
}

// find looks path up in the innermost scope that has it.
func find(scopes []map[string]any, path string) any {
	for _, s := range scopes {
		if v, ok := lookup(s, path); ok {
			return v
		}
	}
	return nil
}

// tagParagraph is a paragraph that holds only tag.
func tagParagraph(tag string) *node {
	p := &node{kind: element, name: "text:p"}
	p.children = []*node{{kind: textNode, text: tag, parent: p}}
	return p
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
// templates load returns (an ODT each), blocks within blocks included, and
// the paragraphs <<refLookup:field>> with the blocks data names.
func Include(odt []byte, data map[string]any, load func(name string) ([]byte, error)) ([]byte, error) {
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
			return nil, &ErrBlock{fmt.Sprintf("blocs imbriqués sur plus de %d niveaux (ou qui s'incluent eux-mêmes) : %s", maxRefDepth, strings.TrimSpace(paragraphText(refs[0])))}
		}
		contexts := lookupContexts(tree)
		for _, p := range refs {
			var names []string
			field := ""
			if m := lookupTag.FindStringSubmatch(paragraphText(p)); m != nil {
				field = strings.TrimSpace(m[2])
				if names = lookupNames(data, contexts[p], field); len(names) == 0 && m[1] == "" {
					return nil, &ErrBlock{fmt.Sprintf("<<refLookup:%s>> : les données ne donnent pas le nom du bloc à insérer (<<refLookupOp:%s>> accepte qu'il manque)", field, field)}
				}
			} else {
				names = []string{refName(p)}
			}
			var inserted []*node
			for _, name := range names {
				if count++; count > maxRefs {
					return nil, &ErrBlock{fmt.Sprintf("plus de %d blocs inclus", maxRefs)}
				}
				if field != "" && strings.ContainsAny(name, `'"\{}<>`) {
					return nil, &ErrBlock{fmt.Sprintf("<<refLookup:%s>> : nom de bloc invalide %q", field, name)}
				}
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
				if field != "" {
					// Kept only where the field names this block.
					body = append(append([]*node{tagParagraph(fmt.Sprintf("<<cs_{%s='%s'}>>", field, name))}, body...), tagParagraph("<<es_>>"))
				}
				inserted = append(inserted, body...)
			}
			if len(inserted) > 0 {
				p.insertAfter(inserted...)
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
