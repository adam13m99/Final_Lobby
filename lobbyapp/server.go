package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io/fs"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"finallobby/client/lobby"
	"finallobby/client/session"
	"finallobby/protocol/ipc"
)

type server struct {
	token string

	mu  sync.Mutex
	cfg *session.Config
}

func newServer(token string) *server {
	cfg, err := session.Load()
	if err != nil {
		cfg = &session.Config{}
	}
	return &server{token: token, cfg: cfg}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()

	ui, _ := fs.Sub(uiFiles, "ui")
	mux.Handle("/", http.FileServer(http.FS(ui)))

	mux.HandleFunc("GET /api/state", s.guard(s.state))
	mux.HandleFunc("POST /api/setup", s.guard(s.setup))
	mux.HandleFunc("POST /api/rooms/create", s.guard(s.createRoom))
	mux.HandleFunc("POST /api/rooms/join", s.guard(s.joinRoom))
	mux.HandleFunc("POST /api/rooms/leave", s.guard(s.leaveRoom))
	mux.HandleFunc("POST /api/rooms/status", s.guard(s.setStatus))
	mux.HandleFunc("POST /api/rooms/kick", s.guard(s.kick))
	mux.HandleFunc("POST /api/connect", s.guard(s.connect))
	mux.HandleFunc("POST /api/disconnect", s.guard(s.disconnect))
	mux.HandleFunc("POST /api/play", s.guard(s.play))

	return mux
}

// guard requires the session token and refuses cross-origin callers. Neither
// alone is enough: the token stops a page that guessed the port, and the
// Origin check stops a page that somehow learned the token from reusing it.
func (s *server) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := r.Header.Get("X-Lobby-Token")
		if tok == "" {
			tok = r.URL.Query().Get("t")
		}
		if subtle.ConstantTimeCompare([]byte(tok), []byte(s.token)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "bad session token"})
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !strings.HasPrefix(origin, "http://127.0.0.1:") {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "cross-origin request refused"})
			return
		}
		next(w, r)
	}
}

func (s *server) snapshot() *session.Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := *s.cfg
	return &c
}

func (s *server) update(fn func(*session.Config)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s.cfg)
	return s.cfg.Save()
}

// --- state --------------------------------------------------------------

// state is one call that answers everything the page needs, so the UI never
// has to stitch several requests together and show a half-updated screen.
func (s *server) state(w http.ResponseWriter, r *http.Request) {
	cfg := s.snapshot()

	out := map[string]any{
		"configured":  cfg.Coordinator != "" && cfg.PlayerID != "",
		"player":      cfg.PlayerID,
		"nick":        cfg.Nick,
		"coordinator": cfg.Coordinator,
		"room_id":     cfg.RoomID,
		"is_host":     cfg.IsHost,
		"virtual_ip":  cfg.VirtualIP,
		"host_ip":     cfg.HostIP,
	}

	// The service: is it installed and what is the tunnel doing?
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if resp, err := ipc.Call(ctx, ipc.Request{Op: ipc.OpStatus}); err != nil {
		out["service"] = false
		out["service_error"] = "The Final Lobby network service is not running. " +
			"Run install.ps1 as Administrator."
	} else {
		out["service"] = true
		out["tunnel"] = resp.State
		out["connected"] = resp.Connected
		out["adapter"] = resp.AdapterName
		if resp.VirtualIP != "" {
			out["virtual_ip"] = resp.VirtualIP
		}
		if resp.Err != "" {
			out["tunnel_error"] = resp.Err
		}
	}

	if cfg.Coordinator != "" && cfg.PlayerID != "" {
		api := lobby.New(cfg.Coordinator, cfg.AuthToken)
		if rooms, err := api.ListRooms(); err != nil {
			out["coordinator_error"] = err.Error()
		} else {
			out["rooms"] = rooms
		}
		if cfg.RoomID != "" {
			if rv, err := api.GetRoom(cfg.RoomID); err == nil {
				out["room"] = rv
			} else {
				// The room is gone - the host left, or the coordinator
				// restarted. Do not strand the player on a dead screen.
				_ = s.update(func(c *session.Config) { c.ClearRoom() })
				out["room_id"] = ""
				out["room_gone"] = true
			}
		}
	}

	writeJSON(w, http.StatusOK, out)
}

// --- actions ------------------------------------------------------------

func (s *server) setup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Coordinator string `json:"coordinator"`
		Token       string `json:"token"`
		Player      string `json:"player"`
		Nick        string `json:"nick"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Coordinator == "" || body.Player == "" {
		fail(w, "Server address and player name are both required.")
		return
	}
	if body.Nick == "" {
		body.Nick = body.Player
	}

	// Check it works before saving, so a typo is caught here rather than
	// three screens later.
	if _, err := lobby.New(body.Coordinator, body.Token).ListRooms(); err != nil {
		fail(w, err.Error())
		return
	}
	if err := s.update(func(c *session.Config) {
		c.Coordinator = body.Coordinator
		c.AuthToken = body.Token
		c.PlayerID = body.Player
		c.Nick = body.Nick
	}); err != nil {
		fail(w, err.Error())
		return
	}
	ok(w)
}

func (s *server) api() *lobby.Client {
	cfg := s.snapshot()
	return lobby.New(cfg.Coordinator, cfg.AuthToken)
}

func (s *server) createRoom(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	cfg := s.snapshot()
	info, err := s.api().CreateRoom(cfg.PlayerID, body.Name)
	if err != nil {
		fail(w, err.Error())
		return
	}
	s.storeRoom(info)
	ok(w)
}

func (s *server) joinRoom(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RoomID string `json:"room_id"`
	}
	if !decode(w, r, &body) {
		return
	}
	cfg := s.snapshot()
	info, err := s.api().JoinRoom(body.RoomID, cfg.PlayerID)
	if err != nil {
		fail(w, err.Error())
		return
	}
	s.storeRoom(info)
	ok(w)
}

func (s *server) storeRoom(info *lobby.ConnectInfo) {
	_ = s.update(func(c *session.Config) {
		c.RoomID = info.RoomID
		c.VirtualIP = info.VirtualIP
		c.HostIP = info.HostIP
		c.Subnet = info.Subnet
		c.Ticket = info.Ticket
		c.RelayAddr = info.RelayAddr
		c.RelayPub = info.RelayPub
		c.IsHost = info.IsHost
	})
}

func (s *server) leaveRoom(w http.ResponseWriter, r *http.Request) {
	cfg := s.snapshot()
	if cfg.RoomID == "" {
		ok(w)
		return
	}
	if err := s.api().LeaveRoom(cfg.RoomID, cfg.PlayerID); err != nil {
		fail(w, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	_, _ = ipc.Call(ctx, ipc.Request{Op: ipc.OpDisconnect})
	_ = s.update(func(c *session.Config) { c.ClearRoom() })
	ok(w)
}

func (s *server) setStatus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Status string `json:"status"`
	}
	if !decode(w, r, &body) {
		return
	}
	cfg := s.snapshot()
	if err := s.api().SetStatus(cfg.RoomID, cfg.PlayerID, body.Status); err != nil {
		fail(w, err.Error())
		return
	}
	ok(w)
}

func (s *server) kick(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Target string `json:"target"`
	}
	if !decode(w, r, &body) {
		return
	}
	cfg := s.snapshot()
	if err := s.api().Kick(cfg.RoomID, cfg.PlayerID, body.Target); err != nil {
		fail(w, err.Error())
		return
	}
	ok(w)
}

func (s *server) connect(w http.ResponseWriter, r *http.Request) {
	cfg := s.snapshot()
	if cfg.RoomID == "" {
		fail(w, "Join a room first.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()

	resp, err := ipc.Call(ctx, ipc.Request{
		Op:          ipc.OpConnect,
		RelayAddr:   cfg.RelayAddr,
		RelayPub:    cfg.RelayPub,
		Ticket:      cfg.Ticket,
		VirtualIP:   cfg.VirtualIP,
		Subnet:      cfg.Subnet,
		Coordinator: cfg.Coordinator,
		AuthToken:   cfg.AuthToken,
		RoomID:      cfg.RoomID,
	})
	if err != nil {
		fail(w, err.Error())
		return
	}
	if resp.Err != "" {
		fail(w, resp.Err)
		return
	}
	ok(w)
}

func (s *server) disconnect(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if _, err := ipc.Call(ctx, ipc.Request{Op: ipc.OpDisconnect}); err != nil {
		fail(w, err.Error())
		return
	}
	ok(w)
}

func (s *server) play(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode int    `json:"mode"`
		Team string `json:"team"`
	}
	if !decode(w, r, &body) {
		return
	}
	cfg := s.snapshot()
	if cfg.RoomID == "" {
		fail(w, "Join a room first.")
		return
	}
	if body.Mode == 0 {
		body.Mode = 1
	}
	if body.Team == "" {
		body.Team = "good"
	}

	req := ipc.Request{Op: ipc.OpLaunch, Nick: cfg.Nick, GameMode: body.Mode, Team: body.Team}
	if cfg.IsHost {
		req.Role = "host"
	} else {
		req.Role = "client"
		req.HostIP = cfg.HostIP
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	resp, err := ipc.Call(ctx, req)
	if err != nil {
		fail(w, err.Error())
		return
	}
	if resp.Err != "" {
		fail(w, resp.Err)
		return
	}

	// The service validated the command; we start it, because a service runs
	// in session 0 where there is no desktop and no GPU.
	cmd := exec.Command(resp.DotaPath, resp.Args...)
	cmd.Dir = filepath.Dir(resp.DotaPath)
	if err := cmd.Start(); err != nil {
		fail(w, "Could not start Dota 2: "+err.Error())
		return
	}
	_ = cmd.Process.Release()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "role": req.Role})
}

// --- plumbing -----------------------------------------------------------

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	defer r.Body.Close()
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(dst); err != nil {
		fail(w, "Malformed request.")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func ok(w http.ResponseWriter) { writeJSON(w, http.StatusOK, map[string]any{"ok": true}) }

func fail(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusBadRequest, map[string]any{"error": msg})
}
