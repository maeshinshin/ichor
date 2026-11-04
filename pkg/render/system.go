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

package render

import (
	"context"
	"fmt"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/config"
	"github.com/siderolabs/talos/pkg/machinery/config/types/k8s"
	mv1alpha1 "github.com/siderolabs/talos/pkg/machinery/config/types/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/storage"
)

// SystemInputs is everything Render needs that Ichor itself contributes below the
// TalosConfigPatch layers (the "system" layer) and that has to be read from
// the cluster: resolved registry credentials and the TalosStorage objects whose layer-1
// preparation may apply. It is one argument so that a new system-layer input extends this
// struct instead of adding a parameter every caller must remember.
//
// A nil *SystemInputs means "none": no registry credentials and no storage. That is right for a
// cluster with neither, and for tests; production callers always use ResolveSystemInputs.
type SystemInputs struct {
	// RegistryAuth is ResolveRegistryAuth's result, keyed by TalosClusterRegistry.Host.
	RegistryAuth map[string]RegistryAuth

	// Storage is every TalosStorage in the cluster's namespace (any cluster; storage.Select
	// filters). Order does not matter.
	Storage []ichorv1alpha1.TalosStorage

	// CARotation is a CA rotation's current stage and new CAs. Only the rotation
	// executor sets it; nil is the normal case.
	CARotation *CARotation

	// KeyRotation is a service-account and secretbox key rotation's current stage and new keys.
	// Only the rotation executor sets it; nil is the normal case.
	KeyRotation *KeyRotation

	// Staged pins the controller-manager, scheduler, kubelet and kube-proxy below
	// spec.kubernetesVersion while a Kubernetes upgrade moves the control planes first. Only the upgrade executor sets
	// it; nil is the normal case.
	Staged *StagedKubernetes

	// OIDCTrust is the public trust material of Ichor's own issuer, read from the ConfigMap
	// ichor-oidc-trust when spec.authentication.ichor is enabled. Nil when it is not
	// enabled, or when the ConfigMap does not exist yet (the render then says so).
	OIDCTrust *OIDCTrust
}

// ResolveSystemInputs reads what Render's system layer needs: registry credentials from
// secretNamespace (ResolveRegistryAuth) and the TalosStorages in the cluster's own namespace.
func ResolveSystemInputs(
	ctx context.Context, c client.Client, secretNamespace string, cluster *ichorv1alpha1.TalosCluster,
) (*SystemInputs, error) {
	auth, err := ResolveRegistryAuth(ctx, c, secretNamespace, cluster)
	if err != nil {
		return nil, err
	}

	var storages ichorv1alpha1.TalosStorageList
	// A kind the API server does not serve (Ichor is not installed yet, as in a first adopt) has no objects.
	if err := c.List(ctx, &storages, client.InNamespace(cluster.Namespace)); err != nil && !meta.IsNoMatchError(err) {
		return nil, fmt.Errorf("listing TalosStorages: %w", err)
	}

	trust, err := resolveOIDCTrust(ctx, c, secretNamespace, cluster)
	if err != nil {
		return nil, err
	}

	return &SystemInputs{RegistryAuth: auth, Storage: storages.Items, OIDCTrust: trust}, nil
}

// StorageFor returns the TalosStorage governing m (storage.Select over m's declared
// spec.nodeLabels, the labels Talos stamps onto its Node), or nil. It is the single entry point
// for "does layer 1 apply to this machine": Render, the machine controller's input fingerprint
// and the TalosStorage status controller all call it.
func StorageFor(
	cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, storages []ichorv1alpha1.TalosStorage,
) (*ichorv1alpha1.TalosStorage, error) {
	return storage.Select(cluster, storages, m.Spec.NodeLabels)
}

// injectStorage merges the governing TalosStorage's preset documents (volume, kubelet mount,
// Node label and annotation) into cfg as part of the system layer. Applied before any
// TalosConfigPatch, so a patch that touches the same fields wins.
func injectStorage(
	cfg coreconfig.Provider, contract *coreconfig.VersionContract,
	cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, sys *SystemInputs,
) (coreconfig.Provider, error) {
	chosen, err := StorageFor(cluster, m, sys.Storage)
	if err != nil || chosen == nil {
		return cfg, err
	}

	preset, err := storage.NewPreset(chosen.Spec.DiskPreset)
	if err != nil {
		return nil, fmt.Errorf("render: TalosStorage %q: %w", chosen.Name, err)
	}

	patches, err := preset.TalosPatches(chosen.Spec.DiskSelector)
	if err != nil {
		return nil, fmt.Errorf("render: TalosStorage %q: %w", chosen.Name, err)
	}

	docs := make([]config.Document, 0, len(patches))

	for _, d := range patches {
		doc, err := DocumentFromPatch(d)
		if err != nil {
			return nil, fmt.Errorf("render: TalosStorage %q: %w", chosen.Name, err)
		}

		docs = append(docs, doc)
	}

	// Node registration and kubelet mounts are not TalosConfigPatch documents: which Talos
	// document carries them depends on the version contract.
	docs = append(docs, nodeRegistrationDocs(contract, preset)...)

	return mergeDocument(cfg, fmt.Sprintf("TalosStorage %q", chosen.Name), docs...)
}

// nodeRegistrationDocs returns the Node label and annotation that register the disk with the
// storage system, plus the kubelet mounts, in whichever form contract allows. From Talos 1.14
// (MultidocKubernetesConfigSupported) labels and annotations live in the standalone KubeNodeConfig
// document, and a legacy machine.nodeLabels / machine.kubelet next to the base config's
// multi-document form fails validation. The standalone KubeletConfig document has no
// extraMounts field at all (Talos bind-mounts /var/mnt into the kubelet itself), so under that
// contract the preset's KubeletMounts are not rendered.
func nodeRegistrationDocs(contract *coreconfig.VersionContract, preset storage.Preset) []config.Document {
	if contract.MultidocKubernetesConfigSupported() {
		doc := k8s.NewKubeNodeConfigV1Alpha1()
		doc.LabelsConfig = preset.NodeLabels()
		doc.AnnotationsConfig = preset.NodeAnnotations()

		return []config.Document{doc}
	}

	mounts := make([]mv1alpha1.ExtraMount, 0, len(preset.KubeletMounts()))

	for _, km := range preset.KubeletMounts() {
		mounts = append(mounts, mv1alpha1.ExtraMount{
			Destination: km.Destination, Source: km.Source, Type: km.Type, Options: km.Options,
		})
	}

	//nolint:staticcheck // the legacy blocks are the only form a pre-1.14 contract accepts
	return []config.Document{&mv1alpha1.Config{
		ConfigVersion: mv1alpha1.Version,
		MachineConfig: &mv1alpha1.MachineConfig{
			MachineNodeLabels:      preset.NodeLabels(),
			MachineNodeAnnotations: preset.NodeAnnotations(),
			MachineKubelet:         &mv1alpha1.KubeletConfig{KubeletExtraMounts: mounts},
		},
	}}
}
