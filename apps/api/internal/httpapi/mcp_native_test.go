package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMcpNativeTools(t *testing.T) {
	srv, setup := open(t, nil)
	srv.SetRenderer(&fakeRenderer{})
	h := srv.Handler()
	const password = "a long enough password"
	admin := &client{t: t, h: h}
	admin.do(http.MethodPost, "/api/auth/setup", map[string]string{"setupCode": setup.String(), "username": "admin", "password": password})
	tpl := odtTemplate(t, `<text:p>Bonjour &lt;&lt;nom&gt;&gt;</text:p>`)
	code, body := admin.send(http.MethodPost, "/api/templates", map[string]part{"file": {"tarif.odt", tpl}}, map[string]string{"path": "contrats/tarif.odt"})
	if code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, body)
	}
	id := int64(body["template"].(map[string]any)["id"].(float64))
	for _, u := range []map[string]any{
		{"username": "eve", "firstName": "Eve", "lastName": "Martin", "email": "eve@example.fr", "role": "editor", "password": password},
		{"username": "vic", "firstName": "Vic", "lastName": "Petit", "email": "vic@example.fr", "role": "viewer", "password": password},
	} {
		if code, out, _ := admin.do(http.MethodPost, "/api/users", u); code != http.StatusCreated {
			t.Fatalf("user: %d %v", code, out)
		}
	}
	token := func(name string) string {
		c := &client{t: t, h: h}
		if code, _, _ := c.do(http.MethodPost, "/api/auth/login", map[string]string{"username": name, "password": password}); code != http.StatusOK {
			t.Fatalf("login %s: %d", name, code)
		}
		_, created, _ := c.do(http.MethodPost, "/api/me/tokens", map[string]any{"name": "assistant"})
		return created["secret"].(string)
	}
	_, key, _ := admin.do(http.MethodPost, "/api/keys", map[string]any{"name": "application"})

	ts := httptest.NewServer(h)
	defer ts.Close()
	ctx := context.Background()
	call := func(secret, tool string, args map[string]any) *mcp.CallToolResult {
		t.Helper()
		session, err := connectMCP(t, ts.URL, secret)
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		defer session.Close()
		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		return res
	}
	target := map[string]any{"template_name": "contrats/tarif"}
	source := "#let data = json(bytes(sys.inputs.data))\nBonjour #data.nom\n"
	withSource := map[string]any{"template_name": "contrats/tarif.odt", "typst_source": source}

	// A key of an application, or a viewer, cannot; an editor reads and
	// previews; only an administrator attaches.
	if res := call(key["secret"].(string), "docibis_native_context", target); !res.IsError || !strings.Contains(textOf(res), "jeton personnel") {
		t.Fatalf("application key: %s", textOf(res))
	}
	if res := call(token("vic"), "docibis_native_context", target); !res.IsError || !strings.Contains(textOf(res), "éditeur") {
		t.Fatalf("viewer: %s", textOf(res))
	}
	eve := token("eve")
	res := call(eve, "docibis_native_context", target)
	if res.IsError || !strings.Contains(textOf(res), `"template":"contrats/tarif.odt"`) || !strings.Contains(textOf(res), `"version":1`) ||
		!strings.Contains(textOf(res), "sys.inputs.data") || !strings.Contains(textOf(res), `"test_data":[{`) {
		t.Fatalf("context: %s", textOf(res))
	}
	if res := call(eve, "docibis_native_context", map[string]any{"template_name": "contrats/absent"}); !res.IsError {
		t.Fatal("unknown template")
	}
	if res := call(eve, "docibis_native_preview", withSource); !res.IsError || !strings.Contains(textOf(res), "pas démarré") {
		t.Fatalf("preview without the Typst workers: %s", textOf(res))
	}
	native := &fakeNative{}
	srv.SetNative(native)
	if res := call(eve, "docibis_native_preview", withSource); res.IsError || native.calls != 1 || !strings.Contains(textOf(res), `"test_data_index":0`) {
		t.Fatalf("preview: %s", textOf(res))
	}
	if res := call(eve, "docibis_native_preview", map[string]any{"template_name": "contrats/tarif.odt", "typst_source": "#import \"@preview/x:1.0.0\""}); !res.IsError {
		t.Fatal("a package import must be refused")
	}
	if res := call(eve, "docibis_native_preview", map[string]any{"template_name": "contrats/tarif.odt", "typst_source": source, "test_data_index": 99}); !res.IsError {
		t.Fatal("unknown test data")
	}
	if res := call(eve, "docibis_native_submit", withSource); !res.IsError || !strings.Contains(textOf(res), "administrateur") {
		t.Fatalf("editor submit: %s", textOf(res))
	}
	if rec := admin.raw(http.MethodGet, fmt.Sprintf("/api/templates/%d/versions/1/native", id), "", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("a preview keeps nothing: %d", rec.Code)
	}

	res = call(token("admin"), "docibis_native_submit", withSource)
	if res.IsError || !strings.Contains(textOf(res), `"sourceSha256":"`+hashText(source)+`"`) || !strings.Contains(textOf(res), `"state":`) {
		t.Fatalf("submit: %s", textOf(res))
	}
	if rec := admin.raw(http.MethodGet, fmt.Sprintf("/api/templates/%d/versions/1/native", id), "", nil); rec.Code != http.StatusOK || rec.Body.String() != source {
		t.Fatalf("attached: %d %q", rec.Code, rec.Body.String())
	}
	if _, page, _ := admin.do(http.MethodGet, "/api/audit?q=MCP", nil); !strings.Contains(fmt.Sprint(page), "copie Typst") {
		t.Fatalf("audit: %v", page)
	}
}
