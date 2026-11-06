package httpapi

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// callRender calls /v1/render as an application does: a key, a template and data.
func callRender(h http.Handler, key string, template []byte, fields map[string]string) *httptest.ResponseRecorder {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	fw, _ := w.CreateFormFile("template", "modele.odt")
	_, _ = fw.Write(template)
	for k, v := range fields {
		_ = w.WriteField(k, v)
	}
	_ = w.Close()
	req := httptest.NewRequest(http.MethodPost, "/v1/render", &b)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAPIKeysAndRender(t *testing.T) {
	srv, setup := open(t, nil)
	c := &client{t: t, h: srv.Handler()}
	if code, _, _ := c.do(http.MethodGet, "/api/keys", nil); code != http.StatusUnauthorized {
		t.Fatalf("keys without a session: %d", code)
	}
	if code, _, _ := c.do(http.MethodPost, "/api/auth/setup", map[string]string{"setupCode": setup.String(), "username": "admin", "password": "a long enough password"}); code != http.StatusOK {
		t.Fatalf("setup: %d", code)
	}

	for _, body := range []map[string]any{{"name": ""}, {"name": strings.Repeat("x", 101)}, {"name": "app", "expiresInDays": 0}, {"name": "app", "expiresInDays": 3000}} {
		if code, _, _ := c.do(http.MethodPost, "/api/keys", body); code != http.StatusBadRequest {
			t.Fatalf("%v must be refused: %d", body, code)
		}
	}
	code, body, _ := c.do(http.MethodPost, "/api/keys", map[string]any{"name": "billing-app INT", "expiresInDays": 30})
	secret, _ := body["secret"].(string)
	key, _ := body["key"].(map[string]any)
	if code != http.StatusCreated || !strings.HasPrefix(secret, key["prefix"].(string)+"_") || len(secret) != len("dib_")+8+1+32 || key["status"] != "active" || key["createdBy"] != "admin" {
		t.Fatalf("create: %d %v", code, body)
	}
	expires, _ := time.Parse(time.RFC3339, key["expiresAt"].(string))
	if d := time.Until(expires); d < 29*24*time.Hour || d > 31*24*time.Hour {
		t.Fatalf("expires in %s", d)
	}

	// An application: no session, only its key.
	app := srv.Handler()
	tpl := odtTemplate(t, `<text:p>Bonjour &lt;&lt;name&gt;&gt;</text:p>`)
	for _, k := range []string{"", "dib_wrong_key", secret[:len(secret)-1]} {
		rec := callRender(app, k, tpl, map[string]string{"data": `{"name": "Ann"}`})
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Header().Get("WWW-Authenticate"), "Bearer") || rec.Header().Get("Content-Type") != "application/problem+json" {
			t.Fatalf("key %q: %d %v", k, rec.Code, rec.Header())
		}
	}
	if rec := callRender(app, secret, tpl, map[string]string{"data": `{"name": "Ann"}`}); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("before the workers start: %d", rec.Code)
	}
	srv.SetRenderer(&fakeRenderer{})
	rec := callRender(app, secret, tpl, map[string]string{"data": `{"name": "Ann"}`})
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/pdf" || rec.Header().Get("X-Render-Ms") == "" ||
		rec.Header().Get("Content-Disposition") != `attachment; filename=modele.pdf` || !strings.Contains(rec.Body.String(), "Bonjour Ann") {
		t.Fatalf("render: %d %v %q", rec.Code, rec.Header(), rec.Body.String())
	}
	if rec := callRender(app, secret, tpl, map[string]string{"data": `{"name": "Ann"}`, "format": "docx"}); rec.Code != http.StatusOK ||
		rec.Header().Get("Content-Type") != "application/vnd.openxmlformats-officedocument.wordprocessingml.document" {
		t.Fatalf("docx: %d %v", rec.Code, rec.Header())
	}
	for format, want := range map[string]string{"rtf": "application/rtf", "HTM": "text/html; charset=utf-8", "txt": "text/plain; charset=utf-8", "doc": "application/msword"} {
		rec := callRender(app, secret, tpl, map[string]string{"data": `{"name": "Ann"}`, "format": format})
		ext := strings.ToLower(strings.Replace(format, "HTM", "html", 1))
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != want || rec.Header().Get("Content-Disposition") != "attachment; filename=modele."+ext ||
			(ext == "html") != (rec.Header().Get("Content-Security-Policy") == "sandbox") {
			t.Fatalf("%s: %d %v", format, rec.Code, rec.Header())
		}
	}
	for _, fields := range []map[string]string{{"data": `{"name": "Ann"}`, "format": "xls"}, {"data": "not json"}, {}} {
		if rec := callRender(app, secret, tpl, fields); rec.Code != http.StatusBadRequest || rec.Header().Get("Content-Type") != "application/problem+json" {
			t.Fatalf("%v: %d", fields, rec.Code)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/keys", nil)
	req.Header.Set("Cookie", c.cookie)
	listing := httptest.NewRecorder()
	app.ServeHTTP(listing, req)
	if listing.Code != http.StatusOK || !strings.Contains(listing.Body.String(), `"lastUsedAt"`) || strings.Contains(listing.Body.String(), secret) {
		t.Fatalf("list: %d %s", listing.Code, listing.Body.String())
	}

	id := int64(key["id"].(float64))
	if code, _, _ := c.do(http.MethodDelete, "/api/keys/"+strconv.FormatInt(id, 10), nil); code != http.StatusNoContent {
		t.Fatalf("revoke: %d", code)
	}
	if code, _, _ := c.do(http.MethodDelete, "/api/keys/999", nil); code != http.StatusNotFound {
		t.Fatalf("revoke a missing key: %d", code)
	}
	if rec := callRender(app, secret, tpl, map[string]string{"data": `{}`}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked key: %d", rec.Code)
	}

	contract := httptest.NewRecorder()
	app.ServeHTTP(contract, httptest.NewRequest(http.MethodGet, "/v1/openapi.json", nil))
	if contract.Code != http.StatusOK || !strings.Contains(contract.Body.String(), `"/v1/render"`) {
		t.Fatalf("contract: %d", contract.Code)
	}
}
