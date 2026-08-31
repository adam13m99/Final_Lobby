package room_test

// One person, one room (D82).
//
// The owner's report: "i can create multiple rooms, which is not correct. a
// user can only make one room at a time. a user can only join one room at a
// time."
//
// Nothing in the store knew where a person was. Rooms knew who was in them
// and refused a second seat in the *same* room, but there was no question
// anybody could ask of the store as a whole, so opening a second room was a
// perfectly ordinary operation on a room that had never heard of the first.
//
// The damage is worse than a stray row in the lobby, and that is why these
// tests exist rather than a check in the interface: the coordinator decides a
// room is dead from whether its *host* is online, which is a fact about a
// person and not about a room. A host with two rooms is online for both. The
// one they walked away from never closes, sits in the lobby looking joinable,
// and takes players who then wait for somebody who is never coming.

import (
	"errors"
	"testing"
	"time"

	"lobbybaz/coordinator/internal/room"
)

func TestAPlayerCannotOpenASecondRoom(t *testing.T) {
	s := room.NewStore()
	now := time.Now()

	first, _, err := s.Create("alice", "first", now)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.Create("alice", "second", now)
	if !errors.Is(err, room.ErrAlreadyInAnotherRoom) {
		t.Fatalf("alice opened a second room while hosting %s (err = %v)", first.ID, err)
	}
	if n := len(s.List()); n != 1 {
		t.Errorf("the lobby holds %d rooms, want 1", n)
	}
}

func TestAPlayerCannotJoinASecondRoom(t *testing.T) {
	s := room.NewStore()
	now := time.Now()

	a, _, err := s.Create("alice", "alice's room", now)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := s.Create("bob", "bob's room", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Join(a.ID, room.Anyone("carol"), now); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Join(b.ID, room.Anyone("carol"), now); !errors.Is(err, room.ErrAlreadyInAnotherRoom) {
		t.Errorf("carol joined a second room as a player (err = %v)", err)
	}
	if _, err := s.JoinObserver(b.ID, room.Anyone("carol"), now); !errors.Is(err, room.ErrAlreadyInAnotherRoom) {
		t.Errorf("carol joined a second room to watch (err = %v)", err)
	}
	if _, err := s.JoinAdmin(b.ID, "carol", now); !errors.Is(err, room.ErrAlreadyInAnotherRoom) {
		t.Errorf("carol took a moderator seat in a second room (err = %v)", err)
	}
	// And a host cannot go and sit in somebody else's room either: their PC
	// is running a match for the people in theirs.
	if _, err := s.Join(b.ID, room.Anyone("alice"), now); !errors.Is(err, room.ErrAlreadyInAnotherRoom) {
		t.Errorf("a host joined another room while hosting one (err = %v)", err)
	}
}

// Refusing is only half of it. Somebody who has genuinely left must be able
// to open or join the next one immediately - a rule that outlives the thing
// it is about is worse than no rule.
func TestLeavingFreesYouToOpenAnother(t *testing.T) {
	s := room.NewStore()
	now := time.Now()

	first, _, err := s.Create("alice", "first", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Leave(first.ID, "alice", now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Create("alice", "second", now); err != nil {
		t.Fatalf("alice left her room and still could not open another: %v", err)
	}

	// The same for a player who leaves somebody else's room.
	other, _, err := s.Create("bob", "bob's", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Join(other.ID, room.Anyone("carol"), now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Leave(other.ID, "carol", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Join(other.ID, room.Anyone("carol"), now); err != nil {
		t.Fatalf("carol left and could not come back: %v", err)
	}
}

// A kicked player is out of that room, so nothing should stop them opening
// their own - the kick block is about that room, not about the platform.
func TestBeingKickedFreesYouToOpenYourOwn(t *testing.T) {
	s := room.NewStore()
	now := time.Now()

	r, _, err := s.Create("alice", "alice's", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Join(r.ID, room.Anyone("carol"), now); err != nil {
		t.Fatal(err)
	}
	if err := s.Kick(r.ID, "alice", "carol", now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Create("carol", "carol's", now); err != nil {
		t.Fatalf("a kicked player could not open their own room: %v", err)
	}
}

// A closed room holds nobody. Its members must not be pinned to it while it
// waits out the few minutes it is kept around for so people can read why it
// ended.
func TestAClosedRoomDoesNotHoldOnToItsPlayers(t *testing.T) {
	s := room.NewStore()
	now := time.Now()

	r, _, err := s.Create("alice", "alice's", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Join(r.ID, room.Anyone("carol"), now); err != nil {
		t.Fatal(err)
	}
	// The host leaving closes the room (D70), with everybody still in it.
	if _, err := s.Leave(r.ID, "alice", now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Create("carol", "carol's", now); err != nil {
		t.Fatalf("the room closed under carol and she could not open another: %v", err)
	}
}

// RoomOf is the question nothing could ask before, and it is what everything
// else here is built on.
func TestRoomOfSaysWhereSomebodyIs(t *testing.T) {
	s := room.NewStore()
	now := time.Now()

	if _, in := s.RoomOf("nobody"); in {
		t.Error("somebody who has never joined anything is reported as being in a room")
	}
	r, _, err := s.Create("alice", "alice's", now)
	if err != nil {
		t.Fatal(err)
	}
	if got, in := s.RoomOf("alice"); !in || got != r.ID {
		t.Errorf("the host is in %q (%v), want %s", got, in, r.ID)
	}
	if _, err := s.JoinObserver(r.ID, room.Anyone("watcher"), now); err != nil {
		t.Fatal(err)
	}
	if got, in := s.RoomOf("watcher"); !in || got != r.ID {
		t.Errorf("a watcher is in %q (%v), want %s", got, in, r.ID)
	}
	if _, err := s.JoinAdmin(r.ID, "mod", now); err != nil {
		t.Fatal(err)
	}
	if got, in := s.RoomOf("mod"); !in || got != r.ID {
		t.Errorf("a moderator is in %q (%v), want %s", got, in, r.ID)
	}
}

// A host whose PC dropped still holds their seat: SeeHost starts the grace
// timer and touches nothing else, which is what lets them come back to the
// address every other client is already sending to. So they are still in
// their room, and the rule says so.
//
// That is the right answer rather than a convenient one. The room is still
// alive and still theirs for the next minute; the thing they should be doing
// is going back to it, and the refusal names it so the app can take them
// there. What must not happen is the rule outliving the room - once the grace
// window closes the room does too, and they are free.
func TestAHostWhoseMachineDroppedIsStillInTheirRoom(t *testing.T) {
	s := room.NewStore()
	now := time.Now()

	r, _, err := s.Create("alice", "alice's", now)
	if err != nil {
		t.Fatal(err)
	}
	s.WatchHosts(func(string) room.HostFacts { return room.HostFacts{Online: false} })
	s.Tick(now)

	if _, _, err := s.Create("alice", "another", now); !errors.Is(err, room.ErrAlreadyInAnotherRoom) {
		t.Errorf("a host in their grace window opened a second room (err = %v)", err)
	}
	if got, in := s.RoomOf("alice"); !in || got != r.ID {
		t.Errorf("a host in their grace window is in %q (%v), want %s", got, in, r.ID)
	}

	// The window closes, the room closes with it, and they are free.
	s.Tick(now.Add(room.HostGracePeriod + time.Second))
	if _, in := s.RoomOf("alice"); in {
		t.Error("the room closed and its host is still held by it")
	}
	if _, _, err := s.Create("alice", "another", now); err != nil {
		t.Errorf("the room closed and its host still could not open another: %v", err)
	}
}
