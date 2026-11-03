package httpapi

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/digitorus/pdfsign/verify"
)

// sealPEM makes a self-signed certificate and its key, as SEAL_CERT and
// SEAL_KEY hold them.
func sealPEM(t *testing.T) (cert, key string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Assurances Exemple", Organization: []string{"Exemple"}},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageContentCommitment,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	pk, _ := x509.MarshalPKCS8PrivateKey(priv)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk}))
}

// callRenderForm posts a PDF form and its fields to /v1/render.
func callRenderForm(h http.Handler, key string, template []byte, fields map[string]string) *httptest.ResponseRecorder {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	fw, _ := w.CreateFormFile("template", "formulaire.pdf")
	_, _ = fw.Write(template)
	for k, v := range fields {
		_ = w.WriteField(k, v)
	}
	_ = w.Close()
	req := httptest.NewRequest(http.MethodPost, "/v1/render", &b)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestSeal(t *testing.T) {
	cert, key := sealPEM(t)
	t.Setenv("SEAL_CERT", cert)
	t.Setenv("SEAL_KEY", "")
	if _, err := loadSealer(); err == nil {
		t.Fatal("a certificate without its key must be refused")
	}
	other, _ := sealPEM(t)
	t.Setenv("SEAL_CERT", other)
	t.Setenv("SEAL_KEY", key)
	if _, err := loadSealer(); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("a key of another certificate: %v", err)
	}
	t.Setenv("SEAL_CERT", cert)

	srv, _ := open(t, nil)
	srv.SetRenderer(&fakeRenderer{})
	h, k := srv.Handler(), testKey(t, srv, "contrats")
	post := func(fields map[string]string) (int, []byte) {
		rec := callRenderForm(h, k, formPDF(), fields)
		return rec.Code, rec.Body.Bytes()
	}
	data := `{"Nom": "Dupont", "client": {"ville": "Paris"}, "accord": true}`
	srv.sealer = nil
	if code, body := post(map[string]string{"data": data, "seal": "true"}); code != http.StatusBadRequest || !strings.Contains(string(body), "SEAL_CERT") {
		t.Fatalf("without a seal configured: %d %s", code, body)
	}
	if srv.sealer, _ = loadSealer(); srv.sealer == nil {
		t.Fatal("seal not loaded")
	}
	code, body := post(map[string]string{"data": data, "seal": "true"})
	if code != http.StatusOK {
		t.Fatalf("sealed: %d %s", code, body)
	}
	file := filepath.Join(t.TempDir(), "sealed.pdf")
	if err := os.WriteFile(file, body, 0o600); err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(file)
	defer f.Close()
	res, err := verify.VerifyFile(f)
	if err != nil || len(res.Signers) != 1 || !res.Signers[0].ValidSignature || res.Signers[0].Name != "Assurances Exemple" {
		t.Fatalf("verify: %v %+v", err, res)
	}
	if code, body := post(map[string]string{"data": data, "seal": "true", "passwordProtect": "secret"}); code != http.StatusBadRequest || !strings.Contains(string(body), "mot de passe") {
		t.Fatalf("seal and password: %d %s", code, body)
	}
}
