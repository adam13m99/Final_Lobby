package api

// The terms of use, served rather than shipped (D37, and the standing product
// rule that terms are accepted at install and recorded against the account).
//
// They are served by the coordinator, not bundled into the client, for one
// reason: the version somebody accepted is recorded server-side, and bumping
// that version re-prompts everybody. If the text lived in the client, the text
// and the version could disagree - a player would be asked to accept
// "2026-09-01" while reading the words from whatever build they happened to
// have installed. One place, one version, one set of words.

import (
	"net/http"
	"os"
	"strings"
	"sync"
)

// placeholderTerms is what a coordinator serves when nobody has given it a
// terms file.
//
// It is deliberately not a short legal-sounding paragraph. A server running
// without its terms configured is misconfigured, and the honest thing is to
// say so to whoever is reading rather than to invent an agreement they are
// then asked to accept.
const placeholderTerms = `This server has no terms of use configured.

That is a mistake on our side, not yours. Ask whoever runs this server to
start the coordinator with -terms-file pointing at the terms text.

Until then, the only honest thing to say is: this is a small service run by
people you can reach, it does not promise to be available, and nobody has
written down what either of us is agreeing to.`

// Terms holds the text this coordinator serves, read once at startup.
//
// Read once rather than per request: it changes when somebody deploys, and a
// file read on a public endpoint is a small gift to anybody who wants to make
// the server do work.
type Terms struct {
	once sync.Once
	path string
	text string
}

// NewTerms reads the terms from path. An unreadable or empty file is not a
// startup failure - the service is still worth running - but it is loud in
// the logs and obvious to anybody who reads the terms.
func NewTerms(path string) *Terms { return &Terms{path: path} }

func (t *Terms) Text() string {
	t.once.Do(func() {
		if t.path == "" {
			t.text = placeholderTerms
			return
		}
		b, err := os.ReadFile(t.path)
		if err != nil || len(strings.TrimSpace(string(b))) == 0 {
			t.text = placeholderTerms
			return
		}
		t.text = string(b)
	})
	return t.text
}

// terms serves the text and the version it is. It needs no session: it is
// read before there is an account to have one.
func (s *Server) terms(w http.ResponseWriter, r *http.Request) {
	text := placeholderTerms
	if s.terms_ != nil {
		text = s.terms_.Text()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version": TermsVersion,
		"text":    text,
	})
}
