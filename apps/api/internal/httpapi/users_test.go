package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// list reads a JSON array answered to an account.
func (c *client) list(path string) (int, []map[string]any) {
	c.t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Cookie", c.cookie)
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	var out []map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestUsersRolesHistoryAndAudit(t *testing.T) {
	srv, setup := open(t, nil)
	h := srv.Handler()
	admin := &client{t: t, h: h}
	const password = "a long enough password"
	if code, _, _ := admin.do(http.MethodPost, "/api/auth/setup", map[string]string{"setupCode": setup.String(), "username": "admin", "password": password}); code != http.StatusOK {
		t.Fatalf("setup: %d", code)
	}

	for _, body := range []map[string]any{
		{"username": "eve", "firstName": "Eve", "lastName": "Martin", "email": "eve@example.fr", "role": "editor", "password": password},
		{"username": "victor", "firstName": "Victor", "lastName": "Hugo", "role": "viewer", "password": password},
	} {
		if code, out, _ := admin.do(http.MethodPost, "/api/users", body); code != http.StatusCreated || out["active"] != true || out["role"] != body["role"] {
			t.Fatalf("create %v: %d %v", body["username"], code, out)
		}
	}
	for _, refused := range []struct {
		body map[string]any
		why  string
	}{
		{map[string]any{"username": "eve", "firstName": "E", "lastName": "M", "role": "editor", "password": password}, "déjà pris"},
		{map[string]any{"username": "x y", "firstName": "X", "lastName": "Y", "role": "editor", "password": password}, "Identifiant"},
		{map[string]any{"username": "zoe", "firstName": "Zoé", "lastName": "Z", "role": "boss", "password": password}, "Rôle"},
		{map[string]any{"username": "zoe", "firstName": "Zoé", "lastName": "Z", "role": "viewer", "password": "short"}, "12"},
		{map[string]any{"username": "zoe", "firstName": "Zoé", "lastName": "Z", "role": "viewer", "password": password, "email": "not an address"}, "e-mail"},
		{map[string]any{"username": "zoe", "role": "viewer", "password": password}, "Prénom et nom"},
	} {
		code, out, _ := admin.do(http.MethodPost, "/api/users", refused.body)
		if detail, _ := out["detail"].(string); code != http.StatusBadRequest || !strings.Contains(detail, refused.why) {
			t.Fatalf("%v must be refused for %q: %d %v", refused.body, refused.why, code, out)
		}
	}
	if code, users := admin.list("/api/users?q=MARTIN"); code != http.StatusOK || len(users) != 1 || users[0]["username"] != "eve" {
		t.Fatalf("search: %d %v", code, users)
	}

	eve, victor := &client{t: t, h: h}, &client{t: t, h: h}
	for name, c := range map[string]*client{"eve": eve, "victor": victor} {
		if code, out, _ := c.do(http.MethodPost, "/api/auth/login", map[string]string{"username": name, "password": password}); code != http.StatusOK {
			t.Fatalf("login %s: %d %v", name, code, out)
		}
	}
	for _, path := range []string{"/api/users", "/api/keys", "/api/settings", "/api/audit", "/api/monitoring"} {
		if code, _, _ := eve.do(http.MethodGet, path, nil); code != http.StatusForbidden {
			t.Fatalf("editor on %s: %d", path, code)
		}
	}
	tpl := odtTemplate(t, `<text:p>Bonjour &lt;&lt;name&gt;&gt;</text:p>`)
	if code, _, _ := victor.upload("/api/try/render", "modele.odt", tpl, map[string]string{"data": `{}`}); code != http.StatusForbidden {
		t.Fatalf("viewer may not produce: %d", code)
	}

	srv.SetRenderer(&fakeRenderer{})
	if code, _, _ := eve.upload("/api/try/render", "modele.odt", tpl, map[string]string{"data": `{"name": "Ann"}`}); code != http.StatusOK {
		t.Fatalf("editor produces: %d", code)
	}
	if code, _, _ := eve.upload("/api/try/render", "tarif.odt", tpl, map[string]string{"data": `[]`}); code != http.StatusBadRequest {
		t.Fatalf("bad data: %d", code)
	}
	code, body, _ := admin.do(http.MethodPost, "/api/keys", map[string]any{"name": "billing-app"})
	if code != http.StatusCreated {
		t.Fatalf("key: %d", code)
	}
	if rec := callRender(h, body["secret"].(string), tpl, map[string]string{"data": `{"name": "Bob"}`}); rec.Code != http.StatusOK {
		t.Fatalf("API render: %d", rec.Code)
	}
	code, history, _ := victor.do(http.MethodGet, "/api/renders?q=modele", nil)
	if code != http.StatusOK || history["total"] != float64(2) {
		t.Fatalf("history for a viewer: %d %v", code, history)
	}
	items := history["items"].([]any)
	if first := items[0].(map[string]any); first["source"] != "api" || first["actor"] != "billing-app" || first["status"] != "ok" {
		t.Fatalf("newest first: %v", first)
	}
	if _, failed, _ := victor.do(http.MethodGet, "/api/renders?status=refused", nil); failed["total"] != float64(1) ||
		!strings.Contains(failed["items"].([]any)[0].(map[string]any)["error"].(string), "JSON") {
		t.Fatalf("failed renders: %v", failed)
	}

	// Refused data is the caller's to fix: it does not degrade the health.
	code, mon, _ := admin.do(http.MethodGet, "/api/monitoring", nil)
	renders, _ := mon["renders"].(map[string]any)
	traffic, _ := mon["traffic"].(map[string]any)
	if code != http.StatusOK || renders["lastHour"] != float64(2) || renders["refusedLastHour"] != float64(1) || renders["errorsLastHour"] != float64(0) ||
		renders["last24h"] != float64(2) || len(traffic["series"].([]any)) != 60 || mon["health"] != "ok" {
		t.Fatalf("monitoring: %d %v", code, mon)
	}

	// Profile of the signed-in account.
	if code, me, _ := victor.do(http.MethodPatch, "/api/me", map[string]any{"firstName": "Victor", "lastName": "Hugo-Marie", "language": "de"}); code != http.StatusOK || me["language"] != "de" || me["displayName"] != "Victor Hugo-Marie" {
		t.Fatalf("profile: %d %v", code, me)
	}
	if code, _, _ := victor.do(http.MethodPatch, "/api/me", map[string]any{"language": "es"}); code != http.StatusBadRequest {
		t.Fatalf("unknown language: %d", code)
	}

	// Disabling an account closes its sessions; the last administrator stays.
	_, users := admin.list("/api/users?q=eve")
	eveID := strconv.FormatInt(int64(users[0]["id"].(float64)), 10)
	if code, out, _ := admin.do(http.MethodPatch, "/api/users/"+eveID, map[string]any{"active": false}); code != http.StatusOK || out["active"] != false {
		t.Fatalf("disable: %d %v", code, out)
	}
	if code, _, _ := eve.do(http.MethodGet, "/api/renders", nil); code != http.StatusUnauthorized {
		t.Fatalf("disabled account still signed in: %d", code)
	}
	if code, _, _ := eve.do(http.MethodPost, "/api/auth/login", map[string]string{"username": "eve", "password": password}); code != http.StatusUnauthorized {
		t.Fatalf("disabled account signs in: %d", code)
	}
	_, me, _ := admin.do(http.MethodGet, "/api/auth/session", nil)
	adminID := strconv.FormatInt(int64(me["user"].(map[string]any)["id"].(float64)), 10)
	if code, _, _ := admin.do(http.MethodPatch, "/api/users/"+adminID, map[string]any{"role": "viewer"}); code != http.StatusBadRequest {
		t.Fatalf("last administrator demoted: %d", code)
	}
	if code, _, _ := admin.do(http.MethodDelete, "/api/users/"+adminID, nil); code != http.StatusBadRequest {
		t.Fatalf("own account deleted: %d", code)
	}
	if code, _, _ := admin.do(http.MethodDelete, "/api/users/"+eveID, nil); code != http.StatusNoContent {
		t.Fatalf("delete: %d", code)
	}

	code, journal, _ := admin.do(http.MethodGet, "/api/audit?q=eve", nil)
	actions := map[string]bool{}
	for _, e := range journal["items"].([]any) {
		actions[e.(map[string]any)["action"].(string)] = true
	}
	for _, want := range []string{"user.create", "login", "login.failed", "user.update", "user.delete"} {
		if !actions[want] {
			t.Fatalf("journal (%d) misses %s: %v", code, want, journal)
		}
	}
	if _, keys, _ := admin.do(http.MethodGet, "/api/audit?action=apikey.create", nil); keys["total"] != float64(1) {
		t.Fatalf("filter by action: %v", keys)
	}
}
