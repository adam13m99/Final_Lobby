package api

import (
	"net/http"
	"testing"
	"time"
)

// makeHeadAdmin bootstraps the owner, the way a deployment does (D47).
func (g *authRig) makeHeadAdmin(accountID string) {
	g.t.Helper()
	if err := g.mod.BootstrapHeadAdmin(accountID, time.Now()); err != nil {
		g.t.Fatalf("bootstrapping head admin: %v", err)
	}
}

func TestModerationIsClosedToOrdinaryPlayers(t *testing.T) {
	g := newAuthRig(t)
	ownerID, _ := g.register("owner", "a long enough password")
	pestID, pest := g.register("pest", "a long enough password")
	g.makeHeadAdmin(ownerID)

	for _, call := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/v1/admin/staff", nil},
		{http.MethodPost, "/v1/admin/staff", map[string]any{"target_id": pestID, "grant": true}},
		{http.MethodPost, "/v1/admin/sanction", map[string]any{"target_id": ownerID, "kind": "ban", "reason": "coup"}},
		{http.MethodPost, "/v1/admin/label", map[string]any{"target_id": ownerID, "label": "noob"}},
		{http.MethodPost, "/v1/admin/banners", map[string]any{"title": "hello"}},
		{http.MethodGet, "/v1/admin/players/" + ownerID, nil},
		{http.MethodGet, "/v1/admin/log?actor=" + ownerID, nil},
	} {
		rec, _ := g.do(call.method, call.path, pest, call.body)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s gave %d, want 403", call.method, call.path, rec.Code)
		}
	}
}

func TestOnlyTheHeadAdminAppointsAdminsOverHTTP(t *testing.T) {
	g := newAuthRig(t)
	ownerID, owner := g.register("owner", "a long enough password")
	adminID, admin := g.register("admin", "a long enough password")
	hopefulID, _ := g.register("hopeful", "a long enough password")
	g.makeHeadAdmin(ownerID)

	if rec, _ := g.do(http.MethodPost, "/v1/admin/staff", owner, map[string]any{
		"target_id": adminID, "grant": true,
	}); rec.Code != http.StatusOK {
		t.Fatalf("the head admin could not appoint: %d", rec.Code)
	}
	// The new admin can use the tools...
	if rec, _ := g.do(http.MethodGet, "/v1/admin/staff", admin, nil); rec.Code != http.StatusOK {
		t.Errorf("a new admin cannot see the staff list: %d", rec.Code)
	}
	// ...but cannot appoint anybody.
	if rec, _ := g.do(http.MethodPost, "/v1/admin/staff", admin, map[string]any{
		"target_id": hopefulID, "grant": true,
	}); rec.Code != http.StatusForbidden {
		t.Errorf("an admin appointed another admin: %d", rec.Code)
	}
}

// A ban has to bite now, not the next time they restart the app.
func TestABanEndsTheSessionAndEmptiesTheSeat(t *testing.T) {
	g := newAuthRig(t)
	ownerID, owner := g.register("owner", "a long enough password")
	pestID, pest := g.register("pest", "a long enough password")
	g.makeHeadAdmin(ownerID)

	_, out := g.do(http.MethodPost, "/v1/rooms", pest, map[string]any{"nick": "pest", "name": "game"})
	roomID, _ := out["room_id"].(string)
	if roomID == "" {
		t.Fatalf("no room: %v", out)
	}

	if rec, _ := g.do(http.MethodPost, "/v1/admin/sanction", owner, map[string]any{
		"target_id": pestID, "kind": "ban", "reason": "cheating",
	}); rec.Code != http.StatusOK {
		t.Fatalf("banning: %d", rec.Code)
	}

	// Their session is gone.
	if rec, _ := g.do(http.MethodGet, "/v1/auth/me", pest, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("a banned player kept their session: %d", rec.Code)
	}
	// And they are out of the room rather than sitting in one they cannot
	// re-enter.
	_, rooms := g.do(http.MethodGet, "/v1/rooms", "", nil)
	for _, one := range rooms["rooms"].([]any) {
		rm, _ := one.(map[string]any)
		for _, m := range rm["members"].([]any) {
			member, _ := m.(map[string]any)
			if member["player_id"] == pestID {
				t.Errorf("a banned player is still seated: %v", member)
			}
		}
	}

	// Signing back in is refused too.
	if rec, _ := g.do(http.MethodPost, "/v1/auth/login", "", map[string]any{
		"username": "pest", "password": "a long enough password",
	}); rec.Code != http.StatusForbidden {
		t.Errorf("a banned player signed back in: %d", rec.Code)
	}
}

func TestATimeoutStopsJoiningAndAMuteStopsTalking(t *testing.T) {
	g := newAuthRig(t)
	ownerID, owner := g.register("owner", "a long enough password")
	_, host := g.register("host", "a long enough password")
	pestID, pest := g.register("pest", "a long enough password")
	g.makeHeadAdmin(ownerID)

	_, out := g.do(http.MethodPost, "/v1/rooms", host, map[string]any{"nick": "host", "name": "game"})
	roomID, _ := out["room_id"].(string)

	// A mute stops the voice, not the game.
	if rec, _ := g.do(http.MethodPost, "/v1/admin/sanction", owner, map[string]any{
		"target_id": pestID, "kind": "mute", "reason": "shouting", "minutes": 60,
	}); rec.Code != http.StatusOK {
		t.Fatalf("muting: %d", rec.Code)
	}
	if rec, _ := g.do(http.MethodPost, "/v1/rooms/"+roomID+"/join", pest, map[string]any{
		"nick": "pest",
	}); rec.Code != http.StatusOK {
		t.Errorf("a mute stopped somebody playing: %d", rec.Code)
	}
	if rec, _ := g.do(http.MethodPost, "/v1/chat", pest, map[string]any{
		"nick": "pest", "channel": roomID, "text": "hello",
	}); rec.Code != http.StatusForbidden {
		t.Errorf("a muted player talked: %d", rec.Code)
	}

	// A timeout stops the game and empties the seat.
	if rec, _ := g.do(http.MethodPost, "/v1/admin/sanction", owner, map[string]any{
		"target_id": pestID, "kind": "timeout", "reason": "cool off", "minutes": 30,
	}); rec.Code != http.StatusOK {
		t.Fatalf("timeout: %d", rec.Code)
	}
	if rec, _ := g.do(http.MethodPost, "/v1/rooms/"+roomID+"/join", pest, map[string]any{
		"nick": "pest",
	}); rec.Code != http.StatusForbidden {
		t.Errorf("a player in a timeout joined a room: %d", rec.Code)
	}
	if rec, _ := g.do(http.MethodPost, "/v1/rooms", pest, map[string]any{
		"nick": "pest", "name": "my own room",
	}); rec.Code != http.StatusForbidden {
		t.Errorf("a player in a timeout opened their own room: %d", rec.Code)
	}
}

func TestLiftingABanLetsSomebodyBackIn(t *testing.T) {
	g := newAuthRig(t)
	ownerID, owner := g.register("owner", "a long enough password")
	pestID, _ := g.register("pest", "a long enough password")
	g.makeHeadAdmin(ownerID)

	rec, sanction := g.do(http.MethodPost, "/v1/admin/sanction", owner, map[string]any{
		"target_id": pestID, "kind": "ban", "reason": "a misunderstanding",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("banning: %d", rec.Code)
	}
	id, _ := sanction["id"].(string)

	if rec, _ := g.do(http.MethodPost, "/v1/admin/sanction/lift", owner, map[string]any{
		"sanction_id": id, "target_id": pestID,
	}); rec.Code != http.StatusOK {
		t.Fatalf("lifting: %d", rec.Code)
	}
	if rec, _ := g.do(http.MethodPost, "/v1/auth/login", "", map[string]any{
		"username": "pest", "password": "a long enough password",
	}); rec.Code != http.StatusOK {
		t.Errorf("an unbanned player could not sign in: %d", rec.Code)
	}
}

// D43: closing a room is one of the powers, and D47 says it is attributed.
func TestAnAdminClosesARoomAndItIsRecorded(t *testing.T) {
	g := newAuthRig(t)
	ownerID, owner := g.register("owner", "a long enough password")
	_, host := g.register("host", "a long enough password")
	g.makeHeadAdmin(ownerID)

	_, out := g.do(http.MethodPost, "/v1/rooms", host, map[string]any{"nick": "host", "name": "game"})
	roomID, _ := out["room_id"].(string)

	if rec, _ := g.do(http.MethodPost, "/v1/admin/rooms/"+roomID+"/close", owner, map[string]any{
		"reason": "griefing",
	}); rec.Code != http.StatusOK {
		t.Fatalf("closing: %d", rec.Code)
	}
	_, rooms := g.do(http.MethodGet, "/v1/rooms", "", nil)
	if list, _ := rooms["rooms"].([]any); len(list) != 0 {
		t.Fatalf("the room survived: %v", list)
	}

	rec, log := g.do(http.MethodGet, "/v1/admin/log?subject="+roomID, owner, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("reading the log: %d", rec.Code)
	}
	actions, _ := log["actions"].([]any)
	if len(actions) != 1 {
		t.Fatalf("log = %v", actions)
	}
	first, _ := actions[0].(map[string]any)
	if first["actor_id"] != ownerID || first["action"] != "close_room" || first["detail"] != "griefing" {
		t.Errorf("log entry = %v", first)
	}
}

// Changing a room's host is host migration under another name (D43): the
// escape hatch for when a room dying with its host is the wrong outcome.
func TestAnAdminChangesTheHost(t *testing.T) {
	g := newAuthRig(t)
	ownerID, owner := g.register("owner", "a long enough password")
	hostID, host := g.register("host", "a long enough password")
	mateID, mate := g.register("mate", "a long enough password")
	g.makeHeadAdmin(ownerID)

	_, out := g.do(http.MethodPost, "/v1/rooms", host, map[string]any{"nick": "host", "name": "game"})
	roomID, _ := out["room_id"].(string)
	hostIP, _ := out["virtual_ip"].(string)

	if rec, _ := g.do(http.MethodPost, "/v1/rooms/"+roomID+"/join", mate, map[string]any{
		"nick": "mate",
	}); rec.Code != http.StatusOK {
		t.Fatalf("joining: %d", rec.Code)
	}

	rec, view := g.do(http.MethodPost, "/v1/admin/rooms/"+roomID+"/host", owner, map[string]any{
		"new_host_id": mateID, "reason": "the host went quiet",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("changing host: %d %s", rec.Code, rec.Body.String())
	}
	if view["host_id"] != mateID {
		t.Fatalf("host = %v, want %s", view["host_id"], mateID)
	}

	// The new host takes the address every client was told to connect to, so
	// they must come back with a fresh ticket rather than their old one.
	rec, fresh := g.do(http.MethodPost, "/v1/rooms/"+roomID+"/connect", mate, map[string]any{})
	if rec.Code != http.StatusOK {
		t.Fatalf("reconnecting: %d", rec.Code)
	}
	if fresh["virtual_ip"] != hostIP {
		t.Errorf("the new host is at %v, want the host address %s", fresh["virtual_ip"], hostIP)
	}
	if fresh["is_host"] != true {
		t.Errorf("the new host is not marked as host: %v", fresh)
	}

	// And the old host is still in the room, at the seat the new host left.
	rec, _ = g.do(http.MethodPost, "/v1/rooms/"+roomID+"/connect", host, map[string]any{})
	if rec.Code != http.StatusOK {
		t.Errorf("the old host was thrown out: %d", rec.Code)
	}
	_ = hostID
}

// A watcher cannot become the host: the host is the person whose PC runs the
// game, and somebody in the gallery is not playing.
func TestAnObserverCannotBeMadeHost(t *testing.T) {
	g := newAuthRig(t)
	ownerID, owner := g.register("owner", "a long enough password")
	_, host := g.register("host", "a long enough password")
	watcherID, watcher := g.register("watcher", "a long enough password")
	g.makeHeadAdmin(ownerID)

	_, out := g.do(http.MethodPost, "/v1/rooms", host, map[string]any{"nick": "host", "name": "game"})
	roomID, _ := out["room_id"].(string)
	if rec, _ := g.do(http.MethodPost, "/v1/rooms/"+roomID+"/spectate", watcher, map[string]any{
		"nick": "watcher",
	}); rec.Code != http.StatusOK {
		t.Fatalf("spectating: %d", rec.Code)
	}

	if rec, _ := g.do(http.MethodPost, "/v1/admin/rooms/"+roomID+"/host", owner, map[string]any{
		"new_host_id": watcherID,
	}); rec.Code == http.StatusOK {
		t.Error("an observer was made the host")
	}
}

func TestLabelsAppearOnAPlayersRecord(t *testing.T) {
	g := newAuthRig(t)
	ownerID, owner := g.register("owner", "a long enough password")
	playerID, _ := g.register("player", "a long enough password")
	g.makeHeadAdmin(ownerID)

	if rec, _ := g.do(http.MethodPost, "/v1/admin/label", owner, map[string]any{
		"target_id": playerID, "label": "verified",
	}); rec.Code != http.StatusOK {
		t.Fatalf("labelling: %d", rec.Code)
	}
	if rec, _ := g.do(http.MethodPost, "/v1/admin/label", owner, map[string]any{
		"target_id": playerID, "label": "made-up",
	}); rec.Code != http.StatusBadRequest {
		t.Errorf("an invented label was accepted: %d", rec.Code)
	}

	rec, record := g.do(http.MethodGet, "/v1/admin/players/"+playerID, owner, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("record: %d", rec.Code)
	}
	labels, _ := record["labels"].([]any)
	if len(labels) != 1 || labels[0] != "verified" {
		t.Errorf("labels = %v", labels)
	}

	// The client is told which labels exist, so nothing hard-codes the list.
	rec, set := g.do(http.MethodGet, "/v1/admin/labels", owner, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("label set: %d", rec.Code)
	}
	if known, _ := set["labels"].([]any); len(known) < 4 {
		t.Errorf("labels = %v", known)
	}
}

// The strip is open to everybody, including somebody who has not signed in:
// an announcement about signing up is precisely for them.
func TestTheBannerStripIsPublicButEditingIsNot(t *testing.T) {
	g := newAuthRig(t)
	ownerID, owner := g.register("owner", "a long enough password")
	_, player := g.register("player", "a long enough password")
	g.makeHeadAdmin(ownerID)

	if rec, _ := g.do(http.MethodPost, "/v1/admin/banners", player, map[string]any{
		"title": "buy gold", "active": true,
	}); rec.Code != http.StatusForbidden {
		t.Errorf("an ordinary player wrote a banner: %d", rec.Code)
	}

	rec, made := g.do(http.MethodPost, "/v1/admin/banners", owner, map[string]any{
		"title": "Tournament on Friday", "body": "Sign up in the lobby",
		"link_url": "https://example.com", "active": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("adding: %d %s", rec.Code, rec.Body.String())
	}
	bannerID, _ := made["id"].(string)

	rec, list := g.do(http.MethodGet, "/v1/banners", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("reading the strip signed out: %d", rec.Code)
	}
	banners, _ := list["banners"].([]any)
	if len(banners) != 1 {
		t.Fatalf("banners = %v", banners)
	}

	if rec, _ := g.do(http.MethodPost, "/v1/admin/banners", owner, map[string]any{
		"id": bannerID, "remove": true,
	}); rec.Code != http.StatusOK {
		t.Fatalf("removing: %d", rec.Code)
	}
	_, list = g.do(http.MethodGet, "/v1/banners", "", nil)
	if banners, _ := list["banners"].([]any); len(banners) != 0 {
		t.Errorf("the banner survived removal: %v", banners)
	}
}

func TestABannerLinkCannotBeExecutable(t *testing.T) {
	g := newAuthRig(t)
	ownerID, owner := g.register("owner", "a long enough password")
	g.makeHeadAdmin(ownerID)

	if rec, _ := g.do(http.MethodPost, "/v1/admin/banners", owner, map[string]any{
		"title": "Click here", "link_url": "javascript:alert(1)", "active": true,
	}); rec.Code != http.StatusBadRequest {
		t.Errorf("a javascript: link was accepted: %d", rec.Code)
	}
}

// A coordinator with no database has no moderation tools, and says so rather
// than leaving the endpoints open.
func TestModerationIsClosedWithoutADatabase(t *testing.T) {
	g := newPlainRig(t)
	if rec, _ := g.do(http.MethodGet, "/v1/admin/staff", "", nil); rec.Code == http.StatusOK {
		t.Errorf("the staff list was readable with no moderation store: %d", rec.Code)
	}
	// The banner strip degrades to empty rather than failing, so a lobby
	// running without a database still draws.
	rec, list := g.do(http.MethodGet, "/v1/banners", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("banners: %d", rec.Code)
	}
	if banners, _ := list["banners"].([]any); len(banners) != 0 {
		t.Errorf("banners = %v", banners)
	}
}
