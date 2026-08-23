package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

// Kicks is the durable record of who was kicked, by whom, and from where.
//
// It is a log, not a state table. The live block that bars a kicked player
// for one, three, five minutes lives in memory with the room it belongs to
// (D52); what is written here is the history a moderator needs when somebody
// says "he keeps doing this" - and the only part of a kick that is still true
// after the room has closed.
type Kicks struct{ db *sql.DB }

func NewKicks(db *sql.DB) *Kicks { return &Kicks{db: db} }

// Record writes one kick down. Failure is logged by the caller and never
// blocks the kick itself: a moderation record that could stop a host removing
// a griefer would be worse than a missing row.
func (k *Kicks) Record(roomID, actorID, targetID string, kickNumber int, blockedFor time.Duration, at time.Time) error {
	id, err := randomID()
	if err != nil {
		return err
	}
	_, err = k.db.Exec(
		`INSERT INTO kick_events (id, room_id, actor_id, target_id, kick_number, blocked_for, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, roomID, actorID, targetID, kickNumber,
		int(blockedFor.Seconds()), at.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("store: recording kick: %w", err)
	}
	return nil
}

// KickRecord is one row of the log.
type KickRecord struct {
	RoomID     string
	ActorID    string
	TargetID   string
	KickNumber int
	BlockedFor time.Duration
	At         time.Time
}

// TimesKicked counts how often somebody has been kicked since a moment. This
// is the number a moderator actually asks for.
func (k *Kicks) TimesKicked(targetID string, since time.Time) (int, error) {
	var n int
	err := k.db.QueryRow(
		`SELECT COUNT(*) FROM kick_events WHERE target_id = ? AND created_at >= ?`,
		targetID, since.UTC().Format(time.RFC3339Nano)).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("store: counting kicks: %w", err)
	}
	return n, nil
}

// History returns somebody's most recent kicks, newest first.
func (k *Kicks) History(targetID string, limit int) ([]KickRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := k.db.Query(
		`SELECT room_id, actor_id, target_id, kick_number, blocked_for, created_at
		 FROM kick_events WHERE target_id = ? ORDER BY created_at DESC LIMIT ?`,
		targetID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: reading kick history: %w", err)
	}
	defer rows.Close()

	var out []KickRecord
	for rows.Next() {
		var (
			r    KickRecord
			secs int
			ts   string
		)
		if err := rows.Scan(&r.RoomID, &r.ActorID, &r.TargetID, &r.KickNumber, &secs, &ts); err != nil {
			return nil, err
		}
		r.BlockedFor = time.Duration(secs) * time.Second
		if at, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			r.At = at
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func randomID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("store: generating id: %w", err)
	}
	return "k_" + hex.EncodeToString(b), nil
}
