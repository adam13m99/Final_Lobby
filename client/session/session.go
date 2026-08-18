package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

	// Set by create/join, cleared by leave.
	RoomID    string `json:"room_id,omitempty"`
	VirtualIP string `json:"virtual_ip,omitempty"`
	HostIP    string `json:"host_ip,omitempty"`
	Subnet    string `json:"subnet,omitempty"`
	Ticket    string `json:"ticket,omitempty"`
	RelayAddr string `json:"relay_addr,omitempty"`
	RelayPub  string `json:"relay_pub,omitempty"`
	IsHost    bool   `json:"is_host,omitempty"`
}

func Path() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "FinalLobby", "lobbycli.json")
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
}

func (c *Config) RequireLogin() error {
	if c.Coordinator == "" || c.PlayerID == "" {
		return fmt.Errorf("run `lobbycli setup` first")
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
