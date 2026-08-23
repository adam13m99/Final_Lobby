// Package chat carries the lobby's text channels: one shared lobby channel
// and one per room.
//
// Chat is not incidental. Two players who cannot say "loading now, give me
// twenty seconds" have to coordinate a match by telephone, which is exactly
// how the predecessor platform felt to use.
package chat

import (
	"errors"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// Lobby is the channel everyone sees, in or out of a room.
	Lobby = "lobby"

	// MaxTextRunes is generous enough for a sentence and short enough that
	// one player cannot push the rest of the conversation off the screen.
	MaxTextRunes = 300

	// History is how many messages a channel keeps. A player who joins late
	// sees the recent conversation and nothing older.
	History = 100
)

var (
	ErrEmpty   = errors.New("chat: message is empty")
	ErrTooLong = errors.New("chat: message is too long")
)

// Message is one line of chat.
type Message struct {
	ID       uint64    `json:"id"`
	PlayerID string    `json:"player_id,omitempty"`
	Nick     string    `json:"nick"`
	Text     string    `json:"text"`
	At       time.Time `json:"at"`
	System   bool      `json:"system,omitempty"`
}

// Clean trims a message and rejects anything unusable. Newlines and other
// control characters become spaces rather than an error: a player who pastes
// two lines meant to send two lines, not to be told off.
func Clean(text string) (string, error) {
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, text)
	text = strings.TrimSpace(text)
	if text == "" {
		return "", ErrEmpty
	}
	if utf8.RuneCountInString(text) > MaxTextRunes {
		return "", ErrTooLong
	}
	return text, nil
}

type channel struct {
	msgs []Message
}

// Board holds every channel.
type Board struct {
	mu       sync.Mutex
	channels map[string]*channel
	nextID   uint64
}

func NewBoard() *Board {
	return &Board{channels: make(map[string]*channel)}
}

// Post adds a player's message and returns it with its assigned ID.
func (b *Board) Post(channelID, playerID, nick, text string, now time.Time) (Message, error) {
	clean, err := Clean(text)
	if err != nil {
		return Message{}, err
	}
	return b.append(channelID, Message{
		PlayerID: playerID,
		Nick:     nick,
		Text:     clean,
		At:       now,
	}), nil
}

// System adds a message from the server itself - somebody joined, the host
// locked the room, a player was kicked. These are what make a room feel
// alive when nobody is typing.
func (b *Board) System(channelID, text string, now time.Time) Message {
	return b.append(channelID, Message{Text: text, At: now, System: true})
}

func (b *Board) append(channelID string, m Message) Message {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.nextID++
	m.ID = b.nextID

	c, ok := b.channels[channelID]
	if !ok {
		c = &channel{}
		b.channels[channelID] = c
	}
	c.msgs = append(c.msgs, m)
	if len(c.msgs) > History {
		// Copy rather than reslice: reslicing keeps the whole backing array
		// alive, so a busy channel would never release the memory it grew.
		c.msgs = append([]Message(nil), c.msgs[len(c.msgs)-History:]...)
	}
	return m
}

// Since returns messages newer than a cursor, oldest first. A cursor of zero
// returns the whole retained history, which is what a client wants when it
// opens a channel for the first time.
func (b *Board) Since(channelID string, cursor uint64) []Message {
	b.mu.Lock()
	defer b.mu.Unlock()

	c, ok := b.channels[channelID]
	if !ok {
		return nil
	}
	var out []Message
	for _, m := range c.msgs {
		if m.ID > cursor {
			out = append(out, m)
		}
	}
	return out
}

// Cursor is the newest message ID in a channel, so a client that only wants
// what happens next can skip the backlog.
func (b *Board) Cursor(channelID string) uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	c, ok := b.channels[channelID]
	if !ok || len(c.msgs) == 0 {
		return 0
	}
	return c.msgs[len(c.msgs)-1].ID
}

// Drop forgets a channel. Called when a room closes, or every finished match
// would leak its conversation for as long as the process runs.
func (b *Board) Drop(channelID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.channels, channelID)
}
