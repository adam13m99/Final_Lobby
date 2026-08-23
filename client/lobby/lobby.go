package lobby

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
type ConnectInfo struct {
	RoomID      string `json:"room_id"`
	Slot        int    `json:"slot"`
	IsHost      bool   `json:"is_host"`
	IsSpectator bool   `json:"is_spectator"`
	VirtualIP   string `json:"virtual_ip"`
	HostIP      string `json:"host_ip"`
	Subnet      string `json:"subnet"`
	Ticket      string `json:"ticket"`
	RelayAddr   string `json:"relay_addr"`
	RelayPub    string `json:"relay_pub"`
	DotaConnect string `json:"dota_connect"`
}

type RoomView struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Status   string   `json:"status"`
	HostID   string   `json:"host_id"`
	HostNick string   `json:"host_nick"`
	Members  []Member `json:"members"`
	Seats    int      `json:"seats"`
	Free     int      `json:"free_slots"`
	AvgMMR   int      `json:"avg_mmr"`
	Joinable bool     `json:"joinable"`
	Players  []string `json:"players"`
}

type Client struct {
	base  string
	token string
	http  *http.Client
}

func New(coordinator, token string) *Client {
	return &Client{
		base:  strings.TrimRight(coordinator, "/"),
		token: token,
		http:  &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *Client) do(method, path string, body any, out any) error {
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
		return "this copy of the app was refused by the server; download it again from the link"
	case code == http.StatusConflict && strings.Contains(msg, "MMR"):
		return "MMR can only be changed once a week"
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
	case strings.Contains(msg, "no free spectator seat"):
		return "all the spectator seats in that room are taken"
	case strings.Contains(msg, "no such room"):
		return "that room has closed"
	case strings.Contains(msg, "not in that room"):
		return "you are no longer in that room; join it again"
	}
	return msg
}

func (c *Client) CreateRoom(playerID, nick, name string) (*ConnectInfo, error) {
	var info ConnectInfo
	err := c.do("POST", "/v1/rooms",
		map[string]string{"player_id": playerID, "nick": nick, "name": name}, &info)
	return &info, err
}

func (c *Client) JoinRoom(roomID, playerID, nick string) (*ConnectInfo, error) {
	var info ConnectInfo
	err := c.do("POST", "/v1/rooms/"+roomID+"/join",
		map[string]string{"player_id": playerID, "nick": nick}, &info)
	return &info, err
}

// Refresh mints a new ticket for a player already seated in a room.
//
// Connect calls this every time rather than reusing what join returned: a
// ticket expires in ten minutes and rooms sit open for far longer.
func (c *Client) Refresh(roomID, playerID string) (*ConnectInfo, error) {
	var info ConnectInfo
	err := c.do("POST", "/v1/rooms/"+roomID+"/connect",
		map[string]string{"player_id": playerID}, &info)
	return &info, err
}

func (c *Client) ListRooms() ([]RoomView, error) {
	var out struct {
		Rooms []RoomView `json:"rooms"`
	}
	err := c.do("GET", "/v1/rooms", nil, &out)
	return out.Rooms, err
}

func (c *Client) GetRoom(roomID string) (*RoomView, error) {
	var rv RoomView
	err := c.do("GET", "/v1/rooms/"+roomID, nil, &rv)
	return &rv, err
}

func (c *Client) LeaveRoom(roomID, playerID string) error {
	return c.do("POST", "/v1/rooms/"+roomID+"/leave", map[string]string{"player_id": playerID}, nil)
}

func (c *Client) Kick(roomID, playerID, targetID string) error {
	return c.do("POST", "/v1/rooms/"+roomID+"/kick",
		map[string]string{"player_id": playerID, "target_id": targetID}, nil)
}

func (c *Client) SetStatus(roomID, playerID, status string) error {
	return c.do("POST", "/v1/rooms/"+roomID+"/status",
		map[string]string{"player_id": playerID, "status": status}, nil)
}
