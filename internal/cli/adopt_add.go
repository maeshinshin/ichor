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
	"bytes"
	"cmp"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
	"github.com/maeshinshin/ichor/pkg/render"
)

// `ichor adopt --add-machine ADDRESS` registers a machine that already runs a
// configuration signed by an existing Ichor cluster's CAs but has no TalosMachine: it joined the
// Talos cluster by the user's own tooling, or its TalosMachine was deleted. It is the adoption with a
// smaller input: the listed addresses are read, planned in the three classes, written
// held, and released after the live zero-drift check. The cluster, its other machines and its CNI are
// not looked at again.

// resolveClusterBundle reads the secrets bundle of an existing TalosCluster from its Secret, for
// --add-machine without --secrets-bundle or --recover-secrets. While a CA rotation is under way the
// nodes trust CAs the bundle does not describe yet, so it refuses.
func resolveClusterBundle(ctx context.Context, k8s client.Reader, namespace, clusterName string) (*secrets.Bundle, error) {
	var cluster ichorv1alpha1.TalosCluster

	if err := k8s.Get(ctx, types.NamespacedName{Namespace: namespace, Name: clusterName}, &cluster); err != nil {
		return nil, fmt.Errorf("adopt: getting TalosCluster %q: %w; --add-machine adds to a cluster Ichor already manages", clusterName, err)
	}

	var secret corev1.Secret

	key := types.NamespacedName{Namespace: ichorControllerNamespace, Name: cluster.Spec.SecretsRef.Name}
	if err := k8s.Get(ctx, key, &secret); err != nil {
		return nil, fmt.Errorf("adopt: getting the secrets bundle Secret %s: %w; pass --secrets-bundle or --recover-secrets instead", key, err)
	}

	if _, rotating := secret.Data[secretsbundle.RotationKey]; rotating {
		return nil, errors.New("adopt: a CA rotation is under way, so the nodes do not yet trust only the CAs in the secrets bundle; add the machine after the rotation completes")
	}

	return secretsbundle.Load(ctx, k8s, key.Namespace, key.Name)
}

// nodeTrustsBundle reports whether a node's own machine configuration belongs to the Talos cluster the
// bundle describes: the same cluster ID, and the same Talos API CA when the configuration states one.
// It is a guard against a wrong address or a stale talosconfig, not an authentication: the dial is.
func nodeTrustsBundle(cfgBytes []byte, bundle *secrets.Bundle) (bool, error) {
	provider, err := configloader.NewFromBytes(cfgBytes)
	if err != nil {
		return false, errors.New("the machine configuration could not be parsed")
	}

	id := ""
	if d := provider.DiscoveryIdentityConfig(); d != nil {
		id = d.ClusterID()
	}

	var ca []byte

	if m := provider.Machine(); m != nil && m.Security() != nil && m.Security().IssuingCA() != nil {
		ca = m.Security().IssuingCA().Crt
	}

	if id == "" && len(ca) == 0 {
		return false, errors.New("the machine configuration names neither a cluster ID nor a Talos API CA, so its cluster cannot be told")
	}

	if id != "" && bundle.Cluster != nil && id != bundle.Cluster.ID {
		return false, nil
	}

	if len(ca) > 0 && !bytes.Equal(bytes.TrimSpace(ca), bytes.TrimSpace(bundle.Certs.OS.Crt)) {
		return false, nil
	}

	return true, nil
}

// clusterID is the cluster ID a node's configuration states, "" if none.
func clusterIDOf(cfgBytes []byte) string {
	provider, err := configloader.NewFromBytes(cfgBytes)
	if err != nil {
		return ""
	}

	if d := provider.DiscoveryIdentityConfig(); d != nil {
		return d.ClusterID()
	}

	return ""
}

// loadAdded is loadAdoption for --add-machine: st gets the existing TalosCluster and the nodes to add,
// and nothing else. Every refusal happens here, before anything is written.
func (st *adoptState) loadAdded(
	ctx context.Context, deps adoptDeps, cluster *ichorv1alpha1.TalosCluster, machines []*ichorv1alpha1.TalosMachine, namespace string,
) error {
	st.cluster, st.adding = cluster, true

	if deps.Bundle == nil {
		return errors.New("adopt: --add-machine needs the cluster's secrets bundle")
	}

	if deps.ReadIdentity == nil {
		return errors.New("adopt: no way to read the nodes' hardware identity was given")
	}

	added, err := st.registrations(ctx, deps, cluster, machines, namespace)
	if err != nil {
		return err
	}

	if len(added) == 0 {
		return nil // runAdopt says so and ends
	}

	// The Talos v1.12 gate runs before anything is read in full.
	if err := gateTalosVersions(ctx, added, deps.DialTalos); err != nil {
		return err
	}

	for _, m := range added {
		n := &adoptNode{M: m, added: true}

		if err := readNode(ctx, deps, n, true); err != nil {
			return err
		}

		trusts, err := nodeTrustsBundle(n.Current, deps.Bundle)
		if err != nil {
			return fmt.Errorf("adopt: machine at %q: %w", m.Spec.Endpoint, err)
		}

		if !trusts {
			return otherClusterRefusal(m.Spec.Endpoint, clusterIDOf(n.Current), cluster.Name)
		}

		st.nodes = append(st.nodes, n)
	}

	if err := checkControlPlaneCount(cluster, machines, st.nodes); err != nil {
		return err
	}

	if st.existing, err = clusterPatches(ctx, deps.K8s, namespace, cluster.Name); err != nil {
		return err
	}

	if st.sys, err = render.ResolveSystemInputs(ctx, deps.K8s, ichorControllerNamespace, st.cluster); err != nil {
		return fmt.Errorf("adopt: %w", err)
	}

	return nil
}

// otherClusterRefusal is the refusal for a machine that does not carry the cluster's CAs: it
// cannot join without a reset, and the reset is `ichor machine reclaim`. id is the machine's cluster
// ID when its configuration could be read, "" when the dial itself failed.
func otherClusterRefusal(addr, id, cluster string) error {
	what := "belongs to a different Talos cluster"
	if id != "" {
		what += fmt.Sprintf(" (id %q)", id)
	} else {
		what += " (its Talos API certificate is not signed by a CA your talosconfig trusts; if the machine should be in this cluster, check --talosconfig)"
	}

	return fmt.Errorf("adopt: the machine at %[1]q %[2]s than %[3]q: it does not carry this cluster's CAs, "+
		"so it cannot join without a reset. To reset it and add it, run `ichor machine reclaim %[1]s --talosconfig FILE --cluster %[3]s` "+
		"(this wipes its STATE and EPHEMERAL partitions)", addr, what, cluster)
}

// unknownAuthority reports whether a Talos dial failed because the node's certificate is not signed by
// a CA the talosconfig trusts. gRPC flattens the x509 error into text, so the type check is backed by
// the message.
func unknownAuthority(err error) bool {
	var ua x509.UnknownAuthorityError

	return errors.As(err, &ua) || strings.Contains(err.Error(), "certificate signed by unknown authority")
}

// registrations builds the in-memory TalosMachine of each address that is not one already. A machine
// whose UUID is a TalosMachine of this cluster is reported and left alone; one of another cluster, or
// one that is not approved yet, is an error.
func (st *adoptState) registrations(
	ctx context.Context, deps adoptDeps, cluster *ichorv1alpha1.TalosCluster, machines []*ichorv1alpha1.TalosMachine, namespace string,
) ([]*ichorv1alpha1.TalosMachine, error) {
	var (
		added []*ichorv1alpha1.TalosMachine
		seen  = map[string]string{}
	)

	for _, addr := range deps.AddMachines {
		id, err := deps.ReadIdentity(ctx, addr)
		if err != nil {
			if unknownAuthority(err) {
				return nil, otherClusterRefusal(addr, "", cluster.Name)
			}

			return nil, fmt.Errorf("adopt: reading the hardware identity of %q: %w", addr, err)
		}

		if id.UUID == "" {
			return nil, fmt.Errorf("adopt: the node at %q reported no SMBIOS UUID; adopt needs a strong hardware identity", addr)
		}

		if prev, dup := seen[id.UUID]; dup {
			return nil, fmt.Errorf("adopt: %q and %q are the same machine (UUID %s)", prev, addr, id.UUID)
		}

		seen[id.UUID] = addr

		if i := slices.IndexFunc(machines, func(m *ichorv1alpha1.TalosMachine) bool { return m.Spec.Identity.UUID == id.UUID }); i >= 0 {
			have := machines[i]
			if !have.Spec.IsApproved() {
				return nil, fmt.Errorf("adopt: the machine at %q is TalosMachine %q, which is not approved yet (%s); approve it, or delete it and run this again",
					addr, have.Name, have.Spec.ApprovalState)
			}

			deps.say(adoptInfo, "the machine at %s is already TalosMachine %s of cluster %s; not adding it", addr, have.Name, cluster.Name)

			continue
		}

		added = append(added, &ichorv1alpha1.TalosMachine{
			Name: id.UUID, Namespace: namespace,
			Spec: ichorv1alpha1.TalosMachineSpec{
				ClusterRef:    corev1.LocalObjectReference{Name: cluster.Name},
				Identity:      ichorv1alpha1.TalosMachineIdentity{UUID: id.UUID, Serial: id.Serial, MACAddresses: id.MACAddresses},
				Endpoint:      addr,
				ApprovalState: ichorv1alpha1.MachineApprovalStateApproved,
			},
		})
	}

	return added, nil
}

// checkControlPlaneCount refuses a control plane the cluster is not configured for: the webhook
// would refuse the approval, and the refusal here is before anything is written and says what to do.
func checkControlPlaneCount(cluster *ichorv1alpha1.TalosCluster, existing []*ichorv1alpha1.TalosMachine, added []*adoptNode) error {
	have := 0

	for _, m := range existing {
		if m.Spec.IsApproved() && m.Spec.Role == roleControlPlane {
			have++
		}
	}

	for _, n := range added {
		if n.controlPlane() {
			have++
		}
	}

	if int32(have) > cluster.Spec.ControlPlane.Count { //nolint:gosec // a handful of machines
		return fmt.Errorf("adopt: the cluster would have %d control planes but spec.controlPlane.count is %d; raise spec.controlPlane.count first (1, 3 or 5)",
			have, cluster.Spec.ControlPlane.Count)
	}

	return nil
}

// inspectAdded is the inspection of --add-machine: the CNI settings come from the existing
// TalosCluster, and a managed cluster still needs KubePrism on the new nodes.
func (st *adoptState) inspectAdded(deps adoptDeps) error {
	st.unmanaged = st.cluster.Spec.CNI.Preset == ichorv1alpha1.TalosClusterCNIPresetUnmanaged
	st.kubeProxy = cmp.Or(st.cluster.Spec.CNI.KubeProxy, kubeProxyEnabled)

	if st.unmanaged {
		return nil
	}

	return st.checkKubePrism(deps)
}
