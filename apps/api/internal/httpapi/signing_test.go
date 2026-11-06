package httpapi

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// signedInAdmin opens a server with its first administrator signed in.
func signedInAdmin(t *testing.T, instance string) (*Server, *client) {
	t.Helper()
	srv, setup := open(t, map[string]string{"INSTANCE_NAME": instance})
	srv.SetRenderer(&fakeRenderer{})
	c := &client{t: t, h: srv.Handler()}
	if code, _, _ := c.do(http.MethodPost, "/api/auth/setup", map[string]string{"setupCode": setup.String(), "username": "admin", "password": "a long enough password"}); code != http.StatusOK {
		t.Fatalf("setup: %d", code)
	}
	return srv, c
}

// rezip copies a zip, changing the content of one file and leaving out
// others.
func rezip(t *testing.T, raw []byte, name string, content []byte, without ...string) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, f := range zr.File {
		if slices.Contains(without, f.Name) {
			continue
		}
		w, _ := zw.Create(f.Name)
		if f.Name == name {
			_, _ = w.Write(content)
			continue
		}
		r, _ := f.Open()
		_, _ = io.Copy(w, r)
		r.Close()
	}
	_ = zw.Close()
	return out.Bytes()
}

func TestSignedExports(t *testing.T) {
	src, c := signedInAdmin(t, "INT")
	if code, out := c.send(http.MethodPost, "/api/templates", map[string]part{"file": {"x.odt", odtTemplate(t, `<text:p>Bonjour &lt;&lt;nom&gt;&gt;</text:p>`)}}, map[string]string{"path": "contrats/lettre.odt"}); code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, out)
	}
	export := c.raw(http.MethodGet, "/api/templates/export", "", nil).Body.Bytes()
	code, info, _ := c.do(http.MethodGet, "/api/signing-key", nil)
	if code != http.StatusOK || info["instance"] != "INT" || !strings.HasPrefix(fmt.Sprint(info["publicKey"]), "ed25519:") {
		t.Fatalf("signing key: %d %v", code, info)
	}
	public := fmt.Sprint(info["publicKey"])
	sig, err := checkSignature(export, src.trustedKeys(t.Context()))
	if err != nil || sig.Status != Trusted || valueOf(sig.Signer) != "INT" || valueOf(sig.PublicKey) != public {
		t.Fatalf("export: %+v %v", sig, err)
	}

	// Another instance previews, then imports.
	dst, c2 := signedInAdmin(t, "PROD")
	importZip := func(raw []byte, fields map[string]string) (int, map[string]any) {
		return c2.send(http.MethodPost, "/api/templates/import", map[string]part{"file": {"export.zip", raw}}, fields)
	}
	code, report := importZip(export, map[string]string{"dryRun": "true"})
	items := fmt.Sprint(report["items"])
	if code != http.StatusOK || report["dryRun"] != true || report["created"] != 1.0 || !strings.Contains(items, "action:created") || !strings.Contains(items, "path:contrats/lettre.odt") ||
		fmt.Sprint(report["signature"].(map[string]any)["status"]) != "untrusted" {
		t.Fatalf("preview: %d %v", code, report)
	}
	if code, list := c2.list("/api/templates"); code != http.StatusOK || len(list) != 0 {
		t.Fatalf("the preview wrote: %v", list)
	}

	// A zip changed after its export is refused.
	changed := rezip(t, export, "templates/contrats/lettre.odt", odtTemplate(t, `<text:p>Autre</text:p>`))
	if code, out := importZip(changed, nil); code != http.StatusBadRequest || !strings.Contains(fmt.Sprint(out["detail"]), "templates/contrats/lettre.odt changé") {
		t.Fatalf("changed zip: %d %v", code, out)
	}

	// Signed imports only: the key of INT must be trusted.
	if code, out, _ := c2.do(http.MethodPut, "/api/settings", map[string]any{"values": map[string]string{"import_require_signature": "true"}}); code != http.StatusOK {
		t.Fatalf("settings: %d %v", code, out)
	}
	if code, out := importZip(export, nil); code != http.StatusBadRequest || !strings.Contains(fmt.Sprint(out["detail"]), public) {
		t.Fatalf("untrusted: %d %v", code, out)
	}
	unsigned := rezip(t, export, "", nil, manifestName, signatureName)
	if code, out := importZip(unsigned, nil); code != http.StatusBadRequest || !strings.Contains(fmt.Sprint(out["detail"]), "pas signé") {
		t.Fatalf("unsigned: %d %v", code, out)
	}
	if code, out, _ := c2.do(http.MethodPut, "/api/settings", map[string]any{"values": map[string]string{"import_trusted_keys": "INT " + public}}); code != http.StatusOK {
		t.Fatalf("trusted keys: %d %v", code, out)
	}
	code, report = importZip(export, nil)
	if code != http.StatusOK || report["created"] != 1.0 || fmt.Sprint(report["signature"].(map[string]any)["signer"]) != "INT" {
		t.Fatalf("trusted import: %d %v", code, report)
	}
	if code, list := c2.list("/api/templates"); code != http.StatusOK || len(list) != 1 {
		t.Fatalf("imported: %v", list)
	}
	// Its own exports are trusted by an instance.
	if sig, err := checkSignature(c2.raw(http.MethodGet, "/api/templates/export", "", nil).Body.Bytes(), dst.trustedKeys(t.Context())); err != nil || valueOf(sig.Signer) != "PROD" {
		t.Fatalf("own export: %+v %v", sig, err)
	}
}
