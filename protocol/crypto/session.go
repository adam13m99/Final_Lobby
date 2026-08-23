// Package crypto provides the per-session AEAD used for tunnel data.
//
// The packet header travels in the clear but is authenticated as additional
// data, so the sequence number that seeds the nonce cannot be altered
// without detection.
package crypto

import (
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"sync"

	"golang.org/x/crypto/chacha20poly1305"

	"lobbybaz/protocol/wire"
)

var (
	ErrKeySize = errors.New("crypto: key must be 32 bytes")
	ErrAuth    = errors.New("crypto: authentication failed")
	ErrReplay  = errors.New("crypto: replayed or too-old sequence number")
)

// replayWindow is how far behind the highest accepted sequence a packet may
// arrive and still be accepted. Reordering is routine on a lossy link.
const replayWindow = 64

// Session holds the directional keys for one peer connection.
type Session struct {
	send, recv cipher.AEAD

	mu      sync.Mutex
	highest uint64
	bitmap  uint64
	seen    bool
}

// NewSession builds a session from two 32-byte directional keys.
func NewSession(sendKey, recvKey []byte) (*Session, error) {
	if len(sendKey) != chacha20poly1305.KeySize || len(recvKey) != chacha20poly1305.KeySize {
		return nil, ErrKeySize
	}
	s, err := chacha20poly1305.New(sendKey)
	if err != nil {
		return nil, err
	}
	r, err := chacha20poly1305.New(recvKey)
	if err != nil {
		return nil, err
	}
	return &Session{send: s, recv: r}, nil
}

func nonceFor(seq uint64) []byte {
	var n [chacha20poly1305.NonceSize]byte // 12 bytes
	binary.BigEndian.PutUint64(n[4:], seq)
	return n[:]
}

// Seal encrypts plaintext and returns header||ciphertext appended to dst.
func (s *Session) Seal(dst []byte, h wire.Header, plaintext []byte) ([]byte, error) {
	hdr := make([]byte, wire.HeaderSize)
	wire.EncodeHeader(hdr, h)
	out := append(dst, hdr...)
	return s.send.Seal(out, nonceFor(h.Sequence), plaintext, hdr), nil
}

// Open authenticates and decrypts a packet, enforcing the replay window.
func (s *Session) Open(packet []byte) (wire.Header, []byte, error) {
	h, err := wire.DecodeHeader(packet)
	if err != nil {
		return wire.Header{}, nil, err
	}
	hdr := packet[:wire.HeaderSize]
	plaintext, err := s.recv.Open(nil, nonceFor(h.Sequence), packet[wire.HeaderSize:], hdr)
	if err != nil {
		return wire.Header{}, nil, ErrAuth
	}
	if err := s.checkReplay(h.Sequence); err != nil {
		return wire.Header{}, nil, err
	}
	return h, plaintext, nil
}

// checkReplay implements a sliding bitmap window.
func (s *Session) checkReplay(seq uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.seen {
		s.seen, s.highest, s.bitmap = true, seq, 1
		return nil
	}
	switch {
	case seq > s.highest:
		shift := seq - s.highest
		if shift >= 64 {
			s.bitmap = 0
		} else {
			s.bitmap <<= shift
		}
		s.bitmap |= 1
		s.highest = seq
		return nil
	default:
		diff := s.highest - seq
		if diff >= replayWindow {
			return ErrReplay
		}
		mask := uint64(1) << diff
		if s.bitmap&mask != 0 {
			return ErrReplay
		}
		s.bitmap |= mask
		return nil
	}
}
