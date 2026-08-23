package room

import (
	"errors"
	"time"

	"lobbybaz/coordinator/internal/secret"
)

// Privacy is the door on a room (D41).
//
// All four kinds ship, as the original spec promised. The owner chose them
// knowing what it implied: friends-only and invite-only rooms are meaningless
// without a friends list, so choosing all four was choosing to build friends
// before launch.
type Privacy string

const (
	// PrivacyPublic is the default: anybody who meets the MMR floor.
	PrivacyPublic Privacy = "public"
	// PrivacyPassword admits anybody who knows the room's password. This is
	// how a group organising in a chat elsewhere keeps its game to itself
	// without everybody having to be friends first.
	PrivacyPassword Privacy = "password"
	// PrivacyFriends admits the host's friends.
	PrivacyFriends Privacy = "friends"
	// PrivacyInvite admits only people the host has invited by name.
	PrivacyInvite Privacy = "invite"
)

// Valid reports whether p is one of the four kinds.
func (p Privacy) Valid() bool {
	switch p {
	case PrivacyPublic, PrivacyPassword, PrivacyFriends, PrivacyInvite:
		return true
	}
	return false
}

// NeedsPassword reports whether joining this kind of room requires typing
// something. The lobby uses it to draw a padlock.
func (p Privacy) NeedsPassword() bool { return p == PrivacyPassword }

var (
	ErrWrongRoomPassword = errors.New("room: wrong room password")
	ErrNeedRoomPassword  = errors.New("room: this room has a password")
	ErrFriendsOnly       = errors.New("room: only the host's friends may join")
	ErrInviteOnly        = errors.New("room: this room is invitation only")
	ErrMMRTooLow         = errors.New("room: your MMR is below this room's minimum")
	ErrBadPrivacy        = errors.New("room: not a kind of room")
	ErrBadMinMMR         = errors.New("room: minimum MMR must be between 0 and 15000")
	ErrPasswordRequired  = errors.New("room: a password room needs a password")
)

// MaxMinMMR is the ceiling on a room's MMR floor. Above the highest real Dota
// rating, so a host cannot set a number nobody alive could meet.
const MaxMinMMR = 15000

// Applicant is somebody asking to come in, and everything the door needs to
// decide.
//
// It is a struct rather than four more arguments because every field here is
// something the *coordinator* establishes, never something the client claims.
// MMR comes from the account row, Friend from the friend graph, Invited from
// the room's own invite list. The only field the person types is Password.
type Applicant struct {
	ID  string
	MMR int

	// Password is what the person typed at the padlock, if anything.
	Password string

	// Friend reports whether this applicant is on the host's friends list.
	Friend bool

	// Admin lets staff past the door. An admin is called in precisely when
	// something has gone wrong inside a room they were never invited to, and
	// a moderator who can be locked out by a password is not a moderator.
	// It never bypasses a kick block - see admissible.
	Admin bool
}

// Anyone is an applicant with nothing to offer: no password, no friendship,
// no invitation. It is what a public room takes, and it makes a test that
// uses it say plainly that no credential was supplied.
func Anyone(id string) Applicant { return Applicant{ID: id} }

// knock decides whether an applicant gets past the door.
//
// Order matters and is deliberate. The MMR floor is checked first because it
// is the only refusal that is about the person rather than about a secret,
// and telling somebody "your MMR is too low" before "wrong password" saves
// them typing a password that was never going to help.
func (r *Room) knock(who Applicant) error {
	if who.Admin {
		return nil
	}
	if r.MinMMR > 0 && who.MMR < r.MinMMR {
		return ErrMMRTooLow
	}

	switch r.Privacy {
	case PrivacyPassword:
		if who.Password == "" {
			return ErrNeedRoomPassword
		}
		if err := secret.VerifyPassword(r.passwordHash, who.Password); err != nil {
			return ErrWrongRoomPassword
		}
	case PrivacyFriends:
		if !who.Friend {
			return ErrFriendsOnly
		}
	case PrivacyInvite:
		if _, invited := r.Invites[who.ID]; !invited {
			return ErrInviteOnly
		}
	}
	return nil
}

// SetPrivacy changes the door. Host only.
//
// Changing the kind never evicts anybody already seated. Somebody who joined
// a public room and is now in a friends-only one stays: they were let in
// legitimately, and throwing them out mid-lobby because the host changed a
// setting is the kind of surprise that makes a host afraid to touch settings.
func (r *Room) SetPrivacy(actorID string, p Privacy, password string, minMMR int, now time.Time) error {
	if actorID != r.HostID {
		return ErrNotHost
	}
	if r.Status == StatusClosed {
		return ErrRoomClosed
	}
	if !p.Valid() {
		return ErrBadPrivacy
	}
	if minMMR < 0 || minMMR > MaxMinMMR {
		return ErrBadMinMMR
	}

	if p == PrivacyPassword {
		switch {
		case password != "":
			hash, err := secret.HashPassword(password)
			if err != nil {
				return err
			}
			r.passwordHash = hash
		case r.passwordHash == "":
			// A password room with no password is a public room that looks
			// locked, which is worse than either.
			return ErrPasswordRequired
		}
	} else {
		// Leaving the old hash behind would mean flipping back to "password"
		// silently restores a password nobody remembers setting.
		r.passwordHash = ""
	}

	r.Privacy = p
	r.MinMMR = minMMR
	return nil
}

// Invite opens the door to one person by name. Host only.
//
// Any member may *send* an invitation as a message (T7); only the host's
// invitation admits somebody to an invite-only room. Otherwise one person
// getting in would be enough to let the rest of their group in, and the door
// would mean nothing.
//
// An invitation lasts as long as the room. Rooms end when their host leaves,
// so there is nothing for a stale invitation to unlock.
func (r *Room) Invite(actorID, targetID string, now time.Time) error {
	if actorID != r.HostID {
		return ErrNotHost
	}
	if r.Status == StatusClosed {
		return ErrRoomClosed
	}
	if r.Invites == nil {
		r.Invites = make(map[string]time.Time)
	}
	r.Invites[targetID] = now
	return nil
}

// Uninvite withdraws an invitation. It does not remove somebody already
// seated; that is a kick.
func (r *Room) Uninvite(actorID, targetID string) error {
	if actorID != r.HostID {
		return ErrNotHost
	}
	delete(r.Invites, targetID)
	return nil
}

// Invited reports whether somebody has been invited.
func (r *Room) Invited(playerID string) bool {
	_, ok := r.Invites[playerID]
	return ok
}

// HasPassword reports whether a password is set, without revealing it.
func (r *Room) HasPassword() bool { return r.passwordHash != "" }
