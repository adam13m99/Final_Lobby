// Package room implements the room lifecycle exactly as specified in
// docs/superpowers/specs/2026-08-18-lobby-platform-design.md section 3, as
// amended by the owner's decisions of 2026-08-24 (D38, D39, D40).
//
// These rules are product decisions, not engineering ones. Do not adjust a
// timer or relax an admission check here without the product owner saying so.
package room

import (
	"errors"
	"time"

	"lobbybaz/coordinator/internal/ipam"
)

// Status is the room's admission state.
type Status string

const (
	// StatusOpen accepts new players.
	StatusOpen Status = "open"
	// StatusLocked is set when a match begins. No new player may join.
	StatusLocked Status = "locked_in_game"
	// StatusOpenToNew is the host explicitly reopening a running match so
	// an abandoned slot can be refilled.
	StatusOpenToNew Status = "open_to_new_players"
	// StatusClosed is terminal.
	StatusClosed Status = "closed"
)

// SeatKind is which of a room's three seating areas somebody occupies.
//
// They are genuinely different things rather than one list with a flag:
// players occupy the ten slots Dota itself cares about, observers watch
// without playing, and admins hold reserved seats outside both so that a full
// match with a full gallery can never keep a moderator out.
type SeatKind string

const (
	SeatPlayer   SeatKind = "player"
	SeatObserver SeatKind = "observer"
	SeatAdmin    SeatKind = "admin"
)

const (
	// HostGracePeriod is how long a room survives without its host. It
	// doubles as the host's window to reconnect and save the match.
	//
	// One minute since D40. It was two, and the owner asked for GameRanger's
	// behaviour "but more friendly": a room whose host has genuinely gone
	// should not hold nine people staring at it for two minutes, and a host
	// who is coming back is back inside sixty seconds. It sits well inside
	// the 120-second sticky-address window, so a host who returns in time
	// keeps their address and the room never noticed they were away.
	HostGracePeriod = 1 * time.Minute

	// KickBlockFirst is how long the first kick from a room bars somebody.
	//
	// Deliberately short. Most kicks are an argument rather than an abuser,
	// and a minute is enough to end the re-join fight without punishing
	// someone for their evening. The escalation below is what deals with a
	// person who keeps coming back. (D39.)
	KickBlockFirst = 1 * time.Minute
	// KickBlockStep is added for each kick after the first: 1, 3, 5, 7...
	KickBlockStep = 2 * time.Minute
)

// KickBlockFor returns how long the n-th kick bars a player, counting from 1.
func KickBlockFor(count int) time.Duration {
	if count < 1 {
		count = 1
	}
	return KickBlockFirst + time.Duration(count-1)*KickBlockStep
}

var (
	ErrRoomLocked     = errors.New("room: locked, no new players")
	ErrRoomFull       = errors.New("room: no free player slot")
	ErrKickBlocked    = errors.New("room: player was kicked recently")
	ErrNotHost        = errors.New("room: only the host may do that")
	ErrRoomClosed     = errors.New("room: closed")
	ErrAlreadyJoined  = errors.New("room: already in this room")
	ErrNoObserverSeat = errors.New("room: no free observer seat")
	ErrNoAdminSeat    = errors.New("room: no free admin seat")
	ErrSlotTaken      = errors.New("room: that slot is taken")
	ErrNoSuchSlot     = errors.New("room: there is no such slot")
	ErrHostSlot       = errors.New("room: the host sits in the first slot")
	// ErrNotMemberOfRoom is returned when somebody is asked to take a role in
	// a room they are not playing in.
	ErrNotMemberOfRoom = errors.New("room: they are not playing in that room")
)

// Room is one lobby. Not safe for concurrent use; the store serialises access.
type Room struct {
	ID     string
	Name   string
	Index  int
	HostID string
	Status Status

	// Description is the host's own sentence about the room - what they want
	// to play, who they want, house rules. D42 puts it in the room list
	// because a name alone does not tell anybody whether a room is for them.
	Description string

	// HostRelayMillis is the host's round trip to the relay, as their own
	// machine measured it, and HostRelayAt is when they last said so.
	//
	// This is the lobby's latency column, and it is deliberately the host's
	// number rather than the reader's: rooms are isolated from each other, so
	// somebody browsing the lobby has no path to a host they have not joined
	// and cannot measure anything. The host's distance from the relay is what
	// determines how the game plays for everyone who joins them, which makes
	// it the more useful number anyway (D42).
	//
	// It is self-reported, and that is acceptable: the only person a host
	// could mislead with it is somebody deciding whether to join their room,
	// and the lie is exposed by the first minute of play.
	HostRelayMillis int
	HostRelayAt     time.Time

	// Privacy is the door on the room (D41); see privacy.go.
	Privacy Privacy
	// MinMMR is the floor a player must declare to be let in. Zero is no
	// floor. It is advisory in the sense that MMR is self-declared, and
	// enforced in the sense that the coordinator, not the client, checks it.
	MinMMR int
	// passwordHash is unexported so a room can never be serialised with its
	// password in it by somebody adding a field to a JSON view.
	passwordHash string
	// Invites is who the host has named, for an invite-only room.
	Invites map[string]time.Time

	Slots     [ipam.PlayerSlots]string
	Observers [ipam.ObserverSlots]string
	Admins    [ipam.AdminSlots]string

	KickedUntil map[string]time.Time
	// KickCount is how many times each player has been kicked from *this*
	// room. It drives the escalating block in D39, and it has to outlive a
	// coordinator restart, or the escalation resets on every deployment and
	// means nothing.
	KickCount map[string]int

	HostGraceUntil time.Time
}

// NewRoom creates a room with the host seated in slot 0, which maps to the
// deterministic host virtual IP that clients are told to connect to.
func NewRoom(id string, index int, hostID string, now time.Time) *Room {
	r := &Room{
		ID:          id,
		Index:       index,
		HostID:      hostID,
		Status:      StatusOpen,
		Privacy:     PrivacyPublic,
		Invites:     make(map[string]time.Time),
		KickedUntil: make(map[string]time.Time),
		KickCount:   make(map[string]int),
	}
	r.Slots[0] = hostID
	return r
}

// admissible runs the checks that every kind of seat shares.
//
// A kick block is checked here, before the door, and is never bypassed - not
// by a password, not by an invitation, not by being staff. The block is
// enforced against identity, not against role.
func (r *Room) admissible(playerID string, now time.Time) error {
	if r.Status == StatusClosed {
		return ErrRoomClosed
	}
	if until, ok := r.KickedUntil[playerID]; ok && now.Before(until) {
		return ErrKickBlocked
	}
	if _, _, seated := r.SlotOf(playerID); seated {
		return ErrAlreadyJoined
	}
	return nil
}

// Join seats a player in the lowest free slot.
func (r *Room) Join(who Applicant, now time.Time) (int, error) {
	playerID := who.ID
	if err := r.admissible(playerID, now); err != nil {
		return 0, err
	}

	// The host reclaiming their own room during the grace window is always
	// allowed, even while the room is locked - otherwise a host who crashed
	// mid-match would be locked out of the match they are running. They are
	// also not asked for their own room's password.
	isHostReturning := playerID == r.HostID && !r.HostGraceUntil.IsZero()
	if r.Status == StatusLocked && !isHostReturning {
		return 0, ErrRoomLocked
	}
	if !isHostReturning {
		if err := r.knock(who); err != nil {
			return 0, err
		}
	}

	for i := range r.Slots {
		if r.Slots[i] == "" {
			r.Slots[i] = playerID
			if isHostReturning {
				r.HostGraceUntil = time.Time{}
			}
			return i, nil
		}
	}
	return 0, ErrRoomFull
}

// JoinObserver seats somebody who wants to watch without playing.
//
// Observers may not walk into a locked room. Watching is a social choice, and
// letting people wander into a match already in progress is where scouting
// and griefing start. An admin is a different case - see JoinAdmin.
func (r *Room) JoinObserver(who Applicant, now time.Time) (int, error) {
	playerID := who.ID
	if err := r.admissible(playerID, now); err != nil {
		return 0, err
	}
	if r.Status == StatusLocked {
		return 0, ErrRoomLocked
	}
	// The door applies to the gallery too. A friends-only room whose observer
	// seats anybody can take is not a friends-only room.
	if err := r.knock(who); err != nil {
		return 0, err
	}
	for i := range r.Observers {
		if r.Observers[i] == "" {
			r.Observers[i] = playerID
			return i, nil
		}
	}
	return 0, ErrNoObserverSeat
}

// JoinAdmin seats a moderator in the reserved area, outside both the playing
// slots and the observer gallery.
//
// An admin may enter a locked room, and that is the entire point of the seat:
// a moderator is called in precisely when a match is already running and
// something has gone wrong inside it. A kicked player is still barred, which
// matters because the block is enforced against identity, not against role.
func (r *Room) JoinAdmin(playerID string, now time.Time) (int, error) {
	if err := r.admissible(playerID, now); err != nil {
		return 0, err
	}
	for i := range r.Admins {
		if r.Admins[i] == "" {
			r.Admins[i] = playerID
			return i, nil
		}
	}
	return 0, ErrNoAdminSeat
}

// Occupants lists every player ID seated in the room, players first.
func (r *Room) Occupants() []string {
	out := make([]string, 0, ipam.SeatsPerRoom)
	for _, group := range [][]string{r.Slots[:], r.Observers[:], r.Admins[:]} {
		for _, id := range group {
			if id != "" {
				out = append(out, id)
			}
		}
	}
	return out
}

// Seats reports how many playing slots are taken.
func (r *Room) Seats() int {
	return countTaken(r.Slots[:])
}

// Watchers reports how many observer seats are taken.
func (r *Room) Watchers() int {
	return countTaken(r.Observers[:])
}

func countTaken(group []string) int {
	n := 0
	for _, id := range group {
		if id != "" {
			n++
		}
	}
	return n
}

// SlotOf finds where a player is sitting: the index within their seating
// area, which area that is, and whether they are seated at all.
func (r *Room) SlotOf(playerID string) (int, SeatKind, bool) {
	for i, id := range r.Slots {
		if id == playerID {
			return i, SeatPlayer, true
		}
	}
	for i, id := range r.Observers {
		if id == playerID {
			return i, SeatObserver, true
		}
	}
	for i, id := range r.Admins {
		if id == playerID {
			return i, SeatAdmin, true
		}
	}
	return 0, SeatPlayer, false
}

// Move puts a seated player in a different playing slot.
//
// Which slot you are in is which team you are on: 0-4 are Radiant and 5-9 are
// Dire, exactly as the game divides them. Picking a side is therefore picking
// a slot, and it is the one thing every player in a room wants to do that
// previously required leaving and rejoining until the numbers came out right.
//
// The rules are the boring ones. A locked room is a match in progress, and a
// player who changes team halfway through it is a player on the wrong team in
// Dota. Slot 0 belongs to the host for the room's whole life, so nobody moves
// into it and the host does not move out - the client, the relay and the room
// list all read the host out of slot 0.
func (r *Room) Move(playerID string, slot int) error {
	if r.Status == StatusClosed {
		return ErrRoomClosed
	}
	if r.Status == StatusLocked {
		return ErrRoomLocked
	}
	if slot < 0 || slot >= len(r.Slots) {
		return ErrNoSuchSlot
	}
	if slot == 0 || playerID == r.HostID {
		return ErrHostSlot
	}
	from, kind, seated := r.SlotOf(playerID)
	if !seated || kind != SeatPlayer {
		return ErrNotMemberOfRoom
	}
	if from == slot {
		return nil
	}
	if r.Slots[slot] != "" {
		return ErrSlotTaken
	}
	r.Slots[from] = ""
	r.Slots[slot] = playerID
	return nil
}

// Leave vacates a player's seat, whichever kind it is. If the host leaves,
// the grace timer starts.
func (r *Room) Leave(playerID string, now time.Time) {
	for _, group := range [][]string{r.Slots[:], r.Observers[:], r.Admins[:]} {
		for i := range group {
			if group[i] == playerID {
				group[i] = ""
			}
		}
	}
	if playerID == r.HostID {
		r.HostGraceUntil = now.Add(HostGracePeriod)
	}
}

// Kick removes a player and bars them for an interval that grows each time
// they are kicked from this room: one minute, then three, then five (D39).
func (r *Room) Kick(actorID, targetID string, now time.Time) error {
	if actorID != r.HostID {
		return ErrNotHost
	}
	r.Leave(targetID, now)
	if r.KickCount == nil {
		r.KickCount = make(map[string]int)
	}
	r.KickCount[targetID]++
	r.KickedUntil[targetID] = now.Add(KickBlockFor(r.KickCount[targetID]))
	return nil
}

// SetStatus changes admission state. Host only.
func (r *Room) SetStatus(actorID string, s Status, now time.Time) error {
	if actorID != r.HostID {
		return ErrNotHost
	}
	if r.Status == StatusClosed {
		return ErrRoomClosed
	}
	r.Status = s
	return nil
}

// Tick advances time-based transitions. Call it on a scheduler.
//
// The host's absence is the only thing that ends a room. A match finishing
// does nothing here, on purpose (D40): the ten people who just played are
// usually the ten who want to play again, and closing the room around them
// treats the end of a game as a failure to recover from.
func (r *Room) Tick(now time.Time) {
	if r.Status == StatusClosed {
		return
	}
	if !r.HostGraceUntil.IsZero() && now.After(r.HostGraceUntil) {
		r.Status = StatusClosed
	}
}

// SetHost moves the room to a new host.
//
// This is host migration under another name (D43). D40 says a room dies a
// minute after its host does; an admin reassigning the host is the escape
// hatch for when that is the wrong outcome - the ten people in the lobby want
// to keep playing and the person who opened it has gone.
//
// **It does not rescue a match in progress**, and nothing here pretends
// otherwise: the Dota server was running on the old host's PC and it is gone.
// What this saves is the room, the people in it, and the arrangement they
// made to play together.
//
// Mechanically it is a swap. The host always occupies slot 0, because slot 0
// is the address every client was told to connect to (ipam.HostIP). So the new
// host takes slot 0 and whoever was sitting there takes theirs. Both change
// address, so both need a fresh ticket - the caller is responsible for
// revoking the old ones, which is why the swapped IDs come back.
func (r *Room) SetHost(newHostID string) (moved []string, err error) {
	if r.Status == StatusClosed {
		return nil, ErrRoomClosed
	}
	if newHostID == r.HostID {
		return nil, nil
	}

	slot, kind, seated := r.SlotOf(newHostID)
	if !seated || kind != SeatPlayer {
		// A watcher cannot become the host: the host is the person whose PC
		// runs the game, and somebody in the observer gallery is not playing.
		return nil, ErrNotMemberOfRoom
	}

	previous := r.Slots[0]
	r.Slots[0], r.Slots[slot] = newHostID, previous
	r.HostID = newHostID

	// The room has a host again, so it is no longer counting down to closure.
	r.HostGraceUntil = time.Time{}

	moved = []string{newHostID}
	if previous != "" && previous != newHostID {
		moved = append(moved, previous)
	}
	return moved, nil
}
