package merge

import (
	"fmt"
	"strings"
	"time"
)

// What the overlay engine needs from the merge: the tags in the order of the
// document (Options.Mark) and the text of each one for some data.

// Boxed tells a tag drawn as a box ☑ or ☐.
func Boxed(tag string) bool {
	_, options, _ := strings.Cut(quotes.Replace(tag), "{")
	return strings.Contains(strings.ToLower(options), "renderer=boolean")
}

// Unchecked is the box of a false value, Checked the one of a true value.
const (
	Unchecked = unchecked
	Checked   = checked
)

// Values computes the text of each tag for data, as Fill writes it at the
// root of the data: a fixed layout is filled this way, without the document.
func Values(tags []string, data []byte, now time.Time) ([]string, error) {
	root, err := decode(data)
	if err != nil {
		return nil, err
	}
	m := &merger{now: now}
	out := make([]string, len(tags))
	for i, tag := range tags {
		if control(quotes.Replace(tag)) {
			continue
		}
		text, c := m.field(tag, scope{root})
		if c != nil {
			return nil, fmt.Errorf("%s draws an image", tag)
		}
		out[i] = text
	}
	return out, nil
}
