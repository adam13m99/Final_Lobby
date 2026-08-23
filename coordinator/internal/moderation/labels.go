package moderation

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Player labels (D43).
//
// The owner asked for a visible mark a moderator can put on a player, with
// *Fake MMR*, *Verified*, *Pro Player* and *Noob* as the examples. The
// mechanism is genuinely useful: MMR is self-declared, so a way to say "this
// number is not real" defends the honest majority, and a way to say "this
// person is who they say they are" is worth having in a community that cannot
// check anything itself.
//
// **One note is recorded here rather than quietly dropped, as it was in
// docs/decisions.md:** *Fake MMR*, *Verified* and *Pro Player* each describe
// something checkable. *Noob* does not — it is an insult carrying staff
// authority, and it is the screenshot that circulates. It ships because the
// owner asked for it; the recommendation was, and is, to let the labels that
// ship be ones a moderator could defend. Removing one is a one-line change to
// KnownLabels and nothing else, which is the reason the set lives here rather
// than being whatever anybody types.

// Label is a visible mark on a player.
type Label string

const (
	// LabelFakeMMR marks a declared rating staff believe is invented. MMR is
	// self-declared, so this is the only defence the honest majority has.
	LabelFakeMMR Label = "fake_mmr"
	// LabelVerified marks somebody staff have confirmed is who they say.
	LabelVerified Label = "verified"
	// LabelProPlayer marks a known competitive player.
	LabelProPlayer Label = "pro_player"
	// LabelNoob is the owner's. See the note above the type.
	LabelNoob Label = "noob"
)

// KnownLabels is the whole set. A label a moderator can type freely is a
// licence to write anything next to somebody's name with staff authority
// behind it, so the set is fixed and adding one is a deliberate change.
var KnownLabels = []Label{LabelFakeMMR, LabelVerified, LabelProPlayer, LabelNoob}

// Valid reports whether l is a label that ships.
func (l Label) Valid() bool {
	for _, known := range KnownLabels {
		if l == known {
			return true
		}
	}
	return false
}

var ErrBadLabel = errors.New("moderation: not a label that exists")

// LabelPlayer marks a player. Attributed, like everything else: a label is
// staff speech attached to somebody's name, and it should always be possible
// to ask who put it there.
func (s *Store) LabelPlayer(actorID, targetID string, label Label, now time.Time) error {
	if err := s.RequireStaff(actorID); err != nil {
		return err
	}
	if !label.Valid() {
		return ErrBadLabel
	}
	if err := s.canActAgainst(actorID, targetID); err != nil {
		return err
	}
	if _, err := s.db.Exec(
		`INSERT INTO player_labels (account_id, label, actor_id, created_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(account_id, label) DO UPDATE SET actor_id = excluded.actor_id, created_at = excluded.created_at`,
		targetID, label, actorID, stamp(now)); err != nil {
		return fmt.Errorf("moderation: labelling player: %w", err)
	}
	return s.record(actorID, "label", targetID, string(label), now)
}

// UnlabelPlayer removes a mark.
func (s *Store) UnlabelPlayer(actorID, targetID string, label Label, now time.Time) error {
	if err := s.RequireStaff(actorID); err != nil {
		return err
	}
	if _, err := s.db.Exec(
		`DELETE FROM player_labels WHERE account_id = ? AND label = ?`, targetID, label); err != nil {
		return fmt.Errorf("moderation: removing label: %w", err)
	}
	return s.record(actorID, "unlabel", targetID, string(label), now)
}

// LabelsOf returns a player's marks, for the room list and their profile.
func (s *Store) LabelsOf(accountID string) ([]Label, error) {
	rows, err := s.db.Query(
		`SELECT label FROM player_labels WHERE account_id = ? ORDER BY created_at`, accountID)
	if err != nil {
		return nil, fmt.Errorf("moderation: reading labels: %w", err)
	}
	defer rows.Close()

	out := []Label{}
	for rows.Next() {
		var l Label
		if err := rows.Scan(&l); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// LabelsOfMany returns labels for several players at once. The room list needs
// this for every seat, and one query beats eighteen.
func (s *Store) LabelsOfMany(ids []string) (map[string][]Label, error) {
	out := map[string][]Label{}
	if len(ids) == 0 {
		return out, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.Query(
		`SELECT account_id, label FROM player_labels WHERE account_id IN (`+placeholders+`) ORDER BY created_at`,
		args...)
	if err != nil {
		return nil, fmt.Errorf("moderation: reading labels: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		var l Label
		if err := rows.Scan(&id, &l); err != nil {
			return nil, err
		}
		out[id] = append(out[id], l)
	}
	return out, rows.Err()
}
