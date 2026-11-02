package fast

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// Variants are the PDFs LibreOffice makes of a template where each tag i is
// replaced by: nothing (background, a box ☐ for a box), Marker(i) (markers),
// Marker(i)+"." (dots, for the alignment), Marker(i) and a long word (long,
// for the room before the line breaks), Marker(i) and Charset (donor, for
// the fonts and codes of every character a value may use).
type Variants struct {
	Background, Markers, Dots, Long, Donor []byte
	// BoxMarks draws the marker of each box tag, the other tags empty.
	BoxMarks []byte
}

// Long is what follows the marker in the long variant.
var Long = strings.Repeat("W", 100)

var markers = func() []rune {
	var r []rune
	for c := rune(0x410); c <= 0x44F; c++ {
		r = append(r, c)
	}
	for c := rune(0x391); c <= 0x3A9; c++ {
		if c != 0x3A2 {
			r = append(r, c)
		}
	}
	for c := rune(0x3B1); c <= 0x3C9; c++ {
		r = append(r, c)
	}
	// Latin Extended-A, but the letters a French value may hold.
	for c := rune(0x100); c <= 0x17F; c++ {
		if !strings.ContainsRune("ŒœŠšŸŽž", c) {
			r = append(r, c)
		}
	}
	return r
}()

// Marker is the character written in place of tag i; false when the
// template has too many tags for the overlay.
func Marker(i int) (string, bool) {
	if i < 0 || i >= len(markers) {
		return "", false
	}
	return string(markers[i]), true
}

// characters a value may use: Latin-1, the French ligatures and quotes, the
// euro, the boxes.
var characters = func() []rune {
	var r []rune
	for c := rune(0x21); c <= 0x7E; c++ {
		r = append(r, c)
	}
	for c := rune(0xA0); c <= 0xFF; c++ {
		r = append(r, c)
	}
	return append(r, 0x152, 0x153, 0x160, 0x161, 0x178, 0x17D, 0x17E, 0x2013, 0x2014, 0x2018, 0x2019, 0x201A, 0x201C, 0x201D,
		0x201E, 0x2020, 0x2021, 0x2022, 0x2026, 0x2030, 0x2039, 0x203A, 0x202F, 0x20AC, 0x2122)
}()

// Charset is what follows the marker in the donor variant: every character
// alone between spaces, so that no ligature joins two of them.
func Charset() string {
	var b strings.Builder
	for _, c := range characters {
		b.WriteRune(' ')
		b.WriteRune(c)
	}
	return b.String()
}

// Overlay renders the documents of one template version.
type Overlay struct {
	Tags  []string
	Slots []Slot
	// Base is the background PDF with the fonts and the pages rewritten to
	// take one more content stream each: Pages[i] is its object number.
	Base   []byte
	Pages  []int
	Prev   int // offset of the cross-reference section of Base
	Size   int
	Root   string
	Info   string
	ID     string
	Engine string // LibreOffice version that drew the background
}

// Slot is one tag of the template, drawn at one or more places.
type Slot struct {
	Background string // what the background shows there ("" or ☐)
	Align      int    // 0 left, 1 right, 2 centre
	Room       float64
	Places     []Place
	Chars      map[string]Char
}

// Place is where a slot is drawn: the state of the first glyph LibreOffice
// drew there.
type Place struct {
	Page          int
	CTM, TM       matrix
	Size          float64
	Tc, Tw, Tz    float64
	Render        int
	Fill          string
	MarkerAdvance float64
	Erase         *[4]float64 // x, y, width, height of the background box
}

// Char is how a character is drawn: a font of the base and its code.
type Char struct {
	Font  string
	Code  byte
	Width float64 // thousandths of the size
}

// Refusal is data the overlay cannot draw (a value too long for its place,
// a character missing from the fonts): the document goes to LibreOffice.
type Refusal struct{ Reason string }

func (r *Refusal) Error() string { return r.Reason }

// Unfit is a template the overlay cannot reproduce.
type Unfit struct{ Reason string }

func (u *Unfit) Error() string { return u.Reason }

func unfit(format string, args ...any) error { return &Unfit{fmt.Sprintf(format, args...)} }

type drawn struct {
	glyphs []glyph
	pages  []*page
	doc    *document
}

func read(raw []byte) (*drawn, error) {
	doc, err := load(raw)
	if err != nil {
		return nil, err
	}
	d := &drawn{doc: doc, pages: doc.pages}
	for n, p := range doc.pages {
		g, err := p.glyphs(n)
		if err != nil {
			return nil, unfit("contenu illisible : %v", err)
		}
		d.glyphs = append(d.glyphs, g...)
	}
	return d, nil
}

// find lists where a text is drawn, as the index of its glyph.
func (d *drawn) find(text string) []int {
	var out []int
	for i, g := range d.glyphs {
		if g.text == text {
			out = append(out, i)
		}
	}
	return out
}

// rightOf is the glyph drawn next to the right of glyph gi on its line, -1
// if none.
func (d *drawn) rightOf(gi int) int {
	m, best := d.glyphs[gi], -1
	for i, g := range d.glyphs {
		if g.page != m.page || math.Abs(g.y()-m.y()) > 0.5 || g.x() < m.x()+m.advance/2 {
			continue
		}
		if best < 0 || g.x() < d.glyphs[best].x() {
			best = i
		}
	}
	return best
}

// inLines sorts glyphs as they are read: in the order of the content, but
// from left to right along a line, which LibreOffice draws in runs of one
// font.
func (d *drawn) inLines(idx []int) []int {
	line := make([]int, len(d.glyphs))
	for i := 1; i < len(d.glyphs); i++ {
		line[i] = line[i-1]
		if d.glyphs[i].page != d.glyphs[i-1].page || math.Abs(d.glyphs[i].y()-d.glyphs[i-1].y()) > 0.5 {
			line[i]++
		}
	}
	out := slices.Clone(idx)
	slices.SortStableFunc(out, func(a, b int) int {
		if line[a] != line[b] {
			return line[a] - line[b]
		}
		return cmp.Compare(d.glyphs[a].x(), d.glyphs[b].x())
	})
	return out
}

// boxPlaces finds the ☐ the background draws for each box tag: the BoxMarks
// variant draws the marker of each box instead, and the boxes of a line are
// taken in the order of their markers along it (LibreOffice draws a line in
// runs of one font, and the cells of a table in its own order).
func boxPlaces(tags []string, boxes []bool, bg, bm *drawn) (map[int]glyph, error) {
	type mark struct {
		i int
		g glyph
	}
	var marks []mark
	for i := range tags {
		if !boxes[i] {
			continue
		}
		m, _ := Marker(i)
		at := bm.find(m)
		if len(at) != 1 {
			return nil, unfit("la case de la balise %d est introuvable", i+1)
		}
		marks = append(marks, mark{i, bm.glyphs[at[0]]})
	}
	same := func(a, b glyph) bool { return a.page == b.page && math.Abs(a.y()-b.y()) < 0.5 }
	squares := bg.find("☐")
	out := map[int]glyph{}
	for _, mk := range marks {
		rank, n := 0, 0
		for _, other := range marks {
			if same(other.g, mk.g) {
				n++
				if other.g.x() < mk.g.x() {
					rank++
				}
			}
		}
		var line []glyph
		for _, si := range squares {
			if same(bg.glyphs[si], mk.g) {
				line = append(line, bg.glyphs[si])
			}
		}
		if len(line) != n {
			return nil, unfit("la ligne de la case de la balise %d change avec les cases", mk.i+1)
		}
		slices.SortFunc(line, func(a, b glyph) int { return cmp.Compare(a.x(), b.x()) })
		out[mk.i] = line[rank]
	}
	return out, nil
}

// Prepare reads the variants and builds the overlay of a template.
func Prepare(tags []string, boxes []bool, v Variants) (*Overlay, error) {
	bg, err := read(v.Background)
	if err != nil {
		return nil, err
	}
	mk, err := read(v.Markers)
	if err != nil {
		return nil, err
	}
	dots, err := read(v.Dots)
	if err != nil {
		return nil, err
	}
	long, err := read(v.Long)
	if err != nil {
		return nil, err
	}
	donor, err := read(v.Donor)
	if err != nil {
		return nil, err
	}
	if len(mk.pages) != len(bg.pages) || len(dots.pages) != len(bg.pages) {
		return nil, unfit("le nombre de pages change avec les données")
	}
	o := &Overlay{Tags: tags, Slots: make([]Slot, len(tags))}
	nBoxes := 0
	for _, b := range boxes {
		if b {
			nBoxes++
		}
	}
	var boxAt map[int]glyph
	if nBoxes > 0 {
		bm, err := read(v.BoxMarks)
		if err != nil {
			return nil, err
		}
		if len(bm.pages) != len(bg.pages) {
			return nil, unfit("le nombre de pages change avec les cases à cocher")
		}
		if boxAt, err = boxPlaces(tags, boxes, bg, bm); err != nil {
			return nil, err
		}
	}
	for i := range tags {
		m, ok := Marker(i)
		if !ok {
			return nil, unfit("plus de %d balises", len(markers))
		}
		s := &o.Slots[i]
		if boxes[i] {
			b := boxAt[i]
			if !b.plain() {
				return nil, unfit("la case de la balise %d est tournée ou déformée", i+1)
			}
			f := bg.pages[b.page].fonts[b.font]
			bottom := b.y() + f.descent/1000*b.size
			s.Background, s.Room = "☐", math.MaxFloat32
			s.placesAppend(Place{Page: b.page, CTM: b.ctm, TM: b.tm, Size: b.size, Tc: b.tc, Tw: b.tw, Tz: b.tz, Render: b.render, Fill: b.fill,
				MarkerAdvance: b.advance, Erase: &[4]float64{b.x(), bottom, b.advance, (f.ascent - f.descent) / 1000 * b.size}})
			continue
		}
		places, err := locate(i, m, mk, dots, long)
		if err != nil {
			return nil, err
		}
		if len(places) > 0 {
			s.Align = places[0].Align
			s.Room = places[0].Room
			for _, p := range places[1:] {
				s.Room = math.Min(s.Room, p.Room)
			}
		}
		for _, p := range places {
			s.placesAppend(p.Place)
		}
	}
	// The fonts and codes of each slot, from the donor: the font LibreOffice
	// drew the marker with, and for a character it lacks, the font that drew
	// that character (a fallback font, as for ☑). The donor draws a line in
	// runs of one font, so the order of its glyphs tells nothing.
	used := map[int]bool{}
	byText := map[string][]int{}
	for gi, g := range donor.glyphs {
		byText[g.text] = append(byText[g.text], gi)
	}
	// The donor draws the boxes ticked, alone (a marker could push the box
	// out of a narrow cell): the k-th ☑ read is the k-th box.
	ticks := donor.inLines(byText["☑"])
	if len(ticks) != nBoxes {
		return nil, unfit("%d cases ☑ dessinées pour %d balises de case à cocher", len(ticks), nBoxes)
	}
	nextTick := 0
	for i := range tags {
		s := &o.Slots[i]
		if boxes[i] {
			g := donor.glyphs[ticks[nextTick]]
			nextTick++
			gf := donor.pages[g.page].fonts[g.font]
			s.Chars = map[string]Char{"☑": {Font: strconv.Itoa(gf.ref), Code: g.code, Width: gf.width(g.code)}}
			used[gf.ref] = true
			continue
		}
		m, _ := Marker(i)
		at := byText[m]
		if len(at) == 0 {
			return nil, unfit("la balise %d n'apparaît pas dans le jeu de caractères", i+1)
		}
		f := donor.pages[donor.glyphs[at[0]].page].fonts[donor.glyphs[at[0]].font]
		if !f.has("a") && !f.has("0") {
			// The marker came from a fallback font: the text font draws the
			// characters right of it.
			if next := donor.rightOf(at[0]); next >= 0 {
				f = donor.pages[donor.glyphs[next].page].fonts[donor.glyphs[next].font]
			}
		}
		s.Chars = map[string]Char{}
		for _, r := range Charset() {
			c := string(r)
			if _, ok := s.Chars[c]; ok {
				continue
			}
			if code, ok := f.code(c); ok {
				s.Chars[c] = Char{Font: strconv.Itoa(f.ref), Code: code, Width: f.width(code)}
				used[f.ref] = true
				continue
			}
			for _, gi := range byText[c] {
				g := donor.glyphs[gi]
				gf := donor.pages[g.page].fonts[g.font]
				s.Chars[c] = Char{Font: strconv.Itoa(gf.ref), Code: g.code, Width: gf.width(g.code)}
				used[gf.ref] = true
				break
			}
		}
	}
	if err := o.build(bg.doc, donor.doc, used); err != nil {
		return nil, err
	}
	return o, nil
}

// placesAppend keeps the places of a slot (the slot itself is filled after).
func (s *Slot) placesAppend(p Place) {
	for i := range s.Places {
		if s.Places[i].Page == p.Page && s.Places[i].TM == p.TM {
			return
		}
	}
	s.Places = append(s.Places, p)
}

type located struct {
	Place
	Align int
	Room  float64
}

// locate finds the places of slot i from its marker in the variants.
func locate(i int, m string, mk, dots, long *drawn) ([]located, error) {
	at, atDots, atLong := mk.find(m), dots.find(m), long.find(m)
	if len(at) != len(atDots) || len(at) != len(atLong) {
		return nil, unfit("la balise %d déplace la page", i+1)
	}
	out := make([]located, 0, len(at))
	for k, gi := range at {
		g := mk.glyphs[gi]
		if !g.plain() {
			return nil, unfit("la balise %d est tournée ou déformée", i+1)
		}
		p := located{Place: Place{Page: g.page, CTM: g.ctm, TM: g.tm, Size: g.size, Tc: g.tc, Tw: g.tw, Tz: g.tz, Render: g.render, Fill: g.fill, MarkerAdvance: g.advance}}
		// Alignment: where the marker starts once a dot follows it.
		dg := dots.glyphs[atDots[k]]
		dot := 0.0
		if atDots[k]+1 < len(dots.glyphs) {
			dot = dots.glyphs[atDots[k]+1].advance
		}
		switch shift := dg.x() - g.x(); {
		case math.Abs(shift) < 0.05:
			p.Align = 0
		case math.Abs(shift+dot) < 0.2:
			p.Align = 1
		case math.Abs(shift+dot/2) < 0.2:
			p.Align = 2
		default:
			return nil, unfit("alignement de la balise %d inconnu", i+1)
		}
		// Room: the line the long word fills.
		lg := long.glyphs[atLong[k]]
		end := lg.x() + lg.advance
		for j := atLong[k] + 1; j < len(long.glyphs); j++ {
			n := long.glyphs[j]
			if n.page != lg.page || math.Abs(n.y()-lg.y()) > 0.5 {
				break
			}
			end = math.Max(end, n.x()+n.advance)
		}
		p.Room = end - lg.x()
		out = append(out, p)
	}
	return out, nil
}

var startxref = regexp.MustCompile(`startxref\s+(\d+)\s*%%EOF\s*$`)

// build writes Base: the background, the fonts of the donor and the pages
// rewritten to show one more content stream, as an incremental update.
func (o *Overlay) build(bg, donor *document, used map[int]bool) error {
	m := startxref.FindSubmatch(bg.raw)
	if m == nil {
		return unfit("fin du PDF illisible")
	}
	prev, _ := strconv.Atoi(string(m[1]))
	if bg.ctx.Root == nil || bg.ctx.Size == nil {
		return unfit("PDF sans catalogue")
	}
	b := &builder{out: bytes.NewBuffer(append([]byte{}, bg.raw...)), next: *bg.ctx.Size, from: donor, numbers: map[int]int{}, offsets: map[int]int{}}
	if !bytes.HasSuffix(bg.raw, []byte("\n")) {
		b.out.WriteByte('\n')
	}
	// Fonts of the donor, under new names.
	refs := make([]int, 0, len(used))
	for r := range used {
		refs = append(refs, r)
	}
	sort.Ints(refs)
	names := map[string]string{}
	fonts := types.Dict{}
	for i, r := range refs {
		n, err := b.copy(r)
		if err != nil {
			return err
		}
		names[strconv.Itoa(r)] = fmt.Sprintf("DIB%d", i)
		fonts[fmt.Sprintf("DIB%d", i)] = types.IndirectRef{ObjectNumber: types.Integer(n)}
	}
	for i := range o.Slots {
		for k, c := range o.Slots[i].Chars {
			c.Font = names[c.Font]
			o.Slots[i].Chars[k] = c
		}
	}
	open, closing := b.stream("q\n"), b.stream("Q\n")
	o.Pages = make([]int, len(bg.pages))
	for i := range bg.pages {
		o.Pages[i] = b.next + i
	}
	b.next += len(bg.pages)
	for i, p := range bg.pages {
		d := types.Dict{}
		for k, v := range p.dict {
			d[k] = v
		}
		contents := types.Array{types.IndirectRef{ObjectNumber: types.Integer(open)}}
		switch c := p.dict["Contents"].(type) {
		case types.IndirectRef:
			contents = append(contents, c)
		case types.Array:
			contents = append(contents, c...)
		}
		d["Contents"] = append(contents, types.IndirectRef{ObjectNumber: types.Integer(closing)}, types.IndirectRef{ObjectNumber: types.Integer(o.Pages[i])})
		res := types.Dict{}
		for k, v := range p.resources {
			res[k] = v
		}
		own := types.Dict{}
		if fd, ok := p.resources.Find("Font"); ok {
			if named, err := bg.ctx.DereferenceDict(fd); err == nil {
				for k, v := range named {
					own[k] = v
				}
			}
		}
		for k, v := range fonts {
			own[k] = v
		}
		res["Font"] = own
		d["Resources"] = res
		b.object(p.ref, plain(d, nil))
	}
	start := b.xref()
	fmt.Fprintf(b.out, "trailer\n<</Size %d/Root %s", b.next, plain(*bg.ctx.Root, nil))
	if bg.ctx.Info != nil {
		fmt.Fprintf(b.out, "/Info %s", plain(*bg.ctx.Info, nil))
	}
	if len(bg.ctx.ID) > 0 {
		fmt.Fprintf(b.out, "/ID %s", plain(bg.ctx.ID, nil))
	}
	fmt.Fprintf(b.out, "/Prev %d>>\nstartxref\n%d\n%%%%EOF\n", prev, start)
	o.Base, o.Prev, o.Size, o.Root = b.out.Bytes(), start, b.next, plain(*bg.ctx.Root, nil)
	if bg.ctx.Info != nil {
		o.Info = plain(*bg.ctx.Info, nil)
	}
	if len(bg.ctx.ID) > 0 {
		o.ID = plain(bg.ctx.ID, nil)
	}
	return nil
}

type builder struct {
	out     *bytes.Buffer
	next    int
	from    *document
	numbers map[int]int // donor object -> base object
	offsets map[int]int
}

func (b *builder) object(n int, body string) {
	b.offsets[n] = b.out.Len()
	fmt.Fprintf(b.out, "%d 0 obj\n%s\nendobj\n", n, body)
}

func (b *builder) stream(content string) int {
	n := b.next
	b.next++
	b.offsets[n] = b.out.Len()
	fmt.Fprintf(b.out, "%d 0 obj\n<</Length %d>>\nstream\n%s\nendstream\nendobj\n", n, len(content), content)
	return n
}

// copy brings an object of the donor and what it refers to into the base.
func (b *builder) copy(r int) (int, error) {
	if n, ok := b.numbers[r]; ok {
		return n, nil
	}
	n := b.next
	b.next++
	b.numbers[r] = n
	e, found := b.from.ctx.FindTableEntryLight(r)
	if !found || e.Object == nil {
		return 0, unfit("objet %d absent du PDF", r)
	}
	// What it refers to comes first, numbered before it is written.
	var refs []int
	collect(e.Object, &refs)
	for _, x := range refs {
		if _, err := b.copy(x); err != nil {
			return 0, err
		}
	}
	switch v := e.Object.(type) {
	case types.StreamDict:
		d := types.Dict{}
		for k, x := range v.Dict {
			d[k] = x
		}
		d["Length"] = types.Integer(len(v.Raw))
		b.offsets[n] = b.out.Len()
		fmt.Fprintf(b.out, "%d 0 obj\n%s\nstream\n", n, plain(d, b.numbers))
		b.out.Write(v.Raw)
		b.out.WriteString("\nendstream\nendobj\n")
	default:
		b.object(n, plain(e.Object, b.numbers))
	}
	return n, nil
}

func collect(o types.Object, refs *[]int) {
	switch v := o.(type) {
	case types.IndirectRef:
		*refs = append(*refs, v.ObjectNumber.Value())
	case types.Dict:
		keys := make([]string, 0, len(v))
		for k := range v {
			if k != "Length" {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			collect(v[k], refs)
		}
	case types.StreamDict:
		collect(v.Dict, refs)
	case types.Array:
		for _, x := range v {
			collect(x, refs)
		}
	}
}

// plain writes an object in PDF syntax, its references renumbered.
func plain(o types.Object, numbers map[int]int) string {
	switch v := o.(type) {
	case nil:
		return "null"
	case types.IndirectRef:
		n := v.ObjectNumber.Value()
		if m, ok := numbers[n]; ok {
			n = m
		}
		return fmt.Sprintf("%d 0 R", n)
	case types.Dict:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var s strings.Builder
		s.WriteString("<<")
		for _, k := range keys {
			s.WriteString(types.Name(k).PDFString())
			s.WriteByte(' ')
			s.WriteString(plain(v[k], numbers))
		}
		s.WriteString(">>")
		return s.String()
	case types.Array:
		parts := make([]string, len(v))
		for i, x := range v {
			parts[i] = plain(x, numbers)
		}
		return "[" + strings.Join(parts, " ") + "]"
	}
	return o.PDFString()
}

// xref writes the cross-reference section of the objects written.
func (b *builder) xref() int {
	start := b.out.Len()
	b.out.WriteString("xref\n")
	writeXref(b.out, b.offsets)
	return start
}

func writeXref(out *bytes.Buffer, offsets map[int]int) {
	nums := make([]int, 0, len(offsets))
	for n := range offsets {
		nums = append(nums, n)
	}
	slices.Sort(nums)
	for i := 0; i < len(nums); {
		j := i
		for j+1 < len(nums) && nums[j+1] == nums[j]+1 {
			j++
		}
		fmt.Fprintf(out, "%d %d\n", nums[i], j-i+1)
		for _, n := range nums[i : j+1] {
			fmt.Fprintf(out, "%010d 00000 n \n", offsets[n])
		}
		i = j + 1
	}
}

type run struct {
	font  string
	codes []byte
}

// encode turns a value into runs of codes and measures it, in the units of
// the page.
func (s *Slot) encode(value string, p Place) ([]run, float64, error) {
	var runs []run
	width := 0.0
	for _, r := range value {
		key := string(r)
		c, ok := s.Chars[key]
		if !ok && (r == '\u00a0' || r == '\u202f') {
			c, ok = s.Chars[" "]
		}
		if !ok && r == ' ' {
			c, ok = s.Chars["\u00a0"]
		}
		if !ok {
			return nil, 0, &Refusal{fmt.Sprintf("caractère %q absent des polices du calque", r)}
		}
		w := c.Width/1000*p.Size + p.Tc
		if c.Code == 32 {
			w += p.Tw
		}
		width += w * p.Tz
		if n := len(runs); n > 0 && runs[n-1].font == c.Font {
			runs[n-1].codes = append(runs[n-1].codes, c.Code)
		} else {
			runs = append(runs, run{font: c.Font, codes: []byte{c.Code}})
		}
	}
	return runs, width, nil
}

// ErrTags is data for another template.
var ErrTags = errors.New("overlay: values do not match the tags")

// Text is what a PDF made by LibreOffice or by the overlay reads, line by
// line from the top of each page, without spaces nor empty boxes (the
// overlay ticks a box over the ☐ of its background).
func Text(raw []byte) (string, error) {
	d, err := read(raw)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for page := range d.pages {
		var gs []glyph
		for _, g := range d.glyphs {
			if g.page == page && strings.TrimSpace(g.text) != "" && g.text != "☐" {
				gs = append(gs, g)
			}
		}
		slices.SortStableFunc(gs, func(a, b glyph) int { return cmp.Compare(b.y(), a.y()) })
		for i := 0; i < len(gs); {
			j := i + 1
			for j < len(gs) && gs[i].y()-gs[j].y() < 2 {
				j++
			}
			line := gs[i:j]
			slices.SortStableFunc(line, func(a, b glyph) int { return cmp.Compare(a.x(), b.x()) })
			for _, g := range line {
				b.WriteString(g.text)
			}
			b.WriteByte('\n')
			i = j
		}
		b.WriteByte('\f')
	}
	return b.String(), nil
}

// Fit keeps what fits of text in the room of slot i, at least its first
// character, for test data that every place can take.
func (o *Overlay) Fit(i int, text string) string {
	s := &o.Slots[i]
	runes := []rune(text)
	if len(s.Places) == 0 || len(runes) == 0 {
		return text
	}
	for n := len(runes); n > 1; n-- {
		cut := strings.TrimSpace(string(runes[:n]))
		if _, width, err := s.encode(cut, s.Places[0]); err == nil && width <= s.Room*0.9 {
			return cut
		}
	}
	return string(runes[:1])
}

// Render writes the values of the tags on the background.
func (o *Overlay) Render(values []string) ([]byte, error) {
	if len(values) != len(o.Tags) {
		return nil, ErrTags
	}
	ops := make([]strings.Builder, len(o.Pages))
	for i := range o.Slots {
		s := &o.Slots[i]
		v := values[i]
		if v == s.Background || len(s.Places) == 0 {
			continue
		}
		for _, p := range s.Places {
			runs, width, err := s.encode(v, p)
			if err != nil {
				return nil, &Refusal{fmt.Sprintf("%v, balise %s", err, o.Tags[i])}
			}
			if width > s.Room+0.5 {
				return nil, &Refusal{fmt.Sprintf("valeur trop longue pour sa place (%.0f pt sur %.0f pt), balise %s", width, s.Room, o.Tags[i])}
			}
			b := &ops[p.Page]
			if p.Erase != nil {
				e := p.Erase
				fmt.Fprintf(b, "q 1 g %s %s %s %s re f Q\n", num(e[0]), num(e[1]), num(e[2]), num(e[3]))
			}
			tm := p.TM
			switch s.Align {
			case 1:
				tm = matrix{1, 0, 0, 1, p.MarkerAdvance - width, 0}.times(tm)
			case 2:
				tm = matrix{1, 0, 0, 1, (p.MarkerAdvance - width) / 2, 0}.times(tm)
			}
			fmt.Fprintf(b, "q %s cm BT %s %d Tr %s Tc %s Tw %s Tz %s Tm", p.CTM, p.Fill, p.Render, num(p.Tc), num(p.Tw), num(p.Tz*100), tm)
			for _, r := range runs {
				fmt.Fprintf(b, " /%s %s Tf <%X> Tj", r.font, num(p.Size), r.codes)
			}
			b.WriteString(" ET Q\n")
		}
	}
	out := bytes.NewBuffer(make([]byte, 0, len(o.Base)+4096))
	out.Write(o.Base)
	offsets := map[int]int{}
	for i, n := range o.Pages {
		content := ops[i].String()
		offsets[n] = out.Len()
		fmt.Fprintf(out, "%d 0 obj\n<</Length %d>>\nstream\n%s\nendstream\nendobj\n", n, len(content), content)
	}
	start := out.Len()
	out.WriteString("xref\n")
	writeXref(out, offsets)
	fmt.Fprintf(out, "trailer\n<</Size %d/Root %s", o.Size, o.Root)
	if o.Info != "" {
		fmt.Fprintf(out, "/Info %s", o.Info)
	}
	if o.ID != "" {
		fmt.Fprintf(out, "/ID %s", o.ID)
	}
	fmt.Fprintf(out, "/Prev %d>>\nstartxref\n%d\n%%%%EOF\n", o.Prev, start)
	return out.Bytes(), nil
}
