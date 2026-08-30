// Package ipam allocates the virtual addresses used inside room networks.
//
// The platform owns 10.87.0.0/16 and gives every room a /27. Thirty-two
// addresses per room: .0 network, .1 reserved for the relay, .2-.11 the ten
// player slots, .12-.15 four observers, .17-.19 three admin seats, .20-.30
// spare, .31 broadcast. (.16 was the fifth observer until 2026-08-30 and is
// left unused on purpose - see ObserverSlots.)
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
	//
	// Four since 2026-08-30 (D68), two shown on each team board. It was five,
	// which is a number that cannot be split evenly between two sides.
	ObserverSlots = 4
	// AdminSlots sit outside both, so a full match plus a full gallery can
	// never stop a moderator getting in.
	AdminSlots = 3

	// SeatsPerRoom is what a room holds in total.
	SeatsPerRoom = PlayerSlots + ObserverSlots + AdminSlots

	// MemberSlots is how many addresses a room hands out to the people seated
	// in it: a full match plus a full gallery.
	//
	// An address belongs to the person for as long as they are in the room,
	// whatever seat they are sitting in - D74 said that of the ten playing
	// slots, and D79 says it of the watching seats too, because otherwise
	// moving to the gallery changes your address and drops your tunnel, which
	// is the exact bug D74 existed to remove. So there is one pool covering
	// both, and it has to be big enough for everybody who can be seated at
	// once. Moderators are not in it: their seat is reserved, outside both
	// areas, and a moderator never moves between kinds.
	MemberSlots = PlayerSlots + ObserverSlots

	// subnetBits is the prefix length of one room's block.
	subnetBits = 27
	// addressesPerRoom must stay 1<<(32-subnetBits).
	addressesPerRoom = 32

	playerBaseOffset   = 2
	observerBaseOffset = playerBaseOffset + PlayerSlots // 12
	// Pinned at 17 rather than derived, so that dropping the fifth observer
	// seat did not move every admin address down by one. A moderator's
	// address is carried in tickets that are already issued, and a room's
	// addressing must not depend on a product decision about seat counts.
	// The gap at .16 is the price and it is a cheap one - eleven of these
	// thirty-two addresses were already spare.
	adminBaseOffset = 17
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

// MemberIP returns the address held by one member of a room, player or
// watcher.
//
// Indices 0-9 land where SlotIP puts a player and 10-13 where ObserverIP puts
// a watcher: the two ranges were already adjacent, which is what makes one
// pool across both possible without moving a single address. Those two
// functions remain, because the addressing they describe has not changed and
// the tests that pin the layout are written in their terms.
func MemberIP(roomIndex, index int) (netip.Addr, error) {
	if index < 0 || index >= MemberSlots {
		return netip.Addr{}, fmt.Errorf("%w: member slot %d", ErrSlotRange, index)
	}
	return offsetFrom(roomIndex, playerBaseOffset+index)
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
