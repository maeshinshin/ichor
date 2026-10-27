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

package controller

import (
	"testing"
	"time"

	"github.com/maeshinshin/ichor/internal/oidcissuer"
	"github.com/maeshinshin/ichor/internal/oidctrust"
)

// The issuer must read exactly what the controller writes: this feeds KeyStore a document made by
// rotateKeys and marshalled the way the controller stores it, across a rotation.
func TestIssuerKeyStoreReadsControllerKeys(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	ks, _, _, err := rotateKeys(oidctrust.Keys{}, t0, testPlan)
	if err != nil {
		t.Fatal(err)
	}

	later := t0.Add(testPlan.RotateEvery)

	ks, _, _, err = rotateKeys(ks, later, testPlan)
	if err != nil || len(ks.Keys) != 2 {
		t.Fatalf("rotation: keys=%d err=%v", len(ks.Keys), err)
	}

	raw, err := ks.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	now := later
	store := oidcissuer.NewKeyStore(func() time.Time { return now })

	if err := store.Update(raw); err != nil {
		t.Fatalf("issuer rejected the controller's keys.json: %v", err)
	}

	// Before the successor's activation: old signs, both published.
	sig, err := store.Signing()
	if err != nil || sig.KeyID != ks.Keys[0].KID {
		t.Fatalf("signing before activation: %v %v", sig.KeyID, err)
	}

	if got := len(store.Public()); got != 2 {
		t.Fatalf("published before activation = %d, want 2", got)
	}

	now = later.Add(testPlan.ActivationDelay)

	if sig, err = store.Signing(); err != nil || sig.KeyID != ks.Keys[1].KID {
		t.Fatalf("signing after activation: %v %v", sig.KeyID, err)
	}

	// After the old key's retire time only the new key is published.
	now = later.Add(testPlan.ActivationDelay + testPlan.TokenLife + testPlan.RetireGrace)

	if pub := store.Public(); len(pub) != 1 || pub[0].KeyID != ks.Keys[1].KID {
		t.Fatalf("published after retirement: %v", pub)
	}

	// A bad update keeps the previous set.
	if err := store.Update([]byte(`[]`)); err == nil {
		t.Fatal("bare array accepted")
	}

	if _, err := store.Signing(); err != nil {
		t.Fatalf("previous set lost: %v", err)
	}
}
