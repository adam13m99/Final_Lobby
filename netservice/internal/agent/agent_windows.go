//go:build windows

// Package agent is the service's brain: it owns the adapter, the tunnel, the
// lease watchdog and the Dota process, and answers IPC commands.
//
// Everything privileged lives here, behind a small command surface. The
// desktop client asks for a room; it never gets to say which executable to
// run or which address to route.
package agent

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"lobbybaz/netservice/internal/adapter"
	"lobbybaz/netservice/internal/dota"
	"lobbybaz/protocol/ipc"
	"lobbybaz/netservice/internal/tunnel"
	"lobbybaz/netservice/internal/watchdog"
)

// adapterName is the interface the player sees in their network settings.
const adapterName = "LobbyBaz"

// Agent holds all live state. One room at a time.
type Agent struct {
	log *slog.Logger

	mu       sync.Mutex
	adapter  *adapter.Adapter
	client   *tunnel.Client
	cancel   context.CancelFunc
	roomID   string
	vip      netip.Addr
	teardown string
}

func New(log *slog.Logger) *Agent {
	if log == nil {
		log = slog.Default()
	}
	return &Agent{log: log}
}

// Handle answers one IPC request.
func (a *Agent) Handle(ctx context.Context, req ipc.Request) ipc.Response {
	switch req.Op {
	case ipc.OpPing:
		return ipc.Response{Version: "network-core"}
	case ipc.OpStatus:
		return a.status()
	case ipc.OpConnect:
		return a.connect(ctx, req)
	case ipc.OpDisconnect:
		a.Disconnect("client asked")
		return ipc.Response{Connected: false, State: "disconnected"}
	case ipc.OpLaunch:
		return a.launch(req)
	default:
		return ipc.Response{Err: "unknown operation " + req.Op}
	}
}

func (a *Agent) status() ipc.Response {
	a.mu.Lock()
	defer a.mu.Unlock()

	resp := ipc.Response{RoomID: a.roomID, State: "idle"}
	if _, err := dota.FindInstall(); err == nil {
		resp.DotaFound = true
	}
	if a.adapter != nil {
		resp.AdapterName = a.adapter.Name()
	}
	if a.client != nil {
		resp.State = a.client.State().String()
		resp.Connected = a.client.State() == tunnel.StateConnected
		if ip := a.client.VirtualIP(); ip.IsValid() {
			resp.VirtualIP = ip.String()
		}
	}
	if a.teardown != "" {
		resp.Err = a.teardown
	}
	return resp
}

// connect brings the adapter up and starts the tunnel.
func (a *Agent) connect(parent context.Context, req ipc.Request) ipc.Response {
	vip, err := netip.ParseAddr(req.VirtualIP)
	if err != nil {
		return ipc.Response{Err: "bad virtual_ip: " + err.Error()}
	}
	subnet, err := netip.ParsePrefix(req.Subnet)
	if err != nil {
		return ipc.Response{Err: "bad subnet: " + err.Error()}
	}
	if err := adapter.ValidateAssignment(vip, subnet); err != nil {
		return ipc.Response{Err: err.Error()}
	}
	pub, err := hex.DecodeString(req.RelayPub)
	if err != nil || len(pub) != 32 {
		return ipc.Response{Err: "relay_pub must be 64 hex characters"}
	}
	if req.Ticket == "" || req.RelayAddr == "" {
		return ipc.Response{Err: "ticket and relay_addr are required"}
	}

	// Switching rooms tears the old one down first, so a player can never
	// hold two rooms' addresses at once.
	a.Disconnect("joining another room")

	a.mu.Lock()
	defer a.mu.Unlock()

	dev, err := adapter.Open(adapterName, adapter.MTU)
	if err != nil {
		return ipc.Response{Err: "could not create the network adapter: " + err.Error()}
	}
	if err := dev.Configure(vip, subnet); err != nil {
		_ = dev.Close()
		return ipc.Response{Err: "could not configure the adapter: " + err.Error()}
	}

	ctx, cancel := context.WithCancel(parent)
	client := tunnel.New(tunnel.Config{
		RelayAddr: req.RelayAddr,
		RelayPub:  pub,
		Ticket:    []byte(req.Ticket),
		Adapter:   dev,
		Logger:    a.log,
	})

	a.adapter = dev
	a.client = client
	a.cancel = cancel
	a.roomID = req.RoomID
	a.vip = vip
	a.teardown = ""

	go func() {
		err := client.Run(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			a.log.Warn("tunnel stopped", "err", err)
			if errors.Is(err, tunnel.ErrRevoked) {
				a.Disconnect("your access to this room was revoked")
			}
		}
	}()

	// The watchdog is what makes revocation real. It runs here, in the
	// service, so closing the desktop app cannot keep a revoked player
	// connected.
	if req.Coordinator != "" {
		wd := watchdog.New(
			leaseChecker(req.Coordinator, req.Ticket, req.AuthToken),
			30*time.Second,
			3*time.Minute,
			func(reason string) { a.Disconnect(reason) },
		)
		go wd.Run(ctx)
	}

	a.log.Info("connected", "room", req.RoomID, "vip", vip, "adapter", dev.Name())
	return ipc.Response{State: "connecting", VirtualIP: vip.String(), RoomID: req.RoomID,
		AdapterName: dev.Name()}
}

// Disconnect tears everything down. Safe to call when already idle.
func (a *Agent) Disconnect(reason string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	if a.adapter != nil {
		if err := a.adapter.Close(); err != nil {
			a.log.Warn("adapter close failed", "err", err)
		}
		a.adapter = nil
	}
	if a.client != nil {
		a.client = nil
		a.log.Info("disconnected", "room", a.roomID, "reason", reason)
	}
	a.roomID = ""
	a.vip = netip.Addr{}
	if reason != "client asked" && reason != "joining another room" {
		a.teardown = reason
	}
}

// launch validates a Dota 2 launch and returns the exact command to run.
//
// It deliberately does NOT start the process. The service runs as LocalSystem
// in session 0, which has no desktop and no GPU: a game started from here
// fails with "No display adapters found - failed to initialize video".
// Verified on 2026-08-19; this is Windows session isolation, not a bug we
// can configure away.
//
// The client runs in the player's own session and starts the process there.
// Nothing is lost by moving it: launching a game needs no privileges, and
// the argument allowlist still runs here, on this side, where the untrusted
// inputs - the room's host address, another player's nickname - are checked
// before they can reach a command line.
func (a *Agent) launch(req ipc.Request) ipc.Response {
	a.mu.Lock()
	connected := a.client != nil && a.client.State() == tunnel.StateConnected
	vip := a.vip
	a.mu.Unlock()

	if !connected {
		return ipc.Response{Err: "not connected to a room yet"}
	}

	exe, err := dota.FindInstall()
	if err != nil {
		return ipc.Response{Err: err.Error()}
	}

	var args []string
	switch req.Role {
	case "host":
		args, err = dota.BuildHostArgs(req.Nick, req.GameMode, teamOr(req.Team))
	case "client":
		hostIP, perr := netip.ParseAddr(req.HostIP)
		if perr != nil {
			return ipc.Response{Err: "bad host_ip: " + perr.Error()}
		}
		if hostIP == vip {
			return ipc.Response{Err: "you are the host; launch with role=host"}
		}
		args, err = dota.BuildClientArgs(req.Nick, hostIP, teamOr(req.Team))
	default:
		return ipc.Response{Err: "role must be host or client"}
	}
	if err != nil {
		return ipc.Response{Err: err.Error()}
	}

	// -condebug makes Dota write console.log, which is how readiness is
	// detected.
	args = append(args, "-condebug")
	if err := dota.ValidateArgs(args); err != nil {
		return ipc.Response{Err: err.Error()}
	}

	a.log.Info("prepared dota launch", "role", req.Role, "exe", exe, "args", strings.Join(args, " "))
	return ipc.Response{DotaPath: exe, Args: args}
}

func teamOr(t string) string {
	if t == "" {
		return "good"
	}
	return t
}

func parentDir(p string) string {
	if i := strings.LastIndexAny(p, `\/`); i > 0 {
		return p[:i]
	}
	return "."
}

// leaseChecker asks the coordinator whether our ticket is still good.
func leaseChecker(coordinator, ticket, authToken string) watchdog.Checker {
	client := &http.Client{Timeout: 5 * time.Second}
	url := strings.TrimRight(coordinator, "/") + "/v1/lease/renew"

	return func(ctx context.Context) (watchdog.Verdict, error) {
		body := strings.NewReader(fmt.Sprintf(`{"ticket":%q}`, ticket))
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
		if err != nil {
			return watchdog.VerdictUnreachable, err
		}
		req.Header.Set("Content-Type", "application/json")
		if authToken != "" {
			req.Header.Set("Authorization", "Bearer "+authToken)
		}

		resp, err := client.Do(req)
		if err != nil {
			return watchdog.VerdictUnreachable, err
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusUnauthorized {
			// The API is token-gated during the test phase; without a token
			// we cannot tell valid from revoked, so we must not claim valid.
			return watchdog.VerdictUnreachable, errors.New("lease check unauthorised")
		}
		if resp.StatusCode != http.StatusOK {
			return watchdog.VerdictUnreachable, fmt.Errorf("lease check: %s", resp.Status)
		}

		var out struct {
			Valid bool `json:"valid"`
		}
		if err := decodeJSON(resp.Body, &out); err != nil {
			return watchdog.VerdictUnreachable, err
		}
		if !out.Valid {
			return watchdog.VerdictRevoked, nil
		}
		return watchdog.VerdictValid, nil
	}
}
