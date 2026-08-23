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

// Download fetches the installer named by a manifest into dir and returns
// its path. The file is verified against the manifest hash before the path
// is returned, so a caller that gets a path has a file worth running.
func Download(m *Manifest, baseURL, dir string) (string, error) {
	url := m.URL
	if url == "" {
		name := m.Installer
		if name == "" {
			name = "FinalLobby-Setup.exe"
		}
		url = strings.TrimRight(baseURL, "/") + "/" + name
	}

	c := &http.Client{Timeout: 10 * time.Minute}
	resp, err := c.Get(url)
	if err != nil {
		return "", fmt.Errorf("cannot download the update: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the update server answered %s", resp.Status)
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

	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, maxInstaller))
	if err != nil {
		return "", fmt.Errorf("the download was interrupted: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, m.SHA256) {
		return "", fmt.Errorf("the downloaded file is not the one the server described - refusing to run it")
	}
	if m.Size != 0 && m.Size != n {
		return "", fmt.Errorf("the download stopped short: got %d bytes, expected %d", n, m.Size)
	}

	final := filepath.Join(dir, "FinalLobby-Update.exe")
	// Windows will not rename over a file that exists.
	_ = os.Remove(final)
	if err := os.Rename(tmpName, final); err != nil {
		return "", err
	}
	return final, nil
}
