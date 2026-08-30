package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// This file exists because losing lobby.db loses every account there has ever
// been: the usernames, the password hashes nobody can recover, the friend
// graph, the moderation record and who accepted which version of the terms.
// There was no copy of it anywhere until 2026-08-30 (D76).
//
// It is deliberately not `cp`. The database runs in WAL mode, so the file on
// disk is only part of the truth at any instant and copying it while the
// coordinator is writing produces something that opens and is wrong - which
// is the worst kind of backup, because it looks like one. VACUUM INTO asks
// SQLite itself for a consistent copy, needs no external tooling on the
// server, and writes a file that is already compacted.

const (
	// BackupEvery is how often a copy is taken. An hour is chosen against
	// what is actually lost: an hour of new accounts and friend requests,
	// on a service whose busy hour is a few hundred people.
	BackupEvery = time.Hour
	// BackupKeep is how many copies are kept. Twenty-four hours of them, so
	// a corruption noticed the next morning still has a clean file behind
	// it, at a few megabytes each.
	BackupKeep = 24
)

// Backup writes a consistent copy of db into dir and prunes old ones. It
// returns the path it wrote.
func Backup(db *sql.DB, dir string, now time.Time) (string, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("backup directory: %w", err)
	}
	name := filepath.Join(dir, "lobby-"+now.UTC().Format("20060102-1504")+".db")

	// A leftover from an interrupted run would make VACUUM INTO fail: it
	// refuses to write a file that already exists, on purpose.
	_ = os.Remove(name)
	if _, err := db.Exec("VACUUM INTO ?", name); err != nil {
		return "", fmt.Errorf("vacuum into %s: %w", name, err)
	}
	if err := prune(dir, BackupKeep); err != nil {
		// The copy was taken and that is the part that matters. A directory
		// that will not tidy itself is worth saying out loud and nothing more.
		return name, fmt.Errorf("copy written, but pruning failed: %w", err)
	}
	return name, nil
}

// prune keeps the newest `keep` copies and deletes the rest. Names carry a
// sortable timestamp, so this is a sort rather than a stat of every file.
func prune(dir string, keep int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() && len(n) > 6 && n[:6] == "lobby-" && filepath.Ext(n) == ".db" {
			names = append(names, n)
		}
	}
	if len(names) <= keep {
		return nil
	}
	sort.Strings(names)
	for _, n := range names[:len(names)-keep] {
		if err := os.Remove(filepath.Join(dir, n)); err != nil {
			return err
		}
	}
	return nil
}
