// Package secret holds the one password primitive the platform uses.
//
// It is its own package because two unrelated things need it: an account
// password, and the password on a private room. Neither should have to import
// the other, and neither should be tempted to grow a second scheme.
package secret

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id, with the parameters encoded into every stored hash.
//
// Storing the parameters rather than assuming them is what lets us raise the
// cost later without invalidating anybody's password: an old hash still
// verifies against the settings it was made with, and can be re-hashed at the
// next successful login. A scheme that hard-codes its cost can never be
// strengthened.
//
// No custom cryptography, per the project's standing rule. This is the
// library implementation with the parameters the Argon2 RFC recommends for
// interactive logins.
const (
	argonTime    = 1
	argonMemory  = 64 * 1024 // 64 MiB
	argonThreads = 4
	argonKeyLen  = 32
	saltLen      = 16
)

var (
	// ErrPasswordMismatch is deliberately the same error whatever went wrong,
	// so nothing about a failed login distinguishes "no such user" from
	// "wrong password" to somebody probing for valid usernames.
	ErrPasswordMismatch = errors.New("account: username or password is wrong")
	errBadHash          = errors.New("account: stored password hash is unreadable")
)

// HashPassword returns an encoded Argon2id hash, salt and parameters included.
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("secret: generating salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword reports whether password matches the encoded hash.
func VerifyPassword(encoded, password string) error {
	params, salt, want, err := decodeHash(encoded)
	if err != nil {
		return err
	}
	got := argon2.IDKey([]byte(password), salt,
		params.time, params.memory, params.threads, uint32(len(want)))

	// Constant time: a comparison that returns early leaks how much of the
	// hash matched.
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrPasswordMismatch
	}
	return nil
}

// NeedsRehash reports whether a stored hash was made with weaker parameters
// than we now use, so it can be upgraded at the next successful login.
func NeedsRehash(encoded string) bool {
	params, _, _, err := decodeHash(encoded)
	if err != nil {
		// Unreadable means it cannot be verified against either; rewriting it
		// is the only useful thing left to do with it.
		return true
	}
	return params.memory < argonMemory ||
		params.time < argonTime ||
		params.threads < argonThreads
}

type argonParams struct {
	memory  uint32
	time    uint32
	threads uint8
}

func decodeHash(encoded string) (argonParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	// "", "argon2id", "v=19", "m=..,t=..,p=..", salt, key
	if len(parts) != 6 || parts[1] != "argon2id" {
		return argonParams{}, nil, nil, errBadHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return argonParams{}, nil, nil, errBadHash
	}
	if version != argon2.Version {
		return argonParams{}, nil, nil, fmt.Errorf(
			"%w: made by argon2 version %d, this build speaks %d",
			errBadHash, version, argon2.Version)
	}

	var p argonParams
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &p.threads); err != nil {
		return argonParams{}, nil, nil, errBadHash
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return argonParams{}, nil, nil, errBadHash
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) == 0 {
		return argonParams{}, nil, nil, errBadHash
	}
	return p, salt, key, nil
}
