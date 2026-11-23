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

package uiapi

import (
	"slices"
	"testing"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/resources/block"
)

func TestInstallableDisks(t *testing.T) {
	got := installableDisks([]*block.DiskSpec{
		{DevPath: "/dev/vda", Size: 10},
		{DevPath: "/dev/sr0", CDROM: true, Readonly: true},
		{DevPath: "/dev/sdb", Readonly: true},
		{DevPath: "/dev/nvme0n1", Size: 20},
	})

	paths := make([]string, 0, len(got))
	for _, d := range got {
		paths = append(paths, d.GetDevPath())
	}

	if want := []string{"/dev/vda", "/dev/nvme0n1"}; !slices.Equal(paths, want) {
		t.Fatalf("got %v, want %v", paths, want)
	}
}

const testSvc = "a-service"

func TestParseLogTime(t *testing.T) {
	want := time.Date(2026, 11, 24, 10, 11, 12, 123456789, time.UTC)

	for name, tc := range map[string]struct {
		service, line string
		want          time.Time
	}{
		"kernel line keeps its own RFC3339Nano time": {"", "kern:    info: [2026-11-24T10:11:12.123456789Z]: eth0 up", want},
		"service JSON ts string":                     {testSvc, `{"level":"info","ts":"2026-11-24T10:11:12.123456789Z","msg":"x"}`, want},
		"service JSON time string":                   {testSvc, `{"time":"2026-11-24T10:11:12.123456789Z","msg":"x"}`, want},
		"service JSON epoch milliseconds":            {testSvc, `{"ts":1795515072123.456,"msg":"x"}`, time.Unix(1795515072, 123456000).UTC()},
		"service plain line has no time":             {testSvc, "just some text", time.Time{}},
		"service JSON without time has none":         {testSvc, `{"msg":"x"}`, time.Time{}},
		"kernel line without a bracketed time":       {"", "kern:    info: eth0 up", time.Time{}},
		"service text that only looks like a kernel": {testSvc, "kern:    info: [2026-11-24T10:11:12Z]: x", time.Time{}},
	} {
		t.Run(name, func(t *testing.T) {
			got := parseLogTime(tc.service, tc.line)
			if tc.want.IsZero() {
				if !got.IsZero() {
					t.Fatalf("parseLogTime = %v, want no time", got)
				}

				return
			}

			if d := got.Sub(tc.want); d > time.Microsecond || d < -time.Microsecond {
				t.Fatalf("parseLogTime = %v, want %v", got, tc.want)
			}
		})
	}
}
