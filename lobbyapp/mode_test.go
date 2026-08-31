package main

// The game-mode menu exists twice - once in protocol/gamemode, which the
// coordinator stores and the Windows service turns into a real Dota command
// line, and once in index.html, which is what a host actually picks from.
// Two lists is exactly the shape of the bug this project keeps producing: a
// tested subsystem the interface cannot reach, or an interface offering
// something the subsystem refuses.
//
// It cannot be one list. The menu has to be markup, because every option is
// translated through the same catalogue as every other label, and the Go list
// has to be Go, because the service validating the command line has no
// browser in it. So the two are held together here instead, by an id and a
// key, and the failure messages name the mode rather than the index.

import (
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"lobbybaz/protocol/gamemode"
)

// modeOption matches one entry of a mode menu: its Dota number and the string
// key its label comes from.
var modeOption = regexp.MustCompile(
	`<option value="(\d+)" data-t="room\.mode\.([a-z0-9]+)"></option>`)

// modeSelect isolates one <select> so the two menus are checked separately;
// a room whose settings offer Turbo and whose creation dialog does not is
// still a room somebody cannot open in Turbo.
var modeSelect = regexp.MustCompile(`(?s)<select id="(mode|newmode)">(.*?)</select>`)

func TestBothModeMenusOfferExactlyTheModesWeSupport(t *testing.T) {
	html := read(t, filepath.Join(uiDir, "index.html"))

	found := modeSelect.FindAllStringSubmatch(html, -1)
	if len(found) != 2 {
		t.Fatalf("found %d game mode menus in index.html, want 2 "+
			"(the create dialog and room settings)", len(found))
	}

	for _, sel := range found {
		id, body := sel[1], sel[2]
		opts := modeOption.FindAllStringSubmatch(body, -1)
		if len(opts) != len(gamemode.Modes) {
			t.Errorf("#%s offers %d modes, protocol/gamemode has %d",
				id, len(opts), len(gamemode.Modes))
		}
		for i, want := range gamemode.Modes {
			if i >= len(opts) {
				t.Errorf("#%s does not offer %s (%d)", id, want.Name, want.ID)
				continue
			}
			gotID, _ := strconv.Atoi(opts[i][1])
			if gotID != want.ID || opts[i][2] != want.Key {
				t.Errorf("#%s option %d is value=%d key=%q, want %s: value=%d key=%q",
					id, i, gotID, opts[i][2], want.Name, want.ID, want.Key)
			}
		}
		for _, o := range opts {
			gotID, _ := strconv.Atoi(o[1])
			if !gamemode.Valid(gotID) {
				t.Errorf("#%s offers game mode %d, which the service will refuse "+
					"to launch", id, gotID)
			}
		}
	}
}

// The English label a host reads has to be the mode's actual name, because
// that name is also what the coordinator logs, what the CLI prints, and what
// Dota's own interface calls it. A menu reading "Captain's Mode" beside a
// server saying "Captains Mode" is one mode described two ways.
func TestTheEnglishLabelsAreTheRealModeNames(t *testing.T) {
	en := catalogue(t, "en")
	for _, m := range gamemode.Modes {
		key := "room.mode." + m.Key
		got, ok := en[key]
		if !ok {
			t.Errorf("%s (%d) has no label: the catalogue needs %q", m.Name, m.ID, key)
			continue
		}
		if got != m.Name {
			t.Errorf("%s is labelled %q in the interface and %q in protocol/gamemode",
				key, got, m.Name)
		}
	}
}
