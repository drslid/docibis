package httpapi

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// callRenderNamed renders a template of the library with its data.
func callRenderNamed(h http.Handler, key, name, data string) *httptest.ResponseRecorder {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	_ = w.WriteField("templateName", name)
	_ = w.WriteField("data", data)
	_ = w.Close()
	req := httptest.NewRequest(http.MethodPost, "/v1/render", &b)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestSharedBlocks(t *testing.T) {
	srv, setup := open(t, nil)
	srv.SetRenderer(&fakeRenderer{})
	h := srv.Handler()
	admin := &client{t: t, h: h}
	admin.do(http.MethodPost, "/api/auth/setup", map[string]string{"setupCode": setup.String(), "username": "admin", "password": "a long enough password"})
	for path, body := range map[string]string{
		"clauses/rgpd.odt":   `<text:p>Vos données, &lt;&lt;client.nom&gt;&gt;, restent chez nous.</text:p>`,
		"contrats/offre.odt": `<text:p>Offre pour &lt;&lt;client.nom&gt;&gt;</text:p><text:p>&lt;&lt;ref:clauses/rgpd&gt;&gt;</text:p>`,
		"contrats/faux.odt":  `<text:p>&lt;&lt;ref:clauses/absente&gt;&gt;</text:p>`,
	} {
		if code, out := admin.send(http.MethodPost, "/api/templates", map[string]part{"file": {"x.odt", odtTemplate(t, body)}}, map[string]string{"path": path}); code != http.StatusCreated {
			t.Fatalf("create %s: %d %v", path, code, out)
		}
	}
	key := testKey(t, srv, "portail")
	render := func(name string) (int, string) {
		rec := callRenderNamed(h, key, name, `{"client": {"nom": "Dupont"}}`)
		return rec.Code, rec.Body.String()
	}
	if code, body := render("contrats/offre"); code != http.StatusOK || !strings.Contains(body, "Offre pour Dupont") || !strings.Contains(body, "Vos données, Dupont, restent chez nous.") || strings.Contains(body, "ref:") {
		t.Fatalf("render: %d %s", code, body)
	}
	if code, body := render("contrats/faux"); code != http.StatusBadRequest || !strings.Contains(body, "Bloc partagé") || !strings.Contains(body, "clauses/absente") {
		t.Fatalf("missing block: %d %s", code, body)
	}
}
