package httpapi

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"image/png"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"docibis/internal/store"
)

// mfaTicket is a sign-in waiting for its code, or an authenticator waiting
// for its first code. It lives ten minutes, in memory: one task serves the
// console.
type mfaTicket struct {
	userID int64
	// enroll: the code confirms a new authenticator, whose secret is here.
	enroll bool
	secret string
	// profile: started from the profile page by a signed-in account.
	profile      bool
	passwordHash string
	expires      time.Time
	tries        int
}

// mfaState holds the tickets and the last code step used by each account:
// a code opens one session only.
type mfaState struct {
	mu       sync.Mutex
	tickets  map[string]*mfaTicket
	lastStep map[int64]int64
}

const mfaTicketLife = 10 * time.Minute

func (m *mfaState) put(t *mfaTicket) string {
	id := newToken()
	now := time.Now()
	t.expires = now.Add(mfaTicketLife)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tickets == nil {
		m.tickets = map[string]*mfaTicket{}
	}
	for k, old := range m.tickets {
		if now.After(old.expires) {
			delete(m.tickets, k)
		}
	}
	m.tickets[id] = t
	return id
}

func (m *mfaState) get(id string) *mfaTicket {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.tickets[id]
	if t == nil || time.Now().After(t.expires) {
		delete(m.tickets, id)
		return nil
	}
	copied := *t
	return &copied
}

// failed counts a wrong code; the ticket ends after five.
func (m *mfaState) failed(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t := m.tickets[id]; t != nil {
		if t.tries++; t.tries >= 5 {
			delete(m.tickets, id)
		}
	}
}

func (m *mfaState) drop(id string) {
	m.mu.Lock()
	delete(m.tickets, id)
	m.mu.Unlock()
}

// check accepts the current code of the authenticator and its neighbours
// (clock drift), once per account.
func (m *mfaState) check(userID int64, secret, code string) bool {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != 6 || secret == "" {
		return false
	}
	opts := totp.ValidateOpts{Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1}
	now := time.Now()
	for _, drift := range []int{0, -1, 1} {
		at := now.Add(time.Duration(drift) * 30 * time.Second)
		expected, err := totp.GenerateCodeCustom(secret, at, opts)
		if err != nil || subtle.ConstantTimeCompare([]byte(expected), []byte(code)) != 1 {
			continue
		}
		step := at.Unix() / 30
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.lastStep == nil {
			m.lastStep = map[int64]int64{}
		}
		if step <= m.lastStep[userID] {
			return false
		}
		m.lastStep[userID] = step
		return true
	}
	return false
}

// mfaRequired tells whether the role of an account must use a second factor
// (setting mfa_required_for).
func (s *Server) mfaRequired(role string) bool {
	switch s.cfg.Load().String("mfa_required_for") {
	case "all":
		return true
	case "admin":
		return role == string(Admin)
	}
	return false
}

// enrollment draws a new authenticator secret and returns what the console
// shows: QR code, and the secret for manual entry.
func (s *Server) enrollment(u store.User, profile bool) (MfaStep, error) {
	issuer := "DocIbis"
	if instance := s.cfg.Load().String("instance_name"); instance != "" {
		issuer += " " + instance
	}
	account := u.Email
	if account == "" {
		account = u.Username
	}
	key, err := totp.Generate(totp.GenerateOpts{Issuer: issuer, AccountName: account})
	if err != nil {
		return MfaStep{}, err
	}
	img, err := key.Image(240, 240)
	if err != nil {
		return MfaStep{}, err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return MfaStep{}, err
	}
	ticket := s.mfa.put(&mfaTicket{userID: u.ID, enroll: true, secret: key.Secret(), profile: profile, passwordHash: u.PasswordHash})
	secret, url := key.Secret(), key.URL()
	qr := "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
	return MfaStep{Step: Enroll, Ticket: ticket, Secret: &secret, OtpauthUrl: &url, QrPng: &qr}, nil
}

// mfaStep is the second step of a sign-in: the code of the authenticator,
// or its enrolment when the role requires one and the account has none.
func (s *Server) mfaStep(u store.User) (MfaStep, error) {
	if u.TOTPEnabled {
		return MfaStep{Step: Code, Ticket: s.mfa.put(&mfaTicket{userID: u.ID, passwordHash: u.PasswordHash})}, nil
	}
	return s.enrollment(u, false)
}

func gone() GoneApplicationProblemPlusJSONResponse {
	return GoneApplicationProblemPlusJSONResponse(problem(http.StatusGone, "La connexion a expiré : saisissez de nouveau votre mot de passe."))
}

const wrongCode = "Code incorrect. Saisissez le code à 6 chiffres affiché par l'application d'authentification."

func (s *Server) LoginMfa(ctx context.Context, req LoginMfaRequestObject) (LoginMfaResponseObject, error) {
	if req.Body == nil || req.Body.Ticket == nil {
		return LoginMfa410ApplicationProblemPlusJSONResponse{gone()}, nil
	}
	id := *req.Body.Ticket
	t := s.mfa.get(id)
	if t == nil || t.profile {
		return LoginMfa410ApplicationProblemPlusJSONResponse{gone()}, nil
	}
	st := s.db.Load()
	u, found, err := st.UserByID(ctx, t.userID)
	if err != nil {
		return nil, err
	}
	// A password changed or an account disabled since the first step ends it.
	if !found || !u.Active || u.PasswordHash != t.passwordHash || !t.enroll && !u.TOTPEnabled {
		s.mfa.drop(id)
		return LoginMfa410ApplicationProblemPlusJSONResponse{gone()}, nil
	}
	key := "login:" + strings.ToLower(u.Username)
	if wait := s.logins.wait(key); wait > 0 {
		return LoginMfa429ApplicationProblemPlusJSONResponse{tooMany(wait.Seconds())}, nil
	}
	secret := s.openSecret(u.TOTPSecret)
	if t.enroll {
		secret = t.secret
	}
	if !s.mfa.check(u.ID, secret, req.Body.Code) {
		s.mfa.failed(id)
		s.logins.fail(key, s.cfg.Load().Int("login_max_attempts"))
		s.logger.Warn("sign-in refused: wrong second-factor code", "username", u.Username, "enrolment", t.enroll)
		s.audit(ctx, "", "login.failed", "user:"+u.Username, "code de double authentification incorrect")
		return LoginMfa401ApplicationProblemPlusJSONResponse{UnauthorizedApplicationProblemPlusJSONResponse(problem(http.StatusUnauthorized, wrongCode))}, nil
	}
	s.mfa.drop(id)
	if t.enroll {
		sealed, err := s.sealSecret(t.secret)
		if err != nil {
			return nil, err
		}
		if err := st.SetTOTP(ctx, u.ID, sealed, true); err != nil {
			return nil, err
		}
		s.logger.Info("second factor enrolled at sign-in", "username", u.Username)
		s.audit(ctx, u.Username, "mfa.enable", "user:"+u.Username, "à la connexion")
	}
	s.logins.reset(key)
	s.audit(ctx, u.Username, "login", "", "double authentification")
	out, err := s.signIn(ctx, u.ID)
	return LoginMfa200JSONResponse{out}, err
}

func (s *Server) StartMfa(ctx context.Context, _ StartMfaRequestObject) (StartMfaResponseObject, error) {
	u, _ := userFrom(ctx)
	step, err := s.enrollment(u, true)
	if err != nil {
		return nil, err
	}
	return StartMfa200JSONResponse(step), nil
}

func (s *Server) ConfirmMfa(ctx context.Context, req ConfirmMfaRequestObject) (ConfirmMfaResponseObject, error) {
	u, _ := userFrom(ctx)
	if req.Body == nil || req.Body.Ticket == nil {
		return ConfirmMfa400ApplicationProblemPlusJSONResponse{badRequest("L'activation a expiré : recommencez.")}, nil
	}
	id := *req.Body.Ticket
	t := s.mfa.get(id)
	if t == nil || !t.profile || t.userID != u.ID {
		return ConfirmMfa400ApplicationProblemPlusJSONResponse{badRequest("L'activation a expiré : recommencez.")}, nil
	}
	if !s.mfa.check(u.ID, t.secret, req.Body.Code) {
		s.mfa.failed(id)
		return ConfirmMfa400ApplicationProblemPlusJSONResponse{badRequest(wrongCode)}, nil
	}
	s.mfa.drop(id)
	sealed, err := s.sealSecret(t.secret)
	if err != nil {
		return nil, err
	}
	if err := s.db.Load().SetTOTP(ctx, u.ID, sealed, true); err != nil {
		return nil, err
	}
	s.logger.Info("second factor enrolled", "username", u.Username)
	s.audit(ctx, u.Username, "mfa.enable", "user:"+u.Username, "")
	return ConfirmMfa204Response{}, nil
}

func (s *Server) DisableMfa(ctx context.Context, req DisableMfaRequestObject) (DisableMfaResponseObject, error) {
	u, _ := userFrom(ctx)
	if s.mfaRequired(u.Role) {
		return DisableMfa400ApplicationProblemPlusJSONResponse{badRequest("Votre rôle impose la double authentification : elle ne peut pas être retirée.")}, nil
	}
	if !u.TOTPEnabled {
		return DisableMfa204Response{}, nil
	}
	if req.Body == nil || !s.mfa.check(u.ID, s.openSecret(u.TOTPSecret), req.Body.Code) {
		return DisableMfa400ApplicationProblemPlusJSONResponse{badRequest(wrongCode)}, nil
	}
	if err := s.db.Load().SetTOTP(ctx, u.ID, "", false); err != nil {
		return nil, err
	}
	s.audit(ctx, u.Username, "mfa.disable", "user:"+u.Username, "")
	return DisableMfa204Response{}, nil
}

// ResetUserMfa removes the authenticator of an account (lost phone) and
// signs it out: it enrols a new one at its next sign-in.
func (s *Server) ResetUserMfa(ctx context.Context, req ResetUserMfaRequestObject) (ResetUserMfaResponseObject, error) {
	st := s.db.Load()
	target, found, err := st.UserByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if !found {
		return ResetUserMfa404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Compte inconnu."))}, nil
	}
	if err := st.SetTOTP(ctx, target.ID, "", false); err != nil {
		return nil, err
	}
	if _, err := st.DeleteSessions(ctx, target.ID, ""); err != nil {
		return nil, err
	}
	me, _ := userFrom(ctx)
	s.audit(ctx, me.Username, "mfa.reset", "user:"+target.Username, fmt.Sprintf("par %s", me.Username))
	return ResetUserMfa204Response{}, nil
}
