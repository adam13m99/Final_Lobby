package api

import (
	"net/http"
	"testing"
)

// friendsOf pulls one named list out of GET /v1/friends.
func (g *authRig) friendsOf(session, list string) []any {
	g.t.Helper()
	rec, out := g.do(http.MethodGet, "/v1/friends", session, nil)
	if rec.Code != http.StatusOK {
		g.t.Fatalf("GET /v1/friends: %d %s", rec.Code, rec.Body.String())
	}
	got, _ := out[list].([]any)
	return got
}

func (g *authRig) act(session, target, action string) *testResponse {
	g.t.Helper()
	rec, out := g.do(http.MethodPost, "/v1/friends", session, map[string]any{
		"target_id": target, "action": action,
	})
	return &testResponse{code: rec.Code, body: out}
}

type testResponse struct {
	code int
	body map[string]any
}

func TestAFriendRequestTravelsAndIsAnswered(t *testing.T) {
	g := newAuthRig(t)
	rezaID, reza := g.register("reza", "a long enough password")
	aliID, ali := g.register("ali", "a long enough password")

	if r := g.act(reza, aliID, "request"); r.code != http.StatusOK {
		t.Fatalf("request: %d %v", r.code, r.body)
	}
	if got := g.friendsOf(ali, "incoming"); len(got) != 1 {
		t.Fatalf("ali's incoming = %v", got)
	}
	if got := g.friendsOf(reza, "outgoing"); len(got) != 1 {
		t.Fatalf("reza's outgoing = %v", got)
	}
	// An unanswered request is not a friendship.
	if got := g.friendsOf(reza, "friends"); len(got) != 0 {
		t.Fatalf("reza already has friends: %v", got)
	}

	if r := g.act(ali, rezaID, "accept"); r.code != http.StatusOK {
		t.Fatalf("accept: %d %v", r.code, r.body)
	}
	for _, who := range []struct{ name, session string }{{"reza", reza}, {"ali", ali}} {
		got := g.friendsOf(who.session, "friends")
		if len(got) != 1 {
			t.Errorf("%s has %d friends, want 1", who.name, len(got))
		}
	}
}

func TestAFriendRequestNeedsSomebodyRealToBeAddressedTo(t *testing.T) {
	g := newAuthRig(t)
	_, reza := g.register("reza", "a long enough password")
	if r := g.act(reza, "a_nobody", "request"); r.code != http.StatusNotFound {
		t.Fatalf("got %d, want 404", r.code)
	}
}

// Exact match only, and no listing. A search that returned partial matches
// would be a directory of everybody on the platform.
func TestFindingSomebodyByUsername(t *testing.T) {
	g := newAuthRig(t)
	_, reza := g.register("reza", "a long enough password")
	aliID, _ := g.register("ali", "a long enough password")

	rec, out := g.do(http.MethodGet, "/v1/players/find?username=ALI", reza, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	if out["player_id"] != aliID {
		t.Errorf("found %v, want %s", out["player_id"], aliID)
	}
	if rec, _ := g.do(http.MethodGet, "/v1/players/find?username=al", reza, nil); rec.Code != http.StatusNotFound {
		t.Errorf("a partial username matched: %d", rec.Code)
	}
	if rec, _ := g.do(http.MethodGet, "/v1/players/find", reza, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("an empty search gave %d", rec.Code)
	}
}

// Blocking is silent. The person blocked gets a 200 and nothing happens,
// because an error would tell them they had been blocked.
func TestBlockingIsSilentToThePersonBlocked(t *testing.T) {
	g := newAuthRig(t)
	rezaID, reza := g.register("reza", "a long enough password")
	pestID, pest := g.register("pest", "a long enough password")

	if r := g.act(reza, pestID, "block"); r.code != http.StatusOK {
		t.Fatalf("block: %d %v", r.code, r.body)
	}
	if r := g.act(pest, rezaID, "request"); r.code != http.StatusOK {
		t.Fatalf("the blocked person got an error, which tells them they were blocked: %d", r.code)
	}
	if got := g.friendsOf(reza, "incoming"); len(got) != 0 {
		t.Fatalf("a blocked person's request arrived: %v", got)
	}
	if got := g.friendsOf(reza, "blocked"); len(got) != 1 {
		t.Fatalf("the block list = %v", got)
	}
}

func TestPrivateMessagesBetweenFriends(t *testing.T) {
	g := newAuthRig(t)
	rezaID, reza := g.register("reza", "a long enough password")
	aliID, ali := g.register("ali", "a long enough password")
	_, stranger := g.register("stranger", "a long enough password")

	// A stranger cannot start a conversation. An open inbox is an invitation
	// to spam, and the friends list is the permission list people already
	// curate.
	if rec, _ := g.do(http.MethodPost, "/v1/friends/messages", stranger, map[string]any{
		"target_id": rezaID, "body": "buy gold cheap",
	}); rec.Code != http.StatusForbidden {
		t.Errorf("a stranger messaged somebody: %d", rec.Code)
	}

	g.act(reza, aliID, "request")
	g.act(ali, rezaID, "accept")

	if rec, _ := g.do(http.MethodPost, "/v1/friends/messages", reza, map[string]any{
		"target_id": aliID, "body": "سلام، بازی می‌کنی؟",
	}); rec.Code != http.StatusOK {
		t.Fatalf("sending: %d", rec.Code)
	}

	// The unread count reaches the rail, so a badge can go next to the name.
	list := g.friendsOf(ali, "friends")
	first, _ := list[0].(map[string]any)
	if first["unread"] != float64(1) {
		t.Errorf("unread = %v, want 1", first["unread"])
	}

	rec, out := g.do(http.MethodPost, "/v1/friends/messages", ali, map[string]any{
		"target_id": rezaID,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("reading: %d", rec.Code)
	}
	msgs, _ := out["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages = %v", msgs)
	}

	// Reading is what marks it read; a separate call would mean a client that
	// forgot to make it shows a badge for ever.
	list = g.friendsOf(ali, "friends")
	first, _ = list[0].(map[string]any)
	if first["unread"] != nil && first["unread"] != float64(0) {
		t.Errorf("unread after reading = %v", first["unread"])
	}
}

// The message and the door are two different things (D41). Any member may
// send the message; only the host's invitation opens an invite-only room.
func TestInvitingAFriendSendsTheMessageAndOpensTheDoor(t *testing.T) {
	g := newAuthRig(t)
	hostID, host := g.register("host", "a long enough password")
	mateID, mate := g.register("mate", "a long enough password")

	g.act(host, mateID, "request")
	g.act(mate, hostID, "accept")

	_, out := g.do(http.MethodPost, "/v1/rooms", host, map[string]any{
		"nick": "host", "name": "invite game", "privacy": "invite",
	})
	roomID, _ := out["room_id"].(string)

	if rec, _ := g.do(http.MethodPost, "/v1/friends/invite", host, map[string]any{
		"target_id": mateID, "room_id": roomID,
	}); rec.Code != http.StatusOK {
		t.Fatalf("inviting: %d", rec.Code)
	}

	// The notification arrived...
	rec, list := g.do(http.MethodGet, "/v1/friends", mate, nil)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	invites, _ := list["invitations"].([]any)
	if len(invites) != 1 {
		t.Fatalf("invitations = %v", invites)
	}

	// ...and, because it came from the host, so did the door.
	if rec, _ := g.do(http.MethodPost, "/v1/rooms/"+roomID+"/join", mate, map[string]any{
		"nick": "mate",
	}); rec.Code != http.StatusOK {
		t.Errorf("the invited friend could not join: %d", rec.Code)
	}

	if rec, _ := g.do(http.MethodPost, "/v1/friends/invitations/seen", mate, nil); rec.Code != http.StatusOK {
		t.Fatalf("clearing the badge: %d", rec.Code)
	}
	_, list = g.do(http.MethodGet, "/v1/friends", mate, nil)
	if invites, _ := list["invitations"].([]any); len(invites) != 0 {
		t.Errorf("the badge did not clear: %v", invites)
	}
}

func TestYouCannotInviteSomebodyToARoomYouAreNotIn(t *testing.T) {
	g := newAuthRig(t)
	hostID, host := g.register("host", "a long enough password")
	mateID, mate := g.register("mate", "a long enough password")
	outsiderID, outsider := g.register("outsider", "a long enough password")

	g.act(outsider, mateID, "request")
	g.act(mate, outsiderID, "accept")
	_ = hostID

	_, out := g.do(http.MethodPost, "/v1/rooms", host, map[string]any{"nick": "host", "name": "game"})
	roomID, _ := out["room_id"].(string)

	if rec, _ := g.do(http.MethodPost, "/v1/friends/invite", outsider, map[string]any{
		"target_id": mateID, "room_id": roomID,
	}); rec.Code != http.StatusForbidden {
		t.Errorf("somebody outside the room invited a friend into it: %d", rec.Code)
	}
}

// The friend graph the room door consults is the real one, so a friends-only
// room admits a friend and nobody else without any test scaffolding.
func TestAFriendsOnlyRoomUsesTheRealFriendGraph(t *testing.T) {
	g := newAuthRig(t)
	hostID, host := g.register("host", "a long enough password")
	mateID, mate := g.register("mate", "a long enough password")
	_, stranger := g.register("stranger", "a long enough password")
	g.setFriends(g.soc)

	g.act(host, mateID, "request")
	g.act(mate, hostID, "accept")

	_, out := g.do(http.MethodPost, "/v1/rooms", host, map[string]any{
		"nick": "host", "name": "friends game", "privacy": "friends",
	})
	roomID, _ := out["room_id"].(string)

	if rec, _ := g.do(http.MethodPost, "/v1/rooms/"+roomID+"/join", stranger, map[string]any{
		"nick": "stranger",
	}); rec.Code != http.StatusForbidden {
		t.Errorf("a stranger got in: %d", rec.Code)
	}
	if rec, _ := g.do(http.MethodPost, "/v1/rooms/"+roomID+"/join", mate, map[string]any{
		"nick": "mate",
	}); rec.Code != http.StatusOK {
		t.Errorf("a real friend was refused: %d", rec.Code)
	}
}

// Presence and whereabouts are what make the rail worth having: a friend who
// is online, and in a room you can click into.
func TestTheRailShowsWhereAFriendIs(t *testing.T) {
	g := newAuthRig(t)
	rezaID, reza := g.register("reza", "a long enough password")
	aliID, ali := g.register("ali", "a long enough password")
	g.act(reza, aliID, "request")
	g.act(ali, rezaID, "accept")

	_, out := g.do(http.MethodPost, "/v1/rooms", ali, map[string]any{"nick": "ali", "name": "game"})
	roomID, _ := out["room_id"].(string)

	list := g.friendsOf(reza, "friends")
	first, _ := list[0].(map[string]any)
	if first["room_id"] != roomID {
		t.Errorf("room_id = %v, want %s", first["room_id"], roomID)
	}
	if first["online"] != true {
		t.Errorf("a friend who just acted is shown offline: %v", first)
	}
	// Being in a room is not being in a match. A room can be locked while its
	// host is still on the hero screen, so "in game" comes from the player's
	// own service - which launched Dota and watches its log - and not from
	// room state.
	if first["in_game"] != false {
		t.Errorf("a friend sitting in a lobby is shown as in game: %v", first)
	}
	if rec, _ := g.do(http.MethodPost, "/v1/rooms/"+roomID+"/status", ali, map[string]any{
		"status": "locked_in_game",
	}); rec.Code != http.StatusOK {
		t.Fatalf("locking: %d", rec.Code)
	}
	list = g.friendsOf(reza, "friends")
	first, _ = list[0].(map[string]any)
	if first["in_game"] != false {
		t.Errorf("locking a room was mistaken for starting a match: %v", first)
	}

	// The service says so, and now it is true.
	if rec, _ := g.do(http.MethodPost, "/v1/sync", ali, map[string]any{
		"nick": "ali", "in_game": true,
	}); rec.Code != http.StatusOK {
		t.Fatalf("sync: %d", rec.Code)
	}
	list = g.friendsOf(reza, "friends")
	first, _ = list[0].(map[string]any)
	if first["in_game"] != true {
		t.Errorf("the service reported a running game and the rail did not show it: %v", first)
	}
}

// Somebody you blocked is not somebody whose whereabouts you get to watch.
func TestABlockedPersonsWhereaboutsAreNotShown(t *testing.T) {
	g := newAuthRig(t)
	_, reza := g.register("reza", "a long enough password")
	pestID, pest := g.register("pest", "a long enough password")

	_, out := g.do(http.MethodPost, "/v1/rooms", pest, map[string]any{"nick": "pest", "name": "game"})
	if _, ok := out["room_id"].(string); !ok {
		t.Fatalf("no room: %v", out)
	}
	g.act(reza, pestID, "block")

	list := g.friendsOf(reza, "blocked")
	if len(list) != 1 {
		t.Fatalf("block list = %v", list)
	}
	first, _ := list[0].(map[string]any)
	if first["room_id"] != nil || first["online"] != false {
		t.Errorf("a blocked person's whereabouts leaked: %v", first)
	}
}

func TestTheFriendsListNeedsAnAccount(t *testing.T) {
	g := newAuthRig(t)
	if rec, _ := g.do(http.MethodGet, "/v1/friends", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", rec.Code)
	}
}
