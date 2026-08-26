package lobby

import "time"

// The types here mirror the coordinator's /v1/sync reply. That endpoint
// exists so the app makes one request per poll instead of five for the same
// screen; these are the shapes it draws from.

// Member is one seated player.
type Member struct {
	// Seat is "player", "observer" or "admin" (D38). The room screen draws
	// the observers' five seats and not the admins' three, so dropping this
	// field would put a moderator in a watcher's chair - and, because the
	// two ranges are numbered separately, in one that is already taken.
	Seat      string `json:"seat,omitempty"`
	PlayerID  string `json:"player_id"`
	Nick      string `json:"nick"`
	MMR       int    `json:"mmr"`
	Slot      int    `json:"slot"`
	IsHost    bool   `json:"is_host"`
	Spectator bool   `json:"spectator"`
	// RelayMillis is this member's own round trip to the relay. Zero means
	// they have not reported one, or the one they reported is stale - never
	// that they are instantaneously close to it.
	RelayMillis int `json:"relay_ms,omitempty"`
}

// Profile is the player as the server knows them.
type Profile struct {
	ID       string    `json:"id"`
	Nick     string    `json:"nick"`
	MMR      int       `json:"mmr"`
	MMRSetAt time.Time `json:"mmr_set_at"`
}

// ChatMessage is one line in a channel.
type ChatMessage struct {
	ID       uint64    `json:"id"`
	PlayerID string    `json:"player_id"`
	Nick     string    `json:"nick"`
	Text     string    `json:"text"`
	At       time.Time `json:"at"`
	System   bool      `json:"system"`
}

// SyncRequest is what the app sends on each poll.
type SyncRequest struct {
	PlayerID    string `json:"player_id"`
	Nick        string `json:"nick"`
	RoomID      string `json:"room_id,omitempty"`
	LobbyCursor uint64 `json:"lobby_cursor"`
	RoomCursor  uint64 `json:"room_cursor"`

	// InGame is whether Dota is actually running on this machine. The
	// friends rail shows it, and it has to come from the machine because
	// nothing the server can see distinguishes a locked room whose match has
	// started from one whose host is still arranging teams.
	InGame bool `json:"in_game,omitempty"`

	// RelayMillis is this machine's round trip to the relay. The server
	// keeps it twice: against this player, which puts a number beside their
	// seat in a room, and - when this player hosts the room they are in -
	// against the room, which is the lobby's latency column (D42, D54). The
	// lobby's copy is labelled as the host's latency rather than the
	// reader's, because that is what it is.
	RelayMillis int `json:"relay_ms,omitempty"`
}

// SyncResponse is the whole screen.
type SyncResponse struct {
	Player   Profile    `json:"player"`
	Online   int        `json:"online"`
	Rooms    []RoomView `json:"rooms"`
	Room     *RoomView  `json:"room"`
	RoomGone bool       `json:"room_gone"`
	Seated   bool       `json:"seated"`

	LobbyChat   []ChatMessage `json:"lobby_chat"`
	LobbyCursor uint64        `json:"lobby_cursor"`
	RoomChat    []ChatMessage `json:"room_chat"`
	RoomCursor  uint64        `json:"room_cursor"`

	ServerTime time.Time `json:"server_time"`
}

// Sync fetches everything the app draws in one round trip.
func (c *Client) Sync(req SyncRequest) (*SyncResponse, error) {
	var out SyncResponse
	err := c.do("POST", "/v1/sync", req, &out)
	return &out, err
}

// SaveProfile sets the player's name and, when mmr is non-nil, their
// declared rating. The server enforces the once-a-week rule.
func (c *Client) SaveProfile(playerID, nick string, mmr *int) (*Profile, error) {
	body := map[string]any{"player_id": playerID, "nick": nick}
	if mmr != nil {
		body["mmr"] = *mmr
	}
	var p Profile
	err := c.do("POST", "/v1/me", body, &p)
	return &p, err
}

// PostChat says something in a channel. An empty channel means the lobby.
func (c *Client) PostChat(playerID, nick, channel, text string) error {
	return c.do("POST", "/v1/chat", map[string]string{
		"player_id": playerID,
		"nick":      nick,
		"channel":   channel,
		"text":      text,
	}, nil)
}

// Spectate takes the reserved admin seat in a room, which works even while
// the room is locked and in game.
func (c *Client) Spectate(roomID, playerID, nick string) (*ConnectInfo, error) {
	var info ConnectInfo
	err := c.do("POST", "/v1/rooms/"+roomID+"/spectate",
		map[string]string{"player_id": playerID, "nick": nick}, &info)
	return &info, err
}

// DiagCheck is one line of a self-test.
type DiagCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
	Millis int    `json:"ms,omitempty"`
}

// DiagReport is one machine's self-test, sent to the server so the results
// can be read from the development machine rather than read aloud down a
// telephone by somebody squinting at a status pill.
type DiagReport struct {
	PlayerID string      `json:"player_id"`
	Machine  string      `json:"machine"`
	Version  string      `json:"version"`
	Checks   []DiagCheck `json:"checks"`
	Notes    string      `json:"notes,omitempty"`
}

// SendDiag uploads a self-test. A failure to upload is not a failure of the
// test itself, so callers report it and carry on.
func (c *Client) SendDiag(rep DiagReport) error {
	return c.do("POST", "/v1/diag", rep, nil)
}
