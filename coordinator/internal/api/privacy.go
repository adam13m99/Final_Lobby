package api

import (
	"net/http"

	"lobbybaz/coordinator/internal/room"
)

// Friends answers whether two people are friends.
//
// It is an interface because the friend graph arrives in T7 and room privacy
// (D41) is built here. Nil means no friend graph, and a friends-only room
// then admits nobody but its host - which is the honest failure: it refuses,
// rather than quietly letting everybody in.
type Friends interface {
	AreFriends(a, b string) (bool, error)
}

// barred reports whether a sanction stops somebody joining a room, and says
// so in words a player can act on.
//
// It is checked here rather than inside the room package because a sanction is
// platform-wide and a room knows nothing about it. The room's own door (D41)
// and its kick block are separate and both still apply.
func (s *Server) barred(playerID string) (string, bool) {
	rest := s.restricted(playerID)
	switch {
	case rest.Banned:
		return "your account is banned: " + rest.Reason, true
	case rest.Timeout:
		msg := "you are in a timeout: " + rest.Reason
		if !rest.Until.IsZero() {
			msg += " (until " + rest.Until.Format("15:04") + ")"
		}
		return msg, true
	}
	return "", false
}

// applicant assembles what the door needs to know about somebody.
//
// Every field except the password is established here, from the server's own
// records - never taken from the request. A client that could send its own
// MMR could walk into any room with a floor on it, and one that could send
// `friend: true` could walk into any friends-only room.
func (s *Server) applicant(r *http.Request, rm room.Room, id, password string) room.Applicant {
	who := room.Applicant{ID: id, Password: password}

	if p, ok := s.players.Get(id); ok {
		who.MMR = p.MMR
	}
	if s.friends != nil && rm.HostID != "" && rm.HostID != id {
		if yes, err := s.friends.AreFriends(rm.HostID, id); err == nil {
			who.Friend = yes
		}
	}
	return who
}

// doorStatus maps a refusal at the door to an HTTP code.
//
// A wrong password is 403 rather than 401, because 401 means "you are not
// signed in" everywhere else in this API and the client's error handling
// turns that into "sign in again" - which would be a confusing thing to tell
// somebody who simply mistyped a room password.
func doorStatus(err error) (int, bool) {
	switch err {
	case room.ErrNeedRoomPassword, room.ErrWrongRoomPassword,
		room.ErrFriendsOnly, room.ErrInviteOnly, room.ErrMMRTooLow:
		return http.StatusForbidden, true
	case room.ErrBadPrivacy, room.ErrBadMinMMR, room.ErrPasswordRequired:
		return http.StatusBadRequest, true
	}
	return 0, false
}

// setPrivacy changes a room's door. Host only.
func (s *Server) setPrivacy(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PlayerID string `json:"player_id"`
		Privacy  string `json:"privacy"`
		Password string `json:"password"`
		MinMMR   int    `json:"min_mmr"`
	}
	if !decode(w, r, &body) {
		return
	}
	// The session decides who this is; the body only suggests it.
	body.PlayerID = s.actor(r, body.PlayerID)
	if body.PlayerID == "" {
		writeErr(w, http.StatusBadRequest, "player_id is required")
		return
	}

	id := r.PathValue("id")
	err := s.rooms.SetPrivacy(id, body.PlayerID, room.Privacy(body.Privacy), body.Password, body.MinMMR, s.now())
	if err != nil {
		if code, ok := doorStatus(err); ok {
			writeErr(w, code, err.Error())
			return
		}
		writeErr(w, statusFor(err), err.Error())
		return
	}

	rm, err := s.rooms.Get(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "that room has closed")
		return
	}
	s.chat.System(id, "The host changed who can join", s.now())
	s.log.Info("room privacy changed", "room", id, "privacy", body.Privacy, "min_mmr", body.MinMMR)
	writeJSON(w, http.StatusOK, s.view(rm))
}

// invite opens an invite-only room to one named person.
func (s *Server) invite(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PlayerID string `json:"player_id"`
		TargetID string `json:"target_id"`
		// Withdraw removes an invitation instead of granting one.
		Withdraw bool `json:"withdraw"`
	}
	if !decode(w, r, &body) {
		return
	}
	// The session decides who this is; the body only suggests it.
	body.PlayerID = s.actor(r, body.PlayerID)
	if body.PlayerID == "" || body.TargetID == "" {
		writeErr(w, http.StatusBadRequest, "player_id and target_id are required")
		return
	}

	id := r.PathValue("id")
	var err error
	if body.Withdraw {
		err = s.rooms.Uninvite(id, body.PlayerID, body.TargetID)
	} else {
		err = s.rooms.Invite(id, body.PlayerID, body.TargetID, s.now())
	}
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// setDescription changes what the host says their room is for (D42).
//
// It lives beside the door handlers because it is the same kind of thing: the
// host describing their room to people who are not in it. The store refuses
// anybody who is not the host, so this does not check that itself.
func (s *Server) setDescription(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PlayerID    string `json:"player_id"`
		Description string `json:"description"`
	}
	if !decode(w, r, &body) {
		return
	}
	body.PlayerID = s.actor(r, body.PlayerID)
	if body.PlayerID == "" {
		writeErr(w, http.StatusBadRequest, "player_id is required")
		return
	}
	roomID := r.PathValue("id")
	if err := s.rooms.SetDescription(roomID, body.PlayerID, body.Description); err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	rm, err := s.rooms.Get(roomID)
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.view(rm))
}
