// Package player is the live view of who is here: who is online, what they
// are called right now, and whether their copy of Dota is running.
//
// It is deliberately in memory and deliberately not the source of truth. Since
// T5 a player's durable facts - username, password, declared MMR, display
// name - live in the accounts table, and this registry mirrors them for the
// things a room list has to draw on every poll. What is genuinely only here is
// presence: last seen, and in-game. Neither survives a restart, and neither
// should: a coordinator that has just started has not seen anybody yet, and
// saying otherwise would show a lobby full of ghosts.
//
// A coordinator running without a database (the loadtest harness) has only
// this, and that is the one case where it is the whole of identity.
package player

import (
	"errors"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// MMRChangeInterval is the product rule: self-declared, changeable once
	// per week. The first declaration is free.
	MMRChangeInterval = 7 * 24 * time.Hour
	// MaxMMR sits above the highest real Dota rating with room to spare.
	MaxMMR = 15000

	minNickRunes = 2
	maxNickRunes = 20
)

var (
	ErrBadNick    = errors.New("player: name must be 2-20 characters")
	ErrBadMMR     = errors.New("player: MMR must be between 0 and 15000")
	ErrMMRTooSoon = errors.New("player: MMR can only be changed once a week")
	ErrNotFound   = errors.New("player: unknown player")
)

// Player is one person as the server knows them.
type Player struct {
	ID        string    `json:"id"`
	Nick      string    `json:"nick"`
	MMR       int       `json:"mmr"`
	MMRSetAt  time.Time `json:"mmr_set_at,omitempty"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`

	// InGame is reported by the player's own service, which knows because it
	// launched Dota and watches its log (D41). It is not inferred from room
	// state: a room can be locked while its host is still on the hero screen,
	// and telling somebody their friend is playing when they are waiting for
	// them is exactly the wrong thing to say.
	InGame bool `json:"in_game"`

	// RelayMillis is this player's own round trip to the relay, as their own
	// machine measured it, and RelayAt is when they last said so. It is the
	// number shown beside their seat in a room; for the host of a room it is
	// also the lobby's latency column (D54).
	RelayMillis int       `json:"relay_ms,omitempty"`
	RelayAt     time.Time `json:"relay_at,omitempty"`
}

// MMRChangeableAt is when this player may next change their MMR. Zero means
// now - they have never set one.
func (p Player) MMRChangeableAt() time.Time {
	if p.MMRSetAt.IsZero() {
		return time.Time{}
	}
	return p.MMRSetAt.Add(MMRChangeInterval)
}

// Registry is every player the coordinator has seen since it started.
type Registry struct {
	mu      sync.Mutex
	players map[string]*Player
}

func NewRegistry() *Registry {
	return &Registry{players: make(map[string]*Player)}
}

// CleanNick trims and validates a chosen name. Persian and Cyrillic names
// are as welcome as Latin ones, so this counts runes rather than bytes and
// rejects only control characters.
func CleanNick(nick string) (string, error) {
	nick = strings.TrimSpace(nick)
	if n := utf8.RuneCountInString(nick); n < minNickRunes || n > maxNickRunes {
		return "", ErrBadNick
	}
	for _, r := range nick {
		if unicode.IsControl(r) {
			return "", ErrBadNick
		}
	}
	return nick, nil
}

// Seen records a player, creating them on first sight and updating their
// name and last-seen time afterwards. This is called on every authenticated
// request, so it is the only place LastSeen moves.
func (r *Registry) Seen(id, nick string, now time.Time) (Player, error) {
	clean, err := CleanNick(nick)
	if err != nil {
		return Player{}, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	p, ok := r.players[id]
	if !ok {
		p = &Player{ID: id, FirstSeen: now}
		r.players[id] = p
	}
	p.Nick = clean
	p.LastSeen = now
	return *p, nil
}

// SetMMR records a self-declared rating, at most once per week.
func (r *Registry) SetMMR(id string, mmr int, now time.Time) (Player, error) {
	if mmr < 0 || mmr > MaxMMR {
		return Player{}, ErrBadMMR
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	p, ok := r.players[id]
	if !ok {
		return Player{}, ErrNotFound
	}
	// The first declaration is free; after that the week has to have passed.
	// Re-declaring the same number is not a change and is always allowed, so
	// a client that echoes its current value back never gets an error.
	if !p.MMRSetAt.IsZero() && mmr != p.MMR && now.Before(p.MMRSetAt.Add(MMRChangeInterval)) {
		return Player{}, ErrMMRTooSoon
	}
	if mmr != p.MMR || p.MMRSetAt.IsZero() {
		p.MMR = mmr
		p.MMRSetAt = now
	}
	return *p, nil
}

// SetInGame records whether somebody's copy of Dota is running.
//
// Reported, not inferred. The signal costs nothing - the service launched the
// game and is already watching its log - and it is the only honest source: a
// coordinator cannot see a match start.
func (r *Registry) SetInGame(id string, playing bool, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.players[id]
	if !ok {
		return
	}
	p.InGame = playing
	p.LastSeen = now
}

// SetRelay records a player's own round trip to the relay.
//
// Only that player's machine can measure it - the relay is the one host every
// client shares, and nobody in the lobby has a path to anybody else. It is
// reported on the heartbeat and read back in two places: beside a player's
// seat in a room, and (for the host of a room) as the lobby's latency column.
//
// A zero or negative reading means "not measured", never "instant", and is
// discarded rather than stored. RelayAt is kept beside it so a reader can
// tell a fresh number from one left behind by a client that went away.
func (r *Registry) SetRelay(id string, millis int, now time.Time) {
	if millis <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.players[id]
	if !ok {
		return
	}
	p.RelayMillis = millis
	p.RelayAt = now
}

// SeenSince returns every player whose last-seen time is at or after t.
//
// It exists so the coordinator can flush presence to the database on a timer
// rather than on every heartbeat. At a thousand players polling every two
// seconds, writing a row per poll would be five hundred writes a second at
// SQLite for a number nobody reads more than once a minute.
func (r *Registry) SeenSince(t time.Time) []Player {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Player, 0, len(r.players))
	for _, p := range r.players {
		if !p.LastSeen.Before(t) {
			out = append(out, *p)
		}
	}
	return out
}

// Forget drops a player from the registry, as a restart would.
//
// It exists for tests that need the state a coordinator is in after a
// deployment: the accounts are still in the database, and nothing in memory
// remembers that any of them was ever here.
func (r *Registry) Forget(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.players, id)
}

// Get returns one player.
func (r *Registry) Get(id string) (Player, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.players[id]
	if !ok {
		return Player{}, false
	}
	return *p, true
}

// Lookup returns several players at once, skipping any that are unknown.
// Room views need this for every occupied slot.
func (r *Registry) Lookup(ids []string) map[string]Player {
	out := make(map[string]Player, len(ids))
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range ids {
		if p, ok := r.players[id]; ok {
			out[id] = *p
		}
	}
	return out
}

// Online counts players seen within the window. The lobby shows this so an
// empty server looks empty rather than broken.
func (r *Registry) Online(within time.Duration, now time.Time) int {
	cutoff := now.Add(-within)
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, p := range r.players {
		if p.LastSeen.After(cutoff) {
			n++
		}
	}
	return n
}
