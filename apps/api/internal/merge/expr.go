package merge

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// scope is the data visible to a tag: the item of each enclosing repeat,
// then the root. A name is looked up from the innermost scope outwards.
type scope []any

func (s scope) push(v any) scope { return append(append(scope(nil), s...), v) }

// item is the scope of the i-th item of a repeat of n items: the item, then
// its number ($itemIdx from 0, $itemNum from 1, or $rowidx and $rownum as in
// Docmosis), $size and itself as $item.
func (s scope) item(i, n int, v any) scope {
	idx, num := json.Number(strconv.Itoa(i)), json.Number(strconv.Itoa(i+1))
	meta := map[string]any{"$itemIdx": idx, "$itemNum": num, "$rowidx": idx, "$rownum": num,
		"$size": json.Number(strconv.Itoa(n)), "$item": v}
	return s.push(meta).push(v)
}

// lookup resolves a path such as customer.address.city or items[0].name.
func (s scope) lookup(path string) (any, bool) {
	path = strings.TrimSpace(path)
	for i := len(s) - 1; i >= 0; i-- {
		if v, ok := resolve(s[i], path); ok {
			return v, true
		}
	}
	return nil, false
}

func resolve(v any, path string) (any, bool) {
	if path == "" {
		return v, true
	}
	for _, part := range strings.Split(path, ".") {
		name, idx, hasIdx := strings.Cut(part, "[")
		m, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		if v, ok = m[name]; !ok {
			return nil, false
		}
		if hasIdx {
			n, err := strconv.Atoi(strings.TrimSuffix(idx, "]"))
			list, ok := v.([]any)
			if err != nil || !ok || n < 0 || n >= len(list) {
				return nil, false
			}
			v = list[n]
		}
	}
	return v, true
}

// text renders a value as Docmosis does: strings as they are, numbers as
// written in the JSON, empty for null or missing values.
func text(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		return t.String()
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

// truthy decides a condition: true, a non-empty text other than
// "false"/"no"/"0", a non-zero number, a non-empty list or object.
func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		s := strings.ToLower(strings.TrimSpace(t))
		return s != "" && s != "false" && s != "no" && s != "n" && s != "0"
	case json.Number:
		f, err := t.Float64()
		return err == nil && f != 0
	case float64:
		return t != 0
	case []any:
		return len(t) > 0
	case map[string]any:
		return len(t) > 0
	}
	return true
}

// unquote turns Word's typographic quotes back into plain ones.
var quotes = strings.NewReplacer("‘", "'", "’", "'", "“", `"`, "”", `"`, "«", `"`, "»", `"`)

// condition evaluates the expression of cs_{…}: a path, a comparison such
// as isMinor=true, amount!=0 or status='ok', or any expression.
func condition(expr string, s scope) bool {
	if v, ok := evaluate(expr, s, time.Time{}); ok {
		return truthy(value(v))
	}
	expr = strings.TrimSpace(quotes.Replace(expr))
	for _, op := range []string{"!=", "==", "="} {
		left, right, ok := strings.Cut(expr, op)
		if !ok {
			continue
		}
		v, _ := s.lookup(left)
		equal := sameValue(v, strings.TrimSpace(right))
		if op == "!=" {
			return !equal
		}
		return equal
	}
	if rest, ok := strings.CutPrefix(expr, "!"); ok {
		v, _ := s.lookup(rest)
		return !truthy(v)
	}
	v, _ := s.lookup(expr)
	return truthy(v)
}

func sameValue(v any, literal string) bool {
	switch {
	case literal == "true" || literal == "false":
		return truthy(v) == (literal == "true")
	case len(literal) >= 2 && (literal[0] == '\'' || literal[0] == '"'):
		return text(v) == literal[1:len(literal)-1]
	}
	if n, err := strconv.ParseFloat(literal, 64); err == nil {
		f, err := strconv.ParseFloat(text(v), 64)
		return err == nil && f == n
	}
	return text(v) == literal
}

// expression evaluates <<{…}>>: a code to draw, or a text. An expression
// that cannot be read shows its path's value, as the first engine did.
func expression(expr string, s scope, now time.Time) (string, *code) {
	if v, ok := evaluate(expr, s, now); ok {
		if c, isCode := v.(code); isCode {
			return "", &c
		}
		return render(v), nil
	}
	v, _ := s.lookup(strings.TrimSpace(quotes.Replace(expr)))
	return text(v), nil
}
