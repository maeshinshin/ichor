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

package storage

import (
	"encoding/json"
	"testing"
)

func TestNewPreset(t *testing.T) {
	p, err := NewPreset(LonghornV1)
	if err != nil {
		t.Fatalf("NewPreset(%q) error: %v", LonghornV1, err)
	}

	if _, ok := p.(longhornV1); !ok {
		t.Fatalf("NewPreset(%q) = %T, want longhornV1", LonghornV1, p)
	}

	if _, err := NewPreset("rook-v1"); err == nil {
		t.Fatal(`NewPreset("rook-v1") = nil error, want an error (not an allowed preset)`)
	}
}

func TestLonghornV1_CheckExtensions(t *testing.T) {
	p := longhornV1{}

	const (
		iscsi     = "siderolabs/iscsi-tools"
		utilLinux = "siderolabs/util-linux-tools"
	)

	cases := []struct {
		name      string
		installed []string
		wantErr   bool
	}{
		{name: "both required extensions present", installed: []string{iscsi, utilLinux}, wantErr: false},
		{
			name:      "extra extensions alongside the required ones",
			installed: []string{iscsi, utilLinux, "siderolabs/qemu-guest-agent"},
			wantErr:   false,
		},
		{name: "missing util-linux-tools", installed: []string{iscsi}, wantErr: true},
		{name: "missing both", installed: nil, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := p.CheckExtensions(tc.installed)
			if (err != nil) != tc.wantErr {
				t.Fatalf("CheckExtensions(%v) = %v, want error = %v", tc.installed, err, tc.wantErr)
			}
		})
	}
}

func TestLonghornV1_TalosPatches(t *testing.T) {
	p := longhornV1{}

	if _, err := p.TalosPatches(""); err == nil {
		t.Fatal(`TalosPatches("") = nil error, want an error (diskSelector is required)`)
	}

	const selector = `disk.transport == "nvme" && !system_disk`

	docs, err := p.TalosPatches(selector)
	if err != nil {
		t.Fatalf("TalosPatches(%q) error: %v", selector, err)
	}

	if len(docs) != 1 {
		t.Fatalf("TalosPatches(%q) returned %d documents, want 1", selector, len(docs))
	}

	userVolume := docs[0]
	if userVolume.Kind != "UserVolumeConfig" {
		t.Fatalf("docs[0].Kind = %q, want %q", userVolume.Kind, "UserVolumeConfig")
	}

	if userVolume.UserVolumeConfig == nil {
		t.Fatal("docs[0].UserVolumeConfig is nil")
	}

	if userVolume.UserVolumeConfig.MetaName != longhornVolumeName {
		t.Errorf("UserVolumeConfig.MetaName = %q, want %q", userVolume.UserVolumeConfig.MetaName, longhornVolumeName)
	}

	provisioning := userVolume.UserVolumeConfig.ProvisioningSpec
	if provisioning == nil || provisioning.DiskSelectorSpec == nil {
		t.Fatal("UserVolumeConfig.ProvisioningSpec.DiskSelectorSpec is nil")
	}

	if got := userVolume.UserVolumeConfig.ProvisioningSpec.DiskSelectorSpec.Match; got != selector {
		t.Errorf("UserVolumeConfig disk selector = %q, want %q", got, selector)
	}

	if provisioning.ProvisioningMaxSize != "100%" {
		t.Errorf("maxSize = %q, want 100%% (Talos requires a size bound)", provisioning.ProvisioningMaxSize)
	}
}

func TestLonghornV1_KubeletMounts(t *testing.T) {
	mounts := longhornV1{}.KubeletMounts()
	if len(mounts) != 1 || mounts[0].Destination != longhornMountPath || mounts[0].Source != longhornMountPath {
		t.Fatalf("KubeletMounts() = %+v, want one bind mount of %q", mounts, longhornMountPath)
	}
}

func TestLonghornV1_NodeLabelsAndAnnotations(t *testing.T) {
	p := longhornV1{}

	labels := p.NodeLabels()
	if got := labels["node.longhorn.io/create-default-disk"]; got != "config" {
		t.Errorf(`NodeLabels()["node.longhorn.io/create-default-disk"] = %q, want "config"`, got)
	}

	annotations := p.NodeAnnotations()

	raw, ok := annotations["node.longhorn.io/default-disks-config"]
	if !ok {
		t.Fatal(`NodeAnnotations() is missing "node.longhorn.io/default-disks-config"`)
	}

	var disks []struct {
		Path            string `json:"path"`
		AllowScheduling bool   `json:"allowScheduling"`
	}
	if err := json.Unmarshal([]byte(raw), &disks); err != nil {
		t.Fatalf("default-disks-config annotation is not valid JSON: %v (%s)", err, raw)
	}

	if len(disks) != 1 || disks[0].Path != longhornMountPath || !disks[0].AllowScheduling {
		t.Errorf("default-disks-config = %+v, want one entry {%q, allowScheduling true}", disks, longhornMountPath)
	}
}

func TestLonghornV1_CheckConsistency(t *testing.T) {
	p := longhornV1{}

	if err := p.CheckConsistency(LiveConfig{CreateDefaultDiskOnLabeledNodes: true}); err != nil {
		t.Errorf("CheckConsistency(enabled) = %v, want nil", err)
	}

	if err := p.CheckConsistency(LiveConfig{CreateDefaultDiskOnLabeledNodes: false}); err == nil {
		t.Error("CheckConsistency(disabled) = nil, want an error")
	}
}
