// Command lobbycli is the throwaway test client for the network core.
//
// It is not the product - the real UI is sub-project 3. It exists so two
// people on two PCs can create a room, join it, bring the tunnel up and
// launch Dota, and so that each of those steps can be checked separately
// when one of them misbehaves.
//
//	lobbycli setup -coordinator http://host:7001 -token XXX -player alice
//	lobbycli rooms
//	lobbycli create -name "Test Room"
//	lobbycli join r123-1
//	lobbycli connect          bring the tunnel up
//	lobbycli status
//	lobbycli play             launch Dota into the current room
//	lobbycli lock / open      host only
//	lobbycli kick bob         host only
//	lobbycli leave
//	lobbycli probe            handshake against the relay without joining
package main

import (
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"finallobby/client/lobby"
	"finallobby/client/session"
	"finallobby/protocol/crypto"
	"finallobby/protocol/ipc"
	"finallobby/protocol/wire"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch cmd {
	case "setup":
		err = cmdSetup(args)
	case "rooms":
		err = cmdRooms()
	case "create":
		err = cmdCreate(args)
	case "join":
		err = cmdJoin(args)
	case "connect":
		err = cmdConnect()
	case "disconnect":
		err = cmdDisconnect()
	case "status":
		err = cmdStatus()
	case "play":
		err = cmdPlay(args)
	case "lock":
		err = cmdStatusChange("locked_in_game")
	case "open":
		err = cmdStatusChange("open_to_new_players")
	case "kick":
		err = cmdKick(args)
	case "leave":
		err = cmdLeave()
	case "probe":
		err = cmdProbe(args)
	case "help", "-h", "--help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`lobbycli - Final Lobby test client

  setup       -coordinator URL -token TOKEN -player NAME [-nick NICK]
  rooms       list open rooms
  create      [-name NAME]        create a room and take the host slot
  join ID                          join an existing room
  connect                          bring the network tunnel up
  disconnect                       take it down
  status                           show room and tunnel state
  play        [-mode N] [-team good|bad]   launch Dota 2 into this room
  lock                             host: close the room, match starting
  open                             host: reopen for a replacement player
  kick NAME                        host: remove a player for 5 minutes
  leave                            leave the room
  probe       -relay ADDR -key HEX -ticket T    raw handshake check
`)
}

// --- commands -----------------------------------------------------------

func cmdSetup(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	coordinator := fs.String("coordinator", "", "coordinator base URL, e.g. http://87.107.110.199:7001")
	token := fs.String("token", "", "shared API token")
	player := fs.String("player", "", "your player ID")
	nick := fs.String("nick", "", "in-game nickname (defaults to the player ID)")
	_ = fs.Parse(args)

	cfg, err := session.Load()
	if err != nil {
		return err
	}
	// The build already knows its server and this installation already has
	// an ID, so setup usually only has to be told a name. The flags remain
	// for pointing a developer build at something else.
	cfg.Prepare()
	if *coordinator != "" {
		cfg.Coordinator = *coordinator
	}
	if *token != "" {
		cfg.AuthToken = *token
	}
	if *player != "" {
		cfg.PlayerID = *player
	}
	if *nick != "" {
		cfg.Nick = *nick
	}
	if cfg.Coordinator == "" {
		return fmt.Errorf("this build has no server stamped into it; pass -coordinator")
	}
	if cfg.Nick == "" {
		return fmt.Errorf("pass -nick to choose the name other players see")
	}
	if err := cfg.Save(); err != nil {
		return err
	}

	// Prove it works now rather than at the worst moment.
	if _, err := lobby.New(cfg.Coordinator, cfg.AuthToken).ListRooms(); err != nil {
		return fmt.Errorf("saved, but the coordinator is not answering: %w", err)
	}
	fmt.Printf("Saved. You are %q talking to %s\n", cfg.PlayerID, cfg.Coordinator)
	return nil
}

func cmdRooms() error {
	cfg, err := session.Load()
	if err != nil {
		return err
	}
	if err := cfg.RequireLogin(); err != nil {
		return err
	}
	rooms, err := lobby.New(cfg.Coordinator, cfg.AuthToken).ListRooms()
	if err != nil {
		return err
	}
	if len(rooms) == 0 {
		fmt.Println("No open rooms. Create one with `lobbycli create`.")
		return nil
	}
	fmt.Printf("%-14s %-22s %-20s %-6s %s\n", "ROOM", "NAME", "STATUS", "FREE", "PLAYERS")
	for _, r := range rooms {
		fmt.Printf("%-14s %-22s %-20s %-6d %s\n",
			r.ID, truncate(r.Name, 22), r.Status, r.Free, strings.Join(r.Players, ", "))
	}
	return nil
}

func cmdCreate(args []string) error {
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	name := fs.String("name", "", "room name")
	_ = fs.Parse(args)

	cfg, err := session.Load()
	if err != nil {
		return err
	}
	if err := cfg.RequireLogin(); err != nil {
		return err
	}
	info, err := lobby.New(cfg.Coordinator, cfg.AuthToken).CreateRoom(cfg.PlayerID, cfg.Nick, *name)
	if err != nil {
		return err
	}
	storeRoom(cfg, info)
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Printf("Room %s created. You are the host at %s.\n", info.RoomID, info.VirtualIP)
	fmt.Println("Next: `lobbycli connect`, then `lobbycli play`.")
	return nil
}

func cmdJoin(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: lobbycli join <room-id>")
	}
	cfg, err := session.Load()
	if err != nil {
		return err
	}
	if err := cfg.RequireLogin(); err != nil {
		return err
	}
	info, err := lobby.New(cfg.Coordinator, cfg.AuthToken).JoinRoom(args[0], cfg.PlayerID, cfg.Nick)
	if err != nil {
		return err
	}
	storeRoom(cfg, info)
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Printf("Joined %s as slot %d. Your address is %s; the host is at %s.\n",
		info.RoomID, info.Slot, info.VirtualIP, info.HostIP)
	fmt.Println("Next: `lobbycli connect`, then `lobbycli play`.")
	return nil
}

func storeRoom(cfg *session.Config, info *lobby.ConnectInfo) {
	cfg.RoomID = info.RoomID
	cfg.VirtualIP = info.VirtualIP
	cfg.HostIP = info.HostIP
	cfg.Subnet = info.Subnet
	cfg.Ticket = info.Ticket
	cfg.RelayAddr = info.RelayAddr
	cfg.RelayPub = info.RelayPub
	cfg.IsHost = info.IsHost
}

func cmdConnect() error {
	cfg, err := session.Load()
	if err != nil {
		return err
	}
	if err := cfg.RequireRoom(); err != nil {
		return err
	}

	// A ticket lasts ten minutes from the moment it is issued, so the one
	// saved at join is usually dead by the time anyone connects. Take a new
	// one first.
	info, err := lobby.New(cfg.Coordinator, cfg.AuthToken).Refresh(cfg.RoomID, cfg.PlayerID)
	if err != nil {
		return err
	}
	storeRoom(cfg, info)
	if err := cfg.Save(); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
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
		return err
	}
	if resp.Err != "" {
		return fmt.Errorf("%s", resp.Err)
	}
	fmt.Printf("Adapter %q up at %s. Waiting for the tunnel...\n", resp.AdapterName, resp.VirtualIP)

	// Connecting is asynchronous; report the outcome rather than leaving the
	// user to guess.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		st, err := ipc.Call(context.Background(), ipc.Request{Op: ipc.OpStatus})
		if err != nil {
			return err
		}
		if st.Connected {
			fmt.Printf("Connected. You are %s in room %s.\n", st.VirtualIP, st.RoomID)
			return nil
		}
		if st.Err != "" {
			return fmt.Errorf("%s", st.Err)
		}
	}
	return fmt.Errorf("the tunnel did not come up within 15 seconds; try `lobbycli status`")
}

func cmdDisconnect() error {
	resp, err := ipc.Call(context.Background(), ipc.Request{Op: ipc.OpDisconnect})
	if err != nil {
		return err
	}
	if resp.Err != "" {
		return fmt.Errorf("%s", resp.Err)
	}
	fmt.Println("Disconnected.")
	return nil
}

func cmdStatus() error {
	cfg, err := session.Load()
	if err != nil {
		return err
	}

	fmt.Printf("player      %s\n", orNone(cfg.PlayerID))
	fmt.Printf("coordinator %s\n", orNone(cfg.Coordinator))
	fmt.Printf("room        %s", orNone(cfg.RoomID))
	if cfg.IsHost {
		fmt.Print("  (you are the host)")
	}
	fmt.Println()

	if cfg.RoomID != "" {
		if rv, err := lobby.New(cfg.Coordinator, cfg.AuthToken).GetRoom(cfg.RoomID); err == nil {
			fmt.Printf("room status %s\n", rv.Status)
			fmt.Printf("players     %s\n", strings.Join(rv.Players, ", "))
		}
	}

	resp, err := ipc.Call(context.Background(), ipc.Request{Op: ipc.OpStatus})
	if err != nil {
		fmt.Printf("\nservice     NOT RUNNING (%v)\n", err)
		fmt.Println("            install it with: netservice.exe install   (as Administrator)")
		return nil
	}
	fmt.Printf("\nservice     running\n")
	fmt.Printf("tunnel      %s\n", resp.State)
	fmt.Printf("adapter     %s\n", orNone(resp.AdapterName))
	fmt.Printf("your IP     %s\n", orNone(resp.VirtualIP))
	if cfg.HostIP != "" {
		fmt.Printf("host IP     %s\n", cfg.HostIP)
	}
	if resp.DotaRunning {
		fmt.Printf("dota        running\n")
	}
	if resp.Err != "" {
		fmt.Printf("last error  %s\n", resp.Err)
	}
	return nil
}

func cmdPlay(args []string) error {
	fs := flag.NewFlagSet("play", flag.ExitOnError)
	mode := fs.Int("mode", 1, "Dota game mode ID (1 = All Pick, 23 = Turbo)")
	team := fs.String("team", "good", "good, bad or spec")
	_ = fs.Parse(args)

	cfg, err := session.Load()
	if err != nil {
		return err
	}
	if err := cfg.RequireRoom(); err != nil {
		return err
	}

	req := ipc.Request{
		Op:       ipc.OpLaunch,
		Nick:     cfg.Nick,
		GameMode: *mode,
		Team:     *team,
	}
	if cfg.IsHost {
		req.Role = "host"
	} else {
		req.Role = "client"
		req.HostIP = cfg.HostIP
	}

	resp, err := ipc.Call(context.Background(), req)
	if err != nil {
		return err
	}
	if resp.Err != "" {
		return fmt.Errorf("%s", resp.Err)
	}

	// The service validates and hands back the command; we start it here.
	// A service runs in session 0, which has no desktop and no GPU, so a
	// game started there dies with "failed to initialize video".
	cmd := exec.Command(resp.DotaPath, resp.Args...)
	cmd.Dir = filepath.Dir(resp.DotaPath)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not start Dota 2: %w", err)
	}
	_ = cmd.Process.Release()

	fmt.Printf("Launched Dota 2 as %s.\n", req.Role)
	fmt.Printf("  %s\n  %s\n", resp.DotaPath, strings.Join(resp.Args, " "))
	if cfg.IsHost {
		fmt.Println("\nWhen the match has loaded, run `lobbycli lock` so nobody joins mid-game.")
	}
	return nil
}

func cmdStatusChange(status string) error {
	cfg, err := session.Load()
	if err != nil {
		return err
	}
	if err := cfg.RequireRoom(); err != nil {
		return err
	}
	if err := lobby.New(cfg.Coordinator, cfg.AuthToken).SetStatus(cfg.RoomID, cfg.PlayerID, status); err != nil {
		return err
	}
	fmt.Printf("Room %s is now %s.\n", cfg.RoomID, status)
	return nil
}

func cmdKick(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: lobbycli kick <player>")
	}
	cfg, err := session.Load()
	if err != nil {
		return err
	}
	if err := cfg.RequireRoom(); err != nil {
		return err
	}
	if err := lobby.New(cfg.Coordinator, cfg.AuthToken).Kick(cfg.RoomID, cfg.PlayerID, args[0]); err != nil {
		return err
	}
	fmt.Printf("%s removed. They cannot rejoin for 5 minutes.\n", args[0])
	return nil
}

func cmdLeave() error {
	cfg, err := session.Load()
	if err != nil {
		return err
	}
	if err := cfg.RequireRoom(); err != nil {
		return err
	}
	if err := lobby.New(cfg.Coordinator, cfg.AuthToken).LeaveRoom(cfg.RoomID, cfg.PlayerID); err != nil {
		return err
	}
	// Drop the tunnel too. Leaving the room but keeping the adapter up is
	// exactly the kind of half-state that confuses people.
	if _, err := ipc.Call(context.Background(), ipc.Request{Op: ipc.OpDisconnect}); err != nil {
		fmt.Fprintf(os.Stderr, "note: could not reach the service to disconnect: %v\n", err)
	}
	room := cfg.RoomID
	cfg.ClearRoom()
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Printf("Left %s.\n", room)
	return nil
}

// cmdProbe is the raw handshake check from before the coordinator existed.
// It stays because when something is broken it answers "is the relay itself
// reachable and healthy" without any other moving part.
func cmdProbe(args []string) error {
	fs := flag.NewFlagSet("probe", flag.ExitOnError)
	relayAddr := fs.String("relay", "", "relay host:port")
	pubHex := fs.String("key", "", "relay static public key, hex")
	tkt := fs.String("ticket", "", "session ticket")
	count := fs.Int("count", 3, "attempts")
	timeout := fs.Duration("timeout", 5*time.Second, "per-attempt timeout")
	_ = fs.Parse(args)

	cfg, _ := session.Load()
	if *relayAddr == "" && cfg != nil {
		*relayAddr = cfg.RelayAddr
	}
	if *pubHex == "" && cfg != nil {
		*pubHex = cfg.RelayPub
	}
	if *tkt == "" && cfg != nil {
		*tkt = cfg.Ticket
	}
	if *relayAddr == "" || *pubHex == "" || *tkt == "" {
		return fmt.Errorf("need -relay, -key and -ticket (or join a room first)")
	}
	pub, err := hex.DecodeString(*pubHex)
	if err != nil || len(pub) != 32 {
		return fmt.Errorf("-key must be 64 hex characters")
	}

	var ok int
	for i := 1; i <= *count; i++ {
		rtt, accept, err := probeOnce(*relayAddr, pub, []byte(*tkt), *timeout)
		if err != nil {
			fmt.Printf("attempt %d: FAILED after %v: %v\n", i, rtt.Round(time.Millisecond), err)
			continue
		}
		ok++
		fmt.Printf("attempt %d: ok in %v - session %d, virtual IP %s, room %q\n",
			i, rtt.Round(time.Millisecond), accept.SessionID, accept.VirtualIP, accept.RoomID)
	}
	fmt.Printf("\n%d/%d handshakes succeeded against %s\n", ok, *count, *relayAddr)
	if ok == 0 {
		return fmt.Errorf("the relay did not answer")
	}
	return nil
}

func probeOnce(addr string, relayPub, tkt []byte, timeout time.Duration) (time.Duration, wire.Accept, error) {
	start := time.Now()
	conn, err := net.Dial("udp", addr)
	if err != nil {
		return time.Since(start), wire.Accept{}, err
	}
	defer conn.Close()

	msg1, finish, err := crypto.ClientHandshake(relayPub, tkt)
	if err != nil {
		return time.Since(start), wire.Accept{}, err
	}
	hdr := make([]byte, wire.HeaderSize)
	wire.EncodeHeader(hdr, wire.Header{Version: wire.ProtocolVersion, Type: wire.TypeHandshakeInit})
	if _, err := conn.Write(append(hdr, msg1...)); err != nil {
		return time.Since(start), wire.Accept{}, err
	}

	buf := make([]byte, 2048)
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	n, err := conn.Read(buf)
	if err != nil {
		return time.Since(start), wire.Accept{}, fmt.Errorf("no reply: %w", err)
	}
	rtt := time.Since(start)

	h, err := wire.DecodeHeader(buf[:n])
	if err != nil {
		return rtt, wire.Accept{}, err
	}
	if h.Type != wire.TypeHandshakeResp {
		return rtt, wire.Accept{}, fmt.Errorf("unexpected reply type %d", h.Type)
	}
	_, payload, err := finish(buf[wire.HeaderSize:n])
	if err != nil {
		return rtt, wire.Accept{}, err
	}
	accept, err := wire.DecodeAccept(payload)
	if err != nil {
		return rtt, wire.Accept{}, err
	}
	return rtt, accept, nil
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
