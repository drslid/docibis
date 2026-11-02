package merge

import (
	"archive/zip"
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Analysis tells what a template uses and which engine can render it: the
// overlay (fixed layout: the data never moves the rest), the native engine
// (the text flows, without positioned shapes) or the standard one.
type Analysis struct {
	Fields        int      `json:"fields"`
	FlowFields    int      `json:"flowFields"` // followed by text on their line
	Checkboxes    int      `json:"checkboxes"`
	Sections      int      `json:"sections"`
	BlockSections int      `json:"blockSections"` // across paragraphs
	Repeats       int      `json:"repeats"`
	Images        int      `json:"images"`
	Shapes        int      `json:"shapes"` // drawings and text boxes
	Tables        int      `json:"tables"`
	Unsupported   []string `json:"unsupported,omitempty"`
	Engine        string   `json:"engine"` // overlay, native or standard
	Reason        string   `json:"reason"`
}

var plainPath = regexp.MustCompile(`^\$?[\pL\pN_.\[\]]+$`)

// readable tells whether an expression can be read and only calls known
// functions.
func readable(src string) bool {
	e, err := parseExpr(src)
	return err == nil && check(e) == nil
}

// drawsCode tells whether an expression draws a QR code or a barcode.
func drawsCode(e expr) bool {
	c, ok := e.(callExpr)
	return ok && (c.name == "qrcode" || c.name == "barcode")
}

// Analyze reads the tags and the drawings of a template prepared as ODT.
func Analyze(template []byte) (Analysis, error) {
	var a Analysis
	zr, err := zip.NewReader(bytes.NewReader(template), int64(len(template)))
	if err != nil {
		return a, fmt.Errorf("template is not an ODT: %w", err)
	}
	unsupported := map[string]bool{}
	for _, f := range zr.File {
		if !parts[f.Name] {
			continue
		}
		raw, err := read(f)
		if err != nil {
			return a, err
		}
		tree, err := parse(raw)
		if err != nil {
			return a, fmt.Errorf("%s: %w", f.Name, err)
		}
		normalize(tree)
		tree.walk(func(n *node) bool {
			switch {
			case n.name == "table:table":
				a.Tables++
			case n.name == "draw:custom-shape" || (n.name == "draw:frame" && has(n, "draw:text-box") && !floatingTable(n)):
				a.Shapes++
			case n.name == "text:bookmark-start" && strings.HasPrefix(n.attr("text:name"), "imgfit_"):
				a.Images++
			}
			return true
		})
		var open []occurrence
		for _, o := range occurrences(tree) {
			tag := quotes.Replace(o.tag)
			path, options, _ := strings.Cut(tag, "{")
			switch {
			case strings.HasPrefix(tag, "cs_"):
				a.Sections++
				open = append(open, o)
				if expr := strings.TrimPrefix(tag, "cs_"); strings.HasPrefix(expr, "{") && !readable(strings.Trim(expr, "{}")) {
					unsupported[tag] = true
				}
			case strings.HasPrefix(tag, "rs_"):
				a.Repeats++
				open = append(open, o)
			case strings.HasPrefix(tag, "es_") && len(open) > 0:
				start := open[len(open)-1]
				open = open[:len(open)-1]
				if start.para != o.para {
					a.BlockSections++
				} else if flows(o) {
					a.FlowFields++
				}
			case tag == "else", strings.HasPrefix(tag, "##"):
			case strings.HasPrefix(tag, "cr_"), strings.HasPrefix(tag, "cc_"), tag == "pageBreak", tag == "pageBreakNotLast":
				// Rows or columns that may go, or a new page: what follows moves.
				a.BlockSections++
				if expr := tag[min(3, len(tag)):]; (strings.HasPrefix(tag, "cr_") || strings.HasPrefix(tag, "cc_")) && strings.HasPrefix(expr, "{") && !readable(strings.Trim(expr, "{}")) {
					unsupported[tag] = true
				}
			case strings.HasPrefix(tag, "else_"):
				if expr := strings.TrimPrefix(tag, "else_"); strings.HasPrefix(expr, "{") && !readable(strings.Trim(expr, "{}")) {
					unsupported[tag] = true
				}
			case strings.HasPrefix(tag, "op:"):
				// The paragraph may go: what follows moves up.
				a.BlockSections++
			case strings.HasPrefix(tag, "link:"), strings.HasPrefix(tag, "link_"):
				a.Fields++
				a.FlowFields++
			case strings.HasPrefix(tag, "rr_"):
				a.Repeats++
			case strings.HasPrefix(tag, "er_"):
			case strings.HasPrefix(tag, "{"):
				switch e, err := parseExpr(strings.Trim(tag, "{}")); {
				case err != nil || check(e) != nil:
					unsupported[tag] = true
				case drawsCode(e):
					a.Images++
				default:
					a.Fields++
				}
			case control(tag), !plainPath.MatchString(path):
				unsupported[tag] = true
			case options != "":
				switch r := strings.ToLower(options); {
				case strings.Contains(r, "renderer=boolean"):
					a.Checkboxes++
				case strings.Contains(r, "renderer=yesno"), strings.HasPrefix(r, "numformat="), strings.HasPrefix(r, "dateformat="):
					a.Fields++
				default:
					unsupported[tag] = true
				}
			default:
				a.Fields++
				if flows(o) {
					a.FlowFields++
				}
			}
		}
	}
	for t := range unsupported {
		a.Unsupported = append(a.Unsupported, t)
	}
	sort.Strings(a.Unsupported)
	switch {
	case len(a.Unsupported) > 0:
		a.Engine, a.Reason = "standard", "syntaxe pas encore prise en charge : "+strings.Join(a.Unsupported, ", ")
	case a.Repeats == 0 && a.BlockSections == 0 && a.FlowFields == 0:
		a.Engine, a.Reason = "overlay", "mise en page fixe : "+count(a.Fields, "champ en bout de ligne", "champs en bout de ligne")+", "+
			count(a.Checkboxes, "case", "cases")+", "+count(a.Images, "image", "images")
	case a.Shapes == 0:
		a.Engine, a.Reason = "native", reasons(a, "le texte coule")
	default:
		a.Engine, a.Reason = "standard", reasons(a, count(a.Shapes, "forme positionnée", "formes positionnées"))
	}
	return a, nil
}

func count(n int, one, many string) string {
	if n > 1 {
		return fmt.Sprintf("%d %s", n, many)
	}
	return fmt.Sprintf("%d %s", n, one)
}

func reasons(a Analysis, first string) string {
	r := []string{first}
	if a.Repeats > 0 {
		r = append(r, count(a.Repeats, "ligne répétée", "lignes répétées"))
	}
	if a.BlockSections > 0 {
		r = append(r, count(a.BlockSections, "section de paragraphes", "sections de paragraphes"))
	}
	if a.FlowFields > 0 {
		r = append(r, count(a.FlowFields, "champ suivi de texte", "champs suivis de texte"))
	}
	return strings.Join(r, ", ")
}

func has(n *node, name string) bool {
	found := false
	n.walk(func(c *node) bool {
		found = found || c.name == name
		return !found
	})
	return found
}

// floatingTable tells whether a frame only holds a table: a Word table with
// text wrapping, which flows like a table.
func floatingTable(frame *node) bool {
	tables := 0
	for _, box := range frame.children {
		if box.name != "draw:text-box" {
			continue
		}
		for _, c := range box.children {
			switch {
			case c.name == "table:table":
				tables++
			case c.kind == element && !(isParagraph(c) && blankContent(c)):
				return false
			}
		}
	}
	return tables > 0
}

// flows tells whether visible text follows the tag on its line: a longer
// value would move it. A tab or a line break stops the search: what follows
// a tab stays at the tab stop.
func flows(o occurrence) bool {
	if strings.TrimSpace(o.text.text[o.end:]) != "" {
		return true
	}
	after := false
	result := false
	var visit func(n *node) bool
	visit = func(n *node) bool {
		for _, c := range n.children {
			switch {
			case c == o.text:
				after = true
			case !after:
				if c.kind == element && !opaque[c.name] && !visit(c) {
					return false
				}
			case c.name == "text:tab" || c.name == "text:line-break":
				return false
			case c.kind == textNode && strings.TrimSpace(c.text) != "":
				result = true
				return false
			case c.kind == element && !opaque[c.name]:
				if !visit(c) {
					return false
				}
			}
		}
		return true
	}
	visit(o.para)
	return result
}
