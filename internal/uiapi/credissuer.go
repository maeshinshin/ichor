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

package uiapi

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/siderolabs/crypto/x509"
	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/credentials"
)

// This backend holds no CA key. Whatever it needs a certificate or a cluster's public facts for,
// it asks the controller by creating a TalosCredentialRequest, which the admission webhook gates and the
// controller answers. The key pair is made here and stays here: the request carries only a certificate
// request, the answer only a certificate.

const (
	// credentialWait bounds how long a request may wait for the controller.
	credentialWait = 20 * time.Second
	credentialPoll = 100 * time.Millisecond

	// publicTTL is how long a cluster's public facts are reused.
	publicTTL = time.Minute
)

// TalosIdentity is a Talos API client identity the controller issued for a key made in this process.
type TalosIdentity struct {
	// CA is the PEM certificate(s) the node's API is verified against.
	CA []byte
	// Crt and Key are the client certificate and its private key (PEM).
	Crt, Key []byte
	// NotAfter is when Crt expires.
	NotAfter time.Time
}

// credentialCache holds what is reused between calls: the reader identity per cluster, and the public facts.
type credentialCache struct {
	mu      sync.Mutex
	readers map[types.NamespacedName]*TalosIdentity
	publics map[types.NamespacedName]cachedPublic
}

type cachedPublic struct {
	facts *ichorv1alpha1.TalosCredentialPublic
	until time.Time
}

// credentialAnswer creates req with c (the identity whose permission the webhook checks), waits for the
// controller's answer, read with the backend's own client, and returns it.
func (s *Server) credentialAnswer(ctx context.Context, c client.Client, req *ichorv1alpha1.TalosCredentialRequest) (*ichorv1alpha1.TalosCredentialRequestStatus, error) {
	if err := c.Create(ctx, req); err != nil {
		return nil, err
	}

	key := types.NamespacedName{Namespace: req.Namespace, Name: req.Name}

	var answer *ichorv1alpha1.TalosCredentialRequestStatus

	err := wait.PollUntilContextTimeout(ctx, credentialPoll, credentialWait, true, func(ctx context.Context) (bool, error) {
		var got ichorv1alpha1.TalosCredentialRequest
		if err := s.Client.Get(ctx, key, &got); err != nil {
			return false, err
		}

		if got.Status.Phase == "" {
			return false, nil
		}

		answer = &got.Status

		return true, nil
	})
	if err != nil {
		return nil, fmt.Errorf("waiting for the controller to answer the credential request: %w", err)
	}

	if answer.Phase != ichorv1alpha1.CredentialPhaseIssued {
		return nil, fmt.Errorf("the controller refused the credential request: %s", truncateText(answer.Message, 300))
	}

	return answer, nil
}

func newCredentialRequest(cluster *ichorv1alpha1.TalosCluster, purpose ichorv1alpha1.TalosCredentialPurpose) *ichorv1alpha1.TalosCredentialRequest {
	return &ichorv1alpha1.TalosCredentialRequest{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "cred-", Namespace: cluster.Namespace},
		Spec: ichorv1alpha1.TalosCredentialRequestSpec{
			ClusterRef: corev1.LocalObjectReference{Name: cluster.Name}, Purpose: purpose,
		},
	}
}

// issue asks the controller, as c, for a certificate for purpose. kubernetes selects the Kubernetes
// administrator certificate (ECDSA key) over a Talos one (Ed25519). machine is the TalosMachine of a
// download purpose. ttl zero means the purpose's default; talosRole is "reader" or empty (admin) for
// credentials-talos.
func (s *Server) issue(
	ctx context.Context, c client.Client, cluster *ichorv1alpha1.TalosCluster, purpose ichorv1alpha1.TalosCredentialPurpose,
	machine string, ttl time.Duration, talosRole string,
) (*TalosIdentity, error) {
	csr, key, err := credentials.NewCSR(purpose == ichorv1alpha1.CredentialPurposeCredentialsKubernetes)
	if err != nil {
		return nil, err
	}

	req := newCredentialRequest(cluster, purpose)
	req.Spec.CSR, req.Spec.TalosRole = string(csr), talosRole

	if machine != "" {
		req.Spec.MachineRef = &corev1.LocalObjectReference{Name: machine}
	}

	if ttl > 0 {
		req.Spec.TTL = &metav1.Duration{Duration: ttl}
	}

	answer, err := s.credentialAnswer(ctx, c, req)
	if err != nil {
		return nil, err
	}

	id := &TalosIdentity{CA: []byte(answer.Trust), Crt: []byte(answer.Certificate), Key: key}
	if answer.NotAfter != nil {
		id.NotAfter = answer.NotAfter.Time
	}

	return id, nil
}

// issueAsCaller is issue as the signed-in user, so the webhook checks that user's permission.
func (s *Server) issueAsCaller(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, purpose ichorv1alpha1.TalosCredentialPurpose,
	machine string, ttl time.Duration, talosRole string,
) (*TalosIdentity, error) {
	c, err := s.cl(ctx)
	if err != nil {
		return nil, fmt.Errorf("acting as the user: %w", err)
	}

	return s.issue(ctx, c, cluster, purpose, machine, ttl, talosRole)
}

// nodeReader is the identity for this backend's own reads of a cluster's nodes. The backend asks for it as
// itself (the permission `issue` on talosclusters/node-reader) and reuses it until half its life is gone.
func (s *Server) nodeReader(ctx context.Context, cluster *ichorv1alpha1.TalosCluster) (*TalosIdentity, error) {
	key := client.ObjectKeyFromObject(cluster)

	s.creds.mu.Lock()
	defer s.creds.mu.Unlock()

	if id := s.creds.readers[key]; id != nil && time.Until(id.NotAfter) > credentials.DialLifetime/2 {
		return id, nil
	}

	id, err := s.issue(ctx, s.Client, cluster, ichorv1alpha1.CredentialPurposeNodeReader, "", 0, "")
	if err != nil {
		return nil, err
	}

	if s.creds.readers == nil {
		s.creds.readers = map[types.NamespacedName]*TalosIdentity{}
	}

	s.creds.readers[key] = id

	return id, nil
}

// publicFacts is the cluster's public data (ID, CA certificates), asked for as the backend itself.
func (s *Server) publicFacts(ctx context.Context, cluster *ichorv1alpha1.TalosCluster) (*ichorv1alpha1.TalosCredentialPublic, error) {
	key := client.ObjectKeyFromObject(cluster)

	s.creds.mu.Lock()
	defer s.creds.mu.Unlock()

	if p, ok := s.creds.publics[key]; ok && time.Now().Before(p.until) {
		return p.facts, nil
	}

	answer, err := s.credentialAnswer(ctx, s.Client, newCredentialRequest(cluster, ichorv1alpha1.CredentialPurposePublic))
	if err != nil {
		return nil, err
	}

	if answer.Public == nil {
		return nil, errors.New("the controller's answer carries no public facts")
	}

	if s.creds.publics == nil {
		s.creds.publics = map[types.NamespacedName]cachedPublic{}
	}

	s.creds.publics[key] = cachedPublic{facts: answer.Public, until: time.Now().Add(publicTTL)}

	return answer.Public, nil
}

var (
	placeholderMu      sync.Mutex
	placeholderBundles = map[string]*secrets.Bundle{}
)

// renderBundle is the bundle this backend renders a machine's configuration with (effective configuration,
// CNI facts). It has the cluster's real ID and CA certificates, from the controller's public facts, and
// random keys and tokens of its own: the rendering is only ever shown with every secret masked, so no real
// secret is needed, and none is readable here.
func (s *Server) renderBundle(ctx context.Context, cluster *ichorv1alpha1.TalosCluster) (*secrets.Bundle, error) {
	facts, err := s.publicFacts(ctx, cluster)
	if err != nil {
		return nil, err
	}

	contract, err := coreconfig.ParseContractFromVersion(cluster.Spec.VersionContract)
	if err != nil {
		return nil, fmt.Errorf("version contract %q: %w", cluster.Spec.VersionContract, err)
	}

	placeholderMu.Lock()

	base := placeholderBundles[cluster.Spec.VersionContract]
	if base == nil {
		base, err = secrets.NewBundle(secrets.NewClock(), contract)
		if err == nil {
			placeholderBundles[cluster.Spec.VersionContract] = base
		}
	}

	placeholderMu.Unlock()

	if err != nil {
		return nil, fmt.Errorf("making a placeholder bundle: %w", err)
	}

	// A copy: the shared base is never written.
	b := *base
	cl := *base.Cluster
	certs := *base.Certs
	b.Cluster, b.Certs = &cl, &certs

	cl.ID = facts.ClusterID

	for _, ca := range []struct {
		dst **x509.PEMEncodedCertificateAndKey
		crt string
	}{
		{&certs.OS, facts.TalosCA}, {&certs.K8s, facts.KubernetesCA},
		{&certs.Etcd, facts.EtcdCA}, {&certs.K8sAggregator, facts.KubernetesAggregatorCA},
	} {
		if ca.crt == "" || *ca.dst == nil {
			continue
		}

		repl := **ca.dst
		repl.Crt = []byte(ca.crt)
		*ca.dst = &repl
	}

	return &b, nil
}

// maskedSecrets is the client the backend renders a configuration with: every Secret it is asked for comes
// back as a placeholder. render.ResolveSystemInputs reads the registry credentials Secrets by name, which
// would make every Secret of the namespace readable by this backend; the effective configuration shows
// those credentials masked anyway. A registry Secret that does not exist is therefore not
// noticed here; the controller reports it.
type maskedSecrets struct{ client.Client }

func (m maskedSecrets) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if sec, ok := obj.(*corev1.Secret); ok {
		sec.Namespace, sec.Name = key.Namespace, key.Name
		sec.Data = map[string][]byte{"password": []byte("placeholder")}

		return nil
	}

	return m.Client.Get(ctx, key, obj, opts...)
}
