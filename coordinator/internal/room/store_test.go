package room_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"lobbybaz/coordinator/internal/room"
)

func TestCreateSeatsHostAtTheDeterministicAddress(t *testing.T) {
	s := room.NewStore()
	r, m, err := s.Create("host-1", "Test Room", t0)
	if err != nil {
		t.Fatal(err)
	}
	if m.Slot != 0 || !m.IsHost {
		t.Fatalf("host got slot %d, isHost %v", m.Slot, m.IsHost)
	}
	if m.VirtualIP != m.HostIP {
		t.Fatalf("host address %s != advertised host address %s", m.VirtualIP, m.HostIP)
	}
	if !m.Subnet.Contains(m.VirtualIP) {
		t.Fatalf("%s is outside the room subnet %s", m.VirtualIP, m.Subnet)
	}
	if r.Name != "Test Room" {
		t.Errorf("name = %q", r.Name)
	}
}

func TestRoomsGetDistinctSubnets(t *testing.T) {
	s := room.NewStore()
	_, a, _ := s.Create("h1", "A", t0)
	_, b, _ := s.Create("h2", "B", t0)
	if a.Subnet == b.Subnet {
		t.Fatalf("two rooms share subnet %s", a.Subnet)
	}
	if a.Subnet.Contains(b.VirtualIP) {
		t.Fatal("one room's subnet contains another room's address")
	}
}

func TestJoinerLearnsTheHostAddress(t *testing.T) {
	s := room.NewStore()
	_, hostM, _ := s.Create("h1", "A", t0)
	m, err := s.Join(hostM.RoomID, room.Anyone("p2"), t0)
	if err != nil {
		t.Fatal(err)
	}
	if m.HostIP != hostM.VirtualIP {
		t.Fatalf("joiner told host is %s, host actually at %s", m.HostIP, hostM.VirtualIP)
	}
	if m.IsHost {
		t.Error("joiner marked as host")
	}
	if m.Slot != 1 {
		t.Errorf("slot = %d, want 1", m.Slot)
	}
}

func TestJoinUnknownRoom(t *testing.T) {
	s := room.NewStore()
	if _, err := s.Join("nope", room.Anyone("p"), t0); !errors.Is(err, room.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestListHidesClosedRooms(t *testing.T) {
	s := room.NewStore()
	_, m, _ := s.Create("h1", "A", t0)
	if len(s.List()) != 1 {
		t.Fatalf("expected 1 room listed")
	}
	if err := s.SetStatus(m.RoomID, "h1", room.StatusClosed, t0); err != nil {
		t.Fatal(err)
	}
	if got := len(s.List()); got != 0 {
		t.Fatalf("closed room still listed (%d)", got)
	}
}

// A host who stopped answering is the case the grace period was written for
// (D40, D70). Nobody pressed anything: the store notices on its own tick,
// starts the countdown, and reports the closure once when it expires.
func TestTickReportsRoomsThatJustClosed(t *testing.T) {
	s := room.NewStore()
	_, m, _ := s.Create("h1", "A", t0)
	_, _ = s.Join(m.RoomID, room.Anyone("p2"), t0)
	s.WatchHosts(func(string) room.HostFacts { return room.HostFacts{} })

	if closed := s.Tick(t0.Add(time.Second)); len(closed) != 0 {
		t.Fatal("room closed the instant its host went quiet")
	}
	if closed := s.Tick(t0.Add(time.Minute)); len(closed) != 0 {
		t.Fatal("room closed before the grace period expired")
	}
	closed := s.Tick(t0.Add(3 * time.Minute))
	if len(closed) != 1 || closed[0] != m.RoomID {
		t.Fatalf("closed = %v, want [%s]", closed, m.RoomID)
	}
	// Reporting it twice would revoke tickets for an already-dead room over
	// and over.
	if again := s.Tick(t0.Add(4 * time.Minute)); len(again) != 0 {
		t.Fatalf("room reported closed twice: %v", again)
	}
}

func TestClosedRoomIndexIsReused(t *testing.T) {
	s := room.NewStore()
	_, first, _ := s.Create("h1", "A", t0)
	_, _ = s.Leave(first.RoomID, "h1", t0)
	s.Tick(t0.Add(3 * time.Minute))
	s.Tick(t0.Add(30 * time.Minute)) // past the linger window

	_, second, err := s.Create("h2", "B", t0.Add(31*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if second.Subnet != first.Subnet {
		t.Fatalf("second room got %s; the freed index %s should have been reused",
			second.Subnet, first.Subnet)
	}
}

// A player sitting in a room must be able to ask for their addressing again
// without leaving. The ticket minted at join dies after ten minutes, so
// before this existed the only way to connect to a room you had been waiting
// in was to leave it and join again.
func TestMembershipForSeatedPlayer(t *testing.T) {
	s := room.NewStore()
	_, host, err := s.Create("h1", "A", t0)
	if err != nil {
		t.Fatal(err)
	}
	joined, err := s.Join(host.RoomID, room.Anyone("p2"), t0)
	if err != nil {
		t.Fatal(err)
	}

	// Much later - well past any ticket lifetime - the same seat is returned.
	again, err := s.Membership(host.RoomID, "p2")
	if err != nil {
		t.Fatalf("a seated player was refused their own membership: %v", err)
	}
	if again.VirtualIP != joined.VirtualIP || again.Slot != joined.Slot {
		t.Fatalf("membership changed: got slot %d %s, want slot %d %s",
			again.Slot, again.VirtualIP, joined.Slot, joined.VirtualIP)
	}
	if again.IsHost {
		t.Fatal("a joining player was reported as the host")
	}

	h, err := s.Membership(host.RoomID, "h1")
	if err != nil {
		t.Fatal(err)
	}
	if !h.IsHost {
		t.Fatal("the host was not reported as the host")
	}
}

func TestMembershipRefusesOutsiders(t *testing.T) {
	s := room.NewStore()
	_, host, _ := s.Create("h1", "A", t0)

	if _, err := s.Membership(host.RoomID, "stranger"); !errors.Is(err, room.ErrNotMember) {
		t.Fatalf("a stranger was given room addressing: %v", err)
	}
	if _, err := s.Membership("r-nope", "h1"); !errors.Is(err, room.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}

	// A player who left must not keep their address.
	_, _ = s.Leave(host.RoomID, "h1", t0)
	if _, err := s.Membership(host.RoomID, "h1"); !errors.Is(err, room.ErrNotMember) {
		t.Fatalf("a departed player kept their membership: %v", err)
	}
}

// --- the lobby's latency column and the host's description (D42) --------

func TestOnlyTheHostsOwnLatencyReachesTheColumn(t *testing.T) {
	s := room.NewStore()
	now := time.Now()

	_, host, err := s.Create("host", "a room", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Join(host.RoomID, room.Anyone("guest"), now); err != nil {
		t.Fatal(err)
	}

	// The column is labelled as the host's. A guest on a terrible connection
	// must not be able to write their own number into it - everyone reading
	// the lobby would blame the host for it, and the room would look worse
	// than it plays.
	s.ReportHostLatency(host.RoomID, "guest", 400, now)
	if got := roomOf(t, s, host.RoomID).HostRelayMillis; got != 0 {
		t.Errorf("a guest wrote %d into the host's latency column", got)
	}

	s.ReportHostLatency(host.RoomID, "host", 38, now)
	if got := roomOf(t, s, host.RoomID).HostRelayMillis; got != 38 {
		t.Errorf("the host's own reading came out as %d, want 38", got)
	}
}

// Zero means "not measured yet", and the interface has to be able to tell
// that apart from an excellent connection. Storing it would make every
// unmeasured room look like the best room in the lobby.
func TestAnUnmeasuredLatencyIsNotStored(t *testing.T) {
	s := room.NewStore()
	now := time.Now()
	_, host, err := s.Create("host", "a room", now)
	if err != nil {
		t.Fatal(err)
	}
	s.ReportHostLatency(host.RoomID, "host", 38, now)
	s.ReportHostLatency(host.RoomID, "host", 0, now)
	if got := roomOf(t, s, host.RoomID).HostRelayMillis; got != 38 {
		t.Errorf("an unmeasured reading overwrote a real one: got %d, want 38", got)
	}
}

func TestOnlyTheHostDescribesTheRoom(t *testing.T) {
	s := room.NewStore()
	now := time.Now()
	_, host, err := s.Create("host", "a room", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Join(host.RoomID, room.Anyone("guest"), now); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDescription(host.RoomID, "guest", "come to my room instead"); err == nil {
		t.Error("a guest rewrote the host's description")
	}
	if err := s.SetDescription(host.RoomID, "host", "  need 2, we start at 9  "); err != nil {
		t.Fatal(err)
	}
	if got := roomOf(t, s, host.RoomID).Description; got != "need 2, we start at 9" {
		t.Errorf("description came out as %q", got)
	}
}

// One host must not be able to push every other room off the screen.
func TestADescriptionIsBounded(t *testing.T) {
	s := room.NewStore()
	now := time.Now()
	_, host, err := s.Create("host", "a room", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDescription(host.RoomID, "host", strings.Repeat("x", 500)); err != nil {
		t.Fatal(err)
	}
	if got := len(roomOf(t, s, host.RoomID).Description); got > room.MaxDescription {
		t.Errorf("description kept %d characters, the limit is %d", got, room.MaxDescription)
	}
}

func roomOf(t *testing.T, s *room.Store, id string) room.Room {
	t.Helper()
	r, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// The point of D64 as amended by D74: a host may pick a side, and the address
// every other client is connecting to does not move when they do.
//
// D64 got the first half by making the room's address follow the host's seat.
// D74 gets both halves by making it follow the host: a seat is which team you
// are on, and it has nothing to do with what your machine is called.
func TestTheHostTakesTheRoomAddressWithThem(t *testing.T) {
	s := room.NewStore()
	_, hostM, _ := s.Create("h1", "A", t0)
	mate, err := s.Join(hostM.RoomID, room.Anyone("p2"), t0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Move(hostM.RoomID, "h1", 6, false); err != nil {
		t.Fatalf("the host moving to Dire: %v", err)
	}

	moved, err := s.Membership(hostM.RoomID, "h1")
	if err != nil {
		t.Fatal(err)
	}
	if moved.Slot != 6 {
		t.Fatalf("host slot = %d, want 6", moved.Slot)
	}
	if moved.VirtualIP != hostM.VirtualIP {
		t.Fatalf("the host changed seat and their address moved from %s to %s;"+
			" every client in the room was already connecting to the first one",
			hostM.VirtualIP, moved.VirtualIP)
	}
	if moved.HostIP != moved.VirtualIP {
		t.Errorf("the host is told to connect to %s while sitting at %s",
			moved.HostIP, moved.VirtualIP)
	}

	// And the person who was already in the room, who did nothing at all.
	again, err := s.Membership(hostM.RoomID, "p2")
	if err != nil {
		t.Fatal(err)
	}
	if again.HostIP != moved.VirtualIP {
		t.Errorf("the other player is still pointed at %s, but the host is at %s",
			again.HostIP, moved.VirtualIP)
	}
	if again.VirtualIP != mate.VirtualIP {
		t.Errorf("somebody else's move changed this player's own address from %s to %s",
			mate.VirtualIP, again.VirtualIP)
	}
}

// --- the host, watched rather than asked (D69, D70) ----------------------

// The bug the owner reported: they left a room and it was still in the lobby,
// still open, and they could walk back into it as its host. Leaving on
// purpose is not the same event as disappearing, and only the second one is
// what the grace period exists for.
func TestAHostWhoLeavesClosesTheRoomThereAndThen(t *testing.T) {
	s := room.NewStore()
	_, m, _ := s.Create("h1", "A", t0)
	_, _ = s.Join(m.RoomID, room.Anyone("p2"), t0)

	closed, err := s.Leave(m.RoomID, "h1", t0)
	if err != nil {
		t.Fatal(err)
	}
	if !closed {
		t.Fatal("leaving as host did not close the room")
	}
	if rooms := s.List(); len(rooms) != 0 {
		t.Fatalf("the room is still in the lobby: %v", rooms)
	}
	if _, err := s.Join(m.RoomID, room.Anyone("h1"), t0.Add(time.Second)); err == nil {
		t.Fatal("the host walked back into the room they closed")
	}
}

// Somebody who is not the host leaves an ordinary seat and nothing else
// happens - the room and the other nine people are untouched.
func TestAPlayerLeavingDoesNotCloseTheRoom(t *testing.T) {
	s := room.NewStore()
	_, m, _ := s.Create("h1", "A", t0)
	_, _ = s.Join(m.RoomID, room.Anyone("p2"), t0)

	closed, err := s.Leave(m.RoomID, "p2", t0)
	if err != nil {
		t.Fatal(err)
	}
	if closed {
		t.Fatal("an ordinary player leaving closed the room")
	}
	if len(s.List()) != 1 {
		t.Fatal("the room went away with them")
	}
}

// The other half: a host who goes quiet starts the countdown, and coming back
// inside it saves the room. This is the crash, the dropped line, the laptop
// that went to sleep.
func TestAHostWhoComesBackInsideTheGraceSavesTheRoom(t *testing.T) {
	s := room.NewStore()
	_, m, _ := s.Create("h1", "A", t0)

	here := false
	s.WatchHosts(func(string) room.HostFacts { return room.HostFacts{Online: here} })

	s.Tick(t0.Add(time.Second))
	r, _ := s.Get(m.RoomID)
	if !r.HostAway() {
		t.Fatal("nothing noticed the host had gone")
	}

	here = true
	s.Tick(t0.Add(30 * time.Second))
	r, _ = s.Get(m.RoomID)
	if r.HostAway() {
		t.Fatal("the host came back and the room kept counting down")
	}
	if closed := s.Tick(t0.Add(10 * time.Minute)); len(closed) != 0 {
		t.Fatal("the room closed under a host who was sitting in it")
	}
}

// While the host is in a match nobody may change seat, and nobody new may
// walk in - whether or not the host remembered to press Lock (D69).
func TestAHostInAMatchLocksTheRoom(t *testing.T) {
	s := room.NewStore()
	_, m, _ := s.Create("h1", "A", t0)
	_, _ = s.Join(m.RoomID, room.Anyone("p2"), t0)

	playing := false
	s.WatchHosts(func(string) room.HostFacts {
		return room.HostFacts{Online: true, InGame: playing}
	})
	s.Tick(t0.Add(time.Second))
	if err := s.Move(m.RoomID, "p2", 7, false); err != nil {
		t.Fatalf("a seat move was refused before the match started: %v", err)
	}

	playing = true
	s.Tick(t0.Add(2 * time.Second))
	if err := s.Move(m.RoomID, "p2", 8, false); !errors.Is(err, room.ErrRoomLocked) {
		t.Fatalf("moved seat during the host's match: %v", err)
	}
	if _, err := s.Join(m.RoomID, room.Anyone("p3"), t0.Add(3*time.Second)); !errors.Is(err, room.ErrRoomLocked) {
		t.Fatalf("joined a room whose host was in a match: %v", err)
	}

	// And it lets go again when the match ends. The room outlives the game
	// (D40): the ten who just played are the ten who want to play again.
	playing = false
	s.Tick(t0.Add(time.Minute))
	if err := s.Move(m.RoomID, "p2", 8, false); err != nil {
		t.Fatalf("still locked after the match ended: %v", err)
	}
}

// The host's own control still wins where it is meant to. Reopening a running
// match to new players is how an abandoned slot gets refilled, and it would
// be useless if being in the match cancelled it.
func TestReopeningOverridesTheAutomaticLock(t *testing.T) {
	s := room.NewStore()
	_, m, _ := s.Create("h1", "A", t0)
	s.WatchHosts(func(string) room.HostFacts {
		return room.HostFacts{Online: true, InGame: true}
	})
	s.Tick(t0.Add(time.Second))

	if err := s.SetStatus(m.RoomID, "h1", room.StatusOpenToNew, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Join(m.RoomID, room.Anyone("p2"), t0.Add(2*time.Second)); err != nil {
		t.Fatalf("the host reopened the room and nobody could join: %v", err)
	}
	// Seats still do not move: the match is running either way.
	if err := s.Move(m.RoomID, "p2", 9, false); !errors.Is(err, room.ErrRoomLocked) {
		t.Fatalf("moved seat mid-match in a reopened room: %v", err)
	}
}

// --- an address belongs to the player, not to the seat (D74) -------------

// The owner's report: changing seat disconnected them from the room's network
// and connected them again. It did, and this is why - their address was
// derived from the seat they were sitting in, so picking a side made the
// ticket they were holding name somebody else's address.
func TestChangingSeatDoesNotChangeYourAddress(t *testing.T) {
	s := room.NewStore()
	_, host, _ := s.Create("h1", "A", t0)
	before, err := s.Join(host.RoomID, room.Anyone("p2"), t0)
	if err != nil {
		t.Fatal(err)
	}

	for _, seat := range []int{7, 9, 2, 5} {
		if err := s.Move(host.RoomID, "p2", seat, false); err != nil {
			t.Fatalf("moving to seat %d: %v", seat, err)
		}
		after, err := s.Membership(host.RoomID, "p2")
		if err != nil {
			t.Fatal(err)
		}
		if after.VirtualIP != before.VirtualIP {
			t.Fatalf("seat %d moved the address from %s to %s",
				seat, before.VirtualIP, after.VirtualIP)
		}
		if after.Slot != seat {
			t.Fatalf("seat = %d, want %d", after.Slot, seat)
		}
		if after.HostIP != host.VirtualIP {
			t.Fatalf("the host address moved to %s when somebody else changed team",
				after.HostIP)
		}
	}
}

// Two people in a room never share an address, however they move around it,
// and an address is released when the person holding it gets up.
func TestAddressesAreUniqueAndReleasedOnLeaving(t *testing.T) {
	s := room.NewStore()
	_, host, _ := s.Create("h1", "A", t0)

	seen := map[string]string{host.VirtualIP.String(): "h1"}
	ids := []string{"p2", "p3", "p4"}
	for _, id := range ids {
		m, err := s.Join(host.RoomID, room.Anyone(id), t0)
		if err != nil {
			t.Fatal(err)
		}
		if who, clash := seen[m.VirtualIP.String()]; clash {
			t.Fatalf("%s was given %s, which is %s's", id, m.VirtualIP, who)
		}
		seen[m.VirtualIP.String()] = id
	}

	// Everybody swaps sides, and nobody's address moves or collides.
	for i, id := range ids {
		if err := s.Move(host.RoomID, id, 5+i, false); err != nil {
			t.Fatal(err)
		}
	}
	addrs := map[string]bool{}
	for _, id := range append([]string{"h1"}, ids...) {
		m, err := s.Membership(host.RoomID, id)
		if err != nil {
			t.Fatal(err)
		}
		if addrs[m.VirtualIP.String()] {
			t.Fatalf("%s shares address %s with somebody else", id, m.VirtualIP)
		}
		addrs[m.VirtualIP.String()] = true
	}

	// Getting up is what releases it, and the next person in takes it back.
	gone, err := s.Membership(host.RoomID, "p2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Leave(host.RoomID, "p2", t0); err != nil {
		t.Fatal(err)
	}
	back, err := s.Join(host.RoomID, room.Anyone("p5"), t0)
	if err != nil {
		t.Fatal(err)
	}
	if back.VirtualIP != gone.VirtualIP {
		t.Fatalf("the freed address %s was not reused; p5 got %s",
			gone.VirtualIP, back.VirtualIP)
	}
}

// A host who crashed has to come back to the address nine other clients are
// already sending to. The grace window is exactly the period in which their
// address must not be given to anybody else.
func TestAHostKeepsTheirAddressThroughTheGraceWindow(t *testing.T) {
	s := room.NewStore()
	_, host, _ := s.Create("h1", "A", t0)
	if _, err := s.Join(host.RoomID, room.Anyone("p2"), t0); err != nil {
		t.Fatal(err)
	}

	s.WatchHosts(func(string) room.HostFacts { return room.HostFacts{} })
	s.Tick(t0.Add(time.Second))

	mate, err := s.Membership(host.RoomID, "p2")
	if err != nil {
		t.Fatal(err)
	}
	if mate.HostIP != host.VirtualIP {
		t.Fatalf("the room's address moved to %s while its host was away", mate.HostIP)
	}

	// Somebody else joining during the grace must not be handed it.
	other, err := s.Join(host.RoomID, room.Anyone("p3"), t0.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if other.VirtualIP == host.VirtualIP {
		t.Fatalf("a new player was given the absent host's address %s", other.VirtualIP)
	}
}

// --- the gallery is a seat like any other (D79) --------------------------

// The owner's request: "all players including the host can switch positions
// to Watchers/observers as well."
//
// The rule this has to keep is D74's, and it is the one that is easy to lose
// here: an address belongs to the person for as long as they are in the room.
// Watchers used to be addressed from a range of their own, indexed by the
// seat - so moving into the gallery would have changed somebody's address,
// invalidated the ticket naming it, and dropped the tunnel. That is exactly
// the bug the owner reported two days earlier, and it would have come back
// wearing a different hat.
func TestGoingToWatchDoesNotChangeYourAddress(t *testing.T) {
	s := room.NewStore()
	r, _, _ := s.Create("h1", "test", t0)
	m, err := s.Join(r.ID, room.Anyone("p2"), t0)
	if err != nil {
		t.Fatal(err)
	}
	before := m.VirtualIP

	if err := s.Move(r.ID, "p2", 1, true); err != nil {
		t.Fatalf("a player could not go and watch: %v", err)
	}
	after, err := s.Membership(r.ID, "p2")
	if err != nil {
		t.Fatalf("a watcher has no membership: %v", err)
	}
	if after.VirtualIP != before {
		t.Fatalf("going to watch moved the address from %v to %v - the tunnel would drop",
			before, after.VirtualIP)
	}
	if !after.IsSpectator() {
		t.Fatal("a member sitting in the gallery is not reported as watching")
	}

	// And back to a playing seat, still the same address.
	if err := s.Move(r.ID, "p2", 6, false); err != nil {
		t.Fatalf("a watcher could not sit down and play: %v", err)
	}
	back, err := s.Membership(r.ID, "p2")
	if err != nil {
		t.Fatal(err)
	}
	if back.VirtualIP != before {
		t.Fatalf("coming back to play moved the address from %v to %v", before, back.VirtualIP)
	}
	if back.IsSpectator() || back.Slot != 6 {
		t.Fatalf("expected to be playing in seat 6, got spectator=%v slot=%d",
			back.IsSpectator(), back.Slot)
	}
}

// The host's own address is what every other client in the room is sending
// to, so this is the one that ends a match rather than inconveniencing one
// person.
func TestAHostWhoGoesToWatchKeepsTheRoomsAddress(t *testing.T) {
	s := room.NewStore()
	r, hostM, _ := s.Create("h1", "test", t0)
	mate, err := s.Join(r.ID, room.Anyone("p2"), t0)
	if err != nil {
		t.Fatal(err)
	}
	if mate.HostIP != hostM.VirtualIP {
		t.Fatalf("the room's host address is %v, the host holds %v", mate.HostIP, hostM.VirtualIP)
	}

	if err := s.Move(r.ID, "h1", 0, true); err != nil {
		t.Fatalf("the host could not go and watch their own room: %v", err)
	}
	after, err := s.Membership(r.ID, "p2")
	if err != nil {
		t.Fatal(err)
	}
	if after.HostIP != hostM.VirtualIP {
		t.Fatalf("the host went to watch and the room's address moved from %v to %v - "+
			"every client is now connecting to nobody", hostM.VirtualIP, after.HostIP)
	}
}

// Watchers hold addresses out of the same pool as players, so a full room
// plus a full gallery has to fit in it. Fourteen people, fourteen addresses,
// no two the same.
func TestAFullRoomAndAFullGalleryAllGetAddresses(t *testing.T) {
	s := room.NewStore()
	r, host, _ := s.Create("h1", "test", t0)

	seen := map[string]string{host.VirtualIP.String(): "h1"}
	add := func(id string, m room.Membership, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s could not be seated: %v", id, err)
		}
		if !m.VirtualIP.IsValid() {
			t.Fatalf("%s was seated with no address", id)
		}
		if other, clash := seen[m.VirtualIP.String()]; clash {
			t.Fatalf("%s and %s were both given %v", id, other, m.VirtualIP)
		}
		seen[m.VirtualIP.String()] = id
	}

	for i := 2; i <= 10; i++ { // nine more players fills the ten slots
		id := fmt.Sprintf("p%d", i)
		m, err := s.Join(r.ID, room.Anyone(id), t0)
		add(id, m, err)
	}
	for i := 1; i <= 4; i++ { // and four watchers
		id := fmt.Sprintf("w%d", i)
		m, err := s.JoinObserver(r.ID, room.Anyone(id), t0)
		add(id, m, err)
	}
	if len(seen) != 14 {
		t.Fatalf("seated %d people with distinct addresses, want 14", len(seen))
	}
}

// A host whose PC died while they were watching comes back to watching. The
// grace window must not quietly put them on a team.
func TestAWatchingHostComesBackToWatching(t *testing.T) {
	rm := newRoom(t)
	if err := rm.Move("host-1", 2, true); err != nil {
		t.Fatal(err)
	}
	rm.Leave("host-1", t0)
	rm.HostGraceUntil = t0.Add(time.Minute)

	if _, err := rm.Join(room.Anyone("host-1"), t0); err != nil {
		t.Fatalf("the host could not come back: %v", err)
	}
	slot, kind, seated := rm.SlotOf("host-1")
	if !seated || kind != room.SeatObserver || slot != 2 {
		t.Fatalf("the host came back to %v seat %d, want observer seat 2", kind, slot)
	}
}

// A moderator's seat is reserved outside both areas so that a full match plus
// a full gallery can never keep them out. Letting them vacate it to play
// would hand that reservation back to the room.
func TestAModeratorCannotSitDownAndPlay(t *testing.T) {
	s := room.NewStore()
	r, _, _ := s.Create("h1", "test", t0)
	if _, err := s.JoinAdmin(r.ID, "mod", t0); err != nil {
		t.Fatal(err)
	}
	if err := s.Move(r.ID, "mod", 5, false); err == nil {
		t.Fatal("a moderator gave up the reserved seat to take a playing one")
	}
}
