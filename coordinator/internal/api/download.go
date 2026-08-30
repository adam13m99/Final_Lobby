package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The download endpoint is how a player gets the app: they open a link, they
// get a file, they run it. There is no folder to copy and nothing to type.
//
// It is served by the coordinator on the port we already own. The obvious
// alternative - a web server on a new port, or a vhost on the box's nginx -
// would mean a new firewall rule and a change to a config that a live,
// unrelated business depends on. Neither is worth it to serve one file.
//
// It cannot be behind the bearer token, because a browser fetches it and a
// browser has no token. The unguessable path segment is the secret instead.
// That is weak, and deliberately so for the test phase: the worst outcome is
// a stranger downloading a test build of a lobby for a game they cannot
// reach. Real accounts replace this before real players arrive.

// InstallerName is the file a player downloads.
const InstallerName = "LobbyBaz-Setup.exe"

// manifestName is the file describing the current build, written by the
// deploy script next to the installer.
const manifestName = "version.json"

// InstallerWriteWindow is how long one download of the installer may take.
//
// It replaces the server-wide WriteTimeout for this one response. See
// downloadFile for why that timeout cannot apply here.
const InstallerWriteWindow = 30 * time.Minute

// Manifest is what the app reads to decide whether it is out of date.
type Manifest struct {
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
	// URL is absolute so the app does not have to reassemble it, and so the
	// download host can move without a new client build.
	URL       string    `json:"url"`
	BuiltAt   time.Time `json:"built_at,omitempty"`
	Installer string    `json:"installer"`
}

type downloads struct {
	dir string
	key string

	mu       sync.Mutex
	checked  time.Time
	verified bool
	problem  string
}

// downloadRoutes registers the public download surface. It is only mounted
// when a directory is configured, so a coordinator run for tests serves
// nothing.
func (s *Server) downloadRoutes(mux *http.ServeMux) {
	if s.dl == nil || s.dl.dir == "" || s.dl.key == "" {
		return
	}
	mux.HandleFunc("GET /d/{key}", s.limitedPublic(s.downloadIndex))
	mux.HandleFunc("GET /d/{key}/", s.limitedPublic(s.downloadIndex))
	mux.HandleFunc("GET /d/{key}/{file}", s.limitedPublic(s.downloadFile))
}

// limitedPublic is the rate limiter without the bearer-token check. Used
// only for the download pages, which a browser has to be able to reach.
func (s *Server) limitedPublic(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.limitRead.Allow(clientKey(r), s.now()) {
			w.Header().Set("Retry-After", "2")
			writeErr(w, http.StatusTooManyRequests, "slow down")
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.PathValue("key")), []byte(s.dl.key)) != 1 {
			http.NotFound(w, r)
			return
		}
		next(w, r)
	}
}

func (s *Server) downloadFile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	// Only ever serve files we name ourselves. Anything derived from the
	// request path is a directory-traversal bug waiting to be written.
	switch name {
	case InstallerName, manifestName:
	case "":
		s.downloadIndex(w, r)
		return
	default:
		http.NotFound(w, r)
		return
	}

	path := filepath.Join(s.dl.dir, name)
	f, err := os.Open(path)
	if err != nil {
		s.log.Warn("download missing", "file", name, "err", err)
		writeErr(w, http.StatusNotFound, "that build is not on the server yet")
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot read the build")
		return
	}
	if name == InstallerName {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="`+InstallerName+`"`)
	} else {
		w.Header().Set("Content-Type", "application/json")
	}
	// No caching: an app checking for an update must not be handed the
	// manifest it saw an hour ago by some middlebox on the way.
	w.Header().Set("Cache-Control", "no-store")

	if name == InstallerName {
		// The server's WriteTimeout is fifteen seconds, which is right for an
		// API answering in milliseconds and catastrophic for a thirteen
		// megabyte file: it is one deadline covering the whole response, set
		// when the request headers are read, so anybody who cannot pull the
		// installer at about 900 KB/s has their connection cut mid-file and
		// gets an unexpected EOF. On a domestic Iranian link that is
		// everybody, which is the entire audience (D78).
		//
		// So this one response gets its own deadline. Thirty minutes is
		// roughly 7 KB/s across the whole file: slower than any connection
		// this product is usable on, and still bounded, so a client that
		// opens the download and stops reading cannot hold a connection for
		// ever.
		if err := http.NewResponseController(w).SetWriteDeadline(
			time.Now().Add(InstallerWriteWindow)); err != nil {
			// Not fatal. An unsupported deadline means the fifteen-second one
			// still applies, which is the behaviour that was broken rather
			// than a new failure - but it is worth saying out loud, because
			// the symptom lands on a player and not here.
			s.log.Warn("cannot extend the write deadline for the installer; "+
				"a slow download will be cut off", "err", err)
		}
	}

	http.ServeContent(w, r, name, info.ModTime(), f)
}

// manifest reads version.json and, at most once a minute, re-verifies that
// the hash in it actually matches the installer sitting beside it.
//
// This check exists because an upload has silently failed before: the file
// on the server was the old one while everything reported success, and the
// next twenty minutes were spent testing a binary nobody had changed. A
// mismatch here is reported rather than served.
func (d *downloads) manifest() (Manifest, error) {
	var m Manifest
	raw, err := os.ReadFile(filepath.Join(d.dir, manifestName))
	if err != nil {
		return m, fmt.Errorf("no build published yet: %w", err)
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return m, fmt.Errorf("published manifest is corrupt: %w", err)
	}
	m.Installer = InstallerName

	d.mu.Lock()
	stale := time.Since(d.checked) > time.Minute
	d.mu.Unlock()
	if stale {
		ok, problem := d.verify(m)
		d.mu.Lock()
		d.checked, d.verified, d.problem = time.Now(), ok, problem
		d.mu.Unlock()
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.verified {
		return m, fmt.Errorf("%s", d.problem)
	}
	return m, nil
}

func (d *downloads) verify(m Manifest) (bool, string) {
	f, err := os.Open(filepath.Join(d.dir, InstallerName))
	if err != nil {
		return false, "the installer named by the manifest is not on the server"
	}
	defer f.Close()

	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return false, "the installer could not be read"
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, m.SHA256) {
		return false, "the published installer does not match its manifest - the upload was incomplete"
	}
	if m.Size != 0 && m.Size != n {
		return false, "the published installer is not the size the manifest claims"
	}
	return true, ""
}

func (s *Server) downloadIndex(w http.ResponseWriter, r *http.Request) {
	m, err := s.dl.manifest()
	base := "/d/" + s.dl.key + "/"

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")

	body := fmt.Sprintf(downloadPage, base+InstallerName, m.Version, m.Size/(1024*1024))
	if err != nil {
		body = fmt.Sprintf(downloadProblemPage, err.Error())
		w.WriteHeader(http.StatusServiceUnavailable)
		s.log.Error("download page unavailable", "err", err)
	}
	_, _ = io.WriteString(w, body)
}

// The page a player lands on. Deliberately one screen with one button: the
// person opening this link is being talked through it over the phone.
const downloadPage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>LobbyBaz</title>
<style>
 :root{color-scheme:dark}
 body{margin:0;min-height:100vh;display:grid;place-items:center;
      background:#14161c;color:#e7e9ee;
      font:16px/1.6 "Segoe UI",system-ui,sans-serif}
 .card{max-width:30rem;padding:2.5rem;text-align:center}
 h1{margin:0 0 .25rem;font-size:1.9rem;letter-spacing:-.02em}
 .sub{color:#8b93a7;margin:0 0 2rem}
 a.btn{display:inline-block;background:#e04a2f;color:#fff;text-decoration:none;
       padding:.9rem 2.2rem;border-radius:.6rem;font-weight:600;font-size:1.05rem}
 a.btn:hover{background:#f2593d}
 .meta{color:#6d7488;font-size:.85rem;margin-top:1.5rem}
 ol{text-align:right;color:#9aa2b5;font-size:.9rem;margin:2rem 0 0;padding:0 1.2rem}
 ol{text-align:left}
 li{margin:.4rem 0}
</style></head><body><div class="card">
<h1>LobbyBaz</h1>
<p class="sub">Dota 2 with your friends, over the domestic network.</p>
<a class="btn" href="%s">Download for Windows</a>
<ol>
<li>Run the file you just downloaded.</li>
<li>Say yes when Windows asks for permission &mdash; only this once.</li>
<li>Pick a name. That is the whole setup.</li>
</ol>
<p class="meta">Version %s &middot; about %d MB &middot; Windows 10 or 11</p>
</div></body></html>`

const downloadProblemPage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>LobbyBaz</title>
<style>:root{color-scheme:dark}body{margin:0;min-height:100vh;display:grid;
 place-items:center;background:#14161c;color:#e7e9ee;
 font:16px/1.6 "Segoe UI",system-ui,sans-serif;text-align:center}
 code{color:#f2593d}</style></head><body><div>
<h1>Not ready yet</h1>
<p>The download is not available right now.</p>
<p><code>%s</code></p>
</div></body></html>`
