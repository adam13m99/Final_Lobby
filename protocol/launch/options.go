// Package launch parses the extra Dota 2 command-line options a player types
// into Settings.
//
// It lives in the protocol module because both ends need exactly the same
// answer: the app checks the text the moment somebody saves it, so a typo is
// caught while they are looking at the field rather than four clicks later
// when the match will not start, and the network service checks it again
// before the arguments reach a process. Two copies of a rule like this drift,
// and the half that drifts is always the one nobody is testing.
//
// The rule is deliberately narrow. Dota takes two kinds of argument: engine
// flags beginning with a hyphen, and console commands beginning with a plus.
// **Only the hyphen kind is allowed here.** The plus space is where +connect,
// +map, +jointeam and gamemode live - the arguments LobbyBaz builds itself to
// put somebody in the right match on the right team - and a player who could
// type those could point their own client at a different server, or join the
// team they were not seated on, and then wonder why the room was broken.
package launch

import (
	"errors"
	"fmt"
	"strings"
)

// ErrBadOption is returned for anything the parser will not pass through.
var ErrBadOption = errors.New("launch options")

const (
	// MaxLength is the longest option string accepted, in characters. It is
	// a text field on a settings screen, not a script.
	MaxLength = 200
	// MaxTokens caps how many words that text may become.
	MaxTokens = 24
	maxToken  = 64
)

// Options splits and checks a player's own launch options.
//
// Empty text is not an error - it is the normal case, and it means no extra
// arguments at all.
func Options(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if len(raw) > MaxLength {
		return nil, fmt.Errorf("%w: %d characters is longer than the %d allowed",
			ErrBadOption, len(raw), MaxLength)
	}
	tokens := strings.Fields(raw)
	if len(tokens) > MaxTokens {
		return nil, fmt.Errorf("%w: %d words is more than the %d allowed",
			ErrBadOption, len(tokens), MaxTokens)
	}
	// The first token has to be a flag, or the rest is a value belonging to
	// nothing and the player has typed something they did not mean.
	if !strings.HasPrefix(tokens[0], "-") {
		return nil, fmt.Errorf("%w: %q is not a flag - options start with a hyphen, like -novid",
			ErrBadOption, tokens[0])
	}
	for _, tok := range tokens {
		if len(tok) > maxToken {
			return nil, fmt.Errorf("%w: %q is too long", ErrBadOption, tok)
		}
		if strings.HasPrefix(tok, "+") {
			return nil, fmt.Errorf(
				"%w: %q is a console command, and LobbyBaz sets those itself to put you in the right match",
				ErrBadOption, tok)
		}
		if strings.HasPrefix(tok, "-") {
			if !IsPlayerFlag(tok) {
				return nil, fmt.Errorf("%w: %q is not a flag LobbyBaz will pass on", ErrBadOption, tok)
			}
			continue
		}
		if !isValue(tok) {
			return nil, fmt.Errorf("%w: %q has characters that are not allowed in a value",
				ErrBadOption, tok)
		}
	}
	return tokens, nil
}

// IsPlayerFlag reports whether tok is a well-formed engine flag a player is
// allowed to add. Lowercase letters and digits after one hyphen, and nothing
// else: every real Dota flag looks like this, and it leaves no room for a
// second hyphen, a slash or a quote to mean something to somebody's shell.
func IsPlayerFlag(tok string) bool {
	name, ok := strings.CutPrefix(tok, "-")
	if !ok || name == "" || len(name) > 24 {
		return false
	}
	for _, c := range name {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_':
		default:
			return false
		}
	}
	return true
}

// isValue reports whether tok is acceptable as a flag's value: a window
// height, a language, a path. Nothing that could be read as another argument.
func isValue(tok string) bool {
	for _, c := range tok {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.', c == '_', c == ':', c == '/', c == '\\':
		default:
			return false
		}
	}
	return true
}
