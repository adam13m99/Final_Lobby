// Package room implements the room lifecycle exactly as specified in
// docs/superpowers/specs/2026-08-18-lobby-platform-design.md section 3.
//
// These rules are product decisions, not engineering ones. Do not adjust a
// timer or relax an admission check here without the product owner saying so.
package room

import (
	"errors"
	"time"

	"finallobby/coordinator/internal/ipam"
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

const (
	// HostGracePeriod is how long a room survives without its host. It
	// doubles as the host's window to reconnect and save the match.
	HostGracePeriod = 2 * time.Minute
	// KickBlockPeriod is how long a kicked player is barred from the room.
	// A player who leaves voluntarily is not barred at all.
	KickBlockPeriod = 5 * time.Minute
)

var (
	ErrRoomLocked      = errors.New("room: locked, no new players")
	ErrRoomFull        = errors.New("room: no free player slot")
	ErrKickBlocked     = errors.New("room: player was kicked recently")
	ErrNotHost         = errors.New("room: only the host may do that")
	ErrRoomClosed      = errors.New("room: closed")
	ErrAlreadyJoined   = errors.New("room: already in this room")
	ErrNoSpectatorSeat = errors.New("room: no free spectator seat")
)

// Room is one lobby. Not safe for concurrent use; the store serialises access.
type Room struct {
	ID     string
	Name   string
	Index  int
	HostID string
	Status Status

	Slots      [ipam.PlayerSlots]string
	Spectators [ipam.SpectatorSlots]string

	KickedUntil    map[string]time.Time
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
		KickedUntil: make(map[string]time.Time),
	}
	r.Slots[0] = hostID
	return r
}

// Join seats a player in the lowest free slot.
func (r *Room) Join(playerID string, now time.Time) (int, error) {
	if r.Status == StatusClosed {
		return 0, ErrRoomClosed
	}
	if until, ok := r.KickedUntil[playerID]; ok && now.Before(until) {
		return 0, ErrKickBlocked
	}
	for _, occupant := range r.Slots {
		if occupant == playerID {
			return 0, ErrAlreadyJoined
		}
	}

	// The host reclaiming their own room during the grace window is always
	// allowed, even while the room is locked - otherwise a host who crashed
	// mid-match would be locked out of the match they are running.
	isHostReturning := playerID == r.HostID && !r.HostGraceUntil.IsZero()
	if r.Status == StatusLocked && !isHostReturning {
		return 0, ErrRoomLocked
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

// JoinSpectator seats an admin in the reserved spectator area, outside the
// ten playing slots.
//
// A spectator may enter a locked room. That is the whole point of the seat:
// an admin is called in precisely when a match is already running and
// something has gone wrong in it. A kicked player is still barred.
func (r *Room) JoinSpectator(playerID string, now time.Time) (int, error) {
	if r.Status == StatusClosed {
		return 0, ErrRoomClosed
	}
	if until, ok := r.KickedUntil[playerID]; ok && now.Before(until) {
		return 0, ErrKickBlocked
	}
	for _, occupant := range r.Slots {
		if occupant == playerID {
			return 0, ErrAlreadyJoined
		}
	}
	for _, occupant := range r.Spectators {
		if occupant == playerID {
			return 0, ErrAlreadyJoined
		}
	}
	for i := range r.Spectators {
		if r.Spectators[i] == "" {
			r.Spectators[i] = playerID
			return i, nil
		}
	}
	return 0, ErrNoSpectatorSeat
}

// Occupants lists every player ID seated in the room, players first.
func (r *Room) Occupants() []string {
	out := make([]string, 0, len(r.Slots)+len(r.Spectators))
	for _, id := range r.Slots {
		if id != "" {
			out = append(out, id)
		}
	}
	for _, id := range r.Spectators {
		if id != "" {
			out = append(out, id)
		}
	}
	return out
}

// Seats reports how many playing slots are taken.
func (r *Room) Seats() int {
	n := 0
	for _, id := range r.Slots {
		if id != "" {
			n++
		}
	}
	return n
}

// SlotOf finds where a player is sitting. Returns the slot index, whether
// they are a spectator, and whether they are seated at all.
func (r *Room) SlotOf(playerID string) (int, bool, bool) {
	for i, id := range r.Slots {
		if id == playerID {
			return i, false, true
		}
	}
	for i, id := range r.Spectators {
		if id == playerID {
			return i, true, true
		}
	}
	return 0, false, false
}

// Leave vacates a player's slot. If the host leaves, the grace timer starts.
func (r *Room) Leave(playerID string, now time.Time) {
	for i := range r.Slots {
		if r.Slots[i] == playerID {
			r.Slots[i] = ""
		}
	}
	for i := range r.Spectators {
		if r.Spectators[i] == playerID {
			r.Spectators[i] = ""
		}
	}
	if playerID == r.HostID {
		r.HostGraceUntil = now.Add(HostGracePeriod)
	}
}

// Kick removes a player and bars them for KickBlockPeriod.
func (r *Room) Kick(actorID, targetID string, now time.Time) error {
	if actorID != r.HostID {
		return ErrNotHost
	}
	r.Leave(targetID, now)
	r.KickedUntil[targetID] = now.Add(KickBlockPeriod)
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
func (r *Room) Tick(now time.Time) {
	if r.Status == StatusClosed {
		return
	}
	if !r.HostGraceUntil.IsZero() && now.After(r.HostGraceUntil) {
		r.Status = StatusClosed
	}
}
