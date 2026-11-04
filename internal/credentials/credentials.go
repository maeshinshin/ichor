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

// Package credentials builds the operator's kubeconfig and talosconfig from a cluster's secrets
// bundle and, during a CA rotation, its rotation.yaml (ADR 0091), and checks that a kubeconfig
// authenticates. The CLI and the UI backend (ADR 0093) share it, so the two hand over the same
// files. Nothing here prints, logs or returns key material in an error.
package credentials

import (
	"context"
	stdx509 "crypto/x509"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/siderolabs/crypto/x509"
	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/siderolabs/talos/pkg/machinery/role"
	authv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
)

const (
	// KubeAdminOrganization is the group that makes a Kubernetes client certificate cluster-admin;
	// KubeAdminCommonName is the name Talos gives its own admin kubeconfig.
	KubeAdminOrganization = "system:masters"
	KubeAdminCommonName   = "admin"

	// Lifetime is how long the client certificates are valid; the CAs outlive them.
	Lifetime = 365 * 24 * time.Hour

	// skew backdates the certificates' start, like the ones Talos issues.
	skew = 10 * time.Second

	roleControlPlane = "controlplane"
)

// Sources is what the credentials are made from: for each API the CAs to trust (all of them) and
// the one CA key that signs the client certificate.
type Sources struct {
	// Dual is true when a rotation is in progress, so the trust lists hold the old and the new CA.
	Dual bool

	KubernetesTrust  []byte
	KubernetesSigner *x509.PEMEncodedCertificateAndKey

	TalosTrust  []byte
	TalosSigner *x509.PEMEncodedCertificateAndKey

	// TTL is how long the client certificates Kubeconfig and Talosconfig make are valid; zero means Lifetime.
	// It is never longer than MaxLifetime (ADR 0108).
	TTL time.Duration

	// Reader makes Talosconfig an os:reader client instead of os:admin.
	Reader bool

	// bundle is the secrets bundle the Talos client certificate is generated from (its OS CA is the
	// signer).
	bundle *secrets.Bundle
}

// NewSources takes the CAs from bundle and, when a rotation is under way, from rot as well: the new
// CAs sign, the old and the new are trusted. rot is nil outside a rotation.
func NewSources(bundle *secrets.Bundle, rot *secretsbundle.Rotation) (*Sources, error) {
	if bundle == nil || bundle.Certs == nil || bundle.Certs.K8s == nil || bundle.Certs.OS == nil {
		return nil, errors.New("the secrets bundle has no Talos or Kubernetes CA")
	}

	s := &Sources{
		KubernetesSigner: bundle.Certs.K8s, KubernetesTrust: pemWithNewline(bundle.Certs.K8s.Crt),
		TalosSigner: bundle.Certs.OS, TalosTrust: pemWithNewline(bundle.Certs.OS.Crt),
	}

	if rot != nil {
		if err := rot.Validate(); err != nil {
			return nil, err
		}

		// A rotation of the etcd or aggregator CA alone changes nothing a credential depends on (ADR 0120).
		if rot.Kubernetes != nil {
			s.Dual = true
			s.KubernetesSigner = rot.Kubernetes
			s.KubernetesTrust = slices.Concat(pemWithNewline(rot.Kubernetes.Crt), pemWithNewline(bundle.Certs.K8s.Crt))
		}

		if rot.Talos != nil {
			s.Dual = true
			s.TalosSigner = rot.Talos
			s.TalosTrust = slices.Concat(pemWithNewline(rot.Talos.Crt), pemWithNewline(bundle.Certs.OS.Crt))
		}
	}

	derived := *bundle
	certs := *bundle.Certs
	certs.OS = s.TalosSigner
	derived.Certs = &certs
	s.bundle = &derived

	return s, nil
}

// Load reads the cluster's secrets bundle and rotation from the Secret in ichorNamespace and
// returns the sources with the Talos endpoints of the cluster's control-plane machines (sorted).
func Load(ctx context.Context, c client.Client, ichorNamespace string, cluster *ichorv1alpha1.TalosCluster) (*Sources, []string, error) {
	bundle, err := secretsbundle.Load(ctx, c, ichorNamespace, cluster.Spec.SecretsRef.Name)
	if err != nil {
		return nil, nil, err
	}

	rot, err := secretsbundle.LoadRotation(ctx, c, ichorNamespace, cluster.Spec.SecretsRef.Name)
	if err != nil {
		return nil, nil, err
	}

	src, err := NewSources(bundle, rot)
	if err != nil {
		return nil, nil, err
	}

	var machines ichorv1alpha1.TalosMachineList
	if err := c.List(ctx, &machines, client.InNamespace(cluster.Namespace)); err != nil {
		return nil, nil, fmt.Errorf("listing the cluster's machines: %w", err)
	}

	return src, Endpoints(machines.Items, cluster.Name), nil
}

// lifetime is TTL within [Lifetime default, MaxLifetime cap].
func (s *Sources) lifetime() time.Duration {
	if s.TTL <= 0 || s.TTL > MaxLifetime {
		return Lifetime
	}

	return s.TTL
}

// Endpoints are the Talos API addresses of the cluster's control-plane machines, sorted.
func Endpoints(machines []ichorv1alpha1.TalosMachine, cluster string) []string {
	var endpoints []string

	for i := range machines {
		m := &machines[i]
		if m.Spec.ClusterRef.Name == cluster && m.Spec.Role == roleControlPlane && m.DeletionTimestamp.IsZero() && m.DialEndpoint() != "" {
			endpoints = append(endpoints, m.DialEndpoint())
		}
	}

	slices.Sort(endpoints)

	return endpoints
}

func pemWithNewline(b []byte) []byte {
	if len(b) > 0 && b[len(b)-1] != '\n' {
		return append(slices.Clone(b), '\n')
	}

	return b
}

// ServerConfig is a kubeconfig template naming only a server, for callers with no kubeconfig of
// their own to copy it from: one cluster, one context and one user, all called name.
func ServerConfig(name, server string) clientcmdapi.Config {
	return clientcmdapi.Config{
		CurrentContext: name,
		Clusters:       map[string]*clientcmdapi.Cluster{name: {Server: server}},
		Contexts:       map[string]*clientcmdapi.Context{name: {Cluster: name, AuthInfo: KubeAdminCommonName}},
	}
}

// Kubeconfig is current with its cluster's CA data replaced by the trust list and its user replaced
// by a client certificate signed by the signer. The server, names and namespace stay as they were;
// only the current context is kept.
func (s *Sources) Kubeconfig(current clientcmdapi.Config) ([]byte, error) {
	if _, _, err := kubeconfigParts(current); err != nil {
		return nil, err
	}

	ca, err := x509.NewCertificateAuthorityFromCertificateAndKey(s.KubernetesSigner)
	if err != nil {
		return nil, fmt.Errorf("reading the Kubernetes CA: %w", err)
	}

	pair, err := x509.NewKeyPair(ca,
		x509.CommonName(KubeAdminCommonName), x509.Organization(KubeAdminOrganization),
		x509.NotBefore(time.Now().Add(-skew)), x509.NotAfter(time.Now().Add(s.lifetime())),
		x509.KeyUsage(stdx509.KeyUsageDigitalSignature|stdx509.KeyUsageKeyEncipherment),
		x509.ExtKeyUsage([]stdx509.ExtKeyUsage{stdx509.ExtKeyUsageClientAuth}),
	)
	if err != nil {
		return nil, fmt.Errorf("generating a Kubernetes client certificate: %w", err)
	}

	cert := x509.NewCertificateAndKeyFromKeyPair(pair)

	return BuildKubeconfig(current, s.KubernetesTrust, cert.Crt, cert.Key)
}

func kubeconfigParts(current clientcmdapi.Config) (*clientcmdapi.Context, *clientcmdapi.Cluster, error) {
	ctxName := current.CurrentContext

	kctx := current.Contexts[ctxName]
	if kctx == nil {
		return nil, nil, errors.New("the current kubeconfig has no current context to copy the server from")
	}

	cluster := current.Clusters[kctx.Cluster]
	if cluster == nil || cluster.Server == "" {
		return nil, nil, fmt.Errorf("the current kubeconfig's context %q names no cluster with a server", ctxName)
	}

	return kctx, cluster, nil
}

// BuildKubeconfig is current with its cluster's CA data replaced by trust and its user replaced by the
// client certificate crt and its key. The server, names and namespace stay as they were; only the
// current context is kept. The controller signs crt for a key the requester holds (ADR 0108).
func BuildKubeconfig(current clientcmdapi.Config, trust, crt, key []byte) ([]byte, error) {
	kctx, cluster, err := kubeconfigParts(current)
	if err != nil {
		return nil, err
	}

	userName := kctx.AuthInfo
	if userName == "" {
		userName = KubeAdminCommonName
	}

	out := clientcmdapi.Config{
		APIVersion: "v1", Kind: "Config", CurrentContext: current.CurrentContext,
		Clusters: map[string]*clientcmdapi.Cluster{kctx.Cluster: {
			Server: cluster.Server, TLSServerName: cluster.TLSServerName, ProxyURL: cluster.ProxyURL,
			CertificateAuthorityData: trust,
		}},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{userName: {ClientCertificateData: crt, ClientKeyData: key}},
		Contexts: map[string]*clientcmdapi.Context{current.CurrentContext: {
			Cluster: kctx.Cluster, AuthInfo: userName, Namespace: kctx.Namespace,
		}},
	}

	return clientcmd.Write(out)
}

// Talosconfig is a one-context talosconfig named name that trusts the Talos API CAs and presents an
// os:admin client certificate (os:reader with Reader) signed by the signer.
func (s *Sources) Talosconfig(name string, endpoints []string) ([]byte, error) {
	r := role.Admin
	if s.Reader {
		r = role.Reader
	}

	cert, err := s.bundle.GenerateTalosAPIClientCertificateWithTTL(role.MakeSet(r), s.lifetime())
	if err != nil {
		return nil, fmt.Errorf("generating a Talos client certificate: %w", err)
	}

	return clientconfig.NewConfig(name, endpoints, s.TalosTrust, cert).Bytes()
}

// BuildTalosconfig is a one-context talosconfig named name that trusts trust and presents the client
// certificate crt and its key.
func BuildTalosconfig(name string, endpoints []string, trust, crt, key []byte) ([]byte, error) {
	return clientconfig.NewConfig(name, endpoints, trust, &x509.PEMEncodedCertificateAndKey{Crt: crt, Key: key}).Bytes()
}

// VerifyKubeconfig asks the API server who the kubeconfig's credentials are. It proves the file
// authenticates, so the operator is not stranded. The error never carries the file's content.
func VerifyKubeconfig(ctx context.Context, kubeconfig []byte) error {
	cfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return fmt.Errorf("reading the kubeconfig: %w", err)
	}

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return err
	}

	_, err = cs.AuthenticationV1().SelfSubjectReviews().Create(ctx, &authv1.SelfSubjectReview{}, metav1.CreateOptions{})

	return err
}
