package main

// Signing up and signing in, from the app's side (D37, D45, D53).
//
// The order these do things in matters. A coordinator with no database has no
// accounts at all, and that is what the live server is running today: the app
// has to work there exactly as it did before, with a typed name and nothing
// else. So the interface asks the server what it can do before it offers
// anything, and the sign-in screen simply does not appear on a server that
// cannot use it.
//
// Where accounts do exist, the session is what decides who a request is from -
// the player id in a request body is ignored (D53). It is saved to the session
// file so nobody types a password twice a day. The password itself is never
// written down, here or anywhere else.

import (
	"net/http"
	"os"
	"time"

	"lobbybaz/client/lobby"
	"lobbybaz/client/session"
)

// infoEvery is how often the app re-asks what the server supports. Rare on
// purpose: the answer changes when somebody redeploys the coordinator, not
// between two ticks of a clock.
const infoEvery = 2 * time.Minute

// serverCan is what this coordinator supports.
type serverCan struct {
	Accounts     bool   `json:"accounts"`
	Friends      bool   `json:"friends"`
	TermsVersion string `json:"terms_version"`
}

// capabilities folds what the server supports into the state reply, so the
// interface can decide whether to offer a sign-in screen at all.
func (s *server) capabilities(out map[string]any) {
	info, _ := s.infoCache.get(func() (*serverCan, error) {
		got, err := s.api().Info()
		if err != nil {
			return nil, err
		}
		return &serverCan{
			Accounts:     got.Accounts,
			Friends:      got.Friends,
			TermsVersion: got.TermsVersion,
		}, nil
	})
	if info == nil {
		return
	}
	out["accounts"] = info.Accounts
	out["friends_enabled"] = info.Friends
	out["terms_version"] = info.TermsVersion
}

// signUp creates an account and signs in with it.
//
// Accepting the terms is part of creating the account rather than a step
// after it, because that is what it is: the coordinator records the
// acceptance at the moment the account is made, so there is no state in which
// an account exists that has not accepted them.
func (s *server) signUp(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		Password    string `json:"password"`
		Terms       string `json:"terms_version"`
	}
	if !decode(w, r, &body) {
		return
	}
	acct, err := s.api().SignUp(body.Username, body.DisplayName, body.Password,
		deviceName(), body.Terms)
	if err != nil {
		fail(w, err.Error())
		return
	}
	s.adopt(acct.PlayerID, acct.Username, acct.DisplayName, acct.MMR, acct.Session)
	ok(w)
}

// changePassword is the only way somebody can change theirs.
//
// The sign-up screen says plainly that a forgotten password cannot be reset,
// which makes this the one lever a person has when they think somebody else
// knows it. The current password is required: a session left open on a shared
// PC must not be enough to lock the owner out of their own account.
func (s *server) changePassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current string `json:"current"`
		Next    string `json:"next"`
	}
	if !decode(w, r, &body) {
		return
	}
	acct, err := s.api().ChangePassword(body.Current, body.Next)
	if err != nil {
		fail(w, err.Error())
		return
	}
	// The coordinator ends every other session when a password changes, and
	// hands this one a new token. Storing it is what keeps this window signed
	// in through its own change.
	if acct != nil && acct.Session != "" {
		s.adopt(acct.PlayerID, acct.Username, acct.DisplayName, acct.MMR, acct.Session)
	}
	s.whoCache.forget()
	ok(w)
}

// acceptTerms records agreement to a new version of the terms.
//
// Terms that changed after somebody signed up are terms they have not agreed
// to. The coordinator keeps both facts - which version is current, and which
// one this account accepted - and this is how the second catches up with the
// first.
func (s *server) acceptTerms(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Version string `json:"version"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := s.api().AcceptTerms(body.Version); err != nil {
		fail(w, err.Error())
		return
	}
	s.whoCache.forget()
	ok(w)
}

// whoami folds what the coordinator knows about this account into the state
// reply. Only one thing here is not already known locally: whether the terms
// this account accepted are still the terms in force.
func (s *server) whoami(out map[string]any) {
	if signedIn, _ := out["signed_in"].(bool); !signedIn {
		return
	}
	acct, _ := s.whoCache.get(func() (*lobby.Account, error) {
		return s.api().Whoami()
	})
	if acct == nil {
		return
	}
	out["terms_accepted"] = acct.TermsAccepted
}

func (s *server) signIn(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	acct, err := s.api().SignIn(body.Username, body.Password, deviceName())
	if err != nil {
		fail(w, err.Error())
		return
	}
	s.adopt(acct.PlayerID, acct.Username, acct.DisplayName, acct.MMR, acct.Session)
	ok(w)
}

// signOut ends the session on the server as well as here.
//
// Forgetting the token locally without telling the coordinator would leave a
// session valid for another thirty days, on a machine somebody has just
// walked away from - which is the one case where signing out actually
// matters. If the server cannot be reached the token is forgotten anyway: a
// session this machine cannot end is not one it should keep using.
func (s *server) signOut(w http.ResponseWriter, r *http.Request) {
	err := s.api().SignOut()
	_ = s.update_(func(c *session.Config) {
		c.Session, c.Username = "", ""
		c.ClearRoom()
	})
	s.friendsCache.forget()
	if err != nil {
		fail(w, err.Error())
		return
	}
	ok(w)
}

// adopt records who this installation is now signed in as.
//
// The player id becomes the account id, which is the point of having accounts
// at all: the account is what rooms, kicks, friends and sanctions are keyed
// by. Leaving the installation's old random id in place would give one person
// two identities on the same PC.
func (s *server) adopt(playerID, username, displayName string, mmr int, token string) {
	_ = s.update_(func(c *session.Config) {
		c.PlayerID = playerID
		c.Username = username
		c.Nick = displayName
		c.MMR = mmr
		c.Session = token
	})
	s.friendsCache.forget()
}

// deviceName is what this machine will be called in the account's list of
// sessions, so somebody can tell one from another when signing the others out.
func deviceName() string {
	if host, err := os.Hostname(); err == nil && host != "" {
		return host
	}
	return "this PC"
}

// terms serves the agreement to the sign-up screen.
//
// Proxied through the app rather than fetched by the page directly so the
// page keeps talking to exactly one origin, and cached because it changes
// when somebody deploys.
func (s *server) terms(w http.ResponseWriter, r *http.Request) {
	got, err := s.termsCache.get(func() (*lobby.TermsOfUse, error) {
		return s.api().Terms()
	})
	if got == nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, got)
}
