package api_test

import (
	"net/http"
	"testing"
)

// Each player's own distance from the relay travels back with their seat
// (D59). Only their machine can measure it - a client in the lobby has no
// path to anybody else - so the heartbeat is the only place it can come from,
// and the room view is the only place anybody reads it.
func TestAPlayersOwnPingTravelsBackWithTheirSeat(t *testing.T) {
	h := newHarness(t)

	code, made := h.post(t, "/v1/rooms", map[string]any{
		"player_id": "alice", "nick": "Alice", "name": "Test room",
	})
	if code != http.StatusCreated {
		t.Fatalf("create returned %d: %v", code, made)
	}
	roomID := made["room_id"].(string)

	if code, out := h.post(t, "/v1/rooms/"+roomID+"/join", map[string]any{
		"player_id": "bob", "nick": "Bob",
	}); code != http.StatusOK {
		t.Fatalf("join returned %d: %v", code, out)
	}

	// Both report, and they report different numbers. A view that showed one
	// number for the whole room would pass a test where they matched.
	sync(t, h, map[string]any{"player_id": "alice", "nick": "Alice", "room_id": roomID, "relay_ms": 18})
	out := sync(t, h, map[string]any{"player_id": "bob", "nick": "Bob", "room_id": roomID, "relay_ms": 74})

	got := map[string]float64{}
	room, _ := out["room"].(map[string]any)
	members, _ := room["members"].([]any)
	for _, one := range members {
		m, _ := one.(map[string]any)
		ms, _ := m["relay_ms"].(float64)
		got[m["player_id"].(string)] = ms
	}
	if got["alice"] != 18 {
		t.Errorf("the host's ping came back as %v, want 18", got["alice"])
	}
	if got["bob"] != 74 {
		t.Errorf("the other player's ping came back as %v, want 74", got["bob"])
	}
}

// Nobody's seat may claim a measurement they never made. Zero milliseconds is
// not an excellent connection, it is no reading at all, and a view that sent
// one would be read as the former.
func TestASeatWithNoMeasurementCarriesNoPing(t *testing.T) {
	h := newHarness(t)

	code, made := h.post(t, "/v1/rooms", map[string]any{
		"player_id": "alice", "nick": "Alice", "name": "Test room",
	})
	if code != http.StatusCreated {
		t.Fatalf("create returned %d: %v", code, made)
	}
	roomID := made["room_id"].(string)

	out := sync(t, h, map[string]any{"player_id": "alice", "nick": "Alice", "room_id": roomID})
	room, _ := out["room"].(map[string]any)
	members, _ := room["members"].([]any)
	if len(members) != 1 {
		t.Fatalf("room has %d members, want 1", len(members))
	}
	m, _ := members[0].(map[string]any)
	if _, present := m["relay_ms"]; present {
		t.Errorf("a seat that never reported a ping carried one: %v", m)
	}
}
