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
	"context"
	"slices"
	"strings"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"

	"github.com/maeshinshin/ichor/pkg/fieldmeta"
)

// isHostnamePath reports whether a changed path belongs to spec.hostname's rendered form:
// the HostnameConfig document (v1.14 contract) or machine.network.hostname (before it).
func isHostnamePath(p string) bool {
	return strings.HasPrefix(p, "HostnameConfig.") || p == "machine.network.hostname"
}

// assessChange is fieldmeta.Assess made value-aware for spec.hostname (ADR 0009 addendum).
// fieldmeta sees only paths, and a hostname path is reboot-required because a rename leaves the
// Kubernetes node under its old name. When the rendered hostname is the one the node already
// carries (its Nodename, which Talos derives from the hostname), nothing changes for the
// kubelet and the paths are dropped: this is what keeps an operator upgrade that starts
// rendering spec.hostname from demanding a reboot of every node. If the node's name cannot
// be read the paths stay, i.e. the safe answer.
func assessChange(
	ctx context.Context, talos TalosClient, hostname string, paths []string, contract *coreconfig.VersionContract,
) fieldmeta.Impact {
	if hostname != "" && slices.ContainsFunc(paths, isHostnamePath) {
		if name, err := talos.NodeName(ctx); err == nil && strings.EqualFold(name, hostname) {
			paths = slices.DeleteFunc(slices.Clone(paths), isHostnamePath)
		}
	}

	return fieldmeta.Assess(paths, contract)
}

// renameNote explains a reboot-required hostname path: the node is renamed, and the Kubernetes
// node keeps its old name until the reboot (empty when no hostname path is among paths).
func renameNote(paths []string) string {
	if !slices.ContainsFunc(paths, isHostnamePath) {
		return ""
	}

	return "; a hostname change renames the node, and the Kubernetes node keeps its old name until it reboots"
}
