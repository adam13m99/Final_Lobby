// Package store opens the coordinator's database and keeps its schema up to
// date.
//
// SQLite, not PostgreSQL as section 6 of the spec assumed. Three reasons, and
// the third is the one that decided it:
//
//  1. At the 500-player launch target the load is trivial. Rooms, accounts and
//     friendships are small tables with small queries; nothing here is a
//     workload SQLite struggles with.
//  2. It is a file. No service to run, no port to open, no password to rotate,
//     and a backup is a copy. The relay already keeps no durable state, so the
//     whole platform's persistence becomes one file that moves with it.
//  3. **The box belongs to somebody else.** Until the platform gets its own
//     server (D49) it is a guest on a machine running the owner's live,
//     unrelated business - which already runs its own PostgreSQL. Adding a
//     second database service there is exactly the entanglement D35 exists to
//     prevent, and using theirs is worse.
//
// The driver is pure Go (modernc.org/sqlite), so the coordinator still
// cross-compiles from Windows to Linux with no C toolchain. A cgo driver
// would have quietly broken the build pipeline.
//
// Recorded as D51. Revisit at the move to a dedicated server: the queries are
// ordinary SQL and the seam is this package, so PostgreSQL is a swap rather
// than a rewrite. Do it when concurrent writes actually contend, which is a
// real limit of SQLite and the reason not to promise it forever.
package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// Open prepares the database at path and applies every pending migration.
//
// Pass ":memory:" for a throwaway database; tests use it.
func Open(path string) (*sql.DB, error) {
	dsn := path
	if path != ":memory:" {
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}
		// WAL keeps a reader from blocking the writer, which matters because
		// the coordinator serves lobby polls continuously while people join
		// and leave. busy_timeout turns the remaining contention into a short
		// wait instead of an immediate error.
		dsn = "file:" + filepath.ToSlash(abs) + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	} else {
		dsn = "file::memory:?_pragma=foreign_keys(1)"
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}

	// SQLite takes one writer at a time. Letting database/sql open several
	// connections buys nothing and turns lock contention into errors that
	// surface as random failures under load.
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: unreachable: %w", err)
	}
	if err := Migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// migrations run in order, exactly once each. Never edit one that has shipped;
// add another. The index in this slice is the version number.
var migrations = []string{
	// 1 - accounts, the contact seam, sessions and terms.
	`
	CREATE TABLE accounts (
		id            TEXT PRIMARY KEY,
		username      TEXT NOT NULL UNIQUE, -- folded to lower case
		display_name  TEXT NOT NULL,
		password_hash TEXT NOT NULL,
		mmr           INTEGER NOT NULL DEFAULT 0,
		mmr_set_at    TEXT,
		created_at    TEXT NOT NULL,
		disabled_at   TEXT
	);

	-- D37: email and SMS are not being built, but the shape they need is
	-- here from the first migration so switching them on later is a feature,
	-- not a schema change. The table ships empty on purpose.
	CREATE TABLE contact_methods (
		id          TEXT PRIMARY KEY,
		account_id  TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
		kind        TEXT NOT NULL CHECK (kind IN ('email','sms')),
		value       TEXT NOT NULL,
		verified_at TEXT,
		created_at  TEXT NOT NULL,
		UNIQUE (kind, value)
	);
	CREATE INDEX contact_methods_account ON contact_methods(account_id);

	-- Only the hash is stored. A stolen database must not hand somebody a
	-- working session token.
	CREATE TABLE sessions (
		token_hash   TEXT PRIMARY KEY,
		account_id   TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
		device       TEXT NOT NULL,
		created_at   TEXT NOT NULL,
		last_seen_at TEXT NOT NULL,
		expires_at   TEXT NOT NULL
	);
	CREATE INDEX sessions_account ON sessions(account_id);

	-- Which version of the terms somebody accepted, and when. Auditable, and
	-- re-promptable when the text changes.
	CREATE TABLE terms_acceptance (
		account_id  TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
		version     TEXT NOT NULL,
		accepted_at TEXT NOT NULL,
		PRIMARY KEY (account_id, version)
	);
	`,

	// 2 - kicks, as events rather than as live blocks (D52).
	//
	// The first draft of this table stored the block itself: room, player,
	// count, blocked-until. It would have been dead weight. A block belongs
	// to a room, rooms live in memory, and a coordinator restart therefore
	// ends every room - so on the next start the block would key into
	// something that no longer exists, and worse, room IDs were being reused,
	// so it could key into a *different* room and bar an innocent person.
	//
	// What is actually worth keeping is the history: a person kicked eleven
	// times this week is a moderation fact, and it is the only part of a kick
	// that outlives the room. The live escalating block stays in memory with
	// the room it belongs to.
	`
	CREATE TABLE kick_events (
		id          TEXT PRIMARY KEY,
		room_id     TEXT NOT NULL,
		actor_id    TEXT NOT NULL,
		target_id   TEXT NOT NULL,
		-- kick_number is which kick this was from this room, so the record
		-- shows the escalation that was applied at the time.
		kick_number INTEGER NOT NULL,
		blocked_for INTEGER NOT NULL, -- seconds
		created_at  TEXT NOT NULL
	);
	CREATE INDEX kick_events_target ON kick_events(target_id, created_at);
	CREATE INDEX kick_events_actor ON kick_events(actor_id, created_at);
	`,

	// 3 - roles are grants with an author, never a flag on an account (D47),
	// and every moderator action is attributed to whoever took it (D43).
	`
	CREATE TABLE role_grants (
		id         TEXT PRIMARY KEY,
		account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
		role       TEXT NOT NULL CHECK (role IN ('admin','head_admin')),
		granted_by TEXT,          -- NULL only for the bootstrap head admin
		granted_at TEXT NOT NULL,
		revoked_by TEXT,
		revoked_at TEXT
	);
	CREATE INDEX role_grants_account ON role_grants(account_id, role);

	CREATE TABLE admin_actions (
		id         TEXT PRIMARY KEY,
		actor_id   TEXT NOT NULL,
		action     TEXT NOT NULL,
		subject    TEXT NOT NULL, -- account id, room id, or banner id
		detail     TEXT NOT NULL,
		created_at TEXT NOT NULL
	);
	CREATE INDEX admin_actions_actor ON admin_actions(actor_id, created_at);
	CREATE INDEX admin_actions_subject ON admin_actions(subject, created_at);
	`,
}

// Migrate brings an open database up to the current schema version.
func Migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("store: schema table: %w", err)
	}
	var current int
	row := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`)
	if err := row.Scan(&current); err != nil {
		return fmt.Errorf("store: reading schema version: %w", err)
	}

	for i := current; i < len(migrations); i++ {
		version := i + 1
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("store: migration %d: %w", version, err)
		}
		for _, stmt := range splitStatements(migrations[i]) {
			if _, err := tx.Exec(stmt); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("store: migration %d: %w\nstatement: %s", version, err, stmt)
			}
		}
		if _, err := tx.Exec(`INSERT INTO schema_version (version) VALUES (?)`, version); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("store: migration %d: recording version: %w", version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("store: migration %d: commit: %w", version, err)
		}
	}
	return nil
}

// Version reports the schema version currently applied.
func Version(db *sql.DB) (int, error) {
	var v int
	err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&v)
	return v, err
}

// splitStatements breaks a migration into individual statements, because the
// driver executes one at a time.
func splitStatements(script string) []string {
	var out []string
	for _, part := range strings.Split(script, ";") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
