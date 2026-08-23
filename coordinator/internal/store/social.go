package store

// Migration 4 adds the friend graph and its private conversations (D41).
//
// It is here rather than in db.go's list only for length; Migrate reads
// socialMigrations as a continuation of migrations, so version numbers keep
// running on from 3.
var socialMigrations = []string{
	// 4 - friendships, blocks, and private messages.
	`
	-- One row per direction. Two rows make a friendship.
	--
	-- Storing it as two directed rows rather than one undirected pair is what
	-- makes a pending request expressible at all: A has asked B, B has not
	-- answered. It also means removing a friend is one row, not a search for
	-- whichever ordering happened to be stored.
	CREATE TABLE friend_edges (
		from_id    TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
		to_id      TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
		state      TEXT NOT NULL CHECK (state IN ('requested','accepted')),
		created_at TEXT NOT NULL,
		PRIMARY KEY (from_id, to_id)
	);
	CREATE INDEX friend_edges_to ON friend_edges(to_id, state);

	-- A block is one-directional and it is not the absence of a friendship:
	-- somebody you blocked must not be able to message you, invite you, or
	-- find out that you blocked them by watching a request go unanswered.
	CREATE TABLE blocks (
		blocker_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
		blocked_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
		created_at TEXT NOT NULL,
		PRIMARY KEY (blocker_id, blocked_id)
	);
	CREATE INDEX blocks_blocked ON blocks(blocked_id);

	-- Private messages are durable, unlike lobby and room chat, which are in
	-- memory and die with the room. A message sent to somebody who is offline
	-- has to still be there when they come back, or "message a friend" means
	-- "message a friend who is currently looking at the app".
	CREATE TABLE private_messages (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		from_id    TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
		to_id      TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
		body       TEXT NOT NULL,
		created_at TEXT NOT NULL,
		read_at    TEXT
	);
	CREATE INDEX private_messages_pair ON private_messages(from_id, to_id, id);
	CREATE INDEX private_messages_inbox ON private_messages(to_id, id);

	-- An invitation to a room, as a notification. It is separate from the
	-- room's own invite list: that list is the door, this is the message
	-- telling somebody the door is open for them.
	CREATE TABLE room_invites (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		room_id    TEXT NOT NULL,
		from_id    TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
		to_id      TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
		created_at TEXT NOT NULL,
		seen_at    TEXT
	);
	CREATE INDEX room_invites_inbox ON room_invites(to_id, id);
	`,
}
