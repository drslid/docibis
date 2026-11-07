package merge

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Spreadsheets (ODS prepared from Excel): a cell that only holds a field gets
// a number or a date when its value is one, so that formulas and charts read
// it; formulas, charts and print ranges follow the rows copied by <<rr_x>>.

// Family is the kind of an OpenDocument file: "text", "spreadsheet",
// "presentation", or "" when unknown.
func Family(odf []byte) string {
	zr, err := zip.NewReader(bytes.NewReader(odf), int64(len(odf)))
	if err != nil {
		return ""
	}
	return familyOf(zr)
}

func familyOf(zr *zip.Reader) string {
	for _, f := range zr.File {
		if f.Name != "mimetype" {
			continue
		}
		m, err := read(f)
		if err != nil {
			return ""
		}
		for _, family := range []string{"text", "spreadsheet", "presentation"} {
			if strings.HasPrefix(string(m), "application/vnd.oasis.opendocument."+family) {
				return family
			}
		}
	}
	return ""
}

// Attributes set on the rows and cells while a sheet is filled, removed after.
const (
	rowAttr   = "docibis:row"
	itemAttr  = "docibis:item"
	valueAttr = "docibis:value"
)

// maxRows is the last row of a sheet in LibreOffice.
const maxRows = 1 << 20

// sheets lists the sheets of a spreadsheet body.
func sheets(root *node) []*node {
	var out []*node
	root.walk(func(n *node) bool {
		if n.name == "table:table" && n.parent != nil && n.parent.name == "office:spreadsheet" {
			out = append(out, n)
			return false
		}
		return true
	})
	return out
}

// rowsOf visits the rows of a sheet in order, inside their groups.
func rowsOf(table *node, visit func(row *node)) {
	for _, c := range table.children {
		switch c.name {
		case "table:table-row":
			visit(c)
		case "table:table-header-rows", "table:table-rows", "table:table-row-group":
			rowsOf(c, visit)
		}
	}
}

// markSheets numbers the rows as they are in the template and marks the
// cells that only hold a field. It returns, by sheet, the rows spanned by
// each row element of the template, by first row.
func markSheets(root *node) map[string]map[int]int {
	template := map[string]map[int]int{}
	for _, table := range sheets(root) {
		spans := map[int]int{}
		r := 1
		rowsOf(table, func(row *node) {
			row.setAttr(rowAttr, strconv.Itoa(r))
			n := repeated(row, "table:number-rows-repeated")
			spans[r] = n
			r += n
		})
		template[table.attr("table:name")] = spans
	}
	root.walk(func(n *node) bool {
		if n.name == "table:table-cell" && valueField(n) {
			n.setAttr(valueAttr, "1")
		}
		return true
	})
	return template
}

// valueField tells a cell whose text is one field, besides the markers of
// rows and sections: its value may be a number or a date. A field with a
// format gives text.
func valueField(cell *node) bool {
	var text strings.Builder
	for _, p := range cell.children {
		if isParagraph(p) {
			for _, t := range texts(p) {
				text.WriteString(t.text)
			}
		}
	}
	fields := 0
	rest := tagPattern.ReplaceAllStringFunc(text.String(), func(raw string) string {
		tag := strings.TrimSpace(tagPattern.FindStringSubmatch(raw)[1])
		if !control(tag) {
			fields++
			if path, options, _ := strings.Cut(tag, "{"); path != "" && options != "" || strings.Contains(tag, ":") {
				fields++
			}
		}
		return ""
	})
	return fields == 1 && strings.TrimSpace(rest) == ""
}

var (
	jsonNumber = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$`)
	isoDate    = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}(?:T[0-9]{2}:[0-9]{2}(?::[0-9]{2}(?:\.[0-9]+)?)?)?$`)
)

// rowRun is a row of the filled sheet: the template row it comes from, the
// rows it spans, where it now starts, and the item of the repeat that made it.
type rowRun struct {
	orig, count, at int
	item            string
}

// sheetRows maps the rows of the template to those of the filled sheet.
type sheetRows struct {
	starts []int         // first template row of each row element, in order
	counts map[int]int   // rows spanned by the element starting at a row
	copies map[int][]int // its copies in runs, in order
	runs   []rowRun
}

// row gives the filled row of template row r. A range starts at the first
// copy and ends at the last; a reference made from a repeated row reads its
// own copy. ok is false when the row went (empty list): a range then keeps
// the rows around it.
func (s *sheetRows) row(r int, end bool, item string) (int, bool) {
	i, found := slices.BinarySearch(s.starts, r)
	if !found {
		i--
	}
	if i < 0 {
		return r, true
	}
	start := s.starts[i]
	offset := r - start
	copies := s.copies[start]
	if offset >= s.counts[start] {
		return r, true
	}
	if item != "" {
		for _, c := range copies {
			if s.runs[c].item == item {
				return s.runs[c].at + offset, true
			}
		}
	}
	if len(copies) == 0 {
		if end {
			for j := i - 1; j >= 0; j-- {
				if c := s.copies[s.starts[j]]; len(c) > 0 {
					last := s.runs[c[len(c)-1]]
					return last.at + last.count - 1, false
				}
			}
		} else {
			for _, next := range s.starts[i+1:] {
				if c := s.copies[next]; len(c) > 0 {
					return s.runs[c[0]].at, false
				}
			}
		}
		return r, false
	}
	c := s.runs[copies[0]]
	if end {
		c = s.runs[copies[len(copies)-1]]
	}
	return min(c.at+offset, maxRows), true
}

// finishSheets types the values of the cells that only held a field, maps
// the rows of the template (template, from markSheets) to those filled,
// moves the references of formulas and ranges, and removes the marks. It
// returns the row maps by sheet name, for the charts.
func finishSheets(root *node, template map[string]map[int]int) map[string]*sheetRows {
	maps := map[string]*sheetRows{}
	for _, table := range sheets(root) {
		name := table.attr("table:name")
		s := &sheetRows{counts: template[name], copies: map[int][]int{}}
		if s.counts == nil {
			s.counts = map[int]int{}
		}
		for start := range s.counts {
			s.starts = append(s.starts, start)
		}
		slices.Sort(s.starts)
		at := 1
		var last *node
		rowsOf(table, func(row *node) {
			orig, _ := strconv.Atoi(row.attr(rowAttr))
			n := repeated(row, "table:number-rows-repeated")
			s.copies[orig] = append(s.copies[orig], len(s.runs))
			s.runs = append(s.runs, rowRun{orig: orig, count: n, at: at, item: row.attr(itemAttr)})
			at += n
			last = row
		})
		// The empty rows LibreOffice writes up to the end of the sheet make
		// room for the rows added.
		if over := at - 1 - maxRows; over > 0 && last != nil {
			if n := repeated(last, "table:number-rows-repeated"); n > over {
				last.setAttr("table:number-rows-repeated", strconv.Itoa(n-over))
			}
		}
		maps[name] = s
	}
	root.walk(func(n *node) bool {
		if n.name == "table:table-cell" && n.attr(valueAttr) != "" {
			typeValue(n)
		}
		if f := n.attr("table:formula"); f != "" && n.name == "table:table-cell" {
			sheet, item := "", ""
			if t := n.ancestor("table:table"); t != nil {
				sheet = t.attr("table:name")
			}
			if row := n.ancestor("table:table-row"); row != nil {
				item = row.attr(itemAttr)
			}
			n.setAttr("table:formula", formulaRefs.ReplaceAllStringFunc(f, func(ref string) string {
				return moveAddresses(ref, sheet, item, maps)
			}))
		}
		moveRanges(n, maps)
		return true
	})
	root.walk(func(n *node) bool {
		n.attrs = slices.DeleteFunc(n.attrs, func(a xml.Attr) bool { return a.Name.Space == "docibis" })
		return true
	})
	return maps
}

// typeValue makes a cell that only held a field a number or a date when its
// text is one; it stays text otherwise.
func typeValue(cell *node) {
	var text strings.Builder
	for _, p := range cell.children {
		if isParagraph(p) {
			for _, t := range texts(p) {
				text.WriteString(t.text)
			}
		}
	}
	v := strings.TrimSpace(text.String())
	kind, attr := "", ""
	switch {
	case jsonNumber.MatchString(v):
		kind, attr = "float", "office:value"
	case isoDate.MatchString(v):
		kind, attr = "date", "office:date-value"
	default:
		return
	}
	cell.attrs = slices.DeleteFunc(cell.attrs, func(a xml.Attr) bool {
		return prefixed(a.Name) == "office:string-value"
	})
	cell.setAttr("office:value-type", kind)
	cell.setAttr(attr, v)
	if cell.attr("calcext:value-type") != "" {
		cell.setAttr("calcext:value-type", kind)
	}
}

// rangeAttrs hold lists of cell addresses, such as "Sheet1.A2:Sheet1.B5".
var rangeAttrs = map[string]bool{
	"table:cell-range-address": true, "chart:values-cell-range-address": true, "chart:label-cell-address": true,
	"draw:notify-on-update-of-ranges": true, "table:print-ranges": true, "table:target-range-address": true,
	"calcext:target-range-address": true, "table:base-cell-address": true,
}

func moveRanges(n *node, maps map[string]*sheetRows) {
	for i, a := range n.attrs {
		if rangeAttrs[prefixed(a.Name)] {
			n.attrs[i].Value = moveAddresses(a.Value, "", "", maps)
		}
	}
}

var (
	formulaRefs = regexp.MustCompile(`\[[^\[\]]*\]`)
	// A cell address: the sheet (maybe quoted, maybe absolute), the column and the row.
	address = regexp.MustCompile(`(\$?(?:'(?:[^']|'')*'|[^\s.'\[\]:;,()"=]*))\.(\$?[A-Z]{1,3}\$?)([0-9]+)`)
)

// moveAddresses rewrites the rows of the addresses of s: a sheet left out is
// sheet (a formula) or the one of the start of the range. A range or a cell
// whose rows all went points to the last row, empty: a total gives 0.
func moveAddresses(s, sheet, item string, maps map[string]*sheetRows) string {
	locs := address.FindAllStringSubmatchIndex(s, -1)
	if len(locs) == 0 {
		return s
	}
	var out strings.Builder
	prev := 0
	put := func(loc []int, row int) {
		out.WriteString(s[prev:loc[6]])
		out.WriteString(strconv.Itoa(row))
		prev = loc[7]
	}
	for i := 0; i < len(locs); i++ {
		loc := locs[i]
		current := sheet
		if name := sheetName(s[loc[2]:loc[3]]); name != "" {
			current = name
		}
		m := maps[current]
		// From another sheet, a reference reads the whole repeat.
		own := item
		if current != sheet {
			own = ""
		}
		first, _ := strconv.Atoi(s[loc[6]:loc[7]])
		pair := i+1 < len(locs) && loc[1] < len(s) && s[loc[1]] == ':' && locs[i+1][0] == loc[1]+1
		if !pair {
			if m != nil {
				r, ok := m.row(first, false, own)
				if !ok {
					r = maxRows
				}
				first = r
			}
			put(loc, first)
			continue
		}
		end := locs[i+1]
		i++
		last, _ := strconv.Atoi(s[end[6]:end[7]])
		if name := sheetName(s[end[2]:end[3]]); m != nil && (name == "" || name == current) {
			a, okA := m.row(first, false, own)
			b, okB := m.row(last, true, own)
			if (!okA || !okB) && a > b {
				a, b = maxRows, maxRows
			}
			first, last = a, b
		}
		put(loc, first)
		put(end, last)
	}
	out.WriteString(s[prev:])
	return out.String()
}

// sheetName reads the sheet of an address: $'Ma feuille' -> Ma feuille.
func sheetName(s string) string {
	s = strings.TrimPrefix(s, "$")
	if strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'") && len(s) >= 2 {
		return strings.ReplaceAll(s[1:len(s)-1], "''", "'")
	}
	return s
}

// slideNames fills the fields of the names of the slides: LibreOffice names
// a slide after its title.
func (m *merger) slideNames(root *node, s scope) {
	root.walk(func(n *node) bool {
		if n.name == "draw:page" && strings.Contains(n.attr("draw:name"), "<<") {
			n.setAttr("draw:name", tagPattern.ReplaceAllStringFunc(n.attr("draw:name"), func(raw string) string {
				tag := strings.TrimSpace(tagPattern.FindStringSubmatch(raw)[1])
				if control(tag) {
					return ""
				}
				v, _ := m.field(tag, s)
				return v
			}))
		}
		return n.name != "draw:page"
	})
}
