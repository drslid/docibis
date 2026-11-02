package fast

import (
	"fmt"
	"strconv"
	"strings"
)

// matrix is a PDF transformation [a b c d e f].
type matrix [6]float64

var identity = matrix{1, 0, 0, 1, 0, 0}

// times is m × n.
func (m matrix) times(n matrix) matrix {
	return matrix{
		m[0]*n[0] + m[1]*n[2], m[0]*n[1] + m[1]*n[3],
		m[2]*n[0] + m[3]*n[2], m[2]*n[1] + m[3]*n[3],
		m[4]*n[0] + m[5]*n[2] + n[4], m[4]*n[1] + m[5]*n[3] + n[5],
	}
}

func (m matrix) String() string {
	parts := make([]string, 6)
	for i, v := range m {
		parts[i] = num(v)
	}
	return strings.Join(parts, " ")
}

func num(v float64) string {
	s := strconv.FormatFloat(v, 'f', 4, 64)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "-0" || s == "" {
		return "0"
	}
	return s
}

// glyph is one character drawn on a page, with the state it was drawn in.
type glyph struct {
	page    int
	font    string // resource name on its page
	code    byte
	text    string
	advance float64 // along the line, in text space
	size    float64
	ctm, tm matrix
	tc, tw  float64
	tz      float64 // horizontal scaling, 1 = 100 %
	render  int
	fill    string // operators that set the fill colour
}

// x and y are where the glyph starts, on the page.
func (g glyph) x() float64 { return g.tm[4]*g.ctm[0] + g.tm[5]*g.ctm[2] + g.ctm[4] }
func (g glyph) y() float64 { return g.tm[4]*g.ctm[1] + g.tm[5]*g.ctm[3] + g.ctm[5] }

// plain tells a glyph drawn without rotation nor scaling.
func (g glyph) plain() bool {
	return g.ctm[0] == 1 && g.ctm[1] == 0 && g.ctm[2] == 0 && g.ctm[3] == 1 &&
		g.tm[0] == 1 && g.tm[1] == 0 && g.tm[2] == 0 && g.tm[3] == 1 && g.tz == 1
}

type name string

type lexer struct {
	src []byte
	pos int
}

func delimiter(c byte) bool { return strings.IndexByte("()<>[]{}/%", c) >= 0 }
func space(c byte) bool     { return strings.IndexByte(" \t\r\n\f\x00", c) >= 0 }

// next reads one operand (float64, string, name, []any, map) or an operator
// (returned as op). ok is false at the end.
func (l *lexer) next() (value any, op string, ok bool) {
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		switch {
		case space(c):
			l.pos++
		case c == '%':
			for l.pos < len(l.src) && l.src[l.pos] != '\n' && l.src[l.pos] != '\r' {
				l.pos++
			}
		default:
			return l.token()
		}
	}
	return nil, "", false
}

func (l *lexer) token() (any, string, bool) {
	c := l.src[l.pos]
	switch {
	case c == '/':
		start := l.pos + 1
		l.pos++
		for l.pos < len(l.src) && !space(l.src[l.pos]) && !delimiter(l.src[l.pos]) {
			l.pos++
		}
		return name(l.src[start:l.pos]), "", true
	case c == '(':
		return l.literal(), "", true
	case c == '<' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '<':
		l.pos += 2
		d := map[string]any{}
		for {
			v, op, ok := l.next()
			if !ok || op == ">>" {
				return d, "", true
			}
			if k, isName := v.(name); isName {
				val, _, _ := l.next()
				d[string(k)] = val
			}
		}
	case c == '>' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '>':
		l.pos += 2
		return nil, ">>", true
	case c == '<':
		end := l.pos + 1
		for end < len(l.src) && l.src[end] != '>' {
			end++
		}
		h := strings.Map(func(r rune) rune {
			if strings.ContainsRune("0123456789abcdefABCDEF", r) {
				return r
			}
			return -1
		}, string(l.src[l.pos+1:end]))
		if len(h)%2 == 1 {
			h += "0"
		}
		b := make([]byte, len(h)/2)
		for i := range b {
			v, _ := strconv.ParseUint(h[2*i:2*i+2], 16, 8)
			b[i] = byte(v)
		}
		l.pos = end + 1
		return string(b), "", true
	case c == '[':
		l.pos++
		var a []any
		for {
			v, op, ok := l.next()
			if !ok || op == "]" {
				return a, "", true
			}
			a = append(a, v)
		}
	case c == ']':
		l.pos++
		return nil, "]", true
	case c == '{' || c == '}' || c == ')' || c == '>':
		l.pos++
		return nil, string(c), true
	}
	start := l.pos
	for l.pos < len(l.src) && !space(l.src[l.pos]) && !delimiter(l.src[l.pos]) {
		l.pos++
	}
	word := string(l.src[start:l.pos])
	if f, err := strconv.ParseFloat(word, 64); err == nil {
		return f, "", true
	}
	if word == "BI" {
		// An inline image: its data runs to EI.
		if end := strings.Index(string(l.src[l.pos:]), "EI"); end >= 0 {
			l.pos += end + 2
		}
		return nil, "", true
	}
	return nil, word, true
}

func (l *lexer) literal() string {
	var b strings.Builder
	depth := 0
	l.pos++
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		l.pos++
		switch c {
		case '(':
			depth++
		case ')':
			if depth == 0 {
				return b.String()
			}
			depth--
		case '\\':
			if l.pos >= len(l.src) {
				return b.String()
			}
			e := l.src[l.pos]
			l.pos++
			switch e {
			case 'n':
				c = '\n'
			case 'r':
				c = '\r'
			case 't':
				c = '\t'
			case 'b':
				c = '\b'
			case 'f':
				c = '\f'
			case '\r', '\n':
				continue
			default:
				if e >= '0' && e <= '7' {
					v := int(e - '0')
					for k := 0; k < 2 && l.pos < len(l.src) && l.src[l.pos] >= '0' && l.src[l.pos] <= '7'; k++ {
						v = v*8 + int(l.src[l.pos]-'0')
						l.pos++
					}
					c = byte(v)
				} else {
					c = e
				}
			}
		}
		b.WriteByte(c)
	}
	return b.String()
}

type state struct {
	ctm                  matrix
	fill                 string
	colorSpace           string
	font                 string
	size, tc, tw, tz, tl float64
	render               int
}

// glyphs runs the content of a page and lists the characters it draws.
func (p *page) glyphs(number int) ([]glyph, error) {
	l := &lexer{src: p.content}
	st := state{ctm: identity, fill: "0 g", tz: 1}
	var stack []state
	var tm, tlm matrix
	var out []glyph
	var args []any
	floats := func(n int) ([]float64, bool) {
		if len(args) < n {
			return nil, false
		}
		f := make([]float64, n)
		for i, a := range args[len(args)-n:] {
			v, ok := a.(float64)
			if !ok {
				return nil, false
			}
			f[i] = v
		}
		return f, true
	}
	show := func(s string) error {
		f := p.fonts[st.font]
		if f == nil {
			return fmt.Errorf("page %d: font %s unknown", number, st.font)
		}
		if f.composite {
			return fmt.Errorf("page %d: composite font %s", number, st.font)
		}
		for i := 0; i < len(s); i++ {
			code := s[i]
			advance := f.width(code) / 1000 * st.size
			advance += st.tc
			if code == 32 {
				advance += st.tw
			}
			advance *= st.tz
			out = append(out, glyph{page: number, font: st.font, code: code, text: f.unicode[code], advance: advance, size: st.size,
				ctm: st.ctm, tm: tm, tc: st.tc, tw: st.tw, tz: st.tz, render: st.render, fill: st.fill})
			tm = matrix{1, 0, 0, 1, advance, 0}.times(tm)
		}
		return nil
	}
	newline := func(tx, ty float64) {
		tlm = matrix{1, 0, 0, 1, tx, ty}.times(tlm)
		tm = tlm
	}
	for {
		v, op, ok := l.next()
		if !ok {
			return out, nil
		}
		if op == "" {
			args = append(args, v)
			continue
		}
		switch op {
		case "q":
			stack = append(stack, st)
		case "Q":
			if n := len(stack); n > 0 {
				st, stack = stack[n-1], stack[:n-1]
			}
		case "cm":
			if f, ok := floats(6); ok {
				st.ctm = matrix{f[0], f[1], f[2], f[3], f[4], f[5]}.times(st.ctm)
			}
		case "g", "rg", "k":
			if f, ok := floats(map[string]int{"g": 1, "rg": 3, "k": 4}[op]); ok {
				parts := make([]string, len(f))
				for i, x := range f {
					parts[i] = num(x)
				}
				st.fill = strings.Join(parts, " ") + " " + op
			}
		case "cs":
			if len(args) > 0 {
				if n, ok := args[len(args)-1].(name); ok {
					st.colorSpace = string(n)
				}
			}
		case "sc", "scn":
			parts := []string{}
			for _, a := range args {
				switch x := a.(type) {
				case float64:
					parts = append(parts, num(x))
				case name:
					parts = append(parts, "/"+string(x))
				}
			}
			st.fill = "/" + st.colorSpace + " cs " + strings.Join(parts, " ") + " scn"
		case "BT":
			tm, tlm = identity, identity
		case "Tf":
			if len(args) >= 2 {
				if n, ok := args[len(args)-2].(name); ok {
					st.font = string(n)
				}
				st.size, _ = args[len(args)-1].(float64)
			}
		case "Tc", "Tw", "Tz", "TL", "Tr":
			if f, ok := floats(1); ok {
				switch op {
				case "Tc":
					st.tc = f[0]
				case "Tw":
					st.tw = f[0]
				case "Tz":
					st.tz = f[0] / 100
				case "TL":
					st.tl = f[0]
				case "Tr":
					st.render = int(f[0])
				}
			}
		case "Td", "TD":
			if f, ok := floats(2); ok {
				if op == "TD" {
					st.tl = -f[1]
				}
				newline(f[0], f[1])
			}
		case "Tm":
			if f, ok := floats(6); ok {
				tlm = matrix{f[0], f[1], f[2], f[3], f[4], f[5]}
				tm = tlm
			}
		case "T*":
			newline(0, -st.tl)
		case "Tj", "'", "\"":
			if op != "Tj" {
				newline(0, -st.tl)
			}
			if op == "\"" {
				if f, ok := floats(3); ok {
					st.tw, st.tc = f[0], f[1]
				}
			}
			if len(args) > 0 {
				if s, ok := args[len(args)-1].(string); ok {
					if err := show(s); err != nil {
						return nil, err
					}
				}
			}
		case "TJ":
			if len(args) > 0 {
				a, _ := args[len(args)-1].([]any)
				for _, item := range a {
					switch x := item.(type) {
					case string:
						if err := show(x); err != nil {
							return nil, err
						}
					case float64:
						tm = matrix{1, 0, 0, 1, -x / 1000 * st.size * st.tz, 0}.times(tm)
					}
				}
			}
		}
		args = args[:0]
	}
}
