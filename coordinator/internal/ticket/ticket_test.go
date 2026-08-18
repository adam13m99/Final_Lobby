package ticket_test

import (
	"errors"
	"net/netip"
	"testing"
	"time"

	"finallobby/coordinator/internal/ticket"
)

var t0 = time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)

func newStore() *ticket.Store { return ticket.NewStore() }

func claims() ticket.Claims {
	return ticket.Claims{
		PlayerID:  "p1",
		RoomID:    "room-a",
		VirtualIP: netip.MustParseAddr("10.87.0.2"),
	}
}

func TestIssuedTicketValidates(t *testing.T) {
	s := newStore()
	tok, err := s.Issue(claims(), t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(tok) < 32 {
		t.Fatalf("ticket is only %d characters; too little entropy to be unguessable", len(tok))
	}
	got, err := s.Validate(tok, t0.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if got.RoomID != "room-a" || got.VirtualIP.String() != "10.87.0.2" {
		t.Fatalf("claims round-tripped wrong: %+v", got)
	}
}

func TestUnknownTicketRejected(t *testing.T) {
	s := newStore()
	if _, err := s.Validate("not-a-real-ticket", t0); !errors.Is(err, ticket.ErrUnknown) {
		t.Fatalf("err = %v, want ErrUnknown", err)
	}
}

func TestExpiredTicketRejected(t *testing.T) {
	s := newStore()
	tok, _ := s.Issue(claims(), t0)
	if _, err := s.Validate(tok, t0.Add(ticket.Lifetime+time.Second)); !errors.Is(err, ticket.ErrExpired) {
		t.Fatalf("err = %v, want ErrExpired", err)
	}
}

func TestRevokeTakesEffectImmediately(t *testing.T) {
	s := newStore()
	tok, _ := s.Issue(claims(), t0)
	s.Revoke(tok)
	if _, err := s.Validate(tok, t0); !errors.Is(err, ticket.ErrUnknown) {
		t.Fatalf("err = %v, want the revoked ticket to be unknown", err)
	}
}

func TestRevokePlayerRoomKicksThatSessionOnly(t *testing.T) {
	s := newStore()
	kicked, _ := s.Issue(ticket.Claims{PlayerID: "p1", RoomID: "room-a",
		VirtualIP: netip.MustParseAddr("10.87.0.2")}, t0)
	other, _ := s.Issue(ticket.Claims{PlayerID: "p2", RoomID: "room-a",
		VirtualIP: netip.MustParseAddr("10.87.0.3")}, t0)
	elsewhere, _ := s.Issue(ticket.Claims{PlayerID: "p1", RoomID: "room-b",
		VirtualIP: netip.MustParseAddr("10.87.1.2")}, t0)

	s.RevokePlayerRoom("p1", "room-a")

	if _, err := s.Validate(kicked, t0); err == nil {
		t.Error("kicked player's ticket still valid")
	}
	if _, err := s.Validate(other, t0); err != nil {
		t.Errorf("another player in the same room was revoked too: %v", err)
	}
	if _, err := s.Validate(elsewhere, t0); err != nil {
		t.Errorf("the same player's ticket in a different room was revoked: %v", err)
	}
}

func TestRenewExtendsWithoutChangingTheTicket(t *testing.T) {
	s := newStore()
	tok, _ := s.Issue(claims(), t0)

	// Halfway through the lifetime the client renews, as the watchdog does.
	later := t0.Add(ticket.Lifetime / 2)
	if err := s.Renew(tok, later); err != nil {
		t.Fatal(err)
	}
	// It must now survive past the original expiry.
	if _, err := s.Validate(tok, t0.Add(ticket.Lifetime+time.Minute)); err != nil {
		t.Fatalf("renewed ticket expired anyway: %v", err)
	}
}

func TestPurgeRemovesOnlyExpired(t *testing.T) {
	s := newStore()
	old, _ := s.Issue(claims(), t0)
	fresh, _ := s.Issue(ticket.Claims{PlayerID: "p9", RoomID: "room-z",
		VirtualIP: netip.MustParseAddr("10.87.9.2")}, t0.Add(ticket.Lifetime))

	s.Purge(t0.Add(ticket.Lifetime + time.Second))

	if _, err := s.Validate(old, t0.Add(ticket.Lifetime+time.Second)); err == nil {
		t.Error("expired ticket survived the purge")
	}
	if _, err := s.Validate(fresh, t0.Add(ticket.Lifetime+time.Second)); err != nil {
		t.Errorf("live ticket was purged: %v", err)
	}
}

func TestTicketsAreUnique(t *testing.T) {
	s := newStore()
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		tok, err := s.Issue(claims(), t0)
		if err != nil {
			t.Fatal(err)
		}
		if seen[tok] {
			t.Fatal("issued a duplicate ticket")
		}
		seen[tok] = true
	}
}

func TestRevokeRoomClearsEveryoneInThatRoomOnly(t *testing.T) {
	s := newStore()
	a, _ := s.Issue(ticket.Claims{PlayerID: "p1", RoomID: "room-a",
		VirtualIP: netip.MustParseAddr("10.87.0.2")}, t0)
	b, _ := s.Issue(ticket.Claims{PlayerID: "p2", RoomID: "room-a",
		VirtualIP: netip.MustParseAddr("10.87.0.3")}, t0)
	other, _ := s.Issue(ticket.Claims{PlayerID: "p3", RoomID: "room-b",
		VirtualIP: netip.MustParseAddr("10.87.1.2")}, t0)

	s.RevokeRoom("room-a")

	for name, tok := range map[string]string{"p1": a, "p2": b} {
		if _, err := s.Validate(tok, t0); err == nil {
			t.Errorf("%s kept access after the room closed", name)
		}
	}
	if _, err := s.Validate(other, t0); err != nil {
		t.Errorf("a different room was revoked too: %v", err)
	}
}
