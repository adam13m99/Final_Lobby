package lobby

import "time"

// The moderation tools, from the client's side (D43, D47).
//
// Every call here is refused with a plain "that is for admins" unless the
// signed-in account holds a role, so a client may simply offer them and let
// the server decide - but a UI that shows a ban button to everybody and then
// tells them no is a worse UI than one that asks first. Whoami's account and
// Staff() together are enough to know whether to draw the tools at all.

// Banner is one slide of the announcement strip.
//
// Render Title and Body as **text, never as HTML**. This is content one person
// writes and every other person's client displays, which is the exact shape of
// a stored scripting hole. The server restricts LinkURL to http and https for
// the same reason.
type Banner struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	ImageURL string `json:"image_url"`
	LinkURL  string `json:"link_url"`
	Sort     int    `json:"sort"`
	Active   bool   `json:"active"`

	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedBy string    `json:"updated_by"`
	UpdatedAt time.Time `json:"updated_at"`
}

// StaffMember is somebody holding a role.
type StaffMember struct {
	AccountID   string    `json:"account_id"`
	DisplayName string    `json:"display_name"`
	Role        string    `json:"role"` // "admin" or "head_admin"
	GrantedBy   string    `json:"granted_by"`
	GrantedAt   time.Time `json:"granted_at"`
}

// Sanction is one ban, mute or timeout.
type Sanction struct {
	ID        string    `json:"id"`
	AccountID string    `json:"account_id"`
	Kind      string    `json:"kind"` // "ban", "mute" or "timeout"
	Reason    string    `json:"reason"`
	ActorID   string    `json:"actor_id"`
	At        time.Time `json:"at"`
	// Until is absent for a sanction that does not expire on its own.
	Until    time.Time `json:"until"`
	LiftedBy string    `json:"lifted_by"`
	LiftedAt time.Time `json:"lifted_at"`
}

// AdminAction is one line of the audit log.
type AdminAction struct {
	ID      string    `json:"id"`
	ActorID string    `json:"actor_id"`
	Action  string    `json:"action"`
	Subject string    `json:"subject"`
	Detail  string    `json:"detail"`
	At      time.Time `json:"at"`
}

// Restriction is what somebody is currently barred from.
type Restriction struct {
	Banned  bool      `json:"banned"`
	Muted   bool      `json:"muted"`
	Timeout bool      `json:"timeout"`
	Until   time.Time `json:"until"`
	Reason  string    `json:"reason"`
}

// PlayerRecord is everything staff need about one person in one place.
type PlayerRecord struct {
	PlayerID    string        `json:"player_id"`
	DisplayName string        `json:"display_name"`
	Sanctions   []Sanction    `json:"sanctions"`
	Labels      []string      `json:"labels"`
	Actions     []AdminAction `json:"actions"`
	Restriction Restriction   `json:"restriction"`
	Kicks       int           `json:"kicks_this_week"`
}

// --- the public half ----------------------------------------------------

// Banners returns the announcement strip. It needs no account: somebody who
// has just installed the app is exactly who an announcement about signing up
// is for.
func (c *Client) Banners() ([]Banner, error) {
	var out struct {
		Banners []Banner `json:"banners"`
	}
	err := c.do("GET", "/v1/banners", nil, &out)
	return out.Banners, err
}

// Labels returns the set of player labels this server recognises, so nothing
// in the client hard-codes the list and removing one is a server change.
func (c *Client) Labels() ([]string, error) {
	var out struct {
		Labels []string `json:"labels"`
	}
	err := c.do("GET", "/v1/admin/labels", nil, &out)
	return out.Labels, err
}

// --- staff --------------------------------------------------------------

// Staff lists everybody holding a role, head admin first.
func (c *Client) Staff() ([]StaffMember, error) {
	var out struct {
		Staff []StaffMember `json:"staff"`
	}
	err := c.do("GET", "/v1/admin/staff", nil, &out)
	return out.Staff, err
}

// GrantAdmin appoints an admin. Head admin only (D47) - an admin who could
// appoint another admin would mean the role spreads and cannot be pulled back.
func (c *Client) GrantAdmin(targetID string) error {
	return c.do("POST", "/v1/admin/staff",
		map[string]any{"target_id": targetID, "grant": true}, nil)
}

// RevokeAdmin withdraws an appointment. Head admin only.
func (c *Client) RevokeAdmin(targetID string) error {
	return c.do("POST", "/v1/admin/staff",
		map[string]any{"target_id": targetID, "grant": false}, nil)
}

// Sanction bans, mutes or times somebody out.
//
// A reason is required: an unexplained sanction cannot be reviewed, appealed,
// or defended by the moderator who gave it. A zero duration means it does not
// expire, and the UI should make that a deliberate choice rather than the
// value an empty field happens to have.
func (c *Client) Sanction(targetID, kind, reason string, howLong time.Duration) (*Sanction, error) {
	var out Sanction
	err := c.do("POST", "/v1/admin/sanction", map[string]any{
		"target_id": targetID,
		"kind":      kind,
		"reason":    reason,
		"minutes":   int(howLong.Minutes()),
	}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// LiftSanction ends one early. The record survives; it is stamped, not
// deleted.
func (c *Client) LiftSanction(sanctionID, targetID string) error {
	return c.do("POST", "/v1/admin/sanction/lift",
		map[string]any{"sanction_id": sanctionID, "target_id": targetID}, nil)
}

// PlayerRecord returns one person's moderation history.
func (c *Client) PlayerRecord(playerID string) (*PlayerRecord, error) {
	var out PlayerRecord
	if err := c.do("GET", "/v1/admin/players/"+playerID, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// LabelPlayer puts a visible mark on somebody. Attributed, like everything
// else: it should always be possible to ask who put it there.
func (c *Client) LabelPlayer(targetID, label string) error {
	return c.do("POST", "/v1/admin/label",
		map[string]any{"target_id": targetID, "label": label}, nil)
}

// UnlabelPlayer removes a mark.
func (c *Client) UnlabelPlayer(targetID, label string) error {
	return c.do("POST", "/v1/admin/label",
		map[string]any{"target_id": targetID, "label": label, "remove": true}, nil)
}

// CloseRoom ends somebody else's room.
func (c *Client) CloseRoom(roomID, reason string) error {
	return c.do("POST", "/v1/admin/rooms/"+roomID+"/close",
		map[string]any{"reason": reason}, nil)
}

// ChangeHost hands a room to somebody else who is playing in it.
//
// This does not rescue a match in progress - the Dota server was running on
// the old host's PC and it is gone. What it saves is the room and the
// arrangement the people in it made to play together.
func (c *Client) ChangeHost(roomID, newHostID, reason string) (*RoomView, error) {
	var rv RoomView
	err := c.do("POST", "/v1/admin/rooms/"+roomID+"/host",
		map[string]any{"new_host_id": newHostID, "reason": reason}, &rv)
	return &rv, err
}

// SaveBanner adds a slide, or edits one if b.ID is set.
func (c *Client) SaveBanner(b Banner) (*Banner, error) {
	var out Banner
	err := c.do("POST", "/v1/admin/banners", map[string]any{
		"id":        b.ID,
		"title":     b.Title,
		"body":      b.Body,
		"image_url": b.ImageURL,
		"link_url":  b.LinkURL,
		"sort":      b.Sort,
		"active":    b.Active,
	}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// RemoveBanner deletes a slide.
func (c *Client) RemoveBanner(bannerID string) error {
	return c.do("POST", "/v1/admin/banners",
		map[string]any{"id": bannerID, "remove": true}, nil)
}

// AuditLog returns what one admin has done, or what has been done to one
// player or room. Exactly one of actorID and subject should be set.
func (c *Client) AuditLog(actorID, subject string) ([]AdminAction, error) {
	path := "/v1/admin/log?"
	if actorID != "" {
		path += "actor=" + urlQuery(actorID)
	} else {
		path += "subject=" + urlQuery(subject)
	}
	var out struct {
		Actions []AdminAction `json:"actions"`
	}
	err := c.do("GET", path, nil, &out)
	return out.Actions, err
}
