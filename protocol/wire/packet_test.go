package wire_test

import (
	"errors"
	"testing"

	"lobbybaz/protocol/wire"
)

func TestHeaderRoundTrip(t *testing.T) {
	in := wire.Header{
		Version:   wire.ProtocolVersion,
		Type:      wire.TypeData,
		SessionID: 0xDEADBEEF,
		Sequence:  0x0102030405060708,
	}
	buf := make([]byte, wire.HeaderSize)
	if n := wire.EncodeHeader(buf, in); n != wire.HeaderSize {
		t.Fatalf("EncodeHeader wrote %d bytes, want %d", n, wire.HeaderSize)
	}
	out, err := wire.DecodeHeader(buf)
	if err != nil {
		t.Fatalf("DecodeHeader: %v", err)
	}
	if out != in {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", out, in)
	}
}

func TestDecodeHeaderRejectsShortPacket(t *testing.T) {
	_, err := wire.DecodeHeader(make([]byte, wire.HeaderSize-1))
	if !errors.Is(err, wire.ErrShortPacket) {
		t.Fatalf("err = %v, want ErrShortPacket", err)
	}
}

func TestDecodeHeaderRejectsWrongVersion(t *testing.T) {
	buf := make([]byte, wire.HeaderSize)
	wire.EncodeHeader(buf, wire.Header{Version: 99, Type: wire.TypeData})
	_, err := wire.DecodeHeader(buf)
	if !errors.Is(err, wire.ErrBadVersion) {
		t.Fatalf("err = %v, want ErrBadVersion", err)
	}
}
