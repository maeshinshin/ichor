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
	"strings"
	"testing"
)

func TestImageOverrideFromFlags(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)

	got, err := imageOverrideFromFlags("v1.20.5", []string{"agent=quay.io/cilium/cilium:v1.20.5@" + digest})
	if err != nil {
		t.Fatalf("imageOverrideFromFlags: %v", err)
	}

	if got.Version != "v1.20.5" || got.Images["agent"] != "quay.io/cilium/cilium:v1.20.5@"+digest {
		t.Errorf("override = %+v", got)
	}

	if none, err := imageOverrideFromFlags("", nil); none != nil || err != nil {
		t.Errorf("no flags = %+v, %v; want nothing", none, err)
	}

	for _, bad := range []struct {
		name, version string
		overrides     []string
	}{
		{"an image without a version", "", []string{"agent=quay.io/cilium/cilium:v1.20.5@" + digest}},
		{"a version without images", "v1.20.5", nil},
		{"an image without a reference", "v1.20.5", []string{"agent="}},
		{"an image without a key", "v1.20.5", []string{"quay.io/cilium/cilium"}},
		{"the same image twice", "v1.20.5", []string{"agent=a", "agent=b"}},
	} {
		if _, err := imageOverrideFromFlags(bad.version, bad.overrides); err == nil {
			t.Errorf("%s was accepted", bad.name)
		}
	}
}
