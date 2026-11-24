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

import "strings"

// normalizeLiftedSettings rewrites the settings the v1alpha1 document spells that Talos 1.14 also spells in a
// document of their own (the moves liftV1Alpha1 in api/v1alpha1 makes) into that document, as
// normalizeRegistryMirrors does for registry mirrors. Talos folds both forms into one resource (for time:
// timeserver_config.go in v1.14.5 reads NetworkTimeSyncConfig(), whichever form set it), so a node an earlier
// build configured in the v1alpha1 form holds the same setting as the rendered document: no change, no reboot.
// A field the document already sets is left as it is.
func normalizeLiftedSettings(docs map[string]configDoc) {
	v1, ok := docs[""]
	if !ok {
		return
	}

	root := v1.data

	if v, ok := takeAt(root, "machine.time.servers"); ok {
		putDoc(docs, "TimeSyncConfig", "", "ntp.servers", v)
	}

	if b, ok := takeAt(root, "machine.time.disabled"); ok {
		if d, isBool := b.(bool); isBool {
			putDoc(docs, "TimeSyncConfig", "", "enabled", !d)
		}
	}

	if b, ok := takeAt(root, "cluster.coreDNS.disabled"); ok {
		if d, isBool := b.(bool); isBool {
			putDoc(docs, "KubeCoreDNSConfig", "", "enabled", !d)
		}
	}

	if v, ok := takeAt(root, "cluster.apiServer.certSANs"); ok {
		putDoc(docs, "KubeAPIServerConfig", "", "certExtraSANs", v)
	}

	if v, ok := takeAt(root, "machine.features.imageCache.localEnabled"); ok {
		putDoc(docs, "ImageCacheConfig", "", "local.enabled", v)
	}

	if v, ok := takeAt(root, "machine.network.nameservers"); ok {
		servers := []any{}

		list, _ := v.([]any)
		for _, s := range list {
			servers = append(servers, map[string]any{"address": s})
		}

		putDoc(docs, "ResolverConfig", "", "nameservers", servers)
	}

	if v, ok := takeAt(root, "machine.network.searchDomains"); ok {
		putDoc(docs, "ResolverConfig", "", "searchDomains.domains", v)
	}

	if v, ok := takeAt(root, "machine.network.disableSearchDomain"); ok {
		putDoc(docs, "ResolverConfig", "", "searchDomains.disableDefault", v)
	}

	eachListEntry(root, "machine.kernel.modules", func(e map[string]any) {
		name, _ := e["name"].(string)
		putDoc(docs, "KernelModuleConfig", name, "", nil)

		if p, ok := e["parameters"]; ok {
			putDoc(docs, "KernelModuleConfig", name, "parameters", p)
		}
	})

	eachListEntry(root, "machine.network.extraHostEntries", func(e map[string]any) {
		ip, _ := e["ip"].(string)
		putDoc(docs, "StaticHostConfig", ip, "hostnames", e["aliases"])
	})

	eachListEntry(root, "cluster.inlineManifests", func(e map[string]any) {
		name, _ := e["name"].(string)
		putDoc(docs, "KubeInlineManifestConfig", name, "manifest", e["contents"])
	})
}

// eachListEntry takes the list at path out of the tree and calls f for each entry that is an object.
func eachListEntry(root map[string]any, path string, f func(map[string]any)) {
	v, ok := takeAt(root, path)
	if !ok {
		return
	}

	list, _ := v.([]any)
	for _, e := range list {
		if m, ok := e.(map[string]any); ok {
			f(m)
		}
	}
}

// putDoc sets path (dotted; empty for none) of the document of the kind and name, making the document when it is
// absent. A field the document already sets is kept.
func putDoc(docs map[string]configDoc, kind, name, path string, v any) {
	key := kind + ":" + name

	d, ok := docs[key]
	if !ok {
		data := map[string]any{"apiVersion": "v1alpha1", "kind": kind}
		if name != "" {
			data[labelName] = name
		}

		d = configDoc{root: kind, data: data}
		docs[key] = d
	}

	if path == "" {
		return
	}

	keys := strings.Split(path, ".")
	t := d.data

	for _, k := range keys[:len(keys)-1] {
		next, ok := t[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			t[k] = next
		}

		t = next
	}

	if _, set := t[keys[len(keys)-1]]; !set {
		t[keys[len(keys)-1]] = v
	}
}

// takeAt removes the value at the dotted path of the tree, and the objects that leaves empty.
func takeAt(tree map[string]any, path string) (any, bool) {
	keys := strings.Split(path, ".")
	parents := []map[string]any{tree}

	for _, k := range keys[:len(keys)-1] {
		next, ok := parents[len(parents)-1][k].(map[string]any)
		if !ok {
			return nil, false
		}

		parents = append(parents, next)
	}

	v, ok := parents[len(parents)-1][keys[len(keys)-1]]
	if !ok {
		return nil, false
	}

	delete(parents[len(parents)-1], keys[len(keys)-1])

	for i := len(parents) - 1; i > 0; i-- {
		if len(parents[i]) > 0 {
			break
		}

		delete(parents[i-1], keys[i-1])
	}

	return v, true
}
