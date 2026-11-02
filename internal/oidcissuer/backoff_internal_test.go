package oidcissuer

import (
	"strconv"
	"testing"
	"time"
)

func TestFloodingFreshKeysDoesNotEvictASlowedKey(t *testing.T) {
	b := NewBackoff()
	now := time.Now()

	for range 6 {
		b.Fail("victim", now)
	}

	if b.Wait("victim", now) == 0 {
		t.Fatal("setup: victim is not slowed")
	}

	for n := range 3 * maxTrackedKeys {
		b.Fail("fake-"+strconv.Itoa(n), now)
	}

	if b.Wait("victim", now) == 0 {
		t.Fatal("an attacker rotating usernames evicted the victim's counter")
	}

	if len(b.t.m) > maxTrackedKeys {
		t.Fatalf("table grew to %d", len(b.t.m))
	}
}
