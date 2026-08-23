package api_test

import (
	"net/http"
	"testing"
)

// syncBody is the one call the app polls. Everything the two screens draw
// comes back from here, so these tests are the contract the frontend is
// written against.
func sync(t *testing.T, h *harness, body map[string]any) map[string]any {
	t.Helper()
	code, out := h.post(t, "/v1/sync", body)
	if code != http.StatusOK {
		t.Fatalf("sync returned %d: %v", code, out)
	}
	return out
}

func TestSyncReturnsTheWholeScreenInOneCall(t *testing.T) {
	h := newHarness(t)

	code, host := h.post(t, "/v1/rooms", map[string]any{
		"player_id": "alice", "nick": "Alice", "name": "Test room",
	})
	if code != http.StatusCreated {
		t.Fatalf("create returned %d: %v", code, host)
	}
	roomID := host["room_id"].(string)

	out := sync(t, h, map[string]any{"player_id": "alice", "nick": "Alice", "room_id": roomID})

	if out["room"] == nil {
		t.Fatal("sync did not return the room the caller is in")
	}
	if seated, _ := out["seated"].(bool); !seated {
		t.Error("the host was not reported as seated in their own room")
	}
	rooms, _ := out["rooms"].([]any)
	if len(rooms) != 1 {
		t.Fatalf("room list has %d entries, want 1", len(rooms))
	}
	if online, _ := out["online"].(float64); online != 1 {
		t.Errorf("online = %v, want 1", online)
	}
	p, _ := out["player"].(map[string]any)
	if p["nick"] != "Alice" {
		t.Errorf("profile came back as %v", p)
	}
}

func TestRoomListShowsNamesAndMMRRatherThanRawIDs(t *testing.T) {
	// A player choosing a game looks at who is in it. A list of opaque
	// player IDs is not something anyone can choose from.
	h := newHarness(t)
	if code, _ := h.post(t, "/v1/me", map[string]any{
		"player_id": "alice", "nick": "Alice", "mmr": 4000,
	}); code != http.StatusOK {
		t.Fatal("could not set up alice")
	}
	if code, _ := h.post(t, "/v1/me", map[string]any{
		"player_id": "bob", "nick": "Bob", "mmr": 2000,
	}); code != http.StatusOK {
		t.Fatal("could not set up bob")
	}

	code, host := h.post(t, "/v1/rooms", map[string]any{"player_id": "alice", "nick": "Alice"})
	if code != http.StatusCreated {
		t.Fatalf("create returned %d", code)
	}
	roomID := host["room_id"].(string)
	if code, out := h.post(t, "/v1/rooms/"+roomID+"/join", map[string]any{
		"player_id": "bob", "nick": "Bob",
	}); code != http.StatusOK {
		t.Fatalf("join returned %d: %v", code, out)
	}

	code, view := h.get(t, "/v1/rooms/"+roomID)
	if code != http.StatusOK {
		t.Fatalf("get room returned %d", code)
	}
	if view["host_nick"] != "Alice" {
		t.Errorf("host_nick = %v", view["host_nick"])
	}
	if avg, _ := view["avg_mmr"].(float64); avg != 3000 {
		t.Errorf("avg_mmr = %v, want 3000", avg)
	}
	members, _ := view["members"].([]any)
	if len(members) != 2 {
		t.Fatalf("got %d members: %v", len(members), members)
	}
	first, _ := members[0].(map[string]any)
	if first["nick"] != "Alice" || first["is_host"] != true {
		t.Errorf("slot 0 should be the host: %v", first)
	}
	second, _ := members[1].(map[string]any)
	if second["nick"] != "Bob" {
		t.Errorf("slot 1 = %v", second)
	}
	if mmr, _ := second["mmr"].(float64); mmr != 2000 {
		t.Errorf("bob's MMR came back as %v", mmr)
	}
}

func TestUnnamedPlayerStillRenders(t *testing.T) {
	// A room whose members never called /v1/me must still be drawable.
	// Falling back to the raw ID is ugly; a blank row is a bug report.
	h := newHarness(t)
	code, host := h.post(t, "/v1/rooms", map[string]any{"player_id": "ghost"})
	if code != http.StatusCreated {
		t.Fatalf("create returned %d", code)
	}
	_, view := h.get(t, "/v1/rooms/"+host["room_id"].(string))
	members, _ := view["members"].([]any)
	if len(members) != 1 {
		t.Fatalf("got %v", members)
	}
	if m, _ := members[0].(map[string]any); m["nick"] != "ghost" {
		t.Errorf("expected the ID as a fallback name, got %v", m["nick"])
	}
}

func TestJoinableReflectsBothSpaceAndStatus(t *testing.T) {
	h := newHarness(t)
	code, host := h.post(t, "/v1/rooms", map[string]any{"player_id": "alice", "nick": "Alice"})
	if code != http.StatusCreated {
		t.Fatalf("create returned %d", code)
	}
	roomID := host["room_id"].(string)

	_, view := h.get(t, "/v1/rooms/"+roomID)
	if view["joinable"] != true {
		t.Error("a fresh room should be joinable")
	}

	if code, _ := h.post(t, "/v1/rooms/"+roomID+"/status", map[string]any{
		"player_id": "alice", "status": "locked_in_game",
	}); code != http.StatusOK {
		t.Fatalf("lock returned %d", code)
	}
	_, view = h.get(t, "/v1/rooms/"+roomID)
	if view["joinable"] != false {
		t.Error("a locked room must not advertise itself as joinable")
	}
}

func TestChatIsScopedToItsChannel(t *testing.T) {
	h := newHarness(t)
	code, host := h.post(t, "/v1/rooms", map[string]any{"player_id": "alice", "nick": "Alice"})
	if code != http.StatusCreated {
		t.Fatalf("create returned %d", code)
	}
	roomID := host["room_id"].(string)

	if code, out := h.post(t, "/v1/chat", map[string]any{
		"player_id": "alice", "nick": "Alice", "channel": "lobby", "text": "anyone about?",
	}); code != http.StatusOK {
		t.Fatalf("lobby chat returned %d: %v", code, out)
	}
	if code, out := h.post(t, "/v1/chat", map[string]any{
		"player_id": "alice", "nick": "Alice", "channel": roomID, "text": "loading now",
	}); code != http.StatusOK {
		t.Fatalf("room chat returned %d: %v", code, out)
	}

	out := sync(t, h, map[string]any{"player_id": "alice", "nick": "Alice", "room_id": roomID})
	lobby, _ := out["lobby_chat"].([]any)
	roomChat, _ := out["room_chat"].([]any)

	if !containsText(lobby, "anyone about?") {
		t.Errorf("lobby chat missing the lobby message: %v", lobby)
	}
	if containsText(lobby, "loading now") {
		t.Error("a room message leaked into the lobby channel")
	}
	if !containsText(roomChat, "loading now") {
		t.Errorf("room chat missing its message: %v", roomChat)
	}
}

func TestOutsiderCannotTalkInARoom(t *testing.T) {
	h := newHarness(t)
	code, host := h.post(t, "/v1/rooms", map[string]any{"player_id": "alice", "nick": "Alice"})
	if code != http.StatusCreated {
		t.Fatalf("create returned %d", code)
	}
	roomID := host["room_id"].(string)

	code, out := h.post(t, "/v1/chat", map[string]any{
		"player_id": "stranger", "nick": "Stranger", "channel": roomID, "text": "you are all bad",
	})
	if code != http.StatusForbidden {
		t.Fatalf("an outsider was allowed to post: %d %v", code, out)
	}
}

func TestChatCursorOnlyDeliversWhatIsNew(t *testing.T) {
	h := newHarness(t)
	if code, _ := h.post(t, "/v1/chat", map[string]any{
		"player_id": "alice", "nick": "Alice", "text": "first",
	}); code != http.StatusOK {
		t.Fatal("post failed")
	}

	out := sync(t, h, map[string]any{"player_id": "alice", "nick": "Alice"})
	cursor := out["lobby_cursor"]
	if !containsText(out["lobby_chat"].([]any), "first") {
		t.Fatal("first message not delivered")
	}

	// Nothing new: the cursor must hold its place rather than reset, or the
	// client replays the whole backlog on every poll.
	out = sync(t, h, map[string]any{"player_id": "alice", "nick": "Alice", "lobby_cursor": cursor})
	if msgs, _ := out["lobby_chat"].([]any); len(msgs) != 0 {
		t.Fatalf("re-delivered old messages: %v", msgs)
	}
	if out["lobby_cursor"] != cursor {
		t.Fatalf("cursor moved on an empty poll: %v then %v", cursor, out["lobby_cursor"])
	}

	if code, _ := h.post(t, "/v1/chat", map[string]any{
		"player_id": "bob", "nick": "Bob", "text": "second",
	}); code != http.StatusOK {
		t.Fatal("second post failed")
	}
	out = sync(t, h, map[string]any{"player_id": "alice", "nick": "Alice", "lobby_cursor": cursor})
	msgs, _ := out["lobby_chat"].([]any)
	if len(msgs) != 1 || !containsText(msgs, "second") {
		t.Fatalf("expected only the new message, got %v", msgs)
	}
}

func TestRoomEventsAppearInRoomChat(t *testing.T) {
	// The room's own chat is where a player finds out what happened. If
	// joins and kicks are silent, a player sees a slot change and has to
	// guess why.
	h := newHarness(t)
	code, host := h.post(t, "/v1/rooms", map[string]any{"player_id": "alice", "nick": "Alice"})
	if code != http.StatusCreated {
		t.Fatalf("create returned %d", code)
	}
	roomID := host["room_id"].(string)

	if code, _ := h.post(t, "/v1/rooms/"+roomID+"/join", map[string]any{
		"player_id": "bob", "nick": "Bob",
	}); code != http.StatusOK {
		t.Fatal("join failed")
	}
	if code, _ := h.post(t, "/v1/rooms/"+roomID+"/kick", map[string]any{
		"player_id": "alice", "target_id": "bob",
	}); code != http.StatusOK {
		t.Fatal("kick failed")
	}

	out := sync(t, h, map[string]any{"player_id": "alice", "nick": "Alice", "room_id": roomID})
	msgs, _ := out["room_chat"].([]any)
	if !containsText(msgs, "Bob joined") {
		t.Errorf("no join announcement: %v", msgs)
	}
	if !containsText(msgs, "Bob was removed by the host") {
		t.Errorf("no kick announcement: %v", msgs)
	}
}

func TestSyncTellsAClientWhenItsRoomIsGone(t *testing.T) {
	// Without this the app sits on a room screen forever after the room
	// closes, showing a match that no longer exists.
	h := newHarness(t)
	out := sync(t, h, map[string]any{"player_id": "alice", "nick": "Alice", "room_id": "r-vanished"})
	if out["room_gone"] != true {
		t.Fatalf("expected room_gone, got %v", out)
	}
}

func TestMMRWeeklyLimitSurfacesAsAConflict(t *testing.T) {
	h := newHarness(t)
	if code, _ := h.post(t, "/v1/me", map[string]any{
		"player_id": "alice", "nick": "Alice", "mmr": 3000,
	}); code != http.StatusOK {
		t.Fatal("first MMR rejected")
	}
	code, out := h.post(t, "/v1/me", map[string]any{
		"player_id": "alice", "nick": "Alice", "mmr": 9000,
	})
	if code != http.StatusConflict {
		t.Fatalf("second change in the same week returned %d: %v", code, out)
	}
}

func TestRenamingDoesNotTripTheMMRLimit(t *testing.T) {
	// The app sends the whole profile when a player edits their name. If an
	// unchanged MMR counted as a change, renaming would fail six days out
	// of seven and look like a broken app.
	h := newHarness(t)
	if code, _ := h.post(t, "/v1/me", map[string]any{
		"player_id": "alice", "nick": "Alice", "mmr": 3000,
	}); code != http.StatusOK {
		t.Fatal("setup failed")
	}
	code, out := h.post(t, "/v1/me", map[string]any{
		"player_id": "alice", "nick": "Alice Two", "mmr": 3000,
	})
	if code != http.StatusOK {
		t.Fatalf("rename returned %d: %v", code, out)
	}
	if out["nick"] != "Alice Two" {
		t.Errorf("nick = %v", out["nick"])
	}
}

// /spectate seats an observer - an ordinary player choosing to watch (D38).
// The admin seat is a different thing and is reached another way, so this
// endpoint follows the observer rules: outside the playing slots, and refused
// once a match has started.
func TestObserverGetsASeatOutsideThePlayingSlots(t *testing.T) {
	h := newHarness(t)
	code, host := h.post(t, "/v1/rooms", map[string]any{"player_id": "alice", "nick": "Alice"})
	if code != http.StatusCreated {
		t.Fatalf("create returned %d", code)
	}
	roomID := host["room_id"].(string)

	code, spec := h.post(t, "/v1/rooms/"+roomID+"/spectate", map[string]any{
		"player_id": "watcher", "nick": "Watcher",
	})
	if code != http.StatusOK {
		t.Fatalf("spectate returned %d: %v", code, spec)
	}
	if spec["is_spectator"] != true {
		t.Error("connect info did not mark the seat as a watching seat")
	}
	if spec["virtual_ip"] == host["virtual_ip"] {
		t.Error("observer was given the host's address")
	}

	_, view := h.get(t, "/v1/rooms/"+roomID)
	if free, _ := view["free_slots"].(float64); free != 9 {
		t.Errorf("free_slots = %v; an observer must not consume a playing slot", free)
	}
	if w, _ := view["watchers"].(float64); w != 1 {
		t.Errorf("watchers = %v, want 1", w)
	}
}

// Wandering into a match already in progress is where scouting and griefing
// start, so the gallery closes when the room locks.
func TestObserverIsRefusedOnceTheMatchStarts(t *testing.T) {
	h := newHarness(t)
	_, host := h.post(t, "/v1/rooms", map[string]any{"player_id": "alice", "nick": "Alice"})
	roomID := host["room_id"].(string)

	if code, _ := h.post(t, "/v1/rooms/"+roomID+"/status", map[string]any{
		"player_id": "alice", "status": "locked_in_game",
	}); code != http.StatusOK {
		t.Fatal("lock failed")
	}
	code, out := h.post(t, "/v1/rooms/"+roomID+"/spectate", map[string]any{
		"player_id": "nosy", "nick": "Nosy",
	})
	if code != http.StatusForbidden {
		t.Fatalf("spectate on a locked room returned %d: %v; want 403", code, out)
	}
}

func TestDiagnosticsRoundTrip(t *testing.T) {
	h := newHarness(t)
	code, _ := h.post(t, "/v1/diag", map[string]any{
		"player_id": "alice",
		"machine":   "PC-A",
		"version":   "test",
		"checks": []map[string]any{
			{"name": "server reachable", "ok": true, "ms": 42},
			{"name": "tunnel up", "ok": false, "detail": "timed out"},
		},
	})
	if code != http.StatusOK {
		t.Fatalf("posting a report returned %d", code)
	}
	code, out := h.get(t, "/v1/diag")
	if code != http.StatusOK {
		t.Fatalf("reading reports returned %d", code)
	}
	reports, _ := out["reports"].([]any)
	if len(reports) != 1 {
		t.Fatalf("got %d reports", len(reports))
	}
	rep, _ := reports[0].(map[string]any)
	if rep["machine"] != "PC-A" {
		t.Errorf("report = %v", rep)
	}
	checks, _ := rep["checks"].([]any)
	if len(checks) != 2 {
		t.Fatalf("checks = %v", checks)
	}
}

func containsText(msgs []any, want string) bool {
	for _, m := range msgs {
		mm, ok := m.(map[string]any)
		if !ok {
			continue
		}
		if s, _ := mm["text"].(string); s == want {
			return true
		}
	}
	return false
}
