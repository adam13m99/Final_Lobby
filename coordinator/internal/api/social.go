package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"lobbybaz/coordinator/internal/account"
	"lobbybaz/coordinator/internal/social"
)

// The friends rail, over HTTP (D41).
//
// One thing here is worth stating because it looks like a bug until you know
// it is not: several of these endpoints return 200 for something that did not
// happen. Messaging or friending somebody who has blocked you is accepted and
// dropped. An error would tell the sender they had been blocked, which turns
// blocking into a message of its own and hands a determined person a reason
// to make another account. The `social` package makes that decision; this
// file only refrains from undoing it.

// PresenceWindow is how recently somebody must have been seen to count as
// online. It is generous on purpose: the client polls every few seconds, and
// a person whose Wi-Fi hiccups should not blink offline in their friends'
// lists.
const PresenceWindow = 90 * time.Second

// friendView is one person on the rail.
type friendView struct {
	PlayerID    string `json:"player_id"`
	DisplayName string `json:"display_name"`
	MMR         int    `json:"mmr"`

	// State is "accepted" or "requested"; Incoming distinguishes a request
	// waiting for my answer from one waiting for theirs. They need entirely
	// different buttons, so they are not the same field.
	State    string `json:"state"`
	Incoming bool   `json:"incoming,omitempty"`

	Online bool `json:"online"`
	// InGame is nearly free: the service already knows whether Dota is
	// running, because it launched it and watches its log (D41). What is
	// surfaced here is that signal, not a guess from room membership - being
	// in a room and being in a match are different things, and conflating
	// them would tell somebody their friend is playing when they are sitting
	// in a lobby waiting for them.
	InGame bool `json:"in_game"`
	// RoomID is where they are, so "join my friend" is one click.
	RoomID string `json:"room_id,omitempty"`
	Unread int    `json:"unread,omitempty"`
	// LastSeen is when somebody who is offline was last here, so the rail
	// can answer the question it is actually asked - is it worth waiting for
	// them. Absent for anybody online (the answer is "now"), and absent for
	// anybody this server has never recorded, which a reader must show as
	// nothing rather than as a date in 1970.
	//
	// A pointer, not a time.Time: `omitempty` does not suppress a zero
	// struct, so a plain field would put 0001-01-01 on the wire for every
	// person the server has never heard of - and the rail would print it.
	LastSeen *time.Time `json:"last_seen,omitempty"`
}

func (s *Server) socialOn() bool { return s.social != nil }

func (s *Server) requireSocial(w http.ResponseWriter) bool {
	if !s.socialOn() {
		writeErr(w, http.StatusServiceUnavailable, "this server has no friends list")
		return false
	}
	return true
}

// friendList returns the rail: friends, then requests waiting on me, then
// requests waiting on them.
func (s *Server) friendList(w http.ResponseWriter, r *http.Request) {
	if !s.requireSocial(w) {
		return
	}
	me, _ := session(r)

	var out struct {
		Friends     []friendView        `json:"friends"`
		Incoming    []friendView        `json:"incoming"`
		Outgoing    []friendView        `json:"outgoing"`
		Blocked     []friendView        `json:"blocked"`
		Invitations []social.Invitation `json:"invitations"`
	}

	unread, err := s.social.Unread(me.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not read your messages")
		return
	}

	for _, group := range []struct {
		load func(string) ([]social.Relation, error)
		into *[]friendView
	}{
		{s.social.Friends, &out.Friends},
		{s.social.Incoming, &out.Incoming},
		{s.social.Outgoing, &out.Outgoing},
	} {
		rel, err := group.load(me.ID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "could not read your friends list")
			return
		}
		views := make([]friendView, 0, len(rel))
		for _, one := range rel {
			v := s.friendView(one.AccountID)
			v.State, v.Incoming = string(one.State), one.Incoming
			v.Unread = unread[one.AccountID]
			views = append(views, v)
		}
		*group.into = views
	}

	blocked, err := s.social.Blocked(me.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not read your block list")
		return
	}
	s.withLastSeen(out.Friends, out.Incoming, out.Outgoing)

	out.Blocked = make([]friendView, 0, len(blocked))
	for _, id := range blocked {
		v := s.friendView(id)
		// Somebody you blocked is not somebody whose whereabouts you get to
		// watch. The name is enough to unblock them by.
		// When they were last online is whereabouts too.
		v.Online, v.InGame, v.RoomID, v.LastSeen = false, false, "", nil
		out.Blocked = append(out.Blocked, v)
	}

	if out.Invitations, err = s.social.Invitations(me.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not read your invitations")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// friendView fills in everything the rail draws about one person.
// withLastSeen fills in when each of these people was last here.
//
// One query for the whole rail rather than one per name: this is redrawn by
// every signed-in client every couple of seconds. Anybody online is skipped -
// their answer is "now", and it is already on the row.
func (s *Server) withLastSeen(groups ...[]friendView) {
	if !s.accountsOn() {
		return
	}
	ids := []string{}
	for _, g := range groups {
		for _, v := range g {
			if !v.Online {
				ids = append(ids, v.PlayerID)
			}
		}
	}
	if len(ids) == 0 {
		return
	}
	seen, err := s.accounts.LastSeenMany(ids)
	if err != nil {
		// Not worth failing the whole rail over. A missing timestamp shows
		// as "offline" with nothing after it, which is what it was before.
		return
	}
	for _, g := range groups {
		for i := range g {
			// The registry's own value wins when it has one: it is
			// updated on every heartbeat, and the stored copy is written on
			// a timer, so the database is always the coarser of the two.
			at, ok := seen[g[i].PlayerID]
			if !ok || g[i].Online {
				continue
			}
			if g[i].LastSeen == nil || at.After(*g[i].LastSeen) {
				when := at
				g[i].LastSeen = &when
			}
		}
	}
}

func (s *Server) friendView(id string) friendView {
	v := friendView{PlayerID: id, DisplayName: id}
	if p, ok := s.players.Get(id); ok {
		v.DisplayName, v.MMR = p.Nick, p.MMR
		v.Online = s.now().Sub(p.LastSeen) < PresenceWindow
		// Only somebody online can be in a game. A stale flag from a client
		// that quit without saying so would leave a friend permanently
		// "playing".
		v.InGame = v.Online && p.InGame
		if !v.Online && !p.LastSeen.IsZero() {
			at := p.LastSeen
			v.LastSeen = &at
		}
	}
	if s.accountsOn() {
		if a, err := s.accounts.Get(id); err == nil {
			v.DisplayName, v.MMR = a.DisplayName, a.MMR
		}
	}
	v.RoomID = s.whereabouts(id)
	return v
}

// whereabouts finds which room somebody is in, so "join my friend" is one
// click. Whether they are *playing* is a separate question, answered by their
// own service - see friendView.
func (s *Server) whereabouts(id string) string {
	for _, rm := range s.rooms.List() {
		if _, _, seated := rm.SlotOf(id); seated {
			return rm.ID
		}
	}
	return ""
}

// befriend covers request, accept, decline, remove, block and unblock. They
// are one endpoint because they are one gesture with six outcomes, and six
// routes would be six rate limiters to keep in step.
func (s *Server) befriend(w http.ResponseWriter, r *http.Request) {
	if !s.requireSocial(w) {
		return
	}
	me, _ := session(r)

	var body struct {
		TargetID string `json:"target_id"`
		Action   string `json:"action"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.TargetID == "" {
		writeErr(w, http.StatusBadRequest, "target_id is required")
		return
	}
	if s.accountsOn() {
		if _, err := s.accounts.Get(body.TargetID); err != nil {
			writeErr(w, http.StatusNotFound, "no such player")
			return
		}
	}

	var err error
	switch strings.ToLower(strings.TrimSpace(body.Action)) {
	case "", "request", "add":
		err = s.social.Request(me.ID, body.TargetID, s.now())
	case "accept":
		err = s.social.Accept(me.ID, body.TargetID, s.now())
	case "decline":
		err = s.social.Decline(me.ID, body.TargetID)
	case "remove":
		err = s.social.Remove(me.ID, body.TargetID)
	case "block":
		err = s.social.Block(me.ID, body.TargetID, s.now())
	case "unblock":
		err = s.social.Unblock(me.ID, body.TargetID)
	default:
		writeErr(w, http.StatusBadRequest, "action must be request, accept, decline, remove, block or unblock")
		return
	}
	if err != nil {
		writeErr(w, socialStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// privateMessages reads one conversation and marks it read.
func (s *Server) privateMessages(w http.ResponseWriter, r *http.Request) {
	if !s.requireSocial(w) {
		return
	}
	me, _ := session(r)

	var body struct {
		TargetID string `json:"target_id"`
		After    int64  `json:"after"`
		// Body, when present, sends a message before reading.
		Body string `json:"body"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.TargetID == "" {
		writeErr(w, http.StatusBadRequest, "target_id is required")
		return
	}

	if strings.TrimSpace(body.Body) != "" {
		if _, err := s.social.Send(me.ID, body.TargetID, body.Body, s.now()); err != nil {
			writeErr(w, socialStatus(err), err.Error())
			return
		}
	}

	msgs, err := s.social.Conversation(me.ID, body.TargetID, body.After, 100)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not read the conversation")
		return
	}
	// Reading a conversation is what marks it read. A separate call would
	// mean a client that forgot to make it shows a badge for ever.
	if err := s.social.MarkRead(me.ID, body.TargetID, s.now()); err != nil {
		s.log.Error("could not mark messages read", "account", me.ID, "err", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": msgs})
}

// inviteFriend tells a friend a room is open for them, and - if the caller
// hosts an invite-only room - opens the door as well.
func (s *Server) inviteFriend(w http.ResponseWriter, r *http.Request) {
	if !s.requireSocial(w) {
		return
	}
	me, _ := session(r)

	var body struct {
		TargetID string `json:"target_id"`
		RoomID   string `json:"room_id"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.TargetID == "" || body.RoomID == "" {
		writeErr(w, http.StatusBadRequest, "target_id and room_id are required")
		return
	}

	rm, err := s.rooms.Get(body.RoomID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "that room has closed")
		return
	}
	// You have to be in a room to invite somebody to it.
	if _, _, seated := rm.SlotOf(me.ID); !seated {
		writeErr(w, http.StatusForbidden, "you are not in that room")
		return
	}

	if err := s.social.InviteToRoom(me.ID, body.TargetID, body.RoomID, s.now()); err != nil {
		writeErr(w, socialStatus(err), err.Error())
		return
	}
	// The message and the door are two different things (D41). Any member may
	// send the message; only the host's entry on the room's own list actually
	// admits anybody - so this second call succeeds for a host and is
	// harmlessly refused for everybody else.
	if me.ID == rm.HostID {
		if err := s.rooms.Invite(body.RoomID, me.ID, body.TargetID, s.now()); err != nil {
			s.log.Error("could not open the door for an invited friend",
				"room", body.RoomID, "target", body.TargetID, "err", err)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// seenInvitations clears the invitation badge.
func (s *Server) seenInvitations(w http.ResponseWriter, r *http.Request) {
	if !s.requireSocial(w) {
		return
	}
	me, _ := session(r)
	if err := s.social.MarkInvitationsSeen(me.ID, s.now()); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not clear your invitations")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// findPlayer looks somebody up by username, so a friend request has something
// to be addressed to.
//
// Exact match only, and no listing. A search that returned partial matches
// would be a directory of everybody on the platform, which is a thing to be
// scraped rather than a feature anybody asked for.
func (s *Server) findPlayer(w http.ResponseWriter, r *http.Request) {
	if !s.accountsOn() {
		writeErr(w, http.StatusServiceUnavailable, "this server has no accounts")
		return
	}
	username := strings.TrimSpace(r.URL.Query().Get("username"))
	if username == "" {
		writeErr(w, http.StatusBadRequest, "username is required")
		return
	}
	folded, err := account.CleanUsername(username)
	if err != nil {
		writeErr(w, http.StatusNotFound, "no player with that username")
		return
	}
	a, err := s.accounts.ByUsername(folded)
	if err != nil {
		writeErr(w, http.StatusNotFound, "no player with that username")
		return
	}
	writeJSON(w, http.StatusOK, s.friendView(a.ID))
}

func socialStatus(err error) int {
	switch {
	case errors.Is(err, social.ErrSelf),
		errors.Is(err, social.ErrEmptyMessage),
		errors.Is(err, social.ErrMessageTooLong):
		return http.StatusBadRequest
	case errors.Is(err, social.ErrNotFriends):
		return http.StatusForbidden
	case errors.Is(err, social.ErrNoRequest):
		return http.StatusNotFound
	case errors.Is(err, social.ErrTooManyFriends):
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}
