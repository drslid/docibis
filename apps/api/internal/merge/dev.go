package merge

import (
	"fmt"
	"strings"
)

// devStyle is the text style of the problems shown in development mode.
const devStyle = "DocIbisDev"

// addDevStyle declares the style of the problems in a part of the document.
func addDevStyle(tree *node) {
	var doc *node
	for _, c := range tree.children {
		if c.kind == element && strings.HasPrefix(c.name, "office:document") {
			doc = c
		}
	}
	if doc == nil {
		return
	}
	var styles *node
	for _, c := range doc.children {
		if c.name == "office:automatic-styles" {
			styles = c
		}
	}
	if styles == nil {
		styles = &node{kind: element, name: "office:automatic-styles"}
		at := len(doc.children)
		for i, c := range doc.children {
			if c.name == "office:master-styles" || c.name == "office:body" {
				at = i
				break
			}
		}
		styles.parent = doc
		doc.children = append(doc.children[:at], append([]*node{styles}, doc.children[at:]...)...)
	}
	style := &node{kind: element, name: "style:style", parent: styles}
	style.setAttr("style:name", devStyle)
	style.setAttr("style:family", "text")
	props := &node{kind: element, name: "style:text-properties", parent: style}
	props.setAttr("fo:color", "#c00000")
	props.setAttr("fo:background-color", "#ffff00")
	props.setAttr("fo:font-weight", "bold")
	style.children = []*node{props}
	styles.children = append(styles.children, style)
}

// problem tells, in development mode, why a field cannot be filled: data the
// request does not have, or an expression that cannot be read or run.
func (m *merger) problem(tag string, s scope) string {
	tag = quotes.Replace(tag)
	switch kind, rest := fieldPrefix(tag); {
	case control(tag), strings.HasPrefix(tag, "$"), kind != "", strings.Contains(strings.ToLower(tag), "renderer="):
		return ""
	case strings.HasPrefix(rest, "{") && strings.HasSuffix(rest, "}"):
		src := rest[1 : len(rest)-1]
		e, err := parseExpr(src)
		if err == nil {
			err = check(e)
		}
		if err == nil {
			_, err = evaluator{s, m.now}.eval(e)
		}
		if err != nil {
			return fmt.Sprintf("<<%s>> : %v", tag, err)
		}
		return ""
	}
	path, _, _ := strings.Cut(tag, "{")
	if _, ok := s.lookup(path); !ok {
		return fmt.Sprintf("<<%s>> : donnée absente", tag)
	}
	return ""
}

// devSpan shows a problem in the line, highlighted.
func devSpan(text string) *node {
	span := &node{kind: element, name: "text:span"}
	span.setAttr("text:style-name", devStyle)
	span.children = []*node{{kind: textNode, text: "[" + text + "]", parent: span}}
	return span
}
