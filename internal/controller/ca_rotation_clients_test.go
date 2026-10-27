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
	"encoding/pem"
	"testing"

	"github.com/maeshinshin/ichor/internal/secretsbundle"
)

func TestPemBundleHolds(t *testing.T) {
	pemOf := func(der string) []byte {
		return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte(der)})
	}

	oldCA, newCA := pemOf("old-ca"), pemOf("new-ca")

	fp, err := secretsbundle.Fingerprint(newCA)
	if err != nil {
		t.Fatal(err)
	}

	for name, tc := range map[string]struct {
		bundle []byte
		want   bool
	}{
		"only the old CA":      {oldCA, false},
		"only the new CA":      {newCA, true},
		"both, new one second": {append(append([]byte{}, oldCA...), newCA...), true},
		"empty":                {nil, false},
		"not PEM":              {[]byte("garbage"), false},
	} {
		if got := pemBundleHolds(tc.bundle, fp); got != tc.want {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}
