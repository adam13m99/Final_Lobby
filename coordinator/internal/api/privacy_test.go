package api

import (
	"net/http"
	"testing"
)

// friendGraph is a stand-in for T7's real one.
type friendGraph map[string]map[string]bool

func (g friendGraph) AreFriends(a, b string) (bool, error) { return g[a][b], nil }

func (g friendGraph) befriend(a, b string) {
	for _, pair := range [][2]string{{a, b}, {b, a}} {
		if g[pair[0]] == nil {
			g[pair[0]] = map[string]bool{}
		}
		g[pair[0]][pair[1]] = true
	}
}

func TestAPasswordRoomOverHTTP(t *testing.T) {
	g := newAuthRig(t)
	_, host := g.register("host", "a long enough password")
	_, guest := g.register("guest", "a long enough password")

	rec, out := g.do(http.MethodPost, "/v1/rooms", host, map[string]any{
		"nick": "host", "name": "private game",
		"privacy": "password", "password": "open sesame",
	})
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	roomID, _ := out["room_id"].(string)
	if roomID == "" {
		t.Fatalf("no room id in %v", out)
	}

	// The padlock is visible in the list; the password is not.
	_, rooms := g.do(http.MethodGet, "/v1/rooms", "", nil)
	list, _ := rooms["rooms"].([]any)
	first, _ := list[0].(map[string]any)
	if first["privacy"] != "password" || first["needs_password"] != true {
		t.Errorf("room list does not show the padlock: %v", first)
	}
	for k, v := range first {
		if s, ok := v.(string); ok && s == "open sesame" {
			t.Fatalf("the room password is in the public room list under %q", k)
		}
	}

	if rec, _ := g.do(http.MethodPost, "/v1/rooms/"+roomID+"/join", guest, map[string]any{
		"nick": "guest",
	}); rec.Code != http.StatusForbidden {
		t.Errorf("joining with no password gave %d, want 403", rec.Code)
	}
	if rec, _ := g.do(http.MethodPost, "/v1/rooms/"+roomID+"/join", guest, map[string]any{
		"nick": "guest", "password": "open sesam",
	}); rec.Code != http.StatusForbidden {
		t.Errorf("joining with a wrong password gave %d, want 403", rec.Code)
	}
	if rec, _ := g.do(http.MethodPost, "/v1/rooms/"+roomID+"/join", guest, map[string]any{
		"nick": "guest", "password": "open sesame",
	}); rec.Code != http.StatusOK {
		t.Errorf("joining with the right password gave %d", rec.Code)
	}
}

// A room asked for with a door it cannot have must not be left standing open.
func TestARoomThatCannotHaveTheDoorItAskedForIsNotLeftOpen(t *testing.T) {
	g := newAuthRig(t)
	_, host := g.register("host", "a long enough password")

	rec, _ := g.do(http.MethodPost, "/v1/rooms", host, map[string]any{
		"nick": "host", "name": "private game", "privacy": "password",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", rec.Code)
	}
	_, rooms := g.do(http.MethodGet, "/v1/rooms", "", nil)
	if list, _ := rooms["rooms"].([]any); len(list) != 0 {
		t.Fatalf("a room was left standing: %v", list)
	}
}

func TestAFriendsOnlyRoomOverHTTP(t *testing.T) {
	g := newAuthRig(t)
	hostID, host := g.register("host", "a long enough password")
	mateID, mate := g.register("mate", "a long enough password")
	_, stranger := g.register("stranger", "a long enough password")

	graph := friendGraph{}
	graph.befriend(hostID, mateID)
	g.setFriends(graph)

	_, out := g.do(http.MethodPost, "/v1/rooms", host, map[string]any{
		"nick": "host", "name": "friends game", "privacy": "friends",
	})
	roomID, _ := out["room_id"].(string)

	if rec, _ := g.do(http.MethodPost, "/v1/rooms/"+roomID+"/join", stranger, map[string]any{
		"nick": "stranger",
	}); rec.Code != http.StatusForbidden {
		t.Errorf("a stranger got %d, want 403", rec.Code)
	}
	if rec, body := g.do(http.MethodPost, "/v1/rooms/"+roomID+"/join", mate, map[string]any{
		"nick": "mate",
	}); rec.Code != http.StatusOK {
		t.Errorf("a friend got %d: %v", rec.Code, body)
	}
}

func TestAnInviteOnlyRoomOverHTTP(t *testing.T) {
	g := newAuthRig(t)
	_, host := g.register("host", "a long enough password")
	guestID, guest := g.register("guest", "a long enough password")

	_, out := g.do(http.MethodPost, "/v1/rooms", host, map[string]any{
		"nick": "host", "name": "invite game", "privacy": "invite",
	})
	roomID, _ := out["room_id"].(string)

	if rec, _ := g.do(http.MethodPost, "/v1/rooms/"+roomID+"/join", guest, map[string]any{
		"nick": "guest",
	}); rec.Code != http.StatusForbidden {
		t.Errorf("an uninvited player got %d, want 403", rec.Code)
	}

	// Only the host may invite.
	if rec, _ := g.do(http.MethodPost, "/v1/rooms/"+roomID+"/invite", guest, map[string]any{
		"target_id": guestID,
	}); rec.Code != http.StatusForbidden {
		t.Errorf("a guest invited themselves: %d", rec.Code)
	}
	if rec, _ := g.do(http.MethodPost, "/v1/rooms/"+roomID+"/invite", host, map[string]any{
		"target_id": guestID,
	}); rec.Code != http.StatusOK {
		t.Fatalf("the host could not invite: %d", rec.Code)
	}
	if rec, _ := g.do(http.MethodPost, "/v1/rooms/"+roomID+"/join", guest, map[string]any{
		"nick": "guest",
	}); rec.Code != http.StatusOK {
		t.Errorf("an invited player got %d", rec.Code)
	}
}

// The MMR the door measures comes from the account row, never from the
// request. A client that could send its own would walk into any room with a
// floor on it.
func TestTheMMRFloorUsesTheDeclaredRatingNotTheClaimedOne(t *testing.T) {
	g := newAuthRig(t)
	_, host := g.register("host", "a long enough password")
	_, low := g.register("beginner", "a long enough password")

	if rec, _ := g.do(http.MethodPost, "/v1/me", low, map[string]any{"mmr": 900}); rec.Code != http.StatusOK {
		t.Fatalf("declaring MMR gave %d", rec.Code)
	}

	_, out := g.do(http.MethodPost, "/v1/rooms", host, map[string]any{
		"nick": "host", "name": "high game", "min_mmr": 3000,
	})
	roomID, _ := out["room_id"].(string)

	// The join body has no MMR field at all, so there is nothing to lie with;
	// the coordinator reads the declared 900 and refuses.
	if rec, _ := g.do(http.MethodPost, "/v1/rooms/"+roomID+"/join", low, map[string]any{
		"nick": "beginner",
	}); rec.Code != http.StatusForbidden {
		t.Errorf("a 900-MMR player got into a 3000 room: %d", rec.Code)
	}

	// The floor is visible in the list, so nobody joins to find out.
	_, rooms := g.do(http.MethodGet, "/v1/rooms", "", nil)
	list, _ := rooms["rooms"].([]any)
	first, _ := list[0].(map[string]any)
	if first["min_mmr"] != float64(3000) {
		t.Errorf("min_mmr in the room list = %v, want 3000", first["min_mmr"])
	}
}

func TestOnlyTheHostChangesTheDoorOverHTTP(t *testing.T) {
	g := newAuthRig(t)
	_, host := g.register("host", "a long enough password")
	_, other := g.register("other", "a long enough password")

	_, out := g.do(http.MethodPost, "/v1/rooms", host, map[string]any{"nick": "host", "name": "game"})
	roomID, _ := out["room_id"].(string)

	if rec, _ := g.do(http.MethodPost, "/v1/rooms/"+roomID+"/privacy", other, map[string]any{
		"privacy": "invite",
	}); rec.Code != http.StatusForbidden {
		t.Errorf("a non-host changed the door: %d", rec.Code)
	}
	if rec, view := g.do(http.MethodPost, "/v1/rooms/"+roomID+"/privacy", host, map[string]any{
		"privacy": "invite", "min_mmr": 2000,
	}); rec.Code != http.StatusOK {
		t.Errorf("the host could not change the door: %d", rec.Code)
	} else if view["privacy"] != "invite" || view["min_mmr"] != float64(2000) {
		t.Errorf("the room view did not come back changed: %v", view)
	}
}

// Without a friend graph a friends-only room refuses everybody but its host.
// That is the honest failure: it refuses, rather than quietly letting
// everybody in.
func TestWithoutAFriendGraphAFriendsOnlyRoomRefusesEverybody(t *testing.T) {
	g := newAuthRig(t) // no Friends configured
	_, host := g.register("host", "a long enough password")
	_, other := g.register("other", "a long enough password")

	_, out := g.do(http.MethodPost, "/v1/rooms", host, map[string]any{
		"nick": "host", "name": "game", "privacy": "friends",
	})
	roomID, _ := out["room_id"].(string)

	if rec, _ := g.do(http.MethodPost, "/v1/rooms/"+roomID+"/join", other, map[string]any{
		"nick": "other",
	}); rec.Code != http.StatusForbidden {
		t.Errorf("got %d, want 403", rec.Code)
	}
}
