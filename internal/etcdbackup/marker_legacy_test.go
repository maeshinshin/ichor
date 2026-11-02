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

package etcdbackup

import (
	"context"
	"testing"
)

// ADR 0108: a marker signed with the v1 key by an earlier release is still accepted after the upgrade.
func TestMarkerSignedWithTheV1KeyIsAccepted(t *testing.T) {
	st := memStore{}
	ctx := context.Background()

	if err := PutMarker(ctx, st, "p", Key{Current: "v1"}, &RestoreMarker{}); err != nil {
		t.Fatal(err)
	}

	if _, err := GetMarker(ctx, st, "p", Key{Current: "v2", Legacy: "v1"}); err != nil {
		t.Fatalf("v1-signed marker refused: %v", err)
	}

	if _, err := GetMarker(ctx, st, "p", Key{Current: "v2", Legacy: "unknown"}); err == nil {
		t.Fatal("a marker signed with an unknown key was accepted")
	}
}
