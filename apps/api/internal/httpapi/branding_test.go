package httpapi

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBranding(t *testing.T) {
	srv, setup := open(t, nil)
	h := srv.Handler()
	admin, anonymous := &client{t: t, h: h}, &client{t: t, h: h}
	const password = "a long enough password"
	admin.do(http.MethodPost, "/api/auth/setup", map[string]string{"setupCode": setup.String(), "username": "admin", "password": password})

	if code, b, _ := anonymous.do(http.MethodGet, "/api/branding", nil); code != http.StatusOK || b["companyLogo"] != "none" {
		t.Fatalf("default logo, without session: %d %v", code, b)
	}
	if code, _, _ := anonymous.do(http.MethodPut, "/api/branding", map[string]any{"companyLogo": "none"}); code != http.StatusUnauthorized {
		t.Fatalf("change without session: %d", code)
	}
	admin.do(http.MethodPost, "/api/users", map[string]any{"username": "lea", "firstName": "Léa", "lastName": "L", "role": "viewer", "password": password})
	viewer := &client{t: t, h: h}
	viewer.do(http.MethodPost, "/api/auth/login", map[string]string{"username": "lea", "password": password})
	if code, _, _ := viewer.do(http.MethodPut, "/api/branding", map[string]any{"companyLogo": "none"}); code != http.StatusForbidden {
		t.Fatalf("change by a viewer: %d", code)
	}
	if code, b, _ := viewer.do(http.MethodGet, "/api/branding", nil); code != http.StatusOK || b["companyLogo"] != "none" {
		t.Fatalf("read by a viewer: %d %v", code, b)
	}

	dataURL := func(kind string, data []byte) string {
		return "data:" + kind + ";base64," + base64.StdEncoding.EncodeToString(data)
	}
	for _, image := range []string{
		dataURL("image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)),
		dataURL("image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><rect onload="alert(1)"/></svg>`)),
		dataURL("image/gif", []byte("GIF89a....")),
		dataURL("image/png", make([]byte, maxLogo+1)),
		"https://example.com/logo.png",
	} {
		if code, _, _ := admin.do(http.MethodPut, "/api/branding", map[string]any{"companyLogo": "custom", "image": image}); code != http.StatusBadRequest {
			t.Fatalf("logo %.40q must be refused: %d", image, code)
		}
	}
	if code, _, _ := admin.do(http.MethodPut, "/api/branding", map[string]any{"companyLogo": "custom"}); code != http.StatusBadRequest {
		t.Fatalf("custom without any image: %d", code)
	}

	png := append([]byte("\x89PNG\r\n\x1a\n"), "rest of the image"...)
	code, b, _ := admin.do(http.MethodPut, "/api/branding", map[string]any{"companyLogo": "custom", "image": dataURL("image/png", png)})
	version, _ := b["version"].(string)
	if code != http.StatusOK || b["companyLogo"] != "custom" || len(version) != 16 {
		t.Fatalf("custom logo: %d %v", code, b)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/branding/logo?v="+version, nil))
	if rec.Code != http.StatusOK || rec.Body.String() != string(png) || rec.Header().Get("Content-Type") != "image/png" ||
		rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" ||
		rec.Header().Get("Content-Security-Policy") != "default-src 'none'; style-src 'unsafe-inline'; sandbox" {
		t.Fatalf("logo served: %d %v", rec.Code, rec.Header())
	}

	// No logo, then the logo already sent again, without sending it again.
	admin.do(http.MethodPut, "/api/branding", map[string]any{"companyLogo": "none"})
	if code, b, _ := admin.do(http.MethodPut, "/api/branding", map[string]any{"companyLogo": "custom"}); code != http.StatusOK || b["version"] != version {
		t.Fatalf("back to the custom logo: %d %v", code, b)
	}
	if _, journal, _ := admin.do(http.MethodGet, "/api/audit?action=branding.update", nil); journal["total"] != float64(3) {
		t.Fatalf("journal: %v", journal)
	}
}
