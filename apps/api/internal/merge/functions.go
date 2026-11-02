package merge

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// More functions of the Docmosis expressions (text, logic, dates, numbers in
// words), with their number of arguments.
var moreFunctions = map[string][2]int{
	"toUpperCase": {1, 1}, "toLowerCase": {1, 1}, "titleCase": {1, 1}, "toSentence": {1, 1},
	"left": {2, 2}, "right": {2, 2}, "split": {3, 3}, "indexOf": {2, 2}, "charAt": {2, 2}, "char": {1, 1},
	"equalsIgnoreCase": {2, 2}, "replaceStr": {3, 4}, "replaceFirst": {3, 4}, "countStr": {2, 3},
	"toRoman": {1, 1}, "toAlpha": {1, 1}, "toAlpha2": {1, 1},
	"ceil": {1, 1}, "floor": {1, 1}, "isNumber": {1, 1}, "pow": {2, 2}, "sqrt": {1, 1},
	"ifBlank": {2, 2}, "isBlank": {1, 1}, "map": {3, -1}, "mapi": {3, -1},
	"dateAdd": {3, 5}, "dateDiff": {3, 4},
	"numToText": {1, 2}, "numToDollars": {1, 1}, "numToEuros": {1, 2},
}

func init() {
	for name, n := range moreFunctions {
		Functions[name] = n
	}
}

var errUnknown = errors.New("unknown function")

// more runs the functions of moreFunctions; errUnknown for any other name.
func more(name string, args []any, now time.Time) (any, error) {
	str := func(i int) string {
		if i < len(args) {
			return text(args[i])
		}
		return ""
	}
	num := func(i int) (float64, bool) {
		if i < len(args) {
			return number(args[i])
		}
		return 0, false
	}
	ignoreCase := func(i int) bool { return i < len(args) && truthy(args[i]) }
	switch name {
	case "toUpperCase":
		return strings.ToUpper(str(0)), nil
	case "toLowerCase":
		return strings.ToLower(str(0)), nil
	case "titleCase":
		return titleCase(str(0)), nil
	case "toSentence":
		return toSentence(str(0)), nil
	case "left", "right":
		r := []rune(str(0))
		n, _ := num(1)
		k := clamp(int(n), len(r))
		if name == "left" {
			return string(r[:k]), nil
		}
		return string(r[len(r)-k:]), nil
	case "split":
		parts := strings.Split(str(0), str(1))
		i, _ := num(2)
		if int(i) < 0 || int(i) >= len(parts) {
			return "", nil
		}
		return parts[int(i)], nil
	case "indexOf":
		i := strings.Index(str(0), str(1))
		if i > 0 {
			i = utf8.RuneCountInString(str(0)[:i])
		}
		return float64(i), nil
	case "charAt":
		r := []rune(str(0))
		i, _ := num(1)
		if int(i) < 0 || int(i) >= len(r) {
			return "", nil
		}
		return string(r[int(i)]), nil
	case "char":
		s := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(str(0)), "&#"), ";")
		var n int64
		var err error
		if h, ok := strings.CutPrefix(strings.ToLower(s), "x"); ok {
			n, err = strconv.ParseInt(h, 16, 32)
		} else if h, ok := strings.CutPrefix(strings.ToLower(s), "0x"); ok {
			n, err = strconv.ParseInt(h, 16, 32)
		} else {
			n, err = strconv.ParseInt(s, 10, 32)
		}
		if err != nil || n <= 0 || !utf8.ValidRune(rune(n)) {
			return "", nil
		}
		return string(rune(n)), nil
	case "equalsIgnoreCase":
		return strings.EqualFold(str(0), str(1)), nil
	case "replaceStr", "replaceFirst":
		count := -1
		if name == "replaceFirst" {
			count = 1
		}
		return replaceText(str(0), str(1), str(2), count, ignoreCase(3)), nil
	case "countStr":
		s, find := str(0), str(1)
		if find == "" {
			return float64(0), nil
		}
		if ignoreCase(2) {
			s, find = strings.ToLower(s), strings.ToLower(find)
		}
		return float64(strings.Count(s, find)), nil
	case "toRoman":
		n, ok := num(0)
		if !ok || n < 1 || n >= 4000 {
			return str(0), nil
		}
		return roman(int(n)), nil
	case "toAlpha", "toAlpha2":
		n, ok := num(0)
		if !ok || n < 1 {
			return str(0), nil
		}
		return alpha(int(n), name == "toAlpha2"), nil
	case "ceil", "floor", "sqrt":
		f, ok := num(0)
		if !ok {
			return str(0), nil
		}
		switch name {
		case "ceil":
			return math.Ceil(f), nil
		case "floor":
			return math.Floor(f), nil
		}
		if f < 0 {
			return nil, errors.New("racine carrée d'un nombre négatif")
		}
		return math.Sqrt(f), nil
	case "pow":
		a, ok1 := num(0)
		b, ok2 := num(1)
		if !ok1 || !ok2 {
			return nil, errors.New("pow : deux nombres attendus")
		}
		return math.Pow(a, b), nil
	case "isNumber":
		_, ok := num(0)
		return ok, nil
	case "ifBlank":
		if blankValue(args[0]) {
			return args[1], nil
		}
		return args[0], nil
	case "isBlank":
		return blankValue(args[0]), nil
	case "map", "mapi":
		v := str(0)
		pairs := args[1:]
		for i := 0; i+1 < len(pairs); i += 2 {
			in := text(pairs[i])
			if v == in || name == "mapi" && strings.EqualFold(v, in) {
				return pairs[i+1], nil
			}
		}
		if len(pairs)%2 == 1 {
			return pairs[len(pairs)-1], nil
		}
		return "", nil
	case "dateAdd":
		t, ok := parseDate(args[0], str(4))
		if !ok {
			return str(0), nil
		}
		n, ok := num(1)
		if !ok {
			return nil, errors.New("dateAdd : nombre attendu")
		}
		t, err := addToDate(t, int(n), str(2))
		if err != nil {
			return nil, err
		}
		if out := str(3); out != "" {
			return formatDate(t, out, "", now), nil
		}
		return t, nil
	case "dateDiff":
		a, ok1 := parseDate(args[0], str(3))
		b, ok2 := parseDate(args[1], str(3))
		if !ok1 || !ok2 {
			return "", nil
		}
		return dateDiff(a, b, str(2))
	case "numToText", "numToDollars", "numToEuros":
		f, ok := num(0)
		if !ok {
			return str(0), nil
		}
		mode := str(1)
		switch {
		case name == "numToDollars":
			return amountInWords(f, "en", "dollar"), nil
		case name == "numToEuros" && strings.HasPrefix(strings.ToLower(mode), "en"):
			return amountInWords(f, "en", "euro"), nil
		case name == "numToEuros":
			return amountInWords(f, "fr", "euro"), nil
		}
		return numberInWords(f, mode), nil
	}
	return nil, errUnknown
}

// blankValue is no data, or text made of spaces only.
func blankValue(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(t) == ""
	}
	return false
}

func titleCase(s string) string {
	var b strings.Builder
	start := true
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) && start:
			b.WriteRune(unicode.ToUpper(r))
			start = false
		case unicode.IsLetter(r):
			b.WriteRune(unicode.ToLower(r))
		default:
			b.WriteRune(r)
			start = unicode.IsSpace(r) || r == '-'
		}
	}
	return b.String()
}

// toSentence capitalises the first letter of the text and of each sentence.
func toSentence(s string) string {
	var b strings.Builder
	start := true
	for _, r := range s {
		if start && unicode.IsLetter(r) {
			b.WriteRune(unicode.ToUpper(r))
			start = false
			continue
		}
		b.WriteRune(r)
		if r == '.' || r == '!' || r == '?' {
			start = true
		}
	}
	return b.String()
}

func replaceText(s, find, with string, count int, fold bool) string {
	if find == "" {
		return s
	}
	if !fold {
		return strings.Replace(s, find, with, count)
	}
	var b strings.Builder
	lower, lowFind := strings.ToLower(s), strings.ToLower(find)
	for count != 0 {
		i := strings.Index(lower, lowFind)
		if i < 0 || len(lower) != len(s) {
			break
		}
		b.WriteString(s[:i])
		b.WriteString(with)
		s, lower = s[i+len(find):], lower[i+len(lowFind):]
		count--
	}
	b.WriteString(s)
	return b.String()
}

func roman(n int) string {
	values := []int{1000, 900, 500, 400, 100, 90, 50, 40, 10, 9, 5, 4, 1}
	symbols := []string{"m", "cm", "d", "cd", "c", "xc", "l", "xl", "x", "ix", "v", "iv", "i"}
	var b strings.Builder
	for i, v := range values {
		for n >= v {
			b.WriteString(symbols[i])
			n -= v
		}
	}
	return b.String()
}

// alpha numbers with letters: a…z then aa, bb… (toAlpha) or aa, ab…
// (toAlpha2, like spreadsheet columns).
func alpha(n int, columns bool) string {
	if !columns {
		return strings.Repeat(string(rune('a'+(n-1)%26)), (n-1)/26+1)
	}
	var out []byte
	for n > 0 {
		n--
		out = append([]byte{byte('a' + n%26)}, out...)
		n /= 26
	}
	return string(out)
}

// parseDate reads a date: a time, an ISO 8601 or French text, or a text in
// the Java pattern given (dd/MM/yyyy, yyyyMMdd…).
func parseDate(v any, pattern string) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t, true
	case nil:
		return time.Time{}, false
	}
	s := strings.TrimSpace(text(v))
	layouts := []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02", "02/01/2006"}
	if pattern != "" {
		layouts = []string{javaLayout(pattern)}
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// javaLayout turns the numeric fields of a Java date pattern into a Go layout.
func javaLayout(p string) string {
	return strings.NewReplacer("yyyy", "2006", "yy", "06", "MM", "01", "dd", "02", "HH", "15", "mm", "04", "ss", "05", "SSS", "000").Replace(p)
}

func unit(s string) string {
	u := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), "s")
	if u == "milli" {
		return "millis"
	}
	return u
}

func addToDate(t time.Time, n int, units string) (time.Time, error) {
	switch unit(units) {
	case "millis":
		return t.Add(time.Duration(n) * time.Millisecond), nil
	case "second":
		return t.Add(time.Duration(n) * time.Second), nil
	case "minute":
		return t.Add(time.Duration(n) * time.Minute), nil
	case "hour":
		return t.Add(time.Duration(n) * time.Hour), nil
	case "day":
		return t.AddDate(0, 0, n), nil
	case "week":
		return t.AddDate(0, 0, 7*n), nil
	case "month":
		return addMonths(t, n), nil
	case "year":
		return addMonths(t, 12*n), nil
	}
	return t, fmt.Errorf("unité de date inconnue : %q (days, weeks, months, years…)", units)
}

// addMonths keeps the day, or takes the last day of a shorter month
// (31 January + 1 month = 28 or 29 February), as Java does.
func addMonths(t time.Time, n int) time.Time {
	first := time.Date(t.Year(), t.Month(), 1, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location()).AddDate(0, n, 0)
	last := first.AddDate(0, 1, -1).Day()
	return first.AddDate(0, 0, min(t.Day(), last)-1)
}

// dateDiff is b minus a in whole units.
func dateDiff(a, b time.Time, units string) (any, error) {
	d := b.Sub(a)
	switch unit(units) {
	case "millis":
		return float64(d.Milliseconds()), nil
	case "second":
		return math.Trunc(d.Seconds()), nil
	case "minute":
		return math.Trunc(d.Minutes()), nil
	case "hour":
		return math.Trunc(d.Hours()), nil
	case "day":
		return math.Trunc(d.Hours() / 24), nil
	case "week":
		return math.Trunc(d.Hours() / 24 / 7), nil
	case "month", "year":
		months := (b.Year()-a.Year())*12 + int(b.Month()) - int(a.Month())
		switch {
		case months > 0 && addMonths(a, months).After(b):
			months--
		case months < 0 && addMonths(a, months).Before(b):
			months++
		}
		if unit(units) == "year" {
			return float64(months / 12), nil
		}
		return float64(months), nil
	}
	return nil, fmt.Errorf("unité de date inconnue : %q (days, weeks, months, years…)", units)
}
