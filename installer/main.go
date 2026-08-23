// Command FinalLobby-Setup installs Final Lobby.
//
// One file. A player downloads it, runs it, allows the one Windows prompt,
// and the app opens. There is no folder to copy, no script to right-click,
// no server address to paste and no access code to type - the build knows
// where its own server is.
//
// It replaces a PowerShell script that had to be shipped alongside three
// executables and a text file holding an API token in the clear. That
// arrangement asked a non-technical person to do five things correctly
// before anything could be tested, on two machines, every time anything
// changed.
package main

import (
	"compress/gzip"
	"embed"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"finallobby/client/build"
)

// The payload. scripts/build.sh compiles each component, compresses it, and
// drops it here before this binary is built, so what ships is always the
// build that was tested beside it.
//
//go:embed payload
var payload embed.FS

// installDir is machine-wide because the network service runs as the system
// account and must be able to read its own binary regardless of who is
// logged in.
const appName = "Final Lobby"

var components = []struct {
	file    string
	service bool
}{
	{"netservice.exe", true},
	{"lobbyapp.exe", false},
	{"lobbycli.exe", false},
}

func main() {
	silent := hasFlag("/silent") || hasFlag("-silent")
	uninstall := hasFlag("/uninstall") || hasFlag("-uninstall")

	if !silent {
		banner()
	}

	if !elevated() {
		// Re-run ourselves with a UAC prompt. Creating a virtual network
		// adapter and registering a service are privileged; doing both once
		// here is what lets a player join a room later with no prompt at all.
		if err := reexecElevated(); err != nil {
			stop("Windows would not grant permission.\n\n"+
				"Right-click this file and choose \"Run as administrator\", then try again.", err, silent)
		}
		return
	}

	var err error
	if uninstall {
		err = removeAll()
	} else {
		err = install(silent)
	}
	if err != nil {
		stop("Setup could not finish.", err, silent)
	}

	if uninstall {
		say("Final Lobby has been removed.")
	} else {
		say("")
		say("  Done. Open \"" + appName + "\" from your desktop to play.")
		say("  You will not be asked for permission again.")
	}
	if !silent {
		say("")
		fmt.Print("Press Enter to close this window.")
		_, _ = fmt.Scanln()
	}
}

func install(silent bool) error {
	dir := installDir()
	say("Installing to " + dir)

	// Anything holding the old files open has to go first, or Windows
	// refuses to replace them. A half-removed install - service registered,
	// binary already deleted - is exactly the state a second attempt runs
	// into, so each half is handled on its own.
	stopExisting(dir)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("could not create %s: %w", dir, err)
	}
	for _, c := range components {
		if err := writeComponent(dir, c.file); err != nil {
			return err
		}
	}

	say("Registering the background network service")
	svc := filepath.Join(dir, "netservice.exe")
	if out, err := exec.Command(svc, "install").CombinedOutput(); err != nil {
		return fmt.Errorf("the service would not install: %s", strings.TrimSpace(string(out)))
	}
	if err := waitForService(20 * time.Second); err != nil {
		return err
	}
	say("Service is running")

	if err := writeShortcut(dir); err != nil {
		// A missing shortcut is annoying, not fatal: the app is installed
		// and can be started from its folder.
		say("Note: could not create the desktop shortcut: " + err.Error())
	} else {
		say("Shortcut created")
	}
	if err := registerUninstall(dir); err != nil {
		say("Note: could not register in Add or Remove Programs: " + err.Error())
	}

	// After an update, put the player back where they were.
	if silent {
		launchAsUser(filepath.Join(dir, "lobbyapp.exe"))
	}
	return nil
}

// writeComponent decompresses one embedded executable into place.
func writeComponent(dir, name string) error {
	f, err := payload.Open("payload/" + name + ".gz")
	if err != nil {
		return fmt.Errorf("this installer was built without %s", name)
	}
	defer f.Close()

	zr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("%s in this installer is damaged: %w", name, err)
	}
	defer zr.Close()

	target := filepath.Join(dir, name)
	// Write beside the target and rename, so an interrupted install never
	// leaves a truncated executable at a name something else will run.
	tmp, err := os.CreateTemp(dir, name+".*.part")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := io.Copy(tmp, zr); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	_ = os.Remove(target)
	if err := os.Rename(tmpName, target); err != nil {
		return fmt.Errorf("could not write %s: %w", target, err)
	}
	say("  " + name)
	return nil
}

func removeAll() error {
	dir := installDir()
	stopExisting(dir)
	unregisterUninstall()
	if p, err := desktopShortcutPath(); err == nil {
		_ = os.Remove(p)
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	return nil
}

// --- output -------------------------------------------------------------

func banner() {
	fmt.Println()
	fmt.Println("  Final Lobby - setup")
	fmt.Println("  ===================")
	fmt.Println("  version", build.Version)
	fmt.Println()
}

func say(msg string) {
	if msg == "" {
		fmt.Println()
		return
	}
	fmt.Println("  " + msg)
}

func stop(msg string, err error, silent bool) {
	fmt.Println()
	fmt.Println("  " + msg)
	if err != nil {
		fmt.Println("  " + err.Error())
	}
	fmt.Println()
	if !silent {
		fmt.Print("Press Enter to close this window.")
		_, _ = fmt.Scanln()
	}
	os.Exit(1)
}

func hasFlag(name string) bool {
	for _, a := range os.Args[1:] {
		if strings.EqualFold(a, name) {
			return true
		}
	}
	return false
}
