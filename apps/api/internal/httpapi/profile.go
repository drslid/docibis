package httpapi

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"docibis/internal/store"
)

// sessionID shows a session without its hash: enough to tell sessions
// apart and close one.
func sessionID(hash string) string {
	if len(hash) > 16 {
		return hash[:16]
	}
	return hash
}

// sessionTouches remembers when each session was last noted as used, so
// that the base is written at most once a minute per session.
type sessionTouches struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func (t *sessionTouches) due(hash string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.last == nil || len(t.last) > 10000 {
		t.last = map[string]time.Time{}
	}
	if now.Sub(t.last[hash]) < time.Minute {
		return false
	}
	t.last[hash] = now
	return true
}

func (s *Server) touchSession(ctx context.Context, st *store.Store, hash string) {
	now := time.Now()
	if !s.touches.due(hash, now) {
		return
	}
	if err := st.TouchSession(ctx, hash, now); err != nil {
		s.logger.Warn("cannot record the use of a session", "error", err)
	}
}

func (s *Server) ListMySessions(ctx context.Context, _ ListMySessionsRequestObject) (ListMySessionsResponseObject, error) {
	u, _ := userFrom(ctx)
	current, _ := ctx.Value(tokenKey).(string)
	rows, err := s.db.Load().UserSessions(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	out := make(ListMySessions200JSONResponse, 0, len(rows))
	for _, r := range rows {
		item := UserSession{Id: sessionID(r.ID), CreatedAt: r.CreatedAt, LastUsedAt: r.LastUsedAt, ExpiresAt: r.ExpiresAt, Current: r.ID == current}
		if r.UserAgent != "" {
			ua := r.UserAgent
			item.UserAgent = &ua
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *Server) CloseMySession(ctx context.Context, req CloseMySessionRequestObject) (CloseMySessionResponseObject, error) {
	u, _ := userFrom(ctx)
	st := s.db.Load()
	rows, err := st.UserSessions(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if sessionID(r.ID) != req.Id {
			continue
		}
		if _, err := st.DeleteUserSession(ctx, u.ID, r.ID); err != nil {
			return nil, err
		}
		s.audit(ctx, u.Username, "session.close", "user:"+u.Username, "")
		return CloseMySession204Response{}, nil
	}
	return CloseMySession404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Session inconnue ou déjà fermée."))}, nil
}

var (
	avatarIcon  = regexp.MustCompile(`^[a-z0-9-]{0,40}$`)
	avatarColor = regexp.MustCompile(`^([a-z]{3,12}(-[0-9]{2,3})?|#[0-9A-Fa-f]{6})?$`)
)

func (s *Server) SaveAvatar(ctx context.Context, req SaveAvatarRequestObject) (SaveAvatarResponseObject, error) {
	u, _ := userFrom(ctx)
	if req.Body == nil || !avatarIcon.MatchString(req.Body.Icon) || !avatarColor.MatchString(req.Body.Color) {
		return SaveAvatar400ApplicationProblemPlusJSONResponse{badRequest("Icône ou couleur inconnue.")}, nil
	}
	st := s.db.Load()
	if err := st.SaveAvatar(ctx, u.ID, req.Body.Icon, req.Body.Color); err != nil {
		return nil, err
	}
	u.AvatarIcon, u.AvatarColor = req.Body.Icon, req.Body.Color
	return SaveAvatar200JSONResponse(s.userOut(u)), nil
}

// newUserToken draws a personal token such as dit_k7qm2xv9_… (160 random
// bits after the prefix). Only its hash is stored.
func newUserToken() (secret, prefix string) {
	prefix = "dit_" + randomText(8)
	return prefix + "_" + randomText(32), prefix
}

func userToken(t store.UserToken) UserToken {
	out := UserToken{Id: t.ID, Name: t.Name, Prefix: t.Prefix, CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt, LastUsedAt: t.LastUsedAt, Status: UserTokenStatusActive}
	switch {
	case t.RevokedAt != nil:
		out.Status = UserTokenStatusRevoked
	case t.ExpiresAt != nil && t.ExpiresAt.Before(time.Now()):
		out.Status = UserTokenStatusExpired
	}
	return out
}

func (s *Server) ListMyTokens(ctx context.Context, _ ListMyTokensRequestObject) (ListMyTokensResponseObject, error) {
	u, _ := userFrom(ctx)
	rows, err := s.db.Load().UserTokens(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	out := make(ListMyTokens200JSONResponse, 0, len(rows))
	for _, t := range rows {
		out = append(out, userToken(t))
	}
	return out, nil
}

func (s *Server) CreateMyToken(ctx context.Context, req CreateMyTokenRequestObject) (CreateMyTokenResponseObject, error) {
	if req.Body == nil {
		return CreateMyToken400ApplicationProblemPlusJSONResponse{badRequest("Donnez un nom au jeton.")}, nil
	}
	name := strings.TrimSpace(req.Body.Name)
	if name == "" || len([]rune(name)) > 100 {
		return CreateMyToken400ApplicationProblemPlusJSONResponse{badRequest("Donnez un nom au jeton (100 caractères au plus), par exemple celui de votre client MCP.")}, nil
	}
	days := s.cfg.Load().Int("api_key_expiry_days")
	if req.Body.ExpiresInDays != nil && *req.Body.ExpiresInDays > 0 {
		days = *req.Body.ExpiresInDays
	}
	if days < 1 || days > 1825 {
		return CreateMyToken400ApplicationProblemPlusJSONResponse{badRequest("Durée de vie : de 1 à 1825 jours.")}, nil
	}
	u, _ := userFrom(ctx)
	secret, prefix := newUserToken()
	expires := time.Now().UTC().AddDate(0, 0, days)
	t := store.UserToken{UserID: u.ID, Name: name, Prefix: prefix, Hash: hashToken(secret), ExpiresAt: &expires}
	if err := s.db.Load().CreateUserToken(ctx, &t); err != nil {
		return nil, err
	}
	s.logger.Info("personal token created", "token", prefix, "name", name, "by", u.Username, "expires", expires.Format(time.DateOnly))
	s.audit(ctx, u.Username, "token.create", "token:"+prefix, name+", expire le "+expires.Format(time.DateOnly))
	return CreateMyToken201JSONResponse{Token: userToken(t), Secret: secret}, nil
}

func (s *Server) RevokeMyToken(ctx context.Context, req RevokeMyTokenRequestObject) (RevokeMyTokenResponseObject, error) {
	u, _ := userFrom(ctx)
	ok, err := s.db.Load().RevokeUserToken(ctx, u.ID, req.Id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return RevokeMyToken404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Jeton inconnu ou déjà révoqué."))}, nil
	}
	s.audit(ctx, u.Username, "token.revoke", "token:"+u.Username, "")
	return RevokeMyToken204Response{}, nil
}

func (s *Server) ListMcpCalls(ctx context.Context, req ListMcpCallsRequestObject) (ListMcpCallsResponseObject, error) {
	u, _ := userFrom(ctx)
	userID := u.ID
	if req.Params.All != nil && *req.Params.All && u.Role == string(Admin) {
		userID = 0
	}
	rows, total, err := s.db.Load().MCPCalls(ctx, userID, page(req.Params.Limit, req.Params.Offset))
	if err != nil {
		return nil, err
	}
	out := McpCallPage{Items: make([]McpCall, 0, len(rows)), Total: total}
	for _, c := range rows {
		item := McpCall{Id: c.ID, CreatedAt: c.CreatedAt, Actor: c.Actor, Via: c.Via, Method: c.Method, Status: McpCallStatus(c.Status), DurationMs: c.DurationMs}
		if c.Tool != "" {
			item.Tool = &c.Tool
		}
		if c.Error != "" {
			item.Error = &c.Error
		}
		out.Items = append(out.Items, item)
	}
	return ListMcpCalls200JSONResponse(out), nil
}
