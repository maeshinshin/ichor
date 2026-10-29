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

package cilium

import "fmt"

// FindInlineManifest returns the name of the Talos cluster.inlineManifests entry whose contents
// hold the cilium DaemonSet in kube-system, found by content and not by name,
// or "" when none does. entries maps the entry name to its contents. Two matching entries are an
// error: Ichor cannot say which one to drop.
func FindInlineManifest(entries map[string]string) (string, error) {
	found := ""

	for name, contents := range entries {
		objs, err := splitObjects([]byte(contents))
		if err != nil {
			continue // an entry that is not Kubernetes YAML is not Cilium
		}

		for _, o := range objs {
			// The namespace must be explicit: refOf's kube-system default is not what apply does.
			meta, _ := o["metadata"].(map[string]any)
			if str(o["kind"]) == "DaemonSet" && str(meta["name"]) == ReleaseName && str(meta["namespace"]) == ReleaseNamespace {
				if found != "" {
					return "", fmt.Errorf("cilium: cluster.inlineManifests entries %q and %q both hold the cilium DaemonSet", found, name)
				}

				found = name
			}
		}
	}

	return found, nil
}
