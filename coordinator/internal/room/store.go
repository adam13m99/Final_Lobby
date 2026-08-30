package room

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/netip"
	"strings"
	"sync"
	"time"

	"lobbybaz/coordinator/internal/ipam"
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

	// Kind is which seating area this membership belongs to. Observers and
	// admins sit outside the ten playing slots and draw their addresses from
	// separate ranges (D38).
	Kind SeatKind
}

// IsSpectator reports whether this membership is anything other than a
// playing slot. Kept because the client and the relay only care about
// "playing or not"; the distinction between an observer and an admin is a
// product concern, not a networking one.
func (m Membership) IsSpectator() bool { return m.Kind != SeatPlayer }

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

	// onKick, if set, is called after a successful kick so the event can be
	// written down. It runs while the store's lock is held, so it must be
	// quick and must not call back into the store.
	onKick func(KickEvent)

	// hosts, if set, answers what the coordinator knows about a host that a
	// room cannot see for itself (D69, D70). Like onKick it runs under the
	// store's lock, so it must be a lookup and nothing more.
	hosts func(hostID string) HostFacts
}

// HostFacts is the coordinator's view of one room's host.
//
// Online is whether they have been heard from recently enough to still count
// as present; InGame is whether their own service says Dota is running. Both
// are things only the player registry knows, and neither can be inferred from
// room state - which is why they are pushed in rather than looked up.
type HostFacts struct {
	Online bool
	InGame bool
}

// WatchHosts installs the lookup that Tick uses to observe every host. Nil
// disables it, which is what a store under test does when it wants to drive
// the timers by hand.
func (s *Store) WatchHosts(fn func(hostID string) HostFacts) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hosts = fn
}

func NewStore() *Store {
	return &Store{
		rooms:   make(map[string]*Room),
		indexes: make(map[int]string),
	}
}

// KickEvent is one kick, as something that happened rather than as a live
// block. See D52: the block lives with the room, in memory; the event is what
// moderation needs months later.
type KickEvent struct {
	RoomID     string
	ActorID    string
	TargetID   string
	KickNumber int // which kick this was from this room
	BlockedFor time.Duration
	At         time.Time
}

// OnKick installs a recorder. Nil disables recording, which is what a
// coordinator running without a database does.
func (s *Store) OnKick(fn func(KickEvent)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onKick = fn
}

// Create opens a new room with hostID in slot 0.
func (s *Store) Create(hostID, name string, now time.Time) (*Room, Membership, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	index, ok := s.freeIndexLocked()
	if !ok {
		return nil, Membership{}, ErrNoRoomIndexes
	}
	// Random, and never reused. The first version counted up from a
	// timestamp, which meant that after a restart a fresh room could take an
	// ID a previous room had used - so anything keyed by room ID, from a
	// chat log to a kick record to a tournament result, could attach itself
	// to the wrong room. Sixteen random bytes cost nothing and close that
	// off permanently.
	id := newRoomID()

	r := NewRoom(id, index, hostID, now)
	r.Name = name
	s.rooms[id] = r
	s.indexes[index] = id

	m, err := membershipFor(r, hostID)
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
func (s *Store) Join(roomID string, who Applicant, now time.Time) (Membership, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.rooms[roomID]
	if !ok {
		return Membership{}, ErrNotFound
	}
	if _, err := r.Join(who, now); err != nil {
		return Membership{}, err
	}
	return membershipFor(r, who.ID)
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
	slot, kind, seated := r.SlotOf(playerID)
	if !seated {
		return Membership{}, ErrNotMember
	}
	_ = slot
	if kind == SeatAdmin {
		return adminMembershipFor(r, slot)
	}
	// A watcher goes through the same door as a player (D79): their address
	// is the one they hold, not one derived from the seat they are in, which
	// is what lets them move between the two without reconnecting.
	return membershipFor(r, playerID)
}

// JoinObserver seats somebody who wants to watch without playing.
func (s *Store) JoinObserver(roomID string, who Applicant, now time.Time) (Membership, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.rooms[roomID]
	if !ok {
		return Membership{}, ErrNotFound
	}
	if _, err := r.JoinObserver(who, now); err != nil {
		return Membership{}, err
	}
	return membershipFor(r, who.ID)
}

// JoinAdmin seats a moderator in the reserved area outside the gallery.
func (s *Store) JoinAdmin(roomID, playerID string, now time.Time) (Membership, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.rooms[roomID]
	if !ok {
		return Membership{}, ErrNotFound
	}
	seat, err := r.JoinAdmin(playerID, now)
	if err != nil {
		return Membership{}, err
	}
	return adminMembershipFor(r, seat)
}

// Leave vacates a player's slot. It reports whether that closed the room,
// which it does when the person leaving was the host (D70): everybody else in
// there has to lose their network access, and the caller is the only one that
// can revoke it.
func (s *Store) Leave(roomID, playerID string, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rooms[roomID]
	if !ok {
		return false, ErrNotFound
	}
	if r.Status == StatusClosed {
		r.Leave(playerID, now)
		return false, nil
	}
	r.Leave(playerID, now)
	// Somebody pressing Leave room is a decision, and when the person making
	// it is the host the decision is to end the room (D70). Everything else -
	// a crash, a dropped line - reaches a room through SeeHost instead, and
	// gets the grace period D40 describes.
	if playerID == r.HostID {
		r.Close(now)
		return true, nil
	}
	return false, nil
}

// Move changes which playing slot a player sits in, which is how a player
// changes team.
func (s *Store) Move(roomID, playerID string, seat int, watching bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rooms[roomID]
	if !ok {
		return ErrNotFound
	}
	return r.Move(playerID, seat, watching)
}

// Kick removes a player and bars them, host only.
func (s *Store) Kick(roomID, actorID, targetID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rooms[roomID]
	if !ok {
		return ErrNotFound
	}
	if err := r.Kick(actorID, targetID, now); err != nil {
		return err
	}
	if s.onKick != nil {
		n := r.KickCount[targetID]
		s.onKick(KickEvent{
			RoomID:     roomID,
			ActorID:    actorID,
			TargetID:   targetID,
			KickNumber: n,
			BlockedFor: KickBlockFor(n),
			At:         now,
		})
	}
	return nil
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
		// What the coordinator can see and the room cannot: whether the host
		// is still there, and whether they are in a match (D69, D70). This
		// runs before the timers, so a host who came back inside the window
		// stops the countdown on the same tick that notices them.
		if s.hosts != nil {
			f := s.hosts(r.HostID)
			r.SeeHost(f.Online, f.InGame, now)
		}
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

// adminMembershipFor derives the addressing for a moderator's seat.
func adminMembershipFor(r *Room, seat int) (Membership, error) {
	return nonPlayingMembership(r, seat, SeatAdmin)
}

// nonPlayingMembership derives the addressing for a seat outside the member
// pool. Only a moderator's is left: a watcher draws an address like a player
// now, so that moving between the two does not change it (D79).
func nonPlayingMembership(r *Room, seat int, kind SeatKind) (Membership, error) {
	vip, err := ipam.AdminIP(r.Index, seat)
	if err != nil {
		return Membership{}, err
	}
	host, err := hostAddr(r)
	if err != nil {
		return Membership{}, err
	}
	subnet, err := ipam.RoomSubnet(r.Index)
	if err != nil {
		return Membership{}, err
	}
	return Membership{
		RoomID:    r.ID,
		Slot:      seat,
		VirtualIP: vip,
		HostIP:    host,
		Subnet:    subnet,
		Kind:      kind,
	}, nil
}

// membershipFor derives the addressing a seated player needs.
//
// The address comes from what they hold in Addr, not from where they are
// sitting (D74). Slot is still reported, because the interface draws a seat
// number from it, but nothing about the network depends on it any more.
func membershipFor(r *Room, playerID string) (Membership, error) {
	index, ok := r.AddressOf(playerID)
	if !ok {
		return Membership{}, ErrNotMember
	}
	vip, err := ipam.MemberIP(r.Index, index)
	if err != nil {
		return Membership{}, err
	}
	host, err := hostAddr(r)
	if err != nil {
		return Membership{}, err
	}
	subnet, err := ipam.RoomSubnet(r.Index)
	if err != nil {
		return Membership{}, err
	}
	// The kind is read rather than assumed. It used to be hardcoded to
	// SeatPlayer, which was true while only players came through here and
	// became a lie the moment watchers did (D79) - and IsSpectator is what
	// the app draws a seat from.
	slot, kind, _ := r.SlotOf(playerID)
	return Membership{
		RoomID:    r.ID,
		Slot:      slot,
		VirtualIP: vip,
		HostIP:    host,
		Subnet:    subnet,
		IsHost:    playerID == r.HostID,
		Kind:      kind,
	}, nil
}

// hostAddr is the address every client in a room is told to connect to.
//
// It is the host's own address, and it belongs to the host rather than to a
// seat (D74). It was derived from the seat the host was sitting in (D64,
// which is what first let a host pick a side); making it theirs outright is
// the same idea carried the rest of the way, and it means a host changing
// team no longer changes the address nine other people are connecting to.
func hostAddr(r *Room) (netip.Addr, error) {
	index, ok := r.AddressOf(r.HostID)
	if !ok {
		// A room whose host holds no address cannot be connected to, and
		// saying so is better than handing out an address that is somebody
		// else's or nobody's.
		return netip.Addr{}, ErrNotMember
	}
	return ipam.SlotIP(r.Index, index)
}

// newRoomID returns an identifier no room has held before.
func newRoomID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand does not fail on any platform we run on; if it somehow
		// did, a predictable room ID is not a security boundary here - the
		// ticket is - so falling back is better than refusing to open a room.
		return "r" + time.Now().UTC().Format("20060102150405.000000000")
	}
	return "r" + hex.EncodeToString(b)
}

// SetPrivacy changes a room's door. Host only.
func (s *Store) SetPrivacy(roomID, actorID string, p Privacy, password string, minMMR int, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rooms[roomID]
	if !ok {
		return ErrNotFound
	}
	return r.SetPrivacy(actorID, p, password, minMMR, now)
}

// Invite admits one named person to an invite-only room. Host only.
func (s *Store) Invite(roomID, actorID, targetID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rooms[roomID]
	if !ok {
		return ErrNotFound
	}
	return r.Invite(actorID, targetID, now)
}

// Uninvite withdraws an invitation. Host only.
func (s *Store) Uninvite(roomID, actorID, targetID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rooms[roomID]
	if !ok {
		return ErrNotFound
	}
	return r.Uninvite(actorID, targetID)
}

// Close ends a room now, without waiting for the host's grace period.
//
// Two callers need it: a host who explicitly closes their own room, and the
// coordinator undoing a half-made room - one that was created but could not
// be given the door its host asked for. Leaving that one standing would put a
// public room where somebody asked for a private one, which is the exact
// opposite of what they wanted.
func (s *Store) Close(roomID, actorID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rooms[roomID]
	if !ok {
		return ErrNotFound
	}
	if actorID != "" && actorID != r.HostID {
		return ErrNotHost
	}
	r.Status = StatusClosed
	delete(s.indexes, r.Index)
	delete(s.rooms, roomID)
	return nil
}

// SetHost moves a room to a new host and returns everybody whose address
// changed, so their tickets can be revoked and reissued.
func (s *Store) SetHost(roomID, newHostID string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rooms[roomID]
	if !ok {
		return nil, ErrNotFound
	}
	return r.SetHost(newHostID)
}

// MaxDescription bounds the host's sentence about their room. Long enough for
// "need 2, no first-time Invokers, we start at 9" and short enough that one
// room cannot push every other room off the screen.
const MaxDescription = 120

// SetDescription records what the host says their room is for (D42).
//
// It is the host's own words, shown to strangers, so it is rendered as text
// and never as markup by anything that displays it - the same rule as the
// announcement banners, for the same reason.
func (s *Store) SetDescription(roomID, actorID, text string) error {
	text = strings.TrimSpace(text)
	if len(text) > MaxDescription {
		text = strings.TrimSpace(text[:MaxDescription])
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rooms[roomID]
	if !ok {
		return ErrNotFound
	}
	if actorID != r.HostID {
		return ErrNotHost
	}
	r.Description = text
	return nil
}

// ReportHostLatency records a host's measurement of their own distance from
// the relay, for the lobby's latency column (D42).
//
// It is a no-op unless the reporting player actually hosts the room, so an
// ordinary member's reading - which describes their connection, not the one
// everybody else in the room will be routed through - can never end up in the
// column labelled as the host's.
func (s *Store) ReportHostLatency(roomID, playerID string, millis int, now time.Time) {
	if millis <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rooms[roomID]
	if !ok || r.HostID != playerID {
		return
	}
	r.HostRelayMillis = millis
	r.HostRelayAt = now
}
