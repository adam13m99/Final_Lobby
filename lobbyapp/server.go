package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"finallobby/client/build"
	"finallobby/client/lobby"
	"finallobby/client/session"
	"finallobby/protocol/ipc"
)

// chatKeep is how many messages the app holds for the page to draw. The
// server keeps more; this is what fits on a screen and scrolls.
const chatKeep = 200

type server struct {
	token string

	mu  sync.Mutex
	cfg *session.Config

	// Chat accumulates here rather than in the page, so a browser refresh
	// does not lose the conversation and the page stays a renderer.
	lobbyChat   []lobby.ChatMessage
	roomChat    []lobby.ChatMessage
	lobbyCursor uint64
	roomCursor  uint64
	chatRoom    string

	// update is set when the server is publishing a build other than this
	// one. The page offers it; nothing installs itself behind the player.
	update *pendingUpdate

	diagMu   sync.Mutex
	diagBusy bool
	diagLast []lobby.DiagCheck
	diagAt   time.Time
}

type pendingUpdate struct {
	Version string `json:"version"`
	Ready   bool   `json:"ready"`
	Path    string `json:"-"`
	Error   string `json:"error,omitempty"`
}

func newServer(token string) *server {
	cfg, err := session.Load()
	if err != nil {
		cfg = &session.Config{}
	}
	// Fill in the server address and this installation's ID. The player is
	// never asked for either.
	if cfg.Prepare() {
		_ = cfg.Save()
	}
	return &server{token: token, cfg: cfg}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()

	ui, _ := fs.Sub(uiFiles, "ui")
	mux.Handle("/", http.FileServer(http.FS(ui)))

	mux.HandleFunc("GET /api/state", s.guard(s.state))
	mux.HandleFunc("POST /api/profile", s.guard(s.saveProfile))
	mux.HandleFunc("POST /api/chat", s.guard(s.postChat))
	mux.HandleFunc("POST /api/rooms/create", s.guard(s.createRoom))
	mux.HandleFunc("POST /api/rooms/join", s.guard(s.joinRoom))
	mux.HandleFunc("POST /api/rooms/spectate", s.guard(s.spectateRoom))
	mux.HandleFunc("POST /api/rooms/leave", s.guard(s.leaveRoom))
	mux.HandleFunc("POST /api/rooms/status", s.guard(s.setStatus))
	mux.HandleFunc("POST /api/rooms/kick", s.guard(s.kick))
	mux.HandleFunc("POST /api/connect", s.guard(s.connect))
	mux.HandleFunc("POST /api/disconnect", s.guard(s.disconnect))
	mux.HandleFunc("POST /api/play", s.guard(s.play))
	mux.HandleFunc("POST /api/diagnose", s.guard(s.diagnose))
	mux.HandleFunc("POST /api/update", s.guard(s.applyUpdate))

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

func (s *server) update_(fn func(*session.Config)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s.cfg)
	return s.cfg.Save()
}

func (s *server) api() *lobby.Client {
	cfg := s.snapshot()
	return lobby.New(cfg.Coordinator, cfg.AuthToken)
}

// --- state --------------------------------------------------------------

// state is the single call the page polls. One request per tick returns
// everything both screens draw: the profile, the room list with who is in
// each room, the room this player is in, both chat channels, and what the
// network service is doing.
func (s *server) state(w http.ResponseWriter, r *http.Request) {
	cfg := s.snapshot()

	out := map[string]any{
		"version":   build.Version,
		"named":     cfg.Nick != "",
		"player_id": cfg.PlayerID,
		"nick":      cfg.Nick,
		"mmr":       cfg.MMR,
		"room_id":   cfg.RoomID,
		"is_host":   cfg.IsHost,
		"spectator": cfg.IsSpectator,
		"host_ip":   cfg.HostIP,
	}
	if cfg.VirtualIP != "" {
		out["virtual_ip"] = cfg.VirtualIP
	}
	if !build.Configured() {
		out["build_warning"] = "This is a developer build with no server configured."
	}

	// The service: is it installed, and what is the tunnel doing?
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if resp, err := ipc.Call(ctx, ipc.Request{Op: ipc.OpStatus}); err != nil {
		out["service"] = false
		out["service_error"] = "The Final Lobby network service is not running. " +
			"Reinstall the app from the download link."
	} else {
		out["service"] = true
		out["tunnel"] = resp.State
		out["connected"] = resp.Connected
		out["adapter"] = resp.AdapterName
		out["dota_running"] = resp.DotaRunning
		if resp.VirtualIP != "" {
			out["virtual_ip"] = resp.VirtualIP
		}
		if resp.Err != "" {
			out["tunnel_error"] = resp.Err
		}
	}

	if cfg.PlayerID != "" && cfg.Coordinator != "" {
		s.pull(cfg, out)
	}

	s.mu.Lock()
	out["lobby_chat"] = append([]lobby.ChatMessage(nil), s.lobbyChat...)
	out["room_chat"] = append([]lobby.ChatMessage(nil), s.roomChat...)
	upd := s.update
	s.mu.Unlock()
	if upd != nil {
		out["update"] = upd
	}

	s.diagMu.Lock()
	if s.diagBusy {
		out["diag_running"] = true
	}
	if s.diagLast != nil {
		out["diagnostics"] = s.diagLast
		out["diag_at"] = s.diagAt
	}
	s.diagMu.Unlock()

	writeJSON(w, http.StatusOK, out)
}

// pull performs the coordinator sync and folds the result into the reply.
func (s *server) pull(cfg *session.Config, out map[string]any) {
	s.mu.Lock()
	req := lobby.SyncRequest{
		PlayerID:    cfg.PlayerID,
		Nick:        cfg.Nick,
		RoomID:      cfg.RoomID,
		LobbyCursor: s.lobbyCursor,
		RoomCursor:  s.roomCursor,
	}
	// A different room means the chat we are holding belongs to somewhere
	// else. Start it again rather than showing the last room's conversation.
	if s.chatRoom != cfg.RoomID {
		s.roomChat, s.roomCursor, s.chatRoom = nil, 0, cfg.RoomID
		req.RoomCursor = 0
	}
	s.mu.Unlock()

	resp, err := s.api().Sync(req)
	if err != nil {
		out["coordinator_error"] = err.Error()
		return
	}

	out["rooms"] = resp.Rooms
	out["online"] = resp.Online
	out["profile"] = resp.Player
	if resp.Player.Nick != "" {
		out["nick"] = resp.Player.Nick
		out["mmr"] = resp.Player.MMR
		if !resp.Player.MMRSetAt.IsZero() {
			out["mmr_locked_until"] = resp.Player.MMRSetAt.Add(7 * 24 * time.Hour)
		}
	}
	if resp.Room != nil {
		out["room"] = resp.Room
	}
	if resp.RoomGone && cfg.RoomID != "" {
		// The host left, or the coordinator restarted. Do not strand the
		// player on a screen for a room that no longer exists.
		_ = s.update_(func(c *session.Config) { c.ClearRoom() })
		out["room_id"] = ""
		out["room"] = nil
		out["room_gone"] = true
	}
	// A player kicked from a room finds out here: the room still exists but
	// they are no longer seated in it.
	if resp.Room != nil && !resp.Seated && cfg.RoomID != "" {
		_ = s.update_(func(c *session.Config) { c.ClearRoom() })
		out["room_id"] = ""
		out["room"] = nil
		out["removed"] = true
	}

	s.mu.Lock()
	s.lobbyChat = appendChat(s.lobbyChat, resp.LobbyChat)
	s.lobbyCursor = resp.LobbyCursor
	if s.chatRoom == cfg.RoomID {
		s.roomChat = appendChat(s.roomChat, resp.RoomChat)
		s.roomCursor = resp.RoomCursor
	}
	s.mu.Unlock()
}

func appendChat(have, incoming []lobby.ChatMessage) []lobby.ChatMessage {
	if len(incoming) == 0 {
		return have
	}
	have = append(have, incoming...)
	if len(have) > chatKeep {
		have = append([]lobby.ChatMessage(nil), have[len(have)-chatKeep:]...)
	}
	return have
}

// --- profile ------------------------------------------------------------

func (s *server) saveProfile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Nick string `json:"nick"`
		MMR  *int   `json:"mmr"`
	}
	if !decode(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Nick) == "" {
		fail(w, "Choose a name other players will see.")
		return
	}
	cfg := s.snapshot()
	p, err := s.api().SaveProfile(cfg.PlayerID, body.Nick, body.MMR)
	if err != nil {
		fail(w, err.Error())
		return
	}
	if err := s.update_(func(c *session.Config) {
		c.Nick = p.Nick
		c.MMR = p.MMR
	}); err != nil {
		fail(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "profile": p})
}

// --- chat ---------------------------------------------------------------

func (s *server) postChat(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Channel string `json:"channel"`
		Text    string `json:"text"`
	}
	if !decode(w, r, &body) {
		return
	}
	cfg := s.snapshot()
	channel := body.Channel
	if channel == "room" {
		if cfg.RoomID == "" {
			fail(w, "You are not in a room.")
			return
		}
		channel = cfg.RoomID
	} else {
		channel = "lobby"
	}
	if err := s.api().PostChat(cfg.PlayerID, cfg.Nick, channel, body.Text); err != nil {
		fail(w, err.Error())
		return
	}
	ok(w)
}

// --- rooms --------------------------------------------------------------

func (s *server) createRoom(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	cfg := s.snapshot()
	info, err := s.api().CreateRoom(cfg.PlayerID, cfg.Nick, body.Name)
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
	info, err := s.api().JoinRoom(body.RoomID, cfg.PlayerID, cfg.Nick)
	if err != nil {
		fail(w, err.Error())
		return
	}
	s.storeRoom(info)
	ok(w)
}

func (s *server) spectateRoom(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RoomID string `json:"room_id"`
	}
	if !decode(w, r, &body) {
		return
	}
	cfg := s.snapshot()
	info, err := s.api().Spectate(body.RoomID, cfg.PlayerID, cfg.Nick)
	if err != nil {
		fail(w, err.Error())
		return
	}
	s.storeRoom(info)
	ok(w)
}

func (s *server) storeRoom(info *lobby.ConnectInfo) {
	_ = s.update_(func(c *session.Config) {
		c.RoomID = info.RoomID
		c.VirtualIP = info.VirtualIP
		c.HostIP = info.HostIP
		c.Subnet = info.Subnet
		c.Ticket = info.Ticket
		c.RelayAddr = info.RelayAddr
		c.RelayPub = info.RelayPub
		c.IsHost = info.IsHost
		c.IsSpectator = info.IsSpectator
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
	_ = s.update_(func(c *session.Config) { c.ClearRoom() })
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

// --- network ------------------------------------------------------------

func (s *server) connect(w http.ResponseWriter, r *http.Request) {
	cfg := s.snapshot()
	if cfg.RoomID == "" {
		fail(w, "Join a room first.")
		return
	}

	// Always take a fresh ticket. The one stored at join expires after ten
	// minutes, and a room waiting for players is open for much longer than
	// that, so reusing it meant Connect simply stopped working after a while
	// with nothing on screen to explain why.
	info, err := s.api().Refresh(cfg.RoomID, cfg.PlayerID)
	if err != nil {
		fail(w, err.Error())
		return
	}
	s.storeRoom(info)
	cfg = s.snapshot()

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

// --- update -------------------------------------------------------------

// applyUpdate runs the installer that was already downloaded and verified,
// then quits so it can replace these files.
func (s *server) applyUpdate(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	upd := s.update
	s.mu.Unlock()
	if upd == nil || !upd.Ready {
		fail(w, "There is no update ready to install.")
		return
	}

	cmd := exec.Command(upd.Path, "/silent")
	if err := cmd.Start(); err != nil {
		fail(w, "Could not start the update: "+err.Error())
		return
	}
	_ = cmd.Process.Release()
	ok(w)

	// Give the reply time to reach the page before this process goes away.
	go func() {
		time.Sleep(700 * time.Millisecond)
		os.Exit(0)
	}()
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
