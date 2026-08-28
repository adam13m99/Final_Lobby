package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"lobbybaz/coordinator/internal/account"
)

// Accounts over the wire (D37).
//
// Two things are worth stating before the handlers, because they are the
// reason this file exists rather than a `player_id` string in every request
// body:
//
//   - Until now a client asserted who it was. Anybody who could reach the API
//     could act as anybody else by typing their ID - kick from a room they did
//     not host, chat as them, take their seat. That was acceptable while the
//     only two clients were the owner's own PCs and is not acceptable with
//     real players.
//   - So when accounts are enabled, the session decides who you are and the
//     `player_id` in the body is ignored rather than trusted. Ignored, not
//     rejected: a mismatch is far more likely to be an old client than an
//     attack, and refusing it would break every installed copy on the day the
//     coordinator is upgraded.
//
// The header carries the session because a cookie would be wrong here - the
// client is a desktop application, not a browser, and there is no origin to
// scope a cookie to.
const sessionHeader = "X-LobbyBaz-Session"

// TermsVersion is the version of the terms and conditions this build asks
// people to accept. Bumping it re-prompts everybody, which is the point:
// consent to one text is not consent to a different one.
const TermsVersion = "2026-08-28"

type ctxKey int

const ctxAccount ctxKey = iota

// accountsOn reports whether this coordinator has an account database.
//
// It can run without one. The relay and room machinery predate accounts and
// the loadtest harness drives the API with generated IDs; making accounts
// mandatory everywhere would mean carrying a database into tests that have no
// use for one.
func (s *Server) accountsOn() bool { return s.accounts != nil }

// session pulls the signed-in account out of a request, if there is one.
func session(r *http.Request) (account.Account, bool) {
	a, ok := r.Context().Value(ctxAccount).(account.Account)
	return a, ok
}

// actor returns who a request is really from.
//
// With accounts enabled that is the session's account and nothing else. With
// accounts disabled it is whatever the body claimed, which is the test-phase
// behaviour every existing client depends on.
func (s *Server) actor(r *http.Request, claimed string) string {
	if a, ok := session(r); ok {
		return a.ID
	}
	return claimed
}

// authenticated wraps a handler so it runs only for a signed-in account. It is
// for the account endpoints themselves, which are meaningless without a
// database to sign in against.
func (s *Server) authenticated(l *Limiter, next http.HandlerFunc) http.HandlerFunc {
	return s.limited(l, func(w http.ResponseWriter, r *http.Request) {
		if !s.accountsOn() {
			writeErr(w, http.StatusServiceUnavailable, "this server has no accounts")
			return
		}
		if _, ok := session(r); !ok {
			writeErr(w, http.StatusUnauthorized, "sign in first")
			return
		}
		next(w, r)
	})
}

// signedIn wraps a handler that acts on somebody's behalf: creating a room,
// joining one, chatting, kicking.
//
// The distinction from authenticated is what happens on a coordinator with no
// account database. There, this is a no-op and the body's player_id is taken
// at face value - which is what the loadtest harness needs and what the
// two-PC test phase ran on. Where there IS a database, an unsigned request is
// refused outright rather than falling back, because falling back is exactly
// the hole accounts exist to close: without this, sending no session header at
// all would let anybody act as any ID they cared to type.
func (s *Server) signedIn(l *Limiter, next http.HandlerFunc) http.HandlerFunc {
	return s.limited(l, func(w http.ResponseWriter, r *http.Request) {
		if s.accountsOn() {
			if _, ok := session(r); !ok {
				writeErr(w, http.StatusUnauthorized, "sign in first")
				return
			}
		}
		next(w, r)
	})
}

// withSession resolves the session header and attaches the account. It never
// rejects: a request with no session is simply not signed in, and the handler
// or the guard above it decides whether that is allowed.
func (s *Server) withSession(r *http.Request) *http.Request {
	if !s.accountsOn() {
		return r
	}
	token := strings.TrimSpace(r.Header.Get(sessionHeader))
	if token == "" {
		return r
	}
	a, err := s.accounts.Resolve(token, s.now())
	if err != nil {
		return r
	}
	// Presence is a side effect of being seen, so a signed-in player shows as
	// online without the client having to say so.
	if _, err := s.players.Seen(a.ID, a.DisplayName, s.now()); err == nil {
		if a.MMR > 0 {
			_, _ = s.players.SetMMR(a.ID, a.MMR, s.now())
		}
	}
	return r.WithContext(context.WithValue(r.Context(), ctxAccount, a))
}

// --- endpoints ----------------------------------------------------------

type authView struct {
	PlayerID    string `json:"player_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	MMR         int    `json:"mmr"`
	Session     string `json:"session,omitempty"`
	// TermsVersion and TermsAccepted let the client decide whether to show
	// the terms screen without a second round trip.
	TermsVersion  string `json:"terms_version"`
	TermsAccepted bool   `json:"terms_accepted"`
	// CanRecover is false for every account today, and the sign-up screen
	// has to say so plainly rather than discover it at the reset link.
	CanRecover bool `json:"can_recover_password"`
}

func (s *Server) authView(a account.Account, token string) authView {
	v := authView{
		PlayerID:     a.ID,
		Username:     a.Username,
		DisplayName:  a.DisplayName,
		MMR:          a.MMR,
		Session:      token,
		TermsVersion: TermsVersion,
	}
	v.TermsAccepted, _ = s.accounts.HasAcceptedTerms(a.ID, TermsVersion)
	v.CanRecover, _ = s.accounts.CanRecoverPassword(a.ID)
	return v
}

func (s *Server) signUp(w http.ResponseWriter, r *http.Request) {
	if !s.accountsOn() {
		writeErr(w, http.StatusServiceUnavailable, "this server has no accounts")
		return
	}
	var body struct {
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		Password    string `json:"password"`
		Device      string `json:"device"`
		AcceptTerms string `json:"accept_terms_version"`
	}
	if !decode(w, r, &body) {
		return
	}
	// Consent is collected at install (a standing product rule), so an
	// account cannot come into existence without it. Refusing here rather
	// than creating the account and nagging afterwards keeps the record
	// honest: every account in the table agreed to a named version.
	if body.AcceptTerms != TermsVersion {
		writeErr(w, http.StatusBadRequest, "the terms must be accepted to create an account")
		return
	}
	if body.DisplayName == "" {
		body.DisplayName = body.Username
	}

	a, err := s.accounts.SignUp(body.Username, body.DisplayName, body.Password, s.now())
	if err != nil {
		writeErr(w, authStatus(err), err.Error())
		return
	}
	if err := s.accounts.AcceptTerms(a.ID, TermsVersion, s.now()); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not record your acceptance of the terms")
		return
	}

	token, err := s.accounts.StartSession(a.ID, deviceName(body.Device, r), s.now())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not start a session")
		return
	}
	_, _ = s.players.Seen(a.ID, a.DisplayName, s.now())
	s.log.Info("account created", "account", a.ID, "username", a.Username)
	writeJSON(w, http.StatusCreated, s.authView(a, token))
}

func (s *Server) signIn(w http.ResponseWriter, r *http.Request) {
	if !s.accountsOn() {
		writeErr(w, http.StatusServiceUnavailable, "this server has no accounts")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Device   string `json:"device"`
	}
	if !decode(w, r, &body) {
		return
	}

	a, err := s.accounts.Authenticate(body.Username, body.Password, s.now())
	if err != nil {
		// Deliberately unlogged at info level with the username attached:
		// a log of failed sign-ins is a log of guessed usernames.
		writeErr(w, authStatus(err), err.Error())
		return
	}
	token, err := s.accounts.StartSession(a.ID, deviceName(body.Device, r), s.now())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not start a session")
		return
	}
	_, _ = s.players.Seen(a.ID, a.DisplayName, s.now())
	if a.MMR > 0 {
		_, _ = s.players.SetMMR(a.ID, a.MMR, s.now())
	}
	writeJSON(w, http.StatusOK, s.authView(a, token))
}

func (s *Server) signOut(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.Header.Get(sessionHeader))
	if token != "" {
		_ = s.accounts.EndSession(token)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// whoami lets a client that kept a session across a restart find out whether
// it is still valid without joining anything.
func (s *Server) whoami(w http.ResponseWriter, r *http.Request) {
	a, _ := session(r)
	writeJSON(w, http.StatusOK, s.authView(a, ""))
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	a, _ := session(r)
	var body struct {
		Current string `json:"current_password"`
		Next    string `json:"new_password"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := s.accounts.ChangePassword(a.ID, body.Current, body.Next); err != nil {
		writeErr(w, authStatus(err), err.Error())
		return
	}
	// Every device was signed out, including this one. Hand back a session so
	// the person who just changed their password is not thrown to the login
	// screen for doing the right thing.
	token, err := s.accounts.StartSession(a.ID, deviceName("", r), s.now())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "password changed, but a new session could not be started - please sign in again")
		return
	}
	writeJSON(w, http.StatusOK, s.authView(a, token))
}

// acceptTerms records agreement to the current version. Signing up already
// does this; the endpoint exists for the day the text changes and people who
// already have accounts are asked again.
func (s *Server) acceptTerms(w http.ResponseWriter, r *http.Request) {
	a, _ := session(r)
	var body struct {
		Version string `json:"version"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Version != TermsVersion {
		writeErr(w, http.StatusBadRequest, "that is not the current version of the terms")
		return
	}
	if err := s.accounts.AcceptTerms(a.ID, TermsVersion, s.now()); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not record your acceptance")
		return
	}
	writeJSON(w, http.StatusOK, s.authView(a, ""))
}

// --- helpers ------------------------------------------------------------

func authStatus(err error) int {
	switch {
	case errors.Is(err, account.ErrUsernameTaken):
		return http.StatusConflict
	case errors.Is(err, account.ErrBadUsername),
		errors.Is(err, account.ErrBadPassword),
		errors.Is(err, account.ErrBadDisplayName):
		return http.StatusBadRequest
	case errors.Is(err, account.ErrPasswordMismatch),
		errors.Is(err, account.ErrSessionUnknown):
		return http.StatusUnauthorized
	case errors.Is(err, account.ErrDisabled):
		return http.StatusForbidden
	case errors.Is(err, account.ErrNoSuchAccount):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

// deviceName labels a session so somebody looking at their signed-in devices
// sees something they recognise. Whatever the client sends is preferred; the
// fallback is the user agent, trimmed to something that fits in a list.
func deviceName(claimed string, r *http.Request) string {
	name := strings.TrimSpace(claimed)
	if name == "" {
		name = strings.TrimSpace(r.Header.Get("User-Agent"))
	}
	if name == "" {
		name = "unknown device"
	}
	if len([]rune(name)) > 60 {
		name = string([]rune(name)[:60])
	}
	return name
}
