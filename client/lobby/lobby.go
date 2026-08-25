package lobby

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// urlQuery escapes a value for a query string.
func urlQuery(v string) string { return url.QueryEscape(v) }

// SessionHeader carries the signed-in session. It must match the coordinator's
// constant of the same name.
const SessionHeader = "X-LobbyBaz-Session"

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
	Watchers int      `json:"watchers"`
	AvgMMR   int      `json:"avg_mmr"`
	Joinable bool     `json:"joinable"`
	Players  []string `json:"players"`

	// Description is the host's own sentence about the room (D42). It is
	// somebody's free text shown to strangers: render it as text, never as
	// markup.
	Description string `json:"description"`

	// HostRelayMillis is the **host's** round trip to the relay, not this
	// player's. Label it as the host's wherever it appears: a player who
	// reads it as their own ping blames the wrong thing when a game plays
	// badly. Zero means the host has not reported one yet, which is not the
	// same as an excellent connection and must not be drawn as one.
	HostRelayMillis int `json:"host_relay_ms"`

	// The door (D41). Privacy is "public", "password", "friends" or
	// "invite"; NeedsPassword is what the lobby draws a padlock from. The
	// password itself never crosses this boundary in either direction except
	// as something the person typed.
	Privacy       string `json:"privacy"`
	NeedsPassword bool   `json:"needs_password"`
	MinMMR        int    `json:"min_mmr"`
}

// RoomOptions is the door a host wants on a room, at creation or afterwards.
type RoomOptions struct {
	// Privacy is "public", "password", "friends" or "invite". Empty means
	// public.
	Privacy string
	// Password is required when Privacy is "password" and ignored otherwise.
	Password string
	// MinMMR is the floor, or zero for none.
	MinMMR int
}

type Client struct {
	base  string
	token string
	http  *http.Client

	// mu guards session, which is replaced whenever somebody signs in or out
	// and read on every request from whichever goroutine is polling.
	mu      sync.RWMutex
	session string
}

// Account is who the coordinator says you are.
type Account struct {
	PlayerID      string `json:"player_id"`
	Username      string `json:"username"`
	DisplayName   string `json:"display_name"`
	MMR           int    `json:"mmr"`
	Session       string `json:"session"`
	TermsVersion  string `json:"terms_version"`
	TermsAccepted bool   `json:"terms_accepted"`
	// CanRecover is false for every account today. The sign-up screen has to
	// say so before somebody chooses a password, not after they forget it.
	CanRecover bool `json:"can_recover_password"`
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
	if sess := c.Session(); sess != "" {
		req.Header.Set(SessionHeader, sess)
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
	case code == http.StatusUnauthorized && strings.Contains(msg, "sign in"):
		return "you have been signed out - sign in again"
	case code == http.StatusUnauthorized && strings.Contains(msg, "username or password"):
		return "that username and password do not match"
	case code == http.StatusUnauthorized:
		return "this copy of the app was refused by the server; download it again from the link"
	case code == http.StatusForbidden:
		return msg
	case code == http.StatusConflict && strings.Contains(msg, "username"):
		return "that username is taken - choose another"
	case code == http.StatusConflict && strings.Contains(msg, "MMR"):
		return "MMR can only be changed once a week"
	case code == http.StatusTooManyRequests:
		return "you are going too fast; wait a couple of seconds and try again"
	case strings.Contains(msg, "locked"):
		return "that room is locked and in game - ask the host to reopen it"
	case strings.Contains(msg, "kicked"):
		return "you were kicked from that room; try again in a few minutes"
	case strings.Contains(msg, "this room has a password"):
		return "that room needs a password"
	case strings.Contains(msg, "wrong room password"):
		return "that password is not right"
	case strings.Contains(msg, "friends may join"):
		return "that room is for the host's friends only"
	case strings.Contains(msg, "invitation only"):
		return "that room is invitation only - ask the host for an invite"
	case strings.Contains(msg, "MMR is below"):
		return "your MMR is below what that room asks for"
	case strings.Contains(msg, "you are not friends"):
		return "you can only do that with a friend"
	case strings.Contains(msg, "no request to answer"):
		return "there is no friend request from that player"
	case strings.Contains(msg, "no player with that username"):
		return "no player has that username - check the spelling"
	case strings.Contains(msg, "friends list holds at most"):
		return "your friends list is full"
	case strings.Contains(msg, "that is for admins"):
		return "that is for admins"
	case strings.Contains(msg, "only the head admin"):
		return "only the head admin can do that"
	case strings.Contains(msg, "say why"):
		return "give a reason - an unexplained sanction cannot be reviewed"
	case strings.Contains(msg, "your account is banned"),
		strings.Contains(msg, "you are in a timeout"),
		strings.Contains(msg, "you are muted"):
		// Already written for a player to read, and it carries the reason a
		// moderator gave. Passing it through is the whole point.
		return msg
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
	return c.CreateRoomWith(playerID, nick, name, RoomOptions{})
}

// CreateRoomWith opens a room with its door already set.
//
// A host who wanted a private room gets one from the moment it exists.
// Creating it public and locking it a second later is a second during which
// anybody can walk in.
func (c *Client) CreateRoomWith(playerID, nick, name string, opt RoomOptions) (*ConnectInfo, error) {
	var info ConnectInfo
	err := c.do("POST", "/v1/rooms", map[string]any{
		"player_id": playerID,
		"nick":      nick,
		"name":      name,
		"privacy":   opt.Privacy,
		"password":  opt.Password,
		"min_mmr":   opt.MinMMR,
	}, &info)
	return &info, err
}

func (c *Client) JoinRoom(roomID, playerID, nick string) (*ConnectInfo, error) {
	return c.JoinRoomWith(roomID, playerID, nick, "")
}

// JoinRoomWith joins a room, offering a password if it has one.
func (c *Client) JoinRoomWith(roomID, playerID, nick, password string) (*ConnectInfo, error) {
	var info ConnectInfo
	err := c.do("POST", "/v1/rooms/"+roomID+"/join",
		map[string]string{"player_id": playerID, "nick": nick, "password": password}, &info)
	return &info, err
}

// SetPrivacy changes a room's door. Host only.
func (c *Client) SetPrivacy(roomID, playerID string, opt RoomOptions) (*RoomView, error) {
	var rv RoomView
	err := c.do("POST", "/v1/rooms/"+roomID+"/privacy", map[string]any{
		"player_id": playerID,
		"privacy":   opt.Privacy,
		"password":  opt.Password,
		"min_mmr":   opt.MinMMR,
	}, &rv)
	return &rv, err
}

// Invite opens an invite-only room to one person. Host only.
func (c *Client) Invite(roomID, playerID, targetID string) error {
	return c.do("POST", "/v1/rooms/"+roomID+"/invite",
		map[string]any{"player_id": playerID, "target_id": targetID}, nil)
}

// Uninvite withdraws an invitation. It does not remove somebody already
// seated; that is a kick.
func (c *Client) Uninvite(roomID, playerID, targetID string) error {
	return c.do("POST", "/v1/rooms/"+roomID+"/invite",
		map[string]any{"player_id": playerID, "target_id": targetID, "withdraw": true}, nil)
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

// MoveSlot puts a seated player in a different playing slot, which is how
// they change team: slots 0-4 are Radiant and 5-9 are Dire.
func (c *Client) MoveSlot(roomID, playerID string, slot int) error {
	return c.do("POST", "/v1/rooms/"+roomID+"/slot",
		map[string]any{"player_id": playerID, "slot": slot}, nil)
}

func (c *Client) SetStatus(roomID, playerID, status string) error {
	return c.do("POST", "/v1/rooms/"+roomID+"/status",
		map[string]string{"player_id": playerID, "status": status}, nil)
}

// --- accounts -----------------------------------------------------------

// Session returns the session token this client is carrying, if any.
func (c *Client) Session() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.session
}

// UseSession installs a session token, normally one restored from disk after
// a restart. Call Whoami afterwards to find out whether it is still good.
func (c *Client) UseSession(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.session = token
}

func (c *Client) setSession(token string) {
	if token == "" {
		return
	}
	c.UseSession(token)
}

// SignUp creates an account and signs in as it. termsVersion must be the
// version the person was actually shown - the coordinator refuses anything
// else, so a client cannot accept on somebody's behalf by sending a constant.
func (c *Client) SignUp(username, displayName, password, device, termsVersion string) (*Account, error) {
	var a Account
	err := c.do("POST", "/v1/auth/signup", map[string]string{
		"username":             username,
		"display_name":         displayName,
		"password":             password,
		"device":               device,
		"accept_terms_version": termsVersion,
	}, &a)
	if err != nil {
		return nil, err
	}
	c.setSession(a.Session)
	return &a, nil
}

// SignIn exchanges a username and password for a session.
func (c *Client) SignIn(username, password, device string) (*Account, error) {
	var a Account
	err := c.do("POST", "/v1/auth/login", map[string]string{
		"username": username,
		"password": password,
		"device":   device,
	}, &a)
	if err != nil {
		return nil, err
	}
	c.setSession(a.Session)
	return &a, nil
}

// Whoami reports who the current session belongs to. An error means the
// session is gone and the person has to sign in again.
func (c *Client) Whoami() (*Account, error) {
	var a Account
	if err := c.do("GET", "/v1/auth/me", nil, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// SignOut ends this device's session. The local token is cleared whatever the
// server says: a client that keeps a token the person asked it to forget is
// worse than one that fails to tell the server.
func (c *Client) SignOut() error {
	err := c.do("POST", "/v1/auth/logout", nil, nil)
	c.mu.Lock()
	c.session = ""
	c.mu.Unlock()
	return err
}

// ChangePassword replaces the password and adopts the session it returns,
// since changing a password signs every device out including this one.
func (c *Client) ChangePassword(current, next string) (*Account, error) {
	var a Account
	if err := c.do("POST", "/v1/auth/password", map[string]string{
		"current_password": current,
		"new_password":     next,
	}, &a); err != nil {
		return nil, err
	}
	c.setSession(a.Session)
	return &a, nil
}

// AcceptTerms records agreement to a version of the terms.
func (c *Client) AcceptTerms(version string) error {
	return c.do("POST", "/v1/terms/accept", map[string]string{"version": version}, nil)
}

// Describe sets the host's sentence about their room (D42). Only the host may.
func (c *Client) Describe(roomID, description string) (*RoomView, error) {
	var rv RoomView
	err := c.do("POST", "/v1/rooms/"+roomID+"/description",
		map[string]any{"description": description}, &rv)
	return &rv, err
}

// ServerInfo is what a coordinator can do, asked before anything is asked of
// it. A client that knows there are no accounts can decline to offer a sign-in
// screen, rather than offering one and showing the player an error for a thing
// they never chose to do.
type ServerInfo struct {
	OK           bool   `json:"ok"`
	Rooms        int    `json:"rooms"`
	Accounts     bool   `json:"accounts"`
	Friends      bool   `json:"friends"`
	TermsVersion string `json:"terms_version"`
}

// Info asks what the server supports. It needs no session and no account.
func (c *Client) Info() (*ServerInfo, error) {
	var out ServerInfo
	if err := c.do("GET", "/healthz", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// TermsOfUse is the agreement a new account is asked to accept, and the
// version it is. Both come from the server: the version somebody accepted is
// recorded there, so the words and the version must not be able to disagree.
type TermsOfUse struct {
	Version string `json:"version"`
	Text    string `json:"text"`
}

// Terms fetches them. No session and no account: they are read before there
// is an account to read them with.
func (c *Client) Terms() (*TermsOfUse, error) {
	var out TermsOfUse
	if err := c.do("GET", "/v1/terms", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
