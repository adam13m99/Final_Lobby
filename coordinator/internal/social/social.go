// Package social is the friend graph: who knows whom, who has blocked whom,
// and what they say to each other privately.
//
// D41 chose it before launch rather than after, and that was the consistent
// choice: friends-only and invite-only rooms are meaningless without a friends
// list, so choosing all four kinds of room was choosing to build this first.
//
// Two shapes are worth understanding before reading the code.
//
// **A friendship is two directed rows.** One row means "A has asked B"; two
// rows mean they are friends. Storing it that way rather than as an
// undirected pair is what makes a pending request expressible at all, and it
// makes removing a friend one deletion rather than a search for whichever
// ordering happened to be stored.
//
// **A block is not the absence of a friendship.** It is its own row, it is
// one-directional, and it outranks everything: somebody you blocked cannot
// message you, invite you, or discover that you blocked them by watching a
// request go unanswered. Their request is accepted by the API and dropped.
package social

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// MaxMessage is the length of a private message, in characters. Long
	// enough for a sentence and a room name, short enough that the message
	// list stays a list.
	MaxMessage = 500

	// MaxFriends is a ceiling, not a target. It exists so one account cannot
	// turn the friend table into a denial of service by adding everybody.
	MaxFriends = 200
)

var (
	ErrSelf           = errors.New("social: you cannot do that to yourself")
	ErrBlocked        = errors.New("social: that is not possible")
	ErrNotFriends     = errors.New("social: you are not friends")
	ErrAlreadyFriends = errors.New("social: you are already friends")
	ErrNoRequest      = errors.New("social: there is no request to answer")
	ErrEmptyMessage   = errors.New("social: a message cannot be empty")
	ErrMessageTooLong = fmt.Errorf("social: a message is at most %d characters", MaxMessage)
	ErrTooManyFriends = fmt.Errorf("social: a friends list holds at most %d people", MaxFriends)
)

// State is how far along a friendship is.
type State string

const (
	// StateRequested is one row: asked, not answered.
	StateRequested State = "requested"
	// StateAccepted is the other half of a mutual friendship.
	StateAccepted State = "accepted"
)

// Store is the friend graph.
type Store struct{ db *sql.DB }

func New(db *sql.DB) *Store { return &Store{db: db} }

// --- the friend graph ---------------------------------------------------

// Request asks somebody to be a friend.
//
// If they have already asked us, this accepts instead — which is what a
// person means when they send a request to somebody whose request is sitting
// in their own inbox unnoticed.
func (s *Store) Request(from, to string, now time.Time) error {
	if from == to {
		return ErrSelf
	}
	// A block is silent. Reporting it would tell somebody they had been
	// blocked, which turns blocking into a message of its own and gives a
	// determined person a reason to make another account.
	blocked, err := s.eitherBlocked(from, to)
	if err != nil {
		return err
	}
	if blocked {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("social: %w", err)
	}
	defer tx.Rollback()

	var theirs string
	err = tx.QueryRow(`SELECT state FROM friend_edges WHERE from_id = ? AND to_id = ?`, to, from).Scan(&theirs)
	switch {
	case err == nil:
		// They already asked us. Answering is what was meant.
		if err := acceptWithin(tx, from, to, now); err != nil {
			return err
		}
		return tx.Commit()
	case errors.Is(err, sql.ErrNoRows):
	default:
		return fmt.Errorf("social: %w", err)
	}

	n, err := countFriends(tx, from)
	if err != nil {
		return err
	}
	if n >= MaxFriends {
		return ErrTooManyFriends
	}

	_, err = tx.Exec(
		`INSERT OR IGNORE INTO friend_edges (from_id, to_id, state, created_at) VALUES (?, ?, ?, ?)`,
		from, to, StateRequested, stamp(now))
	if err != nil {
		return fmt.Errorf("social: sending request: %w", err)
	}
	return tx.Commit()
}

// Accept answers a pending request.
func (s *Store) Accept(me, them string, now time.Time) error {
	if me == them {
		return ErrSelf
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("social: %w", err)
	}
	defer tx.Rollback()

	var state string
	err = tx.QueryRow(`SELECT state FROM friend_edges WHERE from_id = ? AND to_id = ?`, them, me).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoRequest
	}
	if err != nil {
		return fmt.Errorf("social: %w", err)
	}
	if err := acceptWithin(tx, me, them, now); err != nil {
		return err
	}
	return tx.Commit()
}

func acceptWithin(tx *sql.Tx, me, them string, now time.Time) error {
	for _, pair := range [][2]string{{me, them}, {them, me}} {
		n, err := countFriends(tx, pair[0])
		if err != nil {
			return err
		}
		if n >= MaxFriends {
			return ErrTooManyFriends
		}
	}
	for _, pair := range [][2]string{{me, them}, {them, me}} {
		if _, err := tx.Exec(
			`INSERT INTO friend_edges (from_id, to_id, state, created_at) VALUES (?, ?, ?, ?)
			 ON CONFLICT(from_id, to_id) DO UPDATE SET state = excluded.state`,
			pair[0], pair[1], StateAccepted, stamp(now)); err != nil {
			return fmt.Errorf("social: accepting: %w", err)
		}
	}
	return nil
}

// Decline throws away a request without becoming friends. The person who sent
// it is not told, for the same reason a block is silent.
func (s *Store) Decline(me, them string) error {
	_, err := s.db.Exec(`DELETE FROM friend_edges WHERE from_id = ? AND to_id = ?`, them, me)
	return err
}

// Remove ends a friendship, in both directions. A friendship one person has
// left is not a friendship, and leaving the other row behind would show them
// a friend who cannot see them.
func (s *Store) Remove(me, them string) error {
	_, err := s.db.Exec(
		`DELETE FROM friend_edges WHERE (from_id = ? AND to_id = ?) OR (from_id = ? AND to_id = ?)`,
		me, them, them, me)
	if err != nil {
		return fmt.Errorf("social: removing friend: %w", err)
	}
	return nil
}

// AreFriends reports a mutual, accepted friendship. This is what a
// friends-only room asks (D41), and it is deliberately strict: a pending
// request is not a friendship.
func (s *Store) AreFriends(a, b string) (bool, error) {
	if a == b {
		return false, nil
	}
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM friend_edges
		 WHERE state = 'accepted'
		   AND ((from_id = ? AND to_id = ?) OR (from_id = ? AND to_id = ?))`,
		a, b, b, a).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("social: %w", err)
	}
	return n == 2, nil
}

// Relation is one person on somebody's friends list or in their inbox.
type Relation struct {
	AccountID string
	State     State
	// Incoming distinguishes "they asked me" from "I asked them". Both are
	// StateRequested, and they need entirely different buttons.
	Incoming bool
	Since    time.Time
}

// Friends lists accepted friendships.
func (s *Store) Friends(me string) ([]Relation, error) {
	return s.relations(me, `SELECT to_id, created_at FROM friend_edges
		WHERE from_id = ? AND state = 'accepted' ORDER BY created_at`, StateAccepted, false)
}

// Incoming lists requests waiting for an answer.
func (s *Store) Incoming(me string) ([]Relation, error) {
	return s.relations(me, `SELECT from_id, created_at FROM friend_edges
		WHERE to_id = ? AND state = 'requested' ORDER BY created_at`, StateRequested, true)
}

// Outgoing lists requests we have sent and nobody has answered.
func (s *Store) Outgoing(me string) ([]Relation, error) {
	return s.relations(me, `SELECT to_id, created_at FROM friend_edges
		WHERE from_id = ? AND state = 'requested' ORDER BY created_at`, StateRequested, false)
}

func (s *Store) relations(me, query string, state State, incoming bool) ([]Relation, error) {
	rows, err := s.db.Query(query, me)
	if err != nil {
		return nil, fmt.Errorf("social: %w", err)
	}
	defer rows.Close()

	out := []Relation{}
	for rows.Next() {
		var (
			r  Relation
			ts string
		)
		if err := rows.Scan(&r.AccountID, &ts); err != nil {
			return nil, err
		}
		r.State, r.Incoming = state, incoming
		if at, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			r.Since = at
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// --- blocking -----------------------------------------------------------

// Block stops somebody reaching us. It also ends any friendship and cancels
// any pending request in either direction, because leaving those behind would
// mean a blocked person still appearing on a friends list.
func (s *Store) Block(me, them string, now time.Time) error {
	if me == them {
		return ErrSelf
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("social: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		`INSERT OR IGNORE INTO blocks (blocker_id, blocked_id, created_at) VALUES (?, ?, ?)`,
		me, them, stamp(now)); err != nil {
		return fmt.Errorf("social: blocking: %w", err)
	}
	if _, err := tx.Exec(
		`DELETE FROM friend_edges WHERE (from_id = ? AND to_id = ?) OR (from_id = ? AND to_id = ?)`,
		me, them, them, me); err != nil {
		return fmt.Errorf("social: blocking: %w", err)
	}
	return tx.Commit()
}

// Unblock lets somebody reach us again. It does not restore the friendship;
// that has to be asked for afresh, which is the honest thing - unblocking is
// not the same as forgiving.
func (s *Store) Unblock(me, them string) error {
	_, err := s.db.Exec(`DELETE FROM blocks WHERE blocker_id = ? AND blocked_id = ?`, me, them)
	return err
}

// Blocked lists who we have blocked.
func (s *Store) Blocked(me string) ([]string, error) {
	rows, err := s.db.Query(`SELECT blocked_id FROM blocks WHERE blocker_id = ? ORDER BY created_at`, me)
	if err != nil {
		return nil, fmt.Errorf("social: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// IsBlocked reports whether blocker has blocked blocked.
func (s *Store) IsBlocked(blocker, blocked string) (bool, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM blocks WHERE blocker_id = ? AND blocked_id = ?`, blocker, blocked).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("social: %w", err)
	}
	return n > 0, nil
}

// eitherBlocked reports whether either of two people has blocked the other.
// Contact needs both directions: somebody I blocked must not reach me, and I
// should not be able to reach somebody who blocked me.
func (s *Store) eitherBlocked(a, b string) (bool, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM blocks
		 WHERE (blocker_id = ? AND blocked_id = ?) OR (blocker_id = ? AND blocked_id = ?)`,
		a, b, b, a).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("social: %w", err)
	}
	return n > 0, nil
}

// --- private messages ---------------------------------------------------

// Message is one line of a private conversation.
type Message struct {
	ID     int64     `json:"id"`
	FromID string    `json:"from_id"`
	ToID   string    `json:"to_id"`
	Body   string    `json:"body"`
	At     time.Time `json:"at"`
	Read   bool      `json:"read"`
}

// Send writes a private message.
//
// Friends only. An open inbox is an invitation to spam, and the friends list
// is exactly the permission list a person already curates. A message to
// somebody who has blocked us is accepted and dropped, so a block cannot be
// detected by watching for an error.
func (s *Store) Send(from, to, body string, now time.Time) (Message, error) {
	if from == to {
		return Message{}, ErrSelf
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return Message{}, ErrEmptyMessage
	}
	if utf8.RuneCountInString(body) > MaxMessage {
		return Message{}, ErrMessageTooLong
	}

	blocked, err := s.eitherBlocked(from, to)
	if err != nil {
		return Message{}, err
	}
	if blocked {
		return Message{FromID: from, ToID: to, Body: body, At: now.UTC()}, nil
	}
	friends, err := s.AreFriends(from, to)
	if err != nil {
		return Message{}, err
	}
	if !friends {
		return Message{}, ErrNotFriends
	}

	res, err := s.db.Exec(
		`INSERT INTO private_messages (from_id, to_id, body, created_at) VALUES (?, ?, ?, ?)`,
		from, to, body, stamp(now))
	if err != nil {
		return Message{}, fmt.Errorf("social: sending message: %w", err)
	}
	id, _ := res.LastInsertId()
	return Message{ID: id, FromID: from, ToID: to, Body: body, At: now.UTC()}, nil
}

// Conversation returns messages between two people, oldest first, starting
// after the given ID. The client passes back the highest ID it has, so a poll
// carries only what is new.
func (s *Store) Conversation(me, them string, after int64, limit int) ([]Message, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.db.Query(
		`SELECT id, from_id, to_id, body, created_at, read_at FROM private_messages
		 WHERE ((from_id = ? AND to_id = ?) OR (from_id = ? AND to_id = ?)) AND id > ?
		 ORDER BY id LIMIT ?`,
		me, them, them, me, after, limit)
	if err != nil {
		return nil, fmt.Errorf("social: reading conversation: %w", err)
	}
	defer rows.Close()
	return scanMessages(rows)
}

// Unread counts messages waiting, by sender, so the friends list can put a
// number next to the right name.
func (s *Store) Unread(me string) (map[string]int, error) {
	rows, err := s.db.Query(
		`SELECT from_id, COUNT(*) FROM private_messages
		 WHERE to_id = ? AND read_at IS NULL GROUP BY from_id`, me)
	if err != nil {
		return nil, fmt.Errorf("social: counting unread: %w", err)
	}
	defer rows.Close()

	out := map[string]int{}
	for rows.Next() {
		var (
			id string
			n  int
		)
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// MarkRead marks everything from one person as read.
func (s *Store) MarkRead(me, them string, now time.Time) error {
	_, err := s.db.Exec(
		`UPDATE private_messages SET read_at = ? WHERE to_id = ? AND from_id = ? AND read_at IS NULL`,
		stamp(now), me, them)
	return err
}

func scanMessages(rows *sql.Rows) ([]Message, error) {
	out := []Message{}
	for rows.Next() {
		var (
			m    Message
			ts   string
			read sql.NullString
		)
		if err := rows.Scan(&m.ID, &m.FromID, &m.ToID, &m.Body, &ts, &read); err != nil {
			return nil, err
		}
		if at, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			m.At = at
		}
		m.Read = read.Valid
		out = append(out, m)
	}
	return out, rows.Err()
}

// --- room invitations ---------------------------------------------------

// Invitation is somebody telling a friend that a room is open for them.
//
// It is separate from the room's own invite list: that list is the door, this
// is the message saying the door is open. Any friend may send one; only the
// host's entry on the room's list actually admits anybody (D41).
type Invitation struct {
	ID     int64     `json:"id"`
	RoomID string    `json:"room_id"`
	FromID string    `json:"from_id"`
	At     time.Time `json:"at"`
	Seen   bool      `json:"seen"`
}

// InviteToRoom sends an invitation. Friends only, and silently dropped if
// either has blocked the other.
func (s *Store) InviteToRoom(from, to, roomID string, now time.Time) error {
	if from == to {
		return ErrSelf
	}
	blocked, err := s.eitherBlocked(from, to)
	if err != nil {
		return err
	}
	if blocked {
		return nil
	}
	friends, err := s.AreFriends(from, to)
	if err != nil {
		return err
	}
	if !friends {
		return ErrNotFriends
	}
	_, err = s.db.Exec(
		`INSERT INTO room_invites (room_id, from_id, to_id, created_at) VALUES (?, ?, ?, ?)`,
		roomID, from, to, stamp(now))
	if err != nil {
		return fmt.Errorf("social: sending invitation: %w", err)
	}
	return nil
}

// Invitations returns the invitations somebody has not seen yet.
func (s *Store) Invitations(me string) ([]Invitation, error) {
	rows, err := s.db.Query(
		`SELECT id, room_id, from_id, created_at, seen_at FROM room_invites
		 WHERE to_id = ? AND seen_at IS NULL ORDER BY id`, me)
	if err != nil {
		return nil, fmt.Errorf("social: reading invitations: %w", err)
	}
	defer rows.Close()

	out := []Invitation{}
	for rows.Next() {
		var (
			inv  Invitation
			ts   string
			seen sql.NullString
		)
		if err := rows.Scan(&inv.ID, &inv.RoomID, &inv.FromID, &ts, &seen); err != nil {
			return nil, err
		}
		if at, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			inv.At = at
		}
		inv.Seen = seen.Valid
		out = append(out, inv)
	}
	return out, rows.Err()
}

// MarkInvitationsSeen clears the invitation badge.
func (s *Store) MarkInvitationsSeen(me string, now time.Time) error {
	_, err := s.db.Exec(
		`UPDATE room_invites SET seen_at = ? WHERE to_id = ? AND seen_at IS NULL`, stamp(now), me)
	return err
}

// --- helpers ------------------------------------------------------------

type querier interface {
	QueryRow(query string, args ...any) *sql.Row
}

func countFriends(q querier, id string) (int, error) {
	var n int
	err := q.QueryRow(
		`SELECT COUNT(*) FROM friend_edges WHERE from_id = ? AND state = 'accepted'`, id).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("social: counting friends: %w", err)
	}
	return n, nil
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
