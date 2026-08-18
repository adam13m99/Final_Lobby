// Command lobbyapp is the prototype desktop client.
//
// It runs in the player's own session with no special rights, serves a small
// web UI on loopback, and opens it in the default browser. The privileged
// work - creating the network adapter, running the tunnel - stays in the
// Windows service, which this talks to over a named pipe.
//
// A local web server is not the same mistake the predecessor made. That was
// a *privileged* agent listening on localhost, so any web page a player
// visited could drive it as Administrator. This process has exactly the
// rights the player already has, binds to 127.0.0.1 on a random port, and
// requires a random token that only the page it opened knows. A hostile page
// gains nothing it could not already do by running a program as the user.
//
// The real UI is sub-project 3. This exists so two people can install
// something and play a match without typing commands.
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
	"time"
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

	srv := &http.Server{
		Handler:           newServer(token).routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	fmt.Println("Final Lobby is running.")
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
