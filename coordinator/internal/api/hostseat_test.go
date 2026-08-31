package api_test

// A host who has moved into the gallery is still the host (D79, D81).
//
// The room view read the host's name out of its loop over the *playing*
// slots, which was true for as long as a host had to be in one. The moment
// the owner could sit down to watch their own room, every screen in that room
// stopped saying "Host - Arman Mcc" and started saying the raw account id -
// because the name fell through to a fallback written for a host in their
// grace window, where an id really is better than nothing.

import (
	"net/http"
	"strings"
	"testing"
)

func TestAHostWhoGoesToWatchIsStillCalledByTheirName(t *testing.T) {
	h := newHarness(t)

	_, host := h.post(t, "/v1/rooms", map[string]string{
		"player_id": "a_3427029f79090d91d0de7c18", "nick": "Arman Mcc", "name": "Test",
	})
	roomID := host["room_id"].(string)
	h.post(t, "/v1/rooms/"+roomID+"/join", map[string]string{
		"player_id": "bob", "nick": "Bob",
	})

	_, before := h.get(t, "/v1/rooms/"+roomID)
	if before["host_nick"] != "Arman Mcc" {
		t.Fatalf("the host is called %v before moving", before["host_nick"])
	}

	code, out := h.post(t, "/v1/rooms/"+roomID+"/slot", map[string]any{
		"player_id": "a_3427029f79090d91d0de7c18", "slot": 0, "watching": true,
	})
	if code != http.StatusOK {
		t.Fatalf("the host could not go and watch: %d %v", code, out)
	}

	_, after := h.get(t, "/v1/rooms/"+roomID)
	nick, _ := after["host_nick"].(string)
	if nick != "Arman Mcc" {
		t.Errorf("the host went to watch and the room started calling them %q", nick)
	}
	if strings.HasPrefix(nick, "a_") {
		t.Errorf("the room is showing the host's account id, which is the bug: %q", nick)
	}

	// And their seat card in the gallery is marked as the host's, or the
	// room draws a watcher with nothing saying whose machine the match runs
	// on.
	members, _ := after["members"].([]any)
	var found bool
	for _, raw := range members {
		m, _ := raw.(map[string]any)
		if m["player_id"] != "a_3427029f79090d91d0de7c18" {
			continue
		}
		found = true
		if m["is_host"] != true {
			t.Errorf("the host's watching seat is not marked as the host's: %v", m)
		}
		if m["spectator"] != true {
			t.Errorf("the host is not reported as watching: %v", m)
		}
	}
	if !found {
		t.Error("the host is not in the room's member list at all")
	}
}

// Ten playing slots stay open when the host is watching. This is the owner's
// own reading of it: watchers are not counted in the ten, so a host who sits
// out leaves a place for a tenth player rather than a room that looks full to
// nobody.
func TestAWatchingHostLeavesAllTenPlayingSlotsFree(t *testing.T) {
	h := newHarness(t)
	_, host := h.post(t, "/v1/rooms", map[string]string{"player_id": "alice", "nick": "Alice"})
	roomID := host["room_id"].(string)

	_, seated := h.get(t, "/v1/rooms/"+roomID)
	if seated["seats"] != float64(1) || seated["free_slots"] != float64(9) {
		t.Fatalf("a room with a playing host is %v seated / %v free, want 1 / 9",
			seated["seats"], seated["free_slots"])
	}

	h.post(t, "/v1/rooms/"+roomID+"/slot",
		map[string]any{"player_id": "alice", "slot": 0, "watching": true})

	_, watching := h.get(t, "/v1/rooms/"+roomID)
	if watching["seats"] != float64(0) || watching["free_slots"] != float64(10) {
		t.Errorf("a room with a watching host is %v seated / %v free, want 0 / 10",
			watching["seats"], watching["free_slots"])
	}
	if watching["watchers"] != float64(1) {
		t.Errorf("the watching host is not counted as a watcher: %v", watching["watchers"])
	}
	if watching["joinable"] != true {
		t.Error("a room whose host is watching is not joinable")
	}
}
