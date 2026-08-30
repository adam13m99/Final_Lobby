package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lobbybaz/coordinator/internal/api"
	"lobbybaz/coordinator/internal/room"
	"lobbybaz/coordinator/internal/ticket"
)

type harness struct {
	srv     *httptest.Server
	rooms   *room.Store
	tickets *ticket.Store
	now     *time.Time
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	h := &harness{
		rooms:   room.NewStore(),
		tickets: ticket.NewStore(),
		now:     &now,
	}
	s := api.New(api.Config{
		Rooms:     h.rooms,
		Tickets:   h.tickets,
		RelayAddr: "87.107.110.199:443",
		RelayPub:  "1e07798757a7225f04f6bb2a72ed2ab5116c0f2d7d3ffefd6db96fa4e85bf72e",
		Now:       func() time.Time { return *h.now },
	})
	h.srv = httptest.NewServer(s.Routes())
	t.Cleanup(h.srv.Close)
	return h
}

func (h *harness) post(t *testing.T, path string, body any) (int, map[string]any) {
	t.Helper()
	buf, _ := json.Marshal(body)
	resp, err := http.Post(h.srv.URL+path, "application/json", bytes.NewReader(buf))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func (h *harness) get(t *testing.T, path string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(h.srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func TestCreateAndJoinGivesBothPlayersTheSameRoomNetwork(t *testing.T) {
	h := newHarness(t)

	code, host := h.post(t, "/v1/rooms", map[string]string{"player_id": "alice", "name": "Test"})
	if code != http.StatusCreated {
		t.Fatalf("create returned %d: %v", code, host)
	}
	roomID := host["room_id"].(string)

	code, join := h.post(t, "/v1/rooms/"+roomID+"/join", map[string]string{"player_id": "bob"})
	if code != http.StatusOK {
		t.Fatalf("join returned %d: %v", code, join)
	}

	if host["virtual_ip"] == join["virtual_ip"] {
		t.Fatal("both players were given the same virtual IP")
	}
	if host["host_ip"] != join["host_ip"] {
		t.Fatalf("players disagree about the host address: %v vs %v", host["host_ip"], join["host_ip"])
	}
	if host["virtual_ip"] != host["host_ip"] {
		t.Fatalf("the host is not at the host address: %v vs %v", host["virtual_ip"], host["host_ip"])
	}
	if join["subnet"] != host["subnet"] {
		t.Fatalf("players are on different subnets: %v vs %v", join["subnet"], host["subnet"])
	}
	if join["dota_connect"] != host["host_ip"].(string)+":27015" {
		t.Fatalf("connect string = %v", join["dota_connect"])
	}
	if host["ticket"] == join["ticket"] {
		t.Fatal("both players were issued the same ticket")
	}
	if host["relay_pub"] == "" || host["relay_addr"] == "" {
		t.Fatal("client was not told where the relay is")
	}
}

func TestRelayValidatesAnIssuedTicket(t *testing.T) {
	h := newHarness(t)
	_, host := h.post(t, "/v1/rooms", map[string]string{"player_id": "alice"})

	code, claims := h.post(t, "/internal/validate-ticket",
		map[string]string{"ticket": host["ticket"].(string)})
	if code != http.StatusOK {
		t.Fatalf("relay could not validate a freshly issued ticket: %d %v", code, claims)
	}
	if claims["virtual_ip"] != host["virtual_ip"] {
		t.Fatalf("relay told %v, client told %v", claims["virtual_ip"], host["virtual_ip"])
	}
	if claims["room_id"] != host["room_id"] {
		t.Fatal("room mismatch between ticket and client")
	}
}

func TestRelayRejectsAGarbageTicket(t *testing.T) {
	h := newHarness(t)
	code, _ := h.post(t, "/internal/validate-ticket", map[string]string{"ticket": "made-up"})
	if code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", code)
	}
}

func TestLockedRoomRefusesJoinOverHTTP(t *testing.T) {
	h := newHarness(t)
	_, host := h.post(t, "/v1/rooms", map[string]string{"player_id": "alice"})
	roomID := host["room_id"].(string)

	code, body := h.post(t, "/v1/rooms/"+roomID+"/status",
		map[string]string{"player_id": "alice", "status": "locked_in_game"})
	if code != http.StatusOK {
		t.Fatalf("lock returned %d: %v", code, body)
	}
	code, _ = h.post(t, "/v1/rooms/"+roomID+"/join", map[string]string{"player_id": "bob"})
	if code != http.StatusForbidden {
		t.Fatalf("join into a locked room returned %d, want 403", code)
	}

	// The host reopens it for a replacement, exactly as the product rules say.
	h.post(t, "/v1/rooms/"+roomID+"/status",
		map[string]string{"player_id": "alice", "status": "open_to_new_players"})
	code, _ = h.post(t, "/v1/rooms/"+roomID+"/join", map[string]string{"player_id": "bob"})
	if code != http.StatusOK {
		t.Fatalf("join after reopening returned %d, want 200", code)
	}
}

func TestNonHostCannotLockTheRoom(t *testing.T) {
	h := newHarness(t)
	_, host := h.post(t, "/v1/rooms", map[string]string{"player_id": "alice"})
	roomID := host["room_id"].(string)
	h.post(t, "/v1/rooms/"+roomID+"/join", map[string]string{"player_id": "bob"})

	code, _ := h.post(t, "/v1/rooms/"+roomID+"/status",
		map[string]string{"player_id": "bob", "status": "locked_in_game"})
	if code != http.StatusForbidden {
		t.Fatalf("a non-host locked the room: %d", code)
	}
}

func TestKickRevokesNetworkAccessImmediately(t *testing.T) {
	h := newHarness(t)
	_, host := h.post(t, "/v1/rooms", map[string]string{"player_id": "alice"})
	roomID := host["room_id"].(string)
	_, bob := h.post(t, "/v1/rooms/"+roomID+"/join", map[string]string{"player_id": "bob"})
	bobTicket := bob["ticket"].(string)

	// Before the kick the relay accepts bob.
	if code, _ := h.post(t, "/internal/validate-ticket",
		map[string]string{"ticket": bobTicket}); code != http.StatusOK {
		t.Fatalf("bob's ticket invalid before kick: %d", code)
	}

	h.post(t, "/v1/rooms/"+roomID+"/kick",
		map[string]string{"player_id": "alice", "target_id": "bob"})

	// Removing him from the room list is not enough; his tunnel must die.
	if code, _ := h.post(t, "/internal/validate-ticket",
		map[string]string{"ticket": bobTicket}); code != http.StatusForbidden {
		t.Fatal("a kicked player's ticket still works - he keeps network access")
	}

	// And he cannot come back for five minutes.
	if code, _ := h.post(t, "/v1/rooms/"+roomID+"/join",
		map[string]string{"player_id": "bob"}); code != http.StatusForbidden {
		t.Fatal("kicked player rejoined immediately")
	}
	*h.now = h.now.Add(6 * time.Minute)
	if code, _ := h.post(t, "/v1/rooms/"+roomID+"/join",
		map[string]string{"player_id": "bob"}); code != http.StatusOK {
		t.Fatal("kicked player still barred after the 5 minute block expired")
	}
}

func TestVoluntaryLeaverMayReturnAtOnce(t *testing.T) {
	h := newHarness(t)
	_, host := h.post(t, "/v1/rooms", map[string]string{"player_id": "alice"})
	roomID := host["room_id"].(string)
	h.post(t, "/v1/rooms/"+roomID+"/join", map[string]string{"player_id": "bob"})

	h.post(t, "/v1/rooms/"+roomID+"/leave", map[string]string{"player_id": "bob"})
	if code, _ := h.post(t, "/v1/rooms/"+roomID+"/join",
		map[string]string{"player_id": "bob"}); code != http.StatusOK {
		t.Fatal("a player who left voluntarily could not rejoin")
	}
}

func TestLeavingRevokesTheTunnel(t *testing.T) {
	h := newHarness(t)
	_, host := h.post(t, "/v1/rooms", map[string]string{"player_id": "alice"})
	roomID := host["room_id"].(string)
	_, bob := h.post(t, "/v1/rooms/"+roomID+"/join", map[string]string{"player_id": "bob"})

	h.post(t, "/v1/rooms/"+roomID+"/leave", map[string]string{"player_id": "bob"})
	if code, _ := h.post(t, "/internal/validate-ticket",
		map[string]string{"ticket": bob["ticket"].(string)}); code != http.StatusForbidden {
		t.Fatal("a player who left keeps working network access")
	}
}

func TestLeaseRenewKeepsALongMatchAlive(t *testing.T) {
	h := newHarness(t)
	_, host := h.post(t, "/v1/rooms", map[string]string{"player_id": "alice"})
	tok := host["ticket"].(string)

	// Halfway through the lifetime, as the watchdog does.
	*h.now = h.now.Add(ticket.Lifetime / 2)
	code, body := h.post(t, "/v1/lease/renew", map[string]string{"ticket": tok})
	if code != http.StatusOK || body["valid"] != true {
		t.Fatalf("renew returned %d %v", code, body)
	}

	// Past the original expiry the ticket must still work.
	*h.now = h.now.Add(ticket.Lifetime - time.Minute)
	if code, _ := h.post(t, "/internal/validate-ticket",
		map[string]string{"ticket": tok}); code != http.StatusOK {
		t.Fatal("a renewed ticket expired anyway - this would drop a player mid-match")
	}
}

func TestRoomListing(t *testing.T) {
	h := newHarness(t)
	h.post(t, "/v1/rooms", map[string]string{"player_id": "alice", "name": "Alpha"})

	code, body := h.get(t, "/v1/rooms")
	if code != http.StatusOK {
		t.Fatalf("list returned %d", code)
	}
	rooms := body["rooms"].([]any)
	if len(rooms) != 1 {
		t.Fatalf("listed %d rooms, want 1", len(rooms))
	}
	first := rooms[0].(map[string]any)
	if first["name"] != "Alpha" {
		t.Errorf("name = %v", first["name"])
	}
	if first["free_slots"].(float64) != 9 {
		t.Errorf("free_slots = %v, want 9", first["free_slots"])
	}
}

func TestRateLimiterBlocksAJoinFlood(t *testing.T) {
	h := newHarness(t)
	// Burst is 5 for expensive endpoints; the sixth immediate call is
	// refused. Without this a script can spin up rooms without limit.
	var refused bool
	for i := 0; i < 12; i++ {
		code, _ := h.post(t, "/v1/rooms", map[string]string{"player_id": "flood"})
		if code == http.StatusTooManyRequests {
			refused = true
			break
		}
	}
	if !refused {
		t.Fatal("twelve rapid room creations went through unthrottled")
	}
}

func TestMalformedBodyRejected(t *testing.T) {
	h := newHarness(t)
	resp, err := http.Post(h.srv.URL+"/v1/rooms", "application/json",
		bytes.NewReader([]byte(`{"player_id": "a", "unexpected": 1}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown field accepted: %d", resp.StatusCode)
	}
}

func TestBearerTokenGatesThePlayerAPI(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	s := api.New(api.Config{
		Rooms:     room.NewStore(),
		Tickets:   ticket.NewStore(),
		RelayAddr: "127.0.0.1:443",
		RelayPub:  "00",
		AuthToken: "correct-horse",
		Now:       func() time.Time { return now },
	})
	srv := httptest.NewServer(s.Routes())
	defer srv.Close()

	do := func(token string) int {
		req, _ := http.NewRequest("POST", srv.URL+"/v1/rooms",
			bytes.NewReader([]byte(`{"player_id":"a"}`)))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	if code := do(""); code != http.StatusUnauthorized {
		t.Errorf("no token returned %d, want 401", code)
	}
	if code := do("wrong"); code != http.StatusUnauthorized {
		t.Errorf("wrong token returned %d, want 401", code)
	}
	if code := do("correct-horse"); code != http.StatusCreated {
		t.Errorf("correct token returned %d, want 201", code)
	}
}

func TestHostCanManageTheirRoomInQuickSuccession(t *testing.T) {
	h := newHarness(t)
	_, host := h.post(t, "/v1/rooms", map[string]string{"player_id": "alice"})
	roomID := host["room_id"].(string)
	h.post(t, "/v1/rooms/"+roomID+"/join", map[string]string{"player_id": "bob"})

	// Lock, reopen, kick, lock again - a normal few seconds for a host
	// starting a match. Throttling this reads to the player as the app
	// ignoring them.
	steps := []struct {
		path string
		body map[string]string
	}{
		{"/status", map[string]string{"player_id": "alice", "status": "locked_in_game"}},
		{"/status", map[string]string{"player_id": "alice", "status": "open_to_new_players"}},
		{"/kick", map[string]string{"player_id": "alice", "target_id": "bob"}},
		{"/status", map[string]string{"player_id": "alice", "status": "locked_in_game"}},
		{"/status", map[string]string{"player_id": "alice", "status": "open_to_new_players"}},
		{"/status", map[string]string{"player_id": "alice", "status": "locked_in_game"}},
	}
	for i, st := range steps {
		code, body := h.post(t, "/v1/rooms/"+roomID+st.path, st.body)
		if code == http.StatusTooManyRequests {
			t.Fatalf("step %d (%s) was rate limited; a host managing their own room must not be", i, st.path)
		}
		if code != http.StatusOK {
			t.Fatalf("step %d (%s) returned %d: %v", i, st.path, code, body)
		}
	}
}

// --- the host, watched rather than asked (D69, D70) ----------------------

// The owner's report: they left a room, and it was still sitting in the lobby
// afterwards, still open, and they could join it again as its host.
func TestAHostLeavingTakesTheRoomWithThem(t *testing.T) {
	h := newHarness(t)
	_, host := h.post(t, "/v1/rooms", map[string]string{"player_id": "alice"})
	roomID := host["room_id"].(string)
	_, bob := h.post(t, "/v1/rooms/"+roomID+"/join", map[string]string{"player_id": "bob"})

	h.post(t, "/v1/rooms/"+roomID+"/leave", map[string]string{"player_id": "alice"})

	_, list := h.get(t, "/v1/rooms")
	if rooms, _ := list["rooms"].([]any); len(rooms) != 0 {
		t.Fatalf("the room outlived its host in the lobby: %v", rooms)
	}
	if code, _ := h.post(t, "/v1/rooms/"+roomID+"/join",
		map[string]string{"player_id": "alice"}); code == http.StatusOK {
		t.Fatal("the host walked back into the room they had just left")
	}
	// And everybody who was in it loses the room's network with it, not just
	// the person who left.
	if code, _ := h.post(t, "/internal/validate-ticket",
		map[string]string{"ticket": bob["ticket"].(string)}); code != http.StatusForbidden {
		t.Fatal("a player kept network access to a room that had closed")
	}
}

// A host who stopped answering is the other case, and it is the one D40's
// grace period was written for: the room says so and keeps counting.
func TestTheLobbySaysWhenAHostHasGoneQuiet(t *testing.T) {
	h := newHarness(t)
	_, host := h.post(t, "/v1/rooms", map[string]string{"player_id": "alice"})
	roomID := host["room_id"].(string)
	h.post(t, "/v1/rooms/"+roomID+"/join", map[string]string{"player_id": "bob"})

	h.rooms.WatchHosts(func(string) room.HostFacts { return room.HostFacts{} })
	h.rooms.Tick(h.now.Add(time.Second))

	_, list := h.get(t, "/v1/rooms")
	rooms, _ := list["rooms"].([]any)
	if len(rooms) != 1 {
		t.Fatalf("the room vanished the moment its host went quiet: %v", rooms)
	}
	got := rooms[0].(map[string]any)
	if got["status"] != "host_away" || got["host_away"] != true {
		t.Fatalf("status = %v, host_away = %v", got["status"], got["host_away"])
	}
}

// A host in a match locks their own room without having to remember to press
// anything - they are in Dota, not in this window.
func TestTheLobbySaysWhenTheHostIsInAMatch(t *testing.T) {
	h := newHarness(t)
	_, host := h.post(t, "/v1/rooms", map[string]string{"player_id": "alice"})
	roomID := host["room_id"].(string)
	h.post(t, "/v1/rooms/"+roomID+"/join", map[string]string{"player_id": "bob"})

	h.rooms.WatchHosts(func(string) room.HostFacts {
		return room.HostFacts{Online: true, InGame: true}
	})
	h.rooms.Tick(h.now.Add(time.Second))

	_, list := h.get(t, "/v1/rooms")
	got := list["rooms"].([]any)[0].(map[string]any)
	if got["status"] != "locked_in_game" || got["host_in_game"] != true {
		t.Fatalf("status = %v, host_in_game = %v", got["status"], got["host_in_game"])
	}
	if got["joinable"] != false {
		t.Fatal("a room whose host is in a match was offered as joinable")
	}
	// The seat move is refused at the coordinator, not merely hidden in the
	// interface: it is the whole of the rule.
	if code, _ := h.post(t, "/v1/rooms/"+roomID+"/slot",
		map[string]any{"player_id": "bob", "slot": 7}); code == http.StatusOK {
		t.Fatal("a player changed seat while the host was in a match")
	}
}
