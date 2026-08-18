// Package tunnel connects the local Wintun adapter to the relay.
//
// The adapter is deliberately never torn down here. A player whose link
// blips keeps the same virtual address and the same open adapter, so Dota's
// own reconnect resumes as though nothing happened. Recreating the adapter
// would drop Dota's socket and end the match for that player.
package tunnel

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"finallobby/protocol/crypto"
	"finallobby/protocol/wire"
)

// State is the tunnel's connection state.
type State int32

const (
	StateConnecting State = iota
	StateConnected
	// StateRevoked means the coordinator refused the ticket. Retrying will
	// not help, so the client stops rather than hammering the relay.
	StateRevoked
)

func (s State) String() string {
	switch s {
	case StateConnecting:
		return "connecting"
	case StateConnected:
		return "connected"
	case StateRevoked:
		return "revoked"
	}
	return "unknown"
}

const (
	// handshakeTimeout bounds how long one connect attempt waits for the
	// relay's reply before giving up and backing off.
	handshakeTimeout = 3 * time.Second
	// keepaliveInterval keeps the NAT mapping alive on a quiet tunnel.
	// Home routers commonly expire UDP mappings after 30 seconds.
	keepaliveInterval = 15 * time.Second
	// readTimeout declares the session dead if the relay goes silent. It is
	// generous: a Dota match is a constant packet stream, so real silence
	// this long means the path is gone.
	readTimeout = 30 * time.Second
	// maxDatagram matches the relay's read buffer.
	maxDatagram = 1600
)

// ErrRevoked is returned when the relay will not accept our ticket.
var ErrRevoked = errors.New("tunnel: session revoked")

// BackoffPolicy bounds reconnect attempts.
type BackoffPolicy struct {
	Initial time.Duration
	Max     time.Duration
}

// Next returns the delay following current.
func (b BackoffPolicy) Next(current time.Duration) time.Duration {
	if current == 0 {
		return b.Initial
	}
	if doubled := current * 2; doubled < b.Max {
		return doubled
	}
	return b.Max
}

// PacketDevice is the subset of the adapter the tunnel needs. Tests
// substitute a fake.
type PacketDevice interface {
	Read(buf []byte) (int, error)
	Write(pkt []byte) error
}

// Config configures a Client.
type Config struct {
	RelayAddr string
	RelayPub  []byte
	Ticket    []byte
	Adapter   PacketDevice
	Backoff   BackoffPolicy
	Logger    *slog.Logger
}

// Client maintains one tunnel to the relay.
type Client struct {
	cfg   Config
	log   *slog.Logger
	state atomic.Int32

	// vip is the virtual address the relay assigned. It is published for the
	// UI and for the Dota launcher, which needs it to build the connect
	// string.
	vip atomic.Pointer[netip.Addr]

	// outbound carries packets from the single long-lived adapter reader to
	// whichever connection attempt is currently live. One reader for the
	// client's whole life: re-spawning it per attempt would leak a goroutine
	// blocked in adapter.Read every time the link blipped.
	outbound  chan []byte
	readerOne sync.Once
}

// New builds a Client. It does not touch the network.
func New(cfg Config) *Client {
	if cfg.Backoff.Initial == 0 {
		cfg.Backoff.Initial = 500 * time.Millisecond
	}
	if cfg.Backoff.Max == 0 {
		cfg.Backoff.Max = 15 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Client{
		cfg:      cfg,
		log:      cfg.Logger,
		outbound: make(chan []byte, 256),
	}
}

// State reports the current connection state.
func (c *Client) State() State { return State(c.state.Load()) }

func (c *Client) setState(s State) { c.state.Store(int32(s)) }

// VirtualIP returns the address the relay assigned, or the zero value if no
// handshake has completed yet.
func (c *Client) VirtualIP() netip.Addr {
	if p := c.vip.Load(); p != nil {
		return *p
	}
	return netip.Addr{}
}

// Run maintains the tunnel until ctx is cancelled.
func (c *Client) Run(ctx context.Context) error {
	c.readerOne.Do(func() { go c.readAdapter(ctx) })

	var backoff time.Duration
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		c.setState(StateConnecting)

		err := c.connectOnce(ctx)
		switch {
		case err == nil, errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			return ctx.Err()
		case errors.Is(err, ErrRevoked):
			// Backing off forever against a revoked ticket is pointless and
			// looks like an attack from the relay's side.
			c.setState(StateRevoked)
			return err
		}

		c.log.Debug("tunnel attempt failed", "err", err)
		backoff = c.cfg.Backoff.Next(backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
	}
}

// readAdapter pumps packets off the adapter for the client's whole lifetime.
// While disconnected, packets are dropped: this is game traffic, and a stale
// packet delivered late is worse than one never delivered.
func (c *Client) readAdapter(ctx context.Context) {
	for {
		buf := make([]byte, maxDatagram)
		n, err := c.cfg.Adapter.Read(buf)
		if err != nil {
			return
		}
		if ctx.Err() != nil {
			return
		}
		if n == 0 {
			continue
		}
		select {
		case c.outbound <- buf[:n]:
		default: // queue full: drop the oldest-equivalent, i.e. this one
		}
	}
}

// connectOnce performs one handshake and pumps packets until the session
// fails. It returns nil only on clean shutdown.
func (c *Client) connectOnce(ctx context.Context) error {
	conn, err := net.Dial("udp", c.cfg.RelayAddr)
	if err != nil {
		return fmt.Errorf("tunnel: dial %s: %w", c.cfg.RelayAddr, err)
	}
	udp, ok := conn.(*net.UDPConn)
	if !ok {
		conn.Close()
		return errors.New("tunnel: not a UDP connection")
	}

	// Cancelling ctx must interrupt a blocking read, so close the socket
	// when it fires.
	attemptCtx, cancelAttempt := context.WithCancel(ctx)
	defer cancelAttempt()
	go func() {
		<-attemptCtx.Done()
		_ = udp.Close()
	}()

	sess, accept, err := c.handshake(udp)
	if err != nil {
		return err
	}

	vip := accept.VirtualIP
	c.vip.Store(&vip)
	c.setState(StateConnected)
	c.log.Info("tunnel connected",
		"relay", c.cfg.RelayAddr, "vip", vip, "room", accept.RoomID, "session", accept.SessionID)

	// Either pump failing ends the attempt; Run then reconnects.
	errc := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); errc <- c.pumpOutbound(attemptCtx, udp, sess, accept.SessionID) }()
	go func() { defer wg.Done(); errc <- c.pumpInbound(attemptCtx, udp, sess) }()

	err = <-errc
	cancelAttempt()
	wg.Wait()
	return err
}

// handshake completes Noise NK and returns the session plus the relay's
// assignment.
func (c *Client) handshake(udp *net.UDPConn) (*crypto.Session, wire.Accept, error) {
	msg1, finish, err := crypto.ClientHandshake(c.cfg.RelayPub, c.cfg.Ticket)
	if err != nil {
		return nil, wire.Accept{}, err
	}
	out := make([]byte, wire.HeaderSize, wire.HeaderSize+len(msg1))
	wire.EncodeHeader(out, wire.Header{
		Version: wire.ProtocolVersion,
		Type:    wire.TypeHandshakeInit,
	})
	if _, err := udp.Write(append(out, msg1...)); err != nil {
		return nil, wire.Accept{}, err
	}

	buf := make([]byte, maxDatagram)
	if err := udp.SetReadDeadline(time.Now().Add(handshakeTimeout)); err != nil {
		return nil, wire.Accept{}, err
	}
	n, err := udp.Read(buf)
	if err != nil {
		// No reply covers both a dead relay and a ticket the relay refused:
		// the relay stays silent on rejection rather than confirming which
		// tickets exist.
		return nil, wire.Accept{}, fmt.Errorf("tunnel: no handshake reply: %w", err)
	}
	h, err := wire.DecodeHeader(buf[:n])
	if err != nil {
		return nil, wire.Accept{}, err
	}
	if h.Type != wire.TypeHandshakeResp {
		return nil, wire.Accept{}, fmt.Errorf("tunnel: unexpected reply type %d", h.Type)
	}
	sess, payload, err := finish(buf[wire.HeaderSize:n])
	if err != nil {
		return nil, wire.Accept{}, err
	}
	accept, err := wire.DecodeAccept(payload)
	if err != nil {
		return nil, wire.Accept{}, err
	}
	if !accept.VirtualIP.Is4() {
		return nil, wire.Accept{}, errors.New("tunnel: relay assigned no virtual IP")
	}
	return sess, accept, nil
}

// pumpOutbound seals adapter packets and sends them to the relay, and emits
// keepalives so the NAT mapping survives a quiet moment.
func (c *Client) pumpOutbound(ctx context.Context, udp *net.UDPConn, sess *crypto.Session, sessionID uint32) error {
	var seq uint64
	ticker := time.NewTicker(keepaliveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case pkt := <-c.outbound:
			seq++
			out, err := sess.Seal(nil, wire.Header{
				Version:   wire.ProtocolVersion,
				Type:      wire.TypeData,
				SessionID: sessionID,
				Sequence:  seq,
			}, pkt)
			if err != nil {
				return err
			}
			if _, err := udp.Write(out); err != nil {
				return err
			}

		case <-ticker.C:
			hdr := make([]byte, wire.HeaderSize)
			wire.EncodeHeader(hdr, wire.Header{
				Version:   wire.ProtocolVersion,
				Type:      wire.TypeKeepalive,
				SessionID: sessionID,
			})
			if _, err := udp.Write(hdr); err != nil {
				return err
			}
		}
	}
}

// pumpInbound opens relayed packets and injects them into the adapter.
func (c *Client) pumpInbound(ctx context.Context, udp *net.UDPConn, sess *crypto.Session) error {
	buf := make([]byte, maxDatagram)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := udp.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
			return err
		}
		n, err := udp.Read(buf)
		if err != nil {
			return err
		}
		h, err := wire.DecodeHeader(buf[:n])
		if err != nil || h.Type != wire.TypeData {
			continue
		}
		_, inner, err := sess.Open(buf[:n])
		if err != nil {
			// A forged or replayed packet. Drop it and keep going; one bad
			// datagram must not end a live match.
			continue
		}
		if err := c.cfg.Adapter.Write(inner); err != nil {
			return err
		}
	}
}
