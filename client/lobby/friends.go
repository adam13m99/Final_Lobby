package lobby

import "time"

// The friends rail, from the client's side (D41).

// Friend is one person on the rail.
type Friend struct {
	PlayerID    string `json:"player_id"`
	DisplayName string `json:"display_name"`
	MMR         int    `json:"mmr"`

	// State is "accepted" or "requested". Incoming distinguishes a request
	// waiting for my answer from one waiting for theirs - they need entirely
	// different buttons, so do not collapse them.
	State    string `json:"state"`
	Incoming bool   `json:"incoming"`

	Online bool `json:"online"`
	InGame bool `json:"in_game"`
	// RoomID is where they are, so "join my friend" is one click.
	RoomID string `json:"room_id"`
	Unread int    `json:"unread"`
	// LastSeen is when an offline friend was last here. Absent for anybody
	// online, and absent for anybody this server has never recorded - which
	// the rail must show as nothing, not as a date in 1970.
	//
	// A pointer because the app re-encodes this list for its own page:
	// `omitempty` does not suppress a zero time.Time, so a plain field would
	// put 0001-01-01 on the wire for everybody the server said nothing about.
	LastSeen *time.Time `json:"last_seen,omitempty"`
}

// RoomInvitation is a friend telling you a room is open for you.
type RoomInvitation struct {
	ID     int64     `json:"id"`
	RoomID string    `json:"room_id"`
	FromID string    `json:"from_id"`
	At     time.Time `json:"at"`
}

// PrivateMessage is one line of a private conversation.
type PrivateMessage struct {
	ID     int64     `json:"id"`
	FromID string    `json:"from_id"`
	ToID   string    `json:"to_id"`
	Body   string    `json:"body"`
	At     time.Time `json:"at"`
	Read   bool      `json:"read"`
}

// FriendList is the whole rail in one call, which is what the lobby draws.
type FriendList struct {
	Friends     []Friend         `json:"friends"`
	Incoming    []Friend         `json:"incoming"`
	Outgoing    []Friend         `json:"outgoing"`
	Blocked     []Friend         `json:"blocked"`
	Invitations []RoomInvitation `json:"invitations"`
}

// Friends returns the rail.
func (c *Client) Friends() (*FriendList, error) {
	var out FriendList
	if err := c.do("GET", "/v1/friends", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// FindPlayer looks somebody up by their exact username, so a friend request
// has an address. There is no partial search on purpose: it would be a
// directory of everybody on the platform.
func (c *Client) FindPlayer(username string) (*Friend, error) {
	var f Friend
	if err := c.do("GET", "/v1/players/find?username="+urlQuery(username), nil, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// AddFriend sends a request. If they have already asked you, this accepts.
func (c *Client) AddFriend(targetID string) error { return c.friendAction(targetID, "request") }

// AcceptFriend answers a pending request.
func (c *Client) AcceptFriend(targetID string) error { return c.friendAction(targetID, "accept") }

// DeclineFriend throws a request away. The sender is not told.
func (c *Client) DeclineFriend(targetID string) error { return c.friendAction(targetID, "decline") }

// RemoveFriend ends a friendship, in both directions.
func (c *Client) RemoveFriend(targetID string) error { return c.friendAction(targetID, "remove") }

// BlockPlayer stops somebody reaching you, and ends any friendship.
//
// Note for whoever writes the UI: this returns success whether or not the
// other person notices anything, and that is deliberate throughout - a block
// is silent, so it cannot be detected by watching for an error.
func (c *Client) BlockPlayer(targetID string) error { return c.friendAction(targetID, "block") }

// UnblockPlayer lets somebody reach you again. It does not restore the
// friendship; that has to be asked for afresh.
func (c *Client) UnblockPlayer(targetID string) error { return c.friendAction(targetID, "unblock") }

func (c *Client) friendAction(targetID, action string) error {
	return c.do("POST", "/v1/friends",
		map[string]string{"target_id": targetID, "action": action}, nil)
}

// Conversation reads a private conversation, and sends a message first if one
// is given. Reading is what marks it read.
//
// after is the highest message ID already held, so a poll carries only what is
// new. Pass zero for the whole conversation.
func (c *Client) Conversation(targetID, send string, after int64) ([]PrivateMessage, error) {
	var out struct {
		Messages []PrivateMessage `json:"messages"`
	}
	err := c.do("POST", "/v1/friends/messages", map[string]any{
		"target_id": targetID,
		"body":      send,
		"after":     after,
	}, &out)
	return out.Messages, err
}

// InviteFriend tells a friend a room is open for them. If you host that room
// and it is invite-only, it opens the door as well.
func (c *Client) InviteFriend(targetID, roomID string) error {
	return c.do("POST", "/v1/friends/invite",
		map[string]string{"target_id": targetID, "room_id": roomID}, nil)
}

// InvitationsSeen clears the invitation badge.
func (c *Client) InvitationsSeen() error {
	return c.do("POST", "/v1/friends/invitations/seen", nil, nil)
}
