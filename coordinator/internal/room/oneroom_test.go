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
	"strings"
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

// **A moderator leaves the room they are in to go and moderate another** -
// the owner's answer, 2026-08-31 (D85).
//
// All three parts are here on purpose. Being refused while seated is the
// answer and not a bug: the staff seat exists so that a full match and a full
// gallery cannot keep a moderator out, not so that one person can be in two
// rooms. Leaving must release them at once. And the staff seat itself has to
// release them when they leave it, or a moderator is free exactly once and
// then stuck in the last room they were called to.
func TestAModeratorLeavesTheRoomTheyAreInToGoAndModerate(t *testing.T) {
	s := room.NewStore()
	now := time.Now()

	mine, _, err := s.Create("carol", "the game the mod is playing", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Join(mine.ID, room.Anyone("mod"), now); err != nil {
		t.Fatal(err)
	}
	trouble, _, err := s.Create("alice", "the room with trouble in it", now)
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.JoinAdmin(trouble.ID, "mod", now)
	if !errors.Is(err, room.ErrAlreadyInAnotherRoom) {
		t.Errorf("a moderator moderated one room while sitting in another (err = %v)", err)
	}
	// The refusal has to name the room they are in, or the app cannot offer
	// them the one thing that helps.
	if err == nil || !strings.Contains(err.Error(), mine.ID) {
		t.Errorf("the refusal was %v, and does not name %s", err, mine.ID)
	}

	if _, err := s.Leave(mine.ID, "mod", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.JoinAdmin(trouble.ID, "mod", now); err != nil {
		t.Fatalf("the moderator left their room and still could not moderate: %v", err)
	}

	// Done moderating. Getting up from the staff seat frees them like any
	// other seat does.
	if _, err := s.Leave(trouble.ID, "mod", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Join(mine.ID, room.Anyone("mod"), now); err != nil {
		t.Fatalf("the moderator left the staff seat and is still held by it: %v", err)
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

// A host whose PC dropped loses their room on the tick that notices, and is
// free on that same tick (D84).
//
// This used to be the other way round and the comment here argued for it:
// SeeHost started a one-minute countdown and touched nothing else, so the
// host kept their seat and their address and could walk back into the room
// they had dropped out of. The owner asked for the opposite - a host who is
// gone ends the room, with no grace - and this is the test that would have to
// change to put a grace back, which is the point of it.
//
// The pairing that matters is the two halves together: the room ends AND the
// person is released. A rule that outlives the room it names would leave
// somebody unable to open a room and unable to return to one.
func TestAHostWhoseMachineDroppedLosesTheirRoomAtOnce(t *testing.T) {
	s := room.NewStore()
	now := time.Now()

	r, _, err := s.Create("alice", "alice's", now)
	if err != nil {
		t.Fatal(err)
	}
	s.WatchHosts(func(string) room.HostFacts { return room.HostFacts{Online: false} })
	closed := s.Tick(now)

	if len(closed) != 1 || closed[0] != r.ID {
		t.Errorf("the tick reported %v closed, want just %s", closed, r.ID)
	}
	got, err := s.Get(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != room.StatusClosed {
		t.Errorf("the host dropped and the room is %q, want closed", got.Status)
	}
	// Kept, not swept: the people who were in it have to be able to read why
	// it ended.
	if got.ClosedAt.IsZero() {
		t.Error("the room closed without recording when")
	}

	if _, in := s.RoomOf("alice"); in {
		t.Error("the room closed and its host is still held by it")
	}
	if _, _, err := s.Create("alice", "another", now); err != nil {
		t.Errorf("the room closed and its host still could not open another: %v", err)
	}
}
