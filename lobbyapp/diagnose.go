package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"lobbybaz/client/build"
	"lobbybaz/client/lobby"
	"lobbybaz/client/session"
	"lobbybaz/protocol/ipc"
)

// Diagnostics replace the acceptance checklist that a person used to fill in
// by hand on two machines.
//
// Every check below was previously a numbered case asking someone to read a
// value off a screen and write it down. Reading numbers aloud over a
// telephone is slow and it is where mistakes get introduced; the machine
// knows the answers, so it should report them. The results go to the server
// as well as the screen, so both machines' runs can be compared side by side
// from one place.
//
// What is deliberately NOT here: whether a real Dota 2 match ran. No check
// can answer that, and pretending otherwise would be the worst kind of green
// tick. That stays a human observation.

func (s *server) diagnose(w http.ResponseWriter, r *http.Request) {
	s.diagMu.Lock()
	if s.diagBusy {
		s.diagMu.Unlock()
		fail(w, "A check is already running.")
		return
	}
	s.diagBusy = true
	s.diagMu.Unlock()

	go func() {
		checks := s.runChecks()
		// Print it too, so somebody watching the app's own window sees the
		// outcome without switching to the browser.
		fmt.Println("Diagnostics:", summarise(checks))
		s.diagMu.Lock()
		s.diagLast, s.diagAt, s.diagBusy = checks, time.Now(), false
		s.diagMu.Unlock()

		cfg := s.snapshot()
		host, _ := os.Hostname()
		if err := s.api().SendDiag(lobby.DiagReport{
			PlayerID: cfg.PlayerID,
			Machine:  host,
			Version:  build.Version,
			Checks:   checks,
		}); err != nil {
			// Failing to upload is not a failure of the test. Say so on the
			// screen and keep the local results.
			s.diagMu.Lock()
			s.diagLast = append(s.diagLast, lobby.DiagCheck{
				Name:   "Results sent to the server",
				OK:     false,
				Detail: err.Error(),
			})
			s.diagMu.Unlock()
		}
	}()

	ok(w)
}

func (s *server) runChecks() []lobby.DiagCheck {
	cfg := s.snapshot()
	var out []lobby.DiagCheck

	out = append(out, s.checkCoordinator(cfg))
	svc, status := s.checkService()
	out = append(out, svc)
	out = append(out, s.checkDota(status))

	if cfg.RoomID == "" {
		out = append(out, lobby.DiagCheck{
			Name:   "In a room",
			OK:     false,
			Detail: "Not in a room, so the tunnel and the other player could not be tested. Join a room and run this again.",
		})
		return out
	}
	out = append(out, lobby.DiagCheck{Name: "In a room", OK: true, Detail: cfg.RoomID})
	out = append(out, s.checkTunnel(status))

	if !status.Connected {
		out = append(out, lobby.DiagCheck{
			Name:   "Reached the other player",
			OK:     false,
			Detail: "The tunnel is not up, so there was nothing to reach. Press Connect and run this again.",
		})
		return out
	}
	out = append(out, s.checkReach(cfg)...)
	return out
}

func (s *server) checkCoordinator(cfg *session.Config) lobby.DiagCheck {
	start := time.Now()
	_, err := s.api().Sync(lobby.SyncRequest{PlayerID: cfg.PlayerID, Nick: cfg.Nick})
	ms := int(time.Since(start).Milliseconds())
	if err != nil {
		return lobby.DiagCheck{Name: "Server reachable", OK: false, Detail: err.Error(), Millis: ms}
	}
	return lobby.DiagCheck{
		Name:   "Server reachable",
		OK:     true,
		Detail: cfg.Coordinator,
		Millis: ms,
	}
}

func (s *server) checkService() (lobby.DiagCheck, ipc.Response) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	resp, err := ipc.Call(ctx, ipc.Request{Op: ipc.OpStatus})
	ms := int(time.Since(start).Milliseconds())
	if err != nil {
		return lobby.DiagCheck{
			Name:   "Network service running",
			OK:     false,
			Detail: "Not answering. Reinstall the app from the download link.",
			Millis: ms,
		}, ipc.Response{}
	}
	return lobby.DiagCheck{Name: "Network service running", OK: true, Millis: ms}, resp
}

func (s *server) checkDota(status ipc.Response) lobby.DiagCheck {
	if status.DotaFound {
		return lobby.DiagCheck{Name: "Dota 2 installed", OK: true}
	}
	return lobby.DiagCheck{
		Name:   "Dota 2 installed",
		OK:     false,
		Detail: "The service could not find Dota 2. Install it through Steam and start it once.",
	}
}

func (s *server) checkTunnel(status ipc.Response) lobby.DiagCheck {
	if status.Connected {
		return lobby.DiagCheck{
			Name:   "Tunnel connected",
			OK:     true,
			Detail: fmt.Sprintf("%s on %s", status.VirtualIP, status.AdapterName),
		}
	}
	detail := "Not connected. Press Connect."
	if status.Err != "" {
		detail = status.Err
	}
	return lobby.DiagCheck{Name: "Tunnel connected", OK: false, Detail: detail}
}

// checkReach pings the other end and, when this machine is not the host,
// times a larger packet as well. Two sizes matter: a small one proves the
// path exists, and a game-sized one proves the path carries what Dota will
// actually put through it.
func (s *server) checkReach(cfg *session.Config) []lobby.DiagCheck {
	if cfg.IsHost {
		return []lobby.DiagCheck{{
			Name:   "Reached the other player",
			OK:     true,
			Detail: "This machine is the host, so there is nobody upstream to reach. Run this on the other PC.",
		}}
	}
	if cfg.HostIP == "" {
		return []lobby.DiagCheck{{
			Name: "Reached the other player", OK: false, Detail: "No host address known.",
		}}
	}

	small := pingCheck("Reached the host", cfg.HostIP, 32, 4)
	if !small.OK {
		return []lobby.DiagCheck{small}
	}
	return []lobby.DiagCheck{small, pingCheck("Carried a game-sized packet", cfg.HostIP, 900, 4)}
}

var pingStats = regexp.MustCompile(`(?i)Average\s*=\s*(\d+)ms`)
var pingLoss = regexp.MustCompile(`\((\d+)%\s*loss\)`)

// pingCheck shells out to Windows' own ping. Sending ICMP from Go needs a
// raw socket and therefore Administrator, and requiring that would undo the
// whole point of the one-time install.
func pingCheck(name, addr string, size, count int) lobby.DiagCheck {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "ping.exe",
		"-n", strconv.Itoa(count),
		"-l", strconv.Itoa(size),
		"-w", "2000",
		addr,
	).CombinedOutput()
	text := string(out)

	loss := 100
	if m := pingLoss.FindStringSubmatch(text); m != nil {
		loss, _ = strconv.Atoi(m[1])
	}
	avg := 0
	if m := pingStats.FindStringSubmatch(text); m != nil {
		avg, _ = strconv.Atoi(m[1])
	}

	if err != nil && loss == 100 {
		return lobby.DiagCheck{
			Name:   name,
			OK:     false,
			Detail: fmt.Sprintf("No reply from %s. The other player may not be connected yet.", addr),
		}
	}
	// The first packet after a tunnel comes up is reliably lost while the
	// route settles, so a single loss out of four is expected rather than a
	// fault. Anything worse is worth reporting.
	if loss > 25 {
		return lobby.DiagCheck{
			Name:   name,
			OK:     false,
			Detail: fmt.Sprintf("%d%% of packets to %s were lost", loss, addr),
			Millis: avg,
		}
	}
	detail := fmt.Sprintf("%s replied, %d%% loss", addr, loss)
	if size > 100 {
		detail = fmt.Sprintf("%s replied to %d-byte packets, %d%% loss", addr, size, loss)
	}
	return lobby.DiagCheck{Name: name, OK: true, Detail: detail, Millis: avg}
}

// summarise is used by the log line the app prints, so somebody watching the
// black window sees the outcome without opening the browser.
func summarise(checks []lobby.DiagCheck) string {
	var failed []string
	for _, c := range checks {
		if !c.OK {
			failed = append(failed, c.Name)
		}
	}
	if len(failed) == 0 {
		return fmt.Sprintf("all %d checks passed", len(checks))
	}
	return fmt.Sprintf("%d of %d checks failed: %s",
		len(failed), len(checks), strings.Join(failed, ", "))
}
