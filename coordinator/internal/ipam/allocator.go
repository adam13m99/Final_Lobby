// Package ipam allocates the virtual addresses used inside room networks.
//
// The platform owns 10.87.0.0/16 and gives every room a /27. Thirty-two
// addresses per room: .0 network, .1 reserved for the relay, .2-.11 the ten
// player slots, .12-.16 five observers, .17-.19 three admin seats, .20-.30
// spare, .31 broadcast.
//
// It was a /28 until 2026-08-24. Sixteen addresses held thirteen seats with
// every one spoken for, and the owner's room is eighteen seats (D38), so the
// block had to double. The cost is the room ceiling: 4096 became 2048, which
// is still forty times the 500-player launch target. The eleven spare
// addresses are deliberate - the last resize touched every layer of the
// stack, and the next change to the seat count should not.
package ipam

import (
	"errors"
	"fmt"
	"net/netip"
)

const (
	// MaxRooms is how many /27 blocks fit inside 10.87.0.0/16.
	MaxRooms = 2048
	// PlayerSlots is fixed by Dota 2 itself.
	PlayerSlots = 10
	// ObserverSlots is how many people may watch a room without playing.
	ObserverSlots = 5
	// AdminSlots sit outside both, so a full match plus a full gallery can
	// never stop a moderator getting in.
	AdminSlots = 3

	// SeatsPerRoom is what a room holds in total.
	SeatsPerRoom = PlayerSlots + ObserverSlots + AdminSlots

	// subnetBits is the prefix length of one room's block.
	subnetBits = 27
	// addressesPerRoom must stay 1<<(32-subnetBits).
	addressesPerRoom = 32

	playerBaseOffset   = 2
	observerBaseOffset = playerBaseOffset + PlayerSlots     // 12
	adminBaseOffset    = observerBaseOffset + ObserverSlots // 17
)

var (
	ErrRoomIndexRange = errors.New("ipam: room index out of range")
	ErrSlotRange      = errors.New("ipam: slot index out of range")
)

// RoomSubnet returns the /27 belonging to roomIndex.
func RoomSubnet(roomIndex int) (netip.Prefix, error) {
	base, err := subnetBase(roomIndex)
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.PrefixFrom(base, subnetBits), nil
}

// SlotIP returns the address for a player slot.
//
// There used to be a HostIP helper beside this one, returning slot 0, because
// the host was always in slot 0. The host now sits where they like (D64) and
// the room tracks which seat that is, so a function whose name promises "the
// host's address" from a room index alone would be stating a rule the room no
// longer has. room.hostAddr asks the room instead.
func SlotIP(roomIndex, slot int) (netip.Addr, error) {
	if slot < 0 || slot >= PlayerSlots {
		return netip.Addr{}, fmt.Errorf("%w: player slot %d", ErrSlotRange, slot)
	}
	return offsetFrom(roomIndex, playerBaseOffset+slot)
}

// ObserverIP returns the address for someone watching without playing.
func ObserverIP(roomIndex, index int) (netip.Addr, error) {
	if index < 0 || index >= ObserverSlots {
		return netip.Addr{}, fmt.Errorf("%w: observer slot %d", ErrSlotRange, index)
	}
	return offsetFrom(roomIndex, observerBaseOffset+index)
}

// AdminIP returns the address for a moderator's reserved seat.
func AdminIP(roomIndex, index int) (netip.Addr, error) {
	if index < 0 || index >= AdminSlots {
		return netip.Addr{}, fmt.Errorf("%w: admin slot %d", ErrSlotRange, index)
	}
	return offsetFrom(roomIndex, adminBaseOffset+index)
}

func subnetBase(roomIndex int) (netip.Addr, error) {
	if roomIndex < 0 || roomIndex >= MaxRooms {
		return netip.Addr{}, fmt.Errorf("%w: %d", ErrRoomIndexRange, roomIndex)
	}
	// Each room is 32 addresses, so eight rooms fill one third-octet step.
	offset := roomIndex * addressesPerRoom
	return netip.AddrFrom4([4]byte{10, 87, byte(offset >> 8), byte(offset & 0xFF)}), nil
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
