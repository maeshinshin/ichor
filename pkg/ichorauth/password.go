/*
Copyright 2026 maeshinshin.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package ichorauth holds what ichor-ui, the controller and the CLI share about Ichor's own
// users: argon2id password hashes, the password policy, the credentials-Secret entry
// and the token claim shapes. Nothing in it logs, and no error it returns contains a password
// or a hash.
package ichorauth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Password policy: length only, no composition rules.
const (
	// MinPasswordLength is counted in characters (runes).
	MinPasswordLength = 12
	// MaxPasswordLength is counted in bytes; it bounds the work an unauthenticated caller can make
	// the server hash.
	MaxPasswordLength = 128
	// GeneratedPasswordLength is the length of a password Ichor generates for an admin.
	GeneratedPasswordLength = 24
)

// Params are the argon2id cost parameters. DefaultParams is RFC 9106's second recommended option,
// the one golang.org/x/crypto/argon2 documents for IDKey.
type Params struct {
	Time    uint32 // passes
	Memory  uint32 // KiB
	Threads uint8
	KeyLen  uint32
	SaltLen uint32
}

// DefaultParams: time=3, memory=64 MiB, threads=4, 32-byte key, 16-byte salt.
var DefaultParams = Params{Time: 3, Memory: 64 * 1024, Threads: 4, KeyLen: 32, SaltLen: 16}

// Upper bounds accepted when decoding a stored hash, so a corrupted or hostile entry cannot make
// Verify allocate gigabytes.
const (
	maxDecodeMemory  = 256 * 1024 // KiB
	maxDecodeTime    = 10
	maxDecodeThreads = 16
	maxDecodeKeyLen  = 64
	maxDecodeSaltLen = 64
)

var (
	// ErrPasswordTooShort and ErrPasswordTooLong are the policy failures.
	ErrPasswordTooShort = fmt.Errorf("password must be at least %d characters", MinPasswordLength)
	ErrPasswordTooLong  = fmt.Errorf("password must be at most %d bytes", MaxPasswordLength)
	// ErrInvalidHash means the stored string is not a hash this package can read.
	ErrInvalidHash = errors.New("invalid password hash encoding")
	// ErrIncompatibleVersion means the hash was made by a different argon2 version.
	ErrIncompatibleVersion = errors.New("incompatible argon2 version")
)

// ValidatePassword applies the password policy.
func ValidatePassword(password string) error {
	if len(password) > MaxPasswordLength {
		return ErrPasswordTooLong
	}
	if !utf8.ValidString(password) || utf8.RuneCountInString(password) < MinPasswordLength {
		return ErrPasswordTooShort
	}
	return nil
}

// Hash validates the password against the policy and returns its PHC-encoded argon2id hash with
// DefaultParams: $argon2id$v=19$m=65536,t=3,p=4$<salt>$<key>, unpadded standard base64.
func Hash(password string) (string, error) {
	return HashWithParams(password, DefaultParams)
}

// HashWithParams is Hash with explicit parameters (tests use cheap ones).
func HashWithParams(password string, p Params) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	return hashUnchecked(password, p)
}

func hashUnchecked(password string, p Params) (string, error) {
	salt := make([]byte, p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("read random salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Time, p.Threads, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

// decode parses a PHC string, enforcing the bounds above.
func decode(encoded string) (p Params, salt, key []byte, err error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return p, nil, nil, ErrInvalidHash
	}
	var version int
	if _, err = fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return p, nil, nil, ErrInvalidHash
	}
	if version != argon2.Version {
		return p, nil, nil, ErrIncompatibleVersion
	}
	var m, t, th uint64
	for kv := range strings.SplitSeq(parts[3], ",") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return p, nil, nil, ErrInvalidHash
		}
		n, perr := strconv.ParseUint(v, 10, 32)
		if perr != nil {
			return p, nil, nil, ErrInvalidHash
		}
		switch k {
		case "m":
			m = n
		case "t":
			t = n
		case "p":
			th = n
		default:
			return p, nil, nil, ErrInvalidHash
		}
	}
	if m == 0 || t == 0 || th == 0 || m > maxDecodeMemory || t > maxDecodeTime || th > maxDecodeThreads {
		return p, nil, nil, ErrInvalidHash
	}
	enc := base64.RawStdEncoding.Strict()
	if salt, err = enc.DecodeString(parts[4]); err != nil || len(salt) == 0 || len(salt) > maxDecodeSaltLen {
		return p, nil, nil, ErrInvalidHash
	}
	if key, err = enc.DecodeString(parts[5]); err != nil || len(key) == 0 || len(key) > maxDecodeKeyLen {
		return p, nil, nil, ErrInvalidHash
	}
	p = Params{
		Time: uint32(t), Memory: uint32(m), Threads: uint8(th), KeyLen: uint32(len(key)), SaltLen: uint32(len(salt)),
	}
	return p, salt, key, nil
}

// Verify reports whether password matches the encoded hash, comparing in constant time. The
// second result is true when the hash was made with weaker parameters than DefaultParams, so the
// caller should store a fresh Hash of the (now known) password. A malformed hash returns an error.
func Verify(password, encoded string) (match, needsRehash bool, err error) {
	p, salt, key, err := decode(encoded)
	if err != nil {
		return false, false, err
	}
	got := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	match = subtle.ConstantTimeCompare(got, key) == 1
	return match, match && weaker(p, DefaultParams), nil
}

func weaker(p, want Params) bool {
	return p.Time < want.Time || p.Memory < want.Memory || p.Threads < want.Threads || p.KeyLen < want.KeyLen
}

// dummyHash is a hash of a random, discarded input, so no one can log in with it.
var dummyHash = sync.OnceValue(func() string {
	h, _ := hashUnchecked(rand.Text(), DefaultParams)
	return h
})

// VerifyDummy burns the same work as Verify against a hash nobody can match. Call it for an
// unknown or password-less user so the response time does not reveal which users exist. It always
// returns false.
func VerifyDummy(password string) bool {
	_, _, _ = Verify(password, dummyHash())
	return false
}

const passwordAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"

// GeneratePassword returns GeneratedPasswordLength random characters from crypto/rand, without
// look-alike characters. The caller prints it once and stores it nowhere.
func GeneratePassword() (string, error) {
	out := make([]byte, GeneratedPasswordLength)
	limit := byte(256 - 256%len(passwordAlphabet)) // reject at or above this to avoid modulo bias
	buf := make([]byte, 1)
	for i := 0; i < len(out); {
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("read random: %w", err)
		}
		if buf[0] >= limit {
			continue
		}
		out[i] = passwordAlphabet[int(buf[0])%len(passwordAlphabet)]
		i++
	}
	return string(out), nil
}
