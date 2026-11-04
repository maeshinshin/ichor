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
	"time"

	"github.com/maeshinshin/ichor/internal/etcdbackup"
)

// The human output of `ichor restore --recover-cluster`, on a pipe: progress steps on
// stderr, the result on stdout, no colour codes.

func TestRestoreRecoverClusterHumanOutput(t *testing.T) {
	h := newRestoreHarness(t)
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	putSnapshot(t, h.store, t1, goodSnapshot(t), h.bundle)
	putSnapshot(t, h.store, t1.Add(time.Hour), corruptSnapshot(t), h.bundle)

	if err := h.run(); err != nil {
		t.Fatal(err)
	}

	name := etcdbackup.ObjectName(t1)
	lines := strings.Split(strings.TrimRight(h.stderr.String(), "\n"), "\n")

	wantPrefix := []string{
		"Recover etcd",
		"cluster lost · node 10.0.0.9:50000",
		"✓ Check inputs — ",
		"! skipping " + etcdbackup.ObjectName(t1.Add(time.Hour)) + ":",
		"✓ Verify snapshot — " + name + " (taken 2026-01-01 00:00:00 UTC, ",
		"✓ Connect — 10.0.0.9:50000",
		"✓ Upload snapshot — ",
		"✓ Bootstrap etcd — from the snapshot",
	}
	if len(lines) != len(wantPrefix) {
		t.Fatalf("stderr has %d lines, want %d:\n%s", len(lines), len(wantPrefix), h.stderr.String())
	}

	for i, want := range wantPrefix {
		if !strings.HasPrefix(lines[i], want) {
			t.Errorf("stderr line %d = %q, want prefix %q", i, lines[i], want)
		}
	}

	want := "✓ etcd on 10.0.0.9:50000 is recovering from " + name + "\n" +
		"Next: once the Kubernetes API is back, the restored Ichor controller re-reconciles the cluster; " +
		"check each machine before approving anything.\n"
	if h.stdout.String() != want {
		t.Errorf("stdout = %q, want %q", h.stdout.String(), want)
	}

	h.assertNoLeak(t, nil)
}
