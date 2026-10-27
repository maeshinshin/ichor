package oidcissuer

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	jose "github.com/go-jose/go-jose/v4"
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

// KeyRecord is one entry of the signing Secret's keys.json (a JSON array of
// these). The controller writes it; the issuer only reads it.
type KeyRecord struct {
	KID         string    `json:"kid"`
	PEM         string    `json:"pem"` // PKCS#8 or PKCS#1 RSA private key
	CreatedAt   time.Time `json:"createdAt"`
	ActiveFrom  time.Time `json:"activeFrom"`  // earliest time the key may sign
	RetireAfter time.Time `json:"retireAfter"` // zero means never; after it the key is dropped
}

type loadedKey struct {
	rec  KeyRecord
	priv *rsa.PrivateKey
}

// KeyStore is a KeySource backed by keys.json content that can be replaced at
// runtime. Signing picks the most recently activated key whose ActiveFrom has
// passed; Public lists every key that is not retired.
type KeyStore struct {
	now  func() time.Time
	keys atomic.Pointer[[]loadedKey]
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
	var recs []KeyRecord
	if err := json.Unmarshal(data, &recs); err != nil {
		return fmt.Errorf("oidcissuer: parse keys: %w", err)
	}
	if len(recs) == 0 {
		return errors.New("oidcissuer: keys: empty key set")
	}
	out := make([]loadedKey, 0, len(recs))
	seen := map[string]bool{}
	for _, r := range recs {
		if r.KID == "" || seen[r.KID] {
			return errors.New("oidcissuer: keys: kid empty or duplicated")
		}
		seen[r.KID] = true
		block, _ := pem.Decode([]byte(r.PEM))
		if block == nil {
			return fmt.Errorf("oidcissuer: key %q: no PEM block", r.KID)
		}
		priv, err := parseRSA(block.Bytes)
		if err != nil {
			return fmt.Errorf("oidcissuer: key %q: %w", r.KID, err)
		}
		out = append(out, loadedKey{rec: r, priv: priv})
	}
	s.keys.Store(&out)
	return nil
}

func parseRSA(der []byte) (*rsa.PrivateKey, error) {
	if k, err := x509.ParsePKCS8PrivateKey(der); err == nil {
		if r, ok := k.(*rsa.PrivateKey); ok {
			return r, nil
		}
		return nil, errors.New("not an RSA key")
	}
	k, err := x509.ParsePKCS1PrivateKey(der)
	if err != nil {
		return nil, errors.New("unparseable RSA private key")
	}
	return k, nil
}

func (s *KeyStore) Signing() (jose.JSONWebKey, error) {
	p := s.keys.Load()
	if p == nil {
		return jose.JSONWebKey{}, errors.New("oidcissuer: no signing keys loaded")
	}
	now := s.now()
	var best *loadedKey
	for i := range *p {
		k := &(*p)[i]
		if k.rec.ActiveFrom.After(now) || retired(k.rec, now) {
			continue
		}
		if best == nil || k.rec.ActiveFrom.After(best.rec.ActiveFrom) {
			best = k
		}
	}
	if best == nil {
		return jose.JSONWebKey{}, errors.New("oidcissuer: no active signing key")
	}
	return jose.JSONWebKey{Key: best.priv, KeyID: best.rec.KID, Algorithm: string(jose.RS256), Use: jwkUseSig}, nil
}

func (s *KeyStore) Public() []jose.JSONWebKey {
	p := s.keys.Load()
	if p == nil {
		return nil
	}
	now := s.now()
	var out []jose.JSONWebKey
	for _, k := range *p {
		if retired(k.rec, now) {
			continue
		}
		out = append(out, jose.JSONWebKey{Key: &k.priv.PublicKey, KeyID: k.rec.KID, Algorithm: string(jose.RS256), Use: jwkUseSig})
	}
	return out
}

func retired(r KeyRecord, now time.Time) bool {
	return !r.RetireAfter.IsZero() && now.After(r.RetireAfter)
}
