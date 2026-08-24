package main

// The friends rail, the announcement strip, and the room's description
// (D42, D43).
//
// These live apart from server.go because they answer to a different clock.
// The lobby polls every two seconds because rooms fill in seconds; a friends
// list does not change that fast and an announcement changes about once a
// week. Fetching all of it on every poll would multiply the coordinator's
// load by the number of open windows for no gain a player could see.
//
// Both are also allowed to be unavailable. A coordinator running without a
// database has no accounts, and therefore no friends list - which is the
// state the live server is in right now. That has to read as "not on this
// server" in the interface, not as an error the player did something to
// cause.

import (
	"net/http"
	"sync"
	"time"

	"lobbybaz/client/lobby"
)

const (
	// friendsEvery is how often the rail is refreshed. Slower than the lobby
	// poll and fast enough that accepting a request feels immediate.
	friendsEvery = 5 * time.Second
	// bannersEvery is how often the announcement strip is refreshed. It is
	// edited by hand, perhaps weekly.
	bannersEvery = 5 * time.Minute
	// sulk is how long to leave a failing fetch alone. Without it, a server
	// with no friends list is asked for one every two seconds forever.
	sulk = 60 * time.Second
)

// cached is one thing fetched on a timer rather than on every poll.
type cached[T any] struct {
	mu    sync.Mutex
	value T
	err   string
	at    time.Time
	every time.Duration
}

// get returns the cached value, refreshing it if it is old enough. The fetch
// happens under the lock: two windows polling at once should make one request
// between them, not two.
func (c *cached[T]) get(fetch func() (T, error)) (T, string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	wait := c.every
	if c.err != "" {
		wait = sulk
	}
	if !c.at.IsZero() && time.Since(c.at) < wait {
		return c.value, c.err
	}

	v, err := fetch()
	c.at = time.Now()
	if err != nil {
		// The previous value is kept. A friends list that blinks out every
		// time one request fails is worse than one that is a few seconds
		// stale.
		c.err = err.Error()
		return c.value, c.err
	}
	c.value, c.err = v, ""
	return c.value, ""
}

// forget drops the cached value so the next poll fetches again. Called after
// any action that changes what is cached, so the player sees their own change
// immediately rather than up to five seconds later.
func (c *cached[T]) forget() {
	c.mu.Lock()
	c.at = time.Time{}
	c.mu.Unlock()
}

// social folds the friends rail and the announcement strip into the state
// reply. Neither is fatal: a missing one is reported as a reason, and the
// lobby draws the rest of itself regardless.
func (s *server) social(out map[string]any) {
	friends, err := s.friendsCache.get(func() (*lobby.FriendList, error) {
		return s.api().Friends()
	})
	if friends != nil {
		out["friends"] = friends
	}
	if err != "" {
		out["friends_error"] = err
	}

	banners, err := s.bannersCache.get(func() ([]lobby.Banner, error) {
		return s.api().Banners()
	})
	if len(banners) > 0 {
		out["banners"] = banners
	}
	if err != "" {
		out["banners_error"] = err
	}
}

// friendAction covers every one-word change to the friend graph: request,
// accept, decline, remove, block, unblock. One route rather than six, because
// the client library already names them and the server already refuses the
// ones that do not apply.
func (s *server) friendAction(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action string `json:"action"`
		Target string `json:"target_id"`
	}
	if !decode(w, r, &body) {
		return
	}
	c := s.api()
	var err error
	switch body.Action {
	case "request":
		err = c.AddFriend(body.Target)
	case "accept":
		err = c.AcceptFriend(body.Target)
	case "decline":
		err = c.DeclineFriend(body.Target)
	case "remove":
		err = c.RemoveFriend(body.Target)
	case "block":
		err = c.BlockPlayer(body.Target)
	case "unblock":
		err = c.UnblockPlayer(body.Target)
	default:
		fail(w, "unknown action "+body.Action)
		return
	}
	if err != nil {
		fail(w, err.Error())
		return
	}
	s.friendsCache.forget()
	ok(w)
}

// conversation reads one private thread and optionally adds to it.
func (s *server) conversation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Target string `json:"target_id"`
		Send   string `json:"send"`
		After  int64  `json:"after"`
	}
	if !decode(w, r, &body) {
		return
	}
	msgs, err := s.api().Conversation(body.Target, body.Send, body.After)
	if err != nil {
		fail(w, err.Error())
		return
	}
	// Reading a conversation clears its unread count on the server, so the
	// rail's badge is stale the moment this returns.
	s.friendsCache.forget()
	writeJSON(w, http.StatusOK, map[string]any{"messages": msgs})
}

// inviteFriend asks somebody into the room this player is in.
func (s *server) inviteFriend(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Target string `json:"target_id"`
	}
	if !decode(w, r, &body) {
		return
	}
	cfg := s.snapshot()
	if cfg.RoomID == "" {
		fail(w, "you are not in a room")
		return
	}
	if err := s.api().InviteFriend(body.Target, cfg.RoomID); err != nil {
		fail(w, err.Error())
		return
	}
	ok(w)
}

// findPlayer looks somebody up by the username they signed up with, which is
// the only way to find a person who is not already a friend and not in a room
// you can see.
func (s *server) findPlayer(w http.ResponseWriter, r *http.Request) {
	who := r.URL.Query().Get("username")
	if who == "" {
		fail(w, "a username is required")
		return
	}
	found, err := s.api().FindPlayer(who)
	if err != nil {
		fail(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, found)
}

// describeRoom sets the host's sentence about their room (D42).
func (s *server) describeRoom(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Description string `json:"description"`
	}
	if !decode(w, r, &body) {
		return
	}
	cfg := s.snapshot()
	if cfg.RoomID == "" {
		fail(w, "you are not in a room")
		return
	}
	if _, err := s.api().Describe(cfg.RoomID, body.Description); err != nil {
		fail(w, err.Error())
		return
	}
	ok(w)
}

// invitationsSeen clears the invitation badge once the player has looked.
func (s *server) invitationsSeen(w http.ResponseWriter, r *http.Request) {
	if err := s.api().InvitationsSeen(); err != nil {
		fail(w, err.Error())
		return
	}
	s.friendsCache.forget()
	ok(w)
}
