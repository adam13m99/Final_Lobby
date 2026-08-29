package ipam_test

import (
	"errors"
	"net/netip"
	"testing"

	"lobbybaz/coordinator/internal/ipam"
)

func TestRoomSubnetLayout(t *testing.T) {
	cases := []struct {
		room int
		want string
	}{
		{0, "10.87.0.0/27"},
		{1, "10.87.0.32/27"},
		{7, "10.87.0.224/27"},
		{8, "10.87.1.0/27"},
		{2047, "10.87.255.224/27"},
	}
	for _, c := range cases {
		got, err := ipam.RoomSubnet(c.room)
		if err != nil {
			t.Fatalf("room %d: %v", c.room, err)
		}
		if got.String() != c.want {
			t.Errorf("room %d = %s, want %s", c.room, got, c.want)
		}
	}
}

// Every seat's address is a pure function of the room and the seat, and it
// has to stay one: clients are handed an address directly, which is the whole
// reason Dota never needs LAN discovery. Which of these ten the host occupies
// is the room's business now (D64), not this package's.
func TestSlotAddressesAreDeterministic(t *testing.T) {
	got, err := ipam.SlotIP(8, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "10.87.1.2" {
		t.Fatalf("SlotIP(8, 0) = %s, want 10.87.1.2", got)
	}
	last, err := ipam.SlotIP(8, ipam.PlayerSlots-1)
	if err != nil {
		t.Fatal(err)
	}
	if last.String() != "10.87.1.11" {
		t.Fatalf("SlotIP(8, 9) = %s, want 10.87.1.11", last)
	}
}

func TestSeatRangesDoNotOverlap(t *testing.T) {
	cases := []struct {
		name        string
		first, last string
	}{
		{"players", "10.87.0.2", "10.87.0.11"},
		{"observers", "10.87.0.12", "10.87.0.16"},
		{"admins", "10.87.0.17", "10.87.0.19"},
	}
	get := map[string]func(int, int) (netip.Addr, error){
		"players":   ipam.SlotIP,
		"observers": ipam.ObserverIP,
		"admins":    ipam.AdminIP,
	}
	count := map[string]int{
		"players":   ipam.PlayerSlots,
		"observers": ipam.ObserverSlots,
		"admins":    ipam.AdminSlots,
	}
	for _, c := range cases {
		f := get[c.name]
		first, err := f(0, 0)
		if err != nil {
			t.Fatalf("%s first: %v", c.name, err)
		}
		last, err := f(0, count[c.name]-1)
		if err != nil {
			t.Fatalf("%s last: %v", c.name, err)
		}
		if first.String() != c.first {
			t.Errorf("first %s = %s, want %s", c.name, first, c.first)
		}
		if last.String() != c.last {
			t.Errorf("last %s = %s, want %s", c.name, last, c.last)
		}
	}
}

// Every seat in a full room must be a distinct address that actually falls
// inside the room's own subnet. This is the check that would have caught the
// /28 being too small: eighteen seats simply did not fit.
func TestAFullRoomFitsInItsSubnet(t *testing.T) {
	for _, room := range []int{0, 1, 8, 2047} {
		subnet, err := ipam.RoomSubnet(room)
		if err != nil {
			t.Fatalf("room %d: %v", room, err)
		}
		seen := map[netip.Addr]string{}

		add := func(kind string, addr netip.Addr) {
			t.Helper()
			if !subnet.Contains(addr) {
				t.Errorf("room %d: %s seat %s is outside %s", room, kind, addr, subnet)
			}
			if prev, dup := seen[addr]; dup {
				t.Errorf("room %d: %s collides with %s at %s", room, kind, prev, addr)
			}
			seen[addr] = kind
		}

		for i := 0; i < ipam.PlayerSlots; i++ {
			a, err := ipam.SlotIP(room, i)
			if err != nil {
				t.Fatal(err)
			}
			add("player", a)
		}
		for i := 0; i < ipam.ObserverSlots; i++ {
			a, err := ipam.ObserverIP(room, i)
			if err != nil {
				t.Fatal(err)
			}
			add("observer", a)
		}
		for i := 0; i < ipam.AdminSlots; i++ {
			a, err := ipam.AdminIP(room, i)
			if err != nil {
				t.Fatal(err)
			}
			add("admin", a)
		}

		if len(seen) != ipam.SeatsPerRoom {
			t.Errorf("room %d: %d distinct seats, want %d", room, len(seen), ipam.SeatsPerRoom)
		}

		// The network and broadcast addresses must not be handed to anybody.
		base := subnet.Addr()
		if _, taken := seen[base]; taken {
			t.Errorf("room %d: the network address %s was allocated", room, base)
		}
	}
}

// Neighbouring rooms must not overlap, or two rooms share addresses and the
// relay's room scoping is the only thing standing between them.
func TestAdjacentRoomsDoNotOverlap(t *testing.T) {
	for room := 0; room < 16; room++ {
		this, err := ipam.RoomSubnet(room)
		if err != nil {
			t.Fatal(err)
		}
		next, err := ipam.RoomSubnet(room + 1)
		if err != nil {
			t.Fatal(err)
		}
		if this.Overlaps(next) {
			t.Fatalf("room %d (%s) overlaps room %d (%s)", room, this, room+1, next)
		}
	}
}

func TestOutOfRangeRejected(t *testing.T) {
	if _, err := ipam.RoomSubnet(ipam.MaxRooms); !errors.Is(err, ipam.ErrRoomIndexRange) {
		t.Errorf("RoomSubnet(MaxRooms) err = %v, want ErrRoomIndexRange", err)
	}
	if _, err := ipam.SlotIP(0, ipam.PlayerSlots); !errors.Is(err, ipam.ErrSlotRange) {
		t.Errorf("SlotIP overflow err = %v, want ErrSlotRange", err)
	}
	if _, err := ipam.ObserverIP(0, ipam.ObserverSlots); !errors.Is(err, ipam.ErrSlotRange) {
		t.Errorf("ObserverIP overflow err = %v, want ErrSlotRange", err)
	}
	if _, err := ipam.AdminIP(0, ipam.AdminSlots); !errors.Is(err, ipam.ErrSlotRange) {
		t.Errorf("AdminIP overflow err = %v, want ErrSlotRange", err)
	}
}
