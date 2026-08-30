package selfupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
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

// The owner, on the live build: "An update (2026.08.30-2033) could not be
// downloaded: the download was interrupted: unexpected EOF."
//
// The server's own fault was a write timeout that cut long downloads off
// (D78), and that is fixed where it belongs. This is the other half. Thirteen
// megabytes across Iran's domestic network is not a transfer that either
// completes or fails cleanly; it is one that gets interrupted. Before this,
// one interruption anywhere in it threw away every byte already fetched and
// told the player it had failed.

// flakyServer serves the payload but hangs up part-way through the first
// `breaks` responses, the way a middlebox or a dropped route does. It honours
// Range, as http.ServeContent on the coordinator does, so a resumed request
// asks only for what is missing.
func flakyServer(t *testing.T, payload []byte, breaks int) (*httptest.Server, *int) {
	t.Helper()
	sum := sha256.Sum256(payload)
	m := Manifest{
		Version:   "new",
		SHA256:    hex.EncodeToString(sum[:]),
		Size:      int64(len(payload)),
		Installer: "LobbyBaz-Setup.exe",
	}
	requests := 0

	mux := http.NewServeMux()
	mux.HandleFunc("/version.json", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(m)
	})
	mux.HandleFunc("/LobbyBaz-Setup.exe", func(w http.ResponseWriter, r *http.Request) {
		requests++

		from := 0
		if rng := r.Header.Get("Range"); strings.HasPrefix(rng, "bytes=") {
			var start int
			if _, err := fmt.Sscanf(rng, "bytes=%d-", &start); err == nil && start <= len(payload) {
				from = start
			}
		}

		rest := payload[from:]
		if from > 0 {
			w.Header().Set("Content-Range",
				fmt.Sprintf("bytes %d-%d/%d", from, len(payload)-1, len(payload)))
		}

		if requests <= breaks {
			// Promise the whole remainder, send a third of it, hang up. This
			// is what produces "unexpected EOF" at the other end.
			w.Header().Set("Content-Length", strconv.Itoa(len(rest)))
			if from > 0 {
				w.WriteHeader(http.StatusPartialContent)
			}
			_, _ = w.Write(rest[:len(rest)/3])
			if fl, ok := w.(http.Flusher); ok {
				fl.Flush()
			}
			if hj, ok := w.(http.Hijacker); ok {
				conn, _, err := hj.Hijack()
				if err == nil {
					_ = conn.Close()
				}
			}
			return
		}

		if from > 0 {
			w.WriteHeader(http.StatusPartialContent)
		}
		_, _ = w.Write(rest)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &requests
}

func TestAnInterruptedDownloadResumesInsteadOfFailing(t *testing.T) {
	payload := []byte(strings.Repeat("LobbyBaz installer payload. ", 4000))
	srv, requests := flakyServer(t, payload, 2)

	m, want, err := Check(srv.URL+"/version.json", "old", 5*time.Second)
	if err != nil || !want {
		t.Fatalf("check: %v, want=%v", err, want)
	}

	path, err := Download(m, srv.URL, t.TempDir())
	if err != nil {
		t.Fatalf("two interruptions in thirteen megabytes ended the update: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(payload) {
		t.Fatalf("downloaded %d bytes, want %d", len(got), len(payload))
	}
	if string(got) != string(payload) {
		t.Fatal("the file on disk is not the file the server published - " +
			"a resumed download spliced the bytes together wrongly")
	}
	if *requests < 3 {
		t.Fatalf("the server saw %d requests; two of them were cut off, so a "+
			"third was needed and this test is not exercising resumption", *requests)
	}
}

// A server that ignores Range and answers 200 with the whole file must not
// end up with the first attempt's bytes glued in front of the second's.
func TestAServerThatIgnoresRangeStartsTheFileAgain(t *testing.T) {
	payload := []byte(strings.Repeat("LobbyBaz installer payload. ", 4000))
	sum := sha256.Sum256(payload)
	m := Manifest{
		Version:   "new",
		SHA256:    hex.EncodeToString(sum[:]),
		Size:      int64(len(payload)),
		Installer: "LobbyBaz-Setup.exe",
	}

	requests := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/LobbyBaz-Setup.exe", func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			_, _ = w.Write(payload[:len(payload)/2])
			if hj, ok := w.(http.Hijacker); ok {
				if conn, _, err := hj.Hijack(); err == nil {
					_ = conn.Close()
				}
			}
			return
		}
		// Range ignored on purpose: the whole file, status 200.
		_, _ = w.Write(payload)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	path, err := Download(&m, srv.URL, t.TempDir())
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(payload) {
		t.Fatalf("downloaded %d bytes, want %d - half a file was left in front "+
			"of a whole one", len(got), len(payload))
	}
}

// Giving up is still possible. A server that never finishes must produce a
// failure rather than a loop, and the file must not be handed on.
func TestADownloadThatNeverFinishesGivesUp(t *testing.T) {
	payload := []byte(strings.Repeat("LobbyBaz installer payload. ", 4000))
	srv, _ := flakyServer(t, payload, 99)

	m, _, err := Check(srv.URL+"/version.json", "old", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Download(m, srv.URL, t.TempDir()); err == nil {
		t.Fatal("a download that never completed was reported as succeeding")
	}
}
