package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"docibis/internal/settings"
	"docibis/internal/store"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type client struct {
	t      *testing.T
	h      http.Handler
	cookie string
}

func (c *client) do(method, path string, body any, headers ...string) (int, map[string]any, http.Header) {
	c.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, r)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.cookie != "" {
		req.Header.Set("Cookie", c.cookie)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			c.t.Fatalf("%s %s: invalid JSON %q", method, path, rec.Body.String())
		}
	}
	if sc := rec.Header().Get("Set-Cookie"); sc != "" {
		c.cookie = strings.SplitN(sc, ";", 2)[0]
	}
	return rec.Code, out, rec.Header()
}

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// open starts a server on a fresh SQLite database, without any account.
func open(t *testing.T, vars map[string]string) (*Server, *SetupCode) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, store.Options{Path: filepath.Join(t.TempDir(), "docibis.db")}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close(ctx) })
	srv := NewServer(nil, quiet)
	code := NewSetupCode()
	// Sign-ins without a second factor, unless the test asks for one.
	all := map[string]string{"MFA_REQUIRED_FOR": "none"}
	for k, v := range vars {
		all[k] = v
	}
	srv.SetStore(st, settings.New(nil, env(all)), code)
	return srv, code
}

func TestStarting(t *testing.T) {
	srv := NewServer(nil, quiet)
	c := &client{t: t, h: srv.Handler()}
	if code, _, _ := c.do(http.MethodGet, "/healthz", nil); code != http.StatusOK {
		t.Fatalf("healthz while starting: %d", code)
	}
	for _, path := range []string{"/readyz", "/api/info", "/api/auth/session"} {
		code, body, h := c.do(http.MethodGet, path, nil)
		if code != http.StatusServiceUnavailable || h.Get("Content-Type") != "application/problem+json" || body["status"] != float64(503) {
			t.Fatalf("%s while starting: %d %v", path, code, body)
		}
	}
}

func TestSetupLoginAndSettings(t *testing.T) {
	srv, setup := open(t, map[string]string{"INSTANCE_NAME": "INT"})
	c := &client{t: t, h: srv.Handler()}

	if code, _, _ := c.do(http.MethodGet, "/readyz", nil); code != http.StatusOK {
		t.Fatalf("readyz: %d", code)
	}
	if code, _, _ := c.do(http.MethodGet, "/api/info", nil); code != http.StatusUnauthorized {
		t.Fatalf("info without a session: %d", code)
	}
	if _, body, _ := c.do(http.MethodGet, "/api/auth/session", nil); body["setupRequired"] != true || body["instance"] != "INT" {
		t.Fatalf("session before setup: %v", body)
	}

	if code, _, _ := c.do(http.MethodPost, "/api/auth/setup", map[string]string{"setupCode": "WRONG-CODE", "username": "admin", "password": "a long enough password"}); code != http.StatusBadRequest {
		t.Fatalf("wrong setup code: %d", code)
	}
	if code, _, _ := c.do(http.MethodPost, "/api/auth/setup", map[string]string{"setupCode": setup.String(), "username": "admin", "password": "short"}); code != http.StatusBadRequest {
		t.Fatalf("short password: %d", code)
	}
	code, body, h := c.do(http.MethodPost, "/api/auth/setup", map[string]string{"setupCode": strings.ToLower(setup.String()), "username": "admin", "password": "a long enough password"})
	if code != http.StatusOK || body["authenticated"] != true || !strings.Contains(h.Get("Set-Cookie"), "HttpOnly") || !strings.Contains(h.Get("Set-Cookie"), "SameSite=Strict") {
		t.Fatalf("setup: %d %v %q", code, body, h.Get("Set-Cookie"))
	}
	if code, _, _ := c.do(http.MethodPost, "/api/auth/setup", map[string]string{"setupCode": setup.String(), "username": "other", "password": "a long enough password"}); code != http.StatusConflict {
		t.Fatalf("second setup: %d", code)
	}

	if code, body, _ := c.do(http.MethodGet, "/api/info", nil); code != http.StatusOK || body["instance"] != "INT" {
		t.Fatalf("info with a session: %d %v", code, body)
	}

	// Settings: saved, checked, and locked by the environment.
	code, body, _ = c.do(http.MethodPut, "/api/settings", map[string]any{"values": map[string]string{"render_workers": "3", "image_url_allowlist": " cdn.example.com \n\n img.example.com "}})
	if code != http.StatusOK {
		t.Fatalf("save settings: %d %v", code, body)
	}
	got := map[string]map[string]any{}
	for _, g := range body["groups"].([]any) {
		for _, s := range g.(map[string]any)["settings"].([]any) {
			got[s.(map[string]any)["key"].(string)] = s.(map[string]any)
		}
	}
	if got["render_workers"]["value"] != "3" || got["render_workers"]["source"] != "console" {
		t.Fatalf("render_workers: %v", got["render_workers"])
	}
	if got["image_url_allowlist"]["value"] != "cdn.example.com\nimg.example.com" {
		t.Fatalf("list cleaned: %q", got["image_url_allowlist"]["value"])
	}
	if got["instance_name"]["locked"] != true || got["session_hours"]["value"] != "12" {
		t.Fatalf("locked or default: %v %v", got["instance_name"], got["session_hours"])
	}
	for _, values := range []map[string]string{{"instance_name": "X"}, {"render_workers": "99"}, {"nope": "1"}, {"log_level": "loud"}} {
		if code, _, _ := c.do(http.MethodPut, "/api/settings", map[string]any{"values": values}); code != http.StatusBadRequest {
			t.Fatalf("%v must be refused: %d", values, code)
		}
	}
	if code, _, _ := c.do(http.MethodPut, "/api/settings", map[string]any{"values": map[string]string{}}, "Sec-Fetch-Site", "cross-site"); code != http.StatusForbidden {
		t.Fatalf("cross-site write: %d", code)
	}

	// Sign out, then sign in again.
	if code, _, _ := c.do(http.MethodPost, "/api/auth/logout", nil); code != http.StatusNoContent {
		t.Fatalf("logout: %d", code)
	}
	if code, _, _ := c.do(http.MethodGet, "/api/settings", nil); code != http.StatusUnauthorized {
		t.Fatalf("settings after logout: %d", code)
	}
	for i := 0; i < 5; i++ {
		if code, _, _ := c.do(http.MethodPost, "/api/auth/login", map[string]string{"username": "admin", "password": "wrong password"}); code != http.StatusUnauthorized {
			t.Fatalf("wrong password %d: %d", i, code)
		}
	}
	if code, _, h := c.do(http.MethodPost, "/api/auth/login", map[string]string{"username": "admin", "password": "a long enough password"}); code != http.StatusTooManyRequests || h.Get("Retry-After") == "" {
		t.Fatalf("after 5 failures: %d", code)
	}
	srv.logins.reset("login:admin")
	if code, body, _ := c.do(http.MethodPost, "/api/auth/login", map[string]string{"username": "Admin", "password": "a long enough password"}); code != http.StatusUnauthorized {
		t.Fatalf("usernames are exact: %d %v", code, body)
	}
	if code, _, _ := c.do(http.MethodPost, "/api/auth/login", map[string]string{"username": "admin", "password": "a long enough password"}); code != http.StatusOK {
		t.Fatalf("login: %d", code)
	}
	if code, _, _ := c.do(http.MethodPost, "/api/auth/password", map[string]string{"currentPassword": "a long enough password", "newPassword": "another long password"}); code != http.StatusNoContent {
		t.Fatalf("change password: %d", code)
	}
}

func TestErrorsAreProblems(t *testing.T) {
	srv, _ := open(t, nil)
	c := &client{t: t, h: srv.Handler()}
	for _, tc := range []struct {
		method, path string
		code         int
	}{
		{http.MethodGet, "/nothing-here", http.StatusNotFound},
		{http.MethodPost, "/healthz", http.StatusMethodNotAllowed},
	} {
		code, body, h := c.do(tc.method, tc.path, nil)
		if code != tc.code || h.Get("Content-Type") != "application/problem+json" || body["title"] != http.StatusText(tc.code) {
			t.Fatalf("%s %s: %d %v", tc.method, tc.path, code, body)
		}
	}
}
