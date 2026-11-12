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

package v1alpha1

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// credentialValue is one credential in a patch document's v1alpha1 Config: the registry client identity (a PEM
// private key) or an extraManifestHeaders entry (in practice an Authorization header). field is its path in the
// document, value what it holds, at its path in the spec.
type credentialValue struct {
	field, value, at string
}

// credentialValues lists the credentials docs carry in their `config` field.
func credentialValues(docs []ichorv1alpha1.TalosConfigPatchDocument, specPath string) []credentialValue {
	var out []credentialValue

	for i, d := range docs {
		c := d.Config
		if c == nil {
			continue
		}

		add := func(field, value string) {
			out = append(out, credentialValue{field, value, fmt.Sprintf("%s[%d].config.%s", specPath, i, field)})
		}

		if c.MachineConfig != nil && c.MachineConfig.MachineRegistries != nil {
			regs := c.MachineConfig.MachineRegistries.RegistryConfig
			for _, host := range slices.Sorted(maps.Keys(regs)) {
				if rc := regs[host]; rc != nil && rc.RegistryTLS != nil && rc.RegistryTLS.TLSClientIdentity != nil {
					id, _ := json.Marshal(rc.RegistryTLS.TLSClientIdentity)
					add("machine.registries.config."+host+".tls.clientIdentity", string(id))
				}
			}
		}

		if c.ClusterConfig != nil {
			for _, name := range slices.Sorted(maps.Keys(c.ClusterConfig.ExtraManifestHeaders)) {
				add("cluster.extraManifestHeaders", name+"="+c.ClusterConfig.ExtraManifestHeaders[name])
			}
		}
	}

	return out
}

// refuseCredentials refuses a credential that the write introduces or changes: one in newDocs that oldDocs
// (nil on create) did not already hold, wherever in the list. A value that is already stored stays, so
// an object written before this check can be edited for something else, and the value removed. Admission
// webhooks get no ratcheting from the apiserver (that is for a CRD's own validation), so this is the rule.
// The message names the field, never the value.
func refuseCredentials(oldDocs, newDocs []ichorv1alpha1.TalosConfigPatchDocument, specPath string) error {
	had := map[[2]string]bool{}
	for _, v := range credentialValues(oldDocs, specPath) {
		had[[2]string{v.field, v.value}] = true
	}

	var at []string

	for _, v := range credentialValues(newDocs, specPath) {
		if !had[[2]string{v.field, v.value}] && !slices.Contains(at, v.at) {
			at = append(at, v.at)
		}
	}

	if len(at) == 0 {
		return nil
	}

	return fmt.Errorf("%s: a credential does not belong in a custom resource, which is stored unencrypted in etcd and "+
		"readable by anyone who can read it; credentials belong in the secrets bundle", strings.Join(at, ", "))
}
