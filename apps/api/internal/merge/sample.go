package merge

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Sample builds example data from the tags of an ODT template, as Docmosis
// does: a text for each field, alternating checkboxes, sections shown, two
// items for each repeated row, and a grey band for each image.
func Sample(template []byte) (map[string]any, error) {
	zr, err := zip.NewReader(bytes.NewReader(template), int64(len(template)))
	if err != nil {
		return nil, fmt.Errorf("template is not an ODT: %w", err)
	}
	data := map[string]any{}
	box := false
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
			return nil, err
		}
		normalize(tree)
		tree.walk(func(n *node) bool {
			if name := n.attr("text:name"); n.name == "text:bookmark-start" && strings.HasPrefix(name, "imgfit_") {
				set(data, strings.TrimPrefix(name, "imgfit_"), exampleImage())
			}
			if spec, object := chartField(n), chartObject(n); n.name == "draw:frame" && spec != "" && object != "" {
				path, fields := chartSpec(spec)
				set(data, path, chartSample(zr, object, fields))
			}
			return true
		})
		var lists []map[string]any // the item each repeat fills
		var blocks []string
		var rows []string // what each er_ closes: list (rr_) or condition (cr_)
		for _, o := range occurrences(tree) {
			tag := quotes.Replace(o.tag)
			target := data
			if len(lists) > 0 {
				target = lists[len(lists)-1]
			}
			switch {
			case strings.HasPrefix(tag, "rr_"):
				lists = append(lists, item(target, strings.TrimPrefix(tag, "rr_")))
				rows = append(rows, "list")
			case strings.HasPrefix(tag, "er_") && len(rows) > 0:
				if rows[len(rows)-1] == "list" && len(lists) > 0 {
					lists = lists[:len(lists)-1]
				}
				rows = rows[:len(rows)-1]
			case strings.HasPrefix(tag, "rs_"):
				lists = append(lists, item(target, strings.TrimPrefix(tag, "rs_")))
				blocks = append(blocks, "list")
			case strings.HasPrefix(tag, "es_") && len(blocks) > 0:
				if blocks[len(blocks)-1] == "list" && len(lists) > 0 {
					lists = lists[:len(lists)-1]
				}
				blocks = blocks[:len(blocks)-1]
			case strings.HasPrefix(tag, "cs_"), strings.HasPrefix(tag, "cr_"), strings.HasPrefix(tag, "cc_"):
				if strings.HasPrefix(tag, "cs_") {
					blocks = append(blocks, "section")
				} else if strings.HasPrefix(tag, "cr_") {
					rows = append(rows, "condition")
				}
				expr := strings.Trim(tag[3:], "{}")
				if e, err := parseExpr(expr); err == nil && !simple(e) {
					paths(e, "", func(p, _ string) { set(target, p, true) })
					continue
				}
				path, value := expr, any(true)
				if left, right, ok := strings.Cut(expr, "="); ok {
					path = strings.TrimRight(left, "!=")
					value = literal(strings.TrimLeft(right, "="))
				}
				set(target, strings.TrimPrefix(path, "!"), value)
			case strings.HasPrefix(tag, "{"):
				if e, err := parseExpr(strings.Trim(tag, "{}")); err == nil {
					paths(e, "", func(p, fn string) { set(target, p, exampleFor(p, fn, e)) })
				}
			case control(tag), strings.HasPrefix(tag, "$"):
			case strings.HasPrefix(tag, "ref:"), strings.HasPrefix(tag, "refLookup:"), strings.HasPrefix(tag, "refLookupOp:"):
				// A shared block: no example names one of the library.
			case strings.Contains(tag, "{renderer="):
				path, _, _ := strings.Cut(tag, "{")
				box = !box
				set(target, path, box)
			default:
				kind, rest := fieldPrefix(tag)
				path, options, _ := strings.Cut(rest, "{")
				switch o := strings.ToLower(options); {
				case kind == "link":
					set(target, path, "Exemple|https://www.exemple.fr")
				case strings.HasPrefix(o, "numformat="):
					set(target, path, json.Number("1234.5"))
				case strings.HasPrefix(o, "dateformat="):
					set(target, path, "2026-10-19")
				default:
					set(target, path, example(path))
				}
			}
		}
	}
	for _, v := range data {
		if list, ok := v.([]any); ok && len(list) == 2 {
			second := list[1].(map[string]any)
			for k, v := range list[0].(map[string]any) {
				if s, ok := v.(string); ok {
					v = s + " (2)"
				}
				second[k] = v
			}
		}
	}
	return data, nil
}

var lastPart = regexp.MustCompile(`[^.\[\]]+$`)

// simple tells whether a condition is a path or a comparison of a path
// with a value, which the sample fills with that value.
func simple(e expr) bool {
	switch x := e.(type) {
	case pathExpr:
		return true
	case unaryExpr:
		_, ok := x.x.(pathExpr)
		return ok && x.op == "!"
	case binaryExpr:
		_, l := x.l.(pathExpr)
		return l && (x.op == "=" || x.op == "==" || x.op == "!=")
	}
	return false
}

// exampleFor gives a value that suits the function reading the path: a
// number for numFormat, a date for dateFormat, an address for a QR code.
func exampleFor(path, fn string, e expr) any {
	switch fn {
	case "numFormat", "round", "abs", "min", "max", "ceil", "floor", "numToText", "numToDollars", "numToEuros":
		return json.Number("1234.5")
	case "dateFormat", "dateAdd", "dateDiff":
		return "2026-10-19"
	case "qrcode":
		return "https://www.exemple.fr/verifier/123456"
	case "barcode":
		if c, ok := e.(callExpr); ok && len(c.args) > 1 {
			if l, ok := c.args[1].(literalExpr); ok && strings.HasPrefix(strings.ToLower(text(l.value)), "ean") {
				return "4006381333931"
			}
		}
		return "DOCIBIS-123456"
	}
	return example(path)
}

func example(path string) string {
	return "Exemple " + lastPart.FindString(strings.TrimSuffix(path, "]"))
}

func literal(s string) any {
	s = strings.TrimSpace(s)
	switch {
	case s == "true":
		return true
	case s == "false":
		return false
	}
	return strings.Trim(s, `'"`)
}

// item returns the element of a list where the fields of a repeat go; the
// list gets two items, the second a copy of the first.
func item(data map[string]any, name string) map[string]any {
	name = listPath(name)
	if name == groupItems {
		// The items of a group are those of the list grouped.
		return data
	}
	list, _ := data[name].([]any)
	if len(list) == 0 {
		list = []any{map[string]any{}, map[string]any{}}
		data[name] = list
	}
	return list[0].(map[string]any)
}

func set(target map[string]any, path string, value any) {
	parts := strings.Split(strings.TrimSpace(path), ".")
	for _, p := range parts[:len(parts)-1] {
		next, ok := target[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			target[p] = next
		}
		target = next
	}
	if _, exists := target[parts[len(parts)-1]]; !exists {
		target[parts[len(parts)-1]] = value
	}
}
