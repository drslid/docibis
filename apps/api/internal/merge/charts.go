package merge

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// A chart named chart_x (its name, title or description, in Word,
// PowerPoint or LibreOffice) shows the field x of the data:
//
//	{"categories": ["T1", "T2"], "series": [{"name": "Ventes", "values": [120, 150]}]}
//
// or {"categories": [...], "values": [...]} for one series. Named
// chart_x(trimestre, montant), it reads a list of items instead, the same
// one as a table: the categories in the first field, a series in each
// other. The data go in the table of the chart, in its object
// (Object N/content.xml); the series beyond those of the template look like
// them, in turn. The charts of a sheet read the sheet and are left alone.

// chartMark is set on a chart frame once it is handled.
const chartMark = "docibis:chart"

var (
	objectRef     = regexp.MustCompile(`^(?:\./)?(Object [0-9]+)/?$`)
	objectPart    = regexp.MustCompile(`^Object ([0-9]+)/`)
	manifestEntry = regexp.MustCompile(`<manifest:file-entry\b[^>]*>`)
	fullPath      = regexp.MustCompile(`manifest:full-path="([^"]*)"`)
)

type chartSeries struct {
	name   string
	named  bool
	values []any
}

type chartData struct {
	categories []any
	series     []chartSeries
}

// chartFill is a chart of the document and the data it shows.
type chartFill struct {
	frame  *node
	object string
	data   chartData
}

// addedPart is a file added to the package: a copy of a chart object.
type addedPart struct {
	name string
	data []byte
	tree *node
}

// chartField returns x for a frame named, titled or described chart_x, or
// x(fields) for chart_x(fields).
func chartField(frame *node) string {
	names := []string{frame.attr("draw:name")}
	for _, c := range frame.children {
		if c.name == "svg:title" || c.name == "svg:desc" {
			names = append(names, innerText(c))
		}
	}
	for _, name := range names {
		if field, ok := strings.CutPrefix(strings.TrimSpace(name), "chart_"); ok && field != "" {
			return field
		}
	}
	return ""
}

// chartSpec splits x or x(category, series…) into the path of the data and
// the fields of the items of a list.
func chartSpec(spec string) (string, []string) {
	path, args, ok := strings.Cut(spec, "(")
	if !ok {
		return strings.TrimSpace(spec), nil
	}
	var fields []string
	for _, f := range strings.Split(strings.TrimSuffix(strings.TrimSpace(args), ")"), ",") {
		if f = strings.TrimSpace(f); f != "" {
			fields = append(fields, f)
		}
	}
	return strings.TrimSpace(path), fields
}

// chartObject returns the embedded object a frame shows: "Object 1".
func chartObject(frame *node) string {
	for _, c := range frame.children {
		if c.name == "draw:object" {
			if m := objectRef.FindStringSubmatch(c.attr("xlink:href")); m != nil {
				return m[1]
			}
		}
	}
	return ""
}

func innerText(n *node) string {
	var b strings.Builder
	var visit func(*node)
	visit = func(n *node) {
		for _, c := range n.children {
			switch c.kind {
			case textNode:
				b.WriteString(c.text)
			case element:
				visit(c)
			}
		}
	}
	visit(n)
	return b.String()
}

// charts finds the charts of a subtree and the data they show; a chart
// without data is removed, as an image is (kept in dev mode, to be seen).
func (m *merger) charts(root *node, s scope) {
	if m.sheet {
		return
	}
	var frames []*node
	root.walk(func(n *node) bool {
		if n.name == "draw:frame" && n.attr(chartMark) == "" && chartField(n) != "" && chartObject(n) != "" {
			frames = append(frames, n)
			return false
		}
		return true
	})
	for _, f := range frames {
		f.setAttr(chartMark, "1")
		path, fields := chartSpec(chartField(f))
		value, found := s.lookup(path)
		data, ok := chartDataOf(value, fields)
		if !found || !ok {
			if !m.dev {
				f.remove()
			}
			continue
		}
		if m.copies += len(data.categories) * len(data.series); m.copies > maxCopies {
			m.err = ErrTooLarge
			return
		}
		m.chartFills = append(m.chartFills, chartFill{frame: f, object: chartObject(f), data: data})
	}
}

// chartDataOf reads the categories and the series of a chart in the data:
// an object, or a list of items read through fields.
func chartDataOf(v any, fields []string) (chartData, bool) {
	if list, ok := v.([]any); ok && len(fields) >= 2 && len(list) > 0 {
		d := chartData{series: make([]chartSeries, len(fields)-1)}
		for k, f := range fields[1:] {
			d.series[k].name = f
		}
		for _, item := range list {
			category, _ := resolve(item, fields[0])
			d.categories = append(d.categories, category)
			for k, f := range fields[1:] {
				value, _ := resolve(item, f)
				d.series[k].values = append(d.series[k].values, value)
			}
		}
		return d, true
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return chartData{}, false
	}
	categories, ok := obj["categories"].([]any)
	if !ok || len(categories) == 0 {
		return chartData{}, false
	}
	d := chartData{categories: categories}
	if values, ok := obj["values"].([]any); ok && obj["series"] == nil {
		d.series = []chartSeries{{values: values}}
		return d, true
	}
	list, ok := obj["series"].([]any)
	if !ok || len(list) == 0 {
		return chartData{}, false
	}
	for _, item := range list {
		s, ok := item.(map[string]any)
		if !ok {
			return chartData{}, false
		}
		values, ok := s["values"].([]any)
		if !ok {
			return chartData{}, false
		}
		cs := chartSeries{values: values}
		if name, ok := s["name"]; ok && name != nil {
			cs.name, cs.named = text(name), true
		}
		d.series = append(d.series, cs)
	}
	return d, true
}

// placeCharts writes the data of each chart in its object. The copies of a
// chart made by a repeat share one object: each gets its own, numbered after
// the last one of the template. It returns the files to add and, for the
// manifest, the objects copied.
func (m *merger) placeCharts(zr *zip.Reader, trees map[string]*node) ([]addedPart, [][2]string, error) {
	if len(m.chartFills) == 0 {
		return nil, nil, nil
	}
	last := 0
	for _, f := range zr.File {
		if mm := objectPart.FindStringSubmatch(f.Name); mm != nil {
			if k, err := strconv.Atoi(mm[1]); err == nil {
				last = max(last, k)
			}
		}
	}
	// Charts of a section removed after they were found are not placed.
	present := map[*node]bool{}
	for name, tree := range trees {
		if parts[name] {
			tree.walk(func(n *node) bool {
				present[n] = present[n] || n.attr(chartMark) != ""
				return true
			})
		}
	}
	var added []addedPart
	var copies [][2]string
	templates := map[string]*node{}
	used := map[string]bool{}
	for _, c := range m.chartFills {
		key := c.object + "/content.xml"
		if !present[c.frame] || trees[key] == nil {
			continue
		}
		if templates[key] == nil {
			templates[key] = trees[key].clone(nil)
		}
		tree := templates[key].clone(nil)
		fillChart(tree, c.data)
		object := c.object
		if used[c.object] {
			last++
			object = fmt.Sprintf("Object %d", last)
			copies = append(copies, [2]string{c.object, object})
			for _, f := range zr.File {
				rest, inside := strings.CutPrefix(f.Name, c.object+"/")
				if !inside || rest == "" || strings.HasSuffix(rest, "/") {
					continue
				}
				a := addedPart{name: object + "/" + rest}
				if rest == "content.xml" {
					a.tree = tree
				} else {
					var err error
					if a.data, err = read(f); err != nil {
						return nil, nil, err
					}
				}
				added = append(added, a)
			}
		} else {
			trees[key] = tree
		}
		used[c.object] = true
		for _, child := range slices.Clone(c.frame.children) {
			switch child.name {
			case "draw:object":
				child.setAttr("xlink:href", "./"+object)
			case "draw:image":
				// The picture of the template's chart, shown when the object cannot be read.
				child.remove()
			}
		}
	}
	return added, copies, nil
}

// fillChart writes the data in the table of a chart and points its series,
// categories and labels at them. A chart without a table of its own reads a
// sheet: it is left as it is.
func fillChart(chart *node, d chartData) {
	table, plot := chartParts(chart)
	if table == nil || plot == nil {
		return
	}
	var series []*node
	for _, c := range plot.children {
		if c.name == "chart:series" {
			series = append(series, c)
		}
	}
	if len(series) == 0 {
		return
	}
	names := seriesNames(table)
	n, count := len(d.categories), len(d.series)
	last := n + 1
	template := len(series)
	for len(series) > count {
		series[len(series)-1].remove()
		series = series[:len(series)-1]
	}
	for i := len(series); i < count; i++ {
		source := series[i%template]
		c := source.clone(nil)
		if style := recolour(chart, source.attr("chart:style-name"), i, series[:template]); style != "" {
			c.setAttr("chart:style-name", style)
		}
		series[len(series)-1].insertAfter(c)
		series = append(series, c)
	}
	categories := fmt.Sprintf("local-table.$A$2:.$A$%d", last)
	for k, s := range series {
		col := column(k + 1)
		s.setAttr("chart:values-cell-range-address", fmt.Sprintf("local-table.$%s$2:.$%s$%d", col, col, last))
		s.setAttr("chart:label-cell-address", fmt.Sprintf("local-table.$%s$1", col))
		for _, c := range s.children {
			if c.name == "chart:domain" {
				c.setAttr("table:cell-range-address", categories)
			}
		}
		resizePoints(s, n)
	}
	plot.walk(func(c *node) bool {
		if c.name == "chart:categories" {
			c.setAttr("table:cell-range-address", categories)
		}
		return true
	})
	if plot.attr("table:cell-range-address") != "" {
		plot.setAttr("table:cell-range-address", fmt.Sprintf("local-table.$A$1:.$%s$%d", column(count), last))
	}
	writeTable(table, d, names)
}

// palette gives the series added to a chart the colours Office gives its
// own, those close to the template's series left out.
var palette = []string{"#ed7d31", "#a5a5a5", "#ffc000", "#5b9bd5", "#70ad47", "#4472c4", "#264478", "#9e480e"}

// recolour copies the style of a series for the series i added, with a
// colour of its own; it returns the name of the copy ("" without a style).
func recolour(chart *node, name string, i int, template []*node) string {
	styles := map[string]*node{}
	chart.walk(func(n *node) bool {
		if n.name == "style:style" {
			styles[n.attr("style:name")] = n
			return false
		}
		return true
	})
	style := styles[name]
	if name == "" || style == nil {
		return ""
	}
	var used []string
	for _, s := range template {
		if st := styles[s.attr("chart:style-name")]; st != nil {
			st.walk(func(n *node) bool {
				used = append(used, n.attr("draw:fill-color"), n.attr("svg:stroke-color"))
				return true
			})
		}
	}
	var free []string
	for _, c := range palette {
		if !slices.ContainsFunc(used, func(u string) bool { return near(c, u) }) {
			free = append(free, c)
		}
	}
	if len(free) == 0 {
		free = palette
	}
	colour := free[(i-len(template))%len(free)]
	copied := style.clone(nil)
	copiedName := fmt.Sprintf("%s-%d", name, i+1)
	copied.setAttr("style:name", copiedName)
	copied.walk(func(n *node) bool {
		if n.name == "style:graphic-properties" {
			for _, a := range []string{"draw:fill-color", "svg:stroke-color"} {
				if n.attr(a) != "" {
					n.setAttr(a, colour)
				}
			}
		}
		return true
	})
	style.insertAfter(copied)
	return copiedName
}

// near tells whether two colours #rrggbb look alike.
func near(a, b string) bool {
	x, errA := strconv.ParseUint(strings.TrimPrefix(a, "#"), 16, 32)
	y, errB := strconv.ParseUint(strings.TrimPrefix(b, "#"), 16, 32)
	if errA != nil || errB != nil || len(a) != 7 || len(b) != 7 {
		return false
	}
	distance := 0
	for shift := 0; shift <= 16; shift += 8 {
		d := int(x>>shift&0xff) - int(y>>shift&0xff)
		distance += max(d, -d)
	}
	return distance < 100
}

// chartParts returns the table of a chart and its plot area.
func chartParts(chart *node) (table, plot *node) {
	chart.walk(func(n *node) bool {
		switch {
		case n.name == "table:table" && n.attr("table:name") == "local-table":
			table = n
			return false
		case n.name == "chart:plot-area":
			plot = n
		}
		return true
	})
	return table, plot
}

// column names the column i of a table, from 0: A, B… Z, AA…
func column(i int) string {
	name := ""
	for i++; i > 0; i = (i - 1) / 26 {
		name = string(rune('A'+(i-1)%26)) + name
	}
	return name
}

// tableRows returns the rows of a part of a chart table (header or body).
func tableRows(table *node, part string) []*node {
	var rows []*node
	for _, c := range table.children {
		if c.name == part {
			for _, r := range c.children {
				if r.name == "table:table-row" {
					rows = append(rows, r)
				}
			}
		}
	}
	return rows
}

func rowCells(row *node) []*node {
	var cells []*node
	for _, c := range row.children {
		if c.name == "table:table-cell" {
			cells = append(cells, c)
		}
	}
	return cells
}

// seriesNames reads the names of the series in the header row of a chart table.
func seriesNames(table *node) []string {
	var names []string
	if rows := tableRows(table, "table:table-header-rows"); len(rows) > 0 {
		for i, cell := range rowCells(rows[0]) {
			if i > 0 {
				names = append(names, strings.TrimSpace(innerText(cell)))
			}
		}
	}
	return names
}

// resizePoints gives a series one data point per category: those of the
// template in turn (the colours of the slices of a pie chart).
func resizePoints(series *node, n int) {
	at := -1
	var points []*node
	for i, c := range series.children {
		if c.name != "chart:data-point" {
			continue
		}
		if at < 0 {
			at = i
		}
		repeat := 1
		if r, err := strconv.Atoi(c.attr("chart:repeated")); err == nil && r > 1 {
			repeat = min(r, n)
		}
		for range repeat {
			if len(points) == n {
				break
			}
			p := c.clone(nil)
			p.attrs = slices.DeleteFunc(p.attrs, func(a xml.Attr) bool { return prefixed(a.Name) == "chart:repeated" })
			points = append(points, p)
		}
	}
	if at < 0 {
		return
	}
	series.children = slices.DeleteFunc(series.children, func(c *node) bool { return c.name == "chart:data-point" })
	resized := make([]*node, n)
	for i := range resized {
		resized[i] = points[i%len(points)].clone(series)
	}
	series.children = slices.Insert(series.children, min(at, len(series.children)), resized...)
}

// writeTable replaces the table of a chart with the data: categories in the
// first column, one column per series, their names in the first row.
func writeTable(table *node, d chartData, names []string) {
	var headerColumns *node
	for _, c := range table.children {
		if c.name == "table:table-header-columns" {
			headerColumns = c
		}
	}
	if headerColumns == nil {
		headerColumns = newElement("table:table-header-columns", newElement("table:table-column"))
	}
	columns := newElement("table:table-column")
	columns.setAttr("table:number-columns-repeated", strconv.Itoa(len(d.series)))
	header := newElement("table:table-row", newElement("table:table-cell", newElement("text:p")))
	for k, s := range d.series {
		name := s.name
		if !s.named && k < len(names) {
			name = names[k]
		}
		header.add(stringCell(name))
	}
	rows := newElement("table:table-rows")
	for i, category := range d.categories {
		row := newElement("table:table-row", categoryCell(category))
		for _, s := range d.series {
			var v any
			if i < len(s.values) {
				v = s.values[i]
			}
			row.add(valueCell(v))
		}
		rows.add(row)
	}
	table.children = nil
	table.add(headerColumns, newElement("table:table-columns", columns), newElement("table:table-header-rows", header), rows)
}

func newElement(name string, children ...*node) *node {
	n := &node{kind: element, name: name}
	n.add(children...)
	return n
}

func (n *node) add(children ...*node) {
	for _, c := range children {
		c.parent = n
		n.children = append(n.children, c)
	}
}

func stringCell(s string) *node {
	c := newElement("table:table-cell", newElement("text:p", &node{kind: textNode, text: s}))
	c.setAttr("office:value-type", "string")
	return c
}

// valueCell holds a number; a value that is not one leaves the point out.
func valueCell(v any) *node {
	f, ok := number(v)
	if !ok {
		return newElement("table:table-cell", newElement("text:p"))
	}
	s := strconv.FormatFloat(f, 'f', -1, 64)
	c := newElement("table:table-cell", newElement("text:p", &node{kind: textNode, text: s}))
	c.setAttr("office:value-type", "float")
	c.setAttr("office:value", s)
	return c
}

// categoryCell keeps a number as a number (the x of a scatter chart).
func categoryCell(v any) *node {
	switch v.(type) {
	case json.Number, float64:
		return valueCell(v)
	}
	return stringCell(text(v))
}

// stripChartMarks removes the marks of the chart frames.
func stripChartMarks(tree *node) {
	tree.walk(func(n *node) bool {
		if n.name == "draw:frame" {
			n.attrs = slices.DeleteFunc(n.attrs, func(a xml.Attr) bool { return prefixed(a.Name) == chartMark })
		}
		return true
	})
}

// withObjects adds to the manifest the entries of the objects copied.
func withObjects(raw []byte, copies [][2]string) []byte {
	if len(copies) == 0 {
		return raw
	}
	var entries bytes.Buffer
	for _, c := range copies {
		for _, e := range manifestEntry.FindAll(raw, -1) {
			if p := fullPath.FindSubmatch(e); p != nil && strings.HasPrefix(string(p[1]), c[0]+"/") {
				entries.Write(bytes.Replace(e, []byte(`"`+c[0]+"/"), []byte(`"`+c[1]+"/"), 1))
			}
		}
	}
	end := bytes.LastIndex(raw, []byte("</manifest:manifest>"))
	if end < 0 {
		return raw
	}
	return append(append(append([]byte(nil), raw[:end]...), entries.Bytes()...), raw[end:]...)
}

// chartObjects lists the embedded objects of a package that are charts.
func chartObjects(zr *zip.Reader) map[string]bool {
	objects := map[string]bool{}
	for _, f := range zr.File {
		if f.Name != "META-INF/manifest.xml" {
			continue
		}
		raw, err := read(f)
		if err != nil {
			return objects
		}
		for _, e := range manifestEntry.FindAll(raw, -1) {
			if !bytes.Contains(e, []byte(`manifest:media-type="application/vnd.oasis.opendocument.chart"`)) {
				continue
			}
			if p := fullPath.FindSubmatch(e); p != nil {
				if m := objectRef.FindStringSubmatch(string(p[1])); m != nil {
					objects[m[1]] = true
				}
			}
		}
	}
	return objects
}

// HasCharts tells whether an OpenDocument file holds charts, which
// LibreOffice draws from their embedded objects.
func HasCharts(odf []byte) bool {
	zr, err := zip.NewReader(bytes.NewReader(odf), int64(len(odf)))
	return err == nil && len(chartObjects(zr)) > 0
}

// chartSample returns the data a chart of the template shows, in the form
// the data of a document gives them: an object, or the items of a list
// read through fields.
func chartSample(zr *zip.Reader, object string, fields []string) any {
	sample := chartData{categories: []any{"A", "B", "C"}, series: []chartSeries{{name: "Série 1", values: []any{1, 2, 3}}}}
	for _, f := range zr.File {
		if f.Name != object+"/content.xml" {
			continue
		}
		raw, err := read(f)
		if err != nil {
			break
		}
		tree, err := parse(raw)
		if err != nil {
			break
		}
		table, _ := chartParts(tree)
		if table == nil {
			break
		}
		names := seriesNames(table)
		var categories []any
		values := make([][]any, len(names))
		for _, row := range tableRows(table, "table:table-rows") {
			for i, cell := range rowCells(row) {
				v := any(strings.TrimSpace(innerText(cell)))
				if cell.attr("office:value-type") == "float" {
					if f, err := strconv.ParseFloat(cell.attr("office:value"), 64); err == nil {
						v = f
					}
				}
				switch {
				case i == 0:
					categories = append(categories, v)
				case i-1 < len(values):
					values[i-1] = append(values[i-1], v)
				}
			}
		}
		if len(categories) == 0 || len(names) == 0 {
			break
		}
		sample = chartData{categories: categories}
		for k, name := range names {
			sample.series = append(sample.series, chartSeries{name: name, values: values[k]})
		}
		break
	}
	if len(fields) >= 2 {
		items := make([]any, len(sample.categories))
		for i, category := range sample.categories {
			item := map[string]any{}
			set(item, fields[0], category)
			for k, f := range fields[1:] {
				if k < len(sample.series) && i < len(sample.series[k].values) {
					set(item, f, sample.series[k].values[i])
				}
			}
			items[i] = item
		}
		return items
	}
	series := make([]any, len(sample.series))
	for k, s := range sample.series {
		series[k] = map[string]any{"name": s.name, "values": s.values}
	}
	return map[string]any{"categories": sample.categories, "series": series}
}
