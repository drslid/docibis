package httpapi

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// A renamed or merged template still answers to its former names: the
// applications keep calling them until they move to the new ones.
func TestRenameAndMergeKeepFormerNames(t *testing.T) {
	srv, setup := open(t, nil)
	h := srv.Handler()
	c := &client{t: t, h: h}
	if code, _, _ := c.do(http.MethodPost, "/api/auth/setup", map[string]string{"setupCode": setup.String(), "username": "admin", "password": "a long enough password"}); code != http.StatusOK {
		t.Fatalf("setup: %d", code)
	}
	srv.SetRenderer(&fakeRenderer{})
	hello := odtTemplate(t, `<text:p>Bonjour &lt;&lt;nom&gt;&gt;</text:p>`)
	bye := odtTemplate(t, `<text:p>Au revoir &lt;&lt;nom&gt;&gt;</text:p>`)
	create := func(path string, file []byte) int64 {
		t.Helper()
		code, body := c.send(http.MethodPost, "/api/templates", map[string]part{"file": {"x.odt", file}}, map[string]string{"path": path})
		if code != http.StatusCreated {
			t.Fatalf("create %s: %d %v", path, code, body)
		}
		return int64(body["template"].(map[string]any)["id"].(float64))
	}
	const direct, broker = "acme/contracts/default/monthly/Fiche_conseil.odt", "acme/contracts/broker/monthly/Fiche_conseil.odt"
	directID, brokerID := create(direct, hello), create(broker, hello)

	_, body, _ := c.do(http.MethodPost, "/api/keys", map[string]any{"name": "app"})
	auth := []string{"Authorization", "Bearer " + body["secret"].(string)}
	render := func(name string) (int, string) {
		t.Helper()
		var b bytes.Buffer
		w := multipart.NewWriter(&b)
		_ = w.WriteField("templateName", name)
		_ = w.WriteField("data", `{"nom":"Dupont"}`)
		_ = w.Close()
		rec := c.raw(http.MethodPost, "/v1/render", w.FormDataContentType(), b.Bytes(), auth...)
		return rec.Code, rec.Body.String()
	}
	aliases := func(body map[string]any) string {
		raw, _ := json.Marshal(body["aliases"])
		return string(raw)
	}

	rename := func(id int64, path string) (int, map[string]any) {
		t.Helper()
		code, body, _ := c.do(http.MethodPost, fmt.Sprintf("/api/templates/%d/rename", id), map[string]any{"path": path})
		return code, body
	}
	if code, _ := rename(directID, "habitation/direct/mensuel/fiche-conseil.docx"); code != http.StatusBadRequest {
		t.Fatalf("other extension: %d", code)
	}
	if code, _ := rename(directID, broker); code != http.StatusConflict {
		t.Fatalf("name of another template: %d", code)
	}
	code, body := rename(directID, "habitation/direct/mensuel/fiche-conseil.odt")
	if code != http.StatusOK || body["template"].(map[string]any)["path"] != "habitation/direct/mensuel/fiche-conseil.odt" || aliases(body) != `["`+direct+`"]` {
		t.Fatalf("rename: %d %v", code, body)
	}
	for _, name := range []string{"habitation/direct/mensuel/fiche-conseil", direct, strings.TrimSuffix(direct, ".odt")} {
		if code, out := render(name); code != http.StatusOK || !strings.Contains(out, "Bonjour Dupont") {
			t.Fatalf("render %s: %d %s", name, code, out)
		}
	}

	// The same file under another name: one template, two former names.
	merge := func(into, from int64) (int, map[string]any) {
		t.Helper()
		code, body, _ := c.do(http.MethodPost, fmt.Sprintf("/api/templates/%d/merge", into), map[string]any{"from": from})
		return code, body
	}
	if code, _ := merge(directID, directID); code != http.StatusBadRequest {
		t.Fatalf("merge into itself: %d", code)
	}
	code, body = merge(directID, brokerID)
	if code != http.StatusOK || len(body["versions"].([]any)) != 1 || aliases(body) != `["`+broker+`","`+direct+`"]` {
		t.Fatalf("merge: %d %v", code, body)
	}
	if code, out := render(broker); code != http.StatusOK || !strings.Contains(out, "Bonjour Dupont") {
		t.Fatalf("render merged name: %d %s", code, out)
	}
	if code, _, _ := c.do(http.MethodGet, fmt.Sprintf("/api/templates/%d", brokerID), nil); code != http.StatusNotFound {
		t.Fatalf("merged template still there: %d", code)
	}

	// A former name can not be taken by a new template; importing it adds a
	// version to the template it names.
	if code, body := c.send(http.MethodPost, "/api/templates", map[string]part{"file": {"x.odt", bye}}, map[string]string{"path": broker}); code != http.StatusBadRequest ||
		!strings.Contains(fmt.Sprint(body["detail"]), "ancien nom") {
		t.Fatalf("create with a former name: %d %v", code, body)
	}
	var zipped bytes.Buffer
	zw := zip.NewWriter(&zipped)
	fw, _ := zw.Create("templates/" + broker)
	_, _ = fw.Write(bye)
	_ = zw.Close()
	if code, report := c.send(http.MethodPost, "/api/templates/import", map[string]part{"file": {"docmosis.zip", zipped.Bytes()}}, nil); code != http.StatusOK ||
		report["updated"] != float64(1) || report["created"] != float64(0) {
		t.Fatalf("import of a former name: %d %v", code, report)
	}
	if code, out := render(direct); code != http.StatusOK || !strings.Contains(out, "Au revoir Dupont") {
		t.Fatalf("new version through a former name: %d %s", code, out)
	}

	if code, list := c.list("/api/templates?q=broker/monthly"); code != http.StatusOK || len(list) != 1 {
		t.Fatalf("search by a former name: %d %v", code, list)
	}

	remove := fmt.Sprintf("/api/templates/%d/aliases?name=%s", directID, url.QueryEscape(broker))
	if code, _, _ := c.do(http.MethodDelete, remove, nil); code != http.StatusNoContent {
		t.Fatalf("remove former name: %d", code)
	}
	if code, _, _ := c.do(http.MethodDelete, remove, nil); code != http.StatusNotFound {
		t.Fatalf("remove twice: %d", code)
	}
	if code, _ := render(broker); code != http.StatusNotFound {
		t.Fatalf("render a removed name: %d", code)
	}

	// The former names go with the export to another environment.
	export := c.raw(http.MethodGet, "/api/templates/export", "", nil)
	zr, err := zip.NewReader(bytes.NewReader(export.Body.Bytes()), int64(export.Body.Len()))
	if export.Code != http.StatusOK || err != nil {
		t.Fatalf("export: %d %v", export.Code, err)
	}
	listed := ""
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, ".aliases.txt") {
			r, _ := f.Open()
			raw := new(bytes.Buffer)
			_, _ = raw.ReadFrom(r)
			listed = f.Name + ":" + raw.String()
		}
	}
	if listed != "templates/habitation/direct/mensuel/fiche-conseil.odt.aliases.txt:"+direct+"\n" {
		t.Fatalf("former names in the export: %q", listed)
	}
	other, otherSetup := open(t, nil)
	c2 := &client{t: t, h: other.Handler()}
	if code, _, _ := c2.do(http.MethodPost, "/api/auth/setup", map[string]string{"setupCode": otherSetup.String(), "username": "admin", "password": "a long enough password"}); code != http.StatusOK {
		t.Fatalf("setup of the other environment: %d", code)
	}
	other.SetRenderer(&fakeRenderer{})
	for i, want := range []float64{1, 0} {
		code, report := c2.send(http.MethodPost, "/api/templates/import", map[string]part{"file": {"export.zip", export.Body.Bytes()}}, nil)
		if code != http.StatusOK || valueOrZero(report["aliases"]) != want || len(report["errors"].([]any)) != 0 {
			t.Fatalf("import %d in the other environment: %d %v", i+1, code, report)
		}
	}
	if code, list := c2.list("/api/templates?q=" + url.QueryEscape(direct)); code != http.StatusOK || len(list) != 1 || list[0]["path"] != "habitation/direct/mensuel/fiche-conseil.odt" {
		t.Fatalf("former name in the other environment: %d %v", code, list)
	}
}

func valueOrZero(v any) float64 {
	f, _ := v.(float64)
	return f
}
