package room

import (
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"time"

	"finallobby/coordinator/internal/ipam"
)

var (
	ErrNoRoomIndexes = errors.New("room: no free room index")
	ErrNotFound      = errors.New("room: no such room")
	ErrNotMember     = errors.New("room: not in that room")
)

// Membership is what a player needs in order to connect.
type Membership struct {
	RoomID    string
	Slot      int
	VirtualIP netip.Addr
	HostIP    netip.Addr
	Subnet    netip.Prefix
	IsHost    bool

	// IsSpectator marks the reserved admin seat, which sits outside the ten
	// playing slots and gets an address from the top of the room's block.
	IsSpectator bool
}

// Store holds every live room and serialises access to them.
//
// In memory for now. PostgreSQL persistence is sub-project 2 - a coordinator
// restart during the two-PC test costs a reconnect, and paying for schema
// migrations before the network is proven is how the predecessor spent two
// months without launching a game.
type Store struct {
	mu      sync.Mutex
	rooms   map[string]*Room
	indexes map[int]string // room index -> room ID
	nextID  int
}

func NewStore() *Store {
	return &Store{
		rooms:   make(map[string]*Room),
		indexes: make(map[int]string),
	}
}

// Create opens a new room with hostID in slot 0.
func (s *Store) Create(hostID, name string, now time.Time) (*Room, Membership, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	index, ok := s.freeIndexLocked()
	if !ok {
		return nil, Membership{}, ErrNoRoomIndexes
	}
	s.nextID++
	id := fmt.Sprintf("r%d-%d", now.Unix()%100000, s.nextID)

	r := NewRoom(id, index, hostID, now)
	r.Name = name
	s.rooms[id] = r
	s.indexes[index] = id

	m, err := membershipFor(r, 0, true)
	if err != nil {
		return nil, Membership{}, err
	}
	return r, m, nil
}

func (s *Store) freeIndexLocked() (int, bool) {
	for i := 0; i < ipam.MaxRooms; i++ {
		if _, taken := s.indexes[i]; !taken {
			return i, true
		}
	}
	return 0, false
}

// Join seats a player and returns what they need to connect.
func (s *Store) Join(roomID, playerID string, now time.Time) (Membership, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.rooms[roomID]
	if !ok {
		return Membership{}, ErrNotFound
	}
	slot, err := r.Join(playerID, now)
	if err != nil {
		return Membership{}, err
	}
	return membershipFor(r, slot, playerID == r.HostID)
}

// Membership returns what an already-seated player needs in order to
// connect, without changing the room.
//
// It exists because a ticket is minted when a player joins and dies ten
// minutes later, while a room full of people arranging a match sits open for
// far longer. Without this the ticket a player is holding by the time they
// press Connect is long expired, the relay refuses the handshake, and Join
// will not issue another because they are already in the room - so the only
// escape was to leave and rejoin. Connect asks for a fresh ticket instead.
func (s *Store) Membership(roomID, playerID string) (Membership, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.rooms[roomID]
	if !ok {
		return Membership{}, ErrNotFound
	}
	slot, spectator, seated := r.SlotOf(playerID)
	if !seated {
		return Membership{}, ErrNotMember
	}
	if spectator {
		return spectatorMembershipFor(r, slot)
	}
	return membershipFor(r, slot, playerID == r.HostID)
}

// JoinSpectator seats an admin in the reserved spectator area.
func (s *Store) JoinSpectator(roomID, playerID string, now time.Time) (Membership, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.rooms[roomID]
	if !ok {
		return Membership{}, ErrNotFound
	}
	seat, err := r.JoinSpectator(playerID, now)
	if err != nil {
		return Membership{}, err
	}
	return spectatorMembershipFor(r, seat)
}

// Leave vacates a player's slot.
func (s *Store) Leave(roomID, playerID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rooms[roomID]
	if !ok {
		return ErrNotFound
	}
	r.Leave(playerID, now)
	return nil
}

// Kick removes a player and bars them, host only.
func (s *Store) Kick(roomID, actorID, targetID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rooms[roomID]
	if !ok {
		return ErrNotFound
	}
	return r.Kick(actorID, targetID, now)
}

// SetStatus changes a room's admission state, host only.
func (s *Store) SetStatus(roomID, actorID string, st Status, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rooms[roomID]
	if !ok {
		return ErrNotFound
	}
	return r.SetStatus(actorID, st, now)
}

// Get returns a copy of a room's public state.
func (s *Store) Get(roomID string) (Room, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rooms[roomID]
	if !ok {
		return Room{}, ErrNotFound
	}
	return *r, nil
}

// List returns every room that still accepts attention, newest first.
func (s *Store) List() []Room {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Room, 0, len(s.rooms))
	for _, r := range s.rooms {
		if r.Status == StatusClosed {
			continue
		}
		out = append(out, *r)
	}
	return out
}

// Tick advances every room's timers and removes rooms that have closed.
// Returns the IDs of rooms that closed on this tick, so their tickets can be
// revoked.
func (s *Store) Tick(now time.Time) []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	var closed []string
	for id, r := range s.rooms {
		was := r.Status
		r.Tick(now)
		if r.Status == StatusClosed && was != StatusClosed {
			closed = append(closed, id)
		}
		// Keep a closed room around briefly so players see why it ended,
		// then release its index for reuse.
		if r.Status == StatusClosed && now.After(r.HostGraceUntil.Add(5*time.Minute)) {
			delete(s.rooms, id)
			delete(s.indexes, r.Index)
		}
	}
	return closed
}

// spectatorMembershipFor derives the addressing for the reserved admin seat.
func spectatorMembershipFor(r *Room, seat int) (Membership, error) {
	vip, err := ipam.SpectatorIP(r.Index, seat)
	if err != nil {
		return Membership{}, err
	}
	host, err := ipam.HostIP(r.Index)
	if err != nil {
		return Membership{}, err
	}
	subnet, err := ipam.RoomSubnet(r.Index)
	if err != nil {
		return Membership{}, err
	}
	return Membership{
		RoomID:      r.ID,
		Slot:        seat,
		VirtualIP:   vip,
		HostIP:      host,
		Subnet:      subnet,
		IsSpectator: true,
	}, nil
}

// membershipFor derives the addressing a seated player needs.
func membershipFor(r *Room, slot int, isHost bool) (Membership, error) {
	vip, err := ipam.SlotIP(r.Index, slot)
	if err != nil {
		return Membership{}, err
	}
	host, err := ipam.HostIP(r.Index)
	if err != nil {
		return Membership{}, err
	}
	subnet, err := ipam.RoomSubnet(r.Index)
	if err != nil {
		return Membership{}, err
	}
	return Membership{
		RoomID:    r.ID,
		Slot:      slot,
		VirtualIP: vip,
		HostIP:    host,
		Subnet:    subnet,
		IsHost:    isHost,
	}, nil
}
