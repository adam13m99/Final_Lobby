package account

import (
	"fmt"
	"strings"
	"time"
)

// --- presence -----------------------------------------------------------
//
// When somebody was last here. The player registry knows it for as long as
// the process lives; this is the copy that survives a restart, which is what
// makes "last seen 2h ago" mean anything on a friends list the morning after
// a deployment.
//
// It is deliberately coarse. RecordSeen is called from the coordinator's
// timer with everybody seen since the previous sweep, not from the heartbeat:
// a thousand players polling every two seconds would be five hundred writes a
// second for a number nobody reads more than once a minute.

// RecordSeen writes when each of these accounts was last seen. Ids that are
// not accounts are ignored, which is what happens on a coordinator whose
// players are names rather than logins.
func (s *Store) RecordSeen(seen map[string]time.Time) error {
	if len(seen) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("account: recording presence: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`UPDATE accounts SET last_seen_at = ? WHERE id = ?`)
	if err != nil {
		return fmt.Errorf("account: recording presence: %w", err)
	}
	defer stmt.Close()

	for id, at := range seen {
		if _, err := stmt.Exec(at.UTC().Format(time.RFC3339Nano), id); err != nil {
			return fmt.Errorf("account: recording presence: %w", err)
		}
	}
	return tx.Commit()
}

// LastSeenMany reads the stored last-seen time for several accounts at once.
//
// One query rather than one per friend: the rail is redrawn every couple of
// seconds by every signed-in client, and a query per name in it would turn a
// twenty-friend list into twenty round trips to SQLite on every poll.
//
// An account that has never been recorded is simply absent from the result,
// which the caller must read as "unknown" rather than as "the epoch".
func (s *Store) LastSeenMany(ids []string) (map[string]time.Time, error) {
	out := make(map[string]time.Time, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	q := `SELECT id, last_seen_at FROM accounts WHERE last_seen_at IS NOT NULL AND id IN (?` +
		strings.Repeat(",?", len(ids)-1) + `)`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("account: reading presence: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, at string
		if err := rows.Scan(&id, &at); err != nil {
			return nil, fmt.Errorf("account: reading presence: %w", err)
		}
		if when, perr := time.Parse(time.RFC3339Nano, at); perr == nil {
			out[id] = when
		}
	}
	return out, rows.Err()
}
