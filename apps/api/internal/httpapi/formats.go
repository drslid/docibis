package httpapi

import (
	"net/http"
	"strconv"
	"strings"
)

// formats lists, in order, the formats LibreOffice produces a document in,
// with their content type and the kind of template they come from (any when
// empty). The PDF options only apply to PDF.
var formats = []struct{ name, contentType, family string }{
	{"pdf", "application/pdf", ""},
	{"docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "text"},
	{"doc", "application/msword", "text"},
	{"odt", "application/vnd.oasis.opendocument.text", "text"},
	{"rtf", "application/rtf", "text"},
	{"html", "text/html; charset=utf-8", "text"},
	{"txt", "text/plain; charset=utf-8", "text"},
	{"xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "spreadsheet"},
	{"ods", "application/vnd.oasis.opendocument.spreadsheet", "spreadsheet"},
	{"pptx", "application/vnd.openxmlformats-officedocument.presentationml.presentation", "presentation"},
	{"odp", "application/vnd.oasis.opendocument.presentation", "presentation"},
}

// formatFor tells whether a template of a family (text, spreadsheet,
// presentation) gives a document in format.
func formatFor(format, family string) error {
	var names []string
	for _, f := range formats {
		if f.family == "" || f.family == family {
			if f.name == format {
				return nil
			}
			names = append(names, f.name)
		}
	}
	what := map[string]string{"text": "Un modèle Word", "spreadsheet": "Un modèle Excel", "presentation": "Un modèle PowerPoint"}[family]
	if what == "" {
		return nil
	}
	return refuse("%s ne se produit pas en %s : formats possibles %s.", what, format, strings.Join(names, ", "))
}

// formatAliases are other names applications give a format.
var formatAliases = map[string]string{"htm": "html", "text": "txt"}

// outputFormat normalizes a format asked for, or tells it is unknown.
func outputFormat(name string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if alias, ok := formatAliases[name]; ok {
		name = alias
	}
	if contentType(name) == "" {
		return name, refuse("Format inconnu : %q. Formats possibles : %s.", name, formatNames())
	}
	return name, nil
}

func contentType(format string) string {
	for _, f := range formats {
		if f.name == format {
			return f.contentType
		}
	}
	return ""
}

func formatNames() string {
	names := make([]string, len(formats))
	for i, f := range formats {
		names[i] = f.name
	}
	return strings.Join(names, ", ")
}

// documentResponse writes a document of any format, for the routes that
// return one: the generated types only know PDF and Word.
type documentResponse struct {
	body    []byte
	format  string
	headers map[string]string
}

func (r documentResponse) write(w http.ResponseWriter) error {
	h := w.Header()
	h.Set("Content-Type", contentType(r.format))
	h.Set("Content-Length", strconv.Itoa(len(r.body)))
	for k, v := range r.headers {
		if v != "" {
			h.Set(k, v)
		}
	}
	if r.format == "html" {
		// Opened in the browser, an HTML document runs nothing.
		h.Set("Content-Security-Policy", "sandbox")
	}
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(r.body)
	return err
}

func (r documentResponse) VisitRenderDocumentResponse(w http.ResponseWriter) error { return r.write(w) }

func (r documentResponse) VisitDocmosisRenderResponse(w http.ResponseWriter) error { return r.write(w) }

func (r documentResponse) VisitRegenerateRenderResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r documentResponse) VisitGetRenderJobDocumentResponse(w http.ResponseWriter) error {
	return r.write(w)
}
