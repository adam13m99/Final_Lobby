// Package dota builds and validates Dota 2 launch commands.
//
// Every argument is allowlisted. The service runs with elevated rights, so
// an unvalidated launch path or an unchecked argument would be a
// privilege-escalation vector, not merely untidy.
package dota

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var (
	ErrBadArg  = errors.New("dota: rejected argument")
	ErrBadPath = errors.New("dota: rejected executable path")
)

// gameModes are the Dota 2 game mode IDs we expose.
var gameModes = map[int]string{
	1: "All Pick", 2: "Captain's Mode", 3: "Random Draft",
	4: "Single Draft", 5: "All Random", 18: "Ability Draft",
	22: "Ranked All Pick", 23: "Turbo",
}

// GameModeName returns the display name for a mode ID.
func GameModeName(id int) (string, bool) {
	name, ok := gameModes[id]
	return name, ok
}

var validTeams = map[string]bool{"good": true, "bad": true, "spec": true}

// ourAddressSpace is the only network a client may be told to connect to.
// Without this check a malicious room could point a player's game client at
// an arbitrary host on their own LAN.
var ourAddressSpace = netip.MustParsePrefix("10.87.0.0/16")

// ValidateExePath checks that path really points at a Dota 2 executable.
func ValidateExePath(path string) error {
	lower := strings.ToLower(filepath.ToSlash(path))
	if !strings.HasSuffix(lower, "/dota2.exe") {
		return fmt.Errorf("%w: must end with dota2.exe, got %q", ErrBadPath, path)
	}
	if !strings.Contains(lower, "dota 2 beta") {
		return fmt.Errorf("%w: not inside a 'dota 2 beta' tree", ErrBadPath)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBadPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: symlinks are not accepted", ErrBadPath)
	}
	return nil
}

// steamLibraryPath extracts the "path" entries from libraryfolders.vdf.
var steamLibraryPath = regexp.MustCompile(`(?m)^\s*"path"\s+"(.*)"\s*$`)

// dotaExeSuffix is where Dota 2 lives inside any Steam library.
var dotaExeSuffix = filepath.Join(
	"steamapps", "common", "dota 2 beta", "game", "bin", "win64", "dota2.exe")

// FindInstall locates Dota 2 by reading Steam's own library list, so a
// player never has to browse for the executable themselves. Steam can spread
// libraries across drives, which is why the default path alone is not
// enough.
func FindInstall() (string, error) {
	var roots []string
	for _, base := range steamRoots() {
		roots = append(roots, base)
		vdf := filepath.Join(base, "steamapps", "libraryfolders.vdf")
		data, err := os.ReadFile(vdf)
		if err != nil {
			continue
		}
		for _, m := range steamLibraryPath.FindAllStringSubmatch(string(data), -1) {
			// Paths inside a VDF are escaped, e.g. C:\\Games.
			roots = append(roots, strings.ReplaceAll(m[1], `\\`, `\`))
		}
	}

	seen := make(map[string]bool)
	for _, root := range roots {
		candidate := filepath.Join(root, dotaExeSuffix)
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		if err := ValidateExePath(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%w: no Dota 2 installation found in any Steam library", ErrBadPath)
}

func validateNick(nick string) error {
	if len(nick) == 0 || len(nick) > 32 {
		return fmt.Errorf("%w: nickname length %d", ErrBadArg, len(nick))
	}
	for _, r := range nick {
		if !unicode.IsPrint(r) || strings.ContainsRune(`"'\/`, r) {
			return fmt.Errorf("%w: nickname contains %q", ErrBadArg, r)
		}
	}
	return nil
}

// BuildHostArgs produces the launch arguments for the player hosting.
//
// +sv_lan 1 is the whole trick: it puts Dota into LAN server mode, so the
// host's own machine runs the match and no Valve service is involved.
func BuildHostArgs(nick string, gameMode int, team string) ([]string, error) {
	if err := validateNick(nick); err != nil {
		return nil, err
	}
	if _, ok := gameModes[gameMode]; !ok {
		return nil, fmt.Errorf("%w: unknown game mode %d", ErrBadArg, gameMode)
	}
	if !validTeams[team] {
		return nil, fmt.Errorf("%w: unknown team %q", ErrBadArg, team)
	}
	return []string{
		"+name", nick,
		"+sv_lan", "1",
		"+map", "dota",
		"gamemode", strconv.Itoa(gameMode),
		"+jointeam", team,
	}, nil
}

// BuildClientArgs produces the launch arguments for a joining player.
//
// The client is told the host's address outright. That is what lets the
// relay drop LAN-discovery broadcast entirely rather than carrying it.
func BuildClientArgs(nick string, hostIP netip.Addr, team string) ([]string, error) {
	if err := validateNick(nick); err != nil {
		return nil, err
	}
	if !ourAddressSpace.Contains(hostIP) {
		return nil, fmt.Errorf("%w: %s is outside %s", ErrBadArg, hostIP, ourAddressSpace)
	}
	if !validTeams[team] {
		return nil, fmt.Errorf("%w: unknown team %q", ErrBadArg, team)
	}
	return []string{
		"+name", nick,
		"+connect", hostIP.String() + ":27015",
		"+jointeam", team,
	}, nil
}

// ValidateArgs re-checks a full argument list before it reaches the process.
// The builders above already produce safe lists; this is the second gate, so
// that a future caller assembling arguments by hand cannot slip past.
func ValidateArgs(args []string) error {
	allowed := map[string]bool{
		"+name": true, "+connect": true, "+sv_lan": true, "+map": true,
		"+jointeam": true, "gamemode": true, "-console": true,
		"-enableconsole": true, "-condebug": true,
	}
	for _, a := range args {
		if !strings.HasPrefix(a, "+") && !strings.HasPrefix(a, "-") && a != "gamemode" {
			continue // a value, already checked with its key
		}
		if !allowed[a] {
			return fmt.Errorf("%w: unknown key %q", ErrBadArg, a)
		}
	}
	return nil
}

// readyMarkers are the console.log lines that mean the host's listen server
// is up.
//
// UNVERIFIED against the current Dota 2 build. Confirm these during the
// two-PC acceptance test on a real host and correct them if they differ.
var readyMarkers = []string{
	"Server started",
	"Host_NewGame",
}

// ServerReady reports whether the host's Dota 2 has finished starting its
// LAN server, by looking for a marker in the part of console.log written
// since the launch. Reading only the new tail matters: the log persists
// between matches, so an old marker would otherwise report ready instantly.
func ServerReady(consoleLogPath string, since int64) (bool, error) {
	data, err := os.ReadFile(consoleLogPath)
	if err != nil {
		return false, err
	}
	if int64(len(data)) <= since {
		return false, nil
	}
	tail := string(data[since:])
	for _, marker := range readyMarkers {
		if strings.Contains(tail, marker) {
			return true, nil
		}
	}
	return false, nil
}
