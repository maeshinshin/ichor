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
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/maeshinshin/ichor/pkg/release"
)

// init refuses a set the compatibility table does not hold together (ADR 0115).
func TestCheckInitCompatRefusesAnIncompatibleSet(t *testing.T) {
	t.Parallel()

	if err := checkInitCompat("v1.14.3", "v1.32.0", release.Pinned.Cilium); err == nil || !strings.Contains(err.Error(), "v1.32.0") {
		t.Fatalf("init with Kubernetes the Talos does not run: err = %v", err)
	}

	if err := checkInitCompat("v1.14.3", "v1.37.1", "1.18.0"); err == nil || !strings.Contains(err.Error(), "1.18.0") {
		t.Fatalf("init with a Cilium that does not run the Kubernetes: err = %v", err)
	}
}

// adopt warns about a pair the table marks incompatible, and refuses a version the table does not
// know at all, since Ichor could not operate that cluster (ADR 0115).
func TestCheckAdoptCompatWarnsOrRefuses(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := checkAdoptCompat(&out, "v1.14.3", "v1.32.0", true); err != nil {
		t.Fatalf("an incompatible but known pair is refused: %v", err)
	}

	if !strings.Contains(out.String(), "warning") || !strings.Contains(out.String(), "v1.32.0") {
		t.Fatalf("adopt did not warn about the pair: %q", out.String())
	}

	out.Reset()

	err := checkAdoptCompat(&out, "v1.15.0", release.Pinned.Kubernetes, true)
	if !errors.Is(err, release.ErrUnknownVersion) {
		t.Fatalf("a Talos the table does not know: err = %v, want ErrUnknownVersion", err)
	}
}

// An unmanaged adopt installs no Cilium, so the Cilium the chart this Ichor embeds would install is not
// judged: the pair is judged as the cluster runs it (ADR 0115, amendment 3).
func TestCheckAdoptCompatJudgesNoCiliumOfAnUnmanagedAdopt(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := checkAdoptCompat(&out, "v1.14.3", "v1.32.0", false); err != nil {
		t.Fatalf("an unmanaged adopt is refused: %v", err)
	}

	if strings.Contains(strings.ToLower(out.String()), "cilium") {
		t.Fatalf("an unmanaged adopt judged a Cilium Ichor does not install: %q", out.String())
	}
}
