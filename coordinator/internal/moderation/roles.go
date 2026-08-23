// Package moderation is staff: who they are, what they did, and to whom.
//
// D47 decided the shape and it is worth restating, because getting it wrong is
// painful to unpick later: **a role is granted, so it is a record, not a
// boolean**. A grant knows who gave it, to whom, and when; a withdrawal knows
// the same. Without that, "who made this person an admin?" has no answer,
// which is exactly the question asked after something goes wrong.
//
// Everything else here follows the same rule. A ban, a mute, a timeout, a
// label, a closed room, a changed host, an edited banner — each is a row with
// an author. Powers like D43's without an audit trail are how a moderation
// team loses the trust of its players, and there is no way to reconstruct the
// trail after the fact.
package moderation

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// Role is what somebody is allowed to do.
type Role string

const (
	// RoleNone is an ordinary player.
	RoleNone Role = ""
	// RoleAdmin is staff: everything in D43.
	RoleAdmin Role = "admin"
	// RoleHeadAdmin is the owner. There is exactly one, and only they appoint
	// or remove admins - an admin who could appoint another admin would mean
	// the role spreads and cannot be pulled back.
	RoleHeadAdmin Role = "head_admin"
)

// IsStaff reports whether a role may use the moderation tools at all.
func (r Role) IsStaff() bool { return r == RoleAdmin || r == RoleHeadAdmin }

var (
	ErrNotHeadAdmin  = errors.New("moderation: only the head admin may do that")
	ErrNotStaff      = errors.New("moderation: only an admin may do that")
	ErrAlreadyHeld   = errors.New("moderation: they already hold that role")
	ErrNoSuchGrant   = errors.New("moderation: they do not hold that role")
	ErrHeadAdminSet  = errors.New("moderation: there is already a head admin")
	ErrSelfDemotion  = errors.New("moderation: the head admin cannot remove themselves")
	ErrCannotDemote  = errors.New("moderation: the head admin's role is not granted through the app")
	ErrReasonMissing = errors.New("moderation: say why - an unexplained sanction cannot be reviewed")
)

// Store is roles, sanctions, labels, banners and the audit log.
type Store struct{ db *sql.DB }

func New(db *sql.DB) *Store { return &Store{db: db} }

// --- roles --------------------------------------------------------------

// BootstrapHeadAdmin makes the first head admin.
//
// D47: this happens at deployment, not through the app. A self-service path to
// the most privileged role in the system is a door with no purpose. The grant
// it writes has no author, which is the one and only case of that, and is why
// `granted_by` is nullable.
//
// It refuses if a head admin already exists, so running it twice by accident
// cannot create a second one.
func (s *Store) BootstrapHeadAdmin(accountID string, now time.Time) error {
	existing, err := s.HeadAdmin()
	if err != nil {
		return err
	}
	if existing != "" {
		if existing == accountID {
			return nil
		}
		return ErrHeadAdminSet
	}
	id, err := newID()
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO role_grants (id, account_id, role, granted_by, granted_at)
		 VALUES (?, ?, ?, NULL, ?)`,
		id, accountID, RoleHeadAdmin, stamp(now))
	if err != nil {
		return fmt.Errorf("moderation: bootstrapping head admin: %w", err)
	}
	return s.record(accountID, "bootstrap_head_admin", accountID, "", now)
}

// HeadAdmin returns the head admin's account ID, or empty if there is none.
func (s *Store) HeadAdmin() (string, error) {
	var id string
	err := s.db.QueryRow(
		`SELECT account_id FROM role_grants
		 WHERE role = ? AND revoked_at IS NULL ORDER BY granted_at LIMIT 1`,
		RoleHeadAdmin).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("moderation: reading head admin: %w", err)
	}
	return id, nil
}

// RoleOf returns what somebody currently is.
func (s *Store) RoleOf(accountID string) (Role, error) {
	rows, err := s.db.Query(
		`SELECT role FROM role_grants WHERE account_id = ? AND revoked_at IS NULL`, accountID)
	if err != nil {
		return RoleNone, fmt.Errorf("moderation: reading role: %w", err)
	}
	defer rows.Close()

	role := RoleNone
	for rows.Next() {
		var r Role
		if err := rows.Scan(&r); err != nil {
			return RoleNone, err
		}
		// Head admin outranks admin if somebody somehow holds both.
		if r == RoleHeadAdmin {
			role = RoleHeadAdmin
		} else if role == RoleNone {
			role = r
		}
	}
	return role, rows.Err()
}

// GrantAdmin appoints an admin. Head admin only (D47).
func (s *Store) GrantAdmin(actorID, targetID string, now time.Time) error {
	if err := s.requireHeadAdmin(actorID); err != nil {
		return err
	}
	current, err := s.RoleOf(targetID)
	if err != nil {
		return err
	}
	if current.IsStaff() {
		return ErrAlreadyHeld
	}
	id, err := newID()
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(
		`INSERT INTO role_grants (id, account_id, role, granted_by, granted_at)
		 VALUES (?, ?, ?, ?, ?)`,
		id, targetID, RoleAdmin, actorID, stamp(now)); err != nil {
		return fmt.Errorf("moderation: granting admin: %w", err)
	}
	return s.record(actorID, "grant_admin", targetID, "", now)
}

// RevokeAdmin withdraws an appointment. Head admin only.
//
// The grant is stamped revoked rather than deleted, so the history of who
// appointed whom, and who took it away, survives.
func (s *Store) RevokeAdmin(actorID, targetID string, now time.Time) error {
	if err := s.requireHeadAdmin(actorID); err != nil {
		return err
	}
	if actorID == targetID {
		return ErrSelfDemotion
	}
	res, err := s.db.Exec(
		`UPDATE role_grants SET revoked_by = ?, revoked_at = ?
		 WHERE account_id = ? AND role = ? AND revoked_at IS NULL`,
		actorID, stamp(now), targetID, RoleAdmin)
	if err != nil {
		return fmt.Errorf("moderation: revoking admin: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Distinguish "not an admin" from "is the head admin", because the
		// second is a different conversation.
		if role, _ := s.RoleOf(targetID); role == RoleHeadAdmin {
			return ErrCannotDemote
		}
		return ErrNoSuchGrant
	}
	return s.record(actorID, "revoke_admin", targetID, "", now)
}

// Grant is one appointment, current or withdrawn.
type Grant struct {
	AccountID string    `json:"account_id"`
	Role      Role      `json:"role"`
	GrantedBy string    `json:"granted_by,omitempty"`
	GrantedAt time.Time `json:"granted_at"`
	RevokedBy string    `json:"revoked_by,omitempty"`
	RevokedAt time.Time `json:"revoked_at,omitempty"`
}

// Staff lists everybody who currently holds a role, head admin first.
func (s *Store) Staff() ([]Grant, error) {
	rows, err := s.db.Query(
		`SELECT account_id, role, COALESCE(granted_by, ''), granted_at
		 FROM role_grants WHERE revoked_at IS NULL
		 ORDER BY CASE role WHEN 'head_admin' THEN 0 ELSE 1 END, granted_at`)
	if err != nil {
		return nil, fmt.Errorf("moderation: reading staff: %w", err)
	}
	defer rows.Close()

	out := []Grant{}
	for rows.Next() {
		var (
			g  Grant
			ts string
		)
		if err := rows.Scan(&g.AccountID, &g.Role, &g.GrantedBy, &ts); err != nil {
			return nil, err
		}
		if at, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			g.GrantedAt = at
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// GrantHistory returns every appointment somebody has ever held, including
// withdrawn ones. This is the answer to "who made this person an admin?".
func (s *Store) GrantHistory(accountID string) ([]Grant, error) {
	rows, err := s.db.Query(
		`SELECT account_id, role, COALESCE(granted_by, ''), granted_at,
		        COALESCE(revoked_by, ''), COALESCE(revoked_at, '')
		 FROM role_grants WHERE account_id = ? ORDER BY granted_at`, accountID)
	if err != nil {
		return nil, fmt.Errorf("moderation: reading grant history: %w", err)
	}
	defer rows.Close()

	out := []Grant{}
	for rows.Next() {
		var g Grant
		var granted, revoked string
		if err := rows.Scan(&g.AccountID, &g.Role, &g.GrantedBy, &granted, &g.RevokedBy, &revoked); err != nil {
			return nil, err
		}
		if at, err := time.Parse(time.RFC3339Nano, granted); err == nil {
			g.GrantedAt = at
		}
		if at, err := time.Parse(time.RFC3339Nano, revoked); err == nil {
			g.RevokedAt = at
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) requireHeadAdmin(actorID string) error {
	role, err := s.RoleOf(actorID)
	if err != nil {
		return err
	}
	if role != RoleHeadAdmin {
		return ErrNotHeadAdmin
	}
	return nil
}

// RequireStaff returns an error unless the actor is an admin or the head
// admin. Every moderation entry point calls it.
func (s *Store) RequireStaff(actorID string) error {
	role, err := s.RoleOf(actorID)
	if err != nil {
		return err
	}
	if !role.IsStaff() {
		return ErrNotStaff
	}
	return nil
}

// --- the audit log ------------------------------------------------------

// Action is one thing an admin did.
type Action struct {
	ID      string    `json:"id"`
	ActorID string    `json:"actor_id"`
	Action  string    `json:"action"`
	Subject string    `json:"subject"`
	Detail  string    `json:"detail"`
	At      time.Time `json:"at"`
}

// record writes one line of the audit log. Every mutating method here calls
// it, and none of them succeed without it: an action that is not written down
// did not happen as far as a later review is concerned.
func (s *Store) record(actorID, action, subject, detail string, now time.Time) error {
	id, err := newID()
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO admin_actions (id, actor_id, action, subject, detail, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id, actorID, action, subject, detail, stamp(now))
	if err != nil {
		return fmt.Errorf("moderation: recording action: %w", err)
	}
	return nil
}

// Record writes an action taken outside this package - closing a room,
// changing a host - so the log covers everything in D43 rather than only what
// happens to live here.
func (s *Store) Record(actorID, action, subject, detail string, now time.Time) error {
	if err := s.RequireStaff(actorID); err != nil {
		return err
	}
	return s.record(actorID, action, subject, detail, now)
}

// ActionsBy returns what one admin has done, newest first.
func (s *Store) ActionsBy(actorID string, limit int) ([]Action, error) {
	return s.actions(`SELECT id, actor_id, action, subject, detail, created_at
		FROM admin_actions WHERE actor_id = ? ORDER BY created_at DESC LIMIT ?`, actorID, limit)
}

// ActionsAbout returns what has been done to one player or room, newest
// first. This is the view somebody appealing a ban needs.
func (s *Store) ActionsAbout(subject string, limit int) ([]Action, error) {
	return s.actions(`SELECT id, actor_id, action, subject, detail, created_at
		FROM admin_actions WHERE subject = ? ORDER BY created_at DESC LIMIT ?`, subject, limit)
}

func (s *Store) actions(query, arg string, limit int) ([]Action, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(query, arg, limit)
	if err != nil {
		return nil, fmt.Errorf("moderation: reading the log: %w", err)
	}
	defer rows.Close()

	out := []Action{}
	for rows.Next() {
		var (
			a  Action
			ts string
		)
		if err := rows.Scan(&a.ID, &a.ActorID, &a.Action, &a.Subject, &a.Detail, &ts); err != nil {
			return nil, err
		}
		if at, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			a.At = at
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// --- helpers ------------------------------------------------------------

func newID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("moderation: generating id: %w", err)
	}
	return "m_" + hex.EncodeToString(b), nil
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
