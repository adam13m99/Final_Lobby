package store

// presenceMigrations run after everything above. A new list rather than an
// entry appended to an existing one, because all() concatenates the lists in
// a fixed order and the position in the concatenation is the version number:
// adding to the middle of any earlier list would renumber every migration
// after it and re-run the wrong scripts on a database that is already live.
var presenceMigrations = []string{
	// 1 - when somebody was last here.
	//
	// The registry has always known this, but only for as long as the
	// process lives. A friends list that says "offline" and nothing else
	// cannot answer the question people actually ask it - is it worth
	// waiting for them - and after every deployment it forgot even that.
	//
	// It is written on a timer rather than on every heartbeat: a thousand
	// players polling every two seconds would be five hundred writes a
	// second for a number nobody reads more than once a minute.
	`
	ALTER TABLE accounts ADD COLUMN last_seen_at TEXT;
	`,
}
