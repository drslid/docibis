package merge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/boombuler/barcode"
	"github.com/boombuler/barcode/aztec"
	"github.com/boombuler/barcode/codabar"
	"github.com/boombuler/barcode/code128"
	"github.com/boombuler/barcode/code39"
	"github.com/boombuler/barcode/datamatrix"
	"github.com/boombuler/barcode/ean"
	"github.com/boombuler/barcode/pdf417"
	"github.com/boombuler/barcode/qr"
	"github.com/boombuler/barcode/twooffive"
)

// Expressions of <<{…}>> and cs_{…}, as Docmosis writes them: paths, numbers,
// 'texts', true/false/null, operators (+ - * / % == = != < <= > >= && || !)
// and functions such as numFormat(prime, '#,##0.00').

type expr interface{}

type literalExpr struct{ value any }
type pathExpr struct{ path string }
type unaryExpr struct {
	op string
	x  expr
}
type binaryExpr struct {
	op   string
	l, r expr
}
type callExpr struct {
	name string
	args []expr
}

// Functions lists the functions of the expressions, with their number of
// arguments (minimum, maximum; -1: any).
var Functions = map[string][2]int{
	"dateFormat": {2, 3}, "numFormat": {2, 3},
	"toUpper": {1, 1}, "toLower": {1, 1}, "trim": {1, 1}, "capitalize": {1, 1},
	"length": {1, 1}, "substring": {2, 3}, "replace": {3, 3}, "concat": {1, -1},
	"contains": {2, 2}, "startsWith": {2, 2}, "endsWith": {2, 2}, "isEmpty": {1, 1}, "isNotEmpty": {1, 1},
	"round": {1, 2}, "abs": {1, 1}, "min": {1, -1}, "max": {1, -1}, "sum": {1, 2}, "if": {3, 3},
	"qrcode": {1, 2}, "barcode": {2, 4},
}

type token struct {
	kind string // num, str, ident, op, end
	text string
}

func tokenize(s string) ([]token, error) {
	var out []token
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case unicode.IsSpace(r):
			i += size
		case r >= '0' && r <= '9' || r == '.' && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9':
			j := i
			for j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == '.') {
				j++
			}
			out = append(out, token{"num", s[i:j]})
			i = j
		case r == '\'' || r == '"':
			j := i + 1
			var b strings.Builder
			for ; j < len(s) && rune(s[j]) != r; j++ {
				if s[j] == '\\' && j+1 < len(s) {
					j++
				}
				b.WriteByte(s[j])
			}
			if j >= len(s) {
				return nil, errors.New("texte non fermé")
			}
			out = append(out, token{"str", b.String()})
			i = j + 1
		case unicode.IsLetter(r) || r == '_' || r == '$':
			j := i
			for j < len(s) {
				c, n := utf8.DecodeRuneInString(s[j:])
				if !(unicode.IsLetter(c) || unicode.IsDigit(c) || c == '_' || c == '$' || c == '.' || c == '[' || c == ']') {
					break
				}
				j += n
			}
			out = append(out, token{"ident", s[i:j]})
			i = j
		default:
			two := ""
			if i+1 < len(s) {
				two = s[i : i+2]
			}
			switch two {
			case "&&", "||", "==", "!=", "<=", ">=", "<>":
				out = append(out, token{"op", two})
				i += 2
				continue
			}
			if !strings.ContainsRune("+-*/%<>=!(),", r) {
				return nil, fmt.Errorf("caractère inattendu %q", r)
			}
			out = append(out, token{"op", string(r)})
			i += size
		}
	}
	return append(out, token{kind: "end"}), nil
}

type parser struct {
	tokens []token
	pos    int
}

func (p *parser) peek() token { return p.tokens[p.pos] }
func (p *parser) next() token { t := p.tokens[p.pos]; p.pos++; return t }

var precedence = map[string]int{"||": 1, "or": 1, "&&": 2, "and": 2, "==": 3, "=": 3, "!=": 3, "<>": 3, "<": 4, "<=": 4, ">": 4, ">=": 4, "+": 5, "-": 5, "*": 6, "/": 6, "%": 6}

// maxTokens bounds an expression: it is read and evaluated recursively.
const maxTokens = 2000

// parseExpr reads an expression; it fails on anything it cannot read whole.
func parseExpr(s string) (expr, error) {
	tokens, err := tokenize(strings.TrimSpace(quotes.Replace(s)))
	if err != nil {
		return nil, err
	}
	if len(tokens) > maxTokens {
		return nil, fmt.Errorf("expression trop longue (plus de %d éléments)", maxTokens)
	}
	p := &parser{tokens: tokens}
	e, err := p.expression(0)
	if err != nil {
		return nil, err
	}
	if t := p.peek(); t.kind != "end" {
		return nil, fmt.Errorf("« %s » inattendu", t.text)
	}
	return e, nil
}

func (p *parser) operator() (string, int) {
	t := p.peek()
	op := t.text
	if t.kind == "ident" && (op == "and" || op == "or") {
		return op, precedence[op]
	}
	if t.kind != "op" {
		return "", 0
	}
	return op, precedence[op]
}

func (p *parser) expression(min int) (expr, error) {
	left, err := p.unary()
	if err != nil {
		return nil, err
	}
	for {
		op, prec := p.operator()
		if prec == 0 || prec <= min {
			return left, nil
		}
		p.next()
		right, err := p.expression(prec)
		if err != nil {
			return nil, err
		}
		left = binaryExpr{op, left, right}
	}
}

func (p *parser) unary() (expr, error) {
	t := p.peek()
	if t.kind == "op" && (t.text == "!" || t.text == "-") || t.kind == "ident" && t.text == "not" {
		p.next()
		x, err := p.unary()
		if err != nil {
			return nil, err
		}
		op := t.text
		if op == "not" {
			op = "!"
		}
		return unaryExpr{op, x}, nil
	}
	return p.primary()
}

func (p *parser) primary() (expr, error) {
	t := p.next()
	switch t.kind {
	case "num":
		if _, err := strconv.ParseFloat(t.text, 64); err != nil {
			return nil, fmt.Errorf("nombre %q illisible", t.text)
		}
		return literalExpr{json.Number(t.text)}, nil
	case "str":
		return literalExpr{t.text}, nil
	case "ident":
		switch t.text {
		case "true":
			return literalExpr{true}, nil
		case "false":
			return literalExpr{false}, nil
		case "null":
			return literalExpr{nil}, nil
		}
		if p.peek().kind == "op" && p.peek().text == "(" {
			p.next()
			var args []expr
			if !(p.peek().kind == "op" && p.peek().text == ")") {
				for {
					a, err := p.expression(0)
					if err != nil {
						return nil, err
					}
					args = append(args, a)
					if p.peek().kind == "op" && p.peek().text == "," {
						p.next()
						continue
					}
					break
				}
			}
			if c := p.next(); c.kind != "op" || c.text != ")" {
				return nil, fmt.Errorf("parenthèse manquante après %s(", t.text)
			}
			return callExpr{t.text, args}, nil
		}
		return pathExpr{t.text}, nil
	case "op":
		if t.text == "(" {
			e, err := p.expression(0)
			if err != nil {
				return nil, err
			}
			if c := p.next(); c.kind != "op" || c.text != ")" {
				return nil, errors.New("parenthèse non fermée")
			}
			return e, nil
		}
	}
	if t.kind == "end" {
		return nil, errors.New("expression incomplète")
	}
	return nil, fmt.Errorf("« %s » inattendu", t.text)
}

// check reports the functions an expression calls that do not exist or get
// a wrong number of arguments.
func check(e expr) error {
	switch x := e.(type) {
	case unaryExpr:
		return check(x.x)
	case binaryExpr:
		if err := check(x.l); err != nil {
			return err
		}
		return check(x.r)
	case callExpr:
		n, ok := Functions[x.name]
		if !ok {
			return fmt.Errorf("fonction inconnue : %s", x.name)
		}
		if len(x.args) < n[0] || n[1] >= 0 && len(x.args) > n[1] {
			return fmt.Errorf("%s : nombre d'arguments incorrect", x.name)
		}
		for _, a := range x.args {
			if err := check(a); err != nil {
				return err
			}
		}
	}
	return nil
}

// paths lists the data an expression reads, with the function around each.
func paths(e expr, fn string, visit func(path, fn string)) {
	switch x := e.(type) {
	case pathExpr:
		if !strings.HasPrefix(x.path, "$") {
			visit(x.path, fn)
		}
	case unaryExpr:
		paths(x.x, fn, visit)
	case binaryExpr:
		paths(x.l, fn, visit)
		paths(x.r, fn, visit)
	case callExpr:
		for i, a := range x.args {
			f := x.name
			if i > 0 && x.name != "min" && x.name != "max" && x.name != "concat" && x.name != "if" && x.name != "dateDiff" {
				f = ""
			}
			paths(a, f, visit)
		}
	}
}

// code is a QR code or a barcode to draw as an image.
type code struct {
	kind, content string
	width, height float64 // cm
}

type evaluator struct {
	s   scope
	now time.Time
}

func (ev evaluator) eval(e expr) (any, error) {
	switch x := e.(type) {
	case literalExpr:
		return x.value, nil
	case pathExpr:
		switch x.path {
		case "$nowUTC", "$now":
			return ev.now, nil
		}
		v, ok := ev.s.lookup(x.path)
		if !ok {
			return unresolved(x.path), nil
		}
		return v, nil
	case unaryExpr:
		v, err := ev.eval(x.x)
		if err != nil {
			return nil, err
		}
		if x.op == "!" {
			return !truthy(value(v)), nil
		}
		f, ok := number(value(v))
		if !ok {
			return nil, errors.New("nombre attendu après -")
		}
		return -f, nil
	case binaryExpr:
		return ev.binary(x)
	case callExpr:
		args := make([]any, len(x.args))
		for i, a := range x.args {
			v, err := ev.eval(a)
			if err != nil {
				return nil, err
			}
			args[i] = value(v)
		}
		return call(x.name, args, ev.now)
	}
	return nil, errors.New("expression illisible")
}

// unresolved is a name the data does not have: compared with a value it
// reads as the text of the name (status=ok), as the first engine did.
type unresolved string

func value(v any) any {
	if _, ok := v.(unresolved); ok {
		return nil
	}
	return v
}

func (ev evaluator) binary(x binaryExpr) (any, error) {
	l, err := ev.eval(x.l)
	if err != nil {
		return nil, err
	}
	switch x.op {
	case "&&", "and":
		if !truthy(value(l)) {
			return false, nil
		}
		r, err := ev.eval(x.r)
		return truthy(value(r)), err
	case "||", "or":
		if truthy(value(l)) {
			return true, nil
		}
		r, err := ev.eval(x.r)
		return truthy(value(r)), err
	}
	r, err := ev.eval(x.r)
	if err != nil {
		return nil, err
	}
	switch x.op {
	case "==", "=":
		return equal(l, r), nil
	case "!=", "<>":
		return !equal(l, r), nil
	case "<", "<=", ">", ">=":
		c, ok := compare(value(l), value(r))
		if !ok {
			return false, nil
		}
		switch x.op {
		case "<":
			return c < 0, nil
		case "<=":
			return c <= 0, nil
		case ">":
			return c > 0, nil
		}
		return c >= 0, nil
	}
	lv, rv := value(l), value(r)
	a, aok := number(lv)
	b, bok := number(rv)
	if x.op == "+" && (!aok || !bok) {
		return text(lv) + text(rv), nil
	}
	if !aok || !bok {
		return nil, fmt.Errorf("nombres attendus pour %s", x.op)
	}
	switch x.op {
	case "+":
		return a + b, nil
	case "-":
		return a - b, nil
	case "*":
		return a * b, nil
	case "/":
		if b == 0 {
			return nil, errors.New("division par zéro")
		}
		return a / b, nil
	}
	if b == 0 {
		return nil, errors.New("division par zéro")
	}
	return math.Mod(a, b), nil
}

// equal compares like Docmosis: true/false by truth, numbers by value,
// anything else by text. A name the data does not have reads as its own
// text on the right (status=ok) and as nothing on the left.
func equal(l, r any) bool {
	if name, ok := r.(unresolved); ok {
		r = string(name)
	}
	l = value(l)
	if b, ok := r.(bool); ok {
		return truthy(l) == b
	}
	if b, ok := l.(bool); ok {
		return truthy(r) == b
	}
	if l == nil || r == nil {
		return l == nil && r == nil || text(l) == text(r)
	}
	if a, ok := number(l); ok {
		if b, ok := number(r); ok {
			return a == b
		}
	}
	return text(l) == text(r)
}

func compare(l, r any) (int, bool) {
	if a, ok := number(l); ok {
		if b, ok := number(r); ok {
			switch {
			case a < b:
				return -1, true
			case a > b:
				return 1, true
			}
			return 0, true
		}
	}
	if l == nil || r == nil {
		return 0, false
	}
	return strings.Compare(text(l), text(r)), true
}

// number reads a JSON number, a float or a text written as a number
// (1250.5, or 1 250,50 the French way).
func number(v any) (float64, bool) {
	switch t := v.(type) {
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	case float64:
		return t, true
	case int:
		return float64(t), true
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return 0, false
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f, true
		}
		s = strings.NewReplacer(" ", "", "\u00a0", "", "\u202f", "").Replace(s)
		// With both separators (1.250,50 or 1,250.50), the last one is the decimal one.
		switch c, d := strings.LastIndex(s, ","), strings.LastIndex(s, "."); {
		case c > d && d >= 0:
			s = strings.ReplaceAll(s[:c], ".", "") + "." + s[c+1:]
		case d > c && c >= 0:
			s = strings.ReplaceAll(s, ",", "")
		case strings.Count(s, ",") == 1:
			s = strings.Replace(s, ",", ".", 1)
		}
		f, err := strconv.ParseFloat(s, 64)
		return f, err == nil
	}
	return 0, false
}

func call(name string, args []any, now time.Time) (any, error) {
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
	switch name {
	case "dateFormat":
		return formatDate(args[0], str(1), str(2), now), nil
	case "numFormat":
		f, ok := num(0)
		if !ok {
			return text(args[0]), nil
		}
		return formatNumber(f, str(1), str(2)), nil
	case "toUpper":
		return strings.ToUpper(str(0)), nil
	case "toLower":
		return strings.ToLower(str(0)), nil
	case "trim":
		return strings.TrimSpace(str(0)), nil
	case "capitalize":
		s := str(0)
		r, n := utf8.DecodeRuneInString(s)
		return string(unicode.ToUpper(r)) + s[n:], nil
	case "length":
		switch t := args[0].(type) {
		case []any:
			return json.Number(strconv.Itoa(len(t))), nil
		case map[string]any:
			return json.Number(strconv.Itoa(len(t))), nil
		}
		return json.Number(strconv.Itoa(utf8.RuneCountInString(str(0)))), nil
	case "substring":
		r := []rune(str(0))
		start, _ := num(1)
		end := float64(len(r))
		if e, ok := num(2); ok {
			end = e
		}
		a, b := clamp(int(start), len(r)), clamp(int(end), len(r))
		if a > b {
			return "", nil
		}
		return string(r[a:b]), nil
	case "replace":
		return strings.ReplaceAll(str(0), str(1), str(2)), nil
	case "concat":
		var b strings.Builder
		for i := range args {
			b.WriteString(str(i))
		}
		return b.String(), nil
	case "contains":
		return strings.Contains(str(0), str(1)), nil
	case "startsWith":
		return strings.HasPrefix(str(0), str(1)), nil
	case "endsWith":
		return strings.HasSuffix(str(0), str(1)), nil
	case "isEmpty":
		return !truthy(args[0]) && !isFalse(args[0]), nil
	case "isNotEmpty":
		return truthy(args[0]) || isFalse(args[0]), nil
	case "round":
		f, ok := num(0)
		if !ok {
			return text(args[0]), nil
		}
		digits, _ := num(1)
		p := math.Pow(10, digits)
		return math.Round(f*p) / p, nil
	case "abs":
		f, ok := num(0)
		if !ok {
			return text(args[0]), nil
		}
		return math.Abs(f), nil
	case "min", "max":
		var best float64
		found := false
		for i := range args {
			if f, ok := num(i); ok && (!found || name == "min" && f < best || name == "max" && f > best) {
				best, found = f, true
			}
		}
		if !found {
			return nil, nil
		}
		return best, nil
	case "sum":
		list, _ := args[0].([]any)
		field := str(1)
		total := 0.0
		for _, item := range list {
			v := item
			if field != "" {
				v, _ = resolve(item, field)
			}
			if f, ok := number(v); ok {
				total += f
			}
		}
		return total, nil
	case "if":
		if truthy(args[0]) {
			return args[1], nil
		}
		return args[2], nil
	case "qrcode":
		size := 3.0
		if f, ok := num(1); ok && f > 0 {
			size = f
		}
		return code{kind: "qr", content: str(0), width: size, height: size}, nil
	case "barcode":
		c := code{kind: strings.ToLower(str(1)), content: str(0), width: 5, height: 1.5}
		if f, ok := num(2); ok && f > 0 {
			c.width = f
		}
		if f, ok := num(3); ok && f > 0 {
			c.height = f
		}
		if c.kind == "qr" || c.kind == "qrcode" || c.kind == "datamatrix" || c.kind == "aztec" {
			if _, ok := num(3); !ok {
				c.height = c.width
			}
		}
		return c, nil
	}
	if v, err := more(name, args, now); !errors.Is(err, errUnknown) {
		return v, err
	}
	return nil, fmt.Errorf("fonction inconnue : %s", name)
}

func isFalse(v any) bool { b, ok := v.(bool); return ok && !b }

func clamp(i, n int) int { return max(0, min(i, n)) }

// render writes the result of an expression as text; numbers computed by
// the expression lose the noise of floating point (0.1+0.2 shows 0.3).
func render(v any) string {
	switch t := v.(type) {
	case float64:
		return strconv.FormatFloat(math.Round(t*1e10)/1e10, 'f', -1, 64)
	case time.Time:
		return t.Format("02/01/2006")
	case unresolved:
		return ""
	}
	return text(v)
}

// evaluate runs an expression; ok is false when it cannot be read, so that
// the caller keeps the behaviour of the first engine.
func evaluate(src string, s scope, now time.Time) (any, bool) {
	e, err := parseExpr(src)
	if err != nil || check(e) != nil {
		return nil, false
	}
	v, err := evaluator{s, now}.eval(e)
	if err != nil {
		return nil, true
	}
	return v, true
}

// Condition decides a Docmosis condition on data, as cs_{…} does.
func Condition(src string, data map[string]any) bool {
	return condition(src, scope{data})
}

// CheckCondition tells why a condition cannot be read; nil when it can.
func CheckCondition(src string) error {
	e, err := parseExpr(src)
	if err == nil {
		err = check(e)
	}
	return err
}

var frenchMonths = []string{"janvier", "février", "mars", "avril", "mai", "juin", "juillet", "août", "septembre", "octobre", "novembre", "décembre"}
var frenchShortMonths = []string{"janv.", "févr.", "mars", "avr.", "mai", "juin", "juil.", "août", "sept.", "oct.", "nov.", "déc."}
var frenchDays = []string{"dimanche", "lundi", "mardi", "mercredi", "jeudi", "vendredi", "samedi"}
var frenchShortDays = []string{"dim.", "lun.", "mar.", "mer.", "jeu.", "ven.", "sam."}

// formatDate writes a date with a Java pattern (dd MMMM yyyy, EEEE d MMM…),
// in French unless locale is en.
func formatDate(v any, pattern, locale string, now time.Time) string {
	var t time.Time
	switch x := v.(type) {
	case time.Time:
		t = x
	case nil:
		return ""
	default:
		s := strings.TrimSpace(text(x))
		parsed := false
		for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02", "02/01/2006"} {
			if p, err := time.Parse(layout, s); err == nil {
				t, parsed = p, true
				break
			}
		}
		if !parsed {
			return s
		}
	}
	english := strings.HasPrefix(strings.ToLower(locale), "en")
	var b strings.Builder
	for i := 0; i < len(pattern); {
		c := pattern[i]
		if c == '\'' {
			j := strings.IndexByte(pattern[i+1:], '\'')
			if j < 0 {
				b.WriteString(pattern[i+1:])
				break
			}
			if j == 0 {
				b.WriteByte('\'')
			} else {
				b.WriteString(pattern[i+1 : i+1+j])
			}
			i += j + 2
			continue
		}
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			b.WriteByte(c)
			i++
			continue
		}
		n := 1
		for i+n < len(pattern) && pattern[i+n] == c {
			n++
		}
		i += n
		pad := func(v int) string { return fmt.Sprintf("%0*d", n, v) }
		switch c {
		case 'y':
			if n == 2 {
				b.WriteString(fmt.Sprintf("%02d", t.Year()%100))
			} else {
				b.WriteString(pad(t.Year()))
			}
		case 'M':
			switch {
			case n >= 4 && english:
				b.WriteString(t.Month().String())
			case n >= 4:
				b.WriteString(frenchMonths[t.Month()-1])
			case n == 3 && english:
				b.WriteString(t.Month().String()[:3])
			case n == 3:
				b.WriteString(frenchShortMonths[t.Month()-1])
			default:
				b.WriteString(pad(int(t.Month())))
			}
		case 'd':
			b.WriteString(pad(t.Day()))
		case 'E':
			switch {
			case n >= 4 && english:
				b.WriteString(t.Weekday().String())
			case n >= 4:
				b.WriteString(frenchDays[t.Weekday()])
			case english:
				b.WriteString(t.Weekday().String()[:3])
			default:
				b.WriteString(frenchShortDays[t.Weekday()])
			}
		case 'H':
			b.WriteString(pad(t.Hour()))
		case 'h':
			h := t.Hour() % 12
			if h == 0 {
				h = 12
			}
			b.WriteString(pad(h))
		case 'm':
			b.WriteString(pad(t.Minute()))
		case 's':
			b.WriteString(pad(t.Second()))
		case 'a':
			if t.Hour() < 12 {
				b.WriteString("AM")
			} else {
				b.WriteString("PM")
			}
		default:
			b.WriteString(strings.Repeat(string(c), n))
		}
	}
	return b.String()
}

// formatNumber writes a number with a Java DecimalFormat pattern (#,##0.00,
// 0.0#, #,##0 €, 0%), rounded half to even as Java does, with the French
// separators unless locale is en.
func formatNumber(f float64, pattern, locale string) string {
	if pattern == "" {
		pattern = "#,##0.##"
	}
	// Prefix and suffix are what surrounds the digits.
	start := strings.IndexAny(pattern, "#0.,")
	end := strings.LastIndexAny(pattern, "#0.,")
	if start < 0 {
		return render(f)
	}
	prefix, body, suffix := pattern[:start], pattern[start:end+1], pattern[end+1:]
	if strings.Contains(prefix+suffix, "%") {
		f *= 100
	}
	intPart, fracPart, _ := strings.Cut(body, ".")
	grouping := 0
	if i := strings.LastIndex(intPart, ","); i >= 0 {
		grouping = len(intPart) - i - 1
	}
	minInt := strings.Count(intPart, "0")
	minFrac := strings.Count(fracPart, "0")
	maxFrac := minFrac + strings.Count(fracPart, "#")
	negative := f < 0
	digits := roundHalfEven(math.Abs(f), maxFrac)
	whole, frac, _ := strings.Cut(digits, ".")
	for len(frac) > minFrac && strings.HasSuffix(frac, "0") {
		frac = frac[:len(frac)-1]
	}
	for len(whole) < minInt {
		whole = "0" + whole
	}
	if minInt == 0 && whole == "0" && frac != "" {
		whole = ""
	}
	groupSep, decimalSep := "\u00a0", ","
	if strings.HasPrefix(strings.ToLower(locale), "en") {
		groupSep, decimalSep = ",", "."
	}
	if grouping > 0 {
		var b strings.Builder
		for i, r := range whole {
			if i > 0 && (len(whole)-i)%grouping == 0 {
				b.WriteString(groupSep)
			}
			b.WriteRune(r)
		}
		whole = b.String()
	}
	out := whole
	if frac != "" {
		out += decimalSep + frac
	}
	if negative && strings.Trim(out, "0,.\u00a0") != "" {
		out = "-" + out
	}
	return strings.ReplaceAll(prefix, "'", "") + out + strings.ReplaceAll(suffix, "'", "")
}

// roundHalfEven rounds a positive number to n decimals, on its decimal
// writing (the binary value of 2.675 is not looked at).
func roundHalfEven(f float64, n int) string {
	s := strconv.FormatFloat(f, 'f', -1, 64)
	whole, frac, _ := strings.Cut(s, ".")
	if len(frac) <= n {
		return whole + "." + frac + strings.Repeat("0", n-len(frac))
	}
	keep, rest := frac[:n], frac[n:]
	digits := []byte(whole + keep)
	up := rest[0] > '5' || rest[0] == '5' && (strings.TrimRight(rest[1:], "0") != "" || (digits[len(digits)-1]-'0')%2 == 1)
	if up {
		i := len(digits) - 1
		for ; i >= 0; i-- {
			if digits[i] == '9' {
				digits[i] = '0'
				continue
			}
			digits[i]++
			break
		}
		if i < 0 {
			digits = append([]byte{'1'}, digits...)
		}
	}
	w := string(digits[:len(digits)-n])
	if n == 0 {
		return w
	}
	return w + "." + string(digits[len(digits)-n:])
}

const dpcm = 300 / 2.54 // pixels per centimetre of the generated codes

// image draws the code as a PNG; its size is in centimetres.
func (c code) image() ([]byte, error) {
	var bc barcode.Barcode
	var err error
	switch c.kind {
	case "qr", "qrcode":
		bc, err = qr.Encode(c.content, qr.M, qr.Auto)
	case "code128":
		bc, err = code128.Encode(c.content)
	case "code39":
		bc, err = code39.Encode(c.content, false, true)
	case "ean", "ean13", "ean8":
		bc, err = ean.Encode(c.content)
	case "datamatrix":
		bc, err = datamatrix.Encode(c.content)
	case "pdf417":
		bc, err = pdf417.Encode(c.content, 2)
	case "aztec":
		bc, err = aztec.Encode([]byte(c.content), aztec.DEFAULT_EC_PERCENT, aztec.DEFAULT_LAYERS)
	case "codabar":
		bc, err = codabar.Encode(c.content)
	case "itf", "2of5":
		bc, err = twooffive.Encode(c.content, true)
	default:
		return nil, fmt.Errorf("type de code inconnu : %s", c.kind)
	}
	if err != nil {
		return nil, err
	}
	b := bc.Bounds()
	w, h := int(c.width*dpcm), int(c.height*dpcm)
	// Whole modules keep the bars sharp: the size is rounded down to them.
	w = max(b.Dx(), w/b.Dx()*b.Dx())
	if b.Dy() > 1 {
		h = max(b.Dy(), h/b.Dy()*b.Dy())
	}
	if bc, err = barcode.Scale(bc, w, h); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := png.Encode(&out, bc); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
