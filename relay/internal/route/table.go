package route

import (
	"net/netip"
	"sync"

	"finallobby/relay/internal/sendq"
)

// Peer is one authenticated, connected client.
type Peer struct {
	SessionID uint32
	VirtualIP netip.Addr
	RoomID    string
	Remote    netip.AddrPort
	Queue     *sendq.Queue
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

func (t *Table) Count() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.bySess)
}
