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
	"slices"
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/ichorconfig"
)

func TestBuildDiskRoleVolumes_Empty(t *testing.T) {
	if v := buildDiskRoleVolumes(diskRoleOptions{}); v != nil {
		t.Fatalf("expected no volumes when no selector is given, got %+v", v)
	}
}

func TestBuildDiskRoleVolumes_AllThreeRoles(t *testing.T) {
	v := buildDiskRoleVolumes(diskRoleOptions{
		EphemeralDiskSelector:  testEphemeralDiskSelector,
		StorageDiskSelector:    testStorageDiskSelector,
		UserVolumeDiskSelector: `disk.wwid == "data"`,
	})

	if v == nil || len(v.System) != 1 || len(v.Raw) != 1 || len(v.User) != 1 {
		t.Fatalf("expected one system, raw and user volume, got %+v", v)
	}

	eph := v.System["EPHEMERAL"]
	if eph == nil || eph.Provisioning.DiskSelectorSpec.Match != testEphemeralDiskSelector {
		t.Fatalf("system volumes = %+v, want EPHEMERAL on the ephemeral selector", v.System)
	}

	if v.Raw["storage"] == nil || v.User["data"] == nil {
		t.Fatalf("raw = %+v, user = %+v, want the default names storage and data", v.Raw, v.User)
	}

	// Each lands as the Talos document of its kind.
	cfg := &ichorconfig.Config{Machine: &ichorconfig.Machine{Volumes: v}}

	docs, err := ichorv1alpha1.ConvertConfig(cfg, true)
	if err != nil {
		t.Fatal(err)
	}

	var kinds []string
	for _, d := range docs {
		kinds = append(kinds, d.Kind)
	}

	if want := []string{"RawVolumeConfig", "UserVolumeConfig", "VolumeConfig"}; !slices.Equal(kinds, want) {
		t.Fatalf("documents = %v, want %v", kinds, want)
	}
}

func TestBuildDiskRoleVolumes_CustomNames(t *testing.T) {
	v := buildDiskRoleVolumes(diskRoleOptions{
		StorageDiskSelector: testStorageDiskSelector,
		StorageVolumeName:   "longhorn",
	})

	if v == nil || len(v.Raw) != 1 || v.Raw["longhorn"] == nil {
		t.Fatalf("expected one raw volume named longhorn, got %+v", v)
	}
}
