package httpapi

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// keyTransport sends an API key with every request, as an MCP client
// configured with the key does.
type keyTransport string

func (b keyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+string(b))
	return http.DefaultTransport.RoundTrip(r)
}

func connectMCP(t *testing.T, url, key string) (*mcp.ClientSession, error) {
	t.Helper()
	transport := &mcp.StreamableClientTransport{Endpoint: url + mcpPath, HTTPClient: &http.Client{Transport: keyTransport(key)}}
	return mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(context.Background(), transport, nil)
}

func TestMcpDashboardAndPrometheus(t *testing.T) {
	srv, setup := open(t, nil)
	srv.SetRenderer(&fakeRenderer{})
	h := srv.Handler()
	admin := &client{t: t, h: h}
	const password = "a long enough password"
	admin.do(http.MethodPost, "/api/auth/setup", map[string]string{"setupCode": setup.String(), "username": "admin", "password": password})
	_, created, _ := admin.do(http.MethodPost, "/api/keys", map[string]any{"name": "assistant"})
	secret := created["secret"].(string)

	ts := httptest.NewServer(h)
	defer ts.Close()
	if _, err := connectMCP(t, ts.URL, "dib_unknown_key"); err == nil {
		t.Fatal("MCP without a valid key must be refused")
	}
	session, err := connectMCP(t, ts.URL, secret)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()
	ctx := context.Background()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	readOnly := map[string]bool{}
	for _, tool := range tools.Tools {
		readOnly[tool.Name] = tool.Annotations != nil && tool.Annotations.ReadOnlyHint
	}
	for name, want := range map[string]bool{"docibis_whoami": true, "docibis_analyze_template": true, "docibis_render_document": false, "docibis_list_documents": true, "docibis_stats": true} {
		if got, ok := readOnly[name]; !ok || got != want {
			t.Fatalf("tool %s: present %v, read-only %v", name, ok, got)
		}
	}

	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "docibis_whoami"})
	if err != nil || res.IsError || !strings.Contains(textOf(res), `"key":"assistant"`) {
		t.Fatalf("whoami: %v %v", err, textOf(res))
	}

	tpl := base64.StdEncoding.EncodeToString(odtTemplate(t, `<text:p>Bonjour &lt;&lt;name&gt;&gt;</text:p>`))
	res, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "docibis_render_document", Arguments: map[string]any{
		"template_base64": tpl, "file_name": "lettre.odt", "data": map[string]any{"name": "Ann"},
	}})
	if err != nil || res.IsError || len(res.Content) != 2 {
		t.Fatalf("render: %v %+v", err, res)
	}
	doc, ok := res.Content[1].(*mcp.EmbeddedResource)
	if !ok || doc.Resource.MIMEType != "application/pdf" || len(doc.Resource.Blob) == 0 || doc.Resource.URI != "docibis://documents/lettre.pdf" {
		t.Fatalf("document: %+v", res.Content[1])
	}
	if res, _ = session.CallTool(ctx, &mcp.CallToolParams{Name: "docibis_render_document", Arguments: map[string]any{"template_base64": "pas du base64"}}); !res.IsError {
		t.Fatal("a template that is not base64 must be refused")
	}

	if _, page, _ := admin.do(http.MethodGet, "/api/renders?source=mcp", nil); page["total"] != float64(1) ||
		page["items"].([]any)[0].(map[string]any)["actor"] != "assistant" {
		t.Fatalf("history of the MCP: %v", page)
	}
	_, dash, _ := admin.do(http.MethodGet, "/api/dashboard", nil)
	if dash["today"] != float64(1) || dash["last30d"] != float64(1) || dash["successRate"] != float64(100) || dash["activeKeys"] != float64(1) ||
		dash["users"] != float64(1) || len(dash["days"].([]any)) != 30 || dash["topActors"].([]any)[0].(map[string]any)["name"] != "assistant" {
		t.Fatalf("dashboard: %v", dash)
	}

	scrape := func(token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := scrape(""); rec.Code != http.StatusNotFound {
		t.Fatalf("/metrics without any token configured: %d", rec.Code)
	}
	_, tok, _ := admin.do(http.MethodPost, "/api/metrics/token", nil)
	token, _ := tok["token"].(string)
	if !strings.HasPrefix(token, "dim_") || tok["configured"] != true {
		t.Fatalf("token: %v", tok)
	}
	if rec := scrape("dim_wrong"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("/metrics with a wrong token: %d", rec.Code)
	}
	rec := scrape(token)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `docibis_documents_total{source="mcp",status="ok"} 1`) ||
		!strings.Contains(rec.Body.String(), "docibis_build_info{") || !strings.Contains(rec.Body.String(), "docibis_engine_ready 1") {
		t.Fatalf("/metrics: %d %s", rec.Code, rec.Body.String())
	}
	if _, state, _ := admin.do(http.MethodGet, "/api/metrics/token", nil); state["configured"] != true || state["token"] != nil {
		t.Fatalf("the token is shown only once: %v", state)
	}
	admin.do(http.MethodDelete, "/api/metrics/token", nil)
	if rec := scrape(token); rec.Code != http.StatusNotFound {
		t.Fatalf("/metrics after revocation: %d", rec.Code)
	}
}

func textOf(res *mcp.CallToolResult) string {
	if res == nil {
		return ""
	}
	var b strings.Builder
	for _, c := range res.Content {
		if text, ok := c.(*mcp.TextContent); ok {
			b.WriteString(text.Text)
		}
	}
	return b.String()
}
