package merge

import (
	"slices"
	"strings"
	"time"
)

// A repeat may filter, sort and group its list, as Docmosis does:
// <<rs_products:filter(type='Fruit'):sort(DESC,name)>> or <<rr_lines:group(type)>>,
// a group giving $groupKey and its items in $groupItems.
type modifier struct{ kind, arg string }

const groupItems = "$groupItems"

// listName splits the name of a repeat into the path of its list and its
// modifiers; a part that is not a known modifier stays in the path.
func listName(name string) (string, []modifier) {
	name = strings.TrimSpace(name)
	path, rest, found := strings.Cut(name, ":")
	if !found {
		return name, nil
	}
	var mods []modifier
	for rest != "" {
		kind, after, ok := strings.Cut(rest, "(")
		kind = strings.ToLower(strings.TrimSpace(kind))
		if !ok || (kind != "filter" && kind != "sort" && kind != "group") {
			return name, nil
		}
		arg, tail, ok := closing(after)
		if !ok {
			return name, nil
		}
		mods = append(mods, modifier{kind, strings.Trim(strings.TrimSpace(arg), "{}")})
		rest = strings.TrimSpace(tail)
		if rest != "" {
			if !strings.HasPrefix(rest, ":") {
				return name, nil
			}
			rest = rest[1:]
		}
	}
	return strings.TrimSpace(path), mods
}

// listPath is the path of the list a repeat goes through.
func listPath(name string) string {
	path, _ := listName(name)
	return path
}

// closing splits s at the parenthesis that closes an opened one, quotes
// and nested parentheses included.
func closing(s string) (string, string, bool) {
	depth, quote := 0, rune(0)
	for i, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"' || r == '‘' || r == '’':
			quote = r
			if r == '‘' {
				quote = '’'
			}
		case r == '(':
			depth++
		case r == ')':
			if depth == 0 {
				return s[:i], s[i+1:], true
			}
			depth--
		}
	}
	return "", "", false
}

// arrange applies the modifiers of a repeat to its list, in their order.
func arrange(list []any, mods []modifier, s scope) []any {
	for _, m := range mods {
		switch m.kind {
		case "filter":
			var kept []any
			for i, item := range list {
				if condition(m.arg, s.item(i, len(list), item)) {
					kept = append(kept, item)
				}
			}
			list = kept
		case "sort":
			list = sortList(list, m.arg, s)
		case "group":
			list = groupList(list, m.arg, s)
		}
	}
	return list
}

// sortList sorts by fields or expressions, ascending unless DESC comes
// before them: sort(name), sort(DESC,price), sort(type,DESC,price).
func sortList(list []any, arg string, s scope) []any {
	type key struct {
		expr string
		desc bool
	}
	var keys []key
	desc := false
	for _, part := range strings.Split(arg, ",") {
		part = strings.TrimSpace(part)
		switch strings.ToUpper(part) {
		case "":
			continue
		case "DESC":
			desc = true
			continue
		case "ASC":
			desc = false
			continue
		}
		keys = append(keys, key{part, desc})
	}
	values := make([][]any, len(list))
	for i, item := range list {
		values[i] = make([]any, len(keys))
		for k, kk := range keys {
			v, _ := evaluate(kk.expr, s.item(i, len(list), item), time.Time{})
			values[i][k] = value(v)
		}
	}
	order := make([]int, len(list))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int {
		for k, kk := range keys {
			c, ok := compare(values[a][k], values[b][k])
			if !ok {
				// A missing value comes last, whatever the order.
				c = boolInt(values[a][k] == nil) - boolInt(values[b][k] == nil)
			} else if kk.desc {
				c = -c
			}
			if c != 0 {
				return c
			}
		}
		return 0
	})
	out := make([]any, len(list))
	for i, j := range order {
		out[i] = list[j]
	}
	return out
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// groupList gathers the items with the same key, in the order the keys come.
func groupList(list []any, arg string, s scope) []any {
	var out []any
	index := map[string]int{}
	for i, item := range list {
		v, _ := evaluate(arg, s.item(i, len(list), item), time.Time{})
		v = value(v)
		k := text(v)
		at, ok := index[k]
		if !ok {
			at = len(out)
			index[k] = at
			out = append(out, map[string]any{"$groupKey": v, "$groupItems": []any{}})
		}
		g := out[at].(map[string]any)
		g["$groupItems"] = append(g["$groupItems"].([]any), item)
	}
	return out
}
