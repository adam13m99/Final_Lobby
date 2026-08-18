package crypto_test

import (
	"bytes"
	"testing"

	"finallobby/relay/internal/crypto"
	"finallobby/relay/internal/wire"
)

func TestHandshakeEstablishesMatchingSessions(t *testing.T) {
	pub, priv, err := crypto.GenerateStaticKeypair()
	if err != nil {
		t.Fatal(err)
	}
	ticket := []byte("ticket-abc-123")

	msg1, finish, err := crypto.ClientHandshake(pub, ticket)
	if err != nil {
		t.Fatal(err)
	}
	gotTicket, msg2, serverSess, err := crypto.ServerHandshake(priv, msg1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotTicket, ticket) {
		t.Fatalf("ticket = %q, want %q", gotTicket, ticket)
	}
	clientSess, err := finish(msg2)
	if err != nil {
		t.Fatal(err)
	}

	// Prove the two derived sessions actually interoperate.
	h := wire.Header{Version: wire.ProtocolVersion, Type: wire.TypeData, SessionID: 1, Sequence: 1}
	sealed, err := clientSess.Seal(nil, h, []byte("ping"))
	if err != nil {
		t.Fatal(err)
	}
	_, got, err := serverSess.Open(sealed)
	if err != nil {
		t.Fatalf("server could not open client packet: %v", err)
	}
	if string(got) != "ping" {
		t.Fatalf("got %q, want \"ping\"", got)
	}

	// And the reverse direction.
	sealed2, err := serverSess.Seal(nil, h, []byte("pong"))
	if err != nil {
		t.Fatal(err)
	}
	_, got2, err := clientSess.Open(sealed2)
	if err != nil {
		t.Fatalf("client could not open server packet: %v", err)
	}
	if string(got2) != "pong" {
		t.Fatalf("got %q, want \"pong\"", got2)
	}
}

func TestTicketIsNotSentInPlaintext(t *testing.T) {
	pub, _, _ := crypto.GenerateStaticKeypair()
	ticket := []byte("SUPERSECRETTICKET")

	msg1, _, err := crypto.ClientHandshake(pub, ticket)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(msg1, ticket) {
		t.Fatal("ticket appears in plaintext in the first handshake message")
	}
}

func TestServerRejectsGarbageHandshake(t *testing.T) {
	_, priv, _ := crypto.GenerateStaticKeypair()
	if _, _, _, err := crypto.ServerHandshake(priv, []byte("not a handshake")); err == nil {
		t.Fatal("expected error for malformed handshake")
	}
}

func TestClientRejectsWrongRelayKey(t *testing.T) {
	wrongPub, _, _ := crypto.GenerateStaticKeypair()
	_, realPriv, _ := crypto.GenerateStaticKeypair()

	msg1, _, err := crypto.ClientHandshake(wrongPub, []byte("t"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := crypto.ServerHandshake(realPriv, msg1); err == nil {
		t.Fatal("server accepted a handshake addressed to a different key")
	}
}
