package httpapi

import (
	"context"
	"strings"
	"testing"
)

// With APP_SECRET_KEY, the secrets of the authenticators are sealed, those
// stored before included, and only this key opens them.
func TestSecondFactorSecretsSealed(t *testing.T) {
	ctx := context.Background()
	srv, _ := open(t, nil)
	st := srv.db.Load()
	if err := st.CreateUser(ctx, "eve", "x", "admin"); err != nil {
		t.Fatal(err)
	}
	u, _, err := st.UserByLogin(ctx, "eve")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetTOTP(ctx, u.ID, "JBSWY3DPEHPK3PXP", true); err != nil {
		t.Fatal(err)
	}

	t.Setenv("APP_SECRET_KEY", strings.Repeat("k", 40))
	sealing := NewServer(nil, quiet)
	sealing.sealSecrets(ctx, st)
	got, _, err := st.UserByID(ctx, u.ID)
	if err != nil || !strings.HasPrefix(got.TOTPSecret, sealedPrefix) || strings.Contains(got.TOTPSecret, "JBSWY3DPEHPK3PXP") {
		t.Fatalf("secret stored as %q (%v)", got.TOTPSecret, err)
	}
	if secret := sealing.openSecret(got.TOTPSecret); secret != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("opened as %q", secret)
	}

	t.Setenv("APP_SECRET_KEY", strings.Repeat("o", 40))
	if secret := NewServer(nil, quiet).openSecret(got.TOTPSecret); secret != "" {
		t.Fatalf("another key opened the secret: %q", secret)
	}
	t.Setenv("APP_SECRET_KEY", "")
	if secret := NewServer(nil, quiet).openSecret(got.TOTPSecret); secret != "" {
		t.Fatalf("no key opened the secret: %q", secret)
	}
}
