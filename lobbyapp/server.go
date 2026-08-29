package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"lobbybaz/client/build"
	"lobbybaz/client/lobby"
	"lobbybaz/client/session"
	"lobbybaz/protocol/ipc"
	"lobbybaz/protocol/launch"
)

// chatKeep is how many messages the app holds for the page to draw. The
// server keeps more; this is what fits on a screen and scrolls.
const chatKeep = 200

type server struct {
	token string

	// dev is empty in every build a player runs. See devUI.
	dev devUI

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

	// connectErr carries why the last attempt to get onto the room's network
	// failed, so a background attempt can still say so on screen.
	connectErr string

	diagMu   sync.Mutex
	diagBusy bool
	diagLast []lobby.DiagCheck
	diagAt   time.Time

	// The friends rail and the announcement strip answer to a slower clock
	// than the lobby poll; see social.go.
	friendsCache cached[*lobby.FriendList]
	bannersCache cached[[]lobby.Banner]
	infoCache    cached[*serverCan]
	termsCache   cached[*lobby.TermsOfUse]
	// Who holds a role. Slow, because being appointed a moderator is not a
	// thing that happens between two polls; see admin.go.
	staffCache cached[[]lobby.StaffMember]
	// Who the coordinator says this account is. Asked rarely: the only thing
	// read from it changes when somebody edits the terms.
	whoCache cached[*lobby.Account]
}

type pendingUpdate struct {
	Version string `json:"version"`
	Ready   bool   `json:"ready"`
	Path    string `json:"-"`
	Error   string `json:"error,omitempty"`
}

func newServer(token string) *server {
	return newServerWithUI(token, "")
}

// newServerWithUI is newServer plus the development flag: a directory to
// serve the interface from instead of the copy inside the binary.
func newServerWithUI(token, uiDir string) *server {
	cfg, err := session.Load()
	if err != nil {
		cfg = &session.Config{}
	}
	// Fill in the server address and this installation's ID. The player is
	// never asked for either.
	if cfg.Prepare() {
		_ = cfg.Save()
	}
	srv := &server{token: token, cfg: cfg, dev: devUI{dir: uiDir}}
	// Each of these answers to its own clock; see social.go. Without an
	// interval they would refetch on every poll, which is the thing they
	// exist to stop.
	srv.friendsCache.every = friendsEvery
	srv.bannersCache.every = bannersEvery
	srv.infoCache.every = infoEvery
	srv.termsCache.every = infoEvery
	srv.staffCache.every = staffEvery
	srv.whoCache.every = infoEvery
	return srv
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()

	if s.dev.dir != "" {
		mux.Handle("/", noStore(http.FileServer(http.Dir(s.dev.dir))))
	} else {
		ui, _ := fs.Sub(uiFiles, "ui")
		mux.Handle("/", http.FileServer(http.FS(ui)))
	}

	mux.HandleFunc("GET /api/state", s.guard(s.state))
	mux.HandleFunc("POST /api/profile", s.guard(s.saveProfile))
	mux.HandleFunc("POST /api/launchoptions", s.guard(s.saveLaunchOptions))
	mux.HandleFunc("POST /api/notifications", s.guard(s.saveNotifications))
	mux.HandleFunc("POST /api/chat", s.guard(s.postChat))
	mux.HandleFunc("POST /api/rooms/create", s.guard(s.createRoom))
	mux.HandleFunc("POST /api/rooms/join", s.guard(s.joinRoom))
	mux.HandleFunc("POST /api/rooms/spectate", s.guard(s.spectateRoom))
	mux.HandleFunc("POST /api/rooms/leave", s.guard(s.leaveRoom))
	mux.HandleFunc("POST /api/rooms/status", s.guard(s.setStatus))
	mux.HandleFunc("POST /api/rooms/kick", s.guard(s.kick))
	mux.HandleFunc("POST /api/rooms/slot", s.guard(s.takeSlot))
	mux.HandleFunc("POST /api/connect", s.guard(s.connect))
	mux.HandleFunc("POST /api/disconnect", s.guard(s.disconnect))
	mux.HandleFunc("POST /api/play", s.guard(s.play))
	mux.HandleFunc("POST /api/diagnose", s.guard(s.diagnose))
	mux.HandleFunc("POST /api/update", s.guard(s.applyUpdate))
	mux.HandleFunc("POST /api/rooms/describe", s.guard(s.describeRoom))
	mux.HandleFunc("POST /api/rooms/privacy", s.guard(s.setPrivacy))
	mux.HandleFunc("POST /api/rooms/invite", s.guard(s.inviteToRoom))
	mux.HandleFunc("POST /api/friends", s.guard(s.friendAction))
	mux.HandleFunc("POST /api/friends/messages", s.guard(s.conversation))
	mux.HandleFunc("POST /api/friends/invite", s.guard(s.inviteFriend))
	mux.HandleFunc("POST /api/friends/invitations/seen", s.guard(s.invitationsSeen))
	mux.HandleFunc("GET /api/players/find", s.guard(s.findPlayer))
	mux.HandleFunc("POST /api/auth/signup", s.guard(s.signUp))
	mux.HandleFunc("POST /api/auth/signin", s.guard(s.signIn))
	mux.HandleFunc("POST /api/auth/signout", s.guard(s.signOut))
	mux.HandleFunc("POST /api/auth/password", s.guard(s.changePassword))
	mux.HandleFunc("POST /api/auth/terms", s.guard(s.acceptTerms))
	mux.HandleFunc("GET /api/terms", s.guard(s.terms))

	// Moderation (admin.go). Offered to everybody and refused by the
	// coordinator to everybody without a role - the interface hides them as a
	// courtesy, not as a defence. See admin.go.
	mux.HandleFunc("GET /api/admin/player", s.guard(s.lookUp))
	mux.HandleFunc("GET /api/admin/labels", s.guard(s.labelSet))
	mux.HandleFunc("GET /api/admin/log", s.guard(s.auditLog))
	mux.HandleFunc("POST /api/admin/sanction", s.guard(s.sanction))
	mux.HandleFunc("POST /api/admin/sanction/lift", s.guard(s.liftSanction))
	mux.HandleFunc("POST /api/admin/label", s.guard(s.label))
	mux.HandleFunc("POST /api/admin/rooms/close", s.guard(s.closeRoom))
	mux.HandleFunc("POST /api/admin/rooms/host", s.guard(s.changeHost))
	mux.HandleFunc("POST /api/admin/banners", s.guard(s.saveBanner))
	mux.HandleFunc("POST /api/admin/banners/remove", s.guard(s.removeBanner))
	mux.HandleFunc("POST /api/admin/staff", s.guard(s.setRole))

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
	c := lobby.New(cfg.Coordinator, cfg.AuthToken)
	// The session, when there is one, is what makes a request act as an
	// account rather than as a bare player id (D53). Everything that is
	// account-scoped - friends, private messages, moderation - depends on it
	// being carried on every call, so it is attached here rather than at
	// each call site.
	if cfg.Session != "" {
		c.UseSession(cfg.Session)
	}
	return c
}


// --- development: the interface, served from disk ------------------------

// devUI holds the directory the interface is being served from when the app
// was started with -dev-ui, and nothing at all otherwise.
//
// The installed app serves its interface out of the binary (go:embed), which
// is right for a player and useless for looking at a change: every edit to a
// stylesheet would mean a rebuild, a restart, and a window that has lost
// wherever it was. With -dev-ui the same files are read from disk on every
// request, and the page is told to reload itself the moment one of them
// changes. Nothing a player runs passes the flag.
type devUI struct {
	dir string
}

// stamp is one value that changes whenever any file under the interface
// directory changes. Modification time, total size and file count folded
// together - not a hash, because this runs on every poll and the answer only
// has to be different, not meaningful.
func (d devUI) stamp() string {
	if d.dir == "" {
		return ""
	}
	var newest, total, count int64
	_ = filepath.WalkDir(d.dir, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		if t := info.ModTime().UnixNano(); t > newest {
			newest = t
		}
		total += info.Size()
		count++
		return nil
	})
	return fmt.Sprintf("%d-%d-%d", newest, total, count)
}

// noStore stops the browser holding on to a file the developer has just
// changed. A live window showing yesterday's stylesheet is worse than no
// live window.
func noStore(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		h.ServeHTTP(w, r)
	})
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

		"launch_options": cfg.LaunchOptions,
		// Resolved, never the raw pointer: the tray process reads this to
		// decide whether to interrupt somebody, and a null there would mean
		// "none of them" to it rather than "all of them" (D66).
		"notify": cfg.Notifications(),
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
		out["service_error"] = "The LobbyBaz network service is not running. " +
			"Reinstall the app from the download link."
	} else {
		out["service"] = true
		out["tunnel"] = resp.State
		out["connected"] = resp.Connected
		out["adapter"] = resp.AdapterName
		out["dota_running"] = resp.DotaRunning
		out["dota_path"] = resp.DotaPath
		out["relay_ms"] = resp.RelayMillis
		if resp.VirtualIP != "" {
			out["virtual_ip"] = resp.VirtualIP
		}
		if resp.Err != "" {
			out["tunnel_error"] = resp.Err
		}
	}

	out["username"] = cfg.Username
	out["signed_in"] = cfg.Session != ""
	if cfg.Coordinator != "" {
		s.capabilities(out)
	}
	if cfg.PlayerID != "" && cfg.Coordinator != "" {
		s.pull(cfg, out)
		s.social(out)
		s.role(out)
		s.whoami(out)
	}

	s.mu.Lock()
	out["lobby_chat"] = append([]lobby.ChatMessage(nil), s.lobbyChat...)
	out["room_chat"] = append([]lobby.ChatMessage(nil), s.roomChat...)
	upd := s.update
	connectErr := s.connectErr
	s.mu.Unlock()
	// Only worth showing while it is still true; the service is the authority
	// on whether we are on the network.
	if connectErr != "" && out["connected"] != true {
		out["connect_error"] = connectErr
	}
	if upd != nil {
		out["update"] = upd
	}
	// Development only. The page reloads itself when this changes.
	if stamp := s.dev.stamp(); stamp != "" {
		out["ui_stamp"] = stamp
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
//
// Two of the fields it sends come from this machine and from nowhere else:
// whether Dota is running, and how far this PC is from the relay. The server
// cannot observe either. It shows the first in the friends rail and, when this
// player is hosting, the second beside their room (D42).
func (s *server) pull(cfg *session.Config, out map[string]any) {
	inGame, _ := out["dota_running"].(bool)
	relayMS, _ := out["relay_ms"].(int)

	s.mu.Lock()
	req := lobby.SyncRequest{
		PlayerID:    cfg.PlayerID,
		Nick:        cfg.Nick,
		RoomID:      cfg.RoomID,
		LobbyCursor: s.lobbyCursor,
		RoomCursor:  s.roomCursor,
		InGame:      inGame,
		RelayMillis: relayMS,
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

// saveLaunchOptions stores the player's own extra Dota command line.
//
// The text is parsed here so the mistake is caught while they are looking at
// the field, and stored raw rather than as a parsed list, because the service
// parses it again for itself before anything reaches a process. This side is
// a courtesy; the far side is the gate (D65).
func (s *server) saveLaunchOptions(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Options string `json:"options"`
	}
	if !decode(w, r, &body) {
		return
	}
	opts := strings.TrimSpace(body.Options)
	if _, err := launch.Options(opts); err != nil {
		fail(w, err.Error())
		return
	}
	if err := s.update_(func(c *session.Config) { c.LaunchOptions = opts }); err != nil {
		fail(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "launch_options": opts})
}

// saveNotifications stores which desktop notifications this PC wants.
//
// The whole set arrives every time, never one switch, so there is no way for
// the stored value to end up half written by two saves crossing. Like the
// launch options these belong to the installation and the coordinator is not
// told: which interruptions somebody wants is about the machine in front of
// them (D66).
func (s *server) saveNotifications(w http.ResponseWriter, r *http.Request) {
	var body session.Notify
	if !decode(w, r, &body) {
		return
	}
	if err := s.update_(func(c *session.Config) { c.Notify = &body }); err != nil {
		fail(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "notify": body})
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

// createRoom opens a room with its door already set (D41).
//
// The door travels with the creation rather than following a moment later.
// Opening a room public and locking it a second afterwards is a second in
// which anybody can walk in, and the person who wanted a private room is the
// person least able to get the stranger back out.
func (s *server) createRoom(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string `json:"name"`
		Privacy  string `json:"privacy"`
		Password string `json:"password"`
		MinMMR   int    `json:"min_mmr"`
	}
	if !decode(w, r, &body) {
		return
	}
	if !doorOK(w, body.Privacy, body.Password) {
		return
	}
	cfg := s.snapshot()
	info, err := s.api().CreateRoomWith(cfg.PlayerID, cfg.Nick, body.Name, lobby.RoomOptions{
		Privacy:  body.Privacy,
		Password: body.Password,
		MinMMR:   body.MinMMR,
	})
	if err != nil {
		fail(w, err.Error())
		return
	}
	s.storeRoom(info)
	go s.autoConnect()
	ok(w)
}

// doorOK checks the door makes sense before asking the coordinator.
//
// A password door with no password is the failure worth catching here: the
// coordinator refuses it, but by then the host has watched a room fail to
// open and been told something about a field they cannot see.
func doorOK(w http.ResponseWriter, privacy, password string) bool {
	switch privacy {
	case "", "public", "friends", "invite":
		return true
	case "password":
		if password == "" {
			fail(w, "a password door needs a password")
			return false
		}
		return true
	}
	fail(w, "unknown door "+privacy)
	return false
}

// setPrivacy changes the door on a room that is already open. Host only,
// enforced by the coordinator.
func (s *server) setPrivacy(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Privacy  string `json:"privacy"`
		Password string `json:"password"`
		MinMMR   int    `json:"min_mmr"`
	}
	if !decode(w, r, &body) {
		return
	}
	if !doorOK(w, body.Privacy, body.Password) {
		return
	}
	cfg := s.snapshot()
	if cfg.RoomID == "" {
		fail(w, "you are not in a room")
		return
	}
	got, err := s.api().SetPrivacy(cfg.RoomID, cfg.PlayerID, lobby.RoomOptions{
		Privacy:  body.Privacy,
		Password: body.Password,
		MinMMR:   body.MinMMR,
	})
	if err != nil {
		fail(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, got)
}

// inviteToRoom opens an invite-only room to one person, or withdraws that.
//
// This is not the same as inviting a friend (social.go). That one sends
// somebody a notification asking them to come; this one lets them through the
// door when they try. A host who wants both does both, and the interface
// offers them together.
func (s *server) inviteToRoom(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Target   string `json:"target_id"`
		Withdraw bool   `json:"withdraw"`
	}
	if !decode(w, r, &body) {
		return
	}
	cfg := s.snapshot()
	if cfg.RoomID == "" {
		fail(w, "you are not in a room")
		return
	}
	var err error
	if body.Withdraw {
		err = s.api().Uninvite(cfg.RoomID, cfg.PlayerID, body.Target)
	} else {
		err = s.api().Invite(cfg.RoomID, cfg.PlayerID, body.Target)
	}
	if err != nil {
		fail(w, err.Error())
		return
	}
	ok(w)
}

func (s *server) joinRoom(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RoomID string `json:"room_id"`
		// Password is the only thing at the door the person types. Every
		// other check - friends, invites, the MMR floor, a kick block - is
		// made by the coordinator from its own records (D41).
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	cfg := s.snapshot()
	info, err := s.api().JoinRoomWith(body.RoomID, cfg.PlayerID, cfg.Nick, body.Password)
	if err != nil {
		fail(w, err.Error())
		return
	}
	s.storeRoom(info)
	go s.autoConnect()
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
	go s.autoConnect()
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

// takeSlot is a player clicking an empty seat to change team.
//
// The address a player is given comes from the slot they sit in, so the
// coordinator throws away their ticket when they move. Reconnecting is
// therefore part of the move rather than something the player is left to
// work out, and only for a player who was already connected: somebody
// picking a side before anybody has pressed Connect should not have a tunnel
// brought up under them.
func (s *server) takeSlot(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Slot int `json:"slot"`
	}
	if !decode(w, r, &body) {
		return
	}
	cfg := s.snapshot()
	if cfg.RoomID == "" {
		fail(w, "Join a room first.")
		return
	}
	if err := s.api().MoveSlot(cfg.RoomID, cfg.PlayerID, body.Slot); err != nil {
		fail(w, err.Error())
		return
	}
	if tunnelUp(r.Context()) {
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		if err := s.bringUpTunnel(ctx); err != nil {
			s.mu.Lock()
			s.connectErr = err.Error()
			s.mu.Unlock()
			fail(w, err.Error())
			return
		}
		s.mu.Lock()
		s.connectErr = ""
		s.mu.Unlock()
	}
	ok(w)
}

// tunnelUp asks the service whether this PC is on a room network right now.
// A service that will not answer counts as down: the worst outcome is a
// player having to press Connect themselves.
func tunnelUp(parent context.Context) bool {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	resp, err := ipc.Call(ctx, ipc.Request{Op: ipc.OpStatus})
	return err == nil && resp.Connected
}

// --- network ------------------------------------------------------------

// bringUpTunnel takes a fresh ticket and asks the service to connect.
//
// Shared by the Connect button and by joining a room. The ticket is always
// refreshed rather than reused: the one stored at join expires after ten
// minutes and a room waiting for players stays open far longer than that,
// which is what made Connect stop working for the first two-PC test (D36).
func (s *server) bringUpTunnel(ctx context.Context) error {
	cfg := s.snapshot()
	if cfg.RoomID == "" {
		return errors.New("Join a room first.")
	}

	info, err := s.api().Refresh(cfg.RoomID, cfg.PlayerID)
	if err != nil {
		return err
	}
	s.storeRoom(info)
	cfg = s.snapshot()

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
		return err
	}
	if resp.Err != "" {
		return errors.New(resp.Err)
	}
	return nil
}

// autoConnect puts a player onto the room's network as soon as they are in
// it, without waiting to be asked.
//
// Being in a room and being on the room's network were separate states and
// nothing said the second one existed. The first two-PC test failed with
// both players seated, Dota hosting a game, and no tunnel on either machine:
// the address the joining player was told to use belonged to nobody. The
// Launch Dota 2 button was correctly disabled, but a Dota player starts Dota
// themselves, and then meets the failure minutes later as an error inside the
// game with nothing connecting it to the cause.
//
// Runs in the background so joining a room stays instant. Failure is
// reported on the room screen and the Connect button remains as a retry.
func (s *server) autoConnect() {
	s.mu.Lock()
	s.connectErr = ""
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	msg := ""
	if err := s.bringUpTunnel(ctx); err != nil {
		msg = err.Error()
	}
	s.mu.Lock()
	s.connectErr = msg
	s.mu.Unlock()
}

func (s *server) connect(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()

	if err := s.bringUpTunnel(ctx); err != nil {
		s.mu.Lock()
		s.connectErr = err.Error()
		s.mu.Unlock()
		fail(w, err.Error())
		return
	}
	s.mu.Lock()
	s.connectErr = ""
	s.mu.Unlock()
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

	req := ipc.Request{Op: ipc.OpLaunch, Nick: cfg.Nick, GameMode: body.Mode, Team: body.Team,
		Options: cfg.LaunchOptions}
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
