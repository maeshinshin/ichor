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

package stack

import (
	"fmt"
	"slices"

	"github.com/maeshinshin/ichor/pkg/stack/policies"
)

// Policies returns the CiliumNetworkPolicies of a component's namespace
// ("kokumi" or "argocd") as a YAML stream. Render with
// OmitPolicies for the first apply, then apply these once the namespace and
// its workloads exist; the published bundle already contains them, so Argo CD
// keeps them.
func Policies(component string) ([]byte, error) {
	switch component {
	case "kokumi":
		return slices.Clone(policies.Kokumi), nil
	case "argocd":
		return slices.Clone(policies.Argocd), nil
	}

	return nil, fmt.Errorf("stack: no policies for component %q", component)
}
