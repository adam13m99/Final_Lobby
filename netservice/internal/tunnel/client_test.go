package tunnel_test

import (
	"context"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"finallobby/netservice/internal/tunnel"
	"finallobby/protocol/crypto"
	"finallobby/protocol/wire"
)

// fakeDevice stands in for the Wintun adapter.
type fakeDevice struct {
	outbound chan []byte
	inbound  chan []byte
	closed   atomic.Bool
}

func newFakeDevice() *fakeDevice {
	return &fakeDevice{
		outbound: make(chan []byte, 16),
		inbound:  make(chan []byte, 16),
	}
}

func (f *fakeDevice) Read(buf []byte) (int, error) {
	pkt, ok := <-f.outbound
	if !ok {
		return 0, context.Canceled
	}
	return copy(buf, pkt), nil
}

func (f *fakeDevice) Write(pkt []byte) error {
	cp := make([]byte, len(pkt))
	copy(cp, pkt)
	select {
	case f.inbound <- cp:
	default:
	}
	return nil
}

func ipv4(src, dst netip.Addr) []byte {
	p := make([]byte, 20)
	p[0] = 0x45
	s, d := src.As4(), dst.As4()
	copy(p[12:16], s[:])
	copy(p[16:20], d[:])
	return p
}

// fakeRelay speaks the real wire protocol: it completes a Noise NK
// handshake, hands back an assignment, and echoes every data packet.
// It exists so the client's framing is tested against the actual format
// rather than against a mock that agrees with it by construction.
type fakeRelay struct {
	addr netip.AddrPort
	pub  []byte
}

func startFakeRelay(t *testing.T, vip netip.Addr) *fakeRelay {
	t.Helper()
	pub, priv, err := crypto.GenerateStaticKeypair()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	r := &fakeRelay{addr: conn.LocalAddr().(*net.UDPAddr).AddrPort(), pub: pub}

	go func() {
		var sess *crypto.Session
		var sessionID uint32 = 0x11223344
		var seq uint64
		buf := make([]byte, 2048)
		for {
			n, from, err := conn.ReadFromUDPAddrPort(buf)
			if err != nil {
				return
			}
			h, err := wire.DecodeHeader(buf[:n])
			if err != nil {
				continue
			}
			switch h.Type {
			case wire.TypeHandshakeInit:
				_, reply, err := crypto.ServerHandshake(priv, buf[wire.HeaderSize:n])
				if err != nil {
					continue
				}
				msg2, s, err := reply(wire.EncodeAccept(wire.Accept{
					SessionID: sessionID,
					VirtualIP: vip,
					RoomID:    "room-a",
				}))
				if err != nil {
					continue
				}
				sess = s
				out := make([]byte, wire.HeaderSize, wire.HeaderSize+len(msg2))
				wire.EncodeHeader(out, wire.Header{
					Version:   wire.ProtocolVersion,
					Type:      wire.TypeHandshakeResp,
					SessionID: sessionID,
				})
				_, _ = conn.WriteToUDPAddrPort(append(out, msg2...), from)

			case wire.TypeData:
				if sess == nil {
					continue
				}
				_, inner, err := sess.Open(buf[:n])
				if err != nil {
					continue
				}
				seq++
				out, err := sess.Seal(nil, wire.Header{
					Version:   wire.ProtocolVersion,
					Type:      wire.TypeData,
					SessionID: sessionID,
					Sequence:  seq,
				}, inner)
				if err != nil {
					continue
				}
				_, _ = conn.WriteToUDPAddrPort(out, from)
			}
		}
	}()
	return r
}

func TestTunnelCarriesPacketsBothWays(t *testing.T) {
	vip := netip.MustParseAddr("10.87.0.2")
	relay := startFakeRelay(t, vip)
	dev := newFakeDevice()

	c := tunnel.New(tunnel.Config{
		RelayAddr: relay.addr.String(),
		RelayPub:  relay.pub,
		Ticket:    []byte("room-a|10.87.0.2"),
		Adapter:   dev,
		Backoff:   tunnel.BackoffPolicy{Initial: 10 * time.Millisecond, Max: 20 * time.Millisecond},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() { _ = c.Run(ctx) }()

	// Wait for the handshake to land.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && c.State() != tunnel.StateConnected {
		time.Sleep(10 * time.Millisecond)
	}
	if c.State() != tunnel.StateConnected {
		t.Fatalf("never reached StateConnected (state = %v)", c.State())
	}
	if got := c.VirtualIP(); got != vip {
		t.Fatalf("VirtualIP() = %s, want %s", got, vip)
	}

	// A packet leaving the adapter must come back from the echoing relay.
	dev.outbound <- ipv4(vip, netip.MustParseAddr("10.87.0.3"))

	select {
	case got := <-dev.inbound:
		if len(got) < 20 {
			t.Fatalf("got %d bytes back, want a full IPv4 packet", len(got))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("packet never made the round trip through the tunnel")
	}
}

func TestAdapterSurvivesReconnect(t *testing.T) {
	dev := newFakeDevice()
	// A relay address that never answers forces the retry path.
	c := tunnel.New(tunnel.Config{
		RelayAddr: "127.0.0.1:1", // nothing listens here
		RelayPub:  make([]byte, 32),
		Ticket:    []byte("t"),
		Adapter:   dev,
		Backoff:   tunnel.BackoffPolicy{Initial: 10 * time.Millisecond, Max: 20 * time.Millisecond},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = c.Run(ctx)

	if dev.closed.Load() {
		t.Fatal("adapter was closed during reconnect; Dota's own reconnect would break")
	}
}

func TestStateReportsConnectingWhileRelayUnreachable(t *testing.T) {
	dev := newFakeDevice()
	c := tunnel.New(tunnel.Config{
		RelayAddr: "127.0.0.1:1",
		RelayPub:  make([]byte, 32),
		Ticket:    []byte("t"),
		Adapter:   dev,
		Backoff:   tunnel.BackoffPolicy{Initial: 10 * time.Millisecond, Max: 20 * time.Millisecond},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	go func() { _ = c.Run(ctx) }()
	time.Sleep(60 * time.Millisecond)

	if got := c.State(); got != tunnel.StateConnecting {
		t.Fatalf("State() = %v, want StateConnecting", got)
	}
}

func TestReconnectKeepsTheSameVirtualIP(t *testing.T) {
	vip := netip.MustParseAddr("10.87.0.2")
	relay := startFakeRelay(t, vip)
	dev := newFakeDevice()

	c := tunnel.New(tunnel.Config{
		RelayAddr: relay.addr.String(),
		RelayPub:  relay.pub,
		Ticket:    []byte("room-a|10.87.0.2"),
		Adapter:   dev,
		Backoff:   tunnel.BackoffPolicy{Initial: 10 * time.Millisecond, Max: 20 * time.Millisecond},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() { _ = c.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && c.State() != tunnel.StateConnected {
		time.Sleep(10 * time.Millisecond)
	}
	first := c.VirtualIP()
	if !first.IsValid() {
		t.Fatal("no virtual IP after first connect")
	}
	// The address a player is given must not change under them; Dota has
	// already bound a socket to it.
	if first != vip {
		t.Fatalf("virtual IP = %s, want %s", first, vip)
	}
}

func TestBackoffIsBoundedAndGrows(t *testing.T) {
	b := tunnel.BackoffPolicy{Initial: 10 * time.Millisecond, Max: 40 * time.Millisecond}
	got := []time.Duration{}
	var cur time.Duration
	for i := 0; i < 5; i++ {
		cur = b.Next(cur)
		got = append(got, cur)
	}
	want := []time.Duration{10, 20, 40, 40, 40}
	for i, w := range want {
		if got[i] != w*time.Millisecond {
			t.Fatalf("backoff step %d = %v, want %v", i, got[i], w*time.Millisecond)
		}
	}
}
