package oidcissuer

import (
	"cmp"
	"maps"
	"net/http"
	"slices"
	"sync"
	"time"
)

const (
	freeFailures = 5
	maxBackoff   = 15 * time.Minute
	// maxTrackedKeys bounds each table. When full, the entries with the fewest failures go first
	// (oldest first among equals), so flooding with fresh names evicts other fresh names and never
	// a key that is already being slowed down.
	maxTrackedKeys = 50000
	trackedIdle    = time.Hour

	// Per source: a looser limit than per (user, source), because clients behind one NAT or
	// proxy share a source. It only ever slows, to at most srcMaxBackoff.
	srcFreeFailures = 30
	srcMaxBackoff   = time.Minute

	// Per user, from any source: past userCeilingFailures failures within userCeilingWindow every
	// login of that user waits userCeilingDelay before its password is checked. The delay is
	// never a refusal, so an attacker cannot lock a user out with it, only cap the guess rate.
	userCeilingFailures = 30
	userCeilingWindow   = time.Hour
	userCeilingDelay    = 2 * time.Second
)

type failState struct {
	n    int
	last time.Time
}

// failTable is a bounded map of failure counters.
type failTable struct {
	mu sync.Mutex
	m  map[string]failState
}

// bump counts one failure of key at now. window > 0 restarts the
// count when the last failure is older than window.
func (t *failTable) bump(key string, now time.Time, window time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.m == nil {
		t.m = map[string]failState{}
	}
	if _, ok := t.m[key]; !ok && len(t.m) >= maxTrackedKeys {
		t.evict(now)
	}
	s := t.m[key]
	if window > 0 && now.Sub(s.last) > window {
		s.n = 0
	}
	s.n++
	s.last = now
	t.m[key] = s
}

func (t *failTable) get(key string) (failState, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.m[key]
	return s, ok
}

func (t *failTable) forget(key string) {
	t.mu.Lock()
	delete(t.m, key)
	t.mu.Unlock()
}

// evict makes room: idle entries first, then a tenth of the table, fewest failures and oldest first.
func (t *failTable) evict(now time.Time) {
	maps.DeleteFunc(t.m, func(_ string, s failState) bool { return now.Sub(s.last) > trackedIdle })
	if len(t.m) < maxTrackedKeys {
		return
	}
	keys := slices.SortedFunc(maps.Keys(t.m), func(a, b string) int {
		return cmp.Or(cmp.Compare(t.m[a].n, t.m[b].n), t.m[a].last.Compare(t.m[b].last))
	})
	for _, k := range keys[:len(keys)/10+1] {
		delete(t.m, k)
	}
}

// Backoff counts failed attempts per key in memory: after free failures a key
// must wait 1 s, 2 s, 4 s ... up to max since its last failure. It is per
// replica (ADR 0094, 0108): with N replicas an attacker gets N times the free
// guesses. The login keys it by (user, source); the UserService password
// change keys it by the signed-in user.
type Backoff struct {
	t    failTable
	free int
	max  time.Duration
}

// NewBackoff returns an empty Backoff with five free failures and a 15 minute ceiling.
func NewBackoff() *Backoff { return &Backoff{free: freeFailures, max: maxBackoff} }

// Wait returns how long key must still wait; zero means it may try.
func (b *Backoff) Wait(key string, now time.Time) time.Duration {
	s, ok := b.t.get(key)
	if !ok || s.n < b.free {
		return 0
	}
	shift := min(s.n-b.free, 20)
	d := min(time.Second<<shift, b.max)
	return max(0, s.last.Add(d).Sub(now))
}

// Fail records a failed attempt for key at now.
func (b *Backoff) Fail(key string, now time.Time) { b.t.bump(key, now, 0) }

// Reset forgets the failures of key, after a success.
func (b *Backoff) Reset(key string) { b.t.forget(key) }

// userCeiling counts failures per user across all sources.
type userCeiling struct{ t failTable }

// delay is how long a login of user waits before its password is checked.
func (c *userCeiling) delay(user string, now time.Time) time.Duration {
	if s, ok := c.t.get(user); ok && s.n >= userCeilingFailures && now.Sub(s.last) <= userCeilingWindow {
		return userCeilingDelay
	}
	return 0
}

func (c *userCeiling) fail(user string, now time.Time) { c.t.bump(user, now, userCeilingWindow) }

// bucket is a token-bucket rate limit per source: burst requests at once, rate per second after.
type bucket struct {
	mu          sync.Mutex
	m           map[string]bucketState
	rate, burst float64
}

type bucketState struct {
	tokens float64
	at     time.Time
}

const maxBuckets = 20000

func newBucket(rate, burst float64) *bucket {
	return &bucket{m: map[string]bucketState{}, rate: rate, burst: burst}
}

// allow takes a token for key; false means the source is over its rate.
func (b *bucket) allow(key string, now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.m[key]
	if !ok {
		if len(b.m) >= maxBuckets {
			// Drop sources that have refilled completely; they cost nothing to forget. A full table
			// of active sources is an attack by that many addresses: refuse the new one.
			maps.DeleteFunc(b.m, func(_ string, v bucketState) bool {
				return v.tokens+now.Sub(v.at).Seconds()*b.rate >= b.burst
			})
			if len(b.m) >= maxBuckets {
				return false
			}
		}
		s = bucketState{tokens: b.burst, at: now}
	}
	if dt := now.Sub(s.at).Seconds(); dt > 0 { // a clock step back refills nothing
		s.tokens = min(b.burst, s.tokens+dt*b.rate)
		s.at = now
	}
	ok = s.tokens >= 1
	if ok {
		s.tokens--
	}
	b.m[key] = s
	return ok
}

// limit wraps h with the per-source rate limit.
func (i *Issuer) limit(b *bucket, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !b.allow(i.clientIP(r), i.cfg.Now()) {
			w.Header().Set("Retry-After", "1")
			oauthError(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
			return
		}
		h(w, r)
	}
}

// jtiSet remembers consumed token ids until they expire.
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
	maps.DeleteFunc(s.m, func(_ string, e time.Time) bool { return e.Before(now) })
	s.m[id] = exp
	return true
}
