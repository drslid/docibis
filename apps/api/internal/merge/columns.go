package merge

import (
	"fmt"
	"strconv"
	"strings"
)

// conditionalColumns removes the table columns whose first row holds a
// <<cc_x>> where x does not hold; the table keeps its width and the other
// columns share the room, as in Docmosis.
func (m *merger) conditionalColumns(root *node, s scope) {
	for {
		var tag *occurrence
		for _, o := range occurrences(root) {
			if strings.HasPrefix(o.tag, "cc_") {
				oc := o
				tag = &oc
				break
			}
		}
		if tag == nil {
			return
		}
		o := *tag
		o.text.text = o.text.text[:o.start] + o.text.text[o.end:]
		cell := o.para.ancestor("table:table-cell")
		if cell == nil || cell.parent == nil || cell.parent.name != "table:table-row" {
			continue
		}
		table := cell.parent.ancestor("table:table")
		expr := strings.TrimPrefix(o.tag, "cc_")
		if table == nil || condition(strings.TrimSuffix(strings.TrimPrefix(expr, "{"), "}"), s) {
			continue
		}
		m.removeColumn(table, columnIndex(cell.parent, cell))
	}
}

func repeated(n *node, attr string) int {
	if v, err := strconv.Atoi(n.attr(attr)); err == nil && v > 1 {
		return v
	}
	return 1
}

func setCount(n *node, attr string, v int) {
	if v > 1 {
		n.setAttr(attr, strconv.Itoa(v))
		return
	}
	kept := n.attrs[:0]
	for _, a := range n.attrs {
		if prefixed(a.Name) != attr {
			kept = append(kept, a)
		}
	}
	n.attrs = kept
}

func isCell(n *node) bool {
	return n.name == "table:table-cell" || n.name == "table:covered-table-cell"
}

// columnIndex is the column of a cell in its row, counting repeated cells.
func columnIndex(row, cell *node) int {
	i := 0
	for _, c := range row.children {
		if c == cell {
			return i
		}
		if isCell(c) {
			i += repeated(c, "table:number-columns-repeated")
		}
	}
	return i
}

// tableParts returns the column definitions and the rows of a table, not
// those of the tables inside its cells.
func tableParts(table *node) (columns, rows []*node) {
	var visit func(n *node)
	visit = func(n *node) {
		for _, c := range n.children {
			switch c.name {
			case "table:table-column":
				columns = append(columns, c)
			case "table:table-row":
				rows = append(rows, c)
			case "table:table-columns", "table:table-header-columns", "table:table-column-group",
				"table:table-rows", "table:table-header-rows", "table:table-row-group":
				visit(c)
			}
		}
	}
	visit(table)
	return columns, rows
}

// removeColumn takes column k out of a table: its definition, its cell in
// each row (a merged cell gets narrower), and gives its width to the others.
func (m *merger) removeColumn(table *node, k int) {
	columns, rows := tableParts(table)
	removed := ""
	i := 0
	for _, c := range columns {
		n := repeated(c, "table:number-columns-repeated")
		if k < i+n {
			removed = c.attr("table:style-name")
			if n > 1 {
				setCount(c, "table:number-columns-repeated", n-1)
			} else {
				c.remove()
			}
			break
		}
		i += n
	}
	for _, r := range rows {
		removeCell(r, k)
	}
	m.widen(table, removed)
}

func removeCell(row *node, k int) {
	i := 0
	var spanning *node // the last real cell, which may cover the next ones
	for _, c := range append([]*node(nil), row.children...) {
		if !isCell(c) {
			continue
		}
		n := repeated(c, "table:number-columns-repeated")
		if k >= i+n {
			if c.name == "table:table-cell" {
				spanning = c
			}
			i += n
			continue
		}
		switch {
		case n > 1:
			setCount(c, "table:number-columns-repeated", n-1)
		case c.name == "table:table-cell" && repeated(c, "table:number-columns-spanned") > 1:
			// The merged cell keeps its content; one covered cell goes.
			setCount(c, "table:number-columns-spanned", repeated(c, "table:number-columns-spanned")-1)
			for _, next := range row.children[c.index()+1:] {
				if next.name == "table:covered-table-cell" {
					removeOne(next)
					break
				}
			}
		case c.name == "table:covered-table-cell" && spanning != nil:
			setCount(spanning, "table:number-columns-spanned", repeated(spanning, "table:number-columns-spanned")-1)
			removeOne(c)
		default:
			c.remove()
		}
		return
	}
}

// removeOne removes one of the cells an element stands for.
func removeOne(c *node) {
	if n := repeated(c, "table:number-columns-repeated"); n > 1 {
		setCount(c, "table:number-columns-repeated", n-1)
		return
	}
	c.remove()
}

// widen gives the width of a removed column to the others, in proportion,
// with copies of their column styles.
func (m *merger) widen(table *node, removed string) {
	styles := automaticStyles(table)
	if styles == nil {
		return
	}
	find := func(name string) *node {
		for _, c := range styles.children {
			if c.name == "style:style" && c.attr("style:name") == name && name != "" {
				return c
			}
		}
		return nil
	}
	props := func(style *node) *node {
		if style == nil {
			return nil
		}
		for _, c := range style.children {
			if c.name == "style:table-column-properties" {
				return c
			}
		}
		return nil
	}
	columns, _ := tableParts(table)
	gone, _ := width(props(find(removed)), "style:column-width")
	if gone == 0 {
		return
	}
	total := 0.0
	for _, c := range columns {
		w, _ := width(props(find(c.attr("table:style-name"))), "style:column-width")
		total += w * float64(repeated(c, "table:number-columns-repeated"))
	}
	if total == 0 {
		return
	}
	factor := (total + gone) / total
	copies := map[string]string{}
	for _, c := range columns {
		name := c.attr("table:style-name")
		if copies[name] == "" {
			style := find(name)
			if props(style) == nil {
				continue
			}
			m.breaks++
			copy := style.clone(nil)
			copies[name] = fmt.Sprintf("%s.DocIbis%d", name, m.breaks)
			copy.setAttr("style:name", copies[name])
			p := props(copy)
			if w, u := width(p, "style:column-width"); w > 0 {
				p.setAttr("style:column-width", strconv.FormatFloat(w*factor, 'f', 4, 64)+u)
			}
			if v := p.attr("style:rel-column-width"); strings.HasSuffix(v, "*") {
				if r, err := strconv.ParseFloat(strings.TrimSuffix(v, "*"), 64); err == nil {
					p.setAttr("style:rel-column-width", strconv.Itoa(int(r*factor+0.5))+"*")
				}
			}
			copy.parent = styles
			styles.children = append(styles.children, copy)
		}
		c.setAttr("table:style-name", copies[name])
	}
}

// width reads a length attribute: its value and unit (cm, mm, in, pt).
func width(props *node, attr string) (float64, string) {
	if props == nil {
		return 0, ""
	}
	v := props.attr(attr)
	for _, u := range []string{"cm", "mm", "in", "pt", "pc"} {
		if n, ok := strings.CutSuffix(v, u); ok {
			f, err := strconv.ParseFloat(n, 64)
			if err == nil {
				return f, u
			}
		}
	}
	return 0, ""
}
