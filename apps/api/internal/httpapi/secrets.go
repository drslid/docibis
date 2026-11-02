package httpapi

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"os"
	"strings"

	"docibis/internal/store"
)

// The secrets of the authenticators are sealed with APP_SECRET_KEY (AES-GCM)
// when the variable is set: a copy of the database (Litestream on S3, a
// backup) then gives no second factor away.

const sealedPrefix = "sealed:"

// appKey derives the sealing key from APP_SECRET_KEY, nil when it is unset or
// shorter than 32 characters.
func appKey() []byte {
	k := os.Getenv("APP_SECRET_KEY")
	if len(k) < 32 {
		return nil
	}
	sum := sha256.Sum256([]byte(k))
	return sum[:]
}

func gcmOf(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// sealSecret returns what is stored for a secret: sealed when a key is set.
func (s *Server) sealSecret(secret string) (string, error) {
	if secret == "" || s.key == nil {
		return secret, nil
	}
	gcm, err := gcmOf(s.key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return sealedPrefix + base64.RawStdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(secret), nil)), nil
}

// openSecret returns the secret of what is stored; "" when it cannot be opened
// (no key, or another key): the code is then refused.
func (s *Server) openSecret(stored string) string {
	if !strings.HasPrefix(stored, sealedPrefix) {
		return stored
	}
	fail := func(reason string) string {
		s.logger.Warn("second-factor secret cannot be opened: was APP_SECRET_KEY changed?", "reason", reason)
		return ""
	}
	if s.key == nil {
		return fail("APP_SECRET_KEY is not set")
	}
	raw, err := base64.RawStdEncoding.DecodeString(stored[len(sealedPrefix):])
	if err != nil {
		return fail("not base64")
	}
	gcm, err := gcmOf(s.key)
	if err != nil || len(raw) < gcm.NonceSize() {
		return fail("too short")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return fail("wrong key")
	}
	return string(plain)
}

// sealSecrets seals the secrets stored before APP_SECRET_KEY was set.
func (s *Server) sealSecrets(ctx context.Context, st *store.Store) {
	if s.key == nil {
		s.logger.Warn("APP_SECRET_KEY is not set (32 characters at least): the second-factor secrets are stored in clear")
		return
	}
	users, err := st.Users(ctx, "", "", nil)
	if err != nil {
		s.logger.Warn("second-factor secrets not sealed", "error", err)
		return
	}
	sealed := 0
	for _, u := range users {
		if u.TOTPSecret == "" || strings.HasPrefix(u.TOTPSecret, sealedPrefix) {
			continue
		}
		secret, err := s.sealSecret(u.TOTPSecret)
		if err == nil {
			err = st.SetTOTP(ctx, u.ID, secret, u.TOTPEnabled)
		}
		if err != nil {
			s.logger.Warn("second-factor secret not sealed", "username", u.Username, "error", err)
			continue
		}
		sealed++
	}
	if sealed > 0 {
		s.logger.Info("second-factor secrets sealed", "accounts", sealed)
	}
}
