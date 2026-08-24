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
