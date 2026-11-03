package merge

import (
	"archive/zip"
	"bytes"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// Issue is a problem of a template, found before any data: error, the
// document will be wrong; warning, to check; info, good to know.
type Issue struct {
	Level   string `json:"level"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// LintOptions tells what the server offers.
type LintOptions struct {
	// Font tells whether a font family is installed; nil skips the check.
	Font func(family string) bool
}

// substitutes are the free fonts LibreOffice uses with the same metrics.
var substitutes = map[string]string{
	"calibri": "Carlito", "cambria": "Caladea", "arial": "Liberation Sans", "helvetica": "Liberation Sans",
	"times new roman": "Liberation Serif", "times": "Liberation Serif", "courier new": "Liberation Mono", "arial narrow": "Liberation Sans Narrow",
}

// Lint checks a template prepared as ODT: tags left open, sections and
// repeats not closed, expressions that cannot be read, image placeholders
// without an image, tracked changes, comments and missing fonts.
func Lint(template []byte, o LintOptions) ([]Issue, error) {
	zr, err := zip.NewReader(bytes.NewReader(template), int64(len(template)))
	if err != nil {
		return nil, fmt.Errorf("template is not an ODT: %w", err)
	}
	var issues []Issue
	seen := map[string]bool{}
	add := func(level, code, format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		if !seen[code+msg] {
			seen[code+msg] = true
			issues = append(issues, Issue{level, code, msg})
		}
	}
	trees := map[string]*node{}
	tags := 0
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
			return nil, fmt.Errorf("%s: %w", f.Name, err)
		}
		normalize(tree)
		trees[f.Name] = tree
		tags += lintTags(tree, add)
		lintDocument(tree, add)
	}
	if tags == 0 {
		add("info", "no-tags", "Aucune balise « <<\u2026>> » : le document sera toujours le même.")
	}
	if o.Font != nil {
		for _, family := range usedFonts(trees) {
			lower := strings.ToLower(family)
			if o.Font(family) || strings.HasPrefix(lower, "wingdings") || lower == "symbol" || lower == "opensymbol" {
				continue
			}
			if sub, ok := substitutes[lower]; ok && o.Font(sub) {
				add("info", "font-substituted", "Police « %s » remplacée par %s, de mêmes dimensions : la mise en page ne change pas.", family, sub)
				continue
			}
			add("warning", "font-missing", "Police « %s » absente du serveur : LibreOffice en prend une autre et la mise en page peut changer (lignes, pages). Demandez son ajout ou changez de police.", family)
		}
	}
	order := map[string]int{"error": 0, "warning": 1, "info": 2}
	sort.SliceStable(issues, func(i, j int) bool { return order[issues[i].Level] < order[issues[j].Level] })
	return issues, nil
}

func short(s string) string {
	if utf8.RuneCountInString(s) > 40 {
		return string([]rune(s)[:40]) + "\u2026"
	}
	return s
}

// lintTags checks the tags of a part and returns how many there are.
func lintTags(tree *node, add func(level, code, format string, args ...any)) int {
	// Tags left open: a << that no >> closes in its paragraph.
	tree.walk(func(n *node) bool {
		if !isParagraph(n) {
			return true
		}
		text := paragraphText(n)
		spans := tagPattern.FindAllStringIndex(text, -1)
		inside := func(i int) bool {
			for _, s := range spans {
				if i >= s[0] && i < s[1] {
					return true
				}
			}
			return false
		}
		for i := strings.Index(text, "<<"); i >= 0; {
			if !inside(i) {
				add("error", "tag-open", "Balise non fermée : « %s » (les >> manquent, ou la balise est coupée par un retour à la ligne).", short(text[i:]))
				break
			}
			next := strings.Index(text[i+2:], "<<")
			if next < 0 {
				break
			}
			i += 2 + next
		}
		return true
	})

	type opened struct {
		o    occurrence
		kind string
	}
	var stack []opened
	all := occurrences(tree)
	for _, o := range all {
		tag := quotes.Replace(o.tag)
		path, options, _ := strings.Cut(tag, "{")
		switch {
		case strings.HasPrefix(tag, "cs_"):
			stack = append(stack, opened{o, "cs"})
			if expr := strings.TrimPrefix(tag, "cs_"); strings.HasPrefix(expr, "{") {
				lintExpr(strings.Trim(expr, "{}"), tag, add)
			}
		case strings.HasPrefix(tag, "rs_"):
			stack = append(stack, opened{o, "rs"})
		case tag == "else" || strings.HasPrefix(tag, "else_"):
			if len(stack) == 0 || stack[len(stack)-1].kind != "cs" {
				add("error", "else-alone", "« <<%s>> » hors d'une section « <<cs_\u2026>> ».", short(tag))
			}
			if expr := strings.TrimPrefix(tag, "else_"); expr != tag && strings.HasPrefix(expr, "{") {
				lintExpr(strings.Trim(expr, "{}"), tag, add)
			}
		case strings.HasPrefix(tag, "es_"):
			if len(stack) == 0 {
				add("error", "end-alone", "« <<%s>> » ne ferme rien : il manque le « <<cs_\u2026>> » ou le « <<rs_\u2026>> » qui l'ouvre.", tag)
				continue
			}
			top := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			name := strings.TrimPrefix(tag, "es_")
			if top.kind == "rs" {
				if want := strings.TrimPrefix(top.o.tag, "rs_"); name != "" && name != want {
					add("warning", "end-name", "« <<%s>> » ferme « <<%s>> » : vérifiez l'ordre des balises.", tag, top.o.tag)
				}
				if top.o.para != o.para {
					if t, _, _ := commonAncestor(top.o.para, o.para); t == nil {
						add("error", "repeat-split", "« <<%s>> » et sa fin ne sont pas dans le même bloc (cellule, cadre) : la répétition est ignorée.", top.o.tag)
					}
				}
			}
		case strings.HasPrefix(tag, "rr_"):
			name := strings.TrimPrefix(tag, "rr_")
			row := o.para.ancestor("table:table-row")
			if row == nil {
				add("error", "row-outside", "« <<%s>> » doit être dans une ligne de tableau.", tag)
				continue
			}
			found := false
			for n := row; n != nil && !found; {
				found = hasTag(n, "er_"+name)
				i := n.index() + 1
				if i >= len(n.parent.children) {
					break
				}
				n = n.parent.children[i]
			}
			if !found {
				add("error", "row-open", "« <<%s>> » sans « <<er_%s>> » dans le même tableau.", tag, name)
			}
		case strings.HasPrefix(tag, "cr_"), strings.HasPrefix(tag, "cc_"):
			if o.para.ancestor("table:table-row") == nil {
				add("error", "row-outside", "« <<%s>> » doit être dans une ligne de tableau.", short(tag))
			}
			if expr := tag[3:]; strings.HasPrefix(expr, "{") {
				lintExpr(strings.Trim(expr, "{}"), tag, add)
			}
		case tag == "pageBreak", tag == "pageBreakNotLast":
		case strings.HasPrefix(tag, "er_"), strings.HasPrefix(tag, "##"):
		case strings.HasPrefix(tag, "img_"), strings.HasPrefix(tag, "imgstretch_"):
			add("warning", "image-kind", "« <<%s>> » : seules les images en signet « imgfit_ » sont remplies.", tag)
		case strings.HasPrefix(tag, "{"):
			lintExpr(strings.Trim(tag, "{}"), tag, add)
		case strings.HasPrefix(path, "$"):
		case strings.HasPrefix(tag, "ref:"):
		case strings.HasPrefix(tag, "op:") || strings.HasPrefix(tag, "link:") || strings.HasPrefix(tag, "link_"):
			if _, rest := fieldPrefix(path); !plainPath.MatchString(rest) {
				add("error", "path", "Balise illisible : « <<%s>> » (lettres, chiffres, points et _ seulement après le préfixe).", short(tag))
			}
		case !plainPath.MatchString(strings.TrimSpace(path)):
			add("error", "path", "Balise illisible : « <<%s>> » (lettres, chiffres, points et _ seulement).", short(tag))
		case options != "":
			switch r := strings.ToLower(options); {
			case strings.Contains(r, "renderer=boolean"), strings.Contains(r, "renderer=yesno"), strings.HasPrefix(r, "numformat="), strings.HasPrefix(r, "dateformat="):
			default:
				add("warning", "option", "Option inconnue dans « <<%s>> » : elle est ignorée.", short(tag))
			}
		}
	}
	for _, s := range stack {
		end := "es_" + strings.TrimPrefix(strings.TrimPrefix(s.o.tag, "cs_"), "rs_")
		if s.kind == "cs" {
			end = "es_\u2026"
		}
		add("error", "not-closed", "« <<%s>> » n'est jamais fermé : ajoutez « <<%s>> ».", short(s.o.tag), end)
	}

	// Image placeholders: a bookmark imgfit_x must hold an image.
	tree.walk(func(n *node) bool {
		if name := n.attr("text:name"); n.name == "text:bookmark-start" && strings.HasPrefix(name, "imgfit_") {
			para := n.parent
			for para != nil && !isParagraph(para) {
				para = para.parent
			}
			if para != nil {
				if frames, _ := placeholder(para, n, name); len(frames) == 0 {
					add("error", "image-empty", "Le signet « %s » n'entoure aucune image : placez une image d'exemple dans le signet.", name)
				}
			}
		}
		return true
	})
	return len(all)
}

func lintExpr(src, tag string, add func(level, code, format string, args ...any)) {
	e, err := parseExpr(src)
	if err == nil {
		err = check(e)
	}
	if err != nil {
		add("error", "expression", "Expression illisible dans « <<%s>> » : %v.", short(tag), err)
	}
}

// lintDocument looks for what Word leaves behind: tracked changes, comments.
func lintDocument(tree *node, add func(level, code, format string, args ...any)) {
	changes, comments := 0, 0
	tree.walk(func(n *node) bool {
		switch n.name {
		case "text:changed-region":
			changes++
		case "office:annotation":
			comments++
		}
		return true
	})
	if changes > 0 {
		add("warning", "tracked-changes", "%d modification(s) suivie(s) (révisions Word) : acceptez-les ou refusez-les dans Word avant d'importer.", changes)
	}
	if comments > 0 {
		add("info", "comments", "%d commentaire(s) Word : ils n'apparaissent pas dans les documents produits.", comments)
	}
}

// usedFonts lists the font families of the styles the text uses.
func usedFonts(trees map[string]*node) []string {
	type style struct{ font, parent string }
	styles := map[string]style{}
	faces := map[string]string{}
	defaultFont := ""
	used := map[string]bool{"Standard": true}
	for _, tree := range trees {
		tree.walk(func(n *node) bool {
			switch n.name {
			case "style:font-face":
				faces[n.attr("style:name")] = strings.Trim(n.attr("svg:font-family"), `'"`)
			case "style:style", "style:default-style":
				font := ""
				for _, c := range n.children {
					if c.name == "style:text-properties" {
						if font = c.attr("style:font-name"); font == "" {
							font = strings.Trim(c.attr("fo:font-family"), `'"`)
						}
					}
				}
				if n.name == "style:default-style" {
					if n.attr("style:family") == "paragraph" && font != "" {
						defaultFont = font
					}
				} else {
					styles[n.attr("style:name")] = style{font, n.attr("style:parent-style-name")}
				}
			case "office:automatic-styles", "office:styles":
				return true
			}
			if s := n.attr("text:style-name"); s != "" {
				used[s] = true
			}
			if s := n.attr("table:style-name"); s != "" {
				used[s] = true
			}
			return true
		})
	}
	families := map[string]bool{}
	for name := range used {
		font := ""
		for s, i := name, 0; s != "" && i < 20 && font == ""; i++ {
			st, ok := styles[s]
			if !ok {
				break
			}
			font, s = st.font, st.parent
		}
		if font == "" {
			font = defaultFont
		}
		if family := faces[font]; family != "" {
			families[family] = true
		} else if font != "" {
			families[font] = true
		}
	}
	out := make([]string, 0, len(families))
	for f := range families {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}
