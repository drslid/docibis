package merge

import (
	"net/url"
	"strings"
)

// fieldPrefix splits a prefix that changes how a field shows: "op" for
// <<op:x>> (its paragraph goes when the value is blank), "link" for
// <<link:x>> or <<link_x>> (a hyperlink). Empty for a plain field.
func fieldPrefix(tag string) (kind, rest string) {
	for _, p := range []struct{ prefix, kind string }{{"op:", "op"}, {"link:", "link"}, {"link_", "link"}} {
		if r, ok := strings.CutPrefix(tag, p.prefix); ok {
			return p.kind, strings.TrimSpace(r)
		}
	}
	return "", tag
}

// hyperlink makes the link of <<link:x>> from "address" or "text|address";
// nil when the address is neither a web page nor an e-mail address, which
// then shows as text.
func hyperlink(value string) (*node, string) {
	label, address, ok := strings.Cut(value, "|")
	if !ok {
		label, address = value, value
	}
	label, address = strings.TrimSpace(label), strings.TrimSpace(address)
	if label == "" {
		label = address
	}
	u, err := url.Parse(address)
	if err != nil || address == "" || !(u.Scheme == "http" || u.Scheme == "https" || u.Scheme == "mailto") {
		return nil, label
	}
	a := &node{kind: element, name: "text:a"}
	a.setAttr("xlink:type", "simple")
	a.setAttr("xlink:href", address)
	a.setAttr("text:style-name", "Internet_20_link")
	a.setAttr("text:visited-style-name", "Visited_20_Internet_20_Link")
	a.children = []*node{{kind: textNode, text: label, parent: a}}
	return a, label
}
