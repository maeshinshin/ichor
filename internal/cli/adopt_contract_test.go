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
	"strings"
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// TestAdoptRefusesAContractBelowV114: adopt takes no version contract below v1.14, whether the
// operator names one or the probe picks it, and the refusal says why and what to do, writing nothing.
func TestAdoptRefusesAContractBelowV114(t *testing.T) {
	st := &adoptState{fresh: true, cluster: &ichorv1alpha1.TalosCluster{}}
	st.cluster.Name = "demo"
	st.cluster.Spec.TalosVersion = "v1.13.2"

	err := st.chooseContract(adoptDeps{ContractOverride: "v1.13"})
	if err == nil {
		t.Fatal("adopt accepted version contract v1.13; it must refuse a contract below v1.14")
	}

	for _, want := range []string{"v1.14", "Upgrade Talos", "Nothing was written"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not contain %q", err, want)
		}
	}

	if st.cluster.Spec.VersionContract != "" {
		t.Errorf("the refused contract was set on the cluster: %q", st.cluster.Spec.VersionContract)
	}
}
