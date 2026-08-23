package route

import (
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"lobbybaz/relay/internal/sendq"
)

// Peer is one authenticated, connected client.
//
// RoomID is mutated only by Table under its write lock; read it through
// Table.SenderFor rather than directly, so the read is covered by the read
// lock. The remote address is different: the reader goroutine updates it on
// every packet while the peer's writer goroutine reads it, so it is an
// atomic rather than a plain field.
type Peer struct {
	SessionID uint32
	VirtualIP netip.Addr
	RoomID    string
	Queue     *sendq.Queue

	remote atomic.Pointer[netip.AddrPort]

	// lastSeen is when a packet last arrived from this peer, as Unix
	// nanoseconds. A peer that crashes or loses power never says goodbye,
	// so silence is the only signal we get that it is gone.
	lastSeen atomic.Int64
}

// Touch records that we just heard from this peer.
func (p *Peer) Touch(now time.Time) { p.lastSeen.Store(now.UnixNano()) }

// LastSeen reports when we last heard from this peer.
func (p *Peer) LastSeen() time.Time { return time.Unix(0, p.lastSeen.Load()) }

// SetRemote records where the peer's packets are currently arriving from,
// so a NAT rebinding does not silently black-hole the return path.
func (p *Peer) SetRemote(ap netip.AddrPort) { p.remote.Store(&ap) }

// Remote returns the peer's current source address.
func (p *Peer) Remote() netip.AddrPort {
	if ap := p.remote.Load(); ap != nil {
		return *ap
	}
	return netip.AddrPort{}
}

// Table indexes peers by session and by virtual IP, and groups them by room.
// All lookups are on the packet hot path, so reads take a shared lock.
type Table struct {
	mu     sync.RWMutex
	bySess map[uint32]*Peer
	byIP   map[netip.Addr]*Peer
	byRoom map[string]map[uint32]*Peer
}

func NewTable() *Table {
	return &Table{
		bySess: make(map[uint32]*Peer),
		byIP:   make(map[netip.Addr]*Peer),
		byRoom: make(map[string]map[uint32]*Peer),
	}
}

func (t *Table) Add(p *Peer) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.bySess[p.SessionID] = p
	t.byIP[p.VirtualIP] = p
	t.addToRoomLocked(p, p.RoomID)
}

func (t *Table) addToRoomLocked(p *Peer, room string) {
	if room == "" {
		return
	}
	if t.byRoom[room] == nil {
		t.byRoom[room] = make(map[uint32]*Peer)
	}
	t.byRoom[room][p.SessionID] = p
}

func (t *Table) removeFromRoomLocked(p *Peer, room string) {
	if room == "" {
		return
	}
	if members := t.byRoom[room]; members != nil {
		delete(members, p.SessionID)
		if len(members) == 0 {
			delete(t.byRoom, room)
		}
	}
}

// RemoveBySession removes a peer and returns it, or nil if unknown.
func (t *Table) RemoveBySession(id uint32) *Peer {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.bySess[id]
	if !ok {
		return nil
	}
	delete(t.bySess, id)
	delete(t.byIP, p.VirtualIP)
	t.removeFromRoomLocked(p, p.RoomID)
	return p
}

func (t *Table) ByVirtualIP(ip netip.Addr) (*Peer, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	p, ok := t.byIP[ip]
	return p, ok
}

func (t *Table) BySession(id uint32) (*Peer, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	p, ok := t.bySess[id]
	return p, ok
}

// RoomMembers returns a snapshot of the peers in roomID.
func (t *Table) RoomMembers(roomID string) []*Peer {
	t.mu.RLock()
	defer t.mu.RUnlock()
	members := t.byRoom[roomID]
	out := make([]*Peer, 0, len(members))
	for _, p := range members {
		out = append(out, p)
	}
	return out
}

// SetRoom moves a peer between rooms. Reports whether the session existed.
func (t *Table) SetRoom(sessionID uint32, roomID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.bySess[sessionID]
	if !ok {
		return false
	}
	t.removeFromRoomLocked(p, p.RoomID)
	p.RoomID = roomID
	t.addToRoomLocked(p, roomID)
	return true
}

// SenderFor returns a routing-decision snapshot for a session, taken under
// the read lock so RoomID cannot change mid-read.
func (t *Table) SenderFor(id uint32) (Sender, *Peer, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	p, ok := t.bySess[id]
	if !ok {
		return Sender{}, nil, false
	}
	return Sender{VirtualIP: p.VirtualIP, RoomID: p.RoomID}, p, true
}

// ForwardTarget resolves a destination virtual IP, but only within the
// sender's own room. Doing the lookup and the room check under one lock is
// what makes cross-room isolation atomic rather than best-effort.
func (t *Table) ForwardTarget(dst netip.Addr, room string) (*Peer, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	p, ok := t.byIP[dst]
	if !ok || p.RoomID != room || room == "" {
		return nil, false
	}
	return p, true
}

// IdleSince returns every peer we have not heard from since cutoff.
//
// Without this a session lives forever: the relay only removes a peer that
// politely disconnects, and a crashed client never does. Each stale entry
// costs memory, a claimed virtual address, and a writer goroutine that will
// never write again.
func (t *Table) IdleSince(cutoff time.Time) []*Peer {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var out []*Peer
	for _, p := range t.bySess {
		if p.LastSeen().Before(cutoff) {
			out = append(out, p)
		}
	}
	return out
}

func (t *Table) Count() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.bySess)
}
