package moderation

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Sanctions: ban, mute, timeout (D43).
//
// Three separate things rather than one severity dial, because they stop
// different things and a moderator chooses between them for a reason:
//
//   - **Ban** stops somebody using the platform at all. The account is
//     disabled and every session dropped, so it takes effect now rather than
//     the next time they happen to restart.
//   - **Mute** stops them talking. They can still play, which matters: most
//     of what makes a lobby unpleasant is said, not done, and removing
//     somebody's voice is a smaller thing than removing them.
//   - **Timeout** stops them joining rooms for a while. It is the cooling-off
//     sanction, and unlike a ban it ends by itself.
//
// Every one of them expires by default. A permanent sanction is possible and
// has to be asked for explicitly, because "forever" should be a decision
// somebody made rather than the value a form happened to have.

// Kind is which sanction this is.
type Kind string

const (
	KindBan     Kind = "ban"
	KindMute    Kind = "mute"
	KindTimeout Kind = "timeout"
)

// Valid reports whether k is one of the three.
func (k Kind) Valid() bool { return k == KindBan || k == KindMute || k == KindTimeout }

// MaxReason keeps a reason readable in a list.
const MaxReason = 300

var (
	ErrBadKind    = errors.New("moderation: a sanction is a ban, a mute or a timeout")
	ErrNoSanction = errors.New("moderation: there is no such sanction to lift")
)

// Sanction is one ban, mute or timeout.
type Sanction struct {
	ID        string    `json:"id"`
	AccountID string    `json:"account_id"`
	Kind      Kind      `json:"kind"`
	Reason    string    `json:"reason"`
	ActorID   string    `json:"actor_id"`
	At        time.Time `json:"at"`
	// Until is zero for a sanction that does not expire on its own.
	Until    time.Time `json:"until,omitempty"`
	LiftedBy string    `json:"lifted_by,omitempty"`
	LiftedAt time.Time `json:"lifted_at,omitempty"`
}

// Active reports whether this sanction is in force at a moment.
func (s Sanction) Active(now time.Time) bool {
	if !s.LiftedAt.IsZero() {
		return false
	}
	if s.Until.IsZero() {
		return true
	}
	return now.UTC().Before(s.Until)
}

// Sanction applies a ban, mute or timeout.
//
// A zero duration means it does not expire. A reason is required: an
// unexplained sanction cannot be reviewed, appealed, or defended by the
// moderator who gave it.
func (s *Store) Sanction(actorID, targetID string, kind Kind, reason string, for_ time.Duration, now time.Time) (Sanction, error) {
	if err := s.RequireStaff(actorID); err != nil {
		return Sanction{}, err
	}
	if !kind.Valid() {
		return Sanction{}, ErrBadKind
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Sanction{}, ErrReasonMissing
	}
	if len([]rune(reason)) > MaxReason {
		reason = string([]rune(reason)[:MaxReason])
	}

	// Staff are not immune, but an admin cannot sanction the head admin, and
	// an ordinary admin cannot sanction another admin. Otherwise a single
	// compromised or angry admin could remove the rest of the team.
	if err := s.canActAgainst(actorID, targetID); err != nil {
		return Sanction{}, err
	}

	id, err := newID()
	if err != nil {
		return Sanction{}, err
	}
	out := Sanction{
		ID: id, AccountID: targetID, Kind: kind,
		Reason: reason, ActorID: actorID, At: now.UTC(),
	}
	var until any
	if for_ > 0 {
		out.Until = now.UTC().Add(for_)
		until = stamp(out.Until)
	}
	if _, err := s.db.Exec(
		`INSERT INTO sanctions (id, account_id, kind, reason, actor_id, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, targetID, kind, reason, actorID, stamp(now), until); err != nil {
		return Sanction{}, fmt.Errorf("moderation: applying sanction: %w", err)
	}

	detail := reason
	if for_ > 0 {
		detail = fmt.Sprintf("%s (for %s)", reason, for_)
	}
	if err := s.record(actorID, string(kind), targetID, detail, now); err != nil {
		return Sanction{}, err
	}
	return out, nil
}

// Lift ends a sanction early.
//
// The row is stamped rather than deleted. A moderation table you can erase by
// undoing things is not a record, and "who lifted this, and when" is as much
// part of the history as who applied it.
func (s *Store) Lift(actorID, sanctionID string, now time.Time) error {
	if err := s.RequireStaff(actorID); err != nil {
		return err
	}
	var targetID string
	err := s.db.QueryRow(`SELECT account_id FROM sanctions WHERE id = ? AND lifted_at IS NULL`,
		sanctionID).Scan(&targetID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoSanction
	}
	if err != nil {
		return fmt.Errorf("moderation: %w", err)
	}
	if _, err := s.db.Exec(
		`UPDATE sanctions SET lifted_by = ?, lifted_at = ? WHERE id = ?`,
		actorID, stamp(now), sanctionID); err != nil {
		return fmt.Errorf("moderation: lifting sanction: %w", err)
	}
	return s.record(actorID, "lift", targetID, sanctionID, now)
}

// ActiveSanctions returns everything currently in force against somebody.
func (s *Store) ActiveSanctions(accountID string, now time.Time) ([]Sanction, error) {
	all, err := s.Sanctions(accountID)
	if err != nil {
		return nil, err
	}
	out := []Sanction{}
	for _, one := range all {
		if one.Active(now) {
			out = append(out, one)
		}
	}
	return out, nil
}

// Sanctions returns somebody's whole history, newest first, including lifted
// and expired ones.
func (s *Store) Sanctions(accountID string) ([]Sanction, error) {
	rows, err := s.db.Query(
		`SELECT id, account_id, kind, reason, actor_id, created_at,
		        COALESCE(expires_at, ''), COALESCE(lifted_by, ''), COALESCE(lifted_at, '')
		 FROM sanctions WHERE account_id = ? ORDER BY created_at DESC`, accountID)
	if err != nil {
		return nil, fmt.Errorf("moderation: reading sanctions: %w", err)
	}
	defer rows.Close()

	out := []Sanction{}
	for rows.Next() {
		var (
			one                      Sanction
			created, until, liftedAt string
		)
		if err := rows.Scan(&one.ID, &one.AccountID, &one.Kind, &one.Reason, &one.ActorID,
			&created, &until, &one.LiftedBy, &liftedAt); err != nil {
			return nil, err
		}
		one.At = parse(created)
		one.Until = parse(until)
		one.LiftedAt = parse(liftedAt)
		out = append(out, one)
	}
	return out, rows.Err()
}

// Restriction is the answer to "what is this person currently barred from",
// in the form the rest of the coordinator asks it.
type Restriction struct {
	Banned  bool `json:"banned"`
	Muted   bool `json:"muted"`
	Timeout bool `json:"timeout"`
	// Until is the soonest moment any of the above stops applying. Zero means
	// at least one of them is permanent.
	Until  time.Time `json:"until,omitempty"`
	Reason string    `json:"reason,omitempty"`
}

// Restrictions collapses somebody's active sanctions into what they cannot do.
func (s *Store) Restrictions(accountID string, now time.Time) (Restriction, error) {
	active, err := s.ActiveSanctions(accountID, now)
	if err != nil {
		return Restriction{}, err
	}
	var (
		out       Restriction
		permanent bool
		latest    time.Time
	)
	for _, one := range active {
		switch one.Kind {
		case KindBan:
			out.Banned = true
		case KindMute:
			out.Muted = true
		case KindTimeout:
			out.Timeout = true
		}
		if out.Reason == "" {
			out.Reason = one.Reason
		}
		// Until is when everything stops applying, so it is the latest of the
		// expiries - and absent entirely if any one of them is permanent.
		if one.Until.IsZero() {
			permanent = true
		} else if one.Until.After(latest) {
			latest = one.Until
		}
	}
	if !permanent {
		out.Until = latest
	}
	return out, nil
}

// canActAgainst stops staff being used against each other.
//
// Nobody touches the head admin. An ordinary admin does not touch another
// admin - only the head admin does - so a single compromised or angry admin
// cannot remove the rest of the team.
func (s *Store) canActAgainst(actorID, targetID string) error {
	if actorID == targetID {
		return nil
	}
	targetRole, err := s.RoleOf(targetID)
	if err != nil {
		return err
	}
	if targetRole == RoleHeadAdmin {
		return ErrNotHeadAdmin
	}
	if targetRole == RoleAdmin {
		actorRole, err := s.RoleOf(actorID)
		if err != nil {
			return err
		}
		if actorRole != RoleHeadAdmin {
			return ErrNotHeadAdmin
		}
	}
	return nil
}

func parse(ts string) time.Time {
	if ts == "" {
		return time.Time{}
	}
	at, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Time{}
	}
	return at
}
