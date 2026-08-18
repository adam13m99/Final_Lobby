// Package ipam allocates the virtual addresses used inside room networks.
//
// The platform owns 10.87.0.0/16 and gives every room a /28. Sixteen
// addresses per room: .0 network, .1 reserved for the relay, .2-.11 the ten
// player slots, .12-.14 spectator and admin slots, .15 broadcast.
package ipam

import (
	"errors"
	"fmt"
	"net/netip"
)

const (
	// MaxRooms is how many /28 blocks fit inside 10.87.0.0/16.
	MaxRooms = 4096
	// PlayerSlots is fixed by Dota 2 itself.
	PlayerSlots = 10
	// SpectatorSlots covers admin observers.
	SpectatorSlots = 3

	playerBaseOffset    = 2
	spectatorBaseOffset = 12
)

var (
	ErrRoomIndexRange = errors.New("ipam: room index out of range")
	ErrSlotRange      = errors.New("ipam: slot index out of range")
)

// RoomSubnet returns the /28 belonging to roomIndex.
func RoomSubnet(roomIndex int) (netip.Prefix, error) {
	base, err := subnetBase(roomIndex)
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.PrefixFrom(base, 28), nil
}

// HostIP returns the address the room's host always occupies. Clients are
// told this address directly, which is why Dota never needs LAN discovery.
func HostIP(roomIndex int) (netip.Addr, error) {
	return SlotIP(roomIndex, 0)
}

// SlotIP returns the address for a player slot. Slot 0 is the host.
func SlotIP(roomIndex, slot int) (netip.Addr, error) {
	if slot < 0 || slot >= PlayerSlots {
		return netip.Addr{}, fmt.Errorf("%w: player slot %d", ErrSlotRange, slot)
	}
	return offsetFrom(roomIndex, playerBaseOffset+slot)
}

// SpectatorIP returns the address for a spectator or admin slot.
func SpectatorIP(roomIndex, index int) (netip.Addr, error) {
	if index < 0 || index >= SpectatorSlots {
		return netip.Addr{}, fmt.Errorf("%w: spectator slot %d", ErrSlotRange, index)
	}
	return offsetFrom(roomIndex, spectatorBaseOffset+index)
}

func subnetBase(roomIndex int) (netip.Addr, error) {
	if roomIndex < 0 || roomIndex >= MaxRooms {
		return netip.Addr{}, fmt.Errorf("%w: %d", ErrRoomIndexRange, roomIndex)
	}
	third := byte(roomIndex >> 4)
	fourth := byte((roomIndex & 0x0F) << 4)
	return netip.AddrFrom4([4]byte{10, 87, third, fourth}), nil
}

func offsetFrom(roomIndex, offset int) (netip.Addr, error) {
	base, err := subnetBase(roomIndex)
	if err != nil {
		return netip.Addr{}, err
	}
	b := base.As4()
	b[3] += byte(offset)
	return netip.AddrFrom4(b), nil
}
