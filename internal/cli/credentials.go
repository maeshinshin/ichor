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
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/credentials"
)

// Credentials for the operator (ADR 0091). A CA rotation replaces the CAs the operator's kubeconfig
// and talosconfig trust and sign with. `talosctl rotate-ca` hands the operator a new talosconfig as a
// side effect; Ichor's rotation is run by the controller, so this file is where the operator gets
// theirs: a kubeconfig and a talosconfig that trust the old and the new CAs and carry a client
// certificate from the new one. While the rotation waits in AwaitingCredentials every node accepts
// both CAs, so these files work before the Kubernetes API starts serving the new CA and keep working
// after the old one is dropped.

const (
	// credentialsSuffix is appended to the current kubeconfig's and talosconfig's path to name the
	// files written next to them: the current ones are never overwritten.
	credentialsSuffix = ".ichor-new"
)

// credentialRequest is one run of writeCredentials.
type credentialRequest struct {
	Cluster        string
	Namespace      string
	IchorNamespace string

	// BundleFile, when set, takes the CAs from this file (bundle-export's output) and reads nothing
	// from the cluster: the offline path.
	BundleFile string

	// KubeconfigIn is the kubeconfig the server and names are copied from ("" resolves it like kubectl).
	KubeconfigIn string

	// TalosconfigIn is the current talosconfig ("" resolves it like talosctl); it names the default
	// output and supplies endpoints when nothing else does.
	TalosconfigIn string

	KubeconfigOut  string
	TalosconfigOut string
	TalosEndpoints []string
}

// credentialFiles says what was written.
type credentialFiles struct {
	Kubeconfig  string
	Talosconfig string
	// Dual is true when the files trust the old and the new CAs, which is the case during a rotation.
	Dual bool
	// ForcedStaleBundle is true when the CAs came from a bundle file found stale and used with --force.
	ForcedStaleBundle bool
}

// writeCredentials writes the kubeconfig and talosconfig. With a cluster client (k8s) it reads the
// secrets bundle and, when present, rotation.yaml from the cluster's Secret; with req.BundleFile it
// reads the file and k8s may be nil. Nothing secret is returned or printed.
func writeCredentials(ctx context.Context, k8s client.Client, req credentialRequest) (*credentialFiles, error) {
	src, endpoints, err := loadCredentialSources(ctx, k8s, req)
	if err != nil {
		return nil, err
	}

	kubeIn := currentKubeconfigFile(req.KubeconfigIn)

	current, err := loadKubeconfigTemplate(req.KubeconfigIn)
	if err != nil {
		return nil, err
	}

	talosIn := currentTalosconfigFile(req.TalosconfigIn)
	if len(req.TalosEndpoints) > 0 {
		endpoints = req.TalosEndpoints
	}

	if len(endpoints) == 0 {
		endpoints = endpointsOfTalosconfig(talosIn)
	}

	if len(endpoints) == 0 {
		return nil, errors.New("no Talos API endpoint known for the talosconfig: pass --talos-endpoints")
	}

	kubeOut := firstNonEmpty(req.KubeconfigOut, kubeIn+credentialsSuffix)
	talosOut := firstNonEmpty(req.TalosconfigOut, talosIn+credentialsSuffix)

	if sameFile(kubeOut, kubeIn) || sameFile(talosOut, talosIn) {
		return nil, errors.New("refusing to overwrite the current kubeconfig or talosconfig; choose another output path")
	}

	kube, err := src.Kubeconfig(*current)
	if err != nil {
		return nil, err
	}

	talos, err := src.Talosconfig(req.Cluster, endpoints)
	if err != nil {
		return nil, err
	}

	for path, data := range map[string][]byte{kubeOut: kube, talosOut: talos} {
		if err := os.MkdirAll(filepath.Dir(path), bundleDirPerm); err != nil {
			return nil, fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
		}

		if err := writeFileAtomic(path, data); err != nil {
			return nil, fmt.Errorf("writing %s: %w", path, err)
		}
	}

	return &credentialFiles{Kubeconfig: kubeOut, Talosconfig: talosOut, Dual: src.Dual}, nil
}

// loadCredentialSources returns the CAs and, from the cluster, the control planes' Talos endpoints.
func loadCredentialSources(ctx context.Context, k8s client.Client, req credentialRequest) (*credentials.Sources, []string, error) {
	if req.BundleFile != "" {
		bundle, err := loadBundleFile(req.BundleFile)
		if err != nil {
			return nil, nil, err
		}

		src, err := credentials.NewSources(bundle, nil)

		return src, nil, err
	}

	if k8s == nil {
		return nil, nil, errors.New("no cluster to read the CAs from: pass --bundle with a secrets bundle file")
	}

	var cluster ichorv1alpha1.TalosCluster
	if err := k8s.Get(ctx, types.NamespacedName{Namespace: req.Namespace, Name: req.Cluster}, &cluster); err != nil {
		return nil, nil, fmt.Errorf("getting TalosCluster %s/%s: %w", req.Namespace, req.Cluster, err)
	}

	return credentials.Load(ctx, k8s, req.IchorNamespace, &cluster)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}

	return b
}

func sameFile(a, b string) bool {
	if a == b {
		return true
	}

	ai, errA := os.Stat(a)
	bi, errB := os.Stat(b)

	return errA == nil && errB == nil && os.SameFile(ai, bi)
}

// currentKubeconfigFile is the file the current kubeconfig comes from: the explicit path, else the
// first existing file in kubectl's search order, else a file in the cluster's ichor directory.
func currentKubeconfigFile(explicit string) string {
	if explicit != "" {
		return explicit
	}

	for _, p := range clientcmd.NewDefaultClientConfigLoadingRules().GetLoadingPrecedence() {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	home, _ := os.UserHomeDir()

	return filepath.Join(home, ".kube", "config")
}

func loadKubeconfigTemplate(explicit string) (*clientcmdapi.Config, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	rules.ExplicitPath = explicit

	cfg, err := rules.Load()
	if err != nil {
		return nil, fmt.Errorf("reading the current kubeconfig: %w", err)
	}

	return cfg, nil
}

// currentTalosconfigFile is the talosconfig talosctl would read: the explicit path, $TALOSCONFIG, or
// ~/.talos/config.
func currentTalosconfigFile(explicit string) string {
	if explicit != "" {
		return explicit
	}

	if env := os.Getenv("TALOSCONFIG"); env != "" {
		return env
	}

	home, _ := os.UserHomeDir()

	return filepath.Join(home, ".talos", "config")
}

// endpointsOfTalosconfig reads the endpoints of the current context of the talosconfig at path; none
// if it does not exist or cannot be read.
func endpointsOfTalosconfig(path string) []string {
	if _, err := os.Stat(path); err != nil {
		return nil
	}

	cfg, err := clientconfig.Open(path)
	if err != nil {
		return nil
	}

	if c := cfg.Contexts[cfg.Context]; c != nil {
		return c.Endpoints
	}

	return nil
}

// acknowledgeHandoff tells the controller the operator has credentials that work with both CAs, by
// setting the hand-off annotation on the cluster's ca-rotation operation that is waiting for it. name
// may be empty: then the one waiting operation of the cluster is used.
func acknowledgeHandoff(ctx context.Context, k8s client.Client, namespace, cluster, name string) (string, error) {
	var ops ichorv1alpha1.TalosOperationList
	if err := k8s.List(ctx, &ops, client.InNamespace(namespace)); err != nil {
		return "", fmt.Errorf("listing operations: %w", err)
	}

	var waiting []*ichorv1alpha1.TalosOperation

	for i := range ops.Items {
		op := &ops.Items[i]
		if op.Spec.ClusterRef.Name == cluster && op.Spec.Kind == ichorv1alpha1.TalosOperationKindCARotation &&
			(name == "" || op.Name == name) && op.Status.CARotation != nil &&
			op.Status.CARotation.Stage == ichorv1alpha1.CARotationStageAwaitingCredentials {
			waiting = append(waiting, op)
		}
	}

	switch len(waiting) {
	case 0:
		return "", fmt.Errorf("no CA rotation of cluster %q is waiting for credentials (it is either not yet past Accepting, or already past this step)", cluster)
	case 1:
	default:
		return "", fmt.Errorf("%d CA rotations of cluster %q are waiting for credentials; name one with --operation", len(waiting), cluster)
	}

	op := waiting[0]
	base := op.DeepCopy()

	if op.Annotations == nil {
		op.Annotations = map[string]string{}
	}

	op.Annotations[ichorv1alpha1.CredentialsHandoffAnnotation] = ichorv1alpha1.CARotationHandoffToken(op.Status.CARotation)

	if err := k8s.Patch(ctx, op, client.MergeFrom(base)); err != nil {
		return "", fmt.Errorf("acknowledging the hand-off on operation %s/%s: %w", op.Namespace, op.Name, err)
	}

	return op.Name, nil
}
