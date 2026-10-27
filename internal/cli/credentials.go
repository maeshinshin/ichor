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
	stdx509 "crypto/x509"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/siderolabs/crypto/x509"
	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/siderolabs/talos/pkg/machinery/role"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
)

// Credentials for the operator (ADR 0090). A CA rotation replaces the CAs the operator's kubeconfig
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

	// kubeAdminOrganization is the group that makes a Kubernetes client certificate cluster-admin;
	// kubeAdminCommonName is the name Talos gives its own admin kubeconfig.
	kubeAdminOrganization = "system:masters"
	kubeAdminCommonName   = "admin"

	// credentialLifetime is how long the client certificates are valid; the CAs outlive them.
	credentialLifetime = 365 * 24 * time.Hour

	// credentialSkew backdates the certificates' start, like the ones Talos issues.
	credentialSkew = 10 * time.Second
)

// credentialSources is what the credentials are made from: for each API the CAs to trust (all of
// them) and the one CA key that signs the client certificate.
type credentialSources struct {
	// Dual is true when a rotation is in progress, so the trust lists hold the old and the new CA.
	Dual bool

	KubernetesTrust  []byte
	KubernetesSigner *x509.PEMEncodedCertificateAndKey

	TalosTrust  []byte
	TalosSigner *x509.PEMEncodedCertificateAndKey

	// bundle is the secrets bundle the Talos client certificate is generated from (its OS CA is the
	// signer).
	bundle *secrets.Bundle
}

// newCredentialSources takes the CAs from bundle and, when a rotation is under way, from rot as well:
// the new CAs sign, the old and the new are trusted. rot is nil outside a rotation.
func newCredentialSources(bundle *secrets.Bundle, rot *secretsbundle.Rotation) (*credentialSources, error) {
	if bundle == nil || bundle.Certs == nil || bundle.Certs.K8s == nil || bundle.Certs.OS == nil {
		return nil, errors.New("the secrets bundle has no Talos or Kubernetes CA")
	}

	s := &credentialSources{
		KubernetesSigner: bundle.Certs.K8s, KubernetesTrust: pemWithNewline(bundle.Certs.K8s.Crt),
		TalosSigner: bundle.Certs.OS, TalosTrust: pemWithNewline(bundle.Certs.OS.Crt),
	}

	if rot != nil {
		if err := rot.Validate(); err != nil {
			return nil, err
		}

		s.Dual = true
		s.KubernetesSigner = rot.Kubernetes
		s.KubernetesTrust = slices.Concat(pemWithNewline(rot.Kubernetes.Crt), pemWithNewline(bundle.Certs.K8s.Crt))
		s.TalosSigner = rot.Talos
		s.TalosTrust = slices.Concat(pemWithNewline(rot.Talos.Crt), pemWithNewline(bundle.Certs.OS.Crt))
	}

	derived := *bundle
	certs := *bundle.Certs
	certs.OS = s.TalosSigner
	derived.Certs = &certs
	s.bundle = &derived

	return s, nil
}

func pemWithNewline(b []byte) []byte {
	if len(b) > 0 && b[len(b)-1] != '\n' {
		return append(slices.Clone(b), '\n')
	}

	return b
}

// kubeconfig is current with its cluster's CA data replaced by the trust list and its user replaced by
// a client certificate signed by the signer. The server, names and namespace stay as they were; only
// the current context is kept.
func (s *credentialSources) kubeconfig(current clientcmdapi.Config) ([]byte, error) {
	ctxName := current.CurrentContext

	kctx := current.Contexts[ctxName]
	if kctx == nil {
		return nil, errors.New("the current kubeconfig has no current context to copy the server from")
	}

	cluster := current.Clusters[kctx.Cluster]
	if cluster == nil || cluster.Server == "" {
		return nil, fmt.Errorf("the current kubeconfig's context %q names no cluster with a server", ctxName)
	}

	ca, err := x509.NewCertificateAuthorityFromCertificateAndKey(s.KubernetesSigner)
	if err != nil {
		return nil, fmt.Errorf("reading the Kubernetes CA: %w", err)
	}

	pair, err := x509.NewKeyPair(ca,
		x509.CommonName(kubeAdminCommonName), x509.Organization(kubeAdminOrganization),
		x509.NotBefore(time.Now().Add(-credentialSkew)), x509.NotAfter(time.Now().Add(credentialLifetime)),
		x509.KeyUsage(stdx509.KeyUsageDigitalSignature|stdx509.KeyUsageKeyEncipherment),
		x509.ExtKeyUsage([]stdx509.ExtKeyUsage{stdx509.ExtKeyUsageClientAuth}),
	)
	if err != nil {
		return nil, fmt.Errorf("generating a Kubernetes client certificate: %w", err)
	}

	cert := x509.NewCertificateAndKeyFromKeyPair(pair)

	userName := kctx.AuthInfo
	if userName == "" {
		userName = kubeAdminCommonName
	}

	out := clientcmdapi.Config{
		APIVersion: "v1", Kind: "Config", CurrentContext: ctxName,
		Clusters: map[string]*clientcmdapi.Cluster{kctx.Cluster: {
			Server: cluster.Server, TLSServerName: cluster.TLSServerName, ProxyURL: cluster.ProxyURL,
			CertificateAuthorityData: s.KubernetesTrust,
		}},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{userName: {ClientCertificateData: cert.Crt, ClientKeyData: cert.Key}},
		Contexts: map[string]*clientcmdapi.Context{ctxName: {
			Cluster: kctx.Cluster, AuthInfo: userName, Namespace: kctx.Namespace,
		}},
	}

	return clientcmd.Write(out)
}

// talosconfig is a one-context talosconfig named name that trusts the Talos API CAs and presents an
// os:admin client certificate signed by the signer.
func (s *credentialSources) talosconfig(name string, endpoints []string) ([]byte, error) {
	cert, err := s.bundle.GenerateTalosAPIClientCertificate(role.MakeSet(role.Admin))
	if err != nil {
		return nil, fmt.Errorf("generating a Talos client certificate: %w", err)
	}

	return clientconfig.NewConfig(name, endpoints, s.TalosTrust, cert).Bytes()
}

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

	kube, err := src.kubeconfig(*current)
	if err != nil {
		return nil, err
	}

	talos, err := src.talosconfig(req.Cluster, endpoints)
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
func loadCredentialSources(ctx context.Context, k8s client.Client, req credentialRequest) (*credentialSources, []string, error) {
	if req.BundleFile != "" {
		bundle, err := loadBundleFile(req.BundleFile)
		if err != nil {
			return nil, nil, err
		}

		src, err := newCredentialSources(bundle, nil)

		return src, nil, err
	}

	if k8s == nil {
		return nil, nil, errors.New("no cluster to read the CAs from: pass --bundle with a secrets bundle file")
	}

	var cluster ichorv1alpha1.TalosCluster
	if err := k8s.Get(ctx, types.NamespacedName{Namespace: req.Namespace, Name: req.Cluster}, &cluster); err != nil {
		return nil, nil, fmt.Errorf("getting TalosCluster %s/%s: %w", req.Namespace, req.Cluster, err)
	}

	bundle, err := secretsbundle.Load(ctx, k8s, req.IchorNamespace, cluster.Spec.SecretsRef.Name)
	if err != nil {
		return nil, nil, err
	}

	rot, err := secretsbundle.LoadRotation(ctx, k8s, req.IchorNamespace, cluster.Spec.SecretsRef.Name)
	if err != nil {
		return nil, nil, err
	}

	src, err := newCredentialSources(bundle, rot)
	if err != nil {
		return nil, nil, err
	}

	var machines ichorv1alpha1.TalosMachineList
	if err := k8s.List(ctx, &machines, client.InNamespace(req.Namespace)); err != nil {
		return nil, nil, fmt.Errorf("listing the cluster's machines: %w", err)
	}

	var endpoints []string

	for i := range machines.Items {
		m := &machines.Items[i]
		if m.Spec.ClusterRef.Name == req.Cluster && m.Spec.Role == roleControlPlane && m.DeletionTimestamp.IsZero() && m.DialEndpoint() != "" {
			endpoints = append(endpoints, m.DialEndpoint())
		}
	}

	slices.Sort(endpoints)

	return src, endpoints, nil
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
