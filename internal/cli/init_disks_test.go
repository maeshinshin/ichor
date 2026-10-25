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

import "testing"

func TestBuildDiskRoleDocuments_Empty(t *testing.T) {
	if docs := buildDiskRoleDocuments(diskRoleOptions{}); len(docs) != 0 {
		t.Fatalf("expected no documents when no selector is given, got %+v", docs)
	}
}

func TestBuildDiskRoleDocuments_AllThreeRoles(t *testing.T) {
	docs := buildDiskRoleDocuments(diskRoleOptions{
		EphemeralDiskSelector:  testEphemeralDiskSelector,
		StorageDiskSelector:    testStorageDiskSelector,
		UserVolumeDiskSelector: `disk.wwid == "data"`,
	})

	if len(docs) != 3 {
		t.Fatalf("expected 3 documents, got %d: %+v", len(docs), docs)
	}

	eph := docs[0]
	if eph.Kind != "VolumeConfig" || eph.VolumeConfig == nil || eph.VolumeConfig.MetaName != "EPHEMERAL" {
		t.Fatalf("ephemeral document = %+v, want kind VolumeConfig named EPHEMERAL", eph)
	}

	if got := eph.VolumeConfig.ProvisioningSpec.DiskSelectorSpec.Match; got != testEphemeralDiskSelector {
		t.Fatalf("ephemeral selector = %q", got)
	}

	storage := docs[1]
	if storage.Kind != "RawVolumeConfig" || storage.RawVolumeConfig == nil || storage.RawVolumeConfig.MetaName != "storage" {
		t.Fatalf("storage document = %+v, want kind RawVolumeConfig named storage (default)", storage)
	}

	userVol := docs[2]
	if userVol.Kind != "UserVolumeConfig" || userVol.UserVolumeConfig == nil || userVol.UserVolumeConfig.MetaName != "data" {
		t.Fatalf("user volume document = %+v, want kind UserVolumeConfig named data (default)", userVol)
	}
}

func TestBuildDiskRoleDocuments_CustomNames(t *testing.T) {
	docs := buildDiskRoleDocuments(diskRoleOptions{
		StorageDiskSelector: testStorageDiskSelector,
		StorageVolumeName:   "longhorn",
	})

	if len(docs) != 1 || docs[0].RawVolumeConfig.MetaName != "longhorn" {
		t.Fatalf("expected one RawVolumeConfig named longhorn, got %+v", docs)
	}
}
