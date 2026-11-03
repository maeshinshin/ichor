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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
	"github.com/maeshinshin/ichor/pkg/ichorauth"
	"github.com/maeshinshin/ichor/pkg/render"
)

// envtest's kube-apiserver enforces RBAC, so these specs run handlers as the real ichor-ui
// ServiceAccount identity bound to the generated role (config/rbac/ichor-ui/role.yaml, from
// rbac_markers.go). A read the markers do not grant surfaces as Forbidden here instead of in a
// live cluster (the missing talosstorages rule).
var _ = Describe("ichor-ui generated RBAC", func() {
	const user = "system:serviceaccount:ichor-system:ichor-ui"

	var saClient client.Client

	BeforeEach(func() {
		raw, err := os.ReadFile("../../config/rbac/ichor-ui/role.yaml")
		Expect(err).NotTo(HaveOccurred())

		for doc := range strings.SplitSeq(string(raw), "\n---\n") {
			var meta struct{ Kind string }
			Expect(yaml.Unmarshal([]byte(doc), &meta)).To(Succeed())

			switch meta.Kind {
			case "ClusterRole":
				var cr rbacv1.ClusterRole
				Expect(yaml.Unmarshal([]byte(doc), &cr)).To(Succeed())
				cr.Name = "ichor-ui-role-test"
				Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &cr))).To(Succeed())
				Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &rbacv1.ClusterRoleBinding{
					Name:     "ichor-ui-binding-test",
					RoleRef:  rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: cr.Name},
					Subjects: []rbacv1.Subject{{APIGroup: rbacv1.GroupName, Kind: "User", Name: user}},
				}))).To(Succeed())
			case "Role":
				// The Role lives in ichor-system; the specs run with the controller namespace
				// testNamespace, so the same rules are bound there.
				var r rbacv1.Role
				Expect(yaml.Unmarshal([]byte(doc), &r)).To(Succeed())
				r.Namespace, r.Name = testNamespace, "ichor-ui-role-test"
				Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &r))).To(Succeed())
				Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &rbacv1.RoleBinding{
					Name: "ichor-ui-binding-test", Namespace: testNamespace,
					RoleRef:  rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: r.Name},
					Subjects: []rbacv1.Subject{{APIGroup: rbacv1.GroupName, Kind: "User", Name: user}},
				}))).To(Succeed())
			}
		}

		c := rest.CopyConfig(cfg)
		c.Impersonate = rest.ImpersonationConfig{UserName: user}
		saClient, err = client.New(c, client.Options{Scheme: scheme.Scheme})
		Expect(err).NotTo(HaveOccurred())
	})

	It("lets GetEffectiveConfig render, including its TalosStorage and registry-secret reads", func() {
		secret := newTestSecret(newTestBundle())
		cluster := newTestCluster(secret.Name)
		m := newTestMachine(cluster.Name, strongTestIdentity("uuid-rbac"), roleWorker, ichorv1alpha1.MachineApprovalStateApproved)

		s := &Server{Client: saClient, ControllerNamespace: testNamespace}
		_, err := s.GetEffectiveConfig(ctx, connect.NewRequest(&pb.GetEffectiveConfigRequest{
			Machine: objectRef(m.Namespace, m.Name),
		}))
		Expect(err).NotTo(HaveOccurred())
	})

	// With Ichor's issuer on the render reads ConfigMap ichor-oidc-trust (and only that one).
	It("lets GetEffectiveConfig read the trust ConfigMap when the cluster enables Ichor's issuer", func() {
		secret := newTestSecret(newTestBundle())
		cluster := newTestCluster(secret.Name)
		cluster.Spec.Authentication = &ichorv1alpha1.TalosClusterAuthentication{
			Ichor: &ichorv1alpha1.TalosClusterIchorIssuer{Enabled: true, URL: "https://ichor.example.test"},
		}
		Expect(k8sClient.Update(ctx, cluster)).To(Succeed())

		trust := &corev1.ConfigMap{
			Name: render.OIDCTrustConfigMap, Namespace: testNamespace,
			Data: map[string]string{render.OIDCTrustKeyDiscoveryURL: "https://ichor-ui.ichor-system.svc:8443", render.OIDCTrustKeyCA: testCAPEM()},
		}
		Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, trust))).To(Succeed())

		m := newTestMachine(cluster.Name, strongTestIdentity("uuid-rbac-trust"), roleControlPlane, ichorv1alpha1.MachineApprovalStateApproved)

		s := &Server{Client: saClient, ControllerNamespace: testNamespace}
		_, err := s.GetEffectiveConfig(ctx, connect.NewRequest(&pb.GetEffectiveConfigRequest{
			Machine: objectRef(m.Namespace, m.Name),
		}))
		Expect(err).NotTo(HaveOccurred())

		other := &corev1.ConfigMap{Name: "something-else", Namespace: testNamespace}
		Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, other))).To(Succeed())
		Expect(apierrors.IsForbidden(saClient.Get(ctx, client.ObjectKeyFromObject(other), &corev1.ConfigMap{}))).To(BeTrue())
	})

	It("lets GetNodeTalosRuntime read the Node and the pods bound to it", func() {
		cluster := newTestClusterWithBundle()
		m := newTestMachine(cluster.Name, strongTestIdentity("uuid-rbac-rt"), roleWorker, ichorv1alpha1.MachineApprovalStateApproved)
		m.Spec.Hostname = "node-rbac-rt"
		Expect(k8sClient.Update(ctx, m)).To(Succeed())

		node := &corev1.Node{Name: m.Spec.Hostname}
		Expect(k8sClient.Create(ctx, node)).To(Succeed())

		s := &Server{
			Client: saClient, ControllerNamespace: testNamespace,
			NodeTalosClient: func(context.Context, *ichorv1alpha1.TalosCluster, *ichorv1alpha1.TalosMachine, *TalosIdentity) (NodeTalosClient, error) {
				return &fakeNodeTalosClient{}, nil
			},
		}

		resp, err := s.GetNodeTalosRuntime(ctx, connect.NewRequest(&pb.GetNodeTalosRuntimeRequest{Machine: objectRef(m.Namespace, m.Name)}))
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Msg.GetKubernetes().GetPermissionDenied()).To(BeFalse())
		Expect(resp.Msg.GetKubernetes().GetError()).To(BeEmpty())
		Expect(resp.Msg.GetKubernetes().GetPodsError()).To(BeEmpty(), "the ServiceAccount must be allowed to list pods")
		Expect(resp.Msg.GetKubernetes().GetNodeName()).To(Equal(node.Name))
	})

	// The three downloads are never granted to the ServiceAccount by the generated role, so with
	// OIDC off a default install refuses them; the Event that audits an allowed one can still be created.
	It("refuses every diagnostics download to the ServiceAccount, and still records their Events", func() {
		cluster := newTestClusterWithBundle()
		m := newTestMachine(cluster.Name, strongTestIdentity("uuid-rbac-diag"), roleControlPlane, ichorv1alpha1.MachineApprovalStateApproved)
		s := &Server{Client: saClient, ControllerNamespace: testNamespace}

		for _, scope := range []DownloadScope{ScopeSupportBundle, ScopeEtcdSnapshot, ScopePacketCapture} {
			err := s.authorizeDownload(ctx, m, scope)
			Expect(err).To(HaveOccurred(), string(scope))
			Expect(connect.CodeOf(err)).To(Equal(connect.CodePermissionDenied), string(scope))
		}

		resp, err := s.GetMyPermissions(ctx, connect.NewRequest(&pb.GetMyPermissionsRequest{Machine: objectRef(m.Namespace, m.Name), Cluster: objectRef(m.Namespace, cluster.Name)}))
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Msg.GetSupportBundle() || resp.Msg.GetEtcdSnapshot() || resp.Msg.GetPacketCapture() || resp.Msg.GetClusterCredentials()).To(BeFalse())

		Expect(s.emitDownloadEvent(ctx, m, ScopeEtcdSnapshot, "")).To(Succeed())
	})

	// The ServiceAccount's generated role reads the rotation keys, lists machines, creates the
	// Event and updates the operation (so the acknowledgement works), but is not granted the download verb.
	It("builds and acknowledges CA rotation credentials as the ServiceAccount, and refuses their download", func() {
		contract, err := coreconfig.ParseContractFromVersion(testVersionContract)
		Expect(err).NotTo(HaveOccurred())

		rot, err := secretsbundle.NewRotation(time.Now(), contract)
		Expect(err).NotTo(HaveOccurred())

		raw, err := rot.Marshal()
		Expect(err).NotTo(HaveOccurred())

		secret := newTestSecret(newTestBundle())
		secret.Data[secretsbundle.RotationKey] = raw
		Expect(k8sClient.Update(ctx, secret)).To(Succeed())

		cluster := newTestCluster(secret.Name)
		_ = newTestMachine(cluster.Name, strongTestIdentity("uuid-rbac-cred"), roleControlPlane, ichorv1alpha1.MachineApprovalStateApproved)

		op := &ichorv1alpha1.TalosOperation{
			GenerateName: "ca-", Namespace: testNamespace,
			Spec: ichorv1alpha1.TalosOperationSpec{
				ClusterRef: corev1.LocalObjectReference{Name: cluster.Name}, Kind: ichorv1alpha1.TalosOperationKindCARotation,
			},
		}
		Expect(k8sClient.Create(ctx, op)).To(Succeed())

		op.Status.CARotation = &ichorv1alpha1.TalosOperationCARotationStatus{
			Stage:        ichorv1alpha1.CARotationStageAwaitingCredentials,
			Certificates: []ichorv1alpha1.CARotationCertificate{{Name: certTalos, Previous: "a", Next: "b"}},
		}
		Expect(k8sClient.Status().Update(ctx, op)).To(Succeed())

		s := &Server{
			Client: saClient, ControllerNamespace: testNamespace,
			VerifyKubeconfig: func(context.Context, []byte) error { return nil },
		}

		allowed, err := s.mayDownload(ctx, op.Namespace, resourceTalosClusters, CredentialsScope, op.Spec.ClusterRef.Name)
		Expect(err).NotTo(HaveOccurred())
		Expect(allowed).To(BeFalse())

		Expect(s.emitCredentialsEvent(ctx, cluster, op.Name)).To(Succeed())

		_, err = s.AcknowledgeCARotationCredentials(ctx, connect.NewRequest(&pb.AcknowledgeCARotationCredentialsRequest{Operation: objectRef(op.Namespace, op.Name)}))
		Expect(err).NotTo(HaveOccurred())
	})

	// The backend re-reads an IchorUser (get, no list) and patches one named Secret,
	// the credentials store, and nothing else.
	It("lets the backend read an IchorUser and patch only the credentials Secret", func() {
		u := &ichorv1alpha1.IchorUser{Name: "rbac-user", Namespace: testNamespace}
		Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, u))).To(Succeed())

		Expect(saClient.Get(ctx, client.ObjectKeyFromObject(u), &ichorv1alpha1.IchorUser{})).To(Succeed())
		Expect(apierrors.IsForbidden(saClient.List(ctx, &ichorv1alpha1.IchorUserList{}, client.InNamespace(testNamespace)))).To(BeTrue())

		store := &corev1.Secret{Name: ichorauth.CredentialsSecretName, Namespace: testNamespace}
		Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, store))).To(Succeed())

		s := &Server{Client: saClient, ControllerNamespace: testNamespace}
		Expect(s.writeCredential(ctx, "rbac-user", ichorauth.Credential{Version: ichorauth.CredentialVersion, Hash: "h"})).To(Succeed())

		_, found, err := s.readCredential(ctx, "rbac-user")
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())

		other := &corev1.Secret{Name: "something-else", Namespace: testNamespace}
		Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, other))).To(Succeed())
		Expect(apierrors.IsForbidden(saClient.Patch(ctx, other, client.RawPatch(types.MergePatchType, []byte(`{"data":{"a":"Yg=="}}`))))).To(BeTrue())
		// Reclaim gives the backend create on Secrets, nothing more: it cannot patch, so the line above holds.
	})
})

// testCAPEM is a throwaway self-signed CA: the render validates the PEM it is given.
func testCAPEM() string {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	Expect(err).NotTo(HaveOccurred())

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test"}, IsCA: true, BasicConstraintsValid: true,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	Expect(err).NotTo(HaveOccurred())

	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}
