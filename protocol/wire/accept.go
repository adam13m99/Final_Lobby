package wire

import (
	"encoding/binary"
	"errors"
	"net/netip"
)

// ErrBadAccept is returned when an accept payload cannot be parsed.
var ErrBadAccept = errors.New("wire: malformed accept payload")

// Accept is what the relay tells a client inside the encrypted handshake
// reply: which session it was given, which virtual IP it now owns, and which
// room it belongs to. The client needs the session ID to address later
// packets, and the virtual IP to configure its adapter.
type Accept struct {
	SessionID uint32
	VirtualIP netip.Addr
	RoomID    string
}

const acceptFixedLen = 4 + 4 // session ID + IPv4 address

// EncodeAccept serialises a. The room ID is the variable-length tail.
func EncodeAccept(a Accept) []byte {
	room := []byte(a.RoomID)
	out := make([]byte, acceptFixedLen+len(room))
	binary.BigEndian.PutUint32(out[0:4], a.SessionID)
	ip := a.VirtualIP.As4()
	copy(out[4:8], ip[:])
	copy(out[acceptFixedLen:], room)
	return out
}

// DecodeAccept parses an accept payload.
func DecodeAccept(b []byte) (Accept, error) {
	if len(b) < acceptFixedLen {
		return Accept{}, ErrBadAccept
	}
	return Accept{
		SessionID: binary.BigEndian.Uint32(b[0:4]),
		VirtualIP: netip.AddrFrom4([4]byte(b[4:8])),
		RoomID:    string(b[acceptFixedLen:]),
	}, nil
}
