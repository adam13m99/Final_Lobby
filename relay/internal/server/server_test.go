package server_test

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	"finallobby/protocol/crypto"
	"finallobby/relay/internal/server"
	"finallobby/protocol/wire"
)

// testPeer is a minimal client used to drive the relay in tests.
type testPeer struct {
	conn   *net.UDPConn
	sess   *crypto.Session
	accept wire.Accept
	seq    uint64
}

func dialPeer(t *testing.T, addr netip.AddrPort, relayPub []byte, ticket string) *testPeer {
	t.Helper()
	conn, err := net.DialUDP("udp", nil, net.UDPAddrFromAddrPort(addr))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	msg1, finish, err := crypto.ClientHandshake(relayPub, []byte(ticket))
	if err != nil {
		t.Fatal(err)
	}
	hdr := make([]byte, wire.HeaderSize)
	wire.EncodeHeader(hdr, wire.Header{Version: wire.ProtocolVersion, Type: wire.TypeHandshakeInit})
	if _, err := conn.Write(append(hdr, msg1...)); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 2048)
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("no handshake reply: %v", err)
	}
	h, err := wire.DecodeHeader(buf[:n])
	if err != nil {
		t.Fatalf("bad reply header: %v", err)
	}
	if h.Type != wire.TypeHandshakeResp {
		t.Fatalf("reply type = %d, want TypeHandshakeResp", h.Type)
	}
	sess, payload, err := finish(buf[wire.HeaderSize:n])
	if err != nil {
		t.Fatal(err)
	}
	accept, err := wire.DecodeAccept(payload)
	if err != nil {
		t.Fatal(err)
	}
	return &testPeer{conn: conn, sess: sess, accept: accept}
}

func (p *testPeer) send(t *testing.T, inner []byte) {
	t.Helper()
	p.seq++
	h := wire.Header{
		Version:   wire.ProtocolVersion,
		Type:      wire.TypeData,
		SessionID: p.accept.SessionID,
		Sequence:  p.seq,
	}
	pkt, err := p.sess.Seal(nil, h, inner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.conn.Write(pkt); err != nil {
		t.Fatal(err)
	}
}

func (p *testPeer) recv(t *testing.T, within time.Duration) ([]byte, bool) {
	t.Helper()
	buf := make([]byte, 2048)
	_ = p.conn.SetReadDeadline(time.Now().Add(within))
	n, err := p.conn.Read(buf)
	if err != nil {
		return nil, false
	}
	_, inner, err := p.sess.Open(buf[:n])
	if err != nil {
		t.Fatalf("could not open relayed packet: %v", err)
	}
	return inner, true
}

func ipv4(src, dst netip.Addr) []byte {
	p := make([]byte, 20)
	p[0] = 0x45
	s, d := src.As4(), dst.As4()
	copy(p[12:16], s[:])
	copy(p[16:20], d[:])
	return p
}

func startRelay(t *testing.T) (*server.Server, []byte) {
	t.Helper()
	pub, priv, err := crypto.GenerateStaticKeypair()
	if err != nil {
		t.Fatal(err)
	}
	// Ticket string is "roomID|virtualIP" for the test.
	validate := func(ticket []byte) (server.TicketClaims, error) {
		var room, ip string
		for i, c := range ticket {
			if c == '|' {
				room, ip = string(ticket[:i]), string(ticket[i+1:])
				break
			}
		}
		addr, err := netip.ParseAddr(ip)
		if err != nil {
			return server.TicketClaims{}, err
		}
		return server.TicketClaims{RoomID: room, VirtualIP: addr}, nil
	}
	srv, err := server.New(server.Config{
		Listen:         "127.0.0.1:0",
		StaticPriv:     priv,
		QueueDepth:     64,
		ValidateTicket: validate,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = srv.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return srv, pub
}

func TestHandshakeReplyCarriesAssignment(t *testing.T) {
	srv, pub := startRelay(t)
	alice := dialPeer(t, srv.LocalAddr(), pub, "room-a|10.87.0.2")

	if alice.accept.SessionID == 0 {
		t.Error("relay assigned session ID 0; the client cannot address packets")
	}
	if alice.accept.VirtualIP != netip.MustParseAddr("10.87.0.2") {
		t.Errorf("assigned VIP = %s, want 10.87.0.2", alice.accept.VirtualIP)
	}
	if alice.accept.RoomID != "room-a" {
		t.Errorf("assigned room = %q, want room-a", alice.accept.RoomID)
	}
}

func TestRelayForwardsBetweenRoomMembers(t *testing.T) {
	srv, pub := startRelay(t)

	alice := dialPeer(t, srv.LocalAddr(), pub, "room-a|10.87.0.2")
	bob := dialPeer(t, srv.LocalAddr(), pub, "room-a|10.87.0.3")

	alice.send(t, ipv4(netip.MustParseAddr("10.87.0.2"), netip.MustParseAddr("10.87.0.3")))

	got, ok := bob.recv(t, 2*time.Second)
	if !ok {
		t.Fatal("bob did not receive alice's packet")
	}
	if len(got) < 20 {
		t.Fatalf("received %d bytes, want a full IPv4 header", len(got))
	}
}

func TestRelayDoesNotLeakAcrossRooms(t *testing.T) {
	srv, pub := startRelay(t)

	alice := dialPeer(t, srv.LocalAddr(), pub, "room-a|10.87.0.2")
	eve := dialPeer(t, srv.LocalAddr(), pub, "room-b|10.87.1.2")

	// Alice addresses eve's virtual IP directly. Different room: must not arrive.
	alice.send(t, ipv4(netip.MustParseAddr("10.87.0.2"), netip.MustParseAddr("10.87.1.2")))

	if _, ok := eve.recv(t, 700*time.Millisecond); ok {
		t.Fatal("ROOM ISOLATION BREACH: eve received a packet from another room")
	}
}

func TestRelayDropsBroadcast(t *testing.T) {
	srv, pub := startRelay(t)

	alice := dialPeer(t, srv.LocalAddr(), pub, "room-a|10.87.0.2")
	bob := dialPeer(t, srv.LocalAddr(), pub, "room-a|10.87.0.3")

	alice.send(t, ipv4(netip.MustParseAddr("10.87.0.2"), netip.MustParseAddr("10.87.0.15")))

	if _, ok := bob.recv(t, 700*time.Millisecond); ok {
		t.Fatal("broadcast was forwarded; it must be dropped by default")
	}
}

func TestRelayDropsSpoofedSource(t *testing.T) {
	srv, pub := startRelay(t)

	alice := dialPeer(t, srv.LocalAddr(), pub, "room-a|10.87.0.2")
	bob := dialPeer(t, srv.LocalAddr(), pub, "room-a|10.87.0.3")
	carol := dialPeer(t, srv.LocalAddr(), pub, "room-a|10.87.0.4")

	// Alice claims to be carol.
	alice.send(t, ipv4(netip.MustParseAddr("10.87.0.4"), netip.MustParseAddr("10.87.0.3")))

	if _, ok := bob.recv(t, 700*time.Millisecond); ok {
		t.Fatal("spoofed packet was forwarded")
	}
	_ = carol
}

func TestRelayRejectsBadTicket(t *testing.T) {
	srv, pub := startRelay(t)

	conn, err := net.DialUDP("udp", nil, net.UDPAddrFromAddrPort(srv.LocalAddr()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	msg1, _, err := crypto.ClientHandshake(pub, []byte("garbage-ticket"))
	if err != nil {
		t.Fatal(err)
	}
	hdr := make([]byte, wire.HeaderSize)
	wire.EncodeHeader(hdr, wire.Header{Version: wire.ProtocolVersion, Type: wire.TypeHandshakeInit})
	if _, err := conn.Write(append(hdr, msg1...)); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 2048)
	_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	if _, err := conn.Read(buf); err == nil {
		t.Fatal("relay replied to a handshake with an invalid ticket")
	}
	if srv.Table().Count() != 0 {
		t.Fatalf("table has %d peers, want 0", srv.Table().Count())
	}
}
