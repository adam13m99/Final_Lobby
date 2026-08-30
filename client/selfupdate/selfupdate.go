// Package selfupdate keeps an installed copy current.
//
// Without this, every fix made during a test session costs a reinstall on
// every machine being tested - which in practice means the session stops
// while somebody walks to the other PC. With it, the app notices on its next
// launch and replaces itself.
//
// The design is deliberately dull. No background daemon, no patching, no
// partial downloads: fetch a manifest, compare a version string, download a
// whole file, check its hash, swap it in. Every step can be looked at
// afterwards and understood.
package selfupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Manifest is what the server publishes beside the installer.
type Manifest struct {
	Version   string    `json:"version"`
	SHA256    string    `json:"sha256"`
	Size      int64     `json:"size"`
	URL       string    `json:"url"`
	BuiltAt   time.Time `json:"built_at"`
	Installer string    `json:"installer"`
}

// maxInstaller caps what we will download. The installer is a handful of
// megabytes; anything wildly larger means we are talking to something that
// is not our server, and streaming it to disk would be the only harm done.
const maxInstaller = 128 << 20

// Check fetches the manifest and reports whether it describes a build other
// than the one running.
//
// "Other than", not "newer than": version strings here are build stamps, not
// ordered releases, and a rollback has to reach the machines as readily as a
// fix does. During a test session the server is always right about what
// should be running.
func Check(manifestURL, running string, timeout time.Duration) (*Manifest, bool, error) {
	if manifestURL == "" {
		return nil, false, nil
	}
	c := &http.Client{Timeout: timeout}
	resp, err := c.Get(manifestURL)
	if err != nil {
		return nil, false, fmt.Errorf("cannot reach the update server: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("the update server answered %s", resp.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, false, err
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, false, fmt.Errorf("the update server sent something unreadable: %w", err)
	}
	if m.Version == "" || m.SHA256 == "" {
		return nil, false, fmt.Errorf("the published manifest is incomplete")
	}
	return &m, m.Version != running, nil
}

// Attempts is how many times Download will try before giving up, and
// ResumeGap is how long it waits between tries.
//
// Downloads here cross Iran's domestic network to a box in Tehran, and a
// thirteen megabyte transfer over that is not a thing that either completes
// or fails cleanly - it is a thing that gets interrupted. Before this, one
// interruption anywhere in those thirteen megabytes threw away every byte
// already fetched and reported failure to the player (D78).
const (
	Attempts  = 4
	ResumeGap = 2 * time.Second
)

// Download fetches the installer named by a manifest into dir and returns
// its path. The file is verified against the manifest hash before the path
// is returned, so a caller that gets a path has a file worth running.
//
// An interrupted transfer is resumed rather than restarted: the coordinator
// serves the installer with http.ServeContent, which honours Range, so a
// second attempt asks only for the bytes that are missing. A server that
// ignores the range and starts again from zero is handled too - that answers
// 200 rather than 206, and the partial file is thrown away.
func Download(m *Manifest, baseURL, dir string) (string, error) {
	url := m.URL
	if url == "" {
		name := m.Installer
		if name == "" {
			name = "LobbyBaz-Setup.exe"
		}
		url = strings.TrimRight(baseURL, "/") + "/" + name
	}

	// Download to a temporary name in the destination directory, then rename.
	// A half-written file must never be sitting at the name we later run.
	tmp, err := os.CreateTemp(dir, "update-*.part")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpName)
	}()

	c := &http.Client{Timeout: 10 * time.Minute}
	var have int64
	var lastErr error

	for attempt := 1; attempt <= Attempts; attempt++ {
		if attempt > 1 {
			time.Sleep(ResumeGap)
		}

		n, restart, err := fetchInto(c, url, tmp, have)
		if restart {
			// The server sent the whole file when we asked for part of it, so
			// everything from before this attempt was stale. fetchInto has
			// already emptied the file and written from the start; all that is
			// left here is to stop counting the bytes that are gone.
			//
			// Emptying it a second time from out here is not harmless: it
			// happens after the new bytes have landed, and it deletes them.
			// That is what this looked like the first time it was written.
			have = 0
		}
		have += n

		if err == nil {
			lastErr = nil
			break
		}
		lastErr = err

		// An attempt that fetched nothing new will not do better for being
		// repeated. Stopping here keeps a broken route from turning into four
		// slow failures in a row.
		if n == 0 && attempt > 1 {
			break
		}
		if m.Size != 0 && have >= m.Size {
			// Everything arrived; the error is on the tail of a connection we
			// no longer need. The hash below is the real verdict.
			lastErr = nil
			break
		}
	}
	if lastErr != nil {
		return "", fmt.Errorf("the download was interrupted: %w", lastErr)
	}

	if err := tmp.Close(); err != nil {
		return "", err
	}

	// Hashed by re-reading the finished file rather than as it arrives: with
	// resumption the bytes do not all pass through one stream, and a hash
	// assembled across attempts is a hash nobody can check by hand.
	sum, size, err := hashFile(tmpName)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(sum, m.SHA256) {
		return "", fmt.Errorf("the downloaded file is not the one the server described - refusing to run it")
	}
	if m.Size != 0 && m.Size != size {
		return "", fmt.Errorf("the download stopped short: got %d bytes, expected %d", size, m.Size)
	}

	final := filepath.Join(dir, "LobbyBaz-Update.exe")
	// Windows will not rename over a file that exists.
	_ = os.Remove(final)
	if err := os.Rename(tmpName, final); err != nil {
		return "", err
	}
	return final, nil
}

// fetchInto appends to w, asking only for the bytes after `have`. It returns
// how many arrived, whether the caller must start again from zero, and why it
// stopped if it stopped early.
func fetchInto(c *http.Client, url string, w *os.File, have int64) (int64, bool, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return 0, false, err
	}
	if have > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
	}

	resp, err := c.Do(req)
	if err != nil {
		return 0, false, fmt.Errorf("cannot download the update: %w", err)
	}
	defer resp.Body.Close()

	restart := false
	switch resp.StatusCode {
	case http.StatusOK:
		// A range we asked for and did not get. Start over.
		restart = have > 0
	case http.StatusPartialContent:
	case http.StatusRequestedRangeNotSatisfiable:
		// We already hold at least as many bytes as the server has. Let the
		// hash decide whether they are the right ones.
		return 0, false, nil
	default:
		return 0, false, fmt.Errorf("the update server answered %s", resp.Status)
	}

	if restart {
		if _, err := w.Seek(0, io.SeekStart); err != nil {
			return 0, true, err
		}
		if err := w.Truncate(0); err != nil {
			return 0, true, err
		}
	} else if _, err := w.Seek(0, io.SeekEnd); err != nil {
		return 0, false, err
	}

	n, err := io.Copy(w, io.LimitReader(resp.Body, maxInstaller))
	return n, restart, err
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
