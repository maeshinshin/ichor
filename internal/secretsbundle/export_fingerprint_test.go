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

package secretsbundle

import (
	"testing"
	"time"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
)

// One export fingerprint covers the four CAs and the retired keys: a copy saved before either
// kind of rotation is stale after it, and only a copy of the live bundle with the live retired list is current.
const etcdTarget = "etcd"

func TestExportFingerprintMovesForEveryKindOfRotation(t *testing.T) {
	start := newTestBundle(t)

	contract, err := coreconfig.ParseContractFromVersion("v1.14")
	if err != nil {
		t.Fatal(err)
	}

	caRot, err := NewRotationFor(time.Now(), contract, []string{etcdTarget})
	if err != nil {
		t.Fatal(err)
	}

	keyRot, err := NewKeyRotation(start, []string{TargetSecretbox})
	if err != nil {
		t.Fatal(err)
	}

	retired := &Retired{}
	retired.Add(RetiredKey{RetiredAt: time.Unix(1, 0).UTC(), Secretbox: start.Secrets.SecretboxEncryptionSecret})

	afterCA := caRot.Apply(start)
	afterKeys := keyRot.Apply(start)
	afterBoth := keyRot.Apply(afterCA)

	fps := map[string]string{}

	for name, tc := range map[string]struct {
		bundle  *secrets.Bundle
		retired *Retired
	}{
		"start": {start, nil}, "ca": {afterCA, nil}, "keys": {afterKeys, retired}, "both": {afterBoth, retired},
	} {
		got, err := ExportFingerprint(tc.bundle, tc.retired)
		if err != nil || got == "" {
			t.Fatalf("%s: %q %v", name, got, err)
		}

		for other, fp := range fps {
			if fp == got {
				t.Fatalf("a copy saved at %q reads as current at %q", other, name)
			}
		}

		fps[name] = got
	}

	// A copy of the new bundle without the retired keys is not the live one either.
	if got, _ := ExportFingerprint(afterKeys, nil); got == fps["keys"] {
		t.Fatal("an export without the retired keys counted as current")
	}
}
