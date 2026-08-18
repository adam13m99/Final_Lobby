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
	"sync"

	"finallobby/protocol/crypto"
	"finallobby/relay/internal/route"
	"finallobby/relay/internal/sendq"
	"finallobby/protocol/wire"
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
}

// Server is the relay.
type Server struct {
	cfg      Config
	conn     *net.UDPConn
	table    *route.Table
	log      *slog.Logger
	sessions sync.Map // sessionID (uint32) -> *crypto.Session

	wg sync.WaitGroup
}

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
	addr, err := net.ResolveUDPAddr("udp", cfg.Listen)
	if err != nil {
		return nil, fmt.Errorf("server: resolve %q: %w", cfg.Listen, err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("server: listen: %w", err)
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

// Serve runs the read loop until ctx is cancelled, then waits for every
// per-peer writer to finish.
func (s *Server) Serve(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	closed := make(chan struct{})
	go func() {
		<-ctx.Done()
		_ = s.conn.Close()
		close(closed)
	}()

	buf := make([]byte, maxDatagram)
	for {
		n, from, err := s.conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			s.log.Warn("read error", "err", err)
			continue
		}
		s.handle(ctx, buf[:n], from)
	}

	cancel()
	<-closed
	s.wg.Wait()
	return nil
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
		}
	case wire.TypeDisconnect:
		s.dropSession(h.SessionID)
	}
}

func (s *Server) handleHandshake(ctx context.Context, msg1 []byte, from netip.AddrPort) {
	ticket, reply, err := crypto.ServerHandshake(s.cfg.StaticPriv, msg1)
	if err != nil {
		s.log.Debug("handshake rejected", "from", from)
		return
	}
	claims, err := s.cfg.ValidateTicket(ticket)
	if err != nil {
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
	s.log.Info("peer connected", "vip", claims.VirtualIP, "room", claims.RoomID, "session", sessionID)
}

func (s *Server) handleData(h wire.Header, pkt []byte, from netip.AddrPort) {
	sender, peer, ok := s.table.SenderFor(h.SessionID)
	if !ok {
		return
	}
	sessAny, ok := s.sessions.Load(h.SessionID)
	if !ok {
		return
	}
	sess := sessAny.(*crypto.Session)

	_, inner, err := sess.Open(pkt)
	if err != nil {
		return
	}
	// Keep the peer's remote address current so NAT rebinding survives.
	peer.SetRemote(from)

	decision := route.Decide(sender, inner, route.Options{AllowMulticast: s.cfg.AllowMulticast})

	switch decision.Verdict {
	case route.VerdictForward:
		dst, ok := s.table.ForwardTarget(decision.Dst, sender.RoomID)
		if !ok {
			return // unknown peer, or a different room: drop
		}
		dst.Queue.Push(inner)
	case route.VerdictFanout:
		for _, m := range s.table.RoomMembers(sender.RoomID) {
			if m.SessionID != peer.SessionID {
				m.Queue.Push(inner)
			}
		}
	default:
		// dropped
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
