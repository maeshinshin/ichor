package oidcissuer

import (
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	jose "github.com/go-jose/go-jose/v4"

	"github.com/maeshinshin/ichor/internal/oidctrust"
)

// KeySource gives the issuer its signing key and the public keys to publish.
// Implementations must be safe for concurrent use.
type KeySource interface {
	// Signing returns the private key to sign with now (KeyID set).
	Signing() (jose.JSONWebKey, error)
	// Public returns every public key tokens may still verify against,
	// including keys that are published but not yet signing.
	Public() []jose.JSONWebKey
}

// KeyStore is a KeySource backed by the signing Secret's keys.json content
// (the oidctrust document the controller writes), replaceable at runtime.
type KeyStore struct {
	now  func() time.Time
	keys atomic.Pointer[oidctrust.Keys]
}

// NewKeyStore returns an empty store; call Update before use. now may be nil.
func NewKeyStore(now func() time.Time) *KeyStore {
	if now == nil {
		now = time.Now
	}
	return &KeyStore{now: now}
}

// Update replaces the key set with the parsed keys.json content. On error the
// previous set stays in place.
func (s *KeyStore) Update(data []byte) error {
	ks, err := oidctrust.ParseKeys(data)
	if err != nil {
		return fmt.Errorf("oidcissuer: %w", err)
	}
	if len(ks.Keys) == 0 {
		return errors.New("oidcissuer: keys: empty key set")
	}
	s.keys.Store(&ks)
	return nil
}

func (s *KeyStore) Signing() (jose.JSONWebKey, error) {
	p := s.keys.Load()
	if p == nil {
		return jose.JSONWebKey{}, errors.New("oidcissuer: no signing keys loaded")
	}
	k, ok := p.Signing(s.now())
	if !ok {
		return jose.JSONWebKey{}, errors.New("oidcissuer: no active signing key")
	}
	priv, err := k.Private()
	if err != nil {
		return jose.JSONWebKey{}, fmt.Errorf("oidcissuer: key %q: %w", k.KID, err)
	}
	return sigKey(priv, k.KID), nil
}

func (s *KeyStore) Public() []jose.JSONWebKey {
	p := s.keys.Load()
	if p == nil {
		return nil
	}
	var out []jose.JSONWebKey
	for _, k := range p.Published(s.now()) {
		priv, err := k.Private()
		if err != nil {
			continue // ParseKeys validated every key
		}
		out = append(out, sigKey(&priv.PublicKey, k.KID))
	}
	return out
}

func sigKey(key any, kid string) jose.JSONWebKey {
	return jose.JSONWebKey{Key: key, KeyID: kid, Algorithm: string(jose.RS256), Use: "sig"}
}
