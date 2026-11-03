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

// The base cache: one Talos ISO DataVolume per Talos version and schematic that outlives
// every VM, so a bring-up clones it through CDI (a storage-side copy on the same
// StorageClass) instead of downloading the ISO from the Image Factory again for each VM
// (2-5 minutes). It is the ISO and only the ISO: every VM still boots the ISO in
// maintenance mode and Ichor still performs the Talos install onto the blank system disk
// at approve time. A pre-installed disk image was rejected on purpose, because it would
// skip the install step the product must exercise.
package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/maeshinshin/ichor/test/internal/kube"
)

// baseCacheEnv turns the cache off with 0/false/off/no; anything else (including unset)
// leaves it on.
const baseCacheEnv = "ICHOR_E2E_BASE_CACHE"

// baseCacheEnabled reports whether VMs clone the cached ISO instead of importing their own.
func baseCacheEnabled() bool {
	switch strings.ToLower(os.Getenv(baseCacheEnv)) {
	case "0", "false", "off", "no":
		return false
	}

	return true
}

// baseCacheName is the cache DataVolume's name: per Talos version and the first 8 characters
// of the schematic, so a different image never reuses the wrong ISO. Dots become dashes.
func baseCacheName() string {
	return fmt.Sprintf("ichor-e2e-talos-%s-%s-base",
		strings.ReplaceAll(talosVersion, ".", "-"), talosFactorySchematic[:8])
}

// isoSource is the ISO DataVolume's `source:` body (already indented) and requested size.
type isoSource struct{ yaml, size string }

// httpISO is the old path: every VM downloads its own copy of the ISO.
func httpISO() isoSource {
	return isoSource{
		yaml: "          http:\n            url: https://factory.talos.dev/image/" +
			talosFactorySchematic + "/" + talosVersion + "/metal-amd64.iso",
		size: "1Gi",
	}
}

// clonedISO clones the cache PVC; size must be at least the cache PVC's capacity.
func clonedISO(cache, size string) isoSource {
	return isoSource{
		yaml: "          pvc:\n            namespace: " + namespace + "\n            name: " + cache,
		size: size,
	}
}

// renderVM fills vmManifestTmpl with the VM name, ISO source and harness label value.
func renderVM(name, harness string, iso isoSource) string {
	return fmt.Sprintf(vmManifestTmpl, name, iso.yaml, iso.size, harness)
}

// resolveISO returns the ISO source every VM of this run uses.
func resolveISO() (isoSource, error) {
	if !baseCacheEnabled() {
		return httpISO(), nil
	}

	size, err := ensureBaseCache()
	if err != nil {
		return isoSource{}, err
	}

	return clonedISO(baseCacheName(), size), nil
}

const baseCacheManifestTmpl = `apiVersion: cdi.kubevirt.io/v1beta1
kind: DataVolume
metadata:
  name: %s
  namespace: ichor-e2e
  labels:
    ichor.maesh.dev/e2e-harness: base-cache
  annotations:
    cdi.kubevirt.io/storage.deleteAfterCompletion: "false"
spec:
  source:
    http:
      url: https://factory.talos.dev/image/%s/%s/metal-amd64.iso
  storage:
    accessModes: [ReadWriteOnce]
    volumeMode: Filesystem
    resources:
      requests:
        storage: 1Gi
    storageClassName: ceph-block
`

// ensureBaseCache creates the cache DataVolume if it is missing (kubectl apply is idempotent:
// an existing, finished one is left alone), waits until the import has succeeded and returns
// the cache PVC's capacity. The first run pays the import; later runs find it ready. "down*"
// never deletes it; remove it by hand when the Talos version moves on.
func ensureBaseCache() (string, error) {
	name := baseCacheName()
	started := time.Now()

	if err := kube.Apply(fmt.Sprintf(baseCacheManifestTmpl, name, talosFactorySchematic, talosVersion)); err != nil {
		return "", fmt.Errorf("creating the base cache DataVolume %s: %w", name, err)
	}

	if _, err := kube.Poll("waiting for base cache "+name+" to be ready", 15*time.Minute, func() (string, bool) {
		phase, err := getJSONPath("dv", name, ".status.phase")
		return "", err == nil && phase == "Succeeded"
	}); err != nil {
		return "", err
	}

	size, err := getJSONPath("pvc", name, ".status.capacity.storage")
	if err != nil || size == "" {
		return "", fmt.Errorf("reading the capacity of base cache PVC %s: %v", name, err)
	}

	fmt.Printf("--- base cache %s ready (%s) after %s ---\n", name, size, time.Since(started).Round(time.Second))

	return size, nil
}
