package room

import (
	"testing"
	"time"
)

func TestSpectatorMayEnterALockedRoom(t *testing.T) {
	// The reserved seat exists so an admin can look at a match that is
	// already running. Barring them from a locked room would defeat it.
	s := NewStore()
	r, _, err := s.Create("host", "test", when())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetStatus(r.ID, "host", StatusLocked, when()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Join(r.ID, "player", when()); err != ErrRoomLocked {
		t.Fatalf("an ordinary player must be refused, got %v", err)
	}
	m, err := s.JoinSpectator(r.ID, "admin", when())
	if err != nil {
		t.Fatalf("the admin seat must still open: %v", err)
	}
	if !m.IsSpectator {
		t.Error("membership not marked as spectator")
	}
}

func TestSpectatorAddressIsOutsideThePlayingSlots(t *testing.T) {
	s := NewStore()
	r, hostM, _ := s.Create("host", "test", when())

	var playerIPs []string
	for i := 0; i < 3; i++ {
		m, err := s.Join(r.ID, "p"+string(rune('a'+i)), when())
		if err != nil {
			t.Fatal(err)
		}
		playerIPs = append(playerIPs, m.VirtualIP.String())
	}
	spec, err := s.JoinSpectator(r.ID, "admin", when())
	if err != nil {
		t.Fatal(err)
	}
	for _, ip := range append(playerIPs, hostM.VirtualIP.String()) {
		if spec.VirtualIP.String() == ip {
			t.Fatalf("spectator collided with a player at %s", ip)
		}
	}
	if !spec.Subnet.Contains(spec.VirtualIP) {
		t.Errorf("spectator %s is outside the room subnet %s", spec.VirtualIP, spec.Subnet)
	}
	if spec.HostIP != hostM.VirtualIP {
		t.Errorf("spectator was told the wrong host address")
	}
}

func TestSpectatorSeatsAreFinite(t *testing.T) {
	s := NewStore()
	r, _, _ := s.Create("host", "test", when())
	for i := 0; i < 3; i++ {
		if _, err := s.JoinSpectator(r.ID, "admin"+string(rune('a'+i)), when()); err != nil {
			t.Fatalf("seat %d: %v", i, err)
		}
	}
	if _, err := s.JoinSpectator(r.ID, "one-too-many", when()); err != ErrNoSpectatorSeat {
		t.Fatalf("got %v", err)
	}
}

func TestKickedPlayerCannotSneakBackAsASpectator(t *testing.T) {
	s := NewStore()
	r, _, _ := s.Create("host", "test", when())
	if _, err := s.Join(r.ID, "pest", when()); err != nil {
		t.Fatal(err)
	}
	if err := s.Kick(r.ID, "host", "pest", when()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.JoinSpectator(r.ID, "pest", when()); err != ErrKickBlocked {
		t.Fatalf("the block must cover the spectator seat too, got %v", err)
	}
}

func TestCannotHoldAPlayingSlotAndASpectatorSeat(t *testing.T) {
	s := NewStore()
	r, _, _ := s.Create("host", "test", when())
	if _, err := s.JoinSpectator(r.ID, "host", when()); err != ErrAlreadyJoined {
		t.Fatalf("got %v", err)
	}
}

func TestLeavingReleasesTheSpectatorSeat(t *testing.T) {
	s := NewStore()
	r, _, _ := s.Create("host", "test", when())
	if _, err := s.JoinSpectator(r.ID, "admin", when()); err != nil {
		t.Fatal(err)
	}
	if err := s.Leave(r.ID, "admin", when()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.JoinSpectator(r.ID, "admin", when()); err != nil {
		t.Fatalf("seat was not released: %v", err)
	}
}

func TestOccupantsAndSeatCount(t *testing.T) {
	s := NewStore()
	r, _, _ := s.Create("host", "test", when())
	_, _ = s.Join(r.ID, "p1", when())
	_, _ = s.JoinSpectator(r.ID, "admin", when())

	got, _ := s.Get(r.ID)
	if n := got.Seats(); n != 2 {
		t.Errorf("Seats counted %d playing slots, want 2 (spectators must not count)", n)
	}
	if occ := got.Occupants(); len(occ) != 3 {
		t.Errorf("Occupants returned %v, want all three", occ)
	}
	if slot, isSpec, ok := got.SlotOf("admin"); !ok || !isSpec || slot != 0 {
		t.Errorf("SlotOf(admin) = %d, %v, %v", slot, isSpec, ok)
	}
	if slot, isSpec, ok := got.SlotOf("p1"); !ok || isSpec || slot != 1 {
		t.Errorf("SlotOf(p1) = %d, %v, %v", slot, isSpec, ok)
	}
}

func when() time.Time { return time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC) }
