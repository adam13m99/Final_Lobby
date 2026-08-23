// Package api is the coordinator's HTTP surface: the player-facing room API
// and the internal endpoint the relay uses to check tickets.
//
// This is the stub coordinator. Accounts, passwords, MMR, friends and
// PostgreSQL persistence are sub-project 2; what is here is the minimum that
// lets two real PCs create a room, join it, and play a match.
package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"lobbybaz/coordinator/internal/chat"
	"lobbybaz/coordinator/internal/player"
	"lobbybaz/coordinator/internal/room"
	"lobbybaz/coordinator/internal/ticket"
)

// Server wires the room store and ticket store behind HTTP.
type Server struct {
	rooms   *room.Store
	tickets *ticket.Store
	players *player.Registry
	chat    *chat.Board
	diag    *diagLog
	dl      *downloads
	log     *slog.Logger

	// relayAddr and relayPub are handed to clients so they know where to
	// connect and which key to expect. Shipping the key here rather than
	// baking it into the client means rotating it does not need a new build.
	relayAddr string
	relayPub  string

	limitJoin   *Limiter
	limitManage *Limiter
	limitRead   *Limiter
	limitChat   *Limiter
	now         func() time.Time

	// authToken gates the player-facing API during the test phase. There
	// are no accounts yet (sub-project 2), and the coordinator has to be
	// reachable from two PCs, so a shared bearer token is what stands
	// between the API and anyone who portscans the box. Empty disables it.
	authToken string
}

// Config configures the API server.
type Config struct {
	Rooms   *room.Store
	Tickets *ticket.Store
	Players *player.Registry
	Chat    *chat.Board

	// DistDir holds the published installer and its manifest. Empty means
	// this coordinator serves no downloads.
	DistDir string
	// DownloadKey is the unguessable path segment the download lives under.
	DownloadKey string
	RelayAddr   string
	RelayPub    string
	Logger      *slog.Logger
	Now         func() time.Time
	AuthToken   string
}

func New(cfg Config) *Server {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Players == nil {
		cfg.Players = player.NewRegistry()
	}
	if cfg.Chat == nil {
		cfg.Chat = chat.NewBoard()
	}
	return &Server{
		rooms:     cfg.Rooms,
		tickets:   cfg.Tickets,
		players:   cfg.Players,
		chat:      cfg.Chat,
		diag:      &diagLog{},
		dl:        &downloads{dir: cfg.DistDir, key: cfg.DownloadKey},
		log:       cfg.Logger,
		relayAddr: cfg.RelayAddr,
		relayPub:  cfg.RelayPub,
		// Three tiers, because the risks differ. Creating or joining a room
		// costs us an address allocation and a ticket, and is what a griefer
		// would automate - keep it tight. Managing a room you already host
		// is legitimate and bursty: a host locking, reopening and kicking
		// within a few seconds is normal play, and throttling that is a bug
		// the player experiences as the app ignoring them. Reading is cheap.
		limitJoin:   NewLimiter(0.5, 5),
		limitManage: NewLimiter(2, 15),
		limitRead:   NewLimiter(5, 30),
		// Chat is per-player-visible and abusable, but a burst is normal:
		// somebody types three short lines in a row. One a second sustained,
		// ten in hand.
		limitChat: NewLimiter(1, 10),
		now:       cfg.Now,
		authToken: cfg.AuthToken,
	}
}

// Routes returns the HTTP handler.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"rooms":   len(s.rooms.List()),
			"tickets": s.tickets.Count(),
		})
	})

	mux.HandleFunc("GET /v1/rooms", s.limited(s.limitRead, s.listRooms))
	mux.HandleFunc("POST /v1/rooms", s.limited(s.limitJoin, s.createRoom))
	mux.HandleFunc("GET /v1/rooms/{id}", s.limited(s.limitRead, s.getRoom))
	mux.HandleFunc("POST /v1/rooms/{id}/join", s.limited(s.limitJoin, s.joinRoom))
	mux.HandleFunc("POST /v1/rooms/{id}/leave", s.limited(s.limitManage, s.leaveRoom))
	mux.HandleFunc("POST /v1/rooms/{id}/kick", s.limited(s.limitManage, s.kickPlayer))
	mux.HandleFunc("POST /v1/rooms/{id}/status", s.limited(s.limitManage, s.setStatus))
	mux.HandleFunc("POST /v1/rooms/{id}/spectate", s.limited(s.limitJoin, s.spectateRoom))
	mux.HandleFunc("POST /v1/rooms/{id}/connect", s.limited(s.limitRead, s.connectRoom))
	mux.HandleFunc("POST /v1/lease/renew", s.limited(s.limitRead, s.renewLease))

	// One call per poll. The client asks for everything it draws at once:
	// who it is, what rooms exist, who is in them, and both chat channels.
	// Five separate polls would be five times the request rate for exactly
	// the same screen.
	mux.HandleFunc("POST /v1/sync", s.limited(s.limitRead, s.sync))
	mux.HandleFunc("POST /v1/me", s.limited(s.limitManage, s.updateMe))
	mux.HandleFunc("POST /v1/chat", s.limited(s.limitChat, s.postChat))
	mux.HandleFunc("POST /v1/diag", s.limited(s.limitManage, s.postDiag))
	mux.HandleFunc("GET /v1/diag", s.limited(s.limitRead, s.listDiag))

	// The relay asks about tickets here. Not rate limited: throttling the
	// relay would throttle every player behind it.
	mux.HandleFunc("POST /internal/validate-ticket", s.validateTicket)

	// The download pages. Unauthenticated by necessity - see download.go.
	s.downloadRoutes(mux)

	return logRequests(s.log, mux)
}

func (s *Server) limited(l *Limiter, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authorised(r) {
			writeErr(w, http.StatusUnauthorized, "missing or bad bearer token")
			return
		}
		if !l.Allow(clientKey(r), s.now()) {
			w.Header().Set("Retry-After", "2")
			writeErr(w, http.StatusTooManyRequests, "slow down")
			return
		}
		next(w, r)
	}
}

// authorised checks the shared bearer token when one is configured. The
// comparison is constant-time so a caller cannot discover the token a byte
// at a time by timing the response.
func (s *Server) authorised(r *http.Request) bool {
	if s.authToken == "" {
		return true
	}
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.authToken)) == 1
}

// --- player-facing endpoints -------------------------------------------

// memberView is one seated player as the lobby draws them.
type memberView struct {
	// Seat is "player", "observer" or "admin" (D38).
	Seat string `json:"seat,omitempty"`
	PlayerID  string `json:"player_id"`
	Nick      string `json:"nick"`
	MMR       int    `json:"mmr"`
	Slot      int    `json:"slot"`
	IsHost    bool   `json:"is_host"`
	Spectator bool   `json:"spectator"`
}

type roomView struct {
	ID       string       `json:"id"`
	Name     string       `json:"name"`
	Status   string       `json:"status"`
	HostID   string       `json:"host_id"`
	HostNick string       `json:"host_nick"`
	Members  []memberView `json:"members"`
	Seats    int          `json:"seats"`
	Free     int          `json:"free_slots"`
	// Watchers is how many observer seats are taken, of ipam.ObserverSlots.
	Watchers int `json:"watchers"`
	AvgMMR   int          `json:"avg_mmr"`
	Joinable bool         `json:"joinable"`
	// Players is the bare ID list the first CLI was written against.
	Players []string `json:"players"`
}

// view renders a room for the lobby, resolving every seated ID to the name
// and MMR that player declared. A room list showing raw player IDs is not
// something anyone can choose a game from.
func (s *Server) view(r room.Room) roomView {
	v := roomView{
		ID:      r.ID,
		Name:    r.Name,
		Status:  string(r.Status),
		HostID:  r.HostID,
		Members: make([]memberView, 0, len(r.Slots)),
	}
	known := s.players.Lookup(r.Occupants())

	sumMMR, rated := 0, 0
	for slot, id := range r.Slots {
		if id == "" {
			v.Free++
			continue
		}
		v.Players = append(v.Players, id)
		v.Seats++
		m := memberView{PlayerID: id, Slot: slot, IsHost: id == r.HostID, Nick: id}
		if p, ok := known[id]; ok {
			m.Nick, m.MMR = p.Nick, p.MMR
			if p.MMR > 0 {
				sumMMR += p.MMR
				rated++
			}
		}
		if m.IsHost {
			v.HostNick = m.Nick
		}
		v.Members = append(v.Members, m)
	}
	for _, group := range []struct {
		ids  []string
		kind room.SeatKind
	}{
		{r.Observers[:], room.SeatObserver},
		{r.Admins[:], room.SeatAdmin},
	} {
		for seat, id := range group.ids {
			if id == "" {
				continue
			}
			m := memberView{
				PlayerID:  id,
				Slot:      seat,
				Spectator: true,
				Seat:      string(group.kind),
				Nick:      id,
			}
			if p, ok := known[id]; ok {
				m.Nick, m.MMR = p.Nick, p.MMR
			}
			if group.kind == room.SeatObserver {
				v.Watchers++
			}
			v.Members = append(v.Members, m)
		}
	}
	if rated > 0 {
		v.AvgMMR = sumMMR / rated
	}
	if v.HostNick == "" {
		v.HostNick = r.HostID
	}
	v.Joinable = v.Free > 0 &&
		(r.Status == room.StatusOpen || r.Status == room.StatusOpenToNew)
	return v
}

func (s *Server) listRooms(w http.ResponseWriter, r *http.Request) {
	rooms := s.rooms.List()
	out := make([]roomView, 0, len(rooms))
	for _, rm := range rooms {
		out = append(out, s.view(rm))
	}
	writeJSON(w, http.StatusOK, map[string]any{"rooms": out})
}

func (s *Server) getRoom(w http.ResponseWriter, r *http.Request) {
	rm, err := s.rooms.Get(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "no such room")
		return
	}
	writeJSON(w, http.StatusOK, s.view(rm))
}

// connectInfo is everything a client needs to get onto the network.
type connectInfo struct {
	RoomID      string `json:"room_id"`
	Slot        int    `json:"slot"`
	IsHost      bool   `json:"is_host"`
	IsSpectator bool   `json:"is_spectator,omitempty"`
	VirtualIP   string `json:"virtual_ip"`
	HostIP      string `json:"host_ip"`
	Subnet      string `json:"subnet"`
	Ticket      string `json:"ticket"`
	RelayAddr   string `json:"relay_addr"`
	RelayPub    string `json:"relay_pub"`
	ConnectStr  string `json:"dota_connect"`
}

func (s *Server) issue(m room.Membership, playerID string) (connectInfo, error) {
	tok, err := s.tickets.Issue(ticket.Claims{
		PlayerID:  playerID,
		RoomID:    m.RoomID,
		VirtualIP: m.VirtualIP,
	}, s.now())
	if err != nil {
		return connectInfo{}, err
	}
	return connectInfo{
		RoomID:      m.RoomID,
		Slot:        m.Slot,
		IsHost:      m.IsHost,
		IsSpectator: m.IsSpectator(),
		VirtualIP:   m.VirtualIP.String(),
		HostIP:      m.HostIP.String(),
		Subnet:      m.Subnet.String(),
		Ticket:      tok,
		RelayAddr:   s.relayAddr,
		RelayPub:    s.relayPub,
		ConnectStr:  m.HostIP.String() + ":27015",
	}, nil
}

func (s *Server) createRoom(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PlayerID string `json:"player_id"`
		Nick     string `json:"nick"`
		Name     string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.PlayerID == "" {
		writeErr(w, http.StatusBadRequest, "player_id is required")
		return
	}
	nick := s.seen(body.PlayerID, body.Nick)
	if body.Name == "" {
		body.Name = nick + "'s room"
	}

	_, m, err := s.rooms.Create(body.PlayerID, body.Name, s.now())
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	info, err := s.issue(m, body.PlayerID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not issue ticket")
		return
	}
	s.chat.System(m.RoomID, nick+" opened the room", s.now())
	s.chat.System(chat.Lobby, nick+" opened \""+body.Name+"\"", s.now())
	s.log.Info("room created", "room", m.RoomID, "host", body.PlayerID, "vip", m.VirtualIP)
	writeJSON(w, http.StatusCreated, info)
}

func (s *Server) joinRoom(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PlayerID string `json:"player_id"`
		Nick     string `json:"nick"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.PlayerID == "" {
		writeErr(w, http.StatusBadRequest, "player_id is required")
		return
	}
	nick := s.seen(body.PlayerID, body.Nick)

	m, err := s.rooms.Join(r.PathValue("id"), body.PlayerID, s.now())
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	info, err := s.issue(m, body.PlayerID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not issue ticket")
		return
	}
	s.chat.System(m.RoomID, nick+" joined", s.now())
	s.log.Info("player joined", "room", m.RoomID, "player", body.PlayerID, "slot", m.Slot)
	writeJSON(w, http.StatusOK, info)
}

// connectRoom hands a seated player a fresh ticket.
//
// Tickets live ten minutes and are minted at join. A room of people
// arranging a match stays open much longer than that, so by the time anyone
// pressed Connect their ticket was usually dead - the relay refused the
// handshake and the app could only report that the tunnel had not come up.
// Rejoining did not help, because Join refuses a player already seated.
// Connect now asks for a new ticket every time, which costs one request and
// removes the whole class of failure.
func (s *Server) connectRoom(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PlayerID string `json:"player_id"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.PlayerID == "" {
		writeErr(w, http.StatusBadRequest, "player_id is required")
		return
	}
	m, err := s.rooms.Membership(r.PathValue("id"), body.PlayerID)
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	info, err := s.issue(m, body.PlayerID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not issue ticket")
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) leaveRoom(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PlayerID string `json:"player_id"`
	}
	if !decode(w, r, &body) {
		return
	}
	id := r.PathValue("id")
	if err := s.rooms.Leave(id, body.PlayerID, s.now()); err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	// Revoking here is what actually removes network access; the room
	// bookkeeping alone would leave the tunnel up.
	s.tickets.RevokePlayerRoom(body.PlayerID, id)
	s.chat.System(id, s.nickOf(body.PlayerID)+" left", s.now())
	s.log.Info("player left", "room", id, "player", body.PlayerID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) kickPlayer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PlayerID string `json:"player_id"`
		TargetID string `json:"target_id"`
	}
	if !decode(w, r, &body) {
		return
	}
	id := r.PathValue("id")
	if err := s.rooms.Kick(id, body.PlayerID, body.TargetID, s.now()); err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	s.tickets.RevokePlayerRoom(body.TargetID, id)
	s.chat.System(id, s.nickOf(body.TargetID)+" was removed by the host", s.now())
	s.log.Info("player kicked", "room", id, "by", body.PlayerID, "target", body.TargetID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) setStatus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PlayerID string `json:"player_id"`
		Status   string `json:"status"`
	}
	if !decode(w, r, &body) {
		return
	}
	st := room.Status(body.Status)
	switch st {
	case room.StatusOpen, room.StatusLocked, room.StatusOpenToNew, room.StatusClosed:
	default:
		writeErr(w, http.StatusBadRequest, "unknown status")
		return
	}
	id := r.PathValue("id")
	if err := s.rooms.SetStatus(id, body.PlayerID, st, s.now()); err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	s.chat.System(id, statusAnnouncement(st), s.now())
	s.log.Info("room status changed", "room", id, "status", st)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": body.Status})
}

// renewLease is what the net-service watchdog calls. A ticket that renews is
// still authorised; anything else means tear the tunnel down.
func (s *Server) renewLease(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Ticket string `json:"ticket"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := s.tickets.Renew(body.Ticket, s.now()); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"valid": false, "reason": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"valid": true})
}

// --- relay-facing endpoint ---------------------------------------------

func (s *Server) validateTicket(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Ticket string `json:"ticket"`
	}
	if !decode(w, r, &body) {
		return
	}
	claims, err := s.tickets.Validate(body.Ticket, s.now())
	if err != nil {
		// The relay stays silent to the peer on rejection; this 403 only
		// travels between our own processes.
		writeErr(w, http.StatusForbidden, "invalid ticket")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"room_id":    claims.RoomID,
		"virtual_ip": claims.VirtualIP.String(),
		"player_id":  claims.PlayerID,
	})
}

// --- plumbing ----------------------------------------------------------

func statusFor(err error) int {
	switch {
	case errors.Is(err, room.ErrNotFound),
		errors.Is(err, room.ErrNotMember):
		return http.StatusNotFound
	case errors.Is(err, room.ErrNotHost):
		return http.StatusForbidden
	case errors.Is(err, room.ErrRoomLocked),
		errors.Is(err, room.ErrKickBlocked),
		errors.Is(err, room.ErrRoomClosed):
		return http.StatusForbidden
	case errors.Is(err, room.ErrRoomFull):
		return http.StatusConflict
	case errors.Is(err, room.ErrAlreadyJoined):
		return http.StatusConflict
	}
	return http.StatusBadRequest
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeErr(w, http.StatusBadRequest, "malformed request body")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}

func logRequests(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		if !strings.HasPrefix(r.URL.Path, "/healthz") {
			log.Debug("request", "method", r.Method, "path", r.URL.Path,
				"took", time.Since(start).Round(time.Millisecond))
		}
	})
}
