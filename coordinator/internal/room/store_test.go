package room_test

import (
	"errors"
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

func TestTickReportsRoomsThatJustClosed(t *testing.T) {
	s := room.NewStore()
	_, m, _ := s.Create("h1", "A", t0)
	_, _ = s.Join(m.RoomID, room.Anyone("p2"), t0)
	_ = s.Leave(m.RoomID, "h1", t0)

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
	_ = s.Leave(first.RoomID, "h1", t0)
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
	_ = s.Leave(host.RoomID, "h1", t0)
	if _, err := s.Membership(host.RoomID, "h1"); !errors.Is(err, room.ErrNotMember) {
		t.Fatalf("a departed player kept their membership: %v", err)
	}
}
