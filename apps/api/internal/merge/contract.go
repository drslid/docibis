package merge

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
)

// Field is a piece of data a template reads: its path in the JSON ("client.nom",
// "lignes[].montant"), its kind (text, flag, list, image) and whether it is
// read only inside conditional sections, where it may be absent.
type Field struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Optional bool   `json:"optional"`
}

// Fields lists the data of an ODT template, in the order of the document.
func Fields(template []byte) ([]Field, error) {
	zr, err := zip.NewReader(bytes.NewReader(template), int64(len(template)))
	if err != nil {
		return nil, fmt.Errorf("template is not an ODT: %w", err)
	}
	var out []Field
	seen := map[string]int{}
	add := func(path, kind string, optional bool) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		if i, ok := seen[path]; ok {
			// Read once outside any section: the field is needed.
			out[i].Optional = out[i].Optional && optional
			return
		}
		seen[path] = len(out)
		out = append(out, Field{Path: path, Kind: kind, Optional: optional})
	}
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
				add(strings.TrimPrefix(name, "imgfit_"), "image", true)
			}
			return true
		})
		var lists []string
		var blocks []string // what each es_ closes: section or list
		var rows []string   // what each er_ closes: list (rr_) or condition (cr_)
		sections := 0
		for _, o := range occurrences(tree) {
			tag := quotes.Replace(o.tag)
			prefix := ""
			if len(lists) > 0 {
				prefix = strings.Join(lists, "[].") + "[]."
			}
			switch {
			case strings.HasPrefix(tag, "rr_"):
				name := strings.TrimPrefix(tag, "rr_")
				add(prefix+name, "list", sections > 0)
				lists = append(lists, name)
				rows = append(rows, "list")
			case strings.HasPrefix(tag, "er_") && len(rows) > 0:
				if rows[len(rows)-1] == "list" && len(lists) > 0 {
					lists = lists[:len(lists)-1]
				}
				rows = rows[:len(rows)-1]
			case strings.HasPrefix(tag, "rs_"):
				name := strings.TrimPrefix(tag, "rs_")
				add(prefix+name, "list", sections > 0)
				lists = append(lists, name)
				blocks = append(blocks, "list")
			case strings.HasPrefix(tag, "cs_"), strings.HasPrefix(tag, "cr_"), strings.HasPrefix(tag, "cc_"):
				expr := strings.Trim(tag[3:], "{}")
				if e, err := parseExpr(expr); err == nil && check(e) == nil {
					paths(e, "", func(p, _ string) { add(prefix+p, "flag", true) })
				} else {
					path, _, _ := strings.Cut(expr, "=")
					add(prefix+strings.TrimRight(path, "!"), "flag", true)
				}
				if strings.HasPrefix(tag, "cs_") {
					sections++
					blocks = append(blocks, "section")
				} else if strings.HasPrefix(tag, "cr_") {
					rows = append(rows, "condition")
				}
			case strings.HasPrefix(tag, "else_"):
				if e, err := parseExpr(strings.Trim(strings.TrimPrefix(tag, "else_"), "{}")); err == nil && check(e) == nil {
					paths(e, "", func(p, _ string) { add(prefix+p, "flag", true) })
				}
			case strings.HasPrefix(tag, "es_") && len(blocks) > 0:
				if blocks[len(blocks)-1] == "list" && len(lists) > 0 {
					lists = lists[:len(lists)-1]
				} else if sections > 0 {
					sections--
				}
				blocks = blocks[:len(blocks)-1]
			case strings.HasPrefix(tag, "{"):
				if e, err := parseExpr(strings.Trim(tag, "{}")); err == nil {
					paths(e, "", func(p, _ string) { add(prefix+p, "text", sections > 0) })
				}
			case control(tag), strings.HasPrefix(tag, "$"):
			case strings.Contains(tag, "{renderer="):
				path, _, _ := strings.Cut(tag, "{")
				add(prefix+path, "flag", true)
			default:
				add(prefix+fieldPath(tag), "text", sections > 0)
			}
		}
	}
	return out, nil
}

// fieldPath keeps the path of a tag, without its format ({dateFormat=…}).
func fieldPath(tag string) string {
	_, tag = fieldPrefix(tag)
	if i := strings.IndexAny(tag, "{|"); i >= 0 {
		tag = tag[:i]
	}
	return strings.TrimSpace(tag)
}

// Missing lists the needed fields that the data does not have. Flags and
// images may be absent (unchecked, no image); an empty list has no rows to fill.
func Missing(fields []Field, data map[string]any) []string {
	var out []string
	for _, f := range fields {
		if f.Optional || f.Kind == "flag" || f.Kind == "image" {
			continue
		}
		if missing([]map[string]any{data}, strings.Split(f.Path, "[].")) {
			out = append(out, f.Path)
		}
	}
	return out
}

// missing looks a path up in the innermost scope first: in a repeated row,
// a name is the item's or else the one of the data around it.
func missing(scopes []map[string]any, segments []string) bool {
	var value any
	found := false
	for _, scope := range scopes {
		if value, found = lookup(scope, segments[0]); found {
			break
		}
	}
	if !found {
		return true
	}
	if len(segments) == 1 {
		return false
	}
	items, _ := value.([]any)
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok || missing(append([]map[string]any{m}, scopes...), segments[1:]) {
			return true
		}
	}
	return false
}

func lookup(data map[string]any, path string) (any, bool) {
	var value any = data
	for _, part := range strings.Split(path, ".") {
		m, ok := value.(map[string]any)
		if !ok {
			return nil, false
		}
		if value, ok = m[part]; !ok {
			return nil, false
		}
	}
	return value, true
}
