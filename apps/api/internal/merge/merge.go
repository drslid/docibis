package merge

import (
	"encoding/xml"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Checkboxes are drawn with Unicode characters instead of Wingdings glyphs.
const (
	checked   = "☑"
	unchecked = "☐"
)

// A tag is <<…>>; inside braces it may hold < and > (cs_{capital>60}).
// tagPattern finds <<tag>>: braces, and parentheses such as those of
// filter(price>1), may hold < and >.
var tagPattern = regexp.MustCompile(`<<((?:\((?:[^()<>]|<[^<(]|>[^>(])*\)|\{[^{}]*\}|[^<>{}])*)>>`)

func isParagraph(n *node) bool { return n.name == "text:p" || n.name == "text:h" }

// Elements whose text belongs to other paragraphs (frames, notes…).
var opaque = map[string]bool{
	"text:p": true, "text:h": true, "text:list": true, "text:note": true, "office:annotation": true,
	"draw:frame": true, "draw:custom-shape": true, "draw:g": true, "draw:rect": true, "draw:ellipse": true,
}

// texts returns the text nodes of a paragraph, in order, without the
// paragraphs of the frames anchored in it.
func texts(p *node) []*node {
	var out []*node
	var walk func(*node)
	walk = func(n *node) {
		for _, c := range n.children {
			switch {
			case c.kind == textNode:
				out = append(out, c)
			case c.kind == element && !opaque[c.name]:
				walk(c)
			}
		}
	}
	walk(p)
	return out
}

func joined(ts []*node) (string, []int) {
	var b strings.Builder
	starts := make([]int, len(ts))
	for i, t := range ts {
		starts[i] = b.Len()
		b.WriteString(t.text)
	}
	return b.String(), starts
}

func owner(starts []int, pos int) int {
	i := 0
	for i+1 < len(starts) && starts[i+1] <= pos {
		i++
	}
	return i
}

// normalize moves every tag split across several text nodes (Word cuts its
// runs anywhere) into the text node where it starts.
func normalize(root *node) {
	root.walk(func(n *node) bool {
		if !isParagraph(n) {
			return true
		}
		for {
			ts := texts(n)
			all, starts := joined(ts)
			moved := false
			for _, loc := range tagPattern.FindAllStringIndex(all, -1) {
				i, j := owner(starts, loc[0]), owner(starts, loc[1]-1)
				if i == j {
					continue
				}
				ts[i].text = ts[i].text[:loc[0]-starts[i]] + all[loc[0]:loc[1]]
				for k := i + 1; k < j; k++ {
					ts[k].text = ""
				}
				ts[j].text = ts[j].text[loc[1]-starts[j]:]
				moved = true
				break
			}
			if !moved {
				break
			}
		}
		return true
	})
}

// merger fills a document; now is the date of $nowUTC.
type merger struct {
	now      time.Time
	fetch    func(url string) ([]byte, error)
	mark     func(i int, tag string) string
	marked   int
	pictures []picture // images added to the package
	// copies counts the repeated rows and sections, bounded by maxCopies.
	copies int
	// breaks numbers the styles added (page breaks, wider columns).
	breaks int
	// dev highlights the fields that cannot be filled.
	dev bool
	err error
}

// maxCopies bounds the copies of repeated rows and sections in a document,
// whatever the size of the lists in the data.
const maxCopies = 100_000

// ErrTooLarge is a document whose repetitions go beyond maxCopies.
var ErrTooLarge = fmt.Errorf("plus de %d lignes ou sections répétées", maxCopies)

// listOf returns the items a repeat copies, none once the bound is reached.
func (m *merger) listOf(s scope, name string, copies int) []any {
	list := items(s, name)
	if m.copies += len(list) * max(copies, 1); m.copies > maxCopies {
		m.err = ErrTooLarge
		return nil
	}
	return list
}

// process fills a subtree: repeated rows and sections first, then
// conditional rows and sections, then images, fields and page breaks.
func (m *merger) process(root *node, s scope) {
	m.loops(root, s)
	m.conditionalRows(root, s)
	m.conditionalColumns(root, s)
	m.sections(root, s)
	m.images(root, s)
	m.pageBreaks(root, s)
	m.fields(root, s)
}

// occurrence is one tag of the document.
type occurrence struct {
	text  *node
	para  *node
	start int
	end   int
	raw   string
	tag   string
}

func occurrences(root *node) []occurrence {
	var out []occurrence
	var visit func(*node)
	visit = func(n *node) {
		if isParagraph(n) {
			for _, t := range texts(n) {
				for _, loc := range tagPattern.FindAllStringSubmatchIndex(t.text, -1) {
					out = append(out, occurrence{t, n, loc[0], loc[1], t.text[loc[0]:loc[1]], strings.TrimSpace(t.text[loc[2]:loc[3]])})
				}
			}
		}
	}
	if root.kind == element && root.name != "" {
		visit(root)
	}
	root.walk(func(n *node) bool { visit(n); return true })
	return out
}

// loops copies the repeated rows (<<rr_x>>…<<er_x>>) and the repeated
// sections (<<rs_x>>…<<es_x>>) once per item of the list x, outermost
// first, and fills each copy with its item.
func (m *merger) loops(root *node, s scope) {
	for {
		var first *occurrence
		for _, o := range occurrences(root) {
			if strings.HasPrefix(o.tag, "rs_") || strings.HasPrefix(o.tag, "rr_") && o.para.ancestor("table:table-row") != nil {
				oc := o
				first = &oc
				break
			}
		}
		if first == nil {
			return
		}
		if strings.HasPrefix(first.tag, "rr_") {
			m.repeatRows(*first, s)
		} else {
			m.repeatSection(root, *first, s)
		}
	}
}

// items returns the list a repeat goes through, filtered, sorted and grouped
// as its name asks.
func items(s scope, name string) []any {
	path, mods := listName(name)
	list, _ := s.lookup(path)
	out, _ := list.([]any)
	return arrange(out, mods, s)
}

// repeatRows copies the table rows between <<rr_x>> and <<er_x>>.
func (m *merger) repeatRows(o occurrence, s scope) {
	start, name := o.para.ancestor("table:table-row"), strings.TrimPrefix(o.tag, "rr_")
	// The end may repeat the modifiers of the start, or not.
	ends := []string{"er_" + name, "er_" + listPath(name)}
	isEnd := func(n *node) bool { return hasTag(n, ends[0]) || hasTag(n, ends[1]) }
	rows := []*node{start}
	end := start
	for !isEnd(end) {
		i := end.index() + 1
		if i >= len(end.parent.children) {
			break
		}
		end = end.parent.children[i]
		if end.kind == element {
			rows = append(rows, end)
		}
	}
	for _, r := range rows {
		stripTags(r, "rr_"+name, ends[0], ends[1])
	}
	// As Docmosis does, a row that only held a marker is not repeated.
	var kept []*node
	for i, r := range rows {
		if (i == 0 || i == len(rows)-1) && blank(r) {
			continue
		}
		kept = append(kept, r)
	}
	last := rows[len(rows)-1]
	list := m.listOf(s, name, len(kept))
	for i, item := range list {
		scoped := s.item(i, len(list), item)
		group := make([]*node, 0, len(kept))
		at := last
		for _, r := range kept {
			c := r.clone(nil)
			at.insertAfter(c)
			at = c
			group = append(group, c)
		}
		// The conditional rows of an item are decided on that item.
		left := m.conditionalRowsIn(group, scoped)
		if len(left) > 0 {
			last = left[len(left)-1]
		}
		for _, c := range left {
			m.process(c, scoped)
		}
	}
	for _, r := range rows {
		r.remove()
	}
}

// repeatSection copies what lies between <<rs_x>> and its <<es_x>>: the text
// between them in a paragraph, or the blocks between them (paragraphs,
// tables, list items), each copy filled with its item.
func (m *merger) repeatSection(root *node, open occurrence, s scope) {
	name := strings.TrimPrefix(open.tag, "rs_")
	var end *occurrence
	depth, started := 0, false
	for _, o := range occurrences(root) {
		if !started {
			if started = o.text == open.text && o.start == open.start; !started {
				continue
			}
		}
		switch {
		case strings.HasPrefix(o.tag, "rs_") || strings.HasPrefix(o.tag, "cs_"):
			depth++
		case strings.HasPrefix(o.tag, "es_"):
			depth--
		}
		if depth == 0 {
			oc := o
			end = &oc
			break
		}
	}
	if end == nil {
		open.text.text = strings.Replace(open.text.text, open.raw, "", 1)
		return
	}
	list := m.listOf(s, name, 1)
	if open.para == end.para {
		m.repeatInline(open, *end, list, s)
		return
	}
	top, a, b := commonAncestor(open.para, end.para)
	if top == nil {
		open.text.text = strings.Replace(open.text.text, open.raw, "", 1)
		end.text.text = strings.Replace(end.text.text, end.raw, "", 1)
		return
	}
	empty := func(n *node) bool {
		if isParagraph(n) {
			return blankContent(n)
		}
		return blank(n)
	}
	// What precedes <<rs_x>> and follows <<es_x>> stays once; the rest of the
	// marker blocks and the blocks between them repeat.
	before, first := cut(a, open, cutHead), cut(a, open, cutTail)
	last, after := cut(b, *end, cutHead), cut(b, *end, cutTail)
	var body []*node
	if !empty(first) {
		body = append(body, first)
	}
	body = append(body, top.children[a.index()+1:b.index()]...)
	if !empty(last) {
		body = append(body, last)
	}
	var out []*node
	if !empty(before) {
		out = append(out, before)
	}
	for i, item := range list {
		box := &node{kind: element, name: top.name}
		for _, n := range body {
			box.children = append(box.children, n.clone(box))
		}
		m.process(box, s.item(i, len(list), item))
		out = append(out, box.children...)
	}
	if !empty(after) {
		out = append(out, after)
	}
	if len(out) == 0 && top.name != "office:text" {
		// A cell, a frame or a header keeps one paragraph.
		out = append(out, cut(open.para, open, cutEmpty))
	}
	old := append([]*node(nil), top.children[a.index():b.index()+1]...)
	if len(out) > 0 {
		b.insertAfter(out...)
	}
	for _, n := range old {
		n.remove()
	}
}

// repeatInline repeats the text between two markers of one paragraph; the
// copies lose the formatting changes inside it.
func (m *merger) repeatInline(open, end occurrence, list []any, s scope) {
	ts := texts(open.para)
	var body strings.Builder
	inside := false
	for _, t := range ts {
		switch {
		case t == open.text && t == end.text:
			body.WriteString(t.text[open.end:end.start])
		case t == open.text:
			body.WriteString(t.text[open.end:])
			inside = true
		case t == end.text:
			body.WriteString(t.text[:end.start])
			inside = false
		case inside:
			body.WriteString(t.text)
		}
	}
	var out strings.Builder
	for i, item := range list {
		scoped := s.item(i, len(list), item)
		out.WriteString(tagPattern.ReplaceAllStringFunc(body.String(), func(raw string) string {
			v, _ := m.field(strings.TrimSpace(raw[2:len(raw)-2]), scoped)
			return v
		}))
	}
	if open.text == end.text {
		open.text.text = open.text.text[:open.start] + out.String() + open.text.text[end.end:]
		return
	}
	inside = false
	for _, t := range ts {
		switch {
		case t == open.text:
			t.text = t.text[:open.start] + out.String()
			inside = true
		case t == end.text:
			t.text = t.text[end.end:]
			return
		case inside:
			t.text = ""
		}
	}
}

// blank tells whether a subtree shows nothing: no text, no drawing.
func blank(n *node) bool {
	empty := true
	n.walk(func(c *node) bool {
		if strings.HasPrefix(c.name, "draw:") {
			empty = false
		}
		if isParagraph(c) && strings.TrimSpace(paragraphText(c)) != "" {
			empty = false
		}
		return empty
	})
	return empty
}

func hasTag(n *node, tag string) bool {
	for _, o := range occurrences(n) {
		if o.tag == tag {
			return true
		}
	}
	return false
}

func stripTags(n *node, tags ...string) {
	for _, o := range occurrences(n) {
		for _, t := range tags {
			if o.tag == t {
				o.text.text = strings.Replace(o.text.text, o.raw, "", 1)
			}
		}
	}
}

// sections keeps or removes what lies between <<cs_x>>, <<else_y>> (else
// if), <<else>> and <<es_x>>, inside a paragraph or across paragraphs.
func (m *merger) sections(root *node, s scope) {
	for {
		var stack []occurrence
		branches := map[int][]occurrence{}
		applied := false
		for _, o := range occurrences(root) {
			switch {
			case strings.HasPrefix(o.tag, "cs_"):
				stack = append(stack, o)
				delete(branches, len(stack)-1)
			case (o.tag == "else" || strings.HasPrefix(o.tag, "else_")) && len(stack) > 0:
				branches[len(stack)-1] = append(branches[len(stack)-1], o)
			case strings.HasPrefix(o.tag, "es_") && len(stack) > 0:
				open, list := stack[len(stack)-1], branches[len(stack)-1]
				switch {
				case slices.ContainsFunc(list, func(b occurrence) bool { return b.tag != "else" }):
					m.chain(open, list, o, s)
				case len(list) > 0:
					m.section(open, &list[len(list)-1], o, s)
				default:
					m.section(open, nil, o, s)
				}
				applied = true
			}
			if applied {
				break
			}
		}
		if !applied {
			return
		}
	}
}

// chain turns a section with else-if branches into a plain one: the branch
// to keep is chosen, the others are cut away, and the markers left are a
// <<cs_…>>, at most one <<else>> and the end, which sections then applies.
func (m *merger) chain(open occurrence, branches []occurrence, end occurrence, s scope) {
	holds := func(tag, prefix string) bool {
		expr := strings.TrimPrefix(tag, prefix)
		return condition(strings.TrimSuffix(strings.TrimPrefix(expr, "{"), "}"), s)
	}
	retag := func(o occurrence, tag string) {
		o.text.text = o.text.text[:o.start] + "<<" + tag + ">>" + o.text.text[o.end:]
	}
	kept := len(branches) // none
	if holds(open.tag, "cs_") {
		kept = -1
	} else {
		for i, b := range branches {
			if b.tag == "else" || holds(b.tag, "else_") {
				kept = i
				break
			}
		}
	}
	// From the end backwards, so that the positions of earlier tags stay right.
	switch {
	case kept == -1:
		drop(branches[0], end)
		retag(branches[0], "else")
	case kept == len(branches):
		drop(open, end)
		retag(open, "cs_{false}")
	default:
		if kept+1 < len(branches) {
			next := branches[kept+1]
			drop(next, end)
			next.text.text = next.text.text[:next.start] + next.text.text[next.end:]
			if p := next.para; p != end.para && p != branches[kept].para && p.parent != nil && blankContent(p) && removable(p) {
				p.remove()
			}
		}
		retag(branches[kept], "else")
		drop(open, branches[kept])
		retag(open, "cs_{false}")
	}
}

func (m *merger) section(open occurrence, els *occurrence, end occurrence, s scope) {
	expr := strings.TrimPrefix(open.tag, "cs_")
	expr = strings.TrimSuffix(strings.TrimPrefix(expr, "{"), "}")
	keep := condition(expr, s)
	if siblings(open, els, end) {
		blocks(open, els, end, keep)
		return
	}
	// The part to drop: between the start and else (or the end) when false,
	// between else and the end when true.
	var from, to occurrence
	switch {
	case !keep && els != nil:
		from, to = open, *els
	case !keep:
		from, to = open, end
	case els != nil:
		from, to = *els, end
	}
	paras := map[*node]bool{open.para: true, end.para: true}
	if els != nil {
		paras[els.para] = true
	}
	if from.text != nil {
		drop(from, to)
	}
	// Remove the tags that are left, then the paragraphs they leave empty.
	for _, o := range []*occurrence{&open, els, &end} {
		if o == nil {
			continue
		}
		o.text.text = strings.Replace(o.text.text, o.raw, "", 1)
	}
	for p := range paras {
		if p.parent != nil && strings.TrimSpace(paragraphText(p)) == "" && !hasDrawing(p) && removable(p) {
			p.remove()
		}
	}
}

// siblings tells whether the markers of a section sit in different
// paragraphs of the same parent, the common case of a section of blocks.
func siblings(open occurrence, els *occurrence, end occurrence) bool {
	if open.para == end.para || open.para.parent != end.para.parent {
		return false
	}
	return els == nil || (els.para != open.para && els.para != end.para && els.para.parent == open.para.parent)
}

// blocks applies a section of blocks the way Docmosis does, measured on its
// output: a marker alone in its paragraph removes the paragraph; otherwise
// the paragraph is cut at the marker, and a copy of the opening paragraph,
// emptied, follows the part that is kept. "?" below: unless the paragraph
// only holds its marker.
//
//	kept, no else:   A? body head(B)? empty(A)? tail(B)?
//	removed, no else: head(A)? tail(B)?
//	kept, else:      A? yes head(E)? empty(A)? empty(E)* tail(B)?
//	removed, else:   head(A)? tail(E)* no head(B)? empty(E)* tail(B)?
//
// where A has its marker removed and * means: when E has text after <<else>>.
func blocks(open occurrence, els *occurrence, end occurrence, keep bool) {
	a, b := open.para, end.para
	aSolo, bSolo := markerOnly(a, open), markerOnly(b, end)
	var out []*node
	add := func(ok bool, n *node) {
		if ok {
			out = append(out, n)
		}
	}
	between := func(x, y *node) []*node {
		var list []*node
		for _, n := range x.parent.children[x.index()+1 : y.index()] {
			list = append(list, n)
		}
		return list
	}
	if els == nil {
		if keep {
			add(!aSolo, cut(a, open, cutMarker))
			out = append(out, between(a, b)...)
			add(!bSolo, cut(b, end, cutHead))
			add(!aSolo, cut(a, open, cutEmpty))
			add(!bSolo, cut(b, end, cutTail))
		} else {
			add(!aSolo, cut(a, open, cutHead))
			add(!bSolo, cut(b, end, cutTail))
		}
	} else {
		e := els.para
		eSolo := markerOnly(e, *els)
		eTail := !blankContent(cut(e, *els, cutTail))
		if keep {
			add(!aSolo, cut(a, open, cutMarker))
			out = append(out, between(a, e)...)
			add(!eSolo, cut(e, *els, cutHead))
			add(!aSolo, cut(a, open, cutEmpty))
			add(eTail, cut(e, *els, cutEmpty))
			add(!bSolo, cut(b, end, cutTail))
		} else {
			add(!aSolo, cut(a, open, cutHead))
			add(eTail, cut(e, *els, cutTail))
			out = append(out, between(e, b)...)
			add(!bSolo, cut(b, end, cutHead))
			add(eTail, cut(e, *els, cutEmpty))
			add(!bSolo, cut(b, end, cutTail))
		}
	}
	// Replace the nodes from a to b with out.
	old := append([]*node{a}, between(a, b)...)
	old = append(old, b)
	moved := map[*node]bool{}
	for _, n := range out {
		moved[n] = true
	}
	parent, left := a.parent, countParagraphs(a.parent)
	for _, n := range old {
		if isParagraph(n) && !moved[n] {
			left--
		}
	}
	for _, n := range out {
		if isParagraph(n) && n.parent == nil {
			left++
		}
	}
	if left == 0 && parent.name != "office:text" {
		// A cell, a frame or a header keeps one paragraph.
		out = append(out, cut(a, open, cutEmpty))
	}
	for _, n := range out {
		n.remove()
	}
	if len(out) > 0 {
		a.insertAfter(out...)
	}
	for _, n := range old {
		if !moved[n] {
			n.remove()
		}
	}
}

type cutKind int

const (
	cutMarker cutKind = iota // the paragraph without its marker
	cutHead                  // what precedes the marker
	cutTail                  // what follows the marker
	cutEmpty                 // the paragraph emptied of its content
)

// cut returns a copy of paragraph p cut at the marker o.
func cut(p *node, o occurrence, kind cutKind) *node {
	var path []int
	for n := o.text; n != p; n = n.parent {
		path = append([]int{n.index()}, path...)
	}
	c := p.clone(nil)
	t := c
	for _, i := range path {
		t = t.children[i]
	}
	switch kind {
	case cutMarker:
		t.text = t.text[:o.start] + t.text[o.end:]
	case cutHead:
		t.text = t.text[:o.start]
		removeAfter(t, c)
	case cutTail:
		t.text = t.text[o.end:]
		removeBefore(t, c)
	case cutEmpty:
		t.text = ""
		removeAfter(t, c)
		removeBefore(t, c)
	}
	return c
}

// neutral elements show nothing by themselves.
var neutral = map[string]bool{
	"text:span": true, "text:a": true, "text:bookmark": true, "text:bookmark-start": true, "text:bookmark-end": true,
	"text:soft-page-break": true, "text:reference-mark": true, "text:reference-mark-start": true, "text:reference-mark-end": true,
}

// blankContent tells whether a paragraph holds no text (not even a space)
// and nothing else that shows.
func blankContent(p *node) bool {
	blank := true
	var visit func(*node)
	visit = func(n *node) {
		for _, c := range n.children {
			switch {
			case !blank:
				return
			case c.kind == textNode && c.text != "":
				blank = false
			case c.kind == element && !neutral[c.name]:
				blank = false
			case c.kind == element:
				visit(c)
			}
		}
	}
	visit(p)
	return blank
}

// markerOnly tells whether paragraph p holds nothing but the marker o.
func markerOnly(p *node, o occurrence) bool {
	return blankContent(cut(p, o, cutMarker))
}

func countParagraphs(n *node) int {
	count := 0
	for _, c := range n.children {
		if c.kind == element && isParagraph(c) {
			count++
		}
	}
	return count
}

// drop removes the content from the end of tag a to the start of tag b,
// whether they are in the same paragraph or not.
func drop(a, b occurrence) {
	if a.para == b.para {
		ts := texts(a.para)
		inside := false
		for _, t := range ts {
			switch {
			case t == a.text && t == b.text:
				t.text = t.text[:a.end] + t.text[b.start:]
				return
			case t == a.text:
				t.text = t.text[:a.end]
				inside = true
			case t == b.text:
				t.text = t.text[b.start:]
				return
			case inside:
				t.text = ""
			}
		}
		return
	}
	// Different paragraphs: cut the end of the first, the start of the last,
	// and every block between them.
	a.text.text = a.text.text[:a.end]
	after := false
	for _, t := range texts(a.para) {
		if after {
			t.text = ""
		}
		if t == a.text {
			after = true
		}
	}
	b.text.text = b.text.text[b.start:]
	for _, t := range texts(b.para) {
		if t == b.text {
			break
		}
		t.text = ""
	}
	top, c1, c2 := commonAncestor(a.para, b.para)
	if top == nil {
		return
	}
	removeAfter(a.para, c1)
	removeBefore(b.para, c2)
	i, j := c1.index(), c2.index()
	for _, n := range append([]*node(nil), top.children[i+1:j]...) {
		n.remove()
	}
}

// commonAncestor returns the lowest common ancestor of a and b, and its
// children that contain a and b.
func commonAncestor(a, b *node) (top, ca, cb *node) {
	path := map[*node]*node{}
	for child, p := a, a.parent; p != nil; child, p = p, p.parent {
		path[p] = child
	}
	for child, p := b, b.parent; p != nil; child, p = p, p.parent {
		if c, ok := path[p]; ok {
			return p, c, child
		}
	}
	return nil, nil, nil
}

// removeAfter removes what follows n inside its ancestor stop, at each level
// between them; the siblings of stop are left to the caller.
func removeAfter(n, stop *node) {
	for cur := n; cur != stop && cur.parent != nil; cur = cur.parent {
		i := cur.index()
		for _, s := range append([]*node(nil), cur.parent.children[i+1:]...) {
			s.remove()
		}
	}
}

// removeBefore removes what precedes n inside its ancestor stop.
func removeBefore(n, stop *node) {
	for cur := n; cur != stop && cur.parent != nil; cur = cur.parent {
		i := cur.index()
		for _, s := range append([]*node(nil), cur.parent.children[:i]...) {
			s.remove()
		}
	}
}

func paragraphText(p *node) string {
	all, _ := joined(texts(p))
	return all
}

func hasDrawing(p *node) bool {
	found := false
	p.walk(func(n *node) bool {
		if strings.HasPrefix(n.name, "draw:") {
			found = true
		}
		return !found
	})
	return found
}

// removable is false for the last paragraph of a cell, a frame or a note,
// which must keep one.
func removable(p *node) bool {
	count := 0
	for _, c := range p.parent.children {
		if c.kind == element && isParagraph(c) {
			count++
		}
	}
	return count > 1 || p.parent.name == "office:text"
}

// fields replaces the remaining tags with their values, from the last one so
// that the positions of the others stay right. A code (QR code, barcode)
// becomes an image in the line, <<link:x>> a hyperlink; <<op:x>> removes its
// paragraph when x is blank.
func (m *merger) fields(root *node, s scope) {
	all := occurrences(root)
	first := m.marked
	m.marked += len(all)
	optional := map[*node]bool{}
	// A copy filled apart keeps its page breaks for the document.
	deferred := top(root).name != ""
	for i := len(all) - 1; i >= 0; i-- {
		o := all[i]
		if deferred && o.tag == "pageBreak" {
			continue
		}
		if m.mark != nil {
			o.text.text = o.text.text[:o.start] + m.mark(first+i, o.tag) + o.text.text[o.end:]
			continue
		}
		kind, tag := fieldPrefix(quotes.Replace(o.tag))
		value, c := m.field(tag, s)
		var inline *node
		switch {
		case c != nil:
			inline = m.codeFrame(*c)
		case kind == "link":
			inline, value = hyperlink(value)
		case kind == "op" && strings.TrimSpace(value) == "":
			optional[o.para] = true
		}
		if m.dev && inline == nil && c == nil {
			if p := m.problem(o.tag, s); p != "" {
				inline = devSpan(p)
			}
		}
		if inline == nil {
			o.text.text = o.text.text[:o.start] + value + o.text.text[o.end:]
			continue
		}
		after := &node{kind: textNode, text: o.text.text[o.end:]}
		o.text.text = o.text.text[:o.start]
		o.text.insertAfter(inline, after)
	}
	for p := range optional {
		switch {
		case p.parent == nil:
		case removable(p):
			p.remove()
		default:
			// The last paragraph of a cell or a frame stays, emptied.
			for _, t := range texts(p) {
				t.text = ""
			}
		}
	}
}

func (m *merger) field(tag string, s scope) (string, *code) {
	tag = quotes.Replace(tag)
	switch {
	case strings.HasPrefix(tag, "{") && strings.HasSuffix(tag, "}"):
		return expression(tag[1:len(tag)-1], s, m.now)
	case control(tag):
		return "", nil
	}
	path, options, _ := strings.Cut(tag, "{")
	v, _ := s.lookup(path)
	switch renderer := strings.ToLower(options); {
	case strings.Contains(renderer, "renderer=boolean"):
		if truthy(v) {
			return checked, nil
		}
		return unchecked, nil
	case strings.Contains(renderer, "renderer=yesno"):
		if truthy(v) {
			return "Oui", nil
		}
		return "Non", nil
	case strings.HasPrefix(renderer, "numformat="):
		if f, ok := number(v); ok {
			return formatNumber(f, optionValue(options), ""), nil
		}
	case strings.HasPrefix(renderer, "dateformat="):
		return formatDate(v, optionValue(options), "", m.now), nil
	}
	return text(v), nil
}

// optionValue reads the value of a field option: numFormat='#,##0.00'.
func optionValue(options string) string {
	_, v, _ := strings.Cut(strings.TrimSuffix(options, "}"), "=")
	return strings.Trim(strings.TrimSpace(v), `'"`)
}

// codeFrame draws a code and returns the frame that shows it in the line;
// nil when the value cannot be encoded (letters in an EAN…).
func (m *merger) codeFrame(c code) *node {
	data, err := c.image()
	if err != nil {
		return nil
	}
	p := picture{path: fmt.Sprintf("Pictures/docibis-%d.png", len(m.pictures)+1), mime: "image/png", data: data}
	m.pictures = append(m.pictures, p)
	attr := func(name, value string) xml.Attr {
		space, local, _ := strings.Cut(name, ":")
		return xml.Attr{Name: xml.Name{Space: space, Local: local}, Value: value}
	}
	cm := func(v float64) string { return strconv.FormatFloat(v, 'f', 3, 64) + "cm" }
	frame := &node{kind: element, name: "draw:frame", attrs: []xml.Attr{
		attr("draw:name", fmt.Sprintf("Code %d", len(m.pictures))), attr("text:anchor-type", "as-char"),
		attr("svg:width", cm(c.width)), attr("svg:height", cm(c.height)), attr("draw:z-index", "0"),
	}}
	frame.children = []*node{{kind: element, name: "draw:image", parent: frame, attrs: []xml.Attr{
		attr("xlink:href", p.path), attr("xlink:type", "simple"), attr("xlink:show", "embed"), attr("xlink:actuate", "onLoad"), attr("draw:mime-type", "image/png"),
	}}}
	return frame
}

// control tags never print anything.
func control(tag string) bool {
	for _, p := range []string{"cs_", "es_", "rr_", "er_", "rs_", "cr_", "cc_", "else_", "img_", "imgfit_", "imgstretch_", "##"} {
		if strings.HasPrefix(tag, p) {
			return true
		}
	}
	return tag == "else" || tag == "pageBreak" || tag == "pageBreakNotLast"
}
