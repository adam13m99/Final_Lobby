// Package ipc carries commands between the desktop client and the Windows
// service over a named pipe.
//
// A named pipe, not an HTTP port on localhost. The predecessor project used
// localhost HTTP and had to patch a remote-code-execution hole after
// shipping: any web page the player visited could reach that port and drive
// an agent running as Administrator. A named pipe is not reachable from a
// browser at all, which removes the entire class of attack rather than
// filtering it.
package ipc

import "net/netip"

// PipeName is where the service listens.
const PipeName = `\\.\pipe\finallobby`

// Request is one command from the client.
type Request struct {
	Op string `json:"op"`

	// Connect
	RelayAddr   string `json:"relay_addr,omitempty"`
	RelayPub    string `json:"relay_pub,omitempty"`
	Ticket      string `json:"ticket,omitempty"`
	VirtualIP   string `json:"virtual_ip,omitempty"`
	Subnet      string `json:"subnet,omitempty"`
	Coordinator string `json:"coordinator,omitempty"`
	RoomID      string `json:"room_id,omitempty"`
	// AuthToken lets the service's lease checks authenticate to the
	// coordinator. Without it the watchdog cannot tell a revoked lease from
	// an unreachable one, and fails closed on every check.
	AuthToken string `json:"auth_token,omitempty"`

	// Launch
	Role     string `json:"role,omitempty"` // "host" or "client"
	Nick     string `json:"nick,omitempty"`
	GameMode int    `json:"game_mode,omitempty"`
	Team     string `json:"team,omitempty"`
	HostIP   string `json:"host_ip,omitempty"`
}

// Supported operations.
const (
	OpStatus     = "status"
	OpConnect    = "connect"
	OpDisconnect = "disconnect"
	OpLaunch     = "launch"
	OpPing       = "ping"
)

// Response is the service's reply. Err is empty on success.
type Response struct {
	Err string `json:"error,omitempty"`

	// Status
	State       string `json:"state,omitempty"`
	VirtualIP   string `json:"virtual_ip,omitempty"`
	RoomID      string `json:"room_id,omitempty"`
	AdapterName string `json:"adapter,omitempty"`
	Connected   bool   `json:"connected"`
	DotaRunning bool   `json:"dota_running,omitempty"`
	// DotaFound reports whether the service can locate a Dota 2 install.
	// The app shows this in its diagnostics, so a missing game is named as
	// the problem rather than surfacing later as a launch failure.
	DotaFound bool `json:"dota_found,omitempty"`
	// RelayMillis is this machine's smoothed round trip to the relay, or
	// zero before the first keepalive has come back. The lobby shows the
	// host's copy of this number beside their room (D42); it is the only
	// latency measurable from a lobby, since a player browsing rooms has no
	// path to a host they have not joined.
	RelayMillis int `json:"relay_ms,omitempty"`

	// Launch. DotaPath is also filled in on a status reply, where it is the
	// install the service would use: Settings shows it, because "we could not
	// find Dota" and "we found the wrong Dota" look the same to a player.
	DotaPath string   `json:"dota_path,omitempty"`
	Args     []string `json:"args,omitempty"`

	Version string `json:"version,omitempty"`
}

// ParseAddr is a small helper shared by both ends.
func ParseAddr(s string) (netip.Addr, bool) {
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, false
	}
	return a, true
}
