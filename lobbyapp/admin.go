package main

// Moderation, from the app's side (D43, D47).
//
// T8 built roles, sanctions, labels, banners and an audit log on the
// coordinator, and nothing that ships could reach any of it: the only way to
// ban somebody was a hand-written curl request from a developer's machine.
// That is the same gap accounts had before T11 - a whole subsystem with no
// door - and it fails in the same way. A moderator who cannot moderate from
// the product will moderate from a chat window instead, and nothing they do
// there is written down.
//
// Two rules shape this file:
//
//   - **The server decides, the app only asks.** Every call below is refused
//     by the coordinator unless the session holds a role. Nothing here is a
//     permission check; hiding the tools is a courtesy to people who are not
//     staff, not a defence against people who are not staff.
//   - **A reason travels with every action.** The coordinator requires one
//     and this refuses to send an empty one, because the audit log is read
//     months later by somebody who was not there.

import (
	"net/http"
	"time"

	"lobbybaz/client/lobby"
)

// staffEvery is how often the staff list is refreshed. Roles change when
// somebody is appointed, which is rare and deliberate.
const staffEvery = 2 * time.Minute

// role folds this account's own role into the state reply, so the interface
// knows whether to draw the moderation entry at all.
//
// It is worked out from the staff list rather than asked for directly: the
// coordinator has no "what am I" endpoint for roles, and the staff list is
// small, cached, and readable by any signed-in account.
func (s *server) role(out map[string]any) {
	if signedIn, _ := out["signed_in"].(bool); !signedIn {
		return
	}
	staff, _ := s.staffCache.get(func() ([]lobby.StaffMember, error) {
		return s.api().Staff()
	})
	me, _ := out["player_id"].(string)
	for _, m := range staff {
		if m.AccountID == me {
			out["role"] = m.Role
			break
		}
	}
	// The list itself is only useful to a head admin, who is the only person
	// who can change it (D47).
	if out["role"] == "head_admin" {
		out["staff"] = staff
	}
}

// lookUp finds one player and returns their whole moderation record.
//
// A username, not an ID: staff are told "smurf_1234 is ruining games", never
// "a_9f2c... is ruining games". Resolving the name here rather than in the
// page means one round trip and no chance of the two halves disagreeing.
func (s *server) lookUp(w http.ResponseWriter, r *http.Request) {
	who := r.URL.Query().Get("username")
	id := r.URL.Query().Get("player_id")
	if who == "" && id == "" {
		fail(w, "a username is required")
		return
	}
	c := s.api()
	if id == "" {
		found, err := c.FindPlayer(who)
		if err != nil {
			fail(w, err.Error())
			return
		}
		id = found.PlayerID
	}
	rec, err := c.PlayerRecord(id)
	if err != nil {
		fail(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// sanction bans, mutes or times somebody out.
//
// Minutes of zero means "until somebody lifts it". That is a real choice and
// the interface makes it one; it is also what an empty number field produces,
// so the page sends the kind and the duration together and never defaults.
func (s *server) sanction(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Target  string `json:"target_id"`
		Kind    string `json:"kind"`
		Reason  string `json:"reason"`
		Minutes int    `json:"minutes"`
	}
	if !decode(w, r, &body) {
		return
	}
	switch body.Kind {
	case "ban", "mute", "timeout":
	default:
		fail(w, "a sanction is a ban, a mute or a timeout")
		return
	}
	if body.Reason == "" {
		fail(w, "a reason is required")
		return
	}
	got, err := s.api().Sanction(body.Target, body.Kind, body.Reason,
		time.Duration(body.Minutes)*time.Minute)
	if err != nil {
		fail(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, got)
}

// liftSanction ends one early. The record is stamped, never deleted.
func (s *server) liftSanction(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Sanction string `json:"sanction_id"`
		Target   string `json:"target_id"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := s.api().LiftSanction(body.Sanction, body.Target); err != nil {
		fail(w, err.Error())
		return
	}
	ok(w)
}

// label puts a visible mark on somebody, or takes one off.
func (s *server) label(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Target string `json:"target_id"`
		Label  string `json:"label"`
		Remove bool   `json:"remove"`
	}
	if !decode(w, r, &body) {
		return
	}
	var err error
	if body.Remove {
		err = s.api().UnlabelPlayer(body.Target, body.Label)
	} else {
		err = s.api().LabelPlayer(body.Target, body.Label)
	}
	if err != nil {
		fail(w, err.Error())
		return
	}
	ok(w)
}

// labelSet is the vocabulary of marks the server recognises.
func (s *server) labelSet(w http.ResponseWriter, r *http.Request) {
	got, err := s.api().Labels()
	if err != nil {
		fail(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"labels": got})
}

// closeRoom ends somebody else's room.
//
// The people in it are told by the same mechanism that tells them their host
// left: their next poll finds the room gone. There is no way to close a room
// quietly, and there should not be.
func (s *server) closeRoom(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RoomID string `json:"room_id"`
		Reason string `json:"reason"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Reason == "" {
		fail(w, "a reason is required")
		return
	}
	if err := s.api().CloseRoom(body.RoomID, body.Reason); err != nil {
		fail(w, err.Error())
		return
	}
	ok(w)
}

// changeHost hands a room to somebody else already playing in it.
func (s *server) changeHost(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RoomID string `json:"room_id"`
		NewID  string `json:"new_host_id"`
		Reason string `json:"reason"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Reason == "" {
		fail(w, "a reason is required")
		return
	}
	got, err := s.api().ChangeHost(body.RoomID, body.NewID, body.Reason)
	if err != nil {
		fail(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, got)
}

// saveBanner adds or edits a slide of the announcement strip.
//
// The strip is cached for five minutes, so an editor who could not see their
// own change would reasonably conclude it had not saved and write it again.
// Forgetting the cache here is what stops that.
func (s *server) saveBanner(w http.ResponseWriter, r *http.Request) {
	var body lobby.Banner
	if !decode(w, r, &body) {
		return
	}
	if body.Title == "" && body.Body == "" {
		fail(w, "an announcement needs something to say")
		return
	}
	got, err := s.api().SaveBanner(body)
	s.bannersCache.forget()
	if err != nil {
		fail(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, got)
}

func (s *server) removeBanner(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}
	if !decode(w, r, &body) {
		return
	}
	err := s.api().RemoveBanner(body.ID)
	s.bannersCache.forget()
	if err != nil {
		fail(w, err.Error())
		return
	}
	ok(w)
}

// setRole appoints or withdraws an admin. Head admin only, enforced by the
// coordinator (D47).
func (s *server) setRole(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Target string `json:"target_id"`
		Grant  bool   `json:"grant"`
	}
	if !decode(w, r, &body) {
		return
	}
	var err error
	if body.Grant {
		err = s.api().GrantAdmin(body.Target)
	} else {
		err = s.api().RevokeAdmin(body.Target)
	}
	s.staffCache.forget()
	if err != nil {
		fail(w, err.Error())
		return
	}
	ok(w)
}

// auditLog reads what one admin has done, or what has been done to one player
// or room. Never both: the coordinator takes one or the other.
func (s *server) auditLog(w http.ResponseWriter, r *http.Request) {
	actor := r.URL.Query().Get("actor")
	subject := r.URL.Query().Get("subject")
	if actor == "" && subject == "" {
		fail(w, "an actor or a subject is required")
		return
	}
	got, err := s.api().AuditLog(actor, subject)
	if err != nil {
		fail(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"actions": got})
}
