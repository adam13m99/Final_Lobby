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

	"lobbybaz/coordinator/internal/account"
	"lobbybaz/coordinator/internal/chat"
	"lobbybaz/coordinator/internal/moderation"
	"lobbybaz/coordinator/internal/player"
	"lobbybaz/coordinator/internal/room"
	"lobbybaz/coordinator/internal/social"
	"lobbybaz/coordinator/internal/ticket"
)

// Server wires the room store and ticket store behind HTTP.
type Server struct {
	rooms    *room.Store
	tickets  *ticket.Store
	players  *player.Registry
	chat     *chat.Board
	accounts *account.Store
	social   *social.Store
	friends  Friends
	mod      *moderation.Store
	kicks    Kicks
	diag     *diagLog
	dl       *downloads
	log      *slog.Logger

	// terms_ holds the text served at GET /v1/terms. Named with a trailing
	// underscore only because `terms` is the handler; see terms.go for why
	// the text lives here rather than in the client.
	terms_ *Terms

	// relayAddr and relayPub are handed to clients so they know where to
	// connect and which key to expect. Shipping the key here rather than
	// baking it into the client means rotating it does not need a new build.
	relayAddr string
	relayPub  string

	limitJoin   *Limiter
	limitManage *Limiter
	limitRead   *Limiter
	limitChat   *Limiter
	limitAuth   *Limiter
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
	// Moderation is staff: roles, sanctions, labels, banners and the audit
	// log. Nil disables every moderation endpoint rather than leaving them
	// open, which is the only safe direction for that default to fall.
	Moderation *moderation.Store

	// Kicks is the durable kick log, for the "kicked N times this week"
	// figure on a player's record. Optional.
	Kicks Kicks

	// Social is the friend graph. Nil runs the coordinator without a
	// friends list, which is what the loadtest harness wants.
	Social *social.Store

	// Friends answers whether two people are friends, for friends-only
	// rooms (D41). Nil until T7 lands the friend graph; a friends-only room
	// then admits nobody but its host, which is the honest failure.
	Friends Friends

	// Accounts is nil on a coordinator running without an account database:
	// the loadtest harness and most tests have no use for one, and the
	// room machinery predates it.
	Accounts *account.Store

	// DistDir holds the published installer and its manifest. Empty means
	// this coordinator serves no downloads.
	DistDir string
	// DownloadKey is the unguessable path segment the download lives under.
	DownloadKey string
	RelayAddr   string
	RelayPub    string

	// TermsFile is the file holding the terms of use served at
	// GET /v1/terms. Empty serves a placeholder that says the server is
	// misconfigured, which is better than inventing an agreement and asking
	// somebody to accept it.
	TermsFile string
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
		accounts:  cfg.Accounts,
		social:    cfg.Social,
		friends:   cfg.Friends,
		mod:       cfg.Moderation,
		kicks:     cfg.Kicks,
		diag:      &diagLog{},
		dl:        &downloads{dir: cfg.DistDir, key: cfg.DownloadKey},
		log:       cfg.Logger,
		relayAddr: cfg.RelayAddr,
		relayPub:  cfg.RelayPub,
		terms_:    NewTerms(cfg.TermsFile),
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
		// Signing in is the one place where an attacker gets something for
		// repeating themselves, so it is the tightest tier: a guess every
		// five seconds, five in hand. A person who mistypes their password
		// twice never notices; somebody working through a word list gets
		// roughly seventeen thousand attempts a day from one address, against
		// an Argon2id hash.
		limitAuth: NewLimiter(0.2, 5),
		now:       cfg.Now,
		authToken: cfg.AuthToken,
	}
}

// Routes returns the HTTP handler.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		// The client needs to know what this server can do before it asks
		// for anything, so it can offer a sign-in screen on a server that
		// has accounts and not offer one on a server that does not. Asking
		// by attempting and reading a 503 works but shows the player an
		// error for a thing they never chose to do.
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":            true,
			"rooms":         len(s.rooms.List()),
			"tickets":       s.tickets.Count(),
			"accounts":      s.accountsOn(),
			"friends":       s.socialOn(),
			"terms_version": TermsVersion,
		})
	})

	// Accounts. Signing up and signing in cannot require a session, for the
	// obvious reason; everything below them does.
	mux.HandleFunc("POST /v1/auth/signup", s.limited(s.limitAuth, s.signUp))
	mux.HandleFunc("POST /v1/auth/login", s.limited(s.limitAuth, s.signIn))
	mux.HandleFunc("POST /v1/auth/logout", s.authenticated(s.limitManage, s.signOut))
	mux.HandleFunc("GET /v1/auth/me", s.authenticated(s.limitRead, s.whoami))
	mux.HandleFunc("POST /v1/auth/password", s.authenticated(s.limitAuth, s.changePassword))
	mux.HandleFunc("POST /v1/terms/accept", s.authenticated(s.limitManage, s.acceptTerms))

	// Friends (D41). All of it needs an account: a friends list belongs to
	// somebody, and there is nobody to belong to without one.
	mux.HandleFunc("GET /v1/friends", s.authenticated(s.limitRead, s.friendList))
	mux.HandleFunc("POST /v1/friends", s.authenticated(s.limitManage, s.befriend))
	mux.HandleFunc("GET /v1/players/find", s.authenticated(s.limitRead, s.findPlayer))
	mux.HandleFunc("POST /v1/friends/messages", s.authenticated(s.limitChat, s.privateMessages))
	mux.HandleFunc("POST /v1/friends/invite", s.authenticated(s.limitManage, s.inviteFriend))
	mux.HandleFunc("POST /v1/friends/invitations/seen", s.authenticated(s.limitManage, s.seenInvitations))

	// Moderation (D43, D47). Every one of these checks the acting admin from
	// the session, and every one writes down what was done.
	mux.HandleFunc("GET /v1/admin/staff", s.authenticated(s.limitRead, s.staffList))
	mux.HandleFunc("POST /v1/admin/staff", s.authenticated(s.limitManage, s.setRole))
	mux.HandleFunc("POST /v1/admin/sanction", s.authenticated(s.limitManage, s.sanction))
	mux.HandleFunc("POST /v1/admin/sanction/lift", s.authenticated(s.limitManage, s.liftSanction))
	mux.HandleFunc("GET /v1/admin/players/{id}", s.authenticated(s.limitRead, s.playerRecord))
	mux.HandleFunc("POST /v1/admin/label", s.authenticated(s.limitManage, s.labelPlayer))
	mux.HandleFunc("GET /v1/admin/labels", s.limited(s.limitRead, s.labelSet))
	mux.HandleFunc("POST /v1/admin/rooms/{id}/close", s.authenticated(s.limitManage, s.closeRoom))
	mux.HandleFunc("POST /v1/admin/rooms/{id}/host", s.authenticated(s.limitManage, s.changeHost))
	mux.HandleFunc("POST /v1/admin/banners", s.authenticated(s.limitManage, s.editBanner))
	mux.HandleFunc("GET /v1/admin/log", s.authenticated(s.limitRead, s.auditLog))

	// The banner strip is open to everybody, including somebody who has not
	// signed in: an announcement about signing up is precisely for them.
	mux.HandleFunc("GET /v1/banners", s.limited(s.limitRead, s.banners))
	// Read before there is an account to read it with.
	mux.HandleFunc("GET /v1/terms", s.limited(s.limitRead, s.terms))

	// Reading the room list needs no account. D45: somebody who has just
	// installed the app should see what is going on before deciding whether
	// to sign up - a lobby that is empty until you have an account looks
	// dead, and asking for a password before showing anything is how a new
	// player decides not to bother.
	mux.HandleFunc("GET /v1/rooms", s.limited(s.limitRead, s.listRooms))
	mux.HandleFunc("POST /v1/rooms", s.signedIn(s.limitJoin, s.createRoom))
	mux.HandleFunc("GET /v1/rooms/{id}", s.limited(s.limitRead, s.getRoom))
	mux.HandleFunc("POST /v1/rooms/{id}/join", s.signedIn(s.limitJoin, s.joinRoom))
	mux.HandleFunc("POST /v1/rooms/{id}/leave", s.signedIn(s.limitManage, s.leaveRoom))
	mux.HandleFunc("POST /v1/rooms/{id}/kick", s.signedIn(s.limitManage, s.kickPlayer))
	mux.HandleFunc("POST /v1/rooms/{id}/slot", s.signedIn(s.limitManage, s.moveSlot))
	mux.HandleFunc("POST /v1/rooms/{id}/status", s.signedIn(s.limitManage, s.setStatus))
	mux.HandleFunc("POST /v1/rooms/{id}/privacy", s.signedIn(s.limitManage, s.setPrivacy))
	mux.HandleFunc("POST /v1/rooms/{id}/description", s.signedIn(s.limitManage, s.setDescription))
	mux.HandleFunc("POST /v1/rooms/{id}/invite", s.signedIn(s.limitManage, s.invite))
	mux.HandleFunc("POST /v1/rooms/{id}/spectate", s.signedIn(s.limitJoin, s.spectateRoom))
	mux.HandleFunc("POST /v1/rooms/{id}/connect", s.signedIn(s.limitRead, s.connectRoom))
	// Renewing a lease is deliberately NOT behind signedIn, and putting it
	// back there breaks every match after three minutes. The caller is the
	// Windows service, which holds a ticket and the shared bearer token and
	// has never held a session - sessions belong to the desktop app. With
	// accounts on, signedIn answered it 401, the watchdog could not tell
	// valid from revoked, and it failed closed on schedule (D77).
	//
	// The ticket is the credential here, the same way it is on
	// /internal/validate-ticket: thirty-two random bytes naming one player in
	// one room, revocable at any moment. Whoever holds it already holds the
	// tunnel, so renewing grants nothing they do not already have.
	mux.HandleFunc("POST /v1/lease/renew", s.limited(s.limitRead, s.renewLease))

	// One call per poll. The client asks for everything it draws at once:
	// who it is, what rooms exist, who is in them, and both chat channels.
	// Five separate polls would be five times the request rate for exactly
	// the same screen.
	// Sync is deliberately not behind signedIn. Somebody who has just
	// installed the app must be able to see the lobby before they are asked
	// for anything (D45), and sync is what draws it. Without a session it
	// answers with the browsable part and nothing else; see sync itself.
	mux.HandleFunc("POST /v1/sync", s.limited(s.limitRead, s.sync))
	mux.HandleFunc("POST /v1/me", s.signedIn(s.limitManage, s.updateMe))
	mux.HandleFunc("POST /v1/chat", s.signedIn(s.limitChat, s.postChat))
	mux.HandleFunc("POST /v1/diag", s.signedIn(s.limitManage, s.postDiag))
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
		// Resolved after the rate limiter, so an unauthenticated flood cannot
		// make us hash and look up a session for every request.
		next(w, s.withSession(r))
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
	Seat      string `json:"seat,omitempty"`
	PlayerID  string `json:"player_id"`
	Nick      string `json:"nick"`
	MMR       int    `json:"mmr"`
	Slot      int    `json:"slot"`
	IsHost    bool   `json:"is_host"`
	Spectator bool   `json:"spectator"`
	// RelayMillis is this member's own round trip to the relay, as their
	// machine measured it. Absent when they have not reported one, or when
	// the one they reported is old enough to describe a connection they no
	// longer have - which must be shown as unknown rather than as zero.
	RelayMillis int `json:"relay_ms,omitempty"`
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
	Watchers int  `json:"watchers"`
	AvgMMR   int  `json:"avg_mmr"`
	Joinable bool `json:"joinable"`
	// Privacy is the door (D41). NeedsPassword is separate because the lobby
	// draws a padlock from it; the password itself is never in this view, and
	// the room type keeps its hash unexported so it cannot accidentally be.
	Privacy       string `json:"privacy"`
	NeedsPassword bool   `json:"needs_password"`
	MinMMR        int    `json:"min_mmr,omitempty"`
	// Description is the host's own sentence about the room (D42).
	Description string `json:"description,omitempty"`
	// HostRelayMillis is the *host's* round trip to the relay, not the
	// reader's. Anything displaying it must say so: a player who reads it as
	// their own ping will blame the wrong thing when a game plays badly.
	// Absent when the host has not reported one yet, which the interface must
	// show as unknown rather than as zero.
	HostRelayMillis int `json:"host_relay_ms,omitempty"`
	// Players is the bare ID list the first CLI was written against.
	Players []string `json:"players"`
	// HostAway is set while the room is counting down to closure because its
	// host stopped answering (D70). The room is still there and the host can
	// still come back; saying nothing and drawing it as a normal open room is
	// how somebody joins a room that vanishes ten seconds later.
	HostAway bool `json:"host_away,omitempty"`
	// HostInGame is the coordinator's own observation that the host is in a
	// match (D69). The status below already reads locked_in_game because of
	// it; this says which of the two reasons it is.
	HostInGame bool `json:"host_in_game,omitempty"`
}

// view renders a room for the lobby, resolving every seated ID to the name
// and MMR that player declared. A room list showing raw player IDs is not
// something anyone can choose a game from.
func (s *Server) view(r room.Room) roomView {
	v := roomView{
		ID:              r.ID,
		Name:            r.Name,
		Description:     r.Description,
		Status:          string(r.Status),
		HostID:          r.HostID,
		Privacy:         string(r.Privacy),
		NeedsPassword:   r.HasPassword(),
		MinMMR:          r.MinMMR,
		HostRelayMillis: r.HostRelayMillis,
		Members:         make([]memberView, 0, len(r.Slots)),
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
			m.RelayMillis = s.freshRelay(p)
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
				m.RelayMillis = s.freshRelay(p)
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
	v.Joinable = v.Free > 0 && r.Admits()

	// The two statuses the room does not store, because they are observations
	// rather than decisions (D69, D70). They are derived here, once, so that
	// every reader - the lobby list, the room screen, the CLI - agrees about
	// what a room whose host is in a match or has gone quiet looks like.
	//
	// A room in its host's grace window stays joinable on purpose: the host
	// coming back is a join, and it is the only thing that saves the room.
	v.HostInGame = r.HostInGame
	switch {
	case r.HostAway():
		v.HostAway = true
		v.Status = statusHostAway
	case r.HostInGame && r.Status == room.StatusOpen:
		v.Status = string(room.StatusLocked)
	}
	return v
}

// statusHostAway is a view-only status. It is not a room.Status: the room is
// still open, and what has happened to it is that nobody is hosting it at
// this instant.
const statusHostAway = "host_away"

// freshRelay returns a player's relay latency only while it still describes
// the connection they have now. A client that stopped reporting leaves its
// last number behind, and a stale reading shown as current is worse than no
// reading at all: it is the number somebody will blame a bad game on.
func (s *Server) freshRelay(p player.Player) int {
	if p.RelayMillis <= 0 || p.RelayAt.IsZero() {
		return 0
	}
	if s.now().Sub(p.RelayAt) > OnlineWindow {
		return 0
	}
	return p.RelayMillis
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
		// Description is the host's sentence about the room (D42).
		Description string `json:"description"`
		// The door, if the host wants one from the start (D41).
		Privacy  string `json:"privacy"`
		Password string `json:"password"`
		MinMMR   int    `json:"min_mmr"`
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
	if body.Description != "" {
		// A failure here is not worth undoing the room over: the host gets
		// their room and can set the description again. Losing the room would
		// be the larger surprise.
		_ = s.rooms.SetDescription(m.RoomID, body.PlayerID, body.Description)
	}
	s.chat.System(m.RoomID, nick+" opened the room", s.now())
	s.chat.System(chat.Lobby, nick+" opened \""+body.Name+"\"", s.now())
	// A host who wanted a private room should get one from the moment it
	// exists. Creating it public and locking it a second later is a second
	// during which anybody can walk in.
	if body.Privacy != "" || body.MinMMR > 0 {
		p := room.Privacy(body.Privacy)
		if body.Privacy == "" {
			p = room.PrivacyPublic
		}
		if err := s.rooms.SetPrivacy(m.RoomID, body.PlayerID, p, body.Password, body.MinMMR, s.now()); err != nil {
			// The room exists and the host is in it, but not with the door
			// they asked for. Leaving it standing would put a public room
			// where somebody asked for a private one, so it goes away now
			// rather than lingering through the host's grace period.
			_ = s.rooms.Close(m.RoomID, body.PlayerID)
			s.tickets.RevokeRoom(m.RoomID)
			if code, ok := doorStatus(err); ok {
				writeErr(w, code, err.Error())
				return
			}
			writeErr(w, statusFor(err), err.Error())
			return
		}
	}
	s.log.Info("room created", "room", m.RoomID, "host", body.PlayerID, "vip", m.VirtualIP)
	writeJSON(w, http.StatusCreated, info)
}

func (s *Server) joinRoom(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PlayerID string `json:"player_id"`
		Nick     string `json:"nick"`
		// Password is the only thing at the door the person types. Everything
		// else the door checks comes from the server's own records.
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
	m, err := s.rooms.Join(id, s.applicant(r, rm, body.PlayerID, body.Password), s.now())
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
	// The session decides who this is; the body only suggests it.
	body.PlayerID = s.actor(r, body.PlayerID)
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
	// The session decides who this is; the body only suggests it.
	body.PlayerID = s.actor(r, body.PlayerID)
	id := r.PathValue("id")
	closed, err := s.rooms.Leave(id, body.PlayerID, s.now())
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	// Revoking here is what actually removes network access; the room
	// bookkeeping alone would leave the tunnel up.
	//
	// A host leaving ends the room there and then (D70), and everybody else
	// in it has to lose the room's network with it - their tickets outlive
	// the room otherwise, and the timer that would have swept them up is the
	// one that is no longer running.
	if closed {
		s.tickets.RevokeRoom(id)
		s.chat.System(id, s.nickOf(body.PlayerID)+" closed the room", s.now())
		s.chat.Drop(id)
		s.log.Info("host left, room closed", "room", id, "player", body.PlayerID)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
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
	// The session decides who this is; the body only suggests it.
	body.PlayerID = s.actor(r, body.PlayerID)
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

// moveSlot is a player picking their side by picking their seat. Slots 0-4
// are Radiant, 5-9 are Dire, and the room screen draws them that way, so
// clicking an empty seat is the whole gesture.
func (s *Server) moveSlot(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PlayerID string `json:"player_id"`
		Slot     *int   `json:"slot"`
		// Watching picks which set of seats Slot indexes. Absent means the
		// playing slots, so every client written before the gallery became a
		// destination (D79) keeps working unchanged.
		Watching bool `json:"watching"`
	}
	if !decode(w, r, &body) {
		return
	}
	// The session decides who this is; the body only suggests it.
	body.PlayerID = s.actor(r, body.PlayerID)
	if body.Slot == nil {
		writeErr(w, http.StatusBadRequest, "slot is required")
		return
	}
	id := r.PathValue("id")
	if err := s.rooms.Move(id, body.PlayerID, *body.Slot, body.Watching); err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	// Nothing is revoked here any more (D74). A player's address belongs to
	// them for as long as they are in the room, so the ticket they are holding
	// still names the address they still have, and the tunnel they are on
	// stays up. This used to revoke, which meant picking a side dropped you
	// off the room's network and rebuilt it from the handshake up.
	s.log.Info("player changed seat", "room", id, "player", body.PlayerID,
		"seat", *body.Slot, "watching", body.Watching)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "slot": *body.Slot, "watching": body.Watching})
}

func (s *Server) setStatus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PlayerID string `json:"player_id"`
		Status   string `json:"status"`
	}
	if !decode(w, r, &body) {
		return
	}
	// The session decides who this is; the body only suggests it.
	body.PlayerID = s.actor(r, body.PlayerID)
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

// renewLease is what the net-service watchdog calls, every thirty seconds,
// for every player in a room. A ticket that renews is still authorised;
// anything else means tear the tunnel down.
//
// It answers 200 with valid:false rather than an error status on purpose. The
// watchdog treats any non-200 as "cannot tell" and fails closed after three
// minutes, so a status code here would turn a clear no into a slow one.
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
	case errors.Is(err, room.ErrRoomFull),
		errors.Is(err, room.ErrSlotTaken):
		return http.StatusConflict
	case errors.Is(err, room.ErrAlreadyJoined):
		return http.StatusConflict
	}
	if code, ok := doorStatus(err); ok {
		return code
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
