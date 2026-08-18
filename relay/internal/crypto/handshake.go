package crypto

import (
	"crypto/rand"
	"errors"
	"fmt"

	"github.com/flynn/noise"
	"golang.org/x/crypto/curve25519"
)

// ErrHandshake covers every handshake failure. The relay never tells a peer
// why its handshake failed - that would be a probing oracle.
var ErrHandshake = errors.New("crypto: handshake failed")

// cipherSuite is Noise_NK_25519_ChaChaPoly_BLAKE2s.
//
// NK is the right pattern here: the client knows the relay's static public
// key (shipped in the binary) but has no static key of its own. It proves
// its right to connect with a short-lived coordinator ticket carried inside
// the encrypted handshake payload.
var cipherSuite = noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashBLAKE2s)

// GenerateStaticKeypair produces the relay's long-term identity.
func GenerateStaticKeypair() (pub, priv []byte, err error) {
	kp, err := cipherSuite.GenerateKeypair(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrHandshake, err)
	}
	return kp.Public, kp.Private, nil
}

// PublicFromPrivate derives the X25519 public key for a stored private key,
// so only the private half has to be kept on the relay host.
func PublicFromPrivate(priv []byte) ([]byte, error) {
	pub, err := curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrHandshake, err)
	}
	return pub, nil
}

// ClientHandshake builds the first message and returns a finish function
// that completes the handshake once the relay replies.
func ClientHandshake(relayStaticPub, ticket []byte) (msg1 []byte, finish func([]byte) (*Session, error), err error) {
	hs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite: cipherSuite,
		Pattern:     noise.HandshakeNK,
		Initiator:   true,
		PeerStatic:  relayStaticPub,
		Random:      rand.Reader,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrHandshake, err)
	}
	msg1, _, _, err = hs.WriteMessage(nil, ticket)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrHandshake, err)
	}

	finish = func(msg2 []byte) (*Session, error) {
		_, csInitToResp, csRespToInit, err := hs.ReadMessage(nil, msg2)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrHandshake, err)
		}
		// The initiator writes on the initiator->responder stream.
		return sessionFromNoise(csInitToResp, csRespToInit)
	}
	return msg1, finish, nil
}

// ServerHandshake consumes the client's first message, recovers the ticket,
// and produces the reply plus the established session.
func ServerHandshake(relayStaticPriv, msg1 []byte) (ticket, msg2 []byte, sess *Session, err error) {
	pub, err := PublicFromPrivate(relayStaticPriv)
	if err != nil {
		return nil, nil, nil, err
	}
	hs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite:   cipherSuite,
		Pattern:       noise.HandshakeNK,
		Initiator:     false,
		StaticKeypair: noise.DHKey{Private: relayStaticPriv, Public: pub},
		Random:        rand.Reader,
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%w: %v", ErrHandshake, err)
	}
	ticket, _, _, err = hs.ReadMessage(nil, msg1)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%w: %v", ErrHandshake, err)
	}
	msg2, csInitToResp, csRespToInit, err := hs.WriteMessage(nil, nil)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%w: %v", ErrHandshake, err)
	}
	// The responder mirrors the initiator.
	sess, err = sessionFromNoise(csRespToInit, csInitToResp)
	if err != nil {
		return nil, nil, nil, err
	}
	return ticket, msg2, sess, nil
}

// sessionFromNoise adapts Noise CipherStates onto our Session type by
// lifting out the derived directional keys.
func sessionFromNoise(send, recv *noise.CipherState) (*Session, error) {
	if send == nil || recv == nil {
		return nil, ErrHandshake
	}
	sk := send.UnsafeKey()
	rk := recv.UnsafeKey()
	return NewSession(sk[:], rk[:])
}
