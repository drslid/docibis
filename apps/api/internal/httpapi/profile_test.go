package httpapi

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

func totpCode(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	code, err := totp.GenerateCodeCustom(secret, at, totp.ValidateOpts{Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func TestMfaSignIn(t *testing.T) {
	// The whole flow with the secrets sealed.
	t.Setenv("APP_SECRET_KEY", strings.Repeat("s", 32))
	srv, setup := open(t, map[string]string{"MFA_REQUIRED_FOR": "admin"})
	c := &client{t: t, h: srv.Handler()}
	const password = "a long enough password"
	c.do(http.MethodPost, "/api/auth/setup", map[string]string{"setupCode": setup.String(), "username": "admin", "password": password})
	c.do(http.MethodPost, "/api/auth/logout", nil)

	// First sign-in of an administrator: enrol an authenticator.
	code, step, _ := c.do(http.MethodPost, "/api/auth/login", map[string]string{"username": "admin", "password": password})
	if code != http.StatusAccepted || step["step"] != "enroll" || step["secret"] == nil || !strings.HasPrefix(step["qrPng"].(string), "data:image/png;base64,") {
		t.Fatalf("enrolment step: %d %v", code, step)
	}
	secret, ticket := step["secret"].(string), step["ticket"].(string)
	if code, _, _ := c.do(http.MethodPost, "/api/auth/mfa", map[string]string{"ticket": ticket, "code": "000000"}); code != http.StatusUnauthorized {
		t.Fatalf("wrong code: %d", code)
	}
	if code, _, _ := c.do(http.MethodGet, "/api/me/sessions", nil); code != http.StatusUnauthorized {
		t.Fatalf("no session before the code: %d", code)
	}
	now := time.Now()
	if code, body, _ := c.do(http.MethodPost, "/api/auth/mfa", map[string]string{"ticket": ticket, "code": totpCode(t, secret, now)}); code != http.StatusOK || body["user"].(map[string]any)["mfaEnabled"] != true {
		t.Fatalf("enrolment code: %d %v", code, body)
	}
	if code, _, _ := c.do(http.MethodPost, "/api/auth/mfa", map[string]string{"ticket": ticket, "code": totpCode(t, secret, now)}); code != http.StatusGone {
		t.Fatalf("ticket used twice: %d", code)
	}
	if code, _, _ := c.do(http.MethodDelete, "/api/me/mfa", map[string]string{"code": totpCode(t, secret, now)}); code != http.StatusBadRequest {
		t.Fatalf("an administrator keeps the second factor: %d", code)
	}
	c.do(http.MethodPost, "/api/auth/logout", nil)

	// Next sign-ins ask for the code; the code already used is refused.
	code, step, _ = c.do(http.MethodPost, "/api/auth/login", map[string]string{"username": "admin", "password": password})
	if code != http.StatusAccepted || step["step"] != "code" || step["secret"] != nil {
		t.Fatalf("code step: %d %v", code, step)
	}
	ticket = step["ticket"].(string)
	if code, _, _ := c.do(http.MethodPost, "/api/auth/mfa", map[string]string{"ticket": ticket, "code": totpCode(t, secret, now)}); code != http.StatusUnauthorized {
		t.Fatalf("code replayed: %d", code)
	}
	if code, _, _ := c.do(http.MethodPost, "/api/auth/mfa", map[string]string{"ticket": ticket, "code": totpCode(t, secret, now.Add(30*time.Second))}); code != http.StatusOK {
		t.Fatalf("next code: %d", code)
	}

	// An administrator resets the authenticator of an account.
	_, user, _ := c.do(http.MethodPost, "/api/users", map[string]any{"username": "paul@example.com", "firstName": "Paul", "lastName": "Martin", "email": "paul@example.com", "password": password, "role": "viewer"})
	id := int64(user["id"].(float64))
	if err := srv.db.Load().SetTOTP(context.Background(), id, "JBSWY3DPEHPK3PXP", true); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := c.do(http.MethodDelete, fmt.Sprintf("/api/users/%d/mfa", id), nil); code != http.StatusNoContent {
		t.Fatalf("reset: %d", code)
	}
	paul := &client{t: t, h: srv.Handler()}
	if code, _, _ := paul.do(http.MethodPost, "/api/auth/login", map[string]string{"username": "PAUL@example.com", "password": password}); code != http.StatusOK {
		t.Fatalf("viewer signs in by e-mail without a second factor: %d", code)
	}
	_, sessions, _ := paul.do(http.MethodGet, "/api/auth/session", nil)
	if sessions["user"].(map[string]any)["mfaEnabled"] != false {
		t.Fatalf("after reset: %v", sessions)
	}
}

func TestProfileTokensMcpJournalAndRegenerate(t *testing.T) {
	srv, setup := open(t, nil)
	if err := srv.cfg.Load().Update(context.Background(), srv.db.Load(), map[string]string{"keep_render_data": "true"}); err != nil {
		t.Fatal(err)
	}
	srv.SetRenderer(&fakeRenderer{})
	h := srv.Handler()
	c := &client{t: t, h: h}
	c.do(http.MethodPost, "/api/auth/setup", map[string]string{"setupCode": setup.String(), "username": "admin", "password": "a long enough password"})

	req := httptest.NewRequest(http.MethodGet, "/api/me/sessions", nil)
	req.Header.Set("Cookie", c.cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"current":true`) {
		t.Fatalf("sessions: %d %s", rec.Code, rec.Body.String())
	}
	if code, body, _ := c.do(http.MethodPut, "/api/me/avatar", map[string]string{"icon": "rocket", "color": "teal"}); code != http.StatusOK || body["avatarIcon"] != "rocket" {
		t.Fatalf("avatar: %d %v", code, body)
	}
	if code, _, _ := c.do(http.MethodPut, "/api/me/avatar", map[string]string{"icon": "<script>", "color": "teal"}); code != http.StatusBadRequest {
		t.Fatalf("bad avatar: %d", code)
	}

	code, created, _ := c.do(http.MethodPost, "/api/me/tokens", map[string]any{"name": "Claude Desktop"})
	if code != http.StatusCreated || !strings.HasPrefix(created["secret"].(string), "dit_") {
		t.Fatalf("token: %d %v", code, created)
	}
	secret := created["secret"].(string)

	ts := httptest.NewServer(h)
	defer ts.Close()
	session, err := connectMCP(t, ts.URL, secret)
	if err != nil {
		t.Fatalf("connect with a personal token: %v", err)
	}
	defer session.Close()
	ctx := context.Background()
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "docibis_whoami"})
	if err != nil || res.IsError || !strings.Contains(textOf(res), `"key":"admin"`) {
		t.Fatalf("whoami: %v %v", err, textOf(res))
	}
	tpl := base64.StdEncoding.EncodeToString(odtTemplate(t, `<text:p>Bonjour &lt;&lt;name&gt;&gt;</text:p>`))
	res, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "docibis_render_document", Arguments: map[string]any{
		"template_base64": tpl, "file_name": "lettre.odt", "data": map[string]any{"name": "Ann"}, "pdfa": "PDF/A-2b",
	}})
	if err != nil || res.IsError {
		t.Fatalf("render: %v %+v", err, res)
	}
	if res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "docibis_render_document", Arguments: map[string]any{"template_name": "nope"}}); err != nil || !res.IsError {
		t.Fatalf("refused render: %v %+v", err, res)
	}

	_, calls, _ := c.do(http.MethodGet, "/api/mcp/calls", nil)
	items := calls["items"].([]any)
	tools := map[string]string{}
	for _, it := range items {
		m := it.(map[string]any)
		if tool, ok := m["tool"].(string); ok {
			tools[tool+":"+m["status"].(string)] = m["via"].(string)
		}
		if m["actor"] != "admin" {
			t.Fatalf("actor: %v", m)
		}
	}
	if tools["docibis_render_document:ok"] != "token" || tools["docibis_render_document:error"] != "token" || tools["docibis_whoami:ok"] != "token" {
		t.Fatalf("MCP journal: %v", items)
	}

	_, renders, _ := c.do(http.MethodGet, "/api/renders?status=ok", nil)
	first := renders["items"].([]any)[0].(map[string]any)
	if first["hasData"] != true || first["source"] != "mcp" || !strings.Contains(fmt.Sprint(first["options"]), "PDF/A-2b") {
		t.Fatalf("render kept: %v", first)
	}
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/renders/%d/regenerate", int64(first["id"].(float64))), nil)
	req.Header.Set("Cookie", c.cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/pdf" || rec.Body.Len() == 0 {
		t.Fatalf("regenerate: %d %s", rec.Code, rec.Body.String())
	}

	// Revoked, the token no longer opens the MCP server.
	tokenID := int64(created["token"].(map[string]any)["id"].(float64))
	if code, _, _ := c.do(http.MethodDelete, fmt.Sprintf("/api/me/tokens/%d", tokenID), nil); code != http.StatusNoContent {
		t.Fatalf("revoke: %d", code)
	}
	if _, err := connectMCP(t, ts.URL, secret); err == nil {
		t.Fatal("revoked token accepted")
	}
}
