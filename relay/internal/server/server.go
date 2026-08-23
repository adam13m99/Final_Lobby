// Package server assembles the relay: one UDP socket, one reader goroutine,
// and one writer goroutine per connected peer.
//
// The goroutine count is deliberately proportional to the number of players
// and not to the packet rate. The ancestor platform spawned a goroutine per
// forwarded packet, which at target scale meant roughly a million goroutines
// per second and reordered game traffic as a side effect.
package server

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"lobbybaz/protocol/crypto"
	"lobbybaz/relay/internal/route"
	"lobbybaz/relay/internal/sendq"
	"lobbybaz/protocol/wire"
)

// maxDatagram bounds a single read. Wintun MTU is 1300; this leaves ample
// room for headers and the AEAD tag.
const maxDatagram = 1600

// TicketClaims is what a validated ticket authorises.
type TicketClaims struct {
	VirtualIP netip.Addr
	RoomID    string
}

// Config configures a relay.
type Config struct {
	Listen         string
	StaticPriv     []byte
	AllowMulticast bool
	QueueDepth     int
	// ValidateTicket is supplied by the coordinator integration. It must be
	// safe for concurrent use.
	ValidateTicket func(ticket []byte) (TicketClaims, error)
	Logger         *slog.Logger
	// SocketBuffer is the requested kernel socket buffer size in bytes for
	// both directions. The default 208 KB holds roughly ten milliseconds of
	// traffic at full load, so any scheduling hiccup becomes packet loss
	// the application never even sees. The kernel silently caps the request
	// at net.core.rmem_max, so what was actually granted is logged.
	SocketBuffer int
	// IdleTimeout is how long a peer may stay silent before its session is
	// dropped. Clients send a keepalive every 15 seconds, so the default
	// allows six to be missed before we give up on them.
	IdleTimeout time.Duration
	// Readers is how many goroutines pull from the socket in parallel.
	// Defaults to the CPU count. Each one decrypts and routes independently.
	Readers int
}

// Stats are the relay's packet counters. Attributing loss to a specific
// cause is the difference between diagnosing a capacity problem and
// guessing at one.
type Stats struct {
	Handshakes   atomic.Uint64
	HandshakeBad atomic.Uint64
	DataIn       atomic.Uint64
	AuthFailed   atomic.Uint64
	Forwarded    atomic.Uint64
	FannedOut    atomic.Uint64
	DroppedRoute atomic.Uint64 // spoofed, broadcast, cross-room, unknown peer
	DroppedQueue atomic.Uint64 // peer send queue was full
	WriteErrors  atomic.Uint64
	Expired      atomic.Uint64 // sessions reaped for going silent
}

// Server is the relay.
type Server struct {
	cfg      Config
	conn     *net.UDPConn
	table    *route.Table
	log      *slog.Logger
	sessions sync.Map // sessionID (uint32) -> *crypto.Session
	stats    Stats

	wg sync.WaitGroup
}

// Stats returns the live counters.
func (s *Server) Stats() *Stats { return &s.stats }

func New(cfg Config) (*Server, error) {
	if cfg.ValidateTicket == nil {
		return nil, errors.New("server: ValidateTicket is required")
	}
	if len(cfg.StaticPriv) != 32 {
		return nil, errors.New("server: StaticPriv must be 32 bytes")
	}
	if cfg.QueueDepth <= 0 {
		cfg.QueueDepth = 256
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Readers <= 0 {
		cfg.Readers = runtime.NumCPU()
	}
	if cfg.SocketBuffer <= 0 {
		cfg.SocketBuffer = 8 << 20 // 8 MiB
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 90 * time.Second
	}
	addr, err := net.ResolveUDPAddr("udp", cfg.Listen)
	if err != nil {
		return nil, fmt.Errorf("server: resolve %q: %w", cfg.Listen, err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("server: listen: %w", err)
	}
	// Best effort: a kernel that refuses the size still gives us a working
	// relay, just a more loss-prone one, so this is logged and not fatal.
	if err := conn.SetReadBuffer(cfg.SocketBuffer); err != nil {
		cfg.Logger.Warn("could not set socket read buffer", "want", cfg.SocketBuffer, "err", err)
	}
	if err := conn.SetWriteBuffer(cfg.SocketBuffer); err != nil {
		cfg.Logger.Warn("could not set socket write buffer", "want", cfg.SocketBuffer, "err", err)
	}
	if got := actualReadBuffer(conn); got > 0 && got < cfg.SocketBuffer {
		cfg.Logger.Warn("kernel capped the socket read buffer - raise net.core.rmem_max",
			"requested", cfg.SocketBuffer, "granted", got)
	}

	return &Server{
		cfg:   cfg,
		conn:  conn,
		table: route.NewTable(),
		log:   cfg.Logger,
	}, nil
}

func (s *Server) LocalAddr() netip.AddrPort {
	return s.conn.LocalAddr().(*net.UDPAddr).AddrPort()
}

func (s *Server) Table() *route.Table { return s.table }

// Serve runs the read loops until ctx is cancelled, then waits for every
// per-peer writer to finish.
//
// Reads are spread across several goroutines on the same socket. One reader
// caps out around 50,000 packets per second on a modest core - measured on
// the 4-core development box - because every datagram costs a ChaCha20
// decrypt before it can be routed. At 1500 players that ceiling produced
// 43% loss and multi-second latency. Reader count scales with CPUs, a small
// fixed number, so the rule that goroutines never scale with packet rate
// still holds.
func (s *Server) Serve(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	closed := make(chan struct{})
	go func() {
		<-ctx.Done()
		_ = s.conn.Close()
		close(closed)
	}()

	go s.reapIdle(ctx)

	var readers sync.WaitGroup
	for i := 0; i < s.cfg.Readers; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			s.readLoop(ctx)
		}()
	}
	readers.Wait()

	cancel()
	<-closed
	s.wg.Wait()
	return nil
}

// readLoop pulls datagrams off the shared socket. Concurrent reads on one
// UDP socket are safe; the kernel hands each datagram to exactly one waiter.
func (s *Server) readLoop(ctx context.Context) {
	buf := make([]byte, maxDatagram)
	for {
		n, from, err := s.conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			s.log.Warn("read error", "err", err)
			continue
		}
		s.handle(ctx, buf[:n], from)
	}
}

// handle dispatches on the packet type. Every datagram carries a wire
// header, including handshakes, so there is never any guessing about which
// kind of packet arrived.
func (s *Server) handle(ctx context.Context, pkt []byte, from netip.AddrPort) {
	h, err := wire.DecodeHeader(pkt)
	if err != nil {
		return
	}
	switch h.Type {
	case wire.TypeHandshakeInit:
		s.handleHandshake(ctx, pkt[wire.HeaderSize:], from)
	case wire.TypeData:
		s.handleData(h, pkt, from)
	case wire.TypeKeepalive:
		if _, peer, ok := s.table.SenderFor(h.SessionID); ok {
			peer.SetRemote(from)
			peer.Touch(time.Now())
		}
	case wire.TypeDisconnect:
		s.dropSession(h.SessionID)
	}
}

func (s *Server) handleHandshake(ctx context.Context, msg1 []byte, from netip.AddrPort) {
	ticket, reply, err := crypto.ServerHandshake(s.cfg.StaticPriv, msg1)
	if err != nil {
		s.stats.HandshakeBad.Add(1)
		s.log.Debug("handshake rejected", "from", from)
		return
	}
	claims, err := s.cfg.ValidateTicket(ticket)
	if err != nil {
		s.stats.HandshakeBad.Add(1)
		s.log.Debug("ticket rejected", "from", from)
		return
	}
	if !claims.VirtualIP.Is4() || claims.RoomID == "" {
		s.log.Debug("ticket claims incomplete", "from", from)
		return
	}

	sessionID, err := newSessionID()
	if err != nil {
		s.log.Error("session id generation failed", "err", err)
		return
	}

	msg2, sess, err := reply(wire.EncodeAccept(wire.Accept{
		SessionID: sessionID,
		VirtualIP: claims.VirtualIP,
		RoomID:    claims.RoomID,
	}))
	if err != nil {
		s.log.Debug("handshake reply failed", "from", from, "err", err)
		return
	}

	// Replace any previous session holding this virtual IP. This is what
	// makes a reconnecting player keep the same address mid-match.
	if old, ok := s.table.ByVirtualIP(claims.VirtualIP); ok {
		s.dropSession(old.SessionID)
	}

	peer := &route.Peer{
		SessionID: sessionID,
		VirtualIP: claims.VirtualIP,
		RoomID:    claims.RoomID,
		Queue:     sendq.New(s.cfg.QueueDepth),
	}
	peer.SetRemote(from)
	peer.Touch(time.Now())
	s.table.Add(peer)
	s.sessions.Store(sessionID, sess)

	out := make([]byte, wire.HeaderSize, wire.HeaderSize+len(msg2))
	wire.EncodeHeader(out, wire.Header{
		Version:   wire.ProtocolVersion,
		Type:      wire.TypeHandshakeResp,
		SessionID: sessionID,
	})
	if _, err := s.conn.WriteToUDPAddrPort(append(out, msg2...), from); err != nil {
		s.log.Warn("handshake reply send failed", "err", err)
		s.dropSession(sessionID)
		return
	}

	// One writer goroutine per peer, for the peer's lifetime.
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.writeLoop(ctx, peer, sess)
	}()
	s.stats.Handshakes.Add(1)
	s.log.Debug("peer connected", "vip", claims.VirtualIP, "room", claims.RoomID, "session", sessionID)
}

func (s *Server) handleData(h wire.Header, pkt []byte, from netip.AddrPort) {
	s.stats.DataIn.Add(1)
	sender, peer, ok := s.table.SenderFor(h.SessionID)
	if !ok {
		s.stats.DroppedRoute.Add(1)
		return
	}
	sessAny, ok := s.sessions.Load(h.SessionID)
	if !ok {
		s.stats.DroppedRoute.Add(1)
		return
	}
	sess := sessAny.(*crypto.Session)

	_, inner, err := sess.Open(pkt)
	if err != nil {
		s.stats.AuthFailed.Add(1)
		return
	}
	// Keep the peer's remote address current so NAT rebinding survives.
	peer.SetRemote(from)
	peer.Touch(time.Now())

	decision := route.Decide(sender, inner, route.Options{AllowMulticast: s.cfg.AllowMulticast})

	switch decision.Verdict {
	case route.VerdictForward:
		dst, ok := s.table.ForwardTarget(decision.Dst, sender.RoomID)
		if !ok {
			s.stats.DroppedRoute.Add(1)
			return // unknown peer, or a different room: drop
		}
		if dst.Queue.Push(inner) {
			s.stats.DroppedQueue.Add(1)
		}
		s.stats.Forwarded.Add(1)
	case route.VerdictFanout:
		for _, m := range s.table.RoomMembers(sender.RoomID) {
			if m.SessionID != peer.SessionID {
				if m.Queue.Push(inner) {
					s.stats.DroppedQueue.Add(1)
				}
			}
		}
		s.stats.FannedOut.Add(1)
	default:
		s.stats.DroppedRoute.Add(1)
	}
}

// reapIdle drops sessions we have stopped hearing from.
//
// A client that crashes, is killed, or loses power never sends a disconnect.
// Without this the relay accumulates dead sessions forever - each holding a
// virtual address and a writer goroutine that will never write again. It is
// the slow leak that only shows up after a month of uptime.
func (s *Server) reapIdle(ctx context.Context) {
	every := s.cfg.IdleTimeout / 3
	if every < time.Second {
		every = time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		cutoff := time.Now().Add(-s.cfg.IdleTimeout)
		for _, peer := range s.table.IdleSince(cutoff) {
			s.log.Info("dropping a silent peer",
				"vip", peer.VirtualIP, "session", peer.SessionID,
				"silent_for", time.Since(peer.LastSeen()).Round(time.Second))
			s.dropSession(peer.SessionID)
			s.stats.Expired.Add(1)
		}
	}
}

// dropSession removes a peer and releases its writer goroutine.
func (s *Server) dropSession(id uint32) {
	if removed := s.table.RemoveBySession(id); removed != nil {
		removed.Queue.Close()
	}
	s.sessions.Delete(id)
}

// writeLoop drains one peer's queue. Exactly one of these runs per peer,
// which is what keeps goroutine count proportional to players rather than
// to packet rate.
func (s *Server) writeLoop(ctx context.Context, peer *route.Peer, sess *crypto.Session) {
	var seq uint64
	for {
		inner, err := peer.Queue.Pop(ctx)
		if err != nil {
			return
		}
		seq++
		h := wire.Header{
			Version:   wire.ProtocolVersion,
			Type:      wire.TypeData,
			SessionID: peer.SessionID,
			Sequence:  seq,
		}
		out, err := sess.Seal(nil, h, inner)
		if err != nil {
			continue
		}
		if _, err := s.conn.WriteToUDPAddrPort(out, peer.Remote()); err != nil {
			s.stats.WriteErrors.Add(1)
			s.log.Debug("write failed", "vip", peer.VirtualIP, "err", err)
		}
	}
}

// newSessionID draws a session ID from the CSPRNG. Zero is reserved so that
// a client which has not completed a handshake cannot address anything.
func newSessionID() (uint32, error) {
	var b [4]byte
	for {
		if _, err := rand.Read(b[:]); err != nil {
			return 0, err
		}
		if id := binary.BigEndian.Uint32(b[:]); id != 0 {
			return id, nil
		}
	}
}

// actualReadBuffer reports the receive buffer the kernel really gave us.
// Linux returns double the requested value, so it is halved back here.
func actualReadBuffer(conn *net.UDPConn) int {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0
	}
	var size int
	_ = raw.Control(func(fd uintptr) {
		size, _ = getSockoptRcvbuf(fd)
	})
	return size
}
