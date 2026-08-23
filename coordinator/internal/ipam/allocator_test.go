package ipam_test

import (
	"errors"
	"testing"

	"lobbybaz/coordinator/internal/ipam"
)

func TestRoomSubnetLayout(t *testing.T) {
	cases := []struct {
		room int
		want string
	}{
		{0, "10.87.0.0/28"},
		{1, "10.87.0.16/28"},
		{15, "10.87.0.240/28"},
		{16, "10.87.1.0/28"},
		{4095, "10.87.255.240/28"},
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

func TestHostIPIsDeterministic(t *testing.T) {
	got, err := ipam.HostIP(16)
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "10.87.1.2" {
		t.Fatalf("HostIP(16) = %s, want 10.87.1.2", got)
	}
	// The host must equal slot 0.
	slot0, _ := ipam.SlotIP(16, 0)
	if slot0 != got {
		t.Fatalf("SlotIP(16,0) = %s, want %s", slot0, got)
	}
}

func TestSlotIPsCoverTenPlayers(t *testing.T) {
	first, _ := ipam.SlotIP(0, 0)
	last, _ := ipam.SlotIP(0, ipam.PlayerSlots-1)
	if first.String() != "10.87.0.2" {
		t.Errorf("first slot = %s, want 10.87.0.2", first)
	}
	if last.String() != "10.87.0.11" {
		t.Errorf("last slot = %s, want 10.87.0.11", last)
	}
}

func TestSpectatorIPsSitOutsidePlayerRange(t *testing.T) {
	first, _ := ipam.SpectatorIP(0, 0)
	last, _ := ipam.SpectatorIP(0, ipam.SpectatorSlots-1)
	if first.String() != "10.87.0.12" {
		t.Errorf("first spectator = %s, want 10.87.0.12", first)
	}
	if last.String() != "10.87.0.14" {
		t.Errorf("last spectator = %s, want 10.87.0.14", last)
	}
}

func TestOutOfRangeRejected(t *testing.T) {
	if _, err := ipam.RoomSubnet(ipam.MaxRooms); !errors.Is(err, ipam.ErrRoomIndexRange) {
		t.Errorf("RoomSubnet(MaxRooms) err = %v, want ErrRoomIndexRange", err)
	}
	if _, err := ipam.SlotIP(0, ipam.PlayerSlots); !errors.Is(err, ipam.ErrSlotRange) {
		t.Errorf("SlotIP overflow err = %v, want ErrSlotRange", err)
	}
	if _, err := ipam.SpectatorIP(0, ipam.SpectatorSlots); !errors.Is(err, ipam.ErrSlotRange) {
		t.Errorf("SpectatorIP overflow err = %v, want ErrSlotRange", err)
	}
}
