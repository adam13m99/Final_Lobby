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

	"finallobby/coordinator/internal/room"
	"finallobby/coordinator/internal/ticket"
)

// Server wires the room store and ticket store behind HTTP.
type Server struct {
	rooms   *room.Store
	tickets *ticket.Store
	log     *slog.Logger

	// relayAddr and relayPub are handed to clients so they know where to
	// connect and which key to expect. Shipping the key here rather than
	// baking it into the client means rotating it does not need a new build.
	relayAddr string
	relayPub  string

	limitJoin   *Limiter
	limitManage *Limiter
	limitRead   *Limiter
	now       func() time.Time

	// authToken gates the player-facing API during the test phase. There
	// are no accounts yet (sub-project 2), and the coordinator has to be
	// reachable from two PCs, so a shared bearer token is what stands
	// between the API and anyone who portscans the box. Empty disables it.
	authToken string
}

// Config configures the API server.
type Config struct {
	Rooms     *room.Store
	Tickets   *ticket.Store
	RelayAddr string
	RelayPub  string
	Logger    *slog.Logger
	Now       func() time.Time
	AuthToken string
}

func New(cfg Config) *Server {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Server{
		rooms:     cfg.Rooms,
		tickets:   cfg.Tickets,
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
	mux.HandleFunc("POST /v1/lease/renew", s.limited(s.limitRead, s.renewLease))

	// The relay asks about tickets here. Not rate limited: throttling the
	// relay would throttle every player behind it.
	mux.HandleFunc("POST /internal/validate-ticket", s.validateTicket)

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

type roomView struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Status  string   `json:"status"`
	HostID  string   `json:"host_id"`
	Players []string `json:"players"`
	Free    int      `json:"free_slots"`
}

func view(r room.Room) roomView {
	v := roomView{ID: r.ID, Name: r.Name, Status: string(r.Status), HostID: r.HostID}
	for _, p := range r.Slots {
		if p == "" {
			v.Free++
			continue
		}
		v.Players = append(v.Players, p)
	}
	return v
}

func (s *Server) listRooms(w http.ResponseWriter, r *http.Request) {
	rooms := s.rooms.List()
	out := make([]roomView, 0, len(rooms))
	for _, rm := range rooms {
		out = append(out, view(rm))
	}
	writeJSON(w, http.StatusOK, map[string]any{"rooms": out})
}

func (s *Server) getRoom(w http.ResponseWriter, r *http.Request) {
	rm, err := s.rooms.Get(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "no such room")
		return
	}
	writeJSON(w, http.StatusOK, view(rm))
}

// connectInfo is everything a client needs to get onto the network.
type connectInfo struct {
	RoomID     string `json:"room_id"`
	Slot       int    `json:"slot"`
	IsHost     bool   `json:"is_host"`
	VirtualIP  string `json:"virtual_ip"`
	HostIP     string `json:"host_ip"`
	Subnet     string `json:"subnet"`
	Ticket     string `json:"ticket"`
	RelayAddr  string `json:"relay_addr"`
	RelayPub   string `json:"relay_pub"`
	ConnectStr string `json:"dota_connect"`
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
		RoomID:     m.RoomID,
		Slot:       m.Slot,
		IsHost:     m.IsHost,
		VirtualIP:  m.VirtualIP.String(),
		HostIP:     m.HostIP.String(),
		Subnet:     m.Subnet.String(),
		Ticket:     tok,
		RelayAddr:  s.relayAddr,
		RelayPub:   s.relayPub,
		ConnectStr: m.HostIP.String() + ":27015",
	}, nil
}

func (s *Server) createRoom(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PlayerID string `json:"player_id"`
		Name     string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.PlayerID == "" {
		writeErr(w, http.StatusBadRequest, "player_id is required")
		return
	}
	if body.Name == "" {
		body.Name = body.PlayerID + "'s room"
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
	s.log.Info("room created", "room", m.RoomID, "host", body.PlayerID, "vip", m.VirtualIP)
	writeJSON(w, http.StatusCreated, info)
}

func (s *Server) joinRoom(w http.ResponseWriter, r *http.Request) {
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
	s.log.Info("player joined", "room", m.RoomID, "player", body.PlayerID, "slot", m.Slot)
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
	case errors.Is(err, room.ErrNotFound):
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
