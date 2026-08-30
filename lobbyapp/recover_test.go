package main

import (
	"testing"
	"time"
)

// A tunnel that goes down mid-match used to stay down until somebody noticed a
// banner and pressed a button, on a screen they were not looking at because
// they were inside Dota (D77). It now comes back by itself - but only in the
// one case where coming back is right, and not oftener than once every thirty
// seconds.

// state builds the status reply for a player who is in a room and whose tunnel
// went down when the lease could not be renewed. The tests below change one
// thing about it at a time.
func state() map[string]any {
	return map[string]any{
		"service":          true,
		"connected":        false,
		"tunnel_error":     "lease expired locally: lease check unauthorised",
		"tunnel_error_key": "err.tunnel_lease",
		"room_id":          "r123",
		"room":             map[string]any{"id": "r123"},
	}
}

func TestALostLeaseReconnectsByItself(t *testing.T) {
	s := &server{}
	if !s.shouldRecover(state(), time.Now()) {
		t.Fatal("a player still seated in a room, with the service up and the " +
			"tunnel down after a lease expiry, was left disconnected")
	}
}

func TestRevocationIsNotRetried(t *testing.T) {
	s := &server{}
	out := state()
	out["tunnel_error"] = "authorisation revoked"
	out["tunnel_error_key"] = "err.tunnel_revoked"
	if s.shouldRecover(out, time.Now()) {
		t.Fatal("a kicked player's app tried to let itself back in")
	}
}

func TestNoRoomMeansNothingToReconnectTo(t *testing.T) {
	s := &server{}

	// pull() clears both of these the moment the coordinator stops saying
	// this player is seated, so an empty one here is the loop's own exit.
	out := state()
	out["room_id"] = ""
	if s.shouldRecover(out, time.Now()) {
		t.Error("reconnected to a room the player is no longer in")
	}

	out = state()
	out["room"] = nil
	if s.shouldRecover(out, time.Now()) {
		t.Error("reconnected while the coordinator had not confirmed the room")
	}
}

func TestAWorkingTunnelIsLeftAlone(t *testing.T) {
	s := &server{}
	out := state()
	out["connected"] = true
	if s.shouldRecover(out, time.Now()) {
		t.Fatal("tore down and rebuilt a tunnel that was working")
	}
}

func TestARetryHappensAtMostTwiceAMinute(t *testing.T) {
	s := &server{}
	now := time.Now()

	if !s.shouldRecover(state(), now) {
		t.Fatal("the first attempt was refused")
	}
	// Still in flight: the status poll runs every couple of seconds and must
	// not stack attempts on top of each other.
	if s.shouldRecover(state(), now.Add(2*time.Second)) {
		t.Fatal("a second attempt started while the first was still running")
	}

	s.recovering = false
	if s.shouldRecover(state(), now.Add(RecoverEvery-time.Second)) {
		t.Fatal("retried inside the throttle window")
	}
	if !s.shouldRecover(state(), now.Add(RecoverEvery+time.Second)) {
		t.Fatal("never retried again - one failed attempt would strand the player")
	}
}
