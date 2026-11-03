package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"docibis/internal/store"
)

// fakeNative stands for the Typst workers: the "PDF" carries the copy and
// the data it got.
type fakeNative struct {
	mu     sync.Mutex
	calls  int
	inputs map[string]string
	err    error
}

func (f *fakeNative) Compile(_ context.Context, in, out string, inputs map[string]string) (time.Duration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.inputs = inputs
	if f.err != nil {
		return 0, f.err
	}
	source, err := os.ReadFile(in)
	if err != nil {
		return 0, err
	}
	return time.Millisecond, os.WriteFile(out, []byte("%PDF-native\n"+string(source)+"\n"+inputs["data"]), 0o600)
}

func TestNativeText(t *testing.T) {
	for data, want := range map[string]bool{
		`{"nom":"Éléa Œuvré-Dupont","montant":"12,50 €","note":"l'été : oui (100 %)"}`: true,
		`{"lignes":[{"x":"Ünal ß"}],"n":3,"ok":true}`:                                  true,
		`{"nom":"Δημήτρης"}`:                                                           false,
		`{"nom":"Dupont 😀"}`:                                                           false,
		`{"note":"« oui »"}`:                                                           false,
		`{"note":"l’été – oui…"}`:                                                      false,
		"{\"montant\":\"5\u202f000 €\"}":                                               false,
		`pas du JSON`:                                                                  false,
	} {
		if got := nativeText([]byte(data)); got != want {
			t.Errorf("nativeText(%s) = %v", data, got)
		}
	}
}

func TestNativeData(t *testing.T) {
	v := store.TemplateVersion{
		Sample:        `{"titre":"Exemple titre","clients":[{"mail":"exemple@exemple.fr","age":"76"}]}`,
		ReferenceData: `{"titre":"Devis","clients":[{"mail":"marie.dupont@example.fr","age":"54"},{"mail":"jean@example.fr","age":"60"}]}`,
	}
	sets := nativeData(v)
	var lists, texts, mails bool
	for _, raw := range sets {
		var d struct {
			Titre   string `json:"titre"`
			Clients []struct {
				Mail, Age string
			} `json:"clients"`
		}
		if err := json.Unmarshal(raw, &d); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		lists = lists || len(d.Clients) == 10
		texts = texts || d.Titre == longText
		for _, c := range d.Clients {
			mails = mails || strings.HasPrefix(c.Mail, "prenom.nom-de-famille") && c.Age != longText
		}
	}
	if !lists || !texts || !mails {
		t.Fatalf("variants missing (lists %v, texts %v, mails %v) in %d sets", lists, texts, mails, len(sets))
	}
}

// The copy only serves plain PDFs of a version it was checked for.
func TestRenderNativeDeclines(t *testing.T) {
	s := &Server{}
	s.SetNative(&fakeNative{})
	qualified := `{"state":"qualified","sourceSha256":""}`
	sha := strings.Repeat("a", 64)
	for name, f := range map[string]tryForm{
		"not in the library": {native: qualified, nativeSHA: sha},
		"no copy":            {libVersionID: 1, native: qualified},
		"not qualified":      {libVersionID: 1, native: `{"state":"refused"}`, nativeSHA: sha},
		"PDF/A":              {libVersionID: 1, native: qualified, nativeSHA: sha, opts: renderOptions{pdfa: "PDF/A-2b"}},
		"watermark":          {libVersionID: 1, native: qualified, nativeSHA: sha, opts: renderOptions{watermark: watermark{text: "COPIE"}}},
		"developer mode":     {libVersionID: 1, native: qualified, nativeSHA: sha, opts: renderOptions{devMode: true}},
	} {
		doc := document{format: "pdf"}
		if s.renderNative(t.Context(), f, &doc, t.TempDir()) || doc.engine != "" {
			t.Errorf("%s: the copy served the document", name)
		}
	}
	if s.renderNative(t.Context(), tryForm{libVersionID: 1, native: qualified, nativeSHA: sha}, &document{format: "docx"}, t.TempDir()) {
		t.Error("the copy served a Word document")
	}
}

func TestCheckNativeSource(t *testing.T) {
	for source, ok := range map[string]bool{
		"= Proposition\n#sys.inputs.data": true,
		"":                                false,
		"#import \"@preview/cetz:0.3.0\"": false,
		"a\x00b":                          false,
		string([]byte{0xff, 0xfe}):        false,
	} {
		if err := checkNativeSource([]byte(source)); (err == nil) != ok {
			t.Errorf("checkNativeSource(%q) = %v", source, err)
		}
	}
}

// A Typst copy goes with one version: attached by an administrator, read by
// editors, used once qualified, never by a new version of the Word file.
func TestNativeCopy(t *testing.T) {
	srv, setup := open(t, nil)
	h := srv.Handler()
	admin := &client{t: t, h: h}
	if code, _, _ := admin.do(http.MethodPost, "/api/auth/setup", map[string]string{"setupCode": setup.String(), "username": "admin", "password": "a long enough password"}); code != http.StatusOK {
		t.Fatalf("setup: %d", code)
	}
	srv.SetRenderer(&fakeRenderer{})
	tpl := odtTemplate(t, `<text:p>Bonjour &lt;&lt;nom&gt;&gt;</text:p>`)
	code, body := admin.send(http.MethodPost, "/api/templates", map[string]part{"file": {"tarif.odt", tpl}}, map[string]string{"path": "funeral/tarif.odt"})
	if code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, body)
	}
	id := int64(body["template"].(map[string]any)["id"].(float64))
	native := fmt.Sprintf("/api/templates/%d/versions/1/native", id)
	source := "= Proposition\n#let data = json(bytes(sys.inputs.data))\n"

	// Without the Typst workers, the copy is kept but cannot serve.
	code, body = admin.send(http.MethodPut, native, map[string]part{"source": {"tarif.typ", []byte(source)}}, nil)
	if code != http.StatusOK || body["state"] != "error" || !strings.Contains(fmt.Sprint(body["reason"]), "pas démarré") ||
		body["sourceSha256"] != hashText(source) || body["by"] != "admin" {
		t.Fatalf("attach: %d %v", code, body)
	}
	if code, _ := admin.send(http.MethodPut, native, map[string]part{"source": {"x.typ", []byte("#import \"@preview/x:1.0.0\"")}}, nil); code != http.StatusBadRequest {
		t.Fatalf("package import: %d", code)
	}
	if rec := admin.raw(http.MethodGet, native, "", nil); rec.Code != http.StatusOK || rec.Body.String() != source {
		t.Fatalf("read: %d %q", rec.Code, rec.Body.String())
	}
	_, detail, _ := admin.do(http.MethodGet, fmt.Sprintf("/api/templates/%d", id), nil)
	if versions, _ := detail["versions"].([]any); len(versions) != 1 || versions[0].(map[string]any)["native"].(map[string]any)["state"] != "error" {
		t.Fatalf("detail: %v", detail)
	}

	// Editors read and compare it again; only administrators attach or remove one.
	for _, u := range []map[string]any{
		{"username": "eve", "firstName": "Eve", "lastName": "Martin", "email": "eve@example.fr", "role": "editor", "password": "a long enough password"},
		{"username": "vic", "firstName": "Vic", "lastName": "Petit", "email": "vic@example.fr", "role": "viewer", "password": "a long enough password"},
	} {
		if code, out, _ := admin.do(http.MethodPost, "/api/users", u); code != http.StatusCreated {
			t.Fatalf("user: %d %v", code, out)
		}
	}
	signIn := func(name string) *client {
		c := &client{t: t, h: h}
		if code, out, _ := c.do(http.MethodPost, "/api/auth/login", map[string]string{"username": name, "password": "a long enough password"}); code != http.StatusOK {
			t.Fatalf("login %s: %d %v", name, code, out)
		}
		return c
	}
	editor, viewer := signIn("eve"), signIn("vic")
	if code, _ := editor.send(http.MethodPut, native, map[string]part{"source": {"t.typ", []byte(source)}}, nil); code != http.StatusForbidden {
		t.Fatalf("editor attaches: %d", code)
	}
	if rec := editor.raw(http.MethodGet, native, "", nil); rec.Code != http.StatusOK {
		t.Fatalf("editor reads: %d", rec.Code)
	}
	if rec := viewer.raw(http.MethodGet, native, "", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer reads: %d", rec.Code)
	}
	if code, _, _ := editor.do(http.MethodDelete, native, nil); code != http.StatusForbidden {
		t.Fatalf("editor removes: %d", code)
	}

	// Qualified, it produces the documents of this version.
	fake := &fakeNative{}
	srv.SetNative(fake)
	st := srv.db.Load()
	v, _, err := st.TemplateVersion(t.Context(), id, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetNative(t.Context(), v.ID, `{"state":"qualified","sourceSha256":""}`); err != nil {
		t.Fatal(err)
	}
	_, key, _ := admin.do(http.MethodPost, "/api/keys", map[string]any{"name": "app"})
	auth := []string{"Authorization", "Bearer " + key["secret"].(string)}
	app := &client{t: t, h: h}
	render := func(data string) string {
		var b bytes.Buffer
		w := multipart.NewWriter(&b)
		_ = w.WriteField("templateName", "funeral/tarif.odt")
		_ = w.WriteField("data", data)
		_ = w.Close()
		rec := app.raw(http.MethodPost, "/v1/render", w.FormDataContentType(), b.Bytes(), auth...)
		if rec.Code != http.StatusOK {
			t.Fatalf("render %s: %d %s", data, rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}
	if out := render(`{"nom":"Dupont"}`); !strings.HasPrefix(out, "%PDF-native\n= Proposition") || !strings.Contains(out, `{"nom":"Dupont"}`) {
		t.Fatalf("native render: %q", out)
	}
	if today := fake.inputs["today"]; today != time.Now().UTC().Format("2006-01-02") {
		t.Fatalf("today = %q", today)
	}
	if out := render(`{"nom":"Δημήτρης"}`); strings.HasPrefix(out, "%PDF-native") {
		t.Fatal("Greek letters went to the copy")
	}
	fake.err = errors.New("worker crashed")
	if out := render(`{"nom":"Martin"}`); strings.HasPrefix(out, "%PDF-native") || !strings.Contains(out, "Martin") {
		t.Fatalf("fallback after a failure: %q", out)
	}
	fake.err = nil
	rows, _, err := st.Renders(t.Context(), store.RenderFilter{}, store.Page{Limit: 10})
	if err != nil || len(rows) < 3 || rows[len(rows)-1].Engine != "native" || rows[0].Engine != "standard" {
		t.Fatalf("history: %v %+v", err, rows)
	}

	// The setting stops it at once.
	if code, out, _ := admin.do(http.MethodPut, "/api/settings", map[string]any{"values": map[string]string{"native_engine": "false"}}); code != http.StatusOK {
		t.Fatalf("setting: %d %v", code, out)
	}
	if out := render(`{"nom":"Dupont"}`); strings.HasPrefix(out, "%PDF-native") {
		t.Fatal("the copy served with the engine off")
	}
	if code, out, _ := admin.do(http.MethodPut, "/api/settings", map[string]any{"values": map[string]string{"native_engine": "true"}}); code != http.StatusOK {
		t.Fatalf("setting: %d %v", code, out)
	}

	// A new version of the Word file has no copy: LibreOffice produces it.
	if code, out := admin.send(http.MethodPost, fmt.Sprintf("/api/templates/%d/versions", id), map[string]part{"file": {"tarif.odt", odtTemplate(t, `<text:p>Bonjour cher &lt;&lt;nom&gt;&gt;</text:p>`)}}, map[string]string{"publish": "true"}); code != http.StatusCreated {
		t.Fatalf("new version: %d %v", code, out)
	}
	calls := fake.calls
	if out := render(`{"nom":"Dupont"}`); strings.HasPrefix(out, "%PDF-native") || fake.calls != calls || !strings.Contains(out, "Bonjour cher Dupont") {
		t.Fatalf("new version: %q", out)
	}

	// Removed by an administrator.
	if code, _, _ := admin.do(http.MethodDelete, native, nil); code != http.StatusNoContent {
		t.Fatalf("remove: %d", code)
	}
	if rec := admin.raw(http.MethodGet, native, "", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("read after remove: %d", rec.Code)
	}

	// The import attaches X.docx.native.typ to the version served; the export
	// gives it back.
	zipped := zipOf(t, "templates/autre.odt", string(tpl), "templates/autre.odt.native.typ", "= Autre")
	code, report := admin.send(http.MethodPost, "/api/templates/import", map[string]part{"file": {"modeles.zip", zipped}}, nil)
	if code != http.StatusOK || report["natives"] != float64(1) || report["created"] != float64(1) {
		t.Fatalf("import: %d %v", code, report)
	}
	rec := admin.raw(http.MethodGet, "/api/templates/export", "", nil)
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range zr.File {
		found = found || f.Name == "templates/autre.odt.native.typ"
	}
	if !found {
		t.Fatal("the export lost the Typst copy")
	}
}
