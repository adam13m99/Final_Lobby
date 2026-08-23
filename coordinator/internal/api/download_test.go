package api_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lobbybaz/coordinator/internal/api"
	"lobbybaz/coordinator/internal/room"
	"lobbybaz/coordinator/internal/ticket"
)

const testKey = "k7f3q9x2"

// publish writes a fake installer and its manifest, the way the deploy
// script does. corrupt=true writes a manifest whose hash does not match the
// file beside it, which is exactly what a half-finished upload leaves.
func publish(t *testing.T, dir, contents string, corrupt bool) {
	t.Helper()
	exe := filepath.Join(dir, api.InstallerName)
	if err := os.WriteFile(exe, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(contents))
	hash := hex.EncodeToString(sum[:])
	if corrupt {
		hash = strings.Repeat("0", 64)
	}
	m := api.Manifest{
		Version: "1.2.3",
		SHA256:  hash,
		Size:    int64(len(contents)),
		BuiltAt: time.Now(),
	}
	raw, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, "version.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func downloadServer(t *testing.T, dir string) *httptest.Server {
	t.Helper()
	s := api.New(api.Config{
		Rooms:       room.NewStore(),
		Tickets:     ticket.NewStore(),
		DistDir:     dir,
		DownloadKey: testKey,
		// A token is configured on purpose: the download must work without
		// one, because a browser has no way to send it.
		AuthToken: "secret-token",
	})
	srv := httptest.NewServer(s.Routes())
	t.Cleanup(srv.Close)
	return srv
}

func fetch(t *testing.T, url string) (int, []byte) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}

func TestBrowserCanDownloadWithoutAToken(t *testing.T) {
	dir := t.TempDir()
	publish(t, dir, "pretend this is an installer", false)
	srv := downloadServer(t, dir)

	code, body := fetch(t, srv.URL+"/d/"+testKey+"/")
	if code != http.StatusOK {
		t.Fatalf("landing page returned %d: %s", code, body)
	}
	if !strings.Contains(string(body), "Download for Windows") {
		t.Errorf("landing page does not offer a download:\n%s", body)
	}
	if !strings.Contains(string(body), "1.2.3") {
		t.Errorf("landing page does not name the version:\n%s", body)
	}

	code, body = fetch(t, srv.URL+"/d/"+testKey+"/"+api.InstallerName)
	if code != http.StatusOK {
		t.Fatalf("installer returned %d", code)
	}
	if string(body) != "pretend this is an installer" {
		t.Errorf("wrong bytes served: %q", body)
	}
}

func TestManifestIsServedForSelfUpdate(t *testing.T) {
	dir := t.TempDir()
	publish(t, dir, "build one", false)
	srv := downloadServer(t, dir)

	code, body := fetch(t, srv.URL+"/d/"+testKey+"/version.json")
	if code != http.StatusOK {
		t.Fatalf("manifest returned %d", code)
	}
	var m api.Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("manifest is not JSON: %v", err)
	}
	if m.Version != "1.2.3" || m.SHA256 == "" {
		t.Errorf("manifest = %+v", m)
	}
}

func TestWrongKeyLooksLikeNothingIsThere(t *testing.T) {
	dir := t.TempDir()
	publish(t, dir, "x", false)
	srv := downloadServer(t, dir)

	for _, path := range []string{"/d/wrong/", "/d/wrong/" + api.InstallerName, "/d//"} {
		if code, _ := fetch(t, srv.URL+path); code != http.StatusNotFound {
			t.Errorf("%s returned %d, want 404", path, code)
		}
	}
}

func TestOnlyFilesWeNameAreServed(t *testing.T) {
	// Anything derived from the request path is a traversal bug waiting to
	// happen. The server keeps a list; nothing else leaves the directory.
	dir := t.TempDir()
	publish(t, dir, "x", false)
	if err := os.WriteFile(filepath.Join(dir, "secrets.txt"), []byte("the api token"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := downloadServer(t, dir)

	for _, name := range []string{"secrets.txt", "..%2Fsecrets.txt", "relay.key"} {
		code, body := fetch(t, srv.URL+"/d/"+testKey+"/"+name)
		if code == http.StatusOK {
			t.Errorf("%s was served: %s", name, body)
		}
	}
}

func TestAHalfFinishedUploadIsRefusedRatherThanServed(t *testing.T) {
	// An upload has silently failed before: the file on the server was the
	// old one while every step reported success, and the next twenty
	// minutes went into testing a binary nobody had changed. The manifest
	// hash is checked against the file beside it, and a mismatch stops the
	// download page rather than handing out a broken build.
	dir := t.TempDir()
	publish(t, dir, "truncated upload", true)
	srv := downloadServer(t, dir)

	code, body := fetch(t, srv.URL+"/d/"+testKey+"/")
	if code != http.StatusServiceUnavailable {
		t.Fatalf("landing page returned %d, want 503: %s", code, body)
	}
	if !strings.Contains(string(body), "does not match its manifest") {
		t.Errorf("the page does not say what is wrong:\n%s", body)
	}
}

func TestNothingPublishedYet(t *testing.T) {
	srv := downloadServer(t, t.TempDir())
	code, body := fetch(t, srv.URL+"/d/"+testKey+"/")
	if code != http.StatusServiceUnavailable {
		t.Fatalf("returned %d, want 503", code)
	}
	if !strings.Contains(string(body), "Not ready yet") {
		t.Errorf("unhelpful page:\n%s", body)
	}
}

func TestNoDownloadRoutesWithoutADirectory(t *testing.T) {
	// A coordinator running for tests, or one with no build published,
	// should not expose the route at all.
	s := api.New(api.Config{Rooms: room.NewStore(), Tickets: ticket.NewStore()})
	srv := httptest.NewServer(s.Routes())
	defer srv.Close()

	if code, _ := fetch(t, srv.URL+"/d/"+testKey+"/"); code != http.StatusNotFound {
		t.Errorf("returned %d, want 404", code)
	}
}

func TestTheApiItselfStillNeedsItsToken(t *testing.T) {
	// The download hole must not have widened anything else.
	dir := t.TempDir()
	publish(t, dir, "x", false)
	srv := downloadServer(t, dir)

	if code, _ := fetch(t, srv.URL+"/v1/rooms"); code != http.StatusUnauthorized {
		t.Fatalf("the room list answered without a token: %d", code)
	}
}
