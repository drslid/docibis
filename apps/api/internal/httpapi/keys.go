package httpapi

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v5"

	"docibis/internal/store"
)

// keyAlphabet is base32: a random byte modulo 32 stays uniform.
const keyAlphabet = "abcdefghijklmnopqrstuvwxyz234567"

func randomText(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = keyAlphabet[int(b[i])%len(keyAlphabet)]
	}
	return string(b)
}

// newAPIKey draws a key such as dib_k7qm2xv9_… (160 random bits after the
// prefix). Only its hash is stored.
func newAPIKey() (secret, prefix string) {
	prefix = "dib_" + randomText(8)
	return prefix + "_" + randomText(32), prefix
}

func keyFrom(ctx context.Context) (store.APIKey, bool) {
	k, ok := ctx.Value(keyKey).(store.APIKey)
	return k, ok
}

// keyGuard protects /v1: every call needs an active API key in
// Authorization: Bearer, within the calls per minute of the key. The
// contract itself stays public.
func (s *Server) keyGuard(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		r := c.Request()
		if r.URL.Path == docmosisPath {
			// The Docmosis route may also carry the key in its body.
			c.SetRequest(r.WithContext(context.WithValue(r.Context(), bearerKey, bearer(r))))
			return next(c)
		}
		if !strings.HasPrefix(r.URL.Path, "/v1/") || r.URL.Path == "/v1/openapi.json" {
			return next(c)
		}
		st := s.db.Load()
		if st == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, starting)
		}
		k, ok, err := s.activeKey(r.Context(), bearer(r))
		if err != nil {
			return err
		}
		if !ok {
			c.Response().Header().Set("WWW-Authenticate", `Bearer realm="DocIbis"`)
			return echo.NewHTTPError(http.StatusUnauthorized, "Clé API ou jeton personnel absent, inconnu, expiré ou révoqué. Créez une clé dans la console (page Clés API) ou un jeton dans votre profil.")
		}
		if wait, msg := s.throttle(k); wait > 0 {
			c.Response().Header().Set("Retry-After", strconv.Itoa(wait))
			return echo.NewHTTPError(http.StatusTooManyRequests, msg)
		}
		if producesDocuments(r.Method, r.URL.Path) {
			wait, msg, err := s.quotaWait(r.Context(), k)
			if err != nil {
				return err
			}
			if wait > 0 {
				c.Response().Header().Set("Retry-After", strconv.Itoa(wait))
				return echo.NewHTTPError(http.StatusTooManyRequests, msg)
			}
		}
		c.SetRequest(r.WithContext(context.WithValue(r.Context(), keyKey, k)))
		return next(c)
	}
}

// bearer is the key sent in Authorization: Bearer, or in X-API-Key as some MCP
// clients send it.
func bearer(r *http.Request) string {
	secret, found := strings.CutPrefix(r.Header.Get(echo.HeaderAuthorization), "Bearer ")
	if !found {
		secret = r.Header.Get("X-API-Key")
	}
	return strings.TrimSpace(secret)
}

// activeKey finds the active key of a secret and notes its use, at most once
// a minute per key so that the copy of the base is not flooded. A personal
// token (dit_) reads as a key named after its account.
func (s *Server) activeKey(ctx context.Context, secret string) (store.APIKey, bool, error) {
	if strings.HasPrefix(secret, "dit_") {
		return s.activeUserToken(ctx, secret)
	}
	if !strings.HasPrefix(secret, "dib_") {
		return store.APIKey{}, false, nil
	}
	st := s.db.Load()
	k, ok, err := st.ActiveAPIKey(ctx, hashToken(secret))
	if err != nil || !ok {
		return k, false, err
	}
	if k.LastUsedAt == nil || time.Since(*k.LastUsedAt) > time.Minute {
		if err := st.TouchAPIKey(ctx, k.ID, time.Now()); err != nil {
			s.logger.Warn("cannot record the use of an API key", "key", k.Prefix, "error", err)
		}
	}
	return k, true, nil
}

// activeUserToken checks a personal token. Its calls count apart from the
// API keys (negative number) and are made in the name of its account.
func (s *Server) activeUserToken(ctx context.Context, secret string) (store.APIKey, bool, error) {
	st := s.db.Load()
	t, u, ok, err := st.ActiveUserToken(ctx, hashToken(secret))
	if err != nil || !ok {
		return store.APIKey{}, false, err
	}
	if t.LastUsedAt == nil || time.Since(*t.LastUsedAt) > time.Minute {
		if err := st.TouchUserToken(ctx, t.ID, time.Now()); err != nil {
			s.logger.Warn("cannot record the use of a personal token", "token", t.Prefix, "error", err)
		}
	}
	return store.APIKey{ID: -t.ID, Name: u.Username, Prefix: t.Prefix, Scopes: string(Render), CreatedBy: u.Username,
		CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt, LastUsedAt: t.LastUsedAt, UserID: u.ID}, true, nil
}

func apiKey(k store.APIKey) ApiKey {
	out := ApiKey{
		Id: k.ID, Name: k.Name, Prefix: k.Prefix, CreatedBy: k.CreatedBy, CreatedAt: k.CreatedAt,
		ExpiresAt: k.ExpiresAt, LastUsedAt: k.LastUsedAt, RevokedAt: k.RevokedAt, Status: ApiKeyStatusActive,
		RatePerMinute: &k.RatePerMinute, SpaceId: &k.SpaceID, DailyQuota: &k.DailyQuota, MonthlyQuota: &k.MonthlyQuota,
	}
	for _, scope := range strings.Split(k.Scopes, ",") {
		out.Scopes = append(out.Scopes, ApiKeyScopes(scope))
	}
	switch {
	case k.RevokedAt != nil:
		out.Status = ApiKeyStatusRevoked
	case k.ExpiresAt != nil && k.ExpiresAt.Before(time.Now()):
		out.Status = ApiKeyStatusExpired
	}
	return out
}

func (s *Server) ListApiKeys(ctx context.Context, _ ListApiKeysRequestObject) (ListApiKeysResponseObject, error) {
	keys, err := s.db.Load().APIKeys(ctx)
	if err != nil {
		return nil, err
	}
	scopes := make([]string, 0, len(keys))
	for _, k := range keys {
		scopes = append(scopes, keyScope(k.ID))
	}
	day, month, err := s.db.Load().Usage(ctx, time.Now(), scopes...)
	if err != nil {
		return nil, err
	}
	out := make(ListApiKeys200JSONResponse, 0, len(keys))
	for _, k := range keys {
		item := apiKey(k)
		item.Usage = &Usage{Today: day[keyScope(k.ID)], Month: month[keyScope(k.ID)]}
		out = append(out, item)
	}
	return out, nil
}

// keyLimits checks the space and the quotas given for a key.
func (s *Server) keyLimits(ctx context.Context, k *store.APIKey, space *int64, daily, monthly *int) error {
	if space != nil {
		if *space != 0 {
			if _, ok, err := s.db.Load().SpaceByID(ctx, *space); err != nil {
				return err
			} else if !ok {
				return refuse("Espace inconnu.")
			}
		}
		k.SpaceID = *space
	}
	if daily != nil {
		k.DailyQuota = *daily
	}
	if monthly != nil {
		k.MonthlyQuota = *monthly
	}
	if k.DailyQuota < 0 || k.DailyQuota > maxQuota || k.MonthlyQuota < 0 || k.MonthlyQuota > maxQuota {
		return refuse("Quotas : de 0 (sans limite) à %d documents.", maxQuota)
	}
	return nil
}

func (s *Server) CreateApiKey(ctx context.Context, req CreateApiKeyRequestObject) (CreateApiKeyResponseObject, error) {
	if req.Body == nil {
		return CreateApiKey400ApplicationProblemPlusJSONResponse{badRequest("Donnez un nom à la clé.")}, nil
	}
	name := strings.TrimSpace(req.Body.Name)
	if name == "" || len([]rune(name)) > 100 {
		return CreateApiKey400ApplicationProblemPlusJSONResponse{badRequest("Donnez un nom à la clé (100 caractères au plus), par exemple celui de l'application.")}, nil
	}
	days := s.cfg.Load().Int("api_key_expiry_days")
	if req.Body.ExpiresInDays != nil {
		days = *req.Body.ExpiresInDays
	}
	if days < 1 || days > 1825 {
		return CreateApiKey400ApplicationProblemPlusJSONResponse{badRequest("Durée de vie : de 1 à 1825 jours.")}, nil
	}
	rate := 0
	if req.Body.RatePerMinute != nil {
		rate = *req.Body.RatePerMinute
	}
	if rate < 0 || rate > maxRatePerMinute {
		return CreateApiKey400ApplicationProblemPlusJSONResponse{badRequest(rateMessage)}, nil
	}
	u, _ := userFrom(ctx)
	secret, prefix := newAPIKey()
	now := time.Now().UTC()
	expires := now.AddDate(0, 0, days)
	scopes := string(Render)
	if req.Body.ReceivesTemplates != nil && *req.Body.ReceivesTemplates {
		scopes += "," + string(Templates)
	}
	k := store.APIKey{Name: name, Prefix: prefix, Hash: hashToken(secret), Scopes: scopes, CreatedBy: u.Username, CreatedAt: now, ExpiresAt: &expires, RatePerMinute: rate}
	if err := s.keyLimits(ctx, &k, req.Body.SpaceId, req.Body.DailyQuota, req.Body.MonthlyQuota); err != nil {
		var te *tryError
		if errors.As(err, &te) {
			return CreateApiKey400ApplicationProblemPlusJSONResponse{badRequest(te.msg)}, nil
		}
		return nil, err
	}
	if err := s.db.Load().CreateAPIKey(ctx, &k); err != nil {
		return nil, err
	}
	s.logger.Info("API key created", "key", prefix, "name", name, "by", u.Username, "expires", expires.Format(time.DateOnly))
	s.audit(ctx, u.Username, "apikey.create", "key:"+prefix, name+", expire le "+expires.Format(time.DateOnly)+map[bool]string{true: ", reçoit des modèles"}[scopes != string(Render)])
	return CreateApiKey201JSONResponse{Key: apiKey(k), Secret: secret}, nil
}

func (s *Server) RevokeApiKey(ctx context.Context, req RevokeApiKeyRequestObject) (RevokeApiKeyResponseObject, error) {
	ok, err := s.db.Load().RevokeAPIKey(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return RevokeApiKey404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Clé inconnue.")), nil
	}
	u, _ := userFrom(ctx)
	s.logger.Info("API key revoked", "id", req.Id, "by", u.Username)
	s.audit(ctx, u.Username, "apikey.revoke", fmt.Sprintf("key:%d", req.Id), "")
	return RevokeApiKey204Response{}, nil
}

const (
	maxRatePerMinute = 100000
	rateMessage      = "Appels par minute : de 1 à 100000, ou 0 pour le réglage commun."
)

func (s *Server) UpdateApiKey(ctx context.Context, req UpdateApiKeyRequestObject) (UpdateApiKeyResponseObject, error) {
	st := s.db.Load()
	k, ok, err := st.APIKeyByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return UpdateApiKey404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Clé inconnue."))}, nil
	}
	if req.Body == nil {
		return UpdateApiKey400ApplicationProblemPlusJSONResponse{badRequest("Rien à changer.")}, nil
	}
	if req.Body.Name != nil {
		k.Name = strings.TrimSpace(*req.Body.Name)
		if k.Name == "" || len([]rune(k.Name)) > 100 {
			return UpdateApiKey400ApplicationProblemPlusJSONResponse{badRequest("Donnez un nom à la clé (100 caractères au plus).")}, nil
		}
	}
	if req.Body.RatePerMinute != nil {
		if k.RatePerMinute = *req.Body.RatePerMinute; k.RatePerMinute < 0 || k.RatePerMinute > maxRatePerMinute {
			return UpdateApiKey400ApplicationProblemPlusJSONResponse{badRequest(rateMessage)}, nil
		}
	}
	if err := s.keyLimits(ctx, &k, req.Body.SpaceId, req.Body.DailyQuota, req.Body.MonthlyQuota); err != nil {
		var te *tryError
		if errors.As(err, &te) {
			return UpdateApiKey400ApplicationProblemPlusJSONResponse{badRequest(te.msg)}, nil
		}
		return nil, err
	}
	if err := st.UpdateAPIKey(ctx, &k); err != nil {
		return nil, err
	}
	u, _ := userFrom(ctx)
	s.audit(ctx, u.Username, "apikey.update", "key:"+k.Prefix, fmt.Sprintf("%s, %d appels par minute, espace %d, %d documents par jour, %d par mois", k.Name, k.RatePerMinute, k.SpaceID, k.DailyQuota, k.MonthlyQuota))
	return UpdateApiKey200JSONResponse(apiKey(k)), nil
}
