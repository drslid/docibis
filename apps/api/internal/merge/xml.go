package merge

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// node is an XML node that keeps the prefixes of the document as they are,
// so that a merged ODF part is written back byte for byte, except the
// changes.
type node struct {
	kind     kind
	name     string // prefixed element name, such as "text:p"
	attrs    []xml.Attr
	children []*node
	text     string // decoded text
	raw      []byte // processing instruction, comment or directive
	parent   *node
}

type kind uint8

const (
	element kind = iota
	textNode
	rawNode
)

func prefixed(n xml.Name) string {
	if n.Space == "" {
		return n.Local
	}
	return n.Space + ":" + n.Local
}

// maxDepth bounds the nesting of a part: the tree is walked recursively.
const maxDepth = 1000

// parse reads a whole XML document into a tree under a document node.
func parse(data []byte) (*node, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	root := &node{kind: element}
	cur := root
	depth := 0
	for {
		tok, err := d.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if depth++; depth > maxDepth {
				return nil, fmt.Errorf("more than %d nested elements", maxDepth)
			}
			n := &node{kind: element, name: prefixed(t.Name), attrs: append([]xml.Attr(nil), t.Attr...), parent: cur}
			cur.children = append(cur.children, n)
			cur = n
		case xml.EndElement:
			if cur.parent == nil || cur.name != prefixed(t.Name) {
				return nil, fmt.Errorf("unexpected </%s>", prefixed(t.Name))
			}
			depth--
			cur = cur.parent
		case xml.CharData:
			cur.children = append(cur.children, &node{kind: textNode, text: string(t), parent: cur})
		case xml.ProcInst:
			cur.children = append(cur.children, &node{kind: rawNode, raw: []byte("<?" + t.Target + " " + string(t.Inst) + "?>"), parent: cur})
		case xml.Comment:
			cur.children = append(cur.children, &node{kind: rawNode, raw: []byte("<!--" + string(t) + "-->"), parent: cur})
		case xml.Directive:
			cur.children = append(cur.children, &node{kind: rawNode, raw: []byte("<!" + string(t) + ">"), parent: cur})
		}
	}
	if cur != root {
		return nil, fmt.Errorf("unclosed <%s>", cur.name)
	}
	return root, nil
}

func (n *node) write(b *bytes.Buffer) {
	switch n.kind {
	case textNode:
		escape(b, n.text, false)
	case rawNode:
		b.Write(n.raw)
	case element:
		if n.name == "" {
			for _, c := range n.children {
				c.write(b)
			}
			return
		}
		b.WriteByte('<')
		b.WriteString(n.name)
		for _, a := range n.attrs {
			b.WriteByte(' ')
			b.WriteString(prefixed(a.Name))
			b.WriteString(`="`)
			escape(b, a.Value, true)
			b.WriteByte('"')
		}
		if len(n.children) == 0 {
			b.WriteString("/>")
			return
		}
		b.WriteByte('>')
		for _, c := range n.children {
			c.write(b)
		}
		b.WriteString("</")
		b.WriteString(n.name)
		b.WriteByte('>')
	}
}

func (n *node) bytes() []byte {
	var b bytes.Buffer
	n.write(&b)
	return b.Bytes()
}

func escape(b *bytes.Buffer, s string, attr bool) {
	for _, r := range s {
		switch {
		case r == '&':
			b.WriteString("&amp;")
		case r == '<':
			b.WriteString("&lt;")
		case r == '>':
			b.WriteString("&gt;")
		case r == '"' && attr:
			b.WriteString("&quot;")
		case (r == '\n' || r == '\r' || r == '\t') && attr:
			fmt.Fprintf(b, "&#%d;", r)
		default:
			b.WriteRune(r)
		}
	}
}

func (n *node) attr(name string) string {
	for _, a := range n.attrs {
		if prefixed(a.Name) == name {
			return a.Value
		}
	}
	return ""
}

func (n *node) setAttr(name, value string) {
	for i, a := range n.attrs {
		if prefixed(a.Name) == name {
			n.attrs[i].Value = value
			return
		}
	}
	space, local, _ := strings.Cut(name, ":")
	n.attrs = append(n.attrs, xml.Attr{Name: xml.Name{Space: space, Local: local}, Value: value})
}

func (n *node) index() int {
	for i, c := range n.parent.children {
		if c == n {
			return i
		}
	}
	return -1
}

func (n *node) remove() {
	if n.parent == nil {
		return
	}
	i := n.index()
	n.parent.children = append(n.parent.children[:i], n.parent.children[i+1:]...)
	n.parent = nil
}

func (n *node) clone(parent *node) *node {
	c := &node{kind: n.kind, name: n.name, attrs: append([]xml.Attr(nil), n.attrs...), text: n.text, raw: n.raw, parent: parent}
	for _, child := range n.children {
		c.children = append(c.children, child.clone(c))
	}
	return c
}

// insertAfter puts nodes right after n, under the same parent.
func (n *node) insertAfter(nodes ...*node) {
	i := n.index() + 1
	p := n.parent
	rest := append([]*node(nil), p.children[i:]...)
	p.children = append(append(p.children[:i], nodes...), rest...)
	for _, c := range nodes {
		c.parent = p
	}
}

// walk visits elements in document order; it does not descend when visit
// returns false.
func (n *node) walk(visit func(*node) bool) {
	for _, c := range append([]*node(nil), n.children...) {
		if c.kind == element && visit(c) {
			c.walk(visit)
		}
	}
}

func (n *node) ancestor(name string) *node {
	for p := n.parent; p != nil; p = p.parent {
		if p.name == name {
			return p
		}
	}
	return nil
}
