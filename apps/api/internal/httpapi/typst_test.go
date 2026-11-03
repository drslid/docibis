package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

func TestTypstTemplates(t *testing.T) {
	srv, setup := open(t, nil)
	srv.SetRenderer(&fakeRenderer{})
	h := srv.Handler()
	admin := &client{t: t, h: h}
	admin.do(http.MethodPost, "/api/auth/setup", map[string]string{"setupCode": setup.String(), "username": "admin", "password": "a long enough password"})
	source := "#let data = json(bytes(sys.inputs.data))\n= Offre\nBonjour #data.client.nom\n"
	code, body := admin.send(http.MethodPost, "/api/templates", map[string]part{"file": {"offre.typ", []byte(source)}}, map[string]string{"path": "typst/offre.typ"})
	if code != http.StatusCreated || body["template"].(map[string]any)["kind"] != "typst" {
		t.Fatalf("create: %d %v", code, body)
	}
	if code, _ := admin.send(http.MethodPost, "/api/templates", map[string]part{"file": {"x.typ", []byte("#import \"@preview/x:1.0.0\"")}}, map[string]string{"path": "typst/paquet.typ"}); code != http.StatusBadRequest {
		t.Fatalf("a package import must be refused: %d", code)
	}
	key := testKey(t, srv, "portail")
	data := `{"client": {"nom": "Dupont"}}`
	if rec := callRenderNamed(h, key, "typst/offre", data); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("without the Typst workers: %d %s", rec.Code, rec.Body.String())
	}
	native := &fakeNative{}
	srv.SetNative(native)
	rec := callRenderNamed(h, key, "typst/offre", data)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/pdf" || !strings.Contains(rec.Body.String(), "%PDF-native") ||
		!strings.Contains(rec.Body.String(), `"nom": "Dupont"`) || rec.Header().Get("Content-Disposition") != "attachment; filename=offre.pdf" {
		t.Fatalf("render: %d %v %s", rec.Code, rec.Header(), rec.Body.String())
	}
	if rec := callRenderNamed(h, key, "typst/offre", "pas du JSON"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad data: %d", rec.Code)
	}
}
