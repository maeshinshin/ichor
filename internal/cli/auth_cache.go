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

package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const authDirPerm os.FileMode = 0o700

// authCacheEntry is one login: the ID token kubectl presents and the refresh
// token that renews it. The file holds a refresh token, so it is a credential
// at rest; it is never printed or logged.
type authCacheEntry struct {
	Issuer       string    `json:"issuer"`
	ClientID     string    `json:"clientID"`
	Audience     string    `json:"audience,omitempty"`
	IDToken      string    `json:"idToken"`
	Expiry       time.Time `json:"expiry"`
	RefreshToken string    `json:"refreshToken,omitempty"`
}

// authCacheDir is ~/.ichor/auth.
func authCacheDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding home directory for the login cache: %w", err)
	}

	return filepath.Join(home, ".ichor", "auth"), nil
}

// authCacheKey names the cache file. It keys on (issuer, client); the
// audience joins the key because ichor-kube and ichor-ui tokens differ.
func authCacheKey(issuer, clientID, audience string) string {
	sum := sha256.Sum256([]byte(issuer + "\x00" + clientID + "\x00" + audience))

	return hex.EncodeToString(sum[:])
}

func authCachePath(dir, key string) string { return filepath.Join(dir, key+".json") }

// loadAuthCache returns nil, nil when there is no entry.
func loadAuthCache(dir, key string) (*authCacheEntry, error) {
	data, err := os.ReadFile(authCachePath(dir, key))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("reading the login cache: %w", err)
	}

	var e authCacheEntry
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, fmt.Errorf("the login cache %s is corrupt (run `ichor auth logout`): %w", authCachePath(dir, key), err)
	}

	return &e, nil
}

// saveAuthCache writes the entry atomically (writeFileAtomic forces 0600) into
// a 0700 directory.
func saveAuthCache(dir, key string, e *authCacheEntry) error {
	if err := ensureAuthDir(dir); err != nil {
		return err
	}

	data, err := json.Marshal(e)
	if err != nil {
		return err
	}

	return writeFileAtomic(authCachePath(dir, key), data)
}

func ensureAuthDir(dir string) error {
	if err := os.MkdirAll(dir, authDirPerm); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	// MkdirAll honours the umask and leaves an existing directory alone.
	return os.Chmod(dir, authDirPerm)
}

func deleteAuthCache(dir, key string) error {
	err := os.Remove(authCachePath(dir, key))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	return nil
}

// sameIssuer compares issuer URLs ignoring a trailing slash.
func sameIssuer(a, b string) bool { return strings.TrimRight(a, "/") == strings.TrimRight(b, "/") }

// listAuthCache returns every readable entry in dir by key; an unreadable or
// foreign file is skipped. A missing directory is an empty cache.
func listAuthCache(dir string) (map[string]*authCacheEntry, error) {
	files, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("reading the login cache: %w", err)
	}

	out := map[string]*authCacheEntry{}

	for _, f := range files {
		key, ok := strings.CutSuffix(f.Name(), ".json")
		if !ok {
			continue
		}

		if e, err := loadAuthCache(dir, key); err == nil && e != nil {
			out[key] = e
		}
	}

	return out, nil
}

// deleteAuthCacheIssuer removes every cached login for issuer, of every client
// and audience, or only those of audience when it is not empty. It returns how
// many it removed.
func deleteAuthCacheIssuer(dir, issuer, audience string) (int, error) {
	entries, err := listAuthCache(dir)
	if err != nil {
		return 0, err
	}

	n := 0

	for key, e := range entries {
		if !sameIssuer(e.Issuer, issuer) || (audience != "" && e.Audience != audience) {
			continue
		}

		if err := deleteAuthCache(dir, key); err != nil {
			return n, err
		}

		n++
	}

	return n, nil
}

// lockAuthCache takes an exclusive flock on <key>.lock so concurrent kubectl
// runs refresh (and log in) once. shortcut: flock is Unix-only; Windows
// support needs LockFileEx when the CLI ships there.
func lockAuthCache(dir, key string) (func(), error) {
	if err := ensureAuthDir(dir); err != nil {
		return nil, err
	}

	f, err := os.OpenFile(filepath.Join(dir, key+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()

		return nil, err
	}

	return func() { _ = f.Close() }, nil // closing releases the lock
}
