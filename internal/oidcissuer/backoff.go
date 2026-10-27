package oidcissuer

import (
	"sync"
	"time"
)

const (
	freeFailures   = 5
	maxBackoff     = 15 * time.Minute
	maxTrackedKeys = 10000
	trackedIdle    = time.Hour
)

type failState struct {
	n    int
	last time.Time
}

// backoff counts failed logins per key (user name or source IP) in memory:
// after five failures a key must wait 1 s, 2 s, 4 s ... up to 15 minutes since
// its last failure. It is per replica by design (ADR 0094).
type backoff struct {
	mu sync.Mutex
	m  map[string]failState
}

func newBackoff() *backoff { return &backoff{m: map[string]failState{}} }

// wait returns how long key must still wait; zero means it may try.
func (b *backoff) wait(key string, now time.Time) time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.m[key]
	if !ok || s.n < freeFailures {
		return 0
	}
	shift := min(s.n-freeFailures, 20)
	d := min(time.Second<<shift, maxBackoff)
	if left := s.last.Add(d).Sub(now); left > 0 {
		return left
	}
	return 0
}

func (b *backoff) fail(key string, now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.m) >= maxTrackedKeys {
		for k, s := range b.m {
			if now.Sub(s.last) > trackedIdle {
				delete(b.m, k)
			}
		}
		for k := range b.m { // still full: evict arbitrary entries
			if len(b.m) < maxTrackedKeys {
				break
			}
			delete(b.m, k)
		}
	}
	s := b.m[key]
	s.n++
	s.last = now
	b.m[key] = s
}

func (b *backoff) reset(key string) {
	b.mu.Lock()
	delete(b.m, key)
	b.mu.Unlock()
}

// jtiSet remembers consumed authorization-code ids until they expire.
type jtiSet struct {
	mu sync.Mutex
	m  map[string]time.Time
}

func newJTISet() *jtiSet { return &jtiSet{m: map[string]time.Time{}} }

// use records id and reports false if it was already used.
func (s *jtiSet) use(id string, exp, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[id]; ok {
		return false
	}
	for k, e := range s.m {
		if e.Before(now) {
			delete(s.m, k)
		}
	}
	s.m[id] = exp
	return true
}
