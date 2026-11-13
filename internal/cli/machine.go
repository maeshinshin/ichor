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
	"fmt"
	"strings"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// roleControlPlane is the TalosMachine spec's spelling of the control plane role.
const roleControlPlane = "controlplane"

// parseRole maps the CLI's role spelling (the TalosMachine spec's own) to
// the wire enum. Anything else is refused here, not guessed.
func parseRole(s string) (pb.MachineRole, error) {
	switch strings.ToLower(s) {
	case roleControlPlane:
		return pb.MachineRole_MACHINE_ROLE_CONTROL_PLANE, nil
	case "worker":
		return pb.MachineRole_MACHINE_ROLE_WORKER, nil
	default:
		return pb.MachineRole_MACHINE_ROLE_UNSPECIFIED, fmt.Errorf("invalid --role %q: must be controlplane or worker", s)
	}
}
