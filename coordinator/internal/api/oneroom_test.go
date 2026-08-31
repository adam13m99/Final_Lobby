package api_test

// One person, one room, over the wire (D82).
//
// The owner opened several rooms from the live build. The rooms package's own
// tests cover the rule; these cover the doors and the answer, because the
// answer is the half that matters here: an app told only "no" cannot get its
// user out of a state its user cannot see.

import (
	"net/http"
	"testing"
)

func TestTheCoordinatorRefusesASecondRoom(t *testing.T) {
	h := newHarness(t)

	code, first := h.post(t, "/v1/rooms", map[string]string{"player_id": "alice"})
	if code != http.StatusCreated {
		t.Fatalf("create returned %d: %v", code, first)
	}
	roomID := first["room_id"].(string)

	code, second := h.post(t, "/v1/rooms", map[string]string{"player_id": "alice"})
	if code != http.StatusConflict {
		t.Fatalf("a second room returned %d: %v", code, second)
	}
	// And it says which room, so the app can take them back to it instead of
	// leaving them holding an error about a room they cannot name.
	if second["room_id"] != roomID {
		t.Errorf("the refusal names room %v, want %s", second["room_id"], roomID)
	}

	_, lobby := h.get(t, "/v1/rooms")
	rooms, _ := lobby["rooms"].([]any)
	if len(rooms) != 1 {
		t.Errorf("the lobby holds %d rooms after a refused create, want 1", len(rooms))
	}
}

func TestTheCoordinatorRefusesASecondJoin(t *testing.T) {
	h := newHarness(t)
	_, a := h.post(t, "/v1/rooms", map[string]string{"player_id": "alice"})
	_, b := h.post(t, "/v1/rooms", map[string]string{"player_id": "bob"})

	code, _ := h.post(t, "/v1/rooms/"+a["room_id"].(string)+"/join",
		map[string]string{"player_id": "carol"})
	if code != http.StatusOK {
		t.Fatalf("carol could not join the first room: %d", code)
	}
	code, out := h.post(t, "/v1/rooms/"+b["room_id"].(string)+"/join",
		map[string]string{"player_id": "carol"})
	if code != http.StatusConflict {
		t.Fatalf("carol joined a second room: %d %v", code, out)
	}
	if out["room_id"] != a["room_id"] {
		t.Errorf("the refusal names %v, want the room she is in, %v",
			out["room_id"], a["room_id"])
	}
}

// The escape from the dead end. An app whose own record of where it is has
// been lost - a cleared session, a reinstall, a crash between joining and
// saving - asks sync and is told, so it can put itself right without the
// person having to know anything happened.
func TestSyncSaysWhichRoomTheServerHasYouIn(t *testing.T) {
	h := newHarness(t)

	code, none := h.post(t, "/v1/sync", map[string]any{"player_id": "alice"})
	if code != http.StatusOK {
		t.Fatalf("sync returned %d: %v", code, none)
	}
	if none["in_room_id"] != "" {
		t.Errorf("somebody in no room is reported as being in %v", none["in_room_id"])
	}

	_, made := h.post(t, "/v1/rooms", map[string]string{"player_id": "alice"})
	roomID := made["room_id"].(string)

	// Deliberately *not* telling it which room, the way an app that has lost
	// its own record would ask.
	_, lost := h.post(t, "/v1/sync", map[string]any{"player_id": "alice"})
	if lost["in_room_id"] != roomID {
		t.Errorf("sync says the player is in %v, want %s", lost["in_room_id"], roomID)
	}

	// And once they leave, it says so, so nothing puts them back.
	h.post(t, "/v1/rooms/"+roomID+"/leave", map[string]string{"player_id": "alice"})
	_, gone := h.post(t, "/v1/sync", map[string]any{"player_id": "alice"})
	if gone["in_room_id"] != "" {
		t.Errorf("after leaving, sync still says %v", gone["in_room_id"])
	}
}

// Writing into a room's chat is guarded - "anyone who learns a room ID can
// heckle a match they are not part of" - and reading it was not (D82).
//
// The asymmetry is the bug. Every room's id is in the lobby list that every
// client is handed on every poll, so "learns a room ID" is not a hurdle: it
// is one field away for anybody signed in. Sync would then hand over the room
// conversation of a match they have nothing to do with, including whatever
// the host has just typed to let their friends in.
func TestYouCannotReadTheChatOfARoomYouAreNotIn(t *testing.T) {
	h := newHarness(t)

	_, made := h.post(t, "/v1/rooms", map[string]string{"player_id": "alice", "nick": "Alice"})
	roomID := made["room_id"].(string)
	code, said := h.post(t, "/v1/chat", map[string]string{
		"player_id": "alice", "channel": roomID, "text": "password is hunter2",
	})
	if code != http.StatusOK {
		t.Fatalf("the host could not talk in their own room: %d %v", code, said)
	}

	// The host reads it, because they are in it.
	_, mine := h.post(t, "/v1/sync", map[string]any{"player_id": "alice", "room_id": roomID})
	if msgs, _ := mine["room_chat"].([]any); len(msgs) == 0 {
		t.Fatal("the host cannot read their own room's chat")
	}

	// A stranger claims the room id, which they can read off the lobby list.
	_, theirs := h.post(t, "/v1/sync", map[string]any{"player_id": "mallory", "room_id": roomID})
	if theirs["seated"] == true {
		t.Fatal("a stranger is reported as seated")
	}
	if msgs, _ := theirs["room_chat"].([]any); len(msgs) != 0 {
		t.Errorf("a stranger read %d messages out of a room they are not in", len(msgs))
	}
	// Writing was already refused; check it stayed that way.
	code, _ = h.post(t, "/v1/chat", map[string]string{
		"player_id": "mallory", "channel": roomID, "text": "hello",
	})
	if code != http.StatusForbidden {
		t.Errorf("a stranger wrote into a room they are not in: %d", code)
	}
}
