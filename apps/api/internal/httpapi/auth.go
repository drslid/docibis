package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v5"
	"golang.org/x/crypto/bcrypt"

	"docibis/internal/store"
)

const cookieName = "docibis_session"

type ctxKey int

const (
	userKey ctxKey = iota
	tokenKey
	secureKey
	keyKey
	ipKey
	bearerKey
	agentKey
	requestIDKey
)

// requestID is the identifier of the request, also in its log lines and in
// the X-Request-Id header of the answer.
func requestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// withRequestID puts the identifier drawn by the RequestID middleware in the
// context of the request.
func withRequestID(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		r := c.Request()
		c.SetRequest(r.WithContext(context.WithValue(r.Context(), requestIDKey, c.Response().Header().Get(echo.HeaderXRequestID))))
		return next(c)
	}
}

// securityHeaders marks every answer of the API: nothing sniffed, framed or
// kept in a cache (documents and personal data), no address passed on, HTTPS
// only once seen over HTTPS. A handler may still allow caching, as the logo
// does.
func securityHeaders(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		h := c.Response().Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		h.Set("Strict-Transport-Security", "max-age=31536000")
		return next(c)
	}
}

func userFrom(ctx context.Context) (store.User, bool) {
	u, ok := ctx.Value(userKey).(store.User)
	return u, ok
}

// Paths of the console API that answer without a session. The Docmosis route
// checks its API key itself.
var publicPaths = map[string]bool{
	"/api/auth/session": true,
	"/api/auth/setup":   true,
	"/api/auth/login":   true,
	"/api/auth/mfa":     true,
	"/api/auth/logout":  true,
	docmosisPath:        true,
}

// Paths read without a session (the sign-in page shows the logo).
var publicReads = map[string]bool{
	"/api/branding":      true,
	"/api/branding/logo": true,
}

func public(r *http.Request) bool {
	return publicPaths[r.URL.Path] || publicReads[r.URL.Path] && (r.Method == http.MethodGet || r.Method == http.MethodHead)
}

// guard protects /api: the database must be open, a session is required
// except on the sign-in paths, and cross-site writes are refused.
func (s *Server) guard(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		r := c.Request()
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			return next(c)
		}
		st := s.db.Load()
		if st == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, starting)
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get(echo.HeaderSecFetchSite) == "cross-site" {
			return echo.NewHTTPError(http.StatusForbidden, "Demande d'un autre site refusée.")
		}
		ctx := context.WithValue(r.Context(), secureKey, s.secureRequest(r))
		ctx = context.WithValue(ctx, ipKey, clientIP(r))
		ctx = context.WithValue(ctx, agentKey, r.UserAgent())
		if ck, err := r.Cookie(cookieName); err == nil && ck.Value != "" {
			hash := hashToken(ck.Value)
			ctx = context.WithValue(ctx, tokenKey, hash)
			idle := time.Duration(s.cfg.Load().Int("session_idle_minutes")) * time.Minute
			if u, ok, err := st.SessionUser(ctx, hash, idle); err != nil {
				return err
			} else if ok {
				ctx = context.WithValue(ctx, userKey, u)
				s.touchSession(ctx, st, hash)
			}
		}
		u, signedIn := userFrom(ctx)
		if !signedIn && !public(r) {
			return echo.NewHTTPError(http.StatusUnauthorized, "Connexion nécessaire.")
		}
		if need := requiredRole(r.Method, r.URL.Path); signedIn && rank[u.Role] < rank[need] {
			return echo.NewHTTPError(http.StatusForbidden, "Votre rôle ne donne pas accès à cette page.")
		}
		c.SetRequest(r.WithContext(ctx))
		return next(c)
	}
}

// secureRequest tells whether the browser reached DocIbis over HTTPS:
// directly, through a proxy that says so, or through CloudFront (which only
// forwards HTTPS requests here).
func (s *Server) secureRequest(r *http.Request) bool {
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" || r.Header.Get("X-Amz-Cf-Id") != "" {
		return true
	}
	if cfg := s.cfg.Load(); cfg != nil {
		return strings.HasPrefix(cfg.String("public_url"), "https://")
	}
	return false
}

func sessionCookie(ctx context.Context, token string, maxAge int) string {
	secure, _ := ctx.Value(secureKey).(bool)
	ck := http.Cookie{Name: cookieName, Value: token, Path: "/", MaxAge: maxAge, HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode}
	return ck.String()
}

func newToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func hashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	return string(h), err
}

// dummyHash keeps the answer time the same when the account does not exist.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("docibis-no-such-user"), 12)

func passwordMatches(hash, password string) bool {
	if hash == "" {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,100}$`)

// SetupCode is the one-time code that creates the first administrator. It
// only exists in memory and in the service logs.
type SetupCode struct {
	mu   sync.Mutex
	code string
}

// NewSetupCode draws a code such as K7QM-2XV9 (no ambiguous characters).
func NewSetupCode() *SetupCode {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return &SetupCode{code: string(b[:4]) + "-" + string(b[4:])}
}

// String returns the code, for the logs.
func (c *SetupCode) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.code
}

func (c *SetupCode) matches(input string) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	input = strings.ToUpper(strings.TrimSpace(input))
	return c.code != "" && subtle.ConstantTimeCompare([]byte(input), []byte(c.code)) == 1
}

func (c *SetupCode) clear() {
	if c != nil {
		c.mu.Lock()
		c.code = ""
		c.mu.Unlock()
	}
}

// limiter slows down password guessing: after max failures on a key, each
// new attempt waits 30 s, then twice as long, up to 15 minutes.
type limiter struct {
	mu   sync.Mutex
	keys map[string]*attempts
}

type attempts struct {
	failures int
	until    time.Time
}

func (l *limiter) wait(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if a := l.keys[key]; a != nil {
		return time.Until(a.until)
	}
	return 0
}

func (l *limiter) fail(key string, max int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.keys == nil {
		l.keys = map[string]*attempts{}
	}
	a := l.keys[key]
	if a == nil {
		a = &attempts{}
		l.keys[key] = a
	}
	a.failures++
	if a.failures >= max {
		d := 30 * time.Second << min(a.failures-max, 5)
		a.until = time.Now().Add(min(d, 15*time.Minute))
	}
}

func (l *limiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.keys, key)
}
