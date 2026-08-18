// Package ticket issues and validates the short-lived credentials a client
// presents to the relay.
//
// Tickets are opaque random strings looked up in a table, not signed blobs.
// The relay already asks the coordinator about every ticket it has not seen
// recently, so signing would buy nothing and cost us instant revocation: a
// signed ticket is valid until it expires no matter what we decide later,
// whereas a row in a table can be deleted the moment a player is kicked.
//
// The trade is that a coordinator restart invalidates outstanding tickets.
// At a few hundred concurrent players that is a reconnect, not an outage.
package ticket

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/netip"
	"sync"
	"time"
)

// Lifetime is how long a ticket is good for without renewal. It is short on
// purpose: it bounds how long a revoked player could keep playing if every
// other check failed.
const Lifetime = 10 * time.Minute

var (
	ErrUnknown = errors.New("ticket: unknown or revoked")
	ErrExpired = errors.New("ticket: expired")
)

// Claims is what a ticket authorises.
type Claims struct {
	PlayerID  string
	RoomID    string
	VirtualIP netip.Addr
}

type entry struct {
	claims  Claims
	expires time.Time
}

// Store holds live tickets. Safe for concurrent use.
type Store struct {
	mu      sync.RWMutex
	tickets map[string]entry
}

func NewStore() *Store {
	return &Store{tickets: make(map[string]entry)}
}

// Issue mints a ticket for the given claims.
func (s *Store) Issue(c Claims, now time.Time) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	tok := base64.RawURLEncoding.EncodeToString(raw)

	s.mu.Lock()
	s.tickets[tok] = entry{claims: c, expires: now.Add(Lifetime)}
	s.mu.Unlock()
	return tok, nil
}

// Validate returns the claims behind a ticket.
func (s *Store) Validate(tok string, now time.Time) (Claims, error) {
	s.mu.RLock()
	e, ok := s.tickets[tok]
	s.mu.RUnlock()
	if !ok {
		return Claims{}, ErrUnknown
	}
	if now.After(e.expires) {
		return Claims{}, ErrExpired
	}
	return e.claims, nil
}

// Renew pushes a ticket's expiry out, so a player in a long match is not cut
// off mid-game.
func (s *Store) Renew(tok string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.tickets[tok]
	if !ok {
		return ErrUnknown
	}
	if now.After(e.expires) {
		return ErrExpired
	}
	e.expires = now.Add(Lifetime)
	s.tickets[tok] = e
	return nil
}

// Revoke invalidates one ticket immediately.
func (s *Store) Revoke(tok string) {
	s.mu.Lock()
	delete(s.tickets, tok)
	s.mu.Unlock()
}

// RevokePlayerRoom invalidates every ticket a player holds for one room -
// what a kick needs. It deliberately leaves that player's tickets for other
// rooms alone, and other players in this room alone.
func (s *Store) RevokePlayerRoom(playerID, roomID string) {
	s.mu.Lock()
	for tok, e := range s.tickets {
		if e.claims.PlayerID == playerID && e.claims.RoomID == roomID {
			delete(s.tickets, tok)
		}
	}
	s.mu.Unlock()
}

// RevokeRoom invalidates every ticket for a room, which is what closing a
// room must do - otherwise its players keep a working tunnel to each other
// after the room is gone.
func (s *Store) RevokeRoom(roomID string) {
	s.mu.Lock()
	for tok, e := range s.tickets {
		if e.claims.RoomID == roomID {
			delete(s.tickets, tok)
		}
	}
	s.mu.Unlock()
}

// Purge drops expired tickets. Called on a timer so the table cannot grow
// without bound.
func (s *Store) Purge(now time.Time) {
	s.mu.Lock()
	for tok, e := range s.tickets {
		if now.After(e.expires) {
			delete(s.tickets, tok)
		}
	}
	s.mu.Unlock()
}

// Count reports how many live tickets exist, for metrics.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.tickets)
}
