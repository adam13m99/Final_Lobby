package store

// Migration 5 adds what moderation needs beyond the role grants and the audit
// log that migration 3 already created (D43, D47).
//
// The shape to notice: nothing here is a boolean on an account. A ban, a mute,
// a timeout and a label are all rows with an author and a timestamp, because
// the question asked after something goes wrong is never "is this person
// banned" - it is "who banned them, when, and what for".
var moderationMigrations = []string{
	// 5 - sanctions, labels, banners.
	`
	-- One row per sanction, ever. Lifting one does not delete it; it stamps
	-- lifted_at, so the history survives. A moderation table you can erase by
	-- undoing things is not a record.
	CREATE TABLE sanctions (
		id         TEXT PRIMARY KEY,
		account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
		kind       TEXT NOT NULL CHECK (kind IN ('ban','mute','timeout')),
		reason     TEXT NOT NULL,
		actor_id   TEXT NOT NULL,
		created_at TEXT NOT NULL,
		-- NULL means it does not expire on its own.
		expires_at TEXT,
		lifted_by  TEXT,
		lifted_at  TEXT
	);
	CREATE INDEX sanctions_account ON sanctions(account_id, kind);

	-- A visible mark on a player (D43). Attributed, like everything else: a
	-- label is staff speech attached to somebody's name, and it should be
	-- possible to ask who put it there.
	CREATE TABLE player_labels (
		account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
		label      TEXT NOT NULL,
		actor_id   TEXT NOT NULL,
		created_at TEXT NOT NULL,
		PRIMARY KEY (account_id, label)
	);

	-- The banner strip at the top of the lobby.
	CREATE TABLE banners (
		id         TEXT PRIMARY KEY,
		title      TEXT NOT NULL,
		body       TEXT NOT NULL,
		image_url  TEXT NOT NULL,
		link_url   TEXT NOT NULL,
		sort_order INTEGER NOT NULL DEFAULT 0,
		active     INTEGER NOT NULL DEFAULT 1,
		created_by TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_by TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);
	CREATE INDEX banners_visible ON banners(active, sort_order);
	`,
}
