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
