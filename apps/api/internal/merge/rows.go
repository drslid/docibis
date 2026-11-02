package merge

import (
	"fmt"
	"strings"
)

// conditionalRows applies, in the tables of a subtree, the rows between
// <<cr_x>> and its <<er_…>>: kept when x holds, removed otherwise. The rows
// of the two markers always go, as in Docmosis.
func (m *merger) conditionalRows(root *node, s scope) {
	for {
		var row *node
		for _, o := range occurrences(root) {
			if strings.HasPrefix(o.tag, "cr_") {
				if row = o.para.ancestor("table:table-row"); row != nil {
					break
				}
			}
		}
		if row == nil {
			return
		}
		var rows []*node
		for _, c := range row.parent.children[row.index():] {
			if c.name == "table:table-row" {
				rows = append(rows, c)
			}
		}
		m.conditionalRowsIn(rows, s)
	}
}

// conditionalRowsIn applies the conditional rows whose markers lie in rows,
// consecutive rows of a table, and returns the rows left.
func (m *merger) conditionalRowsIn(rows []*node, s scope) []*node {
	for {
		start := -1
		var open occurrence
		for i, r := range rows {
			for _, o := range occurrences(r) {
				if strings.HasPrefix(o.tag, "cr_") {
					start, open = i, o
					break
				}
			}
			if start >= 0 {
				break
			}
		}
		if start < 0 {
			return rows
		}
		cond := strings.TrimPrefix(open.tag, "cr_")
		end, depth := -1, 0
	search:
		for j := start + 1; j < len(rows); j++ {
			for _, o := range occurrences(rows[j]) {
				switch {
				case strings.HasPrefix(o.tag, "cr_"), strings.HasPrefix(o.tag, "rr_"):
					depth++
				case strings.HasPrefix(o.tag, "er_") && depth > 0:
					depth--
				case o.tag == "er_" || o.tag == "er_"+cond:
					end = j
					break search
				}
			}
		}
		if end < 0 {
			// No end: the marker goes, the rows stay.
			open.text.text = open.text.text[:open.start] + open.text.text[open.end:]
			continue
		}
		keep := condition(strings.TrimSuffix(strings.TrimPrefix(cond, "{"), "}"), s)
		var left []*node
		for i, r := range rows {
			switch {
			case i == start || i == end, i > start && i < end && !keep:
				r.remove()
			default:
				left = append(left, r)
			}
		}
		rows = left
	}
}

// pageBreaks turns <<pageBreak>> into a page break: what follows the tag
// starts a new page. <<pageBreakNotLast>> does so except in the last item
// of a repeat.
func (m *merger) pageBreaks(root *node, s scope) {
	if automaticStyles(root) == nil {
		// A copy filled apart (a repeated section) only decides the last
		// item: its breaks are made once in the document. The headers and
		// footers have none.
		detached := top(root).name != ""
		all := occurrences(root)
		for i := len(all) - 1; i >= 0; i-- {
			o := all[i]
			if o.tag != "pageBreakNotLast" && (o.tag != "pageBreak" || detached) {
				continue
			}
			with := ""
			if detached && (o.tag == "pageBreak" || !lastItem(s)) {
				with = "<<pageBreak>>"
			}
			o.text.text = o.text.text[:o.start] + with + o.text.text[o.end:]
		}
		return
	}
	for {
		var tag *occurrence
		for _, o := range occurrences(root) {
			if o.tag == "pageBreak" || o.tag == "pageBreakNotLast" {
				oc := o
				tag = &oc
				break
			}
		}
		if tag == nil {
			return
		}
		o := *tag
		p := o.para
		if o.tag == "pageBreakNotLast" && lastItem(s) {
			o.text.text = o.text.text[:o.start] + o.text.text[o.end:]
			continue
		}
		styles := automaticStyles(p)
		head, tail := cut(p, o, cutHead), cut(p, o, cutTail)
		var out []*node
		if !blankContent(head) {
			out = append(out, head)
		}
		switch next := nextBlock(p); {
		case blankContent(tail) && next != nil:
			// A tag alone at the end of its paragraph: the next block starts the page.
			m.breakBefore(styles, next)
		default:
			m.breakBefore(styles, tail)
			out = append(out, tail)
		}
		if len(out) > 0 {
			p.insertAfter(out...)
		}
		p.remove()
	}
}

// lastItem tells whether the scope is the last item of a repeat.
func lastItem(s scope) bool {
	i, ok1 := s.lookup("$itemIdx")
	n, ok2 := s.lookup("$size")
	a, ok3 := number(i)
	b, ok4 := number(n)
	return ok1 && ok2 && ok3 && ok4 && a == b-1
}

// top is the outermost ancestor of n: the parsed document, which has no
// name, or a copy filled apart.
func top(n *node) *node {
	for n.parent != nil {
		n = n.parent
	}
	return n
}

// automaticStyles returns the automatic styles of the document that holds n.
func automaticStyles(n *node) *node {
	root := top(n)
	var styles *node
	root.walk(func(c *node) bool {
		if c.name == "office:automatic-styles" && c.parent != nil && c.parent.name == "office:document-content" {
			styles = c
		}
		return styles == nil
	})
	return styles
}

// nextBlock is the paragraph or table that follows p in its parent.
func nextBlock(p *node) *node {
	for _, c := range p.parent.children[p.index()+1:] {
		if isParagraph(c) || c.name == "table:table" {
			return c
		}
		if c.kind == element && c.name != "text:soft-page-break" && c.name != "text:bookmark" {
			return nil
		}
	}
	return nil
}

// breakBefore gives a paragraph or a table a style that starts a new page:
// a copy of its automatic style, or a new style based on its named one.
func (m *merger) breakBefore(styles, block *node) {
	family, props, attr := "paragraph", "style:paragraph-properties", "text:style-name"
	if block.name == "table:table" {
		family, props, attr = "table", "style:table-properties", "table:style-name"
	}
	current := block.attr(attr)
	m.breaks++
	name := fmt.Sprintf("DocIbisBreak%d", m.breaks)
	var style *node
	for _, c := range styles.children {
		if c.name == "style:style" && c.attr("style:name") == current && current != "" {
			style = c.clone(nil)
			break
		}
	}
	if style == nil {
		style = &node{kind: element, name: "style:style"}
		style.setAttr("style:family", family)
		if current != "" {
			style.setAttr("style:parent-style-name", current)
		}
	}
	style.setAttr("style:name", name)
	var p *node
	for _, c := range style.children {
		if c.name == props {
			p = c
		}
	}
	if p == nil {
		p = &node{kind: element, name: props, parent: style}
		style.children = append(style.children, p)
	}
	p.setAttr("fo:break-before", "page")
	style.parent = styles
	styles.children = append(styles.children, style)
	block.setAttr(attr, name)
}
