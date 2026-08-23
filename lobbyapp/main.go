// Command lobbyapp is the Final Lobby desktop client.
//
// It runs in the player's own session with no special rights, serves the UI
// on loopback, and opens it in the default browser. The privileged work -
// creating the network adapter, running the tunnel - stays in the Windows
// service, which this talks to over a named pipe.
//
// A local web server is not the same mistake the predecessor made. That was
// a *privileged* agent listening on localhost, so any web page a player
// visited could drive it as Administrator. This process has exactly the
// rights the player already has, binds to 127.0.0.1 on a random port, and
// requires a random token that only the page it opened knows. A hostile page
// gains nothing it could not already do by running a program as the user.
package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"finallobby/client/build"
	"finallobby/client/selfupdate"
)

//go:embed ui
var uiFiles embed.FS

func main() {
	noBrowser := flag.Bool("no-browser", false, "do not open a browser window")
	addr := flag.String("listen", "127.0.0.1:0", "loopback address to serve on")
	flag.Parse()

	token, err := randomToken()
	if err != nil {
		log.Fatalf("could not generate a session token: %v", err)
	}

	l, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("could not listen on %s: %v", *addr, err)
	}
	url := fmt.Sprintf("http://%s/?t=%s", l.Addr().String(), token)

	app := newServer(token)
	go app.checkForUpdate()

	srv := &http.Server{
		Handler:           app.routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	fmt.Printf("Final Lobby %s is running.\n", build.Version)
	fmt.Println()
	fmt.Println("  ", url)
	fmt.Println()
	fmt.Println("Leave this window open while you play. Close it to quit.")

	if !*noBrowser {
		openBrowser(url)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()

	if err := srv.Serve(l); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server stopped: %v", err)
	}
}

// checkForUpdate asks the server what build it is publishing and, if it is
// not this one, downloads it ready for the player to accept.
//
// Downloading without asking, installing only on request. The download is
// the slow part and doing it in the background means the offer, when it
// appears, is instant. Installing replaces files and restarts the app, which
// is never something to do to somebody mid-conversation.
func (s *server) checkForUpdate() {
	manifestURL := build.UpdateURL()
	if manifestURL == "" {
		return
	}
	clearOldUpdate()
	m, differs, err := selfupdate.Check(manifestURL, build.Version, 10*time.Second)
	if err != nil {
		// A player whose connection is down still gets the app they have.
		fmt.Println("Could not check for updates:", err)
		return
	}
	if !differs {
		return
	}
	fmt.Printf("A different build is published (%s); downloading it.\n", m.Version)

	s.mu.Lock()
	s.update = &pendingUpdate{Version: m.Version}
	s.mu.Unlock()

	dir := updateDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		s.updateFailed(m.Version, err)
		return
	}

	path, err := selfupdate.Download(m, build.DownloadBase, dir)
	if err != nil {
		s.updateFailed(m.Version, err)
		return
	}

	s.mu.Lock()
	s.update = &pendingUpdate{Version: m.Version, Ready: true, Path: path}
	s.mu.Unlock()
	fmt.Printf("Update %s is ready to install.\n", m.Version)
}

// updateDir is where a downloaded installer waits to be run.
//
// Deliberately not %LOCALAPPDATA%\FinalLobby, which is where the first test
// build installed itself: the new installer removes that folder, and putting
// the installer inside the folder it is about to delete meant it was trying
// to delete itself while running. It failed silently and left the folder
// behind, which is how the collision was noticed at all.
func updateDir() string {
	return filepath.Join(os.TempDir(), "FinalLobby-update")
}

// clearOldUpdate deletes an installer left over from a previous update. It
// runs before the update check, when nothing can still be executing from
// there, and saves carrying a spare copy of the whole app on disk forever.
func clearOldUpdate() {
	_ = os.RemoveAll(updateDir())
}

func (s *server) updateFailed(version string, err error) {
	fmt.Println("Could not download the update:", err)
	s.mu.Lock()
	s.update = &pendingUpdate{Version: version, Error: err.Error()}
	s.mu.Unlock()
}

func randomToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func openBrowser(url string) {
	// rundll32 avoids cmd.exe quoting rules mangling the query string.
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "could not open a browser automatically; open the address above yourself")
		return
	}
	_ = cmd.Process.Release()
}
