package httpapi

import (
	"context"
	"net"
	"net/http"
	"net/mail"
	"strings"
	"unicode/utf8"

	"docibis/internal/store"
)

// rank orders the roles: each one can do what the lower ones do.
var rank = map[string]int{"viewer": 1, "editor": 2, "admin": 3}

// requiredRole is the lowest role allowed on a console route; "" means any
// signed-in account.
func requiredRole(method, path string) string {
	write := method != http.MethodGet && method != http.MethodHead
	switch {
	case strings.HasPrefix(path, "/api/users"), strings.HasPrefix(path, "/api/keys"),
		path == "/api/settings", strings.HasPrefix(path, "/api/audit"), strings.HasPrefix(path, "/api/monitoring"), strings.HasPrefix(path, "/api/metrics"),
		strings.HasPrefix(path, "/api/branding") && write,
		strings.HasPrefix(path, "/api/releases") && write,
		strings.HasPrefix(path, "/api/environments") && write,
		strings.HasPrefix(path, "/api/templates") && strings.HasSuffix(path, "/native") && method == http.MethodPut,
		strings.HasPrefix(path, "/api/templates") && (method == http.MethodDelete || path == "/api/templates/check-all"):
		return "admin"
	case strings.HasPrefix(path, "/api/try/"),
		strings.HasPrefix(path, "/api/renders/") && write,
		strings.HasPrefix(path, "/api/bundles") && write,
		strings.HasPrefix(path, "/api/templates") && strings.HasSuffix(path, "/native"),
		strings.HasPrefix(path, "/api/templates") && (write || path == "/api/templates/export"):
		return "editor"
	}
	return ""
}

// clientIP is the chain of addresses the request came through: the one the
// browser claims first, then those seen by CloudFront, the load balancer and
// this server. The first entry alone could be forged, so the chain is kept,
// trusted end first when it has to be cut.
func clientIP(r *http.Request) string {
	chain, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		chain = r.RemoteAddr
	}
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		chain = xff + ", " + chain
	}
	if len(chain) > 200 {
		chain = "…" + chain[len(chain)-200:]
	}
	return chain
}

// audit records an action in the journal; a failure is logged, never shown.
// Each action is also a log line "audit", for the SIEM.
func (s *Server) audit(ctx context.Context, actor, action, target, details string) {
	ip, _ := ctx.Value(ipKey).(string)
	s.logger.Info("audit", "actor", actor, "action", action, "target", target, "details", details, "ip", ip)
	st := s.db.Load()
	if st == nil {
		return
	}
	e := &store.AuditEntry{Actor: actor, Action: action, Target: target, Details: details, IP: ip}
	if err := st.Audit(context.WithoutCancel(ctx), e); err != nil {
		s.logger.Warn("audit entry not recorded", "action", action, "error", err)
	}
}

func (s *Server) userOut(u store.User) User {
	lang := Language(u.Language)
	if !lang.Valid() {
		lang = Fr
	}
	out := User{
		Id: u.ID, Username: u.Username, FirstName: u.FirstName, LastName: u.LastName, DisplayName: displayName(u), Email: u.Email,
		Role: Role(u.Role), Active: u.Active, Language: lang, CreatedAt: u.CreatedAt, LastLoginAt: u.LastLoginAt,
	}
	required := s.mfaRequired(u.Role)
	out.MfaEnabled, out.MfaRequired = &u.TOTPEnabled, &required
	if u.AvatarIcon != "" {
		out.AvatarIcon = &u.AvatarIcon
	}
	if u.AvatarColor != "" {
		out.AvatarColor = &u.AvatarColor
	}
	return out
}

// displayName is "First Last", or the username for accounts without names.
func displayName(u store.User) string {
	if name := strings.TrimSpace(u.FirstName + " " + u.LastName); name != "" {
		return name
	}
	if u.DisplayName != "" {
		return u.DisplayName
	}
	return u.Username
}

// checkProfile validates the names and the e-mail of an account.
func checkProfile(first, last, email *string) string {
	for _, name := range []*string{first, last} {
		if name != nil && utf8.RuneCountInString(strings.TrimSpace(*name)) > 60 {
			return "Prénom et nom : 60 caractères au plus."
		}
	}
	if email != nil && strings.TrimSpace(*email) != "" {
		if a, err := mail.ParseAddress(strings.TrimSpace(*email)); err != nil || a.Address != strings.TrimSpace(*email) || len(*email) > 200 {
			return "Adresse e-mail invalide."
		}
	}
	return ""
}

// setNames applies the names sent, keeping display_name in step for searches.
func setNames(u *store.User, first, last *string) {
	if first != nil {
		u.FirstName = trimmed(first)
	}
	if last != nil {
		u.LastName = trimmed(last)
	}
	u.DisplayName = strings.TrimSpace(u.FirstName + " " + u.LastName)
}

func trimmed(p *string) string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(*p)
}

func (s *Server) ListUsers(ctx context.Context, req ListUsersRequestObject) (ListUsersResponseObject, error) {
	role := ""
	if req.Params.Role != nil {
		role = string(*req.Params.Role)
	}
	users, err := s.db.Load().Users(ctx, trimmed(req.Params.Q), role, req.Params.Active)
	if err != nil {
		return nil, err
	}
	out := make(ListUsers200JSONResponse, 0, len(users))
	for _, u := range users {
		out = append(out, s.userOut(u))
	}
	return out, nil
}

func (s *Server) CreateUser(ctx context.Context, req CreateUserRequestObject) (CreateUserResponseObject, error) {
	if req.Body == nil {
		return CreateUser400ApplicationProblemPlusJSONResponse{badRequest("Compte incomplet.")}, nil
	}
	b := req.Body
	username := strings.TrimSpace(b.Username)
	if !usernamePattern.MatchString(username) {
		return CreateUser400ApplicationProblemPlusJSONResponse{badRequest("Identifiant : lettres, chiffres et . _ @ - seulement.")}, nil
	}
	if !b.Role.Valid() {
		return CreateUser400ApplicationProblemPlusJSONResponse{badRequest("Rôle inconnu.")}, nil
	}
	if msg := checkProfile(&b.FirstName, &b.LastName, b.Email); msg != "" {
		return CreateUser400ApplicationProblemPlusJSONResponse{badRequest(msg)}, nil
	}
	if strings.TrimSpace(b.FirstName) == "" || strings.TrimSpace(b.LastName) == "" {
		return CreateUser400ApplicationProblemPlusJSONResponse{badRequest("Prénom et nom obligatoires.")}, nil
	}
	if err := s.checkPassword(b.Password); err != nil {
		return CreateUser400ApplicationProblemPlusJSONResponse{badRequest(err.Error())}, nil
	}
	st := s.db.Load()
	if _, exists, err := st.UserByUsername(ctx, username); err != nil {
		return nil, err
	} else if exists {
		return CreateUser400ApplicationProblemPlusJSONResponse{badRequest("Cet identifiant est déjà pris.")}, nil
	}
	hash, err := hashPassword(b.Password)
	if err != nil {
		return nil, err
	}
	u := store.User{Username: username, PasswordHash: hash, Role: string(b.Role), Email: trimmed(b.Email)}
	setNames(&u, &b.FirstName, &b.LastName)
	if err := st.CreateAccount(ctx, &u); err != nil {
		return nil, err
	}
	me, _ := userFrom(ctx)
	s.audit(ctx, me.Username, "user.create", "user:"+username, "rôle "+u.Role)
	return CreateUser201JSONResponse(s.userOut(u)), nil
}

func (s *Server) UpdateUser(ctx context.Context, req UpdateUserRequestObject) (UpdateUserResponseObject, error) {
	st := s.db.Load()
	u, ok, err := st.UserByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return UpdateUser404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Compte inconnu."))}, nil
	}
	if req.Body == nil {
		return UpdateUser400ApplicationProblemPlusJSONResponse{badRequest("Aucune modification reçue.")}, nil
	}
	b := req.Body
	if msg := checkProfile(b.FirstName, b.LastName, b.Email); msg != "" {
		return UpdateUser400ApplicationProblemPlusJSONResponse{badRequest(msg)}, nil
	}
	if b.Role != nil && !b.Role.Valid() {
		return UpdateUser400ApplicationProblemPlusJSONResponse{badRequest("Rôle inconnu.")}, nil
	}
	var changes []string
	wasAdmin := u.Role == "admin" && u.Active
	if b.FirstName != nil || b.LastName != nil {
		before := displayName(u)
		setNames(&u, b.FirstName, b.LastName)
		if after := displayName(u); after != before {
			changes = append(changes, "nom "+after)
		}
	}
	if b.Email != nil {
		u.Email = trimmed(b.Email)
	}
	if b.Role != nil && string(*b.Role) != u.Role {
		u.Role = string(*b.Role)
		changes = append(changes, "rôle "+u.Role)
	}
	if b.Active != nil && *b.Active != u.Active {
		u.Active = *b.Active
		changes = append(changes, map[bool]string{true: "réactivé", false: "désactivé"}[u.Active])
	}
	if wasAdmin && (u.Role != "admin" || !u.Active) {
		if n, err := st.CountActiveAdmins(ctx); err != nil {
			return nil, err
		} else if n <= 1 {
			return UpdateUser400ApplicationProblemPlusJSONResponse{badRequest("Il faut garder au moins un administrateur actif.")}, nil
		}
	}
	if b.Password != nil {
		if err := s.checkPassword(*b.Password); err != nil {
			return UpdateUser400ApplicationProblemPlusJSONResponse{badRequest(err.Error())}, nil
		}
		hash, err := hashPassword(*b.Password)
		if err != nil {
			return nil, err
		}
		if err := st.SetPassword(ctx, u.ID, hash); err != nil {
			return nil, err
		}
		changes = append(changes, "mot de passe réinitialisé")
	}
	if err := st.UpdateUser(ctx, &u); err != nil {
		return nil, err
	}
	if b.Password != nil || !u.Active {
		if _, err := st.DeleteSessions(ctx, u.ID, ""); err != nil {
			return nil, err
		}
	}
	me, _ := userFrom(ctx)
	s.audit(ctx, me.Username, "user.update", "user:"+u.Username, strings.Join(changes, ", "))
	return UpdateUser200JSONResponse(s.userOut(u)), nil
}

func (s *Server) DeleteUser(ctx context.Context, req DeleteUserRequestObject) (DeleteUserResponseObject, error) {
	st := s.db.Load()
	u, ok, err := st.UserByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return DeleteUser404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Compte inconnu."))}, nil
	}
	me, _ := userFrom(ctx)
	if u.ID == me.ID {
		return DeleteUser400ApplicationProblemPlusJSONResponse{badRequest("Vous ne pouvez pas supprimer votre propre compte.")}, nil
	}
	if u.Role == "admin" && u.Active {
		if n, err := st.CountActiveAdmins(ctx); err != nil {
			return nil, err
		} else if n <= 1 {
			return DeleteUser400ApplicationProblemPlusJSONResponse{badRequest("Il faut garder au moins un administrateur actif.")}, nil
		}
	}
	if err := st.DeleteUser(ctx, u.ID); err != nil {
		return nil, err
	}
	s.audit(ctx, me.Username, "user.delete", "user:"+u.Username, "")
	return DeleteUser204Response{}, nil
}

func (s *Server) RevokeUserSessions(ctx context.Context, req RevokeUserSessionsRequestObject) (RevokeUserSessionsResponseObject, error) {
	st := s.db.Load()
	u, ok, err := st.UserByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return RevokeUserSessions404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Compte inconnu."))}, nil
	}
	if _, err := st.DeleteSessions(ctx, u.ID, ""); err != nil {
		return nil, err
	}
	me, _ := userFrom(ctx)
	s.audit(ctx, me.Username, "user.sessions_revoked", "user:"+u.Username, "")
	return RevokeUserSessions204Response{}, nil
}

func (s *Server) UpdateProfile(ctx context.Context, req UpdateProfileRequestObject) (UpdateProfileResponseObject, error) {
	me, _ := userFrom(ctx)
	if req.Body == nil {
		return UpdateProfile400ApplicationProblemPlusJSONResponse{badRequest("Aucune modification reçue.")}, nil
	}
	if msg := checkProfile(req.Body.FirstName, req.Body.LastName, nil); msg != "" {
		return UpdateProfile400ApplicationProblemPlusJSONResponse{badRequest(msg)}, nil
	}
	if req.Body.FirstName != nil || req.Body.LastName != nil {
		setNames(&me, req.Body.FirstName, req.Body.LastName)
	}
	if req.Body.Language != nil {
		if !req.Body.Language.Valid() {
			return UpdateProfile400ApplicationProblemPlusJSONResponse{badRequest("Langue inconnue.")}, nil
		}
		me.Language = string(*req.Body.Language)
	}
	if err := s.db.Load().UpdateUser(ctx, &me); err != nil {
		return nil, err
	}
	return UpdateProfile200JSONResponse(s.userOut(me)), nil
}

func (s *Server) CloseOtherSessions(ctx context.Context, _ CloseOtherSessionsRequestObject) (CloseOtherSessionsResponseObject, error) {
	me, _ := userFrom(ctx)
	current, _ := ctx.Value(tokenKey).(string)
	n, err := s.db.Load().DeleteSessions(ctx, me.ID, current)
	if err != nil {
		return nil, err
	}
	s.audit(ctx, me.Username, "sessions.close_others", "user:"+me.Username, "")
	return CloseOtherSessions200JSONResponse{Closed: n}, nil
}
