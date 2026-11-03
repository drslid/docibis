package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"docibis/internal/store"
)

// signIn creates the first administrator of a server and returns its client.
func signIn(t *testing.T, srv *Server, setup *SetupCode) *client {
	t.Helper()
	c := &client{t: t, h: srv.Handler()}
	if code, _, _ := c.do(http.MethodPost, "/api/auth/setup", map[string]string{"setupCode": setup.String(), "username": "admin", "password": "a long enough password"}); code != http.StatusOK {
		t.Fatalf("setup: %d", code)
	}
	return c
}

func unzip(t *testing.T, raw []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("not a zip: %v %q", err, raw)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		b, _ := readZip(f, 1<<20)
		out[f.Name] = string(b)
	}
	return out
}

func TestBundlesStatsAndSchedules(t *testing.T) {
	srv, setup := open(t, nil)
	c := signIn(t, srv, setup)
	srv.SetRenderer(&fakeRenderer{})
	tpl := odtTemplate(t, `<text:p>Contrat de &lt;&lt;nom&gt;&gt;</text:p>`)
	code, body := c.send(http.MethodPost, "/api/templates", map[string]part{"file": {"contrat.odt", tpl}}, map[string]string{"path": "souscription/contrat.odt"})
	if code != http.StatusCreated {
		t.Fatalf("template: %d %v", code, body)
	}
	tmpl := body["template"].(map[string]any)
	id := int64(tmpl["id"].(float64))
	if lint, _ := tmpl["lint"].(map[string]any); lint == nil || lint["errors"] != float64(0) {
		t.Fatalf("checks of the template: %v", tmpl)
	}
	if code, _ := c.send(http.MethodPost, "/api/templates", map[string]part{"file": {"cg.pdf", minimalPDF(1)}}, map[string]string{"path": "souscription/cg.pdf"}); code != http.StatusCreated {
		t.Fatalf("fixed PDF: %d", code)
	}

	// Bundles: checked when saved.
	for _, bad := range []map[string]any{
		{"name": "a b", "items": []any{map[string]any{"template": "souscription/contrat.odt"}}},
		{"name": "x", "items": []any{map[string]any{"template": "inconnu.docx"}}},
		{"name": "x", "items": []any{map[string]any{"template": "souscription/cg.pdf", "condition": "a =="}}},
		{"name": "x", "items": []any{}},
	} {
		if code, body, _ := c.do(http.MethodPost, "/api/bundles", bad); code != http.StatusBadRequest {
			t.Fatalf("bundle %v accepted: %d %v", bad, code, body)
		}
	}
	code, body, _ = c.do(http.MethodPost, "/api/bundles", map[string]any{"name": "souscription", "output": "zip", "items": []any{
		map[string]any{"template": "souscription/contrat.odt", "dataPath": "contrat"},
		map[string]any{"template": "/souscription/cg.pdf", "condition": "options.cg = true"},
	}})
	if code != http.StatusCreated || body["items"].([]any)[1].(map[string]any)["template"] != "souscription/cg.pdf" {
		t.Fatalf("bundle: %d %v", code, body)
	}
	bundleID := int64(body["id"].(float64))
	if code, _, _ := c.do(http.MethodPost, "/api/bundles", map[string]any{"name": "souscription", "items": []any{map[string]any{"template": "souscription/cg.pdf"}}}); code != http.StatusBadRequest {
		t.Fatalf("same name twice: %d", code)
	}

	_, body, _ = c.do(http.MethodPost, "/api/keys", map[string]any{"name": "crm"})
	secret := body["secret"].(string)
	render := func(name string, data map[string]any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(map[string]any{"data": data})
		return c.raw(http.MethodPost, "/v1/bundles/"+name+"/render", "application/json", raw, "Authorization", "Bearer "+secret)
	}
	rec := render("souscription", map[string]any{"contrat": map[string]any{"nom": "Durand"}, "options": map[string]any{"cg": false}})
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Disposition") != "attachment; filename=souscription.zip" {
		t.Fatalf("bundle render: %d %s", rec.Code, rec.Body.String())
	}
	if files := unzip(t, rec.Body.Bytes()); len(files) != 1 || !strings.Contains(files["contrat.pdf"], "Contrat de Durand") {
		t.Fatalf("bundle without the general conditions: %v", files)
	}
	rec = render("souscription", map[string]any{"contrat": map[string]any{"nom": "Martin"}, "options": map[string]any{"cg": true}})
	if files := unzip(t, rec.Body.Bytes()); rec.Code != http.StatusOK || len(files) != 2 || !strings.HasPrefix(files["cg.pdf"], "%PDF-") {
		t.Fatalf("bundle with the general conditions: %d %v", rec.Code, files)
	}
	if rec := render("souscription", map[string]any{"contrat": "x"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("data part not an object: %d %s", rec.Code, rec.Body.String())
	}
	if rec := render("inconnue", map[string]any{}); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown bundle: %d", rec.Code)
	}
	raw, _ := json.Marshal(map[string]any{"data": map[string]any{"contrat": map[string]any{"nom": "Essai"}}})
	if rec := c.raw(http.MethodPost, fmt.Sprintf("/api/bundles/%d/try", bundleID), "application/json", raw); rec.Code != http.StatusOK {
		t.Fatalf("try bundle: %d %s", rec.Code, rec.Body.String())
	}

	// Statistics of the template: the documents of the bundles count.
	code, body, _ = c.do(http.MethodGet, fmt.Sprintf("/api/templates/%d/stats?days=7", id), nil)
	if code != http.StatusOK || body["count"] != float64(3) || body["errors"] != float64(0) || body["refused"] != float64(0) || len(body["series"].([]any)) != 7 {
		t.Fatalf("stats: %d %v", code, body)
	}
	callers := body["callers"].([]any)
	if first := callers[0].(map[string]any); first["actor"] != "crm" || first["count"] != float64(2) || first["source"] != "api" {
		t.Fatalf("callers: %v", callers)
	}
	code, list := c.list("/api/templates/stats")
	if code != http.StatusOK || len(list) != 1 || list[0]["id"] != float64(id) || list[0]["count"] != float64(3) {
		t.Fatalf("uses: %d %v", code, list)
	}

	// A planned publication, done by the server when its time comes.
	if code, _ := c.send(http.MethodPost, fmt.Sprintf("/api/templates/%d/versions", id), map[string]part{"file": {"contrat.odt", odtTemplate(t, `<text:p>V2 &lt;&lt;nom&gt;&gt;</text:p>`)}}, map[string]string{"publish": "false"}); code != http.StatusCreated {
		t.Fatalf("version 2: %d", code)
	}
	at := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	for _, bad := range []map[string]any{{"version": 1, "at": at}, {"version": 3, "at": at}, {"version": 2, "at": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)}} {
		if code, _, _ := c.do(http.MethodPut, fmt.Sprintf("/api/templates/%d/schedule", id), bad); code != http.StatusBadRequest {
			t.Fatalf("schedule %v: %d", bad, code)
		}
	}
	code, body, _ = c.do(http.MethodPut, fmt.Sprintf("/api/templates/%d/schedule", id), map[string]any{"version": 2, "at": at})
	if scheduled, _ := body["template"].(map[string]any)["scheduled"].(map[string]any); code != http.StatusOK || scheduled["version"] != float64(2) || scheduled["by"] != "admin" {
		t.Fatalf("schedule: %d %v", code, body)
	}
	code, body, _ = c.do(http.MethodDelete, fmt.Sprintf("/api/templates/%d/schedule", id), nil)
	if code != http.StatusOK || body["template"].(map[string]any)["scheduled"] != nil {
		t.Fatalf("unschedule: %d %v", code, body)
	}
	past := time.Now().Add(-time.Minute)
	if err := srv.db.Load().ScheduleTemplate(context.Background(), id, 2, &past, "admin"); err != nil {
		t.Fatal(err)
	}
	srv.publishDue(context.Background())
	_, body, _ = c.do(http.MethodGet, fmt.Sprintf("/api/templates/%d", id), nil)
	if got := body["template"].(map[string]any); got["published"] != float64(2) || got["previousPublished"] != float64(1) || got["scheduled"] != nil {
		t.Fatalf("after the planned publication: %v", got)
	}
	code, body, _ = c.do(http.MethodGet, "/api/audit?action=template.publish", nil)
	if items, _ := body["items"].([]any); code != http.StatusOK || len(items) == 0 || !strings.Contains(fmt.Sprint(items[0].(map[string]any)["details"]), "programmée") {
		t.Fatalf("audit of the planned publication: %d %v", code, body)
	}

	if code, _, _ := c.do(http.MethodDelete, fmt.Sprintf("/api/bundles/%d", bundleID), nil); code != http.StatusNoContent {
		t.Fatalf("delete bundle: %d", code)
	}
}

func TestEnvironmentsAndPromotion(t *testing.T) {
	// The target: another DocIbis (REC) with a key that receives templates.
	rec, recSetup := open(t, map[string]string{"INSTANCE_NAME": "REC"})
	target := signIn(t, rec, recSetup)
	remote := httptest.NewServer(rec.Handler())
	defer remote.Close()
	_, body, _ := target.do(http.MethodPost, "/api/keys", map[string]any{"name": "INT", "receivesTemplates": true})
	receiver := body["secret"].(string)
	if scopes := fmt.Sprint(body["key"].(map[string]any)["scopes"]); scopes != "[render templates]" {
		t.Fatalf("scopes: %s", scopes)
	}
	_, body, _ = target.do(http.MethodPost, "/api/keys", map[string]any{"name": "app"})
	renderOnly := body["secret"].(string)

	srv, setup := open(t, map[string]string{"INSTANCE_NAME": "INT"})
	c := signIn(t, srv, setup)
	srv.SetRenderer(&fakeRenderer{})
	code, body := c.send(http.MethodPost, "/api/templates", map[string]part{"file": {"avis.odt", odtTemplate(t, `<text:p>Avis &lt;&lt;nom&gt;&gt;</text:p>`)}}, map[string]string{"path": "avis/avis.odt"})
	if code != http.StatusCreated {
		t.Fatalf("template: %d", code)
	}
	id := int64(body["template"].(map[string]any)["id"].(float64))

	save := func(envs []map[string]any) int {
		raw, _ := json.Marshal(envs)
		return c.raw(http.MethodPut, "/api/environments", "application/json", raw).Code
	}
	for _, bad := range [][]map[string]any{
		{{"name": "REC", "url": "http://rec.example.fr"}},
		{{"name": "REC", "url": remote.URL}, {"name": "rec", "url": remote.URL}},
		{{"name": "REC", "url": remote.URL, "key": "secret"}},
	} {
		if code := save(bad); code != http.StatusBadRequest {
			t.Fatalf("environments %v: %d", bad, code)
		}
	}
	envs := []map[string]any{{"name": "REC", "url": remote.URL + "/", "key": renderOnly}}
	if code := save(envs); code != http.StatusOK {
		t.Fatalf("environments: %d", code)
	}
	code, list := c.list("/api/environments")
	masked, _ := list[0]["key"].(string)
	if code != http.StatusOK || list[0]["url"] != remote.URL || !strings.HasSuffix(masked, "_…") || strings.Contains(masked, renderOnly[len(masked):]) {
		t.Fatalf("environments read: %d %v", code, list)
	}

	// A key that only renders is refused by the target.
	code, body, _ = c.do(http.MethodPost, fmt.Sprintf("/api/templates/%d/promotions", id), map[string]any{"environment": "rec", "version": 1, "publish": true})
	if code != http.StatusOK || body["status"] != "error" || !strings.Contains(body["detail"].(string), "403") {
		t.Fatalf("promotion with a render key: %d %v", code, body)
	}

	// The masked key sent back keeps the saved one; a new key replaces it.
	envs[0]["key"] = masked
	if code := save(envs); code != http.StatusOK {
		t.Fatalf("environments again: %d", code)
	}
	if saved, _ := srv.environments(context.Background()); saved[0].Key != renderOnly {
		t.Fatalf("masked key not kept: %+v", saved)
	}
	envs[0]["key"] = receiver
	if code := save(envs); code != http.StatusOK {
		t.Fatalf("new key: %d", code)
	}
	code, body, _ = c.do(http.MethodPost, fmt.Sprintf("/api/templates/%d/promotions", id), map[string]any{"environment": "REC", "version": 1, "publish": true})
	if code != http.StatusOK || body["status"] != "ok" || body["detail"] != "créé, publiée" {
		t.Fatalf("promotion: %d %v", code, body)
	}
	code, list = target.list("/api/templates")
	if code != http.StatusOK || len(list) != 1 || list[0]["path"] != "avis/avis.odt" || list[0]["published"] != float64(1) {
		t.Fatalf("template received: %d %v", code, list)
	}
	code, body, _ = c.do(http.MethodPost, fmt.Sprintf("/api/templates/%d/promotions", id), map[string]any{"environment": "REC", "version": 1})
	if code != http.StatusOK || body["detail"] != "déjà à jour" {
		t.Fatalf("second promotion: %d %v", code, body)
	}
	code, list = c.list(fmt.Sprintf("/api/templates/%d/promotions", id))
	if code != http.StatusOK || len(list) != 3 || list[0]["detail"] != "déjà à jour" || list[2]["status"] != "error" {
		t.Fatalf("promotions: %d %v", code, list)
	}
	code, body, _ = target.do(http.MethodGet, "/api/audit?action=template.receive", nil)
	if items, _ := body["items"].([]any); code != http.StatusOK || len(items) != 2 || !strings.Contains(fmt.Sprint(items[0].(map[string]any)["details"]), "depuis INT") {
		t.Fatalf("audit of the target: %d %v", code, body)
	}
	if code, _, _ := c.do(http.MethodPost, fmt.Sprintf("/api/templates/%d/promotions", id), map[string]any{"environment": "PROD", "version": 1}); code != http.StatusBadRequest {
		t.Fatalf("unknown environment: %d", code)
	}
}

func TestImageAddresses(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("x", 64))
	calls := 0
	images := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Path {
		case "/logo.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(png)
		case "/page.html":
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, "<html></html>")
		case "/away":
			http.Redirect(w, r, "http://example.org/logo.png", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer images.Close()
	loopbackAllowed = true
	defer func() { loopbackAllowed = false }()
	srv, _ := open(t, map[string]string{})
	if srv.imageFetcher(context.Background()) != nil {
		t.Fatal("images read with no domain allowed")
	}
	if err := srv.cfg.Load().Update(context.Background(), srv.db.Load(), map[string]string{"image_url_allowlist": "https://127.0.0.1/\nexample.com"}); err != nil {
		t.Fatal(err)
	}
	fetch := srv.imageFetcher(context.Background())
	if got, err := fetch(images.URL + "/logo.png"); err != nil || !bytes.Equal(got, png) {
		t.Fatalf("image: %v", err)
	}
	if _, err := fetch(images.URL + "/logo.png"); err != nil || calls != 1 {
		t.Fatalf("image not kept: %d calls, %v", calls, err)
	}
	for _, refused := range []string{images.URL + "/page.html", images.URL + "/away", images.URL + "/none.png", "http://localhost/logo.png", "ftp://127.0.0.1/x.png", "http://169.254.169.254/latest"} {
		if _, err := fetch(refused); err == nil {
			t.Fatalf("%s read", refused)
		}
	}
	if !hostAllowed("cdn.example.com", []string{"example.com"}) || hostAllowed("badexample.com", []string{"example.com"}) {
		t.Fatal("sub-domains")
	}
	loopbackAllowed = false
	for _, address := range []string{"127.0.0.1:443", "[::1]:443", "10.0.3.7:443", "172.16.0.1:80", "192.168.1.1:443", "100.64.0.1:443", "169.254.169.254:80", "169.254.170.2:80", "[fd00:ec2::254]:80", "0.0.0.0:80"} {
		if refuseInternal("tcp", address, nil) == nil {
			t.Errorf("%s reachable", address)
		}
	}
	if err := refuseInternal("tcp", "93.184.215.14:443", nil); err != nil {
		t.Errorf("public address refused: %v", err)
	}
}

func TestReleaseNotesFromCommits(t *testing.T) {
	srv, _ := open(t, nil)
	ctx := context.Background()
	saved := changesFile
	changesFile = filepath.Join(t.TempDir(), "changes.txt")
	defer func() { changesFile = saved }()
	now := time.Now().UTC()
	line := func(sha string, ago time.Duration, subject string) string {
		return sha + "\t" + now.Add(-ago).Format(time.RFC3339) + "\t" + subject
	}
	write := func(lines ...string) {
		if err := os.WriteFile(changesFile, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	notes := func() []store.Release {
		rows, _, err := srv.db.Load().Releases(ctx, store.Page{Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	write(line("c3", time.Hour, "Liasses de documents"), line("c2", 2*time.Hour, "API : code du contrat régénéré"), line("c1", 30*24*time.Hour, "Ancien changement"))
	srv.recordRelease(ctx)
	srv.recordRelease(ctx)
	if rows := notes(); len(rows) != 1 || rows[0].Content != "- Liasses de documents" || !strings.HasPrefix(rows[0].Title, "Mise à jour du ") || rows[0].CreatedBy != "DocIbis" {
		t.Fatalf("first note: %+v", rows)
	}
	write(line("c4", 0, "Moteur rapide"), line("c3", time.Hour, "Liasses de documents"))
	srv.recordRelease(ctx)
	if rows := notes(); len(rows) != 2 || rows[0].Content != "- Moteur rapide" {
		t.Fatalf("second note: %+v", rows)
	}
}

func TestCompareHelpers(t *testing.T) {
	text := diffLines(strings.Fields("a b c d e f g h i j"), strings.Fields("a b c d X f g h i j k"))
	var got []string
	for _, l := range text {
		got = append(got, string(l.Op)[:1]+l.Line)
	}
	if strings.Join(got, " ") != "sc sd re aX sf sg s… si sj ak" {
		t.Fatalf("diff: %v", got)
	}
	a, b := image.NewGray(image.Rect(0, 0, 20, 20)), image.NewGray(image.Rect(0, 0, 20, 20))
	for i := range a.Pix {
		a.Pix[i], b.Pix[i] = 255, 255
	}
	b.Pix[10*20+10] = 0
	permille, overlay := highlight(a, b)
	if permille != 2.5 || overlay.RGBAAt(10, 10).R != 220 || overlay.RGBAAt(0, 0).R != 255 {
		t.Fatalf("highlight: %v %v", permille, overlay.RGBAAt(10, 10))
	}
}

func TestCompareFixedPDFVersions(t *testing.T) {
	if _, err := os.Stat("/usr/local/lib/docibis/pdftools.py"); err != nil && os.Getenv("DOCIBIS_PDFTOOLS") == "" {
		t.Skip("PDF tools not installed (DOCIBIS_PDFTOOLS)")
	}
	srv, setup := open(t, nil)
	c := signIn(t, srv, setup)
	srv.SetRenderer(&fakeRenderer{})
	code, body := c.send(http.MethodPost, "/api/templates", map[string]part{"file": {"cg.pdf", minimalPDF(1)}}, map[string]string{"path": "cg.pdf"})
	if code != http.StatusCreated {
		t.Fatalf("pdf: %d", code)
	}
	id := int64(body["template"].(map[string]any)["id"].(float64))
	if code, _ := c.send(http.MethodPost, fmt.Sprintf("/api/templates/%d/versions", id), map[string]part{"file": {"cg.pdf", minimalPDF(2)}}, nil); code != http.StatusCreated {
		t.Fatalf("version 2: %d", code)
	}
	code, body, _ = c.do(http.MethodPost, fmt.Sprintf("/api/templates/%d/compare", id), map[string]any{"from": 1, "to": 2})
	pages, _ := body["pages"].([]any)
	if code != http.StatusOK || body["identical"] != false || len(pages) != 2 || pages[1].(map[string]any)["permille"] != float64(-1) ||
		!strings.HasPrefix(pages[0].(map[string]any)["before"].(string), "data:image/png;base64,") {
		t.Fatalf("compare: %d %v", code, body)
	}
	if code, _, _ := c.do(http.MethodPost, fmt.Sprintf("/api/templates/%d/compare", id), map[string]any{"from": 1, "to": 1}); code != http.StatusBadRequest {
		t.Fatalf("same version: %d", code)
	}
}
