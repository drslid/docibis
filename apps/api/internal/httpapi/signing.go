package httpapi

import (
	"archive/zip"
	"bytes"
	"cmp"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// The exports and the versions sent to another environment are signed by
// the instance (Ed25519): a manifest lists the SHA-256 of every file of the
// zip, and its signature comes with it. An import refuses a zip changed
// since it was signed and, when the administrators ask it, any zip not
// signed by this instance or an instance they trust.

const (
	signingKeySetting = "signing.key"
	manifestName      = "docibis-manifest.json"
	signatureName     = "docibis-manifest.sig"
	manifestFormat    = "docibis-manifest/1"
	keyPrefix         = "ed25519:"
)

type manifest struct {
	Format    string            `json:"format"`
	Instance  string            `json:"instance"`
	PublicKey string            `json:"publicKey"`
	SignedAt  time.Time         `json:"signedAt"`
	Files     map[string]string `json:"files"`
}

// signingKey returns the key of this instance, made on first use and kept
// sealed with APP_SECRET_KEY when it is set.
func (s *Server) signingKey(ctx context.Context) (ed25519.PrivateKey, error) {
	s.signing.Lock()
	defer s.signing.Unlock()
	st := s.db.Load()
	stored, ok, err := st.Setting(ctx, signingKeySetting)
	if err != nil {
		return nil, err
	}
	if ok && stored != "" {
		seed, err := base64.StdEncoding.DecodeString(s.openSecret(stored))
		if err != nil || len(seed) != ed25519.SeedSize {
			return nil, errors.New("signing key unreadable: was APP_SECRET_KEY changed?")
		}
		return ed25519.NewKeyFromSeed(seed), nil
	}
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return nil, err
	}
	sealed, err := s.sealSecret(base64.StdEncoding.EncodeToString(seed))
	if err != nil {
		return nil, err
	}
	if err := st.SetSetting(ctx, signingKeySetting, sealed); err != nil {
		return nil, err
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

func publicKeyText(key ed25519.PrivateKey) string {
	return keyPrefix + base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey))
}

func (s *Server) GetSigningKey(ctx context.Context, _ GetSigningKeyRequestObject) (GetSigningKeyResponseObject, error) {
	key, err := s.signingKey(ctx)
	if err != nil {
		return nil, err
	}
	return GetSigningKey200JSONResponse{Instance: s.cfg.Load().String("instance_name"), PublicKey: publicKeyText(key)}, nil
}

// zipSum hashes a file of a zip, at most maxUnpacked once unpacked.
func zipSum(f *zip.File) (string, error) {
	r, err := f.Open()
	if err != nil {
		return "", err
	}
	defer r.Close()
	h := sha256.New()
	if n, err := io.CopyN(h, r, maxUnpacked+1); err != nil && !errors.Is(err, io.EOF) {
		return "", err
	} else if n > maxUnpacked {
		return "", errors.New("file too large")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// signZip adds to a zip the manifest of its files and its signature. A zip
// that cannot be signed (the key is unreadable) goes unsigned: the
// instances that require signatures refuse it.
func (s *Server) signZip(ctx context.Context, raw []byte) []byte {
	signed, err := s.sign(ctx, raw)
	if err != nil {
		s.logger.Error("zip not signed", "error", err)
		return raw
	}
	return signed
}

func (s *Server) sign(ctx context.Context, raw []byte) ([]byte, error) {
	key, err := s.signingKey(ctx)
	if err != nil {
		return nil, err
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, err
	}
	m := manifest{Format: manifestFormat, Instance: s.cfg.Load().String("instance_name"), PublicKey: publicKeyText(key),
		SignedAt: time.Now().UTC().Truncate(time.Second), Files: map[string]string{}}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || f.Name == manifestName || f.Name == signatureName {
			continue
		}
		if m.Files[f.Name], err = zipSum(f); err != nil {
			return nil, err
		}
		if err := zw.Copy(f); err != nil {
			return nil, err
		}
	}
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	for name, content := range map[string][]byte{manifestName: body, signatureName: []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(key, body)))} {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: m.SignedAt})
		if err == nil {
			_, err = w.Write(content)
		}
		if err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// trustedKeys gives the public keys whose zips are trusted, with the name
// of their instance: this one, then those of import_trusted_keys ("NAME
// ed25519:…", one per line).
func (s *Server) trustedKeys(ctx context.Context) map[string]string {
	out := map[string]string{}
	cfg := s.cfg.Load()
	if key, err := s.signingKey(ctx); err == nil {
		out[publicKeyText(key)] = cfg.String("instance_name")
	}
	for _, line := range strings.Split(cfg.String("import_trusted_keys"), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || !strings.HasPrefix(fields[len(fields)-1], keyPrefix) {
			continue
		}
		key := fields[len(fields)-1]
		out[key] = cmp.Or(strings.Join(fields[:len(fields)-1], " "), key)
	}
	return out
}

// checkSignature verifies the signature of a zip against the keys trusted.
// A zip changed since it was signed is refused.
func checkSignature(raw []byte, trusted map[string]string) (ImportSignature, error) {
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return ImportSignature{}, refuse("Le fichier n'est pas un zip.")
	}
	var mf, sf *zip.File
	for _, f := range zr.File {
		switch f.Name {
		case manifestName:
			mf = f
		case signatureName:
			sf = f
		}
	}
	if mf == nil && sf == nil {
		return ImportSignature{Status: Unsigned}, nil
	}
	bad := func(why string) (ImportSignature, error) {
		return ImportSignature{}, refuse("Signature du zip invalide (%s) : il a été modifié depuis son export. Exportez-le de nouveau.", why)
	}
	if mf == nil || sf == nil {
		return bad("manifeste ou signature absent")
	}
	body, err := readZip(mf, 16<<20)
	if err != nil {
		return bad("manifeste illisible")
	}
	encoded, err := readZip(sf, 1<<10)
	if err != nil {
		return bad("signature illisible")
	}
	var m manifest
	if json.Unmarshal(body, &m) != nil || m.Format != manifestFormat || !strings.HasPrefix(m.PublicKey, keyPrefix) {
		return bad("manifeste illisible")
	}
	pub, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(m.PublicKey, keyPrefix))
	sig, err2 := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil || err2 != nil || len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, body, sig) {
		return bad("la signature ne correspond pas au manifeste")
	}
	seen := 0
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || f == mf || f == sf {
			continue
		}
		want, ok := m.Files[f.Name]
		if !ok {
			return bad(fmt.Sprintf("%s ajouté", f.Name))
		}
		if got, err := zipSum(f); err != nil || got != want {
			return bad(fmt.Sprintf("%s changé", f.Name))
		}
		seen++
	}
	if seen != len(m.Files) {
		return bad("des fichiers manquent")
	}
	out := ImportSignature{Status: Untrusted, Instance: ptr(m.Instance), PublicKey: ptr(m.PublicKey), SignedAt: ptr(m.SignedAt)}
	if name, ok := trusted[m.PublicKey]; ok {
		out.Status, out.Signer = Trusted, ptr(name)
	}
	return out, nil
}

// acceptZip checks the signature of a zip to import against the policy of
// this instance.
func (s *Server) acceptZip(ctx context.Context, raw []byte) (ImportSignature, error) {
	sig, err := checkSignature(raw, s.trustedKeys(ctx))
	if err != nil || sig.Status == Trusted || s.cfg.Load().String("import_require_signature") != "true" {
		return sig, err
	}
	if sig.Status == Unsigned {
		return sig, refuse("Import refusé : le zip n'est pas signé, et cette instance n'accepte que les zips signés (Configuration, Sécurité).")
	}
	return sig, refuse("Import refusé : le zip est signé par %q, qui n'est pas une instance de confiance. Ajoutez sa clé publique (%s) dans Configuration, Sécurité, Instances de confiance.",
		valueOf(sig.Instance), valueOf(sig.PublicKey))
}
