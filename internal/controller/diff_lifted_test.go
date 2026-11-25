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
	"reflect"
	"testing"

	"github.com/maeshinshin/ichor/pkg/fieldmeta"
)

// A setting that only moved from the v1alpha1 document to the document Talos 1.14 spells it in changes nothing on the
// node, so it must not need a reboot (or any apply).
func TestChangedPathsLiftedSettingIsNoChange(t *testing.T) {
	cases := map[string]struct{ old, new string }{
		"time servers": {
			"version: v1alpha1\nmachine:\n  type: worker\n  time:\n    servers:\n      - time.cloudflare.com\n",
			"version: v1alpha1\nmachine:\n  type: worker\n---\napiVersion: v1alpha1\nkind: TimeSyncConfig\nntp:\n  servers:\n    - time.cloudflare.com\n",
		},
		"nameservers and search domains": {
			"version: v1alpha1\nmachine:\n  type: worker\n  network:\n    nameservers: [1.1.1.1]\n    searchDomains: [example.org]\n",
			"version: v1alpha1\nmachine:\n  type: worker\n---\napiVersion: v1alpha1\nkind: ResolverConfig\nnameservers:\n  - address: 1.1.1.1\nsearchDomains:\n  domains: [example.org]\n",
		},
		"kernel module": {
			"version: v1alpha1\nmachine:\n  type: worker\n  kernel:\n    modules:\n      - name: br_netfilter\n        parameters: [a=1]\n",
			"version: v1alpha1\nmachine:\n  type: worker\n---\napiVersion: v1alpha1\nkind: KernelModuleConfig\nname: br_netfilter\nparameters: [a=1]\n",
		},
		"host entries": {
			"version: v1alpha1\nmachine:\n  type: worker\n  network:\n    extraHostEntries:\n      - ip: 10.0.0.1\n        aliases: [a.example]\n",
			"version: v1alpha1\nmachine:\n  type: worker\n---\napiVersion: v1alpha1\nkind: StaticHostConfig\nname: 10.0.0.1\nhostnames: [a.example]\n",
		},
		"inline manifest": {
			"version: v1alpha1\nmachine:\n  type: controlplane\ncluster:\n  inlineManifests:\n    - name: m\n      contents: x\n",
			"version: v1alpha1\nmachine:\n  type: controlplane\n---\napiVersion: v1alpha1\nkind: KubeInlineManifestConfig\nname: m\nmanifest: x\n",
		},
		"api server SANs, coredns, image cache": {
			"version: v1alpha1\nmachine:\n  type: controlplane\n  features:\n    imageCache:\n      localEnabled: true\ncluster:\n  apiServer:\n    certSANs: [a.example]\n  coreDNS:\n    disabled: true\n",
			"version: v1alpha1\nmachine:\n  type: controlplane\n---\napiVersion: v1alpha1\nkind: KubeAPIServerConfig\ncertExtraSANs: [a.example]\n---\napiVersion: v1alpha1\nkind: KubeCoreDNSConfig\nenabled: false\n---\napiVersion: v1alpha1\nkind: ImageCacheConfig\nlocal:\n  enabled: true\n",
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			paths, err := changedPaths([]byte(c.old), []byte(c.new), "other")
			if err != nil {
				t.Fatal(err)
			}

			if len(paths) != 0 {
				t.Fatalf("a pure move changed %v", paths)
			}
		})
	}
}

// A new value in the lifted document is a change, and time sync applies live.
func TestChangedPathsLiftedTimeServersValueChange(t *testing.T) {
	old := "version: v1alpha1\nmachine:\n  type: worker\n  time:\n    servers: [a]\n"
	rendered := "version: v1alpha1\nmachine:\n  type: worker\n---\napiVersion: v1alpha1\nkind: TimeSyncConfig\nntp:\n  servers: [b]\n"

	paths, err := changedPaths([]byte(old), []byte(rendered), "other")
	if err != nil || len(paths) == 0 {
		t.Fatalf("paths %v err %v", paths, err)
	}

	if fieldmeta.Assess(paths, nil).RebootRequired {
		t.Fatalf("time servers need a reboot: %v", paths)
	}
}

// A lift that fails (the document already sets what the v1alpha1 document spells) leaves every setting where it
// was, including those the lift had already moved before it failed.
func TestNormalizeLiftedSettingsKeepsTheSettingsOnAnError(t *testing.T) {
	build := func() map[string]configDoc {
		return map[string]configDoc{
			"": {data: map[string]any{
				"cluster": map[string]any{"apiServer": map[string]any{"certSANs": []any{"san.example"}}},
				"machine": map[string]any{"time": map[string]any{"servers": []any{"ntp.example"}}},
			}},
			"TimeSyncConfig:": {root: "TimeSyncConfig", data: map[string]any{
				"ntp": map[string]any{"servers": []any{"other.example"}},
			}},
		}
	}

	docs := build()
	normalizeLiftedSettings(docs)

	if want := build(); !reflect.DeepEqual(docs, want) {
		t.Errorf("the settings moved although the lift failed:\n got %v\nwant %v", docs, want)
	}
}
