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

package release

import (
	"fmt"
	"strings"
)

// CheckKubernetesStep is the compatibility rule for a Kubernetes version written to a cluster whose
// Talos is talos and whose installed Cilium is installedCilium ("" when it is not
// observed, in which case the Cilium half is not checked). When the installed Cilium cannot run the
// target, the error names the order: upgrade Cilium first if the chart embedded in this Ichor can.
func CheckKubernetesStep(talos, kubernetes, installedCilium string) error {
	if err := TalosKubernetesError(talos, kubernetes); err != nil {
		return err
	}

	if installedCilium == "" {
		return nil
	}

	err := CiliumKubernetesError(installedCilium, kubernetes)
	if err == nil {
		return nil
	}

	if CiliumKubernetesError(Pinned.Cilium, kubernetes) == nil {
		return fmt.Errorf("%w; upgrade Cilium to %s first, the chart this Ichor embeds, which runs Kubernetes %s",
			err, Pinned.Cilium, kubernetes)
	}

	return fmt.Errorf("%w; this Ichor ships Cilium %s, which does not run Kubernetes %s: a newer Ichor release is needed",
		err, Pinned.Cilium, kubernetes)
}

// CiliumRelease is the Cilium release an observed CNI runs, without its v, for the Cilium rule: "" unless the
// observed CNI is Cilium. A cluster whose CNI is something else has
// no Cilium release to match, so CheckKubernetesStep leaves the Cilium half unchecked.
func CiliumRelease(detected, detectedVersion string) string {
	if detected != "cilium" {
		return ""
	}

	return strings.TrimPrefix(detectedVersion, "v")
}
