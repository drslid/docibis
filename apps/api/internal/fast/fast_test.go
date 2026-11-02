package fast

import (
	"math"
	"strings"
	"testing"
)

// A page drawn as LibreOffice does: text runs with kerning, a colour, a
// saved state and a second run placed by a matrix.
func TestGlyphs(t *testing.T) {
	f := &font{first: 32, widths: make([]float64, 96), unicode: map[byte]string{}}
	for c := 32; c < 128; c++ {
		f.widths[c-32] = 500
		f.unicode[byte(c)] = string(rune(c))
	}
	p := &page{fonts: map[string]*font{"F1": f}, content: []byte(
		"q 0.2 0.4 0.6 rg BT 72 700 Td /F1 10 Tf (Ab) Tj [(c) -1000 <64>] TJ ET Q\n" +
			"BT 1 0 0 1 100 50 Tm /F1 20 Tf 2 Tc (x) Tj ET")}
	got, err := p.glyphs(0)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		text       string
		x, y, size float64
		fill       string
	}{
		{"A", 72, 700, 10, "0.2 0.4 0.6 rg"}, {"b", 77, 700, 10, "0.2 0.4 0.6 rg"}, {"c", 82, 700, 10, "0.2 0.4 0.6 rg"},
		{"d", 97, 700, 10, "0.2 0.4 0.6 rg"}, {"x", 100, 50, 20, "0 g"},
	}
	if len(got) != len(want) {
		t.Fatalf("%d glyphs, want %d", len(got), len(want))
	}
	for i, w := range want {
		g := got[i]
		if g.text != w.text || math.Abs(g.x()-w.x) > 1e-9 || math.Abs(g.y()-w.y) > 1e-9 || g.size != w.size || g.fill != w.fill {
			t.Errorf("glyph %d = %q at (%g, %g) size %g fill %q, want %+v", i, g.text, g.x(), g.y(), g.size, g.fill, w)
		}
	}
	if a := got[4].advance; a != 12 { // 500/1000 × 20 + Tc 2
		t.Errorf("advance with Tc = %g, want 12", a)
	}
}

func TestComposite(t *testing.T) {
	p := &page{fonts: map[string]*font{"F1": {composite: true}}, content: []byte("BT /F1 10 Tf <0001> Tj ET")}
	if _, err := p.glyphs(0); err == nil || !strings.Contains(err.Error(), "composite") {
		t.Fatalf("composite font: err = %v", err)
	}
}

func TestLexer(t *testing.T) {
	l := &lexer{src: []byte(`/Name (a\(b\)\101) <48 49> [1 -2.5 (z)] << /K 1 >> BI /W 1 ID xx EI 3 Tz`)}
	var ops []string
	var values []any
	for {
		v, op, ok := l.next()
		if !ok {
			break
		}
		if op != "" {
			ops = append(ops, op)
		} else {
			values = append(values, v)
		}
	}
	if len(values) < 4 || values[0] != name("Name") || values[1] != "a(b)A" || values[2] != "HI" {
		t.Fatalf("values = %#v", values)
	}
	if arr, ok := values[3].([]any); !ok || len(arr) != 3 || arr[1] != -2.5 || arr[2] != "z" {
		t.Fatalf("array = %#v", values[3])
	}
	if ops[len(ops)-1] != "Tz" {
		t.Fatalf("ops = %v, the inline image is not skipped", ops)
	}
}

func TestMarkersAndCharset(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; ; i++ {
		m, ok := Marker(i)
		if !ok {
			if i < 100 {
				t.Fatalf("only %d markers", i)
			}
			break
		}
		if seen[m] || strings.ContainsAny(m, Charset()) {
			t.Fatalf("marker %d %q is not unique", i, m)
		}
		seen[m] = true
	}
	for _, c := range []string{"é", "Œ", "€", "@", "0"} {
		if !strings.Contains(Charset(), c) {
			t.Errorf("charset without %q", c)
		}
	}
}
