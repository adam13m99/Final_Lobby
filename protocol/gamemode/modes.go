// Package gamemode is the one list of Dota 2 game modes LobbyBaz offers.
//
// It lives in protocol/ because four different programs need the same answer
// and none of them may be allowed to have its own: the coordinator stores the
// mode a host picked, the desktop app draws the menu they picked it from, the
// Windows service turns it into "gamemode N" on a real command line, and the
// CLI prints it. A mode the app offers and the service rejects is a room that
// cannot start a match, and the only way to be sure that never happens is for
// there to be a single list.
//
// The IDs are Valve's own DOTA_GameMode enum, not ours. They are wire values
// that appear on a Dota command line, so they are fixed by the game and must
// never be renumbered to tidy them up:
//
//	https://github.com/SteamDatabase/GameTracking-Dota2/blob/master/Protobufs/dota_shared_enums.proto
//
// What is ours is which of the twenty-six a private LAN lobby should offer.
// The enum contains tutorials, event modes, the coaches' challenge and
// several modes that need Valve's matchmaking to mean anything; none of them
// belong in a menu here. The list below is the ones ten people on a host's
// own listen server can actually play.
package gamemode

// Mode is one entry in the menu.
type Mode struct {
	// ID is Valve's DOTA_GameMode value. This is what reaches the command
	// line as "gamemode N".
	ID int
	// Key is the suffix of this mode's interface string, "room.mode." + Key.
	// The desktop app translates mode names like every other label; this is
	// the only thing tying its catalogue to this list.
	Key string
	// Name is the English name Dota's own interface uses. It is what the CLI
	// and the server logs print, and what a client with no string catalogue
	// falls back to.
	Name string
}

// Modes is the menu, in the order it is offered.
//
// All Pick leads because it is what almost every room plays. The rest follow
// the order Dota's own lobby dialog lists them in, which is roughly the order
// people learned them.
var Modes = []Mode{
	{ID: 1, Key: "ap", Name: "All Pick"},
	{ID: 22, Key: "rap", Name: "Ranked All Pick"},
	{ID: 23, Key: "turbo", Name: "Turbo"},
	{ID: 2, Key: "cm", Name: "Captains Mode"},
	{ID: 8, Key: "rcm", Name: "Reverse Captains Mode"},
	{ID: 16, Key: "cd", Name: "Captains Draft"},
	{ID: 3, Key: "rd", Name: "Random Draft"},
	{ID: 4, Key: "sd", Name: "Single Draft"},
	{ID: 5, Key: "ar", Name: "All Random"},
	{ID: 18, Key: "abd", Name: "Ability Draft"},
	{ID: 20, Key: "ardm", Name: "All Random Deathmatch"},
	{ID: 21, Key: "mid1v1", Name: "1v1 Solo Mid"},
}

// Default is the mode a room starts with when nobody has chosen one.
//
// All Pick, because it is the mode a room that did not think about it wants,
// and because it is what every launch used before the host could choose at
// all - so an existing room, stored before this field existed and read back
// with a zero in it, keeps playing exactly what it was playing.
const Default = 1

// byID is Modes indexed for lookup, built once.
var byID = func() map[int]Mode {
	m := make(map[int]Mode, len(Modes))
	for _, mode := range Modes {
		m[mode.ID] = mode
	}
	return m
}()

// Get returns the mode with this ID.
func Get(id int) (Mode, bool) {
	m, ok := byID[id]
	return m, ok
}

// Name returns the English name of a mode ID.
func Name(id int) (string, bool) {
	m, ok := byID[id]
	return m.Name, ok
}

// Valid reports whether an ID is a mode we offer.
func Valid(id int) bool {
	_, ok := byID[id]
	return ok
}

// OrDefault turns anything stored or received into a mode we will launch.
//
// Zero is the important case and it is not an error: it is a room created
// before rooms had a mode, or a client that did not send one. Both mean
// "whatever a room plays by default", which is what every such room was
// already playing.
func OrDefault(id int) int {
	if id == 0 || !Valid(id) {
		return Default
	}
	return id
}
