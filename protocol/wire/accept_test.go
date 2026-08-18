package wire_test

import (
	"errors"
	"net/netip"
	"testing"

	"finallobby/protocol/wire"
)

func TestAcceptRoundTrip(t *testing.T) {
	in := wire.Accept{
		SessionID: 0xCAFEBABE,
		VirtualIP: netip.MustParseAddr("10.87.3.7"),
		RoomID:    "room-abc",
	}
	out, err := wire.DecodeAccept(wire.EncodeAccept(in))
	if err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", out, in)
	}
}

func TestAcceptRejectsRunt(t *testing.T) {
	if _, err := wire.DecodeAccept([]byte{1, 2, 3}); !errors.Is(err, wire.ErrBadAccept) {
		t.Fatalf("err = %v, want ErrBadAccept", err)
	}
}
