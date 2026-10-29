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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/controller"
)

// annotationTrue is the value of the adoption and hold-apply annotations.
const annotationTrue = "true"

// kubeProxyReplaced is spec.cni.kubeProxy for a cluster whose CNI replaces kube-proxy.
const kubeProxyReplaced = "replaced"

// cniUnmanaged is spec.cni.preset for a cluster whose CNI Ichor leaves alone; kubeProxyEnabled is
// spec.cni.kubeProxy for a cluster where Talos runs kube-proxy.
const (
	cniUnmanaged     = "unmanaged"
	kubeProxyEnabled = "enabled"
)

// adoptWriteInput is everything the write phase of `ichor adopt` needs,
// as plain values: the facts the inspection phase established, not the code that found them. A
// nil or empty part is skipped, so a caller can write only what it has (today's adopt writes
// only Patches).
//
// Nothing here holds secret material except Bundle, which only ever reaches the bundle Secret.
type adoptWriteInput struct {
	Namespace string

	// Bundle, with Cluster set, is stored as the bundle Secret.
	Bundle *secrets.Bundle

	// Cluster, when set, is the TalosCluster to create.
	Cluster *adoptWriteCluster

	// Machines are the TalosMachines to create, all held (hold-apply annotation).
	Machines []adoptWriteMachine

	// Patches are the TalosConfigPatches to create (class A captures).
	Patches []ichorv1alpha1.TalosConfigPatch

	// Ichor, when set, installs Ichor if it is not yet running.
	Ichor *adoptWriteIchor

	// ReadIdentity reads a node's hardware identity over the AUTHENTICATED Talos API. Required
	// when Machines is non-empty.
	ReadIdentity func(ctx context.Context, address string) (controller.HardwareIdentity, error)

	// WebhookWait and Interval bound the wait for Ichor's admission webhooks; zero means the
	// init defaults.
	WebhookWait, Interval time.Duration

	Stderr io.Writer
}

// adoptWriteCluster is the TalosCluster's spec as adopt settled it.
type adoptWriteCluster struct {
	Name string

	Endpoint          ichorv1alpha1.TalosClusterEndpoint
	TalosVersion      string
	KubernetesVersion string
	VersionContract   string
	PodSubnets        []string
	ServiceSubnets    []string

	ControlPlaneCount int32
	Schedulable       string

	// CNIPreset is cilium or unmanaged; TalosCNI (default|none) applies to unmanaged only.
	CNIPreset string
	TalosCNI  string
	// KubeProxy is enabled or replaced, as detected; always written explicitly.
	KubeProxy string
}

// adoptWriteMachine is one node to adopt. Address is where its Talos API answers.
type adoptWriteMachine struct {
	Address     string
	Role        string
	InstallDisk string
}

// adoptWriteIchor is Ichor's install step, the one `ichor init` runs.
type adoptWriteIchor struct {
	Manifest        []byte
	NetworkPolicies []byte

	// IncludeCiliumPolicies applies NetworkPolicies after the install, as init does. It is the
	// seam for the network-policy gate: the caller sets it from the
	// gate's verdict, and sets true to keep init's behaviour.
	IncludeCiliumPolicies bool
	// CiliumWait bounds the wait for Cilium before the policies are applied.
	CiliumWait time.Duration
}

// adoptWriteResult lists the objects a write created and the ones that already matched.
type adoptWriteResult struct {
	Created, Unchanged []string
}

// errAdoptDiffers is the refusal for an object that exists with different content. It names the
// object, never its content.
var errAdoptDiffers = errors.New("already exists and differs from what adopt would write; refusing to overwrite it")

// writeAdopt runs the write phase. Every step is create-or-skip-if-identical and refuses a
// differing object, so a second run writes nothing. Nothing is applied to a node.
//
// Ichor's install comes first when it is needed (its CRDs and namespace must exist before the
// Secret and the CRs), then the bundle Secret, the TalosCluster, the held TalosMachines and the
// TalosConfigPatches.
func writeAdopt(ctx context.Context, c client.Client, in adoptWriteInput) (*adoptWriteResult, error) {
	w := &adoptWriter{c: c, in: in, res: &adoptWriteResult{}}
	if w.in.Stderr == nil {
		w.in.Stderr = io.Discard
	}

	if err := w.installIchor(ctx); err != nil {
		return w.res, err
	}

	if err := w.bundleSecret(ctx); err != nil {
		return w.res, err
	}

	cluster, err := w.cluster(ctx)
	if err != nil {
		return w.res, err
	}

	if err := w.machines(ctx, cluster); err != nil {
		return w.res, err
	}

	for i := range in.Patches {
		p := in.Patches[i].DeepCopy()
		if err := w.ensure(ctx, p, "TalosConfigPatch", func(have client.Object) bool {
			return jsonEqual(have.(*ichorv1alpha1.TalosConfigPatch).Spec.Documents, p.Spec.Documents) &&
				have.(*ichorv1alpha1.TalosConfigPatch).Spec.ClusterRef == p.Spec.ClusterRef
		}); err != nil {
			return w.res, err
		}
	}

	return w.res, nil
}

type adoptWriter struct {
	c   client.Client
	in  adoptWriteInput
	res *adoptWriteResult
}

// ensure creates obj unless an object of that name exists; an existing one must satisfy same.
func (w *adoptWriter) ensure(ctx context.Context, obj client.Object, kind string, same func(have client.Object) bool) error {
	have, ok := obj.DeepCopyObject().(client.Object)
	if !ok {
		return fmt.Errorf("adopt: %s %q: not an object", kind, obj.GetName())
	}

	label := kind + " " + obj.GetName()

	err := w.c.Get(ctx, client.ObjectKeyFromObject(obj), have)

	switch {
	case err == nil:
		if !same(have) {
			return fmt.Errorf("adopt: %s: %w", label, errAdoptDiffers)
		}

		w.res.Unchanged = append(w.res.Unchanged, label)

		return nil
	case !apierrors.IsNotFound(err):
		return fmt.Errorf("adopt: reading %s: %w", label, err)
	}

	if err := createWhenWebhooksUp(ctx, w.c, obj, cmp.Or(w.in.WebhookWait, defaultWebhookWait), w.in.Interval, w.in.Stderr); err != nil {
		return fmt.Errorf("adopt: creating %s: %w", label, err)
	}

	w.res.Created = append(w.res.Created, label)
	_, _ = fmt.Fprintf(w.in.Stderr, "adopt: created %s\n", label)

	return nil
}

func (w *adoptWriter) installIchor(ctx context.Context) error {
	ichor := w.in.Ichor
	if ichor == nil {
		return nil
	}

	running, err := ichorRunning(ctx, w.c)
	if err != nil {
		return err
	}

	if running {
		w.res.Unchanged = append(w.res.Unchanged, "Ichor installation")

		return nil
	}

	if err := applyIchorManifest(ctx, w.c, ichor.Manifest, w.in.Stderr); err != nil {
		return err
	}

	if _, err := createIchorInstallation(ctx, w.c, w.in.Stderr); err != nil {
		return err
	}

	w.res.Created = append(w.res.Created, "Ichor installation")

	if !ichor.IncludeCiliumPolicies {
		return nil
	}

	preset := cniCilium
	if w.in.Cluster != nil {
		preset = w.in.Cluster.CNIPreset
	}

	return applyNetworkPolicies(ctx, w.c, ichor.NetworkPolicies, preset,
		cmp.Or(ichor.CiliumWait, defaultCiliumWait), w.in.Interval, w.in.Stderr)
}

// ichorRunning reports whether the IchorInstallation singleton exists. A cluster without the CRD
// has no Ichor.
func ichorRunning(ctx context.Context, c client.Client) (bool, error) {
	var inst ichorv1alpha1.IchorInstallation

	err := c.Get(ctx, types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}, &inst)

	switch {
	case err == nil:
		return true, nil
	case apierrors.IsNotFound(err) || apimeta.IsNoMatchError(err):
		return false, nil
	default:
		return false, fmt.Errorf("adopt: looking for an existing Ichor installation: %w", err)
	}
}

func (w *adoptWriter) bundleSecret(ctx context.Context) error {
	if w.in.Bundle == nil || w.in.Cluster == nil {
		return nil
	}

	sec, err := buildBundleSecret(w.cluster0(), w.in.Bundle)
	if err != nil {
		return fmt.Errorf("adopt: preparing the secrets bundle Secret: %w", err)
	}

	return w.ensure(ctx, sec, "Secret", func(have client.Object) bool {
		return bytes.Equal(have.(*corev1.Secret).Data[bundleSecretKey], sec.Data[bundleSecretKey])
	})
}

// cluster0 is the TalosCluster object as it will be written.
func (w *adoptWriter) cluster0() *ichorv1alpha1.TalosCluster {
	f := w.in.Cluster

	cluster := &ichorv1alpha1.TalosCluster{
		Name: f.Name, Namespace: w.in.Namespace,
		Annotations: map[string]string{ichorv1alpha1.TalosClusterAdoptedAnnotation: annotationTrue},
		Spec: ichorv1alpha1.TalosClusterSpec{
			Endpoint:          f.Endpoint,
			TalosVersion:      f.TalosVersion,
			KubernetesVersion: f.KubernetesVersion,
			VersionContract:   f.VersionContract,
			SecretsRef:        corev1.LocalObjectReference{Name: secretsRefName(f.Name)},
			ControlPlane:      ichorv1alpha1.TalosClusterControlPlane{Count: f.ControlPlaneCount, Schedulable: f.Schedulable},
			CNI:               ichorv1alpha1.TalosClusterCNI{Preset: f.CNIPreset, KubeProxy: f.KubeProxy},
		},
	}

	if len(f.PodSubnets) > 0 || len(f.ServiceSubnets) > 0 {
		cluster.Spec.Network = &ichorv1alpha1.TalosClusterNetwork{PodSubnets: f.PodSubnets, ServiceSubnets: f.ServiceSubnets}
	}

	if f.CNIPreset == cniUnmanaged {
		cluster.Spec.CNI.Unmanaged = &ichorv1alpha1.TalosClusterCNIUnmanaged{TalosCNI: cmp.Or(f.TalosCNI, "default")}
	}

	return cluster
}

// cluster creates the TalosCluster and returns its name, or "" when none is written here.
//
// CARotationAdvised is not set here: status is a subresource the controller owns, so
// TalosClusterReconciler sets it when it sees the adoption annotation (taloscluster_adopted.go).
func (w *adoptWriter) cluster(ctx context.Context) (string, error) {
	if w.in.Cluster == nil {
		return "", nil
	}

	want := w.cluster0()

	// Compared on what adopt decides; the API server may default the rest.
	err := w.ensure(ctx, want, "TalosCluster", func(have client.Object) bool {
		h := have.(*ichorv1alpha1.TalosCluster)

		return h.Annotations[ichorv1alpha1.TalosClusterAdoptedAnnotation] == annotationTrue &&
			h.Spec.VersionContract == want.Spec.VersionContract &&
			h.Spec.CNI.Preset == want.Spec.CNI.Preset &&
			h.Spec.CNI.KubeProxy == want.Spec.CNI.KubeProxy &&
			cmpUnmanaged(h.Spec.CNI.Unmanaged) == cmpUnmanaged(want.Spec.CNI.Unmanaged) &&
			jsonEqual(h.Spec.Network, want.Spec.Network) &&
			h.Spec.Endpoint == want.Spec.Endpoint
	})

	return want.Name, err
}

func cmpUnmanaged(u *ichorv1alpha1.TalosClusterCNIUnmanaged) string {
	if u == nil {
		return ""
	}

	return cmp.Or(u.TalosCNI, "default")
}

func (w *adoptWriter) machines(ctx context.Context, clusterName string) error {
	if len(w.in.Machines) == 0 {
		return nil
	}

	if w.in.ReadIdentity == nil {
		return errors.New("adopt: no way to read the nodes' hardware identity was given")
	}

	if clusterName == "" {
		return errors.New("adopt: TalosMachines need the TalosCluster they belong to")
	}

	for _, m := range w.in.Machines {
		identity, err := w.in.ReadIdentity(ctx, m.Address)
		if err != nil {
			return fmt.Errorf("adopt: reading the hardware identity of %q: %w", m.Address, err)
		}

		if identity.UUID == "" {
			return fmt.Errorf("adopt: the node at %q reported no SMBIOS UUID; adopt needs a strong hardware identity", m.Address)
		}

		// A node whose configuration states no install disk gets no spec.install: the render
		// then states none either.
		var install *ichorv1alpha1.TalosMachineInstall

		if m.InstallDisk != "" {
			disk, err := parseInstallDisk(m.InstallDisk)
			if err != nil {
				return fmt.Errorf("adopt: node %q: %w", m.Address, err)
			}

			install = &ichorv1alpha1.TalosMachineInstall{Disk: *disk}
		}

		want := &ichorv1alpha1.TalosMachine{
			Name: identity.UUID, Namespace: w.in.Namespace,
			Annotations: map[string]string{ichorv1alpha1.TalosMachineHoldApplyAnnotation: annotationTrue},
			Spec: ichorv1alpha1.TalosMachineSpec{
				ClusterRef: corev1.LocalObjectReference{Name: clusterName},
				Identity: ichorv1alpha1.TalosMachineIdentity{
					UUID: identity.UUID, Serial: identity.Serial, MACAddresses: identity.MACAddresses,
				},
				Endpoint:      m.Address,
				Role:          m.Role,
				Install:       install,
				ApprovalState: ichorv1alpha1.MachineApprovalStateApproved,
			},
		}

		// The hold annotation is not compared: the release phase removes it, and a rerun after
		// that must not look like a conflict.
		if err := w.ensure(ctx, want, "TalosMachine", func(have client.Object) bool {
			h := have.(*ichorv1alpha1.TalosMachine)

			return h.Spec.ClusterRef == want.Spec.ClusterRef && h.Spec.Role == want.Spec.Role &&
				h.Spec.Endpoint == want.Spec.Endpoint && h.Spec.Identity.UUID == want.Spec.Identity.UUID &&
				jsonEqual(h.Spec.Install, want.Spec.Install)
		}); err != nil {
			return err
		}
	}

	return nil
}

// jsonEqual compares two values by their JSON, treating nil and empty alike.
func jsonEqual(a, b any) bool {
	x, errA := json.Marshal(a)
	y, errB := json.Marshal(b)

	return errA == nil && errB == nil && bytes.Equal(x, y)
}
