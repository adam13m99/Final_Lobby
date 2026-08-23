// Package build holds the values stamped into the binary at link time.
//
// This is what removes the setup screen. The first version of the test
// client shipped a setup.txt beside the executable and asked the player to
// paste a server address and an access code into a form. That is three
// chances to mistype something before anyone has played anything, and it put
// the API token in a plaintext file on two machines.
//
// The build knows where its own server is. The player picks a name.
package build

import "strings"

// Set with -ldflags "-X finallobby/client/build.Version=..." by
// scripts/build.sh. The zero values are what a developer building by hand
// gets, and they are deliberately unusable rather than pointing at
// production by accident.
var (
	// Version is the build stamp, compared against the server's manifest to
	// decide whether this copy is out of date.
	Version = "dev"

	// Coordinator is the base URL of the control plane.
	Coordinator = ""

	// AuthToken is the shared bearer token for the test phase.
	AuthToken = ""

	// DownloadBase is the directory URL holding version.json and the
	// installer. Self-update reads it; nothing else does.
	DownloadBase = ""
)

// Configured reports whether this binary was stamped with a server to talk
// to. A build without one is a developer build, and the app says so rather
// than failing with a confusing connection error.
func Configured() bool {
	return Coordinator != ""
}

// UpdateURL is where the manifest for this build lives, or empty if this
// build cannot update itself.
func UpdateURL() string {
	if DownloadBase == "" {
		return ""
	}
	return strings.TrimRight(DownloadBase, "/") + "/version.json"
}
