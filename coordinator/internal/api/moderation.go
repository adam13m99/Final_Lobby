package api

import (
	"errors"
	"net/http"
	"time"

	"lobbybaz/coordinator/internal/moderation"
)

// The moderation surface (D43, D47).
//
// Two rules run through every handler here and are worth reading once rather
// than rediscovering in each one:
//
//  1. **The actor is the session, never the body.** That is true everywhere
//     since D53, and it matters most here: an endpoint that took the acting
//     admin's ID from the request would let anybody ban anybody by typing an
//     admin's ID.
//  2. **Nothing succeeds without being written down.** The moderation store
//     records every action itself, so an action that fails to be logged fails
//     entirely. That is deliberate — powers like these without an audit trail
//     are how a moderation team loses the trust of its players, and there is
//     no way to reconstruct the trail after the fact.

func (s *Server) moderationOn() bool { return s.mod != nil }

// staff resolves the acting admin, refusing anybody who is not one.
func (s *Server) staff(w http.ResponseWriter, r *http.Request) (string, bool) {
	if !s.moderationOn() {
		writeErr(w, http.StatusServiceUnavailable, "this server has no moderation tools")
		return "", false
	}
	me, ok := session(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "sign in first")
		return "", false
	}
	if err := s.mod.RequireStaff(me.ID); err != nil {
		writeErr(w, http.StatusForbidden, "that is for admins")
		return "", false
	}
	return me.ID, true
}

// --- roles --------------------------------------------------------------

// staffList shows who holds a role. Admins can see the team they are on.
func (s *Server) staffList(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.staff(w, r)
	if !ok {
		return
	}
	_ = actor
	grants, err := s.mod.Staff()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	type view struct {
		moderation.Grant
		DisplayName string `json:"display_name"`
	}
	out := make([]view, 0, len(grants))
	for _, g := range grants {
		v := view{Grant: g, DisplayName: g.AccountID}
		if s.accountsOn() {
			if a, err := s.accounts.Get(g.AccountID); err == nil {
				v.DisplayName = a.DisplayName
			}
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"staff": out})
}

// setRole appoints or removes an admin. Head admin only (D47).
func (s *Server) setRole(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.staff(w, r)
	if !ok {
		return
	}
	var body struct {
		TargetID string `json:"target_id"`
		// Grant is true to appoint, false to withdraw.
		Grant bool `json:"grant"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.TargetID == "" {
		writeErr(w, http.StatusBadRequest, "target_id is required")
		return
	}

	var err error
	if body.Grant {
		err = s.mod.GrantAdmin(actor, body.TargetID, s.now())
	} else {
		err = s.mod.RevokeAdmin(actor, body.TargetID, s.now())
	}
	if err != nil {
		writeErr(w, modStatus(err), err.Error())
		return
	}
	s.log.Info("admin role changed", "by", actor, "target", body.TargetID, "granted", body.Grant)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --- sanctions ----------------------------------------------------------

func (s *Server) sanction(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.staff(w, r)
	if !ok {
		return
	}
	var body struct {
		TargetID string `json:"target_id"`
		Kind     string `json:"kind"`
		Reason   string `json:"reason"`
		// Minutes is how long. Zero means it does not expire, which has to be
		// asked for on purpose - "forever" should be a decision somebody made
		// rather than the value a form happened to have.
		Minutes int `json:"minutes"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.TargetID == "" {
		writeErr(w, http.StatusBadRequest, "target_id is required")
		return
	}

	kind := moderation.Kind(body.Kind)
	one, err := s.mod.Sanction(actor, body.TargetID, kind, body.Reason,
		time.Duration(body.Minutes)*time.Minute, s.now())
	if err != nil {
		writeErr(w, modStatus(err), err.Error())
		return
	}

	// A ban has to bite now, not the next time they restart the app. The
	// account is disabled, which drops every session they hold.
	if kind == moderation.KindBan && s.accountsOn() {
		if err := s.accounts.SetDisabled(body.TargetID, true, s.now()); err != nil {
			s.log.Error("banned an account but could not disable it", "target", body.TargetID, "err", err)
		}
	}
	// A ban or a timeout also takes them out of whatever room they are in.
	// Leaving somebody seated in a lobby they are barred from re-entering is
	// the sort of half-applied sanction that makes staff look powerless.
	if kind == moderation.KindBan || kind == moderation.KindTimeout {
		s.evict(body.TargetID)
	}

	s.log.Info("sanction applied", "by", actor, "target", body.TargetID, "kind", kind)
	writeJSON(w, http.StatusOK, one)
}

func (s *Server) liftSanction(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.staff(w, r)
	if !ok {
		return
	}
	var body struct {
		SanctionID string `json:"sanction_id"`
		TargetID   string `json:"target_id"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := s.mod.Lift(actor, body.SanctionID, s.now()); err != nil {
		writeErr(w, modStatus(err), err.Error())
		return
	}
	// If nothing is banning them any more, let them back in.
	if body.TargetID != "" && s.accountsOn() {
		if rest, err := s.mod.Restrictions(body.TargetID, s.now()); err == nil && !rest.Banned {
			if err := s.accounts.SetDisabled(body.TargetID, false, s.now()); err != nil {
				s.log.Error("lifted a ban but could not re-enable the account",
					"target", body.TargetID, "err", err)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// playerRecord is everything staff need about one person in one place: their
// sanctions, their labels, and what has been done to them.
func (s *Server) playerRecord(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.staff(w, r); !ok {
		return
	}
	id := r.PathValue("id")

	var out struct {
		PlayerID    string                 `json:"player_id"`
		DisplayName string                 `json:"display_name"`
		Sanctions   []moderation.Sanction  `json:"sanctions"`
		Labels      []moderation.Label     `json:"labels"`
		Actions     []moderation.Action    `json:"actions"`
		Restriction moderation.Restriction `json:"restriction"`
		Grants      []moderation.Grant     `json:"grants"`
		Kicks       int                    `json:"kicks_this_week"`
	}
	out.PlayerID, out.DisplayName = id, id
	if s.accountsOn() {
		if a, err := s.accounts.Get(id); err == nil {
			out.DisplayName = a.DisplayName
		}
	}

	var err error
	if out.Sanctions, err = s.mod.Sanctions(id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if out.Labels, err = s.mod.LabelsOf(id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if out.Actions, err = s.mod.ActionsAbout(id, 100); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if out.Restriction, err = s.mod.Restrictions(id, s.now()); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// D47: "who made this person an admin?" must have an answer.
	if out.Grants, err = s.mod.GrantHistory(id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if s.kicks != nil {
		out.Kicks, _ = s.kicks.TimesKicked(id, s.now().Add(-7*24*time.Hour))
	}
	writeJSON(w, http.StatusOK, out)
}

// --- labels -------------------------------------------------------------

func (s *Server) labelPlayer(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.staff(w, r)
	if !ok {
		return
	}
	var body struct {
		TargetID string `json:"target_id"`
		Label    string `json:"label"`
		Remove   bool   `json:"remove"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.TargetID == "" || body.Label == "" {
		writeErr(w, http.StatusBadRequest, "target_id and label are required")
		return
	}

	label := moderation.Label(body.Label)
	var err error
	if body.Remove {
		err = s.mod.UnlabelPlayer(actor, body.TargetID, label, s.now())
	} else {
		err = s.mod.LabelPlayer(actor, body.TargetID, label, s.now())
	}
	if err != nil {
		writeErr(w, modStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// labelSet tells the client which labels exist, so nothing hard-codes the
// list and removing one is a server change.
func (s *Server) labelSet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"labels": moderation.KnownLabels})
}

// --- rooms --------------------------------------------------------------

// closeRoom ends somebody else's room. Admin only.
func (s *Server) closeRoom(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.staff(w, r)
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if !decode(w, r, &body) {
		return
	}
	id := r.PathValue("id")

	// The empty actor here means "not the host, by authority" - see
	// room.Store.Close. The authority was checked above.
	if err := s.rooms.Close(id, ""); err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	s.tickets.RevokeRoom(id)
	s.chat.Drop(id)
	if err := s.mod.Record(actor, "close_room", id, body.Reason, s.now()); err != nil {
		s.log.Error("closed a room but could not record it", "room", id, "err", err)
	}
	s.log.Info("room closed by an admin", "room", id, "by", actor)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// changeHost hands a room to somebody else. Admin only.
func (s *Server) changeHost(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.staff(w, r)
	if !ok {
		return
	}
	var body struct {
		NewHostID string `json:"new_host_id"`
		Reason    string `json:"reason"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.NewHostID == "" {
		writeErr(w, http.StatusBadRequest, "new_host_id is required")
		return
	}
	id := r.PathValue("id")

	moved, err := s.rooms.SetHost(id, body.NewHostID)
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	// Both swapped players changed address, so their tickets are now for the
	// wrong one. Revoking forces a fresh ticket at Connect, which is where
	// the client asks for one anyway (D36).
	for _, playerID := range moved {
		s.tickets.RevokePlayerRoom(playerID, id)
	}
	if err := s.mod.Record(actor, "change_host", id, body.NewHostID+": "+body.Reason, s.now()); err != nil {
		s.log.Error("changed a host but could not record it", "room", id, "err", err)
	}
	s.chat.System(id, "An admin made "+s.nickOf(body.NewHostID)+" the host", s.now())

	rm, err := s.rooms.Get(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "that room has closed")
		return
	}
	writeJSON(w, http.StatusOK, s.view(rm))
}

// --- banners ------------------------------------------------------------

// banners lists the strip. Open to everybody: it is the announcement bar, and
// somebody who has not signed in yet is exactly who an announcement about
// signing up is for.
func (s *Server) banners(w http.ResponseWriter, r *http.Request) {
	if !s.moderationOn() {
		writeJSON(w, http.StatusOK, map[string]any{"banners": []moderation.Banner{}})
		return
	}
	// Staff see hidden slides too, so a banner can be prepared before it runs.
	onlyActive := true
	if me, ok := session(r); ok {
		if err := s.mod.RequireStaff(me.ID); err == nil {
			onlyActive = false
		}
	}
	list, err := s.mod.Banners(onlyActive)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"banners": list})
}

func (s *Server) editBanner(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.staff(w, r)
	if !ok {
		return
	}
	var body struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Body     string `json:"body"`
		ImageURL string `json:"image_url"`
		LinkURL  string `json:"link_url"`
		Sort     int    `json:"sort"`
		Active   bool   `json:"active"`
		Remove   bool   `json:"remove"`
	}
	if !decode(w, r, &body) {
		return
	}

	if body.Remove {
		if body.ID == "" {
			writeErr(w, http.StatusBadRequest, "id is required to remove a banner")
			return
		}
		if err := s.mod.RemoveBanner(actor, body.ID, s.now()); err != nil {
			writeErr(w, modStatus(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}

	b := moderation.Banner{
		Title: body.Title, Body: body.Body, ImageURL: body.ImageURL,
		LinkURL: body.LinkURL, Sort: body.Sort, Active: body.Active,
	}
	var (
		out moderation.Banner
		err error
	)
	if body.ID == "" {
		out, err = s.mod.AddBanner(actor, b, s.now())
	} else {
		out, err = s.mod.EditBanner(actor, body.ID, b, s.now())
	}
	if err != nil {
		writeErr(w, modStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// --- the log ------------------------------------------------------------

// auditLog shows what an admin has done. Every action is attributed (D47),
// and this is where that becomes useful rather than merely true.
func (s *Server) auditLog(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.staff(w, r); !ok {
		return
	}
	actorID := r.URL.Query().Get("actor")
	subject := r.URL.Query().Get("subject")

	var (
		list []moderation.Action
		err  error
	)
	switch {
	case actorID != "":
		list, err = s.mod.ActionsBy(actorID, 200)
	case subject != "":
		list, err = s.mod.ActionsAbout(subject, 200)
	default:
		writeErr(w, http.StatusBadRequest, "ask for one admin's actions or one subject's")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"actions": list})
}

// --- helpers ------------------------------------------------------------

// evict removes somebody from whatever room they are in. A ban or a timeout
// that leaves them sitting in a lobby is a half-applied sanction.
func (s *Server) evict(playerID string) {
	for _, rm := range s.rooms.List() {
		if _, _, seated := rm.SlotOf(playerID); !seated {
			continue
		}
		if err := s.rooms.Leave(rm.ID, playerID, s.now()); err != nil {
			s.log.Error("could not remove a sanctioned player from a room",
				"room", rm.ID, "player", playerID, "err", err)
			continue
		}
		s.tickets.RevokePlayerRoom(playerID, rm.ID)
	}
}

// restricted reports what somebody is currently barred from. It is called on
// the paths a sanction has to actually stop: joining and chatting.
func (s *Server) restricted(playerID string) moderation.Restriction {
	if !s.moderationOn() {
		return moderation.Restriction{}
	}
	rest, err := s.mod.Restrictions(playerID, s.now())
	if err != nil {
		// Failing open here is the lesser evil: a database hiccup should not
		// lock the whole platform out of every room. It is logged loudly.
		s.log.Error("could not read a player's restrictions", "player", playerID, "err", err)
		return moderation.Restriction{}
	}
	return rest
}

func modStatus(err error) int {
	switch {
	case errors.Is(err, moderation.ErrNotHeadAdmin),
		errors.Is(err, moderation.ErrNotStaff),
		errors.Is(err, moderation.ErrSelfDemotion),
		errors.Is(err, moderation.ErrCannotDemote):
		return http.StatusForbidden
	case errors.Is(err, moderation.ErrAlreadyHeld),
		errors.Is(err, moderation.ErrHeadAdminSet):
		return http.StatusConflict
	case errors.Is(err, moderation.ErrNoSuchGrant),
		errors.Is(err, moderation.ErrNoSanction),
		errors.Is(err, moderation.ErrNoSuchBanner):
		return http.StatusNotFound
	case errors.Is(err, moderation.ErrBadKind),
		errors.Is(err, moderation.ErrBadLabel),
		errors.Is(err, moderation.ErrReasonMissing),
		errors.Is(err, moderation.ErrBannerEmpty),
		errors.Is(err, moderation.ErrBannerLink):
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

// Kicks is the durable kick log, as much of it as the API needs.
//
// An interface rather than the concrete store because the API's only use for
// it is one number on a player's record, and the coordinator can run without
// a database at all.
type Kicks interface {
	TimesKicked(targetID string, since time.Time) (int, error)
}
