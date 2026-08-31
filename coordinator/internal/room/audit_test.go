package room_test

// The rest of the review the owner asked for: "review all the relations and
// logics and inspect the different flows, so bugs like this wont exist and
// are found."
//
// Each of these states one relation between two things that must hold across
// every path, rather than testing one function. They are here because the
// bug that prompted the review - one person, many rooms - was not a broken
// function either: every function was correct about the room it was given,
// and nothing was responsible for the sentence "a person is in one room".

import (
	"testing"
	"time"

	"lobbybaz/coordinator/internal/room"
)

// A host who hands the room to somebody else hands over a *playing* seat, and
// the room's memory of where its host sits has to move with it - both halves
// of it. HostSlot indexes either the teams or the gallery, and which one is
// HostWatching; leaving that behind means the room remembers a slot number
// against the wrong array.
//
// It matters on exactly one path and that path is the expensive one: a host
// whose PC dies comes back to "the seat they left", read from those two
// fields. Get them out of step and a new host who crashes is put in the
// gallery of the match they are running.
func TestHandingOverTheRoomMovesBothHalvesOfWhereTheHostSits(t *testing.T) {
	s := room.NewStore()
	now := time.Now()

	r, _, err := s.Create("alice", "alice's", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Join(r.ID, room.Anyone("bob"), now); err != nil {
		t.Fatal(err)
	}
	// Alice goes to watch her own room (D79), then hands it to Bob, who is
	// playing.
	if err := s.Move(r.ID, "alice", 0, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetHost(r.ID, "bob"); err != nil {
		t.Fatal(err)
	}

	got, err := s.Get(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	slot, kind, seated := got.SlotOf("bob")
	if !seated || kind != room.SeatPlayer {
		t.Fatalf("the new host is seated=%v as %v", seated, kind)
	}
	if got.HostWatching {
		t.Error("the room thinks its new host is watching; a host who crashes " +
			"would come back to the gallery of their own match")
	}
	if got.HostSlot != slot {
		t.Errorf("the room has its host in slot %d, they are in %d", got.HostSlot, slot)
	}
}

// A host kicking themselves is not a way to leave. Leave is, and it ends the
// room deliberately (D70); a self-kick would empty their seat, drop the
// address every client is connecting to, start the grace countdown and bar
// them from their own room for a minute - four things nobody asked for.
func TestAHostCannotKickThemselves(t *testing.T) {
	s := room.NewStore()
	now := time.Now()

	r, _, err := s.Create("alice", "alice's", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Join(r.ID, room.Anyone("bob"), now); err != nil {
		t.Fatal(err)
	}
	if err := s.Kick(r.ID, "alice", "alice", now); err == nil {
		t.Error("a host kicked themselves out of their own room")
	}
	got, _ := s.Get(r.ID)
	if _, _, seated := got.SlotOf("alice"); !seated {
		t.Error("the host lost their seat to their own kick")
	}
	if got.Status == room.StatusClosed {
		t.Error("a self-kick closed the room")
	}
}

// Whoever the room says is hosting must be somebody the room can be reached
// at. hostAddr derives the address every other client is told to connect to
// from the host's own address, so a host the room holds no address for is a
// room nobody can join - and that has to be impossible rather than merely
// unlikely.
func TestTheHostAlwaysHoldsAnAddress(t *testing.T) {
	s := room.NewStore()
	now := time.Now()

	r, _, err := s.Create("alice", "alice's", now)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"bob", "carol"} {
		if _, err := s.Join(r.ID, room.Anyone(id), now); err != nil {
			t.Fatal(err)
		}
	}

	check := func(what string) {
		t.Helper()
		got, err := s.Get(r.ID)
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if got.Status == room.StatusClosed {
			return
		}
		if _, ok := got.AddressOf(got.HostID); !ok {
			t.Fatalf("%s: the room's host holds no address; nobody can reach it", what)
		}
		m, err := s.Membership(r.ID, "bob")
		if err != nil {
			t.Fatalf("%s: a seated player has no membership: %v", what, err)
		}
		if !m.HostIP.IsValid() {
			t.Fatalf("%s: a player is told to connect to nothing", what)
		}
	}

	check("a fresh room")
	if err := s.Move(r.ID, "alice", 6, false); err != nil {
		t.Fatal(err)
	}
	check("after the host picked a side")
	if err := s.Move(r.ID, "alice", 1, true); err != nil {
		t.Fatal(err)
	}
	check("after the host went to watch")
	if err := s.Kick(r.ID, "alice", "carol", now); err != nil {
		t.Fatal(err)
	}
	check("after a kick")
	if _, err := s.SetHost(r.ID, "bob"); err != nil {
		t.Fatal(err)
	}
	check("after the room changed hands")
}

// Nobody holds two addresses, and no two people hold the same one, on any
// path that seats or unseats somebody. The address is what the ticket names
// and what the relay checks; two people on one address is two tunnels the
// relay cannot tell apart.
func TestNoAddressIsEverHeldTwice(t *testing.T) {
	s := room.NewStore()
	now := time.Now()

	r, _, err := s.Create("alice", "alice's", now)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{"b", "c", "d", "e", "f"}
	for _, id := range ids {
		if _, err := s.Join(r.ID, room.Anyone(id), now); err != nil {
			t.Fatal(err)
		}
	}

	check := func(what string) {
		t.Helper()
		got, _ := s.Get(r.ID)
		seen := map[int]string{}
		for _, id := range got.Occupants() {
			idx, ok := got.AddressOf(id)
			if !ok {
				t.Fatalf("%s: %s is seated with no address", what, id)
			}
			if other, clash := seen[idx]; clash {
				t.Fatalf("%s: %s and %s both hold address %d", what, id, other, idx)
			}
			seen[idx] = id
		}
	}

	check("everybody seated")
	// Every move that is possible, in both directions. A seat that is taken
	// refuses the move and that is correct; what is under test is that the
	// addresses hold whether the move happened or not.
	for i, id := range ids {
		for _, dest := range []struct {
			seat     int
			watching bool
		}{{i % 4, true}, {(i + 5) % 10, false}, {i % 2, true}} {
			if err := s.Move(r.ID, id, dest.seat, dest.watching); err != nil &&
				err != room.ErrSlotTaken {
				t.Fatalf("moving %s to %d (watching=%v): %v", id, dest.seat, dest.watching, err)
			}
			check("after " + id + " moved")
		}
	}
	if _, err := s.Leave(r.ID, "c", now); err != nil {
		t.Fatal(err)
	}
	check("after somebody left")
	if _, err := s.Join(r.ID, room.Anyone("g"), now); err != nil {
		t.Fatal(err)
	}
	check("after somebody new took the free address")
}
