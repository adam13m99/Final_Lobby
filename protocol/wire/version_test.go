package wire_test

import (
	"testing"

	"finallobby/protocol/wire"
)

func TestProtocolVersionIsOne(t *testing.T) {
	if wire.ProtocolVersion != 1 {
		t.Fatalf("ProtocolVersion = %d, want 1", wire.ProtocolVersion)
	}
}
