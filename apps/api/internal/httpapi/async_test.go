package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"docibis/internal/store"
)

// testKey creates an API key straight in the base.
func testKey(t *testing.T, srv *Server, name string) string {
	t.Helper()
	secret, prefix := newAPIKey()
	k := store.APIKey{Name: name, Prefix: prefix, Hash: hashToken(secret), Scopes: string(Render), CreatedBy: "test", CreatedAt: time.Now()}
	if err := srv.db.Load().CreateAPIKey(context.Background(), &k); err != nil {
		t.Fatal(err)
	}
	return secret
}

func postAsync(h http.Handler, key string, template []byte, fields map[string]string) (int, map[string]any, http.Header) {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	if template != nil {
		fw, _ := w.CreateFormFile("template", "modele.odt")
		_, _ = fw.Write(template)
	}
	for k, v := range fields {
		_ = w.WriteField(k, v)
	}
	_ = w.Close()
	req := httptest.NewRequest(http.MethodPost, "/v1/render/async", &b)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out, rec.Header()
}

func getWithKey(h http.Handler, key, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// waitJob reads a job until until accepts it.
func waitJob(t *testing.T, h http.Handler, key, id string, until func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		rec := getWithKey(h, key, "/v1/jobs/"+id)
		var job map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &job)
		if rec.Code == http.StatusOK && until(job) {
			return job
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s: %d %v", id, rec.Code, job)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func finished(job map[string]any) bool { return job["status"] == "done" || job["status"] == "failed" }

// lockedBuffer keeps the logs written by the workers.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

type hookCall struct {
	header http.Header
	body   []byte
}

func TestAsyncRender(t *testing.T) {
	srv, _ := open(t, nil)
	logs := &lockedBuffer{}
	srv.logger = slog.New(slog.NewTextHandler(logs, nil))
	srv.hookKey = []byte("a webhook secret of 32 characters at least")
	waits := hookWaits
	hookWaits = []time.Duration{time.Millisecond, time.Millisecond}
	defer func() { hookWaits = waits }()

	// The application of the caller: an S3-like upload and a callback.
	var uploadStatus atomic.Int32
	uploadStatus.Store(http.StatusOK)
	var mu sync.Mutex
	var uploads []hookCall
	callbacks := make(chan hookCall, 10)
	app := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/upload":
			mu.Lock()
			uploads = append(uploads, hookCall{r.Header.Clone(), body})
			mu.Unlock()
			w.WriteHeader(int(uploadStatus.Load()))
		case r.Method == http.MethodPost && r.URL.Path == "/callback":
			callbacks <- hookCall{r.Header.Clone(), body}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer app.Close()
	srv.hooks = app.Client()
	uploadURL := app.URL + "/upload?X-Amz-Signature=presigned-secret"

	h := srv.Handler()
	key, other := testKey(t, srv, "portail"), testKey(t, srv, "autre")
	tpl := odtTemplate(t, `<text:p>Bonjour &lt;&lt;name&gt;&gt;</text:p>`)
	data := map[string]string{"data": `{"name": "Ann"}`}
	if code, _, _ := postAsync(h, key, tpl, data); code != http.StatusServiceUnavailable {
		t.Fatalf("before the workers start: %d", code)
	}
	fake := &fakeRenderer{}
	srv.SetRenderer(fake)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.StartAsync(ctx, 2)

	// Refused at once: the addresses, the data, the format, the template.
	if code, p, _ := postAsync(h, key, tpl, map[string]string{"data": `{}`, "callbackUrl": app.URL + "/callback"}); code != http.StatusBadRequest ||
		!strings.Contains(p["detail"].(string), "aucun domaine") {
		t.Fatalf("no domain allowed: %d %v", code, p)
	}
	if err := srv.cfg.Load().Update(ctx, srv.db.Load(), map[string]string{"async_url_allowlist": "127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	for _, fields := range []map[string]string{
		{"data": `{}`, "callbackUrl": strings.Replace(app.URL, "https:", "http:", 1) + "/callback"},
		{"data": `{}`, "uploadUrl": "https://elsewhere.example.com/upload"},
		{"data": `{}`, "callbackUrl": "https://user:pass@127.0.0.1/callback"},
		{"data": `{}`, "callbackUrl": "https://127.0.0.1/" + strings.Repeat("x", maxHookURL)},
		{"data": "not json"},
		{"data": `{}`, "format": "xls"},
	} {
		if code, p, _ := postAsync(h, key, tpl, fields); code != http.StatusBadRequest {
			t.Fatalf("%v: %d %v", fields, code, p)
		}
	}
	if code, p, _ := postAsync(h, key, nil, map[string]string{"data": `{}`, "templateName": "absent.docx"}); code != http.StatusNotFound {
		t.Fatalf("unknown template: %d %v", code, p)
	}

	// Uploaded, then called back: the document does not stay.
	fields := map[string]string{"data": `{"name": "Ann"}`, "uploadUrl": uploadURL, "callbackUrl": app.URL + "/callback"}
	code, job, header := postAsync(h, key, tpl, fields)
	id, _ := job["id"].(string)
	if code != http.StatusAccepted || !strings.HasPrefix(id, "job_") || header.Get("Location") != "/v1/jobs/"+id || job["status"] != "queued" ||
		job["upload"] != "pending" || job["callback"] != "pending" || job["template"] != "modele.odt" || job["format"] != "pdf" {
		t.Fatalf("submit: %d %v %v", code, job, header)
	}
	var call hookCall
	select {
	case call = <-callbacks:
	case <-time.After(10 * time.Second):
		t.Fatal("no callback")
	}
	var told map[string]any
	if err := json.Unmarshal(call.body, &told); err != nil || told["id"] != id || told["status"] != "done" || told["upload"] != "done" || told["documentUrl"] != nil ||
		call.header.Get("X-DocIbis-Event") != "render.done" || call.header.Get("X-DocIbis-Job") != id || call.header.Get("Content-Type") != "application/json" {
		t.Fatalf("callback: %v %v %v", told, call.header, err)
	}
	if want := sign(srv.hookKey, call.header.Get("X-DocIbis-Timestamp"), call.body); call.header.Get("X-DocIbis-Signature") != want {
		t.Fatalf("signature %q, want %q", call.header.Get("X-DocIbis-Signature"), want)
	}
	mu.Lock()
	if len(uploads) != 1 || uploads[0].header.Get("Content-Type") != "application/pdf" || !strings.Contains(string(uploads[0].body), "Bonjour Ann") {
		t.Fatalf("uploads: %+v", uploads)
	}
	mu.Unlock()
	done := waitJob(t, h, key, id, func(j map[string]any) bool { return j["callback"] == "done" })
	if done["status"] != "done" || done["finishedAt"] == nil || done["size"] == nil || done["documentUrl"] != nil {
		t.Fatalf("job: %v", done)
	}
	if rec := getWithKey(h, key, "/v1/jobs/"+id+"/document"); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "uploadUrl") {
		t.Fatalf("uploaded document: %d %s", rec.Code, rec.Body.String())
	}
	for _, p := range []string{"/v1/jobs/" + id, "/v1/jobs/" + id + "/document", "/v1/jobs/job_unknown"} {
		if rec := getWithKey(h, other, p); rec.Code != http.StatusNotFound {
			t.Fatalf("another key reads %s: %d", p, rec.Code)
		}
	}

	// Kept when the upload fails, after three attempts; read with the key.
	uploadStatus.Store(http.StatusServiceUnavailable)
	_, job, _ = postAsync(h, key, tpl, map[string]string{"data": `{"name": "Bob"}`, "uploadUrl": uploadURL, "outputName": "lettre.pdf"})
	id = job["id"].(string)
	done = waitJob(t, h, key, id, finished)
	if done["status"] != "done" || done["upload"] != "failed" || done["callback"] != nil || !strings.HasSuffix(done["documentUrl"].(string), "/v1/jobs/"+id+"/document") {
		t.Fatalf("upload failed: %v", done)
	}
	mu.Lock()
	if len(uploads) != 4 {
		t.Fatalf("%d uploads, want 1 + 3 attempts", len(uploads))
	}
	mu.Unlock()
	rec := getWithKey(h, key, "/v1/jobs/"+id+"/document")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/pdf" || rec.Header().Get("Content-Disposition") != "attachment; filename=lettre.pdf" ||
		!strings.Contains(rec.Body.String(), "Bonjour Bob") {
		t.Fatalf("document: %d %v %q", rec.Code, rec.Header(), rec.Body.String())
	}

	// Word, without any address.
	_, job, _ = postAsync(h, key, tpl, map[string]string{"data": `{"name": "Eve"}`, "format": "docx"})
	id = job["id"].(string)
	if done = waitJob(t, h, key, id, finished); done["status"] != "done" || done["upload"] != nil || done["format"] != "docx" {
		t.Fatalf("docx: %v", done)
	}
	if rec := getWithKey(h, key, "/v1/jobs/"+id+"/document"); rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != docxType {
		t.Fatalf("docx document: %d %v", rec.Code, rec.Header())
	}

	// A failure is told too.
	fake.mu.Lock()
	fake.err = errors.New("LibreOffice crashed")
	fake.mu.Unlock()
	_, job, _ = postAsync(h, key, tpl, map[string]string{"data": `{}`, "callbackUrl": app.URL + "/callback", "uploadUrl": uploadURL})
	id = job["id"].(string)
	select {
	case call = <-callbacks:
	case <-time.After(10 * time.Second):
		t.Fatal("no callback of the failure")
	}
	if call.header.Get("X-DocIbis-Event") != "render.failed" {
		t.Fatalf("failure event: %v", call.header)
	}
	done = waitJob(t, h, key, id, func(j map[string]any) bool { return j["callback"] == "done" })
	if done["status"] != "failed" || done["error"] == nil || done["upload"] != "failed" {
		t.Fatalf("failed job: %v", done)
	}
	if rec := getWithKey(h, key, "/v1/jobs/"+id+"/document"); rec.Code != http.StatusConflict {
		t.Fatalf("document of a failed job: %d", rec.Code)
	}

	// A presigned address is a credential: never in the logs.
	if out := logs.String(); strings.Contains(out, "presigned-secret") || !strings.Contains(out, "async job finished") {
		t.Fatalf("logs:\n%s", out)
	}
}

func TestAsyncQueueFull(t *testing.T) {
	srv, _ := open(t, nil)
	srv.SetRenderer(&fakeRenderer{})
	if err := srv.cfg.Load().Update(context.Background(), srv.db.Load(), map[string]string{"async_queue_size": "1", "async_url_allowlist": "127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	callbacks := make(chan []byte, 1)
	app := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		callbacks <- body
	}))
	defer app.Close()
	srv.hooks = app.Client()
	h, key := srv.Handler(), testKey(t, srv, "portail")
	tpl := odtTemplate(t, `<text:p>Bonjour</text:p>`)
	// No worker yet: the first waits, the second finds the queue full.
	code, job, _ := postAsync(h, key, tpl, map[string]string{"data": `{}`, "callbackUrl": app.URL + "/callback"})
	if code != http.StatusAccepted {
		t.Fatalf("first: %d", code)
	}
	if code, p, header := postAsync(h, key, tpl, map[string]string{"data": `{}`}); code != http.StatusServiceUnavailable || header.Get("Retry-After") == "" {
		t.Fatalf("queue full: %d %v", code, p)
	}

	// The service stops: the waiting job fails, and its caller is told.
	srv.StopAsync()
	var told map[string]any
	select {
	case body := <-callbacks:
		_ = json.Unmarshal(body, &told)
	default:
		t.Fatal("no callback")
	}
	if told["id"] != job["id"] || told["status"] != "failed" || told["error"] != jobStopped || told["callback"] != "pending" {
		t.Fatalf("callback: %v", told)
	}
	if rec := getWithKey(h, key, "/v1/jobs/"+job["id"].(string)); !strings.Contains(rec.Body.String(), `"status":"failed"`) || !strings.Contains(rec.Body.String(), `"callback":"done"`) {
		t.Fatalf("stopped job: %s", rec.Body.String())
	}
	if code, _, _ := postAsync(h, key, tpl, map[string]string{"data": `{}`}); code != http.StatusAccepted {
		t.Fatalf("room again: %d", code)
	}
}

func TestRefuseLocal(t *testing.T) {
	for address, refused := range map[string]bool{
		"127.0.0.1:443":               true,
		"[::1]:443":                   true,
		"169.254.169.254:80":          true,
		"169.254.170.2:80":            true,
		"[fd00:ec2::254]:80":          true,
		"0.0.0.0:443":                 true,
		"224.0.0.1:443":               true,
		"10.0.12.7:443":               false,
		"52.219.170.10:443":           false,
		"[2a05:d018::1]:443":          false,
		"[::ffff:169.254.169.254]:80": true,
	} {
		if err := refuseLocal("tcp", address, nil); (err != nil) != refused {
			t.Errorf("%s: %v", address, err)
		}
	}
}
