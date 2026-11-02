package httpapi

import (
	"context"
	"errors"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"docibis/internal/settings"
)

func problem(status int, detail string) Problem {
	return Problem{Status: status, Title: http.StatusText(status), Detail: &detail}
}

func badRequest(detail string) BadRequestApplicationProblemPlusJSONResponse {
	return BadRequestApplicationProblemPlusJSONResponse(problem(http.StatusBadRequest, detail))
}

func refused() UnauthorizedApplicationProblemPlusJSONResponse {
	return UnauthorizedApplicationProblemPlusJSONResponse(problem(http.StatusUnauthorized, "Identifiant ou mot de passe incorrect."))
}

func (s *Server) session(ctx context.Context) (Session, error) {
	cfg := s.cfg.Load()
	out := Session{Instance: cfg.String("instance_name")}
	if u, ok := userFrom(ctx); ok {
		out.Authenticated = true
		profile := s.userOut(u)
		out.User = &profile
		return out, nil
	}
	n, err := s.db.Load().CountUsers(ctx)
	out.SetupRequired = n == 0
	return out, err
}

func (s *Server) GetSession(ctx context.Context, _ GetSessionRequestObject) (GetSessionResponseObject, error) {
	out, err := s.session(ctx)
	return GetSession200JSONResponse(out), err
}

// signIn opens a session for an account and returns its cookie.
func (s *Server) signIn(ctx context.Context, userID int64) (SignedInJSONResponse, error) {
	token := newToken()
	hours := s.cfg.Load().Int("session_hours")
	agent, _ := ctx.Value(agentKey).(string)
	if err := s.db.Load().CreateSession(ctx, hashToken(token), userID, time.Now().Add(time.Duration(hours)*time.Hour), agent); err != nil {
		return SignedInJSONResponse{}, err
	}
	st := s.db.Load()
	u, _, err := st.UserByID(ctx, userID)
	if err != nil {
		return SignedInJSONResponse{}, err
	}
	if err := st.RecordLogin(ctx, userID); err != nil {
		s.logger.Warn("last sign-in not recorded", "error", err)
	}
	cookie := sessionCookie(ctx, token, hours*3600)
	profile := s.userOut(u)
	return SignedInJSONResponse{
		Body:    Session{Authenticated: true, Instance: s.cfg.Load().String("instance_name"), User: &profile},
		Headers: SignedInResponseHeaders{SetCookie: &cookie},
	}, nil
}

func tooMany(wait float64) TooManyAttemptsApplicationProblemPlusJSONResponse {
	secs := int(math.Ceil(wait))
	return TooManyAttemptsApplicationProblemPlusJSONResponse{
		Body:    problem(http.StatusTooManyRequests, "Trop d'essais. Réessayez dans quelques instants."),
		Headers: TooManyAttemptsResponseHeaders{RetryAfter: &secs},
	}
}

// checkPassword applies the password rules of the configuration.
func (s *Server) checkPassword(password string) error {
	minLen := s.cfg.Load().Int("password_min_length")
	switch {
	case len([]rune(password)) < minLen:
		return errors.New("Le mot de passe doit contenir au moins " + strconv.Itoa(minLen) + " caractères.")
	case len(password) > 72:
		return errors.New("Le mot de passe doit faire 72 octets au plus.")
	}
	return nil
}

func (s *Server) Setup(ctx context.Context, req SetupRequestObject) (SetupResponseObject, error) {
	st := s.db.Load()
	if n, err := st.CountUsers(ctx); err != nil {
		return nil, err
	} else if n > 0 {
		return Setup409ApplicationProblemPlusJSONResponse(problem(http.StatusConflict, "L'administrateur existe déjà : connectez-vous.")), nil
	}
	if wait := s.logins.wait("setup"); wait > 0 {
		return Setup429ApplicationProblemPlusJSONResponse{tooMany(wait.Seconds())}, nil
	}
	if req.Body == nil || !s.setup.Load().matches(req.Body.SetupCode) {
		s.logins.fail("setup", s.cfg.Load().Int("login_max_attempts"))
		return Setup400ApplicationProblemPlusJSONResponse{badRequest("Code d'installation incorrect. Il figure dans les journaux du service.")}, nil
	}
	username := strings.TrimSpace(req.Body.Username)
	if !usernamePattern.MatchString(username) {
		return Setup400ApplicationProblemPlusJSONResponse{badRequest("Identifiant : lettres, chiffres et . _ @ - seulement.")}, nil
	}
	if err := s.checkPassword(req.Body.Password); err != nil {
		return Setup400ApplicationProblemPlusJSONResponse{badRequest(err.Error())}, nil
	}
	hash, err := hashPassword(req.Body.Password)
	if err != nil {
		return nil, err
	}
	if err := st.CreateUser(ctx, username, hash, string(Admin)); err != nil {
		return nil, err
	}
	s.setup.Load().clear()
	s.logins.reset("setup")
	s.logger.Info("administrator created", "username", username)
	s.audit(ctx, username, "setup", "user:"+username, "")
	u, _, err := st.UserByUsername(ctx, username)
	if err != nil {
		return nil, err
	}
	out, err := s.signIn(ctx, u.ID)
	return Setup200JSONResponse{out}, err
}

func (s *Server) Login(ctx context.Context, req LoginRequestObject) (LoginResponseObject, error) {
	if req.Body == nil {
		return Login401ApplicationProblemPlusJSONResponse{refused()}, nil
	}
	username := strings.TrimSpace(req.Body.Username)
	key := "login:" + strings.ToLower(username)
	if wait := s.logins.wait(key); wait > 0 {
		return Login429ApplicationProblemPlusJSONResponse{tooMany(wait.Seconds())}, nil
	}
	u, found, err := s.db.Load().UserByLogin(ctx, username)
	if err != nil {
		return nil, err
	}
	if found {
		// The limit follows the account, whichever name it was reached by.
		key = "login:" + strings.ToLower(u.Username)
		if wait := s.logins.wait(key); wait > 0 {
			return Login429ApplicationProblemPlusJSONResponse{tooMany(wait.Seconds())}, nil
		}
	}
	hash := ""
	if found {
		hash = u.PasswordHash
	}
	if !passwordMatches(hash, req.Body.Password) {
		s.logins.fail(key, s.cfg.Load().Int("login_max_attempts"))
		s.logger.Warn("sign-in refused", "username", username)
		s.audit(ctx, "", "login.failed", "user:"+username, "")
		return Login401ApplicationProblemPlusJSONResponse{refused()}, nil
	}
	if !u.Active {
		s.audit(ctx, "", "login.failed", "user:"+username, "compte désactivé")
		return Login401ApplicationProblemPlusJSONResponse{UnauthorizedApplicationProblemPlusJSONResponse(problem(http.StatusUnauthorized, "Ce compte est désactivé : voyez avec un administrateur."))}, nil
	}
	if s.mfaRequired(u.Role) || u.TOTPEnabled {
		step, err := s.mfaStep(u)
		if err != nil {
			return nil, err
		}
		s.logger.Info("sign-in: password accepted, second factor asked", "username", u.Username, "step", step.Step)
		return Login202JSONResponse(step), nil
	}
	s.logins.reset(key)
	s.audit(ctx, u.Username, "login", "", "")
	out, err := s.signIn(ctx, u.ID)
	return Login200JSONResponse{out}, err
}

func (s *Server) Logout(ctx context.Context, _ LogoutRequestObject) (LogoutResponseObject, error) {
	if hash, ok := ctx.Value(tokenKey).(string); ok {
		if err := s.db.Load().DeleteSession(ctx, hash); err != nil {
			return nil, err
		}
	}
	if u, ok := userFrom(ctx); ok {
		s.audit(ctx, u.Username, "logout", "", "")
	}
	cookie := sessionCookie(ctx, "", -1)
	return Logout204Response{Headers: Logout204ResponseHeaders{SetCookie: &cookie}}, nil
}

func (s *Server) ChangePassword(ctx context.Context, req ChangePasswordRequestObject) (ChangePasswordResponseObject, error) {
	u, _ := userFrom(ctx)
	if req.Body == nil || !passwordMatches(u.PasswordHash, req.Body.CurrentPassword) {
		return ChangePassword400ApplicationProblemPlusJSONResponse{badRequest("Mot de passe actuel incorrect.")}, nil
	}
	if err := s.checkPassword(req.Body.NewPassword); err != nil {
		return ChangePassword400ApplicationProblemPlusJSONResponse{badRequest(err.Error())}, nil
	}
	hash, err := hashPassword(req.Body.NewPassword)
	if err != nil {
		return nil, err
	}
	if err := s.db.Load().SetPassword(ctx, u.ID, hash); err != nil {
		return nil, err
	}
	s.audit(ctx, u.Username, "password.change", "user:"+u.Username, "")
	return ChangePassword204Response{}, nil
}

func (s *Server) settingsPage() SettingsPage {
	all := s.cfg.Load().All()
	page := SettingsPage{Startup: s.startup}
	for _, g := range settings.Groups {
		group := SettingsGroup{Id: g.ID, Label: g.Label, Settings: []Setting{}}
		for _, v := range all {
			if v.Group != g.ID {
				continue
			}
			item := Setting{
				Key: v.Key, Label: v.Label, Kind: SettingKind(v.Kind), Value: v.Value, Default: v.Def.Default,
				Source: SettingSource(v.Source), Locked: v.Source == settings.FromEnvironment, Active: v.Active, Restart: v.Restart,
			}
			if v.Help != "" {
				item.Help = &v.Help
			}
			if v.Env != "" {
				item.Env = &v.Env
			}
			if len(v.Options) > 0 {
				item.Options = &v.Options
			}
			if v.Kind == settings.Number {
				item.Min, item.Max = &v.Min, &v.Max
			}
			group.Settings = append(group.Settings, item)
		}
		page.Groups = append(page.Groups, group)
	}
	return page
}

func (s *Server) GetSettings(context.Context, GetSettingsRequestObject) (GetSettingsResponseObject, error) {
	return GetSettings200JSONResponse(s.settingsPage()), nil
}

func (s *Server) UpdateSettings(ctx context.Context, req UpdateSettingsRequestObject) (UpdateSettingsResponseObject, error) {
	if req.Body == nil {
		return UpdateSettings400ApplicationProblemPlusJSONResponse{badRequest("Aucun réglage reçu.")}, nil
	}
	if err := s.cfg.Load().Update(ctx, s.db.Load(), req.Body.Values); err != nil {
		var invalid *settings.InvalidError
		if errors.As(err, &invalid) {
			return UpdateSettings400ApplicationProblemPlusJSONResponse{badRequest(err.Error())}, nil
		}
		return nil, err
	}
	u, _ := userFrom(ctx)
	keys := make([]string, 0, len(req.Body.Values))
	for k := range req.Body.Values {
		keys = append(keys, k)
	}
	s.logger.Info("settings changed", "username", u.Username, "keys", keys)
	sort.Strings(keys)
	s.audit(ctx, u.Username, "settings.update", "", strings.Join(keys, ", "))
	return UpdateSettings200JSONResponse(s.settingsPage()), nil
}
