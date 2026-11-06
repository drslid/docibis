package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"docibis/internal/backup"
)

func TestBackups(t *testing.T) {
	t.Setenv("APP_SECRET_KEY", strings.Repeat("k", 32))
	srv, setup := open(t, nil)
	dir := t.TempDir()
	srv.SetBackups(backup.Dir(filepath.Join(dir, "backups")), dir)
	c := signIn(t, srv, setup)

	list := func() map[string]any {
		t.Helper()
		code, body, _ := c.do(http.MethodGet, "/api/backups", nil)
		if code != http.StatusOK {
			t.Fatalf("list: %d %v", code, body)
		}
		return body
	}
	if body := list(); len(body["backups"].([]any)) != 0 || body["keyMissing"] != false || !strings.HasSuffix(body["location"].(string), "backups") {
		t.Fatalf("empty list: %v", body)
	}
	if code, body, _ := c.do(http.MethodPost, "/api/backups", nil); code != http.StatusAccepted || body["running"] != true {
		t.Fatalf("create: %d %v", code, body)
	}
	var b map[string]any
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		body := list()
		if items := body["backups"].([]any); len(items) == 1 && body["running"] == false {
			b = items[0].(map[string]any)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("backup not made: %v", body)
		}
	}
	if b["kind"] != "manual" || b["verified"] != true || b["engine"] != "sqlite" || b["rows"].(map[string]any)["users"] != float64(1) {
		t.Fatalf("backup: %v", b)
	}
	name := b["name"].(string)
	rec := c.raw(http.MethodGet, "/api/backups/"+name, "", nil)
	if rec.Code != http.StatusOK || !bytes.HasPrefix(rec.Body.Bytes(), []byte("DIBK\x01")) || !strings.Contains(rec.Header().Get("Content-Disposition"), name) {
		t.Fatalf("download: %d %q", rec.Code, rec.Header())
	}
	if rec := c.raw(http.MethodGet, "/api/backups/docibis-20010101-000000-manual.backup", "", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown backup: %d", rec.Code)
	}
	if code, body, _ := c.do(http.MethodGet, "/api/audit?action=backup.download", nil); code != http.StatusOK || !strings.Contains(mustJSON(body), name) {
		t.Fatalf("audit: %d %v", code, body)
	}

	// The docibis command of the same machine, with the token of the data directory.
	token, err := os.ReadFile(filepath.Join(dir, LocalTokenFile))
	if err != nil {
		t.Fatal(err)
	}
	anonymous := &client{t: t, h: srv.Handler()}
	for _, headers := range [][]string{
		{"Authorization", "Bearer nope"},
		{"Authorization", "Bearer " + string(token), "X-Forwarded-For", "203.0.113.9"},
	} {
		if rec := anonymous.raw(http.MethodPost, "/local/backup", "", nil, headers...); rec.Code != http.StatusForbidden {
			t.Fatalf("local backup with %v: %d", headers, rec.Code)
		}
	}
	rec = anonymous.raw(http.MethodPost, "/local/backup?kind=update", "", nil, "Authorization", "Bearer "+string(token))
	var info backup.Info
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &info) != nil || info.Kind != backup.Update || !info.Verified {
		t.Fatalf("local backup: %d %s", rec.Code, rec.Body.String())
	}

	// The night: once a day.
	if code, body, _ := c.do(http.MethodPut, "/api/settings", map[string]any{"values": map[string]string{"backup_hour": "0"}}); code != http.StatusOK {
		t.Fatalf("hour: %d %v", code, body)
	}
	srv.nightly(context.Background())
	srv.nightly(context.Background())
	kinds := map[string]int{}
	for _, item := range list()["backups"].([]any) {
		kinds[item.(map[string]any)["kind"].(string)]++
	}
	if kinds["nightly"] != 1 || kinds["manual"] != 1 || kinds["update"] != 1 {
		t.Fatalf("backups: %v", kinds)
	}
}

func TestBackupsWithoutKey(t *testing.T) {
	t.Setenv("APP_SECRET_KEY", "")
	srv, setup := open(t, nil)
	srv.SetBackups(backup.Dir(t.TempDir()), t.TempDir())
	c := signIn(t, srv, setup)
	if code, body, _ := c.do(http.MethodPost, "/api/backups", nil); code != http.StatusBadRequest || !strings.Contains(body["detail"].(string), "APP_SECRET_KEY") {
		t.Fatalf("create without a key: %d %v", code, body)
	}
	if _, body, _ := c.do(http.MethodGet, "/api/backups", nil); body["keyMissing"] != true {
		t.Fatalf("list without a key: %v", body)
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
