package crypto_test

import (
	"bytes"
	"errors"
	"testing"

	"lobbybaz/protocol/crypto"
	"lobbybaz/protocol/wire"
)

func pair(t *testing.T) (client, server *crypto.Session) {
	t.Helper()
	k1 := bytes.Repeat([]byte{0xA1}, 32)
	k2 := bytes.Repeat([]byte{0xB2}, 32)
	c, err := crypto.NewSession(k1, k2)
	if err != nil {
		t.Fatal(err)
	}
	s, err := crypto.NewSession(k2, k1) // mirrored
	if err != nil {
		t.Fatal(err)
	}
	return c, s
}

func TestSealOpenRoundTrip(t *testing.T) {
	client, server := pair(t)
	h := wire.Header{Version: wire.ProtocolVersion, Type: wire.TypeData, SessionID: 7, Sequence: 1}
	msg := []byte("hello dota")

	sealed, err := client.Seal(nil, h, msg)
	if err != nil {
		t.Fatal(err)
	}
	gotH, gotMsg, err := server.Open(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if gotH != h {
		t.Fatalf("header = %+v, want %+v", gotH, h)
	}
	if !bytes.Equal(gotMsg, msg) {
		t.Fatalf("payload = %q, want %q", gotMsg, msg)
	}
}

func TestRejectsReplay(t *testing.T) {
	client, server := pair(t)
	h := wire.Header{Version: wire.ProtocolVersion, Type: wire.TypeData, SessionID: 7, Sequence: 5}
	sealed, _ := client.Seal(nil, h, []byte("x"))

	if _, _, err := server.Open(sealed); err != nil {
		t.Fatalf("first open: %v", err)
	}
	if _, _, err := server.Open(sealed); !errors.Is(err, crypto.ErrReplay) {
		t.Fatalf("second open err = %v, want ErrReplay", err)
	}
}

func TestAcceptsOutOfOrderWithinWindow(t *testing.T) {
	client, server := pair(t)
	mk := func(seq uint64) []byte {
		h := wire.Header{Version: wire.ProtocolVersion, Type: wire.TypeData, SessionID: 7, Sequence: seq}
		p, _ := client.Seal(nil, h, []byte("y"))
		return p
	}
	// Deliver 10 then 8 - reordering is normal on a lossy link and must
	// not be treated as an attack.
	if _, _, err := server.Open(mk(10)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := server.Open(mk(8)); err != nil {
		t.Fatalf("in-window reorder rejected: %v", err)
	}
}

func TestRejectsTamperedCiphertext(t *testing.T) {
	client, server := pair(t)
	h := wire.Header{Version: wire.ProtocolVersion, Type: wire.TypeData, SessionID: 7, Sequence: 1}
	sealed, _ := client.Seal(nil, h, []byte("secret"))
	sealed[len(sealed)-1] ^= 0xFF

	if _, _, err := server.Open(sealed); !errors.Is(err, crypto.ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
}

func TestRejectsTamperedHeader(t *testing.T) {
	client, server := pair(t)
	h := wire.Header{Version: wire.ProtocolVersion, Type: wire.TypeData, SessionID: 7, Sequence: 1}
	sealed, _ := client.Seal(nil, h, []byte("secret"))
	sealed[2] ^= 0xFF // flip a SessionID bit - header is authenticated

	if _, _, err := server.Open(sealed); !errors.Is(err, crypto.ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
}

func TestRejectsWrongKeySize(t *testing.T) {
	if _, err := crypto.NewSession(make([]byte, 16), make([]byte, 32)); !errors.Is(err, crypto.ErrKeySize) {
		t.Fatalf("err = %v, want ErrKeySize", err)
	}
}
