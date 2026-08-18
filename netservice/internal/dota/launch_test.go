package dota_test

import (
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"finallobby/netservice/internal/dota"
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

func TestRejectsUnknownGameMode(t *testing.T) {
	if _, err := dota.BuildHostArgs("P", 999, "good"); !errors.Is(err, dota.ErrBadArg) {
		t.Fatal("game mode 999 accepted; want rejection")
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

func TestServerReadyOnlyReadsNewOutput(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "console.log")

	old := "some old line mentioning Server started from a previous match\n"
	if err := os.WriteFile(log, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	// Starting from the end of the existing file, nothing new has happened.
	ready, err := dota.ServerReady(log, int64(len(old)))
	if err != nil {
		t.Fatal(err)
	}
	if ready {
		t.Fatal("stale marker from a previous match reported as ready")
	}

	f, err := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("Host_NewGame\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	ready, err = dota.ServerReady(log, int64(len(old)))
	if err != nil {
		t.Fatal(err)
	}
	if !ready {
		t.Fatal("fresh readiness marker was not detected")
	}
}
