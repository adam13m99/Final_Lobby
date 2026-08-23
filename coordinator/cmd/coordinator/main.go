// Command coordinator is the control plane: it owns rooms, allocates virtual
// addresses, and issues the tickets the relay checks.
//
// Accounts, declared MMR and kick history live in a SQLite file (D51) named
// by -db. Without that flag the coordinator runs exactly as it did during the
// two-PC test phase: no accounts, no durable profiles, and a client's claimed
// player_id taken at face value. That mode exists for the loadtest harness,
// not for players.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"lobbybaz/coordinator/internal/account"
	"lobbybaz/coordinator/internal/api"
	"lobbybaz/coordinator/internal/chat"
	"lobbybaz/coordinator/internal/player"
	"lobbybaz/coordinator/internal/room"
	"lobbybaz/coordinator/internal/social"
	"lobbybaz/coordinator/internal/store"
	"lobbybaz/coordinator/internal/ticket"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:7001", "HTTP listen address")
	relayAddr := flag.String("relay-addr", "87.107.110.199:443", "relay address given to clients")
	relayPubFile := flag.String("relay-pub", "/etc/lobbybaz/relay.pub", "file holding the relay public key")
	tickEvery := flag.Duration("tick", 10*time.Second, "how often room timers advance")
	authFile := flag.String("auth-token-file", "", "file holding the shared bearer token for the player API (empty = open)")
	distDir := flag.String("dist-dir", "", "directory holding the published installer and version.json (empty = serve no downloads)")
	dlKeyFile := flag.String("download-key-file", "", "file holding the unguessable path segment the download is served under")
	dbPath := flag.String("db", "", "SQLite file holding accounts and kick history (empty = run without accounts)")
	debug := flag.Bool("debug", false, "verbose logging")
	flag.Parse()

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)

	relayPub, err := os.ReadFile(*relayPubFile)
	if err != nil {
		log.Error("cannot read the relay public key", "file", *relayPubFile, "err", err)
		os.Exit(1)
	}
	pub := strings.TrimSpace(string(relayPub))
	if len(pub) != 64 {
		log.Error("relay public key must be 64 hex characters", "got", len(pub))
		os.Exit(1)
	}

	var authToken string
	if *authFile != "" {
		raw, err := os.ReadFile(*authFile)
		if err != nil {
			log.Error("cannot read the auth token", "file", *authFile, "err", err)
			os.Exit(1)
		}
		authToken = strings.TrimSpace(string(raw))
		if len(authToken) < 16 {
			log.Error("auth token is too short to be worth having", "len", len(authToken))
			os.Exit(1)
		}
	} else {
		log.Warn("no auth token configured - the player API is open to anyone who can reach it")
	}

	var downloadKey string
	if *dlKeyFile != "" {
		raw, err := os.ReadFile(*dlKeyFile)
		if err != nil {
			log.Error("cannot read the download key", "file", *dlKeyFile, "err", err)
			os.Exit(1)
		}
		downloadKey = strings.TrimSpace(string(raw))
		// The key is the only thing standing in front of the download, since
		// a browser cannot send a bearer token. Short enough to guess is the
		// same as absent.
		if len(downloadKey) < 12 {
			log.Error("download key is too short to be unguessable", "len", len(downloadKey))
			os.Exit(1)
		}
	}
	if *distDir != "" && downloadKey == "" {
		log.Error("a dist directory was given with no download key; refusing to publish a build at a guessable path")
		os.Exit(1)
	}

	var (
		accounts *account.Store
		friends  *social.Store
	)
	rooms := room.NewStore()
	if *dbPath != "" {
		db, err := store.Open(*dbPath)
		if err != nil {
			log.Error("cannot open the database", "file", *dbPath, "err", err)
			os.Exit(1)
		}
		defer db.Close()
		version, _ := store.Version(db)
		accounts = account.New(db)
		friends = social.New(db)

		// Every kick is written down, so a moderator can see a pattern that
		// outlives the room it happened in (D52). Recording never blocks the
		// kick: a host removing a griefer must not fail because a disk did.
		kicks := store.NewKicks(db)
		rooms.OnKick(func(e room.KickEvent) {
			if err := kicks.Record(e.RoomID, e.ActorID, e.TargetID, e.KickNumber, e.BlockedFor, e.At); err != nil {
				log.Error("could not record a kick", "room", e.RoomID, "target", e.TargetID, "err", err)
			}
		})
		log.Info("database ready", "file", *dbPath, "schema", version)
	} else {
		log.Warn("running without a database - no accounts, and a client's claimed player_id is taken at face value")
	}

	tickets := ticket.NewStore()
	players := player.NewRegistry()
	board := chat.NewBoard()

	srv := api.New(api.Config{
		Rooms:       rooms,
		Tickets:     tickets,
		Players:     players,
		Chat:        board,
		Accounts:    accounts,
		Social:      friends,
		Friends:     friendsOrNil(friends),
		RelayAddr:   *relayAddr,
		RelayPub:    pub,
		Logger:      log,
		AuthToken:   authToken,
		DistDir:     *distDir,
		DownloadKey: downloadKey,
	})
	if *distDir != "" {
		log.Info("serving downloads", "dir", *distDir, "path", "/d/"+downloadKey+"/")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go runTimers(ctx, rooms, tickets, board, *tickEvery, log)

	httpSrv := &http.Server{
		Addr:              *listen,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	log.Info("coordinator listening", "addr", *listen, "relay", *relayAddr)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("coordinator stopped", "err", err)
		os.Exit(1)
	}
	log.Info("coordinator shut down cleanly")
}

// runTimers advances room state and clears expired tickets. Rooms close on a
// timer, so something has to be turning the handle.
func runTimers(ctx context.Context, rooms *room.Store, tickets *ticket.Store, board *chat.Board, every time.Duration, log *slog.Logger) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		now := time.Now()
		for _, id := range rooms.Tick(now) {
			// The room is over; nobody in it should keep network access.
			log.Info("room closed", "room", id)
			revokeRoom(tickets, id)
			// The conversation goes with the room. Without this every
			// finished match leaks its chat for as long as the process runs.
			board.Drop(id)
		}
		tickets.Purge(now)
	}
}

// revokeRoom drops every ticket belonging to a closed room.
func revokeRoom(tickets *ticket.Store, roomID string) {
	tickets.RevokeRoom(roomID)
}

// friendsOrNil keeps a nil *social.Store from becoming a non-nil api.Friends.
//
// A typed nil in an interface is not nil, so passing the pointer straight
// through would give the room door a Friends that panics the first time
// somebody opens a friends-only room on a coordinator running without a
// database.
func friendsOrNil(s *social.Store) api.Friends {
	if s == nil {
		return nil
	}
	return s
}
