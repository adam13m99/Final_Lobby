package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"lobbybaz/client/build"
)

// Config is what the CLI remembers between commands: who you are, where the
// coordinator is, and which room you are currently in.
//
// A real client will keep this in the app's own storage; the test CLI writes
// a small JSON file so two people on two PCs can run commands without
// retyping everything.
type Config struct {
	Coordinator string `json:"coordinator"`
	AuthToken   string `json:"auth_token"`
	PlayerID    string `json:"player_id"`
	Nick        string `json:"nick"`
	MMR         int    `json:"mmr,omitempty"`

	// Set by create/join, cleared by leave.
	RoomID      string `json:"room_id,omitempty"`
	VirtualIP   string `json:"virtual_ip,omitempty"`
	HostIP      string `json:"host_ip,omitempty"`
	Subnet      string `json:"subnet,omitempty"`
	Ticket      string `json:"ticket,omitempty"`
	RelayAddr   string `json:"relay_addr,omitempty"`
	RelayPub    string `json:"relay_pub,omitempty"`
	IsHost      bool   `json:"is_host,omitempty"`
	IsSpectator bool   `json:"is_spectator,omitempty"`
}

// Prepare fills in everything the player should never have to type: the
// server this build was made for, and a stable ID for this installation.
//
// The ID is random rather than derived from anything about the machine. A
// player who reinstalls becomes a new person, which is a known hole in the
// kick block and is the price of having no accounts yet.
func (c *Config) Prepare() bool {
	changed := false
	if build.Configured() {
		// A stamped build always wins. Otherwise a stale config file left by
		// an older install would keep pointing the app at a dead address,
		// and the player would have no way to correct it.
		if c.Coordinator != build.Coordinator {
			c.Coordinator = build.Coordinator
			changed = true
		}
		if c.AuthToken != build.AuthToken {
			c.AuthToken = build.AuthToken
			changed = true
		}
	}
	if c.PlayerID == "" {
		c.PlayerID = newPlayerID()
		changed = true
	}
	return changed
}

func newPlayerID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		// The only way this fails is a broken OS random source. A fixed ID
		// would silently collide with every other player, so refuse loudly.
		panic("cannot generate a player ID: " + err.Error())
	}
	return "p_" + hex.EncodeToString(b[:])
}

// NeedsName reports whether the player has still to choose a name. It is the
// only thing the app asks for on first run.
func (c *Config) NeedsName() bool { return c.Nick == "" }

func Path() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "LobbyBaz", "lobbycli.json")
}

func Load() (*Config, error) {
	cfg := &Config{}
	data, err := os.ReadFile(Path())
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("config at %s is corrupt: %w", Path(), err)
	}
	return cfg, nil
}

func (c *Config) Save() error {
	p := Path()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	// 0600: the file holds an API token and a session ticket.
	return os.WriteFile(p, data, 0o600)
}

// clearRoom forgets the current room without forgetting who you are.
func (c *Config) ClearRoom() {
	c.RoomID = ""
	c.VirtualIP = ""
	c.HostIP = ""
	c.Subnet = ""
	c.Ticket = ""
	c.IsHost = false
	c.IsSpectator = false
}

func (c *Config) RequireLogin() error {
	if c.Coordinator == "" || c.PlayerID == "" {
		return fmt.Errorf("this build has no server configured; download the app again from the link")
	}
	if c.Nick == "" {
		return fmt.Errorf("choose a player name first")
	}
	return nil
}

func (c *Config) RequireRoom() error {
	if err := c.RequireLogin(); err != nil {
		return err
	}
	if c.RoomID == "" {
		return fmt.Errorf("you are not in a room; use `lobbycli create` or `lobbycli join <room-id>`")
	}
	return nil
}
