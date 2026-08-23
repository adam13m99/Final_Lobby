package lobby

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The session has to ride on every request, not just the account ones, or the
// coordinator has no way to know who a room call is from.
func TestEverySessionedRequestCarriesTheHeader(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get(SessionHeader))
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/auth/login":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"player_id": "a_1", "username": "reza", "session": "tok-123",
			})
		default:
			_, _ = w.Write([]byte(`{"rooms":[]}`))
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	if c.Session() != "" {
		t.Fatal("a fresh client already has a session")
	}
	if _, err := c.SignIn("reza", "a long enough password", "PC-1"); err != nil {
		t.Fatal(err)
	}
	if c.Session() != "tok-123" {
		t.Fatalf("session = %q, want tok-123", c.Session())
	}
	if _, err := c.ListRooms(); err != nil {
		t.Fatal(err)
	}

	if len(seen) != 2 {
		t.Fatalf("saw %d requests, want 2", len(seen))
	}
	if seen[0] != "" {
		t.Errorf("the login request carried a session it could not have had: %q", seen[0])
	}
	if seen[1] != "tok-123" {
		t.Errorf("the room list went out without the session: %q", seen[1])
	}
}

// Signing out must clear the token locally even when the server call fails,
// or an app that lost its connection keeps acting as somebody who asked to
// be signed out.
func TestSignOutClearsTheTokenEvenIfTheServerIsUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"boom"}`, http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	c.UseSession("tok-123")
	if err := c.SignOut(); err == nil {
		t.Fatal("expected the failure to be reported")
	}
	if c.Session() != "" {
		t.Fatalf("session = %q, want it cleared", c.Session())
	}
}

func TestSignUpSendsTheVersionThePersonWasShown(t *testing.T) {
	var got map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"player_id":"a_1","session":"tok","can_recover_password":false}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	a, err := c.SignUp("reza", "Reza", "a long enough password", "PC-1", "2026-08-24")
	if err != nil {
		t.Fatal(err)
	}
	if got["accept_terms_version"] != "2026-08-24" {
		t.Errorf("accept_terms_version = %q", got["accept_terms_version"])
	}
	if a.CanRecover {
		t.Error("the client claimed a password could be recovered")
	}
}

func TestABadSignInIsExplainedInPlainWords(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"account: username or password is wrong"}`))
	}))
	defer srv.Close()

	_, err := New(srv.URL, "").SignIn("reza", "wrong", "PC-1")
	if err == nil {
		t.Fatal("expected an error")
	}
	if err.Error() != "that username and password do not match" {
		t.Errorf("error = %q", err.Error())
	}
}
