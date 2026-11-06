package httpapi

import (
	"net/http"
	"strconv"
	"strings"
)

// formats lists, in order, the formats LibreOffice produces a document in,
// with their content type. The PDF options only apply to PDF.
var formats = []struct{ name, contentType string }{
	{"pdf", "application/pdf"},
	{"docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
	{"doc", "application/msword"},
	{"odt", "application/vnd.oasis.opendocument.text"},
	{"rtf", "application/rtf"},
	{"html", "text/html; charset=utf-8"},
	{"txt", "text/plain; charset=utf-8"},
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
