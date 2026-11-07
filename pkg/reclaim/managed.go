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

package reclaim

import (
	"errors"

	"github.com/siderolabs/talos/pkg/machinery/client/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/siderolabs/talos/pkg/machinery/role"
)

// ClusterCredential returns an os:admin talosconfig for a node of the cluster whose secrets bundle is b: the
// credential Ichor already holds, so that an orphan of that cluster can be wiped without
// the person holding a talosconfig for it. The talosconfig has no endpoints; Dial supplies the one node.
func ClusterCredential(clusterName string, b *secrets.Bundle) (Credential, error) {
	if b == nil || b.Certs == nil || b.Certs.OS == nil {
		return Credential{}, errors.New("the cluster's secrets bundle has no Talos API CA")
	}

	cert, err := b.GenerateTalosAPIClientCertificate(role.MakeSet(role.Admin))
	if err != nil {
		return Credential{}, errors.New("generating the cluster's admin client certificate failed")
	}

	raw, err := config.NewConfig(clusterName, nil, b.Certs.OS.Crt, cert).Bytes()
	if err != nil {
		return Credential{}, errors.New("encoding the cluster's admin talosconfig failed")
	}

	return NewCredential(raw), nil
}
