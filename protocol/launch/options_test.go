package launch_test

import (
	"errors"
	"strings"
	"testing"

	"lobbybaz/protocol/launch"
)

func TestTheOrdinaryCasePassesThrough(t *testing.T) {
	got, err := launch.Options("  -console  -novid -high ")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-console", "-novid", "-high"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestFlagsMayCarryValues(t *testing.T) {
	got, err := launch.Options("-w 1920 -h 1080 -language english")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 6 {
		t.Fatalf("got %v, want six tokens", got)
	}
}

func TestNothingIsNotAnError(t *testing.T) {
	got, err := launch.Options("   ")
	if err != nil || got != nil {
		t.Fatalf("got %v, %v - want nothing, and no complaint about it", got, err)
	}
}

// The whole reason the parser exists. Console commands are how LobbyBaz puts
// somebody in the right match on the right team; a player who could type them
// could point their own client somewhere else and then report the room broken.
func TestConsoleCommandsAreRefused(t *testing.T) {
	for _, raw := range []string{
		"+connect 10.87.0.5:27015",
		"-novid +jointeam bad",
		"+map dota",
	} {
		if _, err := launch.Options(raw); !errors.Is(err, launch.ErrBadOption) {
			t.Errorf("Options(%q) = %v, want a refusal", raw, err)
		}
	}
}

func TestOptionsMustStartWithAFlag(t *testing.T) {
	if _, err := launch.Options("novid"); !errors.Is(err, launch.ErrBadOption) {
		t.Errorf("a bare word was accepted as options")
	}
}

// No quotes, no semicolons, no ampersands. Nothing is passed through a shell,
// so this is not a shell-injection guard - it is a guard against a value that
// turns into a second argument, and against a field that quietly accepts
// anything at all.
func TestValuesAreNarrow(t *testing.T) {
	for _, raw := range []string{
		`-language "en us"`,
		"-w 1920; shutdown",
		"-w $(whoami)",
		"-novid && echo",
		"-CONSOLE",
	} {
		if _, err := launch.Options(raw); !errors.Is(err, launch.ErrBadOption) {
			t.Errorf("Options(%q) was accepted", raw)
		}
	}
}

func TestItIsAFieldNotAScript(t *testing.T) {
	if _, err := launch.Options(strings.Repeat("-novid ", 40)); !errors.Is(err, launch.ErrBadOption) {
		t.Error("forty flags were accepted")
	}
	if _, err := launch.Options("-" + strings.Repeat("x", launch.MaxLength)); !errors.Is(err, launch.ErrBadOption) {
		t.Error("an over-long string was accepted")
	}
}
