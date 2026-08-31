package api_test

// The room's game mode over the wire (D80).
//
// The room store's own tests cover the rules; these cover the doors, because
// this project's recurring bug is a working subsystem the interface has no
// route to. Every one of these calls is one the desktop app makes.

import (
	"net/http"
	"testing"
)

func TestAHostChoosesTheGameModeWhenTheyOpenTheRoom(t *testing.T) {
	h := newHarness(t)

	code, host := h.post(t, "/v1/rooms", map[string]any{
		"player_id": "alice", "name": "Turbo only", "game_mode": 23,
	})
	if code != http.StatusCreated {
		t.Fatalf("create returned %d: %v", code, host)
	}
	roomID := host["room_id"].(string)

	// Everybody sees it, not only the host: a player deciding whether to join
	// reads it from the lobby, before they are in the room at all.
	code, view := h.get(t, "/v1/rooms/"+roomID)
	if code != http.StatusOK {
		t.Fatalf("reading the room returned %d: %v", code, view)
	}
	if got := view["game_mode"]; got != float64(23) {
		t.Errorf("the room reports game mode %v, want 23", got)
	}
	if got := view["game_mode_name"]; got != "Turbo" {
		t.Errorf("the room names its mode %q, want Turbo", got)
	}
}

// A room that says nothing about a mode plays what every room played before
// rooms had one. Answering with nothing at all would leave the lobby with a
// blank cell where the mode goes.
func TestARoomThatChoseNoModePlaysAllPick(t *testing.T) {
	h := newHarness(t)
	_, host := h.post(t, "/v1/rooms", map[string]string{"player_id": "alice"})
	_, view := h.get(t, "/v1/rooms/"+host["room_id"].(string))
	if got := view["game_mode"]; got != float64(1) {
		t.Errorf("a room with no mode reports %v, want 1 (All Pick)", got)
	}
	if got := view["game_mode_name"]; got != "All Pick" {
		t.Errorf("a room with no mode names %q, want All Pick", got)
	}
}

func TestTheHostChangesTheModeAfterwardsAndNobodyElseCan(t *testing.T) {
	h := newHarness(t)
	_, host := h.post(t, "/v1/rooms", map[string]string{"player_id": "alice"})
	roomID := host["room_id"].(string)
	h.post(t, "/v1/rooms/"+roomID+"/join", map[string]string{"player_id": "bob"})

	code, out := h.post(t, "/v1/rooms/"+roomID+"/mode",
		map[string]any{"player_id": "bob", "game_mode": 23})
	if code == http.StatusOK {
		t.Errorf("a guest changed the room's game mode: %v", out)
	}

	// The host's own change comes back as the whole room, because the
	// caller's next act is to redraw it.
	code, out = h.post(t, "/v1/rooms/"+roomID+"/mode",
		map[string]any{"player_id": "alice", "game_mode": 2})
	if code != http.StatusOK {
		t.Fatalf("the host could not change the mode: %d %v", code, out)
	}
	if out["game_mode"] != float64(2) || out["game_mode_name"] != "Captains Mode" {
		t.Errorf("the answer says mode %v (%v), want 2 (Captains Mode)",
			out["game_mode"], out["game_mode_name"])
	}

	// And it stuck, for everybody.
	_, view := h.get(t, "/v1/rooms/"+roomID)
	if view["game_mode"] != float64(2) {
		t.Errorf("the room reports %v after the change, want 2", view["game_mode"])
	}
}

// A mode the Windows service will not put on a command line is refused here,
// where the host is still looking at a dialog and can be told.
func TestAModeTheServiceWouldRefuseIsRefusedAtTheDoor(t *testing.T) {
	h := newHarness(t)
	_, host := h.post(t, "/v1/rooms", map[string]string{"player_id": "alice"})
	roomID := host["room_id"].(string)

	code, out := h.post(t, "/v1/rooms/"+roomID+"/mode",
		map[string]any{"player_id": "alice", "game_mode": 10})
	if code != http.StatusBadRequest {
		t.Fatalf("game mode 10 (the tutorial) returned %d: %v", code, out)
	}
	_, view := h.get(t, "/v1/rooms/"+roomID)
	if view["game_mode"] != float64(1) {
		t.Errorf("a refused mode still changed the room to %v", view["game_mode"])
	}
}

// Creating a room is not the place to argue about a game mode: the host gets
// their room, with the default, and can fix it in room settings. Losing the
// room over a dropdown would be the larger surprise.
func TestACreateWithANonsenseModeStillOpensTheRoom(t *testing.T) {
	h := newHarness(t)
	code, host := h.post(t, "/v1/rooms", map[string]any{
		"player_id": "alice", "game_mode": 4242,
	})
	if code != http.StatusCreated {
		t.Fatalf("create returned %d: %v", code, host)
	}
	_, view := h.get(t, "/v1/rooms/"+host["room_id"].(string))
	if view["game_mode"] != float64(1) {
		t.Errorf("the room plays %v, want the default 1", view["game_mode"])
	}
}
