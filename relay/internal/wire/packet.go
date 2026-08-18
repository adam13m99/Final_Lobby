package wire

import (
	"encoding/binary"
	"errors"
)

// PacketType discriminates the datagram payload.
type PacketType uint8

const (
	TypeHandshakeInit PacketType = 1
	TypeHandshakeResp PacketType = 2
	TypeData          PacketType = 3
	TypeKeepalive     PacketType = 4
	TypeDisconnect    PacketType = 5
)

// HeaderSize is the fixed size of every packet header in bytes.
const HeaderSize = 14

var (
	ErrShortPacket = errors.New("wire: packet shorter than header")
	ErrBadVersion  = errors.New("wire: unsupported protocol version")
)

// Header precedes every datagram. Sequence is transmitted in the clear
// because it seeds the AEAD nonce.
type Header struct {
	Version   uint8
	Type      PacketType
	SessionID uint32
	Sequence  uint64
}

// EncodeHeader writes h into dst and returns the number of bytes written.
// dst must be at least HeaderSize long.
func EncodeHeader(dst []byte, h Header) int {
	_ = dst[HeaderSize-1] // bounds check hint
	dst[0] = h.Version
	dst[1] = byte(h.Type)
	binary.BigEndian.PutUint32(dst[2:6], h.SessionID)
	binary.BigEndian.PutUint64(dst[6:14], h.Sequence)
	return HeaderSize
}

// DecodeHeader parses a header from src.
func DecodeHeader(src []byte) (Header, error) {
	if len(src) < HeaderSize {
		return Header{}, ErrShortPacket
	}
	h := Header{
		Version:   src[0],
		Type:      PacketType(src[1]),
		SessionID: binary.BigEndian.Uint32(src[2:6]),
		Sequence:  binary.BigEndian.Uint64(src[6:14]),
	}
	if h.Version != ProtocolVersion {
		return Header{}, ErrBadVersion
	}
	return h, nil
}
