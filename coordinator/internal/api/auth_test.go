package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lobbybaz/coordinator/internal/account"
	"lobbybaz/coordinator/internal/chat"
	"lobbybaz/coordinator/internal/moderation"
	"lobbybaz/coordinator/internal/player"
	"lobbybaz/coordinator/internal/room"
	"lobbybaz/coordinator/internal/social"
	"lobbybaz/coordinator/internal/store"
	"lobbybaz/coordinator/internal/ticket"
)

// authRig is a coordinator with accounts switched on.
type authRig struct {
	t   *testing.T
	srv http.Handler
	acc *account.Store
	soc *social.Store
	mod *moderation.Store
	// players is the live registry, so a test can forget somebody the way a
	// restart would and see what the database alone can still answer.
	players *player.Registry
	// friends is swappable so a test can install a graph after signing
	// people up - their IDs are not known until then.
	friends *swappableFriends
}

// swappableFriends lets a test install a friend graph after the server is
// built. The real one arrives in T7.
type swappableFriends struct{ inner Friends }

func (f *swappableFriends) AreFriends(a, b string) (bool, error) {
	if f.inner == nil {
		return false, nil
	}
	return f.inner.AreFriends(a, b)
}

func (g *authRig) setFriends(f Friends) { g.friends.inner = f }

func newAuthRig(t *testing.T) *authRig {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	acc := account.New(db)
	soc := social.New(db)
	mod := moderation.New(db)
	kicks := store.NewKicks(db)
	players := player.NewRegistry()
	friends := &swappableFriends{}
	s := New(Config{
		Friends:    friends,
		Rooms:      room.NewStore(),
		Tickets:    ticket.NewStore(),
		Players:    players,
		Chat:       chat.NewBoard(),
		Accounts:   acc,
		Social:     soc,
		Moderation: mod,
		Kicks:      kicks,
		RelayAddr:  "127.0.0.1:443",
		RelayPub:   "00",
		Now:        func() time.Time { return time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC) },
	})
	return &authRig{t: t, srv: s.Routes(), acc: acc, soc: soc, mod: mod, players: players, friends: friends}
}

func (g *authRig) do(method, path, session string, body any) (*httptest.ResponseRecorder, map[string]any) {
	g.t.Helper()
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			g.t.Fatal(err)
		}
		rdr = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	if session != "" {
		req.Header.Set(sessionHeader, session)
	}
	rec := httptest.NewRecorder()
	g.srv.ServeHTTP(rec, req)

	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

// register signs somebody up and returns their account ID and session token.
func (g *authRig) register(username, password string) (string, string) {
	g.t.Helper()
	rec, out := g.do(http.MethodPost, "/v1/auth/signup", "", map[string]any{
		"username":             username,
		"display_name":         username,
		"password":             password,
		"accept_terms_version": TermsVersion,
	})
	if rec.Code != http.StatusCreated {
		g.t.Fatalf("signup: %d %s", rec.Code, rec.Body.String())
	}
	id, _ := out["player_id"].(string)
	token, _ := out["session"].(string)
	if id == "" || token == "" {
		g.t.Fatalf("signup gave back id=%q session=%q", id, token)
	}
	return id, token
}

func TestSignUpAndSignInOverHTTP(t *testing.T) {
	g := newAuthRig(t)
	id, _ := g.register("reza", "a long enough password")

	rec, out := g.do(http.MethodPost, "/v1/auth/login", "", map[string]any{
		"username": "reza",
		"password": "a long enough password",
		"device":   "PC-1",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("login: %d %s", rec.Code, rec.Body.String())
	}
	if out["player_id"] != id {
		t.Errorf("login returned %v, want %s", out["player_id"], id)
	}
	if out["terms_accepted"] != true {
		t.Error("terms acceptance from signup did not carry over to login")
	}
	// The signup screen has to tell people the truth about recovery before
	// they choose a password, not after they forget it (D37).
	if out["can_recover_password"] != false {
		t.Error("an account with no verified contact was told it could recover its password")
	}
}

func TestSignUpRefusesWithoutAcceptingTheTerms(t *testing.T) {
	g := newAuthRig(t)
	rec, _ := g.do(http.MethodPost, "/v1/auth/signup", "", map[string]any{
		"username": "reza", "display_name": "Reza", "password": "a long enough password",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", rec.Code)
	}
	// And no account was created by the attempt.
	if _, err := g.acc.Authenticate("reza", "a long enough password", time.Now()); err == nil {
		t.Fatal("an account exists that never accepted the terms")
	}
}

func TestSignUpRejectsATakenUsername(t *testing.T) {
	g := newAuthRig(t)
	g.register("reza", "a long enough password")
	rec, _ := g.do(http.MethodPost, "/v1/auth/signup", "", map[string]any{
		"username": "REZA", "display_name": "Someone", "password": "a long enough password",
		"accept_terms_version": TermsVersion,
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("got %d, want 409", rec.Code)
	}
}

func TestSignInWithTheWrongPasswordIsRefused(t *testing.T) {
	g := newAuthRig(t)
	g.register("reza", "a long enough password")
	rec, out := g.do(http.MethodPost, "/v1/auth/login", "", map[string]any{
		"username": "reza", "password": "a guess",
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", rec.Code)
	}
	// Nothing in the reply may hint that the username was real.
	if body, _ := out["error"].(string); body == "" {
		t.Error("no error message at all")
	}
}

// This is the whole point of accounts. Before them, a request said who it was
// and the coordinator believed it.
func TestTheSessionDecidesWhoYouAreNotTheRequestBody(t *testing.T) {
	g := newAuthRig(t)
	victim, _ := g.register("victim", "a long enough password")
	_, impostorSession := g.register("impostor", "a long enough password")

	// The impostor creates a room while claiming to be the victim.
	rec, out := g.do(http.MethodPost, "/v1/rooms", impostorSession, map[string]any{
		"player_id": victim,
		"nick":      "victim",
		"name":      "not yours",
	})
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("create room: %d %s", rec.Code, rec.Body.String())
	}
	if got := out["player_id"]; got == victim {
		t.Fatalf("the coordinator accepted a forged player_id: room host is %v", got)
	}

	// And the room really is hosted by the impostor.
	_, rooms := g.do(http.MethodGet, "/v1/rooms", impostorSession, nil)
	list, _ := rooms["rooms"].([]any)
	if len(list) != 1 {
		t.Fatalf("rooms = %d, want 1", len(list))
	}
	first, _ := list[0].(map[string]any)
	if first["host_id"] == victim {
		t.Fatal("the victim is hosting a room they never created")
	}
}

func TestRoomActionsRequireASignedInAccount(t *testing.T) {
	g := newAuthRig(t)
	// No session header at all. With accounts enabled there is nobody to be.
	rec, _ := g.do(http.MethodPost, "/v1/rooms", "", map[string]any{
		"player_id": "a_madeup", "nick": "ghost", "name": "room",
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401 - an unsigned request must not act as a made-up ID", rec.Code)
	}

	// Browsing, though, is open: D45 wants a new player to see the lobby
	// before being asked for a password.
	if rec, _ := g.do(http.MethodGet, "/v1/rooms", "", nil); rec.Code != http.StatusOK {
		t.Errorf("listing rooms without an account gave %d, want 200", rec.Code)
	}
}

func TestWhoamiNeedsAValidSession(t *testing.T) {
	g := newAuthRig(t)
	_, token := g.register("reza", "a long enough password")

	rec, out := g.do(http.MethodGet, "/v1/auth/me", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	if out["username"] != "reza" {
		t.Errorf("username = %v", out["username"])
	}
	// A session that never existed is refused, and so is one that has been
	// signed out.
	if rec, _ := g.do(http.MethodGet, "/v1/auth/me", "not-a-real-token", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("a made-up session gave %d, want 401", rec.Code)
	}
	if rec, _ := g.do(http.MethodPost, "/v1/auth/logout", token, nil); rec.Code != http.StatusOK {
		t.Fatalf("logout gave %d", rec.Code)
	}
	if rec, _ := g.do(http.MethodGet, "/v1/auth/me", token, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("a signed-out session still works: %d", rec.Code)
	}
}

func TestChangingAPasswordHandsBackAWorkingSession(t *testing.T) {
	g := newAuthRig(t)
	_, token := g.register("reza", "a long enough password")

	rec, out := g.do(http.MethodPost, "/v1/auth/password", token, map[string]any{
		"current_password": "a long enough password",
		"new_password":     "a different long password",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	fresh, _ := out["session"].(string)
	if fresh == "" || fresh == token {
		t.Fatal("no new session was issued")
	}
	if rec, _ := g.do(http.MethodGet, "/v1/auth/me", token, nil); rec.Code != http.StatusUnauthorized {
		t.Error("the old session survived a password change")
	}
	if rec, _ := g.do(http.MethodGet, "/v1/auth/me", fresh, nil); rec.Code != http.StatusOK {
		t.Error("the new session does not work")
	}
}

// A declared MMR is a number the player may only change once a week, so it
// has to outlive the process. Before accounts it lived in a map.
func TestDeclaredMMRIsWrittenToTheDatabase(t *testing.T) {
	g := newAuthRig(t)
	id, token := g.register("reza", "a long enough password")

	rec, _ := g.do(http.MethodPost, "/v1/me", token, map[string]any{"mmr": 4200})
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	stored, err := g.acc.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if stored.MMR != 4200 {
		t.Fatalf("stored MMR = %d, want 4200", stored.MMR)
	}

	// The once-a-week rule is now enforced against something durable.
	rec, _ = g.do(http.MethodPost, "/v1/me", token, map[string]any{"mmr": 9000})
	if rec.Code != http.StatusConflict {
		t.Fatalf("second change gave %d, want 409", rec.Code)
	}
}

func TestABannedAccountLosesItsSessionImmediately(t *testing.T) {
	g := newAuthRig(t)
	id, token := g.register("pest", "a long enough password")

	if err := g.acc.SetDisabled(id, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	if rec, _ := g.do(http.MethodGet, "/v1/auth/me", token, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("a banned account kept working: %d", rec.Code)
	}
	rec, _ := g.do(http.MethodPost, "/v1/auth/login", "", map[string]any{
		"username": "pest", "password": "a long enough password",
	})
	if rec.Code != http.StatusForbidden {
		t.Errorf("a banned account signed back in: %d", rec.Code)
	}
}

// newPlainRig is a coordinator with no database at all: no accounts, no
// friends, no moderation. The loadtest harness runs it this way.
func newPlainRig(t *testing.T) *authRig {
	t.Helper()
	s := New(Config{
		Rooms:     room.NewStore(),
		Tickets:   ticket.NewStore(),
		Players:   player.NewRegistry(),
		Chat:      chat.NewBoard(),
		RelayAddr: "127.0.0.1:443",
		RelayPub:  "00",
	})
	return &authRig{t: t, srv: s.Routes(), friends: &swappableFriends{}}
}

// The coordinator still has to run without a database - the loadtest harness
// drives it with generated IDs and has no accounts to sign in with.
func TestACoordinatorWithoutAccountsStillWorks(t *testing.T) {
	g := newPlainRig(t)

	rec, _ := g.do(http.MethodPost, "/v1/rooms", "", map[string]any{
		"player_id": "p_test", "nick": "tester", "name": "room",
	})
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("creating a room without accounts gave %d %s", rec.Code, rec.Body.String())
	}
	if rec, _ := g.do(http.MethodPost, "/v1/auth/login", "", map[string]any{
		"username": "x", "password": "y",
	}); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("signing in to an accountless server gave %d, want 503", rec.Code)
	}
}

// The Windows service holds a ticket and the shared bearer token. It has
// never held a session and cannot: sessions are the desktop app's, and the
// service outlives it and runs as LocalSystem.
//
// This test exists because the one that already covered renewal ran on a
// coordinator with no account database, where signedIn is a deliberate no-op.
// It was green for the whole time production was answering the service 401,
// the watchdog was reading that as "cannot tell", and every match was ending
// three minutes after it started (D77).
//
// So: accounts on, no session header, exactly what the service sends.
func TestTheServiceRenewsALeaseWithoutASession(t *testing.T) {
	g := newAuthRig(t)
	_, session := g.register("reza", "a long enough password")

	rec, room := g.do(http.MethodPost, "/v1/rooms", session, map[string]any{"name": "Ranked"})
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("create room: %d %s", rec.Code, rec.Body.String())
	}
	tok, _ := room["ticket"].(string)
	if tok == "" {
		t.Fatal("hosting a room gave back no ticket")
	}

	rec, out := g.do(http.MethodPost, "/v1/lease/renew", "", map[string]any{"ticket": tok})
	if rec.Code != http.StatusOK {
		t.Fatalf("renew without a session gave %d %s - the service cannot send one, "+
			"so this is every player dropped three minutes into every match",
			rec.Code, rec.Body.String())
	}
	if out["valid"] != true {
		t.Fatalf("renew answered %v, want valid:true", out)
	}
}

// A ticket that is not ours is still refused, session or no session. The
// ticket is the credential; nothing else is being trusted here.
func TestRenewingAnUnknownTicketIsRefused(t *testing.T) {
	g := newAuthRig(t)
	rec, out := g.do(http.MethodPost, "/v1/lease/renew", "", map[string]any{
		"ticket": "not-a-ticket-anybody-ever-issued",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 with valid:false - the watchdog reads any "+
			"other status as \"cannot tell\" and waits three minutes", rec.Code)
	}
	if out["valid"] != false {
		t.Fatalf("an unknown ticket renewed: %v", out)
	}
}

// Revocation still ends a lease at once. This is the property signedIn was
// never providing and the ticket table always was.
func TestRevokingARoomEndsItsLeasesImmediately(t *testing.T) {
	g := newAuthRig(t)
	_, session := g.register("reza", "a long enough password")

	rec, room := g.do(http.MethodPost, "/v1/rooms", session, map[string]any{"name": "Ranked"})
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("create room: %d %s", rec.Code, rec.Body.String())
	}
	tok, _ := room["ticket"].(string)
	id, _ := room["room_id"].(string)

	if _, out := g.do(http.MethodPost, "/v1/lease/renew", "", map[string]any{"ticket": tok}); out["valid"] != true {
		t.Fatal("a fresh lease did not renew")
	}

	// Leaving closes the room, which revokes every ticket in it (D70).
	g.do(http.MethodPost, "/v1/rooms/"+id+"/leave", session, map[string]any{})

	_, out := g.do(http.MethodPost, "/v1/lease/renew", "", map[string]any{"ticket": tok})
	if out["valid"] != false {
		t.Fatalf("a revoked ticket still renews: %v - the watchdog would never tear down", out)
	}
}
