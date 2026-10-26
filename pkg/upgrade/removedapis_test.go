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

package upgrade

import (
	"slices"
	"testing"
)

const metricsSample = `# TYPE apiserver_requested_deprecated_apis gauge
apiserver_requested_deprecated_apis{group="flowcontrol.apiserver.k8s.io",removed_release="1.32",resource="flowschemas",subresource="",version="v1beta3"} 1
apiserver_requested_deprecated_apis{group="",removed_release="1.40",resource="things",subresource="",version="v1alpha1"} 1
apiserver_requested_deprecated_apis{group="policy",removed_release="1.25",resource="podsecuritypolicies",subresource="",version="v1beta1"} 1
apiserver_requested_deprecated_apis{group="policy",removed_release="1.25",resource="podsecuritypolicies",subresource="status",version="v1beta1"} 1
apiserver_request_total{code="200"} 5
`

func TestRemovedAPIsInUse(t *testing.T) {
	got, err := RemovedAPIsInUse(metricsSample, "1.36")
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"flowcontrol.apiserver.k8s.io/v1beta3 flowschemas (removed in 1.32)",
		"policy/v1beta1 podsecuritypolicies (removed in 1.25)",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("RemovedAPIsInUse = %v, want %v (1.40 is not removed by 1.36; duplicates collapse)", got, want)
	}

	none, err := RemovedAPIsInUse(metricsSample, "1.20")
	if err != nil || len(none) != 0 {
		t.Fatalf("RemovedAPIsInUse(1.20) = %v, %v; want nothing", none, err)
	}

	if _, err := RemovedAPIsInUse(metricsSample, "v1.36"); err == nil {
		t.Fatal("a target that is not X.Y must be rejected")
	}
}

func TestKubernetesMinorOf(t *testing.T) {
	if got, err := KubernetesMinorOf("v1.36.2"); err != nil || got != "1.36" {
		t.Fatalf("KubernetesMinorOf = %q, %v", got, err)
	}

	if _, err := KubernetesMinorOf("1.36.2"); err == nil {
		t.Fatal("a version without the leading v must be rejected")
	}
}
