package room

import (
	"testing"
	"time"

	"lobbybaz/coordinator/internal/ipam"
)

// D38 split "spectator" into two genuinely different seats. An observer is an
// ordinary player choosing to watch; an admin is staff. The difference that
// matters is what happens when a match is already running.

func TestAdminMayEnterALockedRoomButAnObserverMayNot(t *testing.T) {
	s := NewStore()
	r, _, err := s.Create("host", "test", when())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetStatus(r.ID, "host", StatusLocked, when()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Join(r.ID, Anyone("player"), when()); err != ErrRoomLocked {
		t.Fatalf("an ordinary player must be refused, got %v", err)
	}

	// An admin is called in precisely when a match is running and something
	// has gone wrong inside it. Barring them would defeat the seat.
	m, err := s.JoinAdmin(r.ID, "admin", when())
	if err != nil {
		t.Fatalf("the admin seat must still open: %v", err)
	}
	if m.Kind != SeatAdmin || !m.IsSpectator() {
		t.Errorf("membership kind = %q, want %q", m.Kind, SeatAdmin)
	}

	// An observer wandering into a running match is how scouting starts.
	if _, err := s.JoinObserver(r.ID, Anyone("nosy"), when()); err != ErrRoomLocked {
		t.Fatalf("an observer must be refused from a locked room, got %v", err)
	}
}

func TestWatchingSeatsSitOutsideThePlayingSlots(t *testing.T) {
	s := NewStore()
	r, hostM, _ := s.Create("host", "test", when())

	var taken []string
	for i := 0; i < 3; i++ {
		m, err := s.Join(r.ID, Anyone("p"+string(rune('a'+i))), when())
		if err != nil {
			t.Fatal(err)
		}
		taken = append(taken, m.VirtualIP.String())
	}
	taken = append(taken, hostM.VirtualIP.String())

	obs, err := s.JoinObserver(r.ID, Anyone("watcher"), when())
	if err != nil {
		t.Fatal(err)
	}
	adm, err := s.JoinAdmin(r.ID, "admin", when())
	if err != nil {
		t.Fatal(err)
	}

	for _, m := range []Membership{obs, adm} {
		for _, ip := range taken {
			if m.VirtualIP.String() == ip {
				t.Fatalf("%s seat collided with a player at %s", m.Kind, ip)
			}
		}
		if !m.Subnet.Contains(m.VirtualIP) {
			t.Errorf("%s %s is outside the room subnet %s", m.Kind, m.VirtualIP, m.Subnet)
		}
		if m.HostIP != hostM.VirtualIP {
			t.Errorf("%s was told the wrong host address", m.Kind)
		}
	}
	if obs.VirtualIP == adm.VirtualIP {
		t.Error("the observer and the admin were given the same address")
	}
}

func TestWatchingSeatsAreFinite(t *testing.T) {
	s := NewStore()
	r, _, _ := s.Create("host", "test", when())

	for i := 0; i < ipam.ObserverSlots; i++ {
		if _, err := s.JoinObserver(r.ID, Anyone("obs"+string(rune('a'+i))), when()); err != nil {
			t.Fatalf("observer seat %d: %v", i, err)
		}
	}
	if _, err := s.JoinObserver(r.ID, Anyone("one-too-many"), when()); err != ErrNoObserverSeat {
		t.Fatalf("got %v, want ErrNoObserverSeat", err)
	}

	for i := 0; i < ipam.AdminSlots; i++ {
		if _, err := s.JoinAdmin(r.ID, "adm"+string(rune('a'+i)), when()); err != nil {
			t.Fatalf("admin seat %d: %v", i, err)
		}
	}
	if _, err := s.JoinAdmin(r.ID, "one-too-many-admins", when()); err != ErrNoAdminSeat {
		t.Fatalf("got %v, want ErrNoAdminSeat", err)
	}
}

// A full room is eighteen people. This is the case the /28 could not hold.
func TestAFullRoomIsEighteenDistinctAddresses(t *testing.T) {
	s := NewStore()
	r, hostM, _ := s.Create("host", "test", when())

	seen := map[string]bool{hostM.VirtualIP.String(): true}
	add := func(m Membership, what string) {
		t.Helper()
		ip := m.VirtualIP.String()
		if seen[ip] {
			t.Fatalf("%s reused address %s", what, ip)
		}
		if !m.Subnet.Contains(m.VirtualIP) {
			t.Fatalf("%s at %s escaped the subnet %s", what, ip, m.Subnet)
		}
		seen[ip] = true
	}

	for i := 1; i < ipam.PlayerSlots; i++ {
		m, err := s.Join(r.ID, Anyone("player"+string(rune('a'+i))), when())
		if err != nil {
			t.Fatalf("player %d: %v", i, err)
		}
		add(m, "player")
	}
	for i := 0; i < ipam.ObserverSlots; i++ {
		m, err := s.JoinObserver(r.ID, Anyone("obs"+string(rune('a'+i))), when())
		if err != nil {
			t.Fatalf("observer %d: %v", i, err)
		}
		add(m, "observer")
	}
	for i := 0; i < ipam.AdminSlots; i++ {
		m, err := s.JoinAdmin(r.ID, "adm"+string(rune('a'+i)), when())
		if err != nil {
			t.Fatalf("admin %d: %v", i, err)
		}
		add(m, "admin")
	}

	if len(seen) != ipam.SeatsPerRoom {
		t.Fatalf("seated %d people, want %d", len(seen), ipam.SeatsPerRoom)
	}

	got, _ := s.Get(r.ID)
	if n := got.Seats(); n != ipam.PlayerSlots {
		t.Errorf("Seats() = %d, want %d", n, ipam.PlayerSlots)
	}
	if n := got.Watchers(); n != ipam.ObserverSlots {
		t.Errorf("Watchers() = %d, want %d", n, ipam.ObserverSlots)
	}
	if n := len(got.Occupants()); n != ipam.SeatsPerRoom {
		t.Errorf("Occupants() = %d, want %d", n, ipam.SeatsPerRoom)
	}
}

func TestKickedPlayerCannotSneakBackIntoAnotherSeat(t *testing.T) {
	s := NewStore()
	r, _, _ := s.Create("host", "test", when())
	if _, err := s.Join(r.ID, Anyone("pest"), when()); err != nil {
		t.Fatal(err)
	}
	if err := s.Kick(r.ID, "host", "pest", when()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.JoinObserver(r.ID, Anyone("pest"), when()); err != ErrKickBlocked {
		t.Fatalf("the block must cover the observer gallery too, got %v", err)
	}
	// The block is enforced against identity, not against role: being staff
	// does not undo being kicked.
	if _, err := s.JoinAdmin(r.ID, "pest", when()); err != ErrKickBlocked {
		t.Fatalf("the block must cover the admin seat too, got %v", err)
	}
}

func TestCannotHoldTwoSeatsAtOnce(t *testing.T) {
	s := NewStore()
	r, _, _ := s.Create("host", "test", when())
	if _, err := s.Join(r.ID, Anyone("p2"), when()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.JoinObserver(r.ID, Anyone("p2"), when()); err != ErrAlreadyJoined {
		t.Fatalf("a seated player took an observer seat as well: %v", err)
	}
	if _, err := s.JoinAdmin(r.ID, "p2", when()); err != ErrAlreadyJoined {
		t.Fatalf("a seated player took an admin seat as well: %v", err)
	}
	// The host is refused for being seated, like anybody else. They used to
	// be refused the gallery outright; the owner reversed that (D79), so what
	// stops them here is the one rule that stops everybody - one seat each.
	// The way in is Move, which vacates the seat they are leaving.
	if _, err := s.JoinObserver(r.ID, Anyone("host"), when()); err != ErrAlreadyJoined {
		t.Fatalf("the host took a second seat: %v", err)
	}
	if _, err := s.JoinAdmin(r.ID, "host", when()); err != ErrAlreadyJoined {
		t.Fatalf("playing host took an admin seat: %v", err)
	}
	if _, err := s.JoinObserver(r.ID, Anyone("watcher"), when()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.JoinAdmin(r.ID, "watcher", when()); err != ErrAlreadyJoined {
		t.Fatalf("observer also took an admin seat: %v", err)
	}
}

func TestLeavingReleasesEverySeatKind(t *testing.T) {
	s := NewStore()
	r, _, _ := s.Create("host", "test", when())

	// Both watching seats, driven through one shape so neither can be
	// released correctly while the other quietly leaks.
	for _, join := range []func(string, string, time.Time) (Membership, error){
		func(id, who string, now time.Time) (Membership, error) {
			return s.JoinObserver(id, Anyone(who), now)
		},
		s.JoinAdmin,
	} {
		if _, err := join(r.ID, "somebody", when()); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Leave(r.ID, "somebody", when()); err != nil {
			t.Fatal(err)
		}
		if _, err := join(r.ID, "somebody", when()); err != nil {
			t.Fatalf("seat was not released: %v", err)
		}
		if _, err := s.Leave(r.ID, "somebody", when()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSlotOfReportsWhichSeatingAreaSomebodyIsIn(t *testing.T) {
	s := NewStore()
	r, _, _ := s.Create("host", "test", when())
	_, _ = s.Join(r.ID, Anyone("p1"), when())
	_, _ = s.JoinObserver(r.ID, Anyone("watcher"), when())
	_, _ = s.JoinAdmin(r.ID, "admin", when())

	got, _ := s.Get(r.ID)
	cases := []struct {
		id   string
		slot int
		kind SeatKind
	}{
		{"host", 0, SeatPlayer},
		{"p1", 1, SeatPlayer},
		{"watcher", 0, SeatObserver},
		{"admin", 0, SeatAdmin},
	}
	for _, c := range cases {
		slot, kind, ok := got.SlotOf(c.id)
		if !ok || slot != c.slot || kind != c.kind {
			t.Errorf("SlotOf(%s) = %d, %q, %v; want %d, %q, true",
				c.id, slot, kind, ok, c.slot, c.kind)
		}
	}
	if _, _, ok := got.SlotOf("stranger"); ok {
		t.Error("a stranger was reported as seated")
	}
}

func when() time.Time { return time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC) }
