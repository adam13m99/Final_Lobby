package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// connectInfo mirrors what the coordinator returns from create and join.
type connectInfo struct {
	RoomID     string `json:"room_id"`
	Slot       int    `json:"slot"`
	IsHost     bool   `json:"is_host"`
	VirtualIP  string `json:"virtual_ip"`
	HostIP     string `json:"host_ip"`
	Subnet     string `json:"subnet"`
	Ticket     string `json:"ticket"`
	RelayAddr  string `json:"relay_addr"`
	RelayPub   string `json:"relay_pub"`
	DotaConnect string `json:"dota_connect"`
}

type roomView struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Status  string   `json:"status"`
	HostID  string   `json:"host_id"`
	Players []string `json:"players"`
	Free    int      `json:"free_slots"`
}

type apiClient struct {
	base  string
	token string
	http  *http.Client
}

func newAPI(cfg *Config) *apiClient {
	return &apiClient{
		base:  strings.TrimRight(cfg.Coordinator, "/"),
		token: cfg.AuthToken,
		http:  &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *apiClient) do(method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, c.base+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach the coordinator at %s: %w", c.base, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return fmt.Errorf("%s", friendly(resp.StatusCode, e.Error))
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// friendly turns the API's terse errors into something a person can act on.
func friendly(code int, msg string) string {
	switch {
	case code == http.StatusUnauthorized:
		return "the coordinator rejected your token - check `lobbycli setup`"
	case code == http.StatusTooManyRequests:
		return "you are going too fast; wait a couple of seconds and try again"
	case strings.Contains(msg, "locked"):
		return "that room is locked and in game - ask the host to reopen it"
	case strings.Contains(msg, "kicked"):
		return "you were kicked from that room; you can return in 5 minutes"
	case strings.Contains(msg, "no free player slot"):
		return "that room is full"
	case strings.Contains(msg, "already in this room"):
		return "you are already in that room"
	case strings.Contains(msg, "only the host"):
		return "only the host can do that"
	}
	return msg
}

func (c *apiClient) createRoom(playerID, name string) (*connectInfo, error) {
	var info connectInfo
	err := c.do("POST", "/v1/rooms", map[string]string{"player_id": playerID, "name": name}, &info)
	return &info, err
}

func (c *apiClient) joinRoom(roomID, playerID string) (*connectInfo, error) {
	var info connectInfo
	err := c.do("POST", "/v1/rooms/"+roomID+"/join", map[string]string{"player_id": playerID}, &info)
	return &info, err
}

func (c *apiClient) listRooms() ([]roomView, error) {
	var out struct {
		Rooms []roomView `json:"rooms"`
	}
	err := c.do("GET", "/v1/rooms", nil, &out)
	return out.Rooms, err
}

func (c *apiClient) getRoom(roomID string) (*roomView, error) {
	var rv roomView
	err := c.do("GET", "/v1/rooms/"+roomID, nil, &rv)
	return &rv, err
}

func (c *apiClient) leaveRoom(roomID, playerID string) error {
	return c.do("POST", "/v1/rooms/"+roomID+"/leave", map[string]string{"player_id": playerID}, nil)
}

func (c *apiClient) kick(roomID, playerID, targetID string) error {
	return c.do("POST", "/v1/rooms/"+roomID+"/kick",
		map[string]string{"player_id": playerID, "target_id": targetID}, nil)
}

func (c *apiClient) setStatus(roomID, playerID, status string) error {
	return c.do("POST", "/v1/rooms/"+roomID+"/status",
		map[string]string{"player_id": playerID, "status": status}, nil)
}
