package selfupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// server publishes a manifest and an installer the way the coordinator does.
func server(t *testing.T, version string, payload []byte, hash string) *httptest.Server {
	t.Helper()
	if hash == "" {
		sum := sha256.Sum256(payload)
		hash = hex.EncodeToString(sum[:])
	}
	m := Manifest{
		Version:   version,
		SHA256:    hash,
		Size:      int64(len(payload)),
		Installer: "LobbyBaz-Setup.exe",
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/version.json", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(m)
	})
	mux.HandleFunc("/LobbyBaz-Setup.exe", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestNoUpdateWhenVersionsMatch(t *testing.T) {
	srv := server(t, "2026.08.23-1", []byte("installer"), "")
	m, newer, err := Check(srv.URL+"/version.json", "2026.08.23-1", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if newer {
		t.Error("offered an update for the version already running")
	}
	if m.Version != "2026.08.23-1" {
		t.Errorf("manifest = %+v", m)
	}
}

func TestUpdateOfferedWhenTheServerHasSomethingElse(t *testing.T) {
	srv := server(t, "2026.08.23-2", []byte("installer"), "")
	_, newer, err := Check(srv.URL+"/version.json", "2026.08.23-1", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !newer {
		t.Error("did not offer the newer build")
	}
}

func TestRollbackReachesMachinesToo(t *testing.T) {
	// Versions are build stamps, not an ordered series. If a bad build goes
	// out mid-session, replacing it must reach the test machines as readily
	// as a fix does - so "different" is what triggers an update, not
	// "greater".
	srv := server(t, "2026.08.23-1", []byte("older installer"), "")
	_, differs, err := Check(srv.URL+"/version.json", "2026.08.23-9", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !differs {
		t.Error("a rollback published by the server was ignored")
	}
}

func TestDownloadVerifiesTheHash(t *testing.T) {
	payload := []byte("the real installer bytes")
	srv := server(t, "v2", payload, "")
	m, _, err := Check(srv.URL+"/version.json", "v1", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	path, err := Download(m, srv.URL, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Errorf("downloaded %q", got)
	}
}

func TestATamperedDownloadIsRefused(t *testing.T) {
	// The download is plain HTTP over a national network. A file whose hash
	// does not match the manifest must never be handed back to a caller who
	// is about to execute it.
	srv := server(t, "v2", []byte("not what was promised"), strings.Repeat("a", 64))
	m, _, err := Check(srv.URL+"/version.json", "v1", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err := Download(m, srv.URL, dir); err == nil {
		t.Fatal("a file that did not match its hash was accepted")
	}
	// And nothing is left behind for anything else to pick up and run.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".exe") {
			t.Errorf("a rejected download was left at %s", e.Name())
		}
	}
}

func TestUnreachableServerIsNotFatal(t *testing.T) {
	// A player whose connection is down should still get the app they have,
	// not a failure to start.
	_, newer, err := Check("http://127.0.0.1:1/version.json", "v1", 500*time.Millisecond)
	if err == nil {
		t.Fatal("expected an error")
	}
	if newer {
		t.Error("offered an update it could not have fetched")
	}
}

func TestNoUpdateURLMeansNoUpdate(t *testing.T) {
	// A developer build has no download base stamped into it.
	m, newer, err := Check("", "dev", time.Second)
	if err != nil || newer || m != nil {
		t.Fatalf("got %v, %v, %v", m, newer, err)
	}
}
