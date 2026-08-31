package dota_test

import (
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"lobbybaz/netservice/internal/dota"
	"lobbybaz/protocol/gamemode"
)

func TestBuildHostArgs(t *testing.T) {
	args, err := dota.BuildHostArgs("Player1", 1, "good")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"+name Player1", "+sv_lan 1", "+map dota", "gamemode 1", "+jointeam good"} {
		if !strings.Contains(joined, want) {
			t.Errorf("host args missing %q; got %q", want, joined)
		}
	}
	if err := dota.ValidateArgs(args); err != nil {
		t.Errorf("generated host args failed our own validation: %v", err)
	}
}

func TestBuildClientArgs(t *testing.T) {
	args, err := dota.BuildClientArgs("Player2", netip.MustParseAddr("10.87.0.2"), "bad")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "+connect 10.87.0.2:27015") {
		t.Errorf("client args missing connect target; got %q", joined)
	}
	if err := dota.ValidateArgs(args); err != nil {
		t.Errorf("generated client args failed validation: %v", err)
	}
}

func TestRejectsConnectOutsideOurAddressSpace(t *testing.T) {
	_, err := dota.BuildClientArgs("P", netip.MustParseAddr("192.168.1.10"), "good")
	if !errors.Is(err, dota.ErrBadArg) {
		t.Fatalf("err = %v, want ErrBadArg for a non-10.87 address", err)
	}
}

func TestRejectsUnknownArgumentKeys(t *testing.T) {
	err := dota.ValidateArgs([]string{"+exec", "evil.cfg"})
	if !errors.Is(err, dota.ErrBadArg) {
		t.Fatalf("err = %v, want ErrBadArg for +exec", err)
	}
}

func TestRejectsInjectedNickname(t *testing.T) {
	for _, bad := range []string{`a" +exec evil`, "a\\b", strings.Repeat("x", 33), ""} {
		if _, err := dota.BuildHostArgs(bad, 1, "good"); !errors.Is(err, dota.ErrBadArg) {
			t.Errorf("nickname %q accepted; want rejection", bad)
		}
	}
}

func TestRejectsBadTeam(t *testing.T) {
	if _, err := dota.BuildHostArgs("P", 1, "neutral"); !errors.Is(err, dota.ErrBadArg) {
		t.Fatal("team 'neutral' accepted; want rejection")
	}
}

// A host who is watching their own room still runs the listen server, and
// goes into it on Dota's spectator side (D81). That is what leaves all ten
// playing slots for the ten people who came to play: without it the host
// occupies one of them to host a match they are not in.
func TestAWatchingHostStartsTheMatchAsASpectator(t *testing.T) {
	args, err := dota.BuildHostArgs("Arman", 23, "spec")
	if err != nil {
		t.Fatalf("a host cannot start their own room as a spectator: %v", err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"+sv_lan 1", "+map dota", "gamemode 23", "+jointeam spec"} {
		if !strings.Contains(joined, want) {
			t.Errorf("host args missing %q; got %q", want, joined)
		}
	}
	if err := dota.ValidateArgs(args); err != nil {
		t.Errorf("a spectating host's args failed our own validation: %v", err)
	}
	// And a watcher who is not the host joins the same way.
	cargs, err := dota.BuildClientArgs("Bob", netip.MustParseAddr("10.87.0.2"), "spec")
	if err != nil {
		t.Fatalf("a watcher cannot join to spectate: %v", err)
	}
	if joined := strings.Join(cargs, " "); !strings.Contains(joined, "+jointeam spec") {
		t.Errorf("a watcher's args do not put them on the spectator side; got %q", joined)
	}
}

func TestRejectsUnknownGameMode(t *testing.T) {
	if _, err := dota.BuildHostArgs("P", 999, "good"); !errors.Is(err, dota.ErrBadArg) {
		t.Fatal("game mode 999 accepted; want rejection")
	}
}

// Every mode the interface offers must survive the command-line builder and
// the second gate behind it (D80).
//
// The list used to be a map in launch.go and a second copy of it lived in the
// interface, so a mode could be offered in the host's menu and rejected here,
// and the only way to find out was a host pressing Start Game and being told
// "rejected argument". There is one list now. This test is what stops a second
// one growing back: put any allowlist of mode IDs in BuildHostArgs and it
// fails with every mode that list forgot, by name.
func TestEveryModeInTheMenuCanActuallyBeLaunched(t *testing.T) {
	for _, m := range gamemode.Modes {
		args, err := dota.BuildHostArgs("Player1", m.ID, "good")
		if err != nil {
			t.Errorf("%s (%d) is in the menu but will not build: %v", m.Name, m.ID, err)
			continue
		}
		want := "gamemode " + strconv.Itoa(m.ID)
		if joined := strings.Join(args, " "); !strings.Contains(joined, want) {
			t.Errorf("%s (%d) built %q, which does not contain %q", m.Name, m.ID, joined, want)
		}
		if err := dota.ValidateArgs(args); err != nil {
			t.Errorf("%s (%d) built args our own validator refuses: %v", m.Name, m.ID, err)
		}
		if name, ok := dota.GameModeName(m.ID); !ok || name != m.Name {
			t.Errorf("GameModeName(%d) = %q, %v; want %q", m.ID, name, ok, m.Name)
		}
	}
}

func TestValidateExePathRequiresDota2Exe(t *testing.T) {
	if err := dota.ValidateExePath(`C:\Games\notdota.exe`); !errors.Is(err, dota.ErrBadPath) {
		t.Fatalf("err = %v, want ErrBadPath", err)
	}
	// A file genuinely named dota2.exe but sitting outside a Dota tree must
	// still be refused - the name alone proves nothing.
	dir := t.TempDir()
	fake := filepath.Join(dir, "dota2.exe")
	if err := os.WriteFile(fake, []byte("not really dota"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := dota.ValidateExePath(fake); !errors.Is(err, dota.ErrBadPath) {
		t.Fatalf("err = %v, want ErrBadPath for dota2.exe outside a dota 2 beta tree", err)
	}
}

// TestFindInstallOnThisMachine runs against the real Steam installation.
// Both test machines have Dota 2 installed, so a failure here is a real
// failure, not a missing fixture.
func TestFindInstallOnThisMachine(t *testing.T) {
	path, err := dota.FindInstall()
	if err != nil {
		t.Skipf("Dota 2 not found on this machine: %v", err)
	}
	t.Logf("found Dota 2 at %s", path)
	if err := dota.ValidateExePath(path); err != nil {
		t.Fatalf("discovered path failed validation: %v", err)
	}
}

// realLogPrologue is copied verbatim from a Dota 2 console.log on a test
// machine: the main menu spins up its own server before any match exists.
// Treating that as "ready" would tell a joining player to connect before the
// host had a game.
const realLogPrologue = `05/08 21:30:20 [Server] CNetworkGameServerBase::SetServerState (ss_dead -> ss_waitingforgamesessionmanifest)
05/08 21:30:20 [Networking] Network socket 'server' opened on port 27015
05/08 21:32:35 [Server] SV:  Spawn Server: <empty>
05/08 21:32:35 [Server] CNetworkGameServerBase::SetServerState (ss_waitingforgamesessionmanifest -> ss_loading)
05/08 21:32:35 [Server] CNetworkGameServerBase::SetServerState (ss_loading -> ss_active)
`

// realLogMatchStart is the sequence a real match produces.
const realLogMatchStart = `05/08 21:30:24 [Server] SV:  Spawn Server: dota
05/08 21:30:24 [Server] CNetworkGameServerBase::SetServerState (ss_waitingforgamesessionmanifest -> ss_loading)
05/08 21:30:24 [Server] CNetworkGameServerBase::SetServerState (ss_loading -> ss_active)
05/08 21:30:24 [Client] CL:  CWaitForGameServerStartupPrerequisite done waiting for server
`

func TestServerReadyIgnoresTheMainMenuServer(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "console.log")
	if err := os.WriteFile(log, []byte(realLogPrologue), 0o600); err != nil {
		t.Fatal(err)
	}
	ready, err := dota.ServerReady(log, 0)
	if err != nil {
		t.Fatal(err)
	}
	if ready {
		t.Fatal("the main menu's own server was reported as a ready match; " +
			"a joining player would be sent to a game that does not exist yet")
	}
}

func TestServerReadyDetectsARealMatch(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "console.log")
	if err := os.WriteFile(log, []byte(realLogPrologue+realLogMatchStart), 0o600); err != nil {
		t.Fatal(err)
	}
	ready, err := dota.ServerReady(log, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !ready {
		t.Fatal("a real match start was not detected")
	}
}

func TestServerReadyOnlyReadsNewOutput(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "console.log")

	// A previous match left its markers in the log.
	old := realLogPrologue + realLogMatchStart
	if err := os.WriteFile(log, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	ready, err := dota.ServerReady(log, int64(len(old)))
	if err != nil {
		t.Fatal(err)
	}
	if ready {
		t.Fatal("markers from a previous match reported as ready")
	}

	f, err := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(realLogMatchStart); err != nil {
		t.Fatal(err)
	}
	f.Close()

	ready, err = dota.ServerReady(log, int64(len(old)))
	if err != nil {
		t.Fatal(err)
	}
	if !ready {
		t.Fatal("a fresh match start after the offset was not detected")
	}
}

func TestServerPortIsReadFromTheLogNotAssumed(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "console.log")
	if err := os.WriteFile(log, []byte(realLogPrologue), 0o600); err != nil {
		t.Fatal(err)
	}
	port, err := dota.ServerPort(log, 0)
	if err != nil {
		t.Fatal(err)
	}
	if port != 27015 {
		t.Fatalf("port = %d, want 27015", port)
	}

	// If Dota ever opens a different port, we must read it rather than keep
	// sending clients to 27015.
	moved := strings.Replace(realLogPrologue, "port 27015", "port 27016", 1)
	if err := os.WriteFile(log, []byte(moved), 0o600); err != nil {
		t.Fatal(err)
	}
	port, err = dota.ServerPort(log, 0)
	if err != nil {
		t.Fatal(err)
	}
	if port != 27016 {
		t.Fatalf("port = %d, want 27016 - the port must come from the log", port)
	}
}

// The two halves of a command line are gated differently on purpose: a
// player's own engine flags are open-ended, console commands are not. These
// two tests are that rule, from the side that enforces it (D65).
func TestAcceptsAPlayersOwnEngineFlags(t *testing.T) {
	args := []string{"+name", "arman", "+sv_lan", "1", "-condebug",
		"-novid", "-high", "-nod3d9ex", "-language", "english"}
	if err := dota.ValidateArgs(args); err != nil {
		t.Fatalf("ValidateArgs rejected ordinary player flags: %v", err)
	}
}

func TestStillRejectsConsoleCommandsAmongPlayerFlags(t *testing.T) {
	args := []string{"+name", "arman", "-novid", "+connect", "10.87.0.2:27015"}
	// +connect is on the allowlist, because we build it ourselves.
	if err := dota.ValidateArgs(args); err != nil {
		t.Fatalf("ValidateArgs = %v, want nil", err)
	}
	args = []string{"+name", "arman", "-novid", "+exec", "evil.cfg"}
	if err := dota.ValidateArgs(args); !errors.Is(err, dota.ErrBadArg) {
		t.Fatalf("err = %v, want ErrBadArg for +exec beside a player flag", err)
	}
}
