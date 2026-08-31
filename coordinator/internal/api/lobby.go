package api

import (
	"errors"
	"net/http"
	"sort"
	"sync"
	"time"

	"lobbybaz/coordinator/internal/account"
	"lobbybaz/coordinator/internal/chat"
	"lobbybaz/coordinator/internal/player"
	"lobbybaz/coordinator/internal/room"
)

// OnlineWindow is how recently a player must have synced to count as online.
// The client polls every couple of seconds, so this is generous enough to
// survive a stall and short enough that a closed laptop drops off quickly.
const OnlineWindow = 30 * time.Second

// seen records a player and returns the name to show for them. An empty or
// unusable nick falls back to whatever we already knew, and finally to the
// raw ID, so a malformed name can never make a room undrawable.
func (s *Server) seen(playerID, nick string) string {
	if nick != "" {
		if p, err := s.players.Seen(playerID, nick, s.now()); err == nil {
			return p.Nick
		}
	}
	return s.nickOf(playerID)
}

func (s *Server) nickOf(playerID string) string {
	if p, ok := s.players.Get(playerID); ok && p.Nick != "" {
		return p.Nick
	}
	return playerID
}

func statusAnnouncement(st room.Status) string {
	switch st {
	case room.StatusLocked:
		return "The host locked the room - the match is starting"
	case room.StatusOpenToNew:
		return "The host reopened the room for a replacement player"
	case room.StatusOpen:
		return "The room is open again"
	case room.StatusClosed:
		return "The room is closed"
	}
	return "The room changed state"
}

// --- sync ---------------------------------------------------------------

type syncRequest struct {
	PlayerID string `json:"player_id"`
	Nick     string `json:"nick"`

	// RoomID is the room this client believes it is in, so the reply can
	// carry that room's detail and chat without a second call.
	RoomID string `json:"room_id,omitempty"`

	LobbyCursor uint64 `json:"lobby_cursor"`
	RoomCursor  uint64 `json:"room_cursor"`

	// InGame is the client telling us whether Dota is running. The service
	// knows because it launched it and watches its log (D41); nothing else
	// on the server can see a match start, so this is the only honest
	// source for a friend's "in game" light.
	InGame bool `json:"in_game,omitempty"`

	// RelayMillis is this client's round trip to the relay, which only its
	// own machine can measure. It is kept twice: against the player, which
	// is what puts a number beside their seat in a room, and against the
	// room they host, which is the lobby's latency column - see
	// room.Store.ReportHostLatency for why that one is host-only.
	RelayMillis int `json:"relay_ms,omitempty"`
}

type syncResponse struct {
	Player   player.Player `json:"player"`
	Online   int           `json:"online"`
	Rooms    []roomView    `json:"rooms"`
	Room     *roomView     `json:"room,omitempty"`
	RoomGone bool          `json:"room_gone,omitempty"`
	Seated   bool          `json:"seated"`
	// RoomID is the room the *server* has this player seated in, empty if
	// none (D82). The client sends the room it believes it is in; this is the
	// answer, and the client is expected to take it.
	//
	// It exists because "you are already in another room" is a dead end for
	// anybody whose own window has lost track - a cleared session, a
	// reinstall, a crash between joining and saving. The server always knows;
	// there is no reason for the app ever to be lost, and every reason not to
	// leave the escape route to a person who cannot see the state they are
	// stuck in.
	RoomID string `json:"in_room_id"`

	LobbyChat   []chat.Message `json:"lobby_chat,omitempty"`
	LobbyCursor uint64         `json:"lobby_cursor"`
	RoomChat    []chat.Message `json:"room_chat,omitempty"`
	RoomCursor  uint64         `json:"room_cursor"`

	ServerTime time.Time `json:"server_time"`
}

// sync is the client's heartbeat and its only polling call. It returns the
// whole screen: the profile, the room list, the room the caller is in, and
// both chat channels since the cursors the caller last saw.
func (s *Server) sync(w http.ResponseWriter, r *http.Request) {
	var body syncRequest
	if !decode(w, r, &body) {
		return
	}
	// The session decides who this is; the body only suggests it.
	body.PlayerID = s.actor(r, body.PlayerID)

	// Nobody at all, on a server that has accounts: somebody browsing before
	// they sign up (D45). They get the lobby and nothing personal - no
	// presence recorded, no room, no private chat - because there is nobody
	// to record it against. Asking them to sign in first is how an install
	// gets abandoned by a person who cannot yet see whether anyone is
	// playing.
	if body.PlayerID == "" {
		if s.accountsOn() {
			s.browse(w)
			return
		}
		writeErr(w, http.StatusBadRequest, "player_id is required")
		return
	}
	s.seen(body.PlayerID, body.Nick)
	s.players.SetInGame(body.PlayerID, body.InGame, s.now())
	s.players.SetRelay(body.PlayerID, body.RelayMillis, s.now())
	if body.RoomID != "" {
		s.rooms.ReportHostLatency(body.RoomID, body.PlayerID, body.RelayMillis, s.now())
	}

	out := syncResponse{
		Online:     s.players.Online(OnlineWindow, s.now()),
		ServerTime: s.now(),
	}
	out.RoomID, _ = s.rooms.RoomOf(body.PlayerID)
	if p, ok := s.players.Get(body.PlayerID); ok {
		out.Player = p
	} else {
		out.Player = player.Player{ID: body.PlayerID, Nick: body.PlayerID}
	}

	rooms := s.rooms.List()
	out.Rooms = make([]roomView, 0, len(rooms))
	for _, rm := range rooms {
		out.Rooms = append(out.Rooms, s.view(rm))
	}
	// Newest room first: a room someone just opened is the one people are
	// waiting for, and it should not appear at the bottom of the list.
	sort.SliceStable(out.Rooms, func(i, j int) bool { return out.Rooms[i].ID > out.Rooms[j].ID })

	out.LobbyChat = s.chat.Since(chat.Lobby, body.LobbyCursor)
	out.LobbyCursor = cursorAfter(body.LobbyCursor, out.LobbyChat)

	if body.RoomID != "" {
		rm, err := s.rooms.Get(body.RoomID)
		if err != nil {
			// The room is gone - closed, or the coordinator restarted. The
			// client has to be told explicitly, or it sits forever on a
			// screen for a room that no longer exists.
			out.RoomGone = true
		} else {
			v := s.view(rm)
			out.Room = &v
			_, _, out.Seated = rm.SlotOf(body.PlayerID)
			// Only people in a room read what is said in it (D82). Writing
			// has been guarded since the chat was built - "anyone who learns
			// a room ID can heckle a match they are not part of" - and
			// reading was not, which is the same objection and the larger
			// half of it. Every room's id is in the lobby list handed to
			// every client on every poll, so claiming one costs nothing.
			//
			// The room itself stays visible: a room in the lobby is meant to
			// be looked at by people deciding whether to join it. What they
			// do not get is the conversation inside it.
			if out.Seated {
				out.RoomChat = s.chat.Since(body.RoomID, body.RoomCursor)
				out.RoomCursor = cursorAfter(body.RoomCursor, out.RoomChat)
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// cursorAfter keeps the caller's cursor when nothing new arrived, rather
// than resetting it to zero and replaying the backlog on the next poll.
func cursorAfter(was uint64, msgs []chat.Message) uint64 {
	if len(msgs) == 0 {
		return was
	}
	return msgs[len(msgs)-1].ID
}

// --- profile ------------------------------------------------------------

func (s *Server) updateMe(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PlayerID string `json:"player_id"`
		Nick     string `json:"nick"`
		MMR      *int   `json:"mmr"`
	}
	if !decode(w, r, &body) {
		return
	}
	// The session decides who this is; the body only suggests it.
	body.PlayerID = s.actor(r, body.PlayerID)
	if body.PlayerID == "" {
		writeErr(w, http.StatusBadRequest, "player_id is required")
		return
	}
	// With accounts enabled the database is where a profile lives and the
	// registry is only the live view of it. Writing to the registry alone
	// would mean a player's declared MMR - a number they may only change once
	// a week - quietly resets every time the coordinator restarts, and the
	// once-a-week rule would be enforced against nothing.
	if body.Nick != "" {
		if s.accountsOn() {
			if _, err := s.accounts.SetDisplayName(body.PlayerID, body.Nick, s.now()); err != nil {
				writeErr(w, authStatus(err), err.Error())
				return
			}
		}
		if _, err := s.players.Seen(body.PlayerID, body.Nick, s.now()); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if body.MMR != nil {
		if s.accountsOn() {
			if _, err := s.accounts.SetMMR(body.PlayerID, *body.MMR, s.now()); err != nil {
				code := http.StatusBadRequest
				if errors.Is(err, account.ErrMMRTooSoon) {
					code = http.StatusConflict
				}
				writeErr(w, code, err.Error())
				return
			}
			// The registry mirrors what the database now says. It keeps its
			// own once-a-week clock, which has already been satisfied above,
			// so a refused mirror write is not an error the player caused.
			_, _ = s.players.SetMMR(body.PlayerID, *body.MMR, s.now())
		} else if _, err := s.players.SetMMR(body.PlayerID, *body.MMR, s.now()); err != nil {
			code := http.StatusBadRequest
			if errors.Is(err, player.ErrMMRTooSoon) {
				code = http.StatusConflict
			}
			writeErr(w, code, err.Error())
			return
		}
	}
	p, ok := s.players.Get(body.PlayerID)
	if !ok {
		writeErr(w, http.StatusBadRequest, "nothing to save - send a name first")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// --- chat ---------------------------------------------------------------

func (s *Server) postChat(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PlayerID string `json:"player_id"`
		Nick     string `json:"nick"`
		Channel  string `json:"channel"`
		Text     string `json:"text"`
	}
	if !decode(w, r, &body) {
		return
	}
	// The session decides who this is; the body only suggests it.
	body.PlayerID = s.actor(r, body.PlayerID)
	if body.PlayerID == "" {
		writeErr(w, http.StatusBadRequest, "player_id is required")
		return
	}
	if body.Channel == "" {
		body.Channel = chat.Lobby
	}
	// Only people in a room may talk in it. Without this check anyone who
	// learns a room ID can heckle a match they are not part of.
	if body.Channel != chat.Lobby {
		rm, err := s.rooms.Get(body.Channel)
		if err != nil {
			writeErr(w, http.StatusNotFound, "no such room")
			return
		}
		if _, _, seated := rm.SlotOf(body.PlayerID); !seated {
			writeErr(w, http.StatusForbidden, "you are not in that room")
			return
		}
	}
	// A mute stops somebody talking without stopping them playing. Most of
	// what makes a lobby unpleasant is said rather than done, and taking away
	// somebody's voice is a smaller thing than taking away their game.
	if rest := s.restricted(body.PlayerID); rest.Muted {
		msg := "you are muted: " + rest.Reason
		if !rest.Until.IsZero() {
			msg += " (until " + rest.Until.Format("15:04") + ")"
		}
		writeErr(w, http.StatusForbidden, msg)
		return
	}
	nick := s.seen(body.PlayerID, body.Nick)
	m, err := s.chat.Post(body.Channel, body.PlayerID, nick, body.Text, s.now())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// --- spectator ----------------------------------------------------------

func (s *Server) spectateRoom(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PlayerID string `json:"player_id"`
		Nick     string `json:"nick"`
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	// The session decides who this is; the body only suggests it.
	body.PlayerID = s.actor(r, body.PlayerID)
	if body.PlayerID == "" {
		writeErr(w, http.StatusBadRequest, "player_id is required")
		return
	}
	if why, yes := s.barred(body.PlayerID); yes {
		writeErr(w, http.StatusForbidden, why)
		return
	}
	nick := s.seen(body.PlayerID, body.Nick)

	id := r.PathValue("id")
	rm, err := s.rooms.Get(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "that room has closed")
		return
	}
	m, err := s.rooms.JoinObserver(id, s.applicant(r, rm, body.PlayerID, body.Password), s.now())
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	info, err := s.issue(m, body.PlayerID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not issue ticket")
		return
	}
	s.chat.System(m.RoomID, nick+" is watching", s.now())
	s.log.Info("observer joined", "room", m.RoomID, "player", body.PlayerID, "seat", m.Slot)
	writeJSON(w, http.StatusOK, info)
}

// --- diagnostics --------------------------------------------------------

// DiagReport is one machine's self-test, posted by the app so the results
// can be read from the development machine instead of read aloud over a
// telephone by somebody squinting at a status pill.
type DiagReport struct {
	PlayerID string      `json:"player_id"`
	Nick     string      `json:"nick"`
	Machine  string      `json:"machine"`
	Version  string      `json:"version"`
	At       time.Time   `json:"at"`
	Checks   []DiagCheck `json:"checks"`
	Notes    string      `json:"notes,omitempty"`
}

// DiagCheck is one line of that report.
type DiagCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
	Millis int    `json:"ms,omitempty"`
}

type diagLog struct {
	mu      sync.Mutex
	reports []DiagReport
}

const maxDiagReports = 200

func (d *diagLog) add(rep DiagReport) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reports = append(d.reports, rep)
	if len(d.reports) > maxDiagReports {
		d.reports = append([]DiagReport(nil), d.reports[len(d.reports)-maxDiagReports:]...)
	}
}

func (d *diagLog) all() []DiagReport {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]DiagReport, len(d.reports))
	copy(out, d.reports)
	return out
}

func (s *Server) postDiag(w http.ResponseWriter, r *http.Request) {
	var rep DiagReport
	if !decode(w, r, &rep) {
		return
	}
	if rep.PlayerID == "" {
		writeErr(w, http.StatusBadRequest, "player_id is required")
		return
	}
	rep.At = s.now()
	rep.Nick = s.nickOf(rep.PlayerID)
	s.diag.add(rep)

	failed := 0
	for _, c := range rep.Checks {
		if !c.OK {
			failed++
		}
	}
	s.log.Info("diagnostics", "player", rep.Nick, "machine", rep.Machine,
		"version", rep.Version, "checks", len(rep.Checks), "failed", failed)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) listDiag(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"reports": s.diag.all()})
}

// browse is what an anonymous caller sees: the lobby, and nothing that
// belongs to a person (D45).
//
// The room list is already public - GET /v1/rooms has always been open, so
// that somebody can look before they commit - and the lobby chat is worth
// showing for the same reason: an empty room list and a silent chat say
// different things about whether a place is worth joining.
//
// What is deliberately absent: no presence is recorded, so browsers do not
// inflate the online count; no room, because they are not in one; no room
// chat, because that belongs to the people in it.
func (s *Server) browse(w http.ResponseWriter) {
	out := syncResponse{
		Online:     s.players.Online(OnlineWindow, s.now()),
		ServerTime: s.now(),
	}
	rooms := s.rooms.List()
	out.Rooms = make([]roomView, 0, len(rooms))
	for _, rm := range rooms {
		out.Rooms = append(out.Rooms, s.view(rm))
	}
	out.LobbyChat = s.chat.Since(chat.Lobby, 0)
	out.LobbyCursor = cursorAfter(0, out.LobbyChat)
	writeJSON(w, http.StatusOK, out)
}
