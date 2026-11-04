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
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	certv1 "k8s.io/api/certificates/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// servingFixture is an enabled cluster with one approved worker named servingNode, on envtest.
type servingFixture struct {
	cluster  *ichorv1alpha1.TalosCluster
	machine  *ichorv1alpha1.TalosMachine
	identity ServingIdentity
	idErr    error
	idCalls  int
	rec      *record.FakeRecorder
	r        *KubeletServingReconciler
}

func newServingFixture(verify bool) *servingFixture {
	secret := newTestSecret(newTestBundle("v1.14"))

	spec := newClusterSpec(secret.Name, "v1.14")
	spec.Security = &ichorv1alpha1.TalosClusterSecurity{KubeletServingCertificates: &ichorv1alpha1.TalosClusterKubeletServing{
		Enabled: true, VerifyFromAPIServer: verify,
	}}

	cluster := &ichorv1alpha1.TalosCluster{GenerateName: testClusterGenerate, Namespace: tmNamespace, Spec: spec}
	Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

	m := newTestMachine(cluster.Name, true)
	m.Spec.Hostname = servingNode
	Expect(k8sClient.Update(ctx, m)).To(Succeed())

	f := &servingFixture{cluster: cluster, machine: m, identity: servingIdentityOf(servingIP), rec: record.NewFakeRecorder(32)}
	f.r = &KubeletServingReconciler{
		Client: k8sClient, ControllerNamespace: tmNamespace, Recorder: f.rec,
		Identity: func(context.Context, *ichorv1alpha1.TalosCluster, *ichorv1alpha1.TalosMachine, *secrets.Bundle) (ServingIdentity, error) {
			f.idCalls++

			return f.identity, f.idErr
		},
	}

	DeferCleanup(func() {
		_ = k8sClient.Delete(ctx, cluster)
		_ = k8sClient.Delete(ctx, m)
	})

	return f
}

func (f *servingFixture) reconcile() ctrl.Result {
	res, err := f.r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(f.cluster)})
	Expect(err).NotTo(HaveOccurred())

	return res
}

func (f *servingFixture) clusterNow() *ichorv1alpha1.TalosCluster {
	var c ichorv1alpha1.TalosCluster
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(f.cluster), &c)).To(Succeed())

	return &c
}

// kubeletClient is a client that the apiserver sees as the kubelet of node (so spec.username and
// spec.groups of what it creates are the real thing); admin is the envtest superuser.
func kubeletClient(node string) client.Client {
	c := rest.CopyConfig(cfg)
	c.Impersonate = rest.ImpersonationConfig{UserName: nodeUserPrefix + node, Groups: []string{nodesGroup, authenticatedGroup}}

	kc, err := client.New(c, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())

	return kc
}

// createServingCSR creates the request as who (nil is the admin) and removes it afterwards.
func createServingCSR(who client.Client, s servingCSR) *certv1.CertificateSigningRequest {
	t := GinkgoT()
	csr := s.build(t)
	csr.Name, csr.GenerateName = "", "serving-"
	csr.Spec.Username, csr.Spec.Groups = "", nil // the apiserver sets both from the caller

	if who == nil {
		who = k8sClient
	}

	Expect(who.Create(ctx, csr)).To(Succeed())
	DeferCleanup(func() { _ = k8sClient.Delete(ctx, csr) })

	return csr
}

func csrNow(csr *certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest {
	var got certv1.CertificateSigningRequest
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: csr.Name}, &got)).To(Succeed())

	return &got
}

func csrCondition(csr *certv1.CertificateSigningRequest, ct certv1.RequestConditionType) *certv1.CertificateSigningRequestCondition {
	for i := range csr.Status.Conditions {
		if csr.Status.Conditions[i].Type == ct {
			return &csr.Status.Conditions[i]
		}
	}

	return nil
}

// issue stands in for kube-controller-manager's signer: it writes a certificate on an approved request.
func issue(csr *certv1.CertificateSigningRequest, notAfter time.Time) {
	got := csrNow(csr)
	got.Status.Certificate = servingCert(GinkgoT(), metav1.NewTime(time.Now().Add(-time.Minute)), metav1.NewTime(notAfter))
	Expect(k8sClient.SubResource("status").Update(ctx, got)).To(Succeed())
}

var _ = Describe("KubeletServingReconciler", func() {
	BeforeEach(func() {
		// Impersonated kubelets are not in envtest's RBAC; let the node group create requests.
		ensureNodeCSRRBAC()
	})

	It("approves the node's own request, records the issued certificate and reports the cluster covered", func() {
		f := newServingFixture(true)
		csr := createServingCSR(kubeletClient(servingNode), servingCSR{})

		res := f.reconcile()
		Expect(res.RequeueAfter).To(Equal(kubeletServingPending))

		approved := csrCondition(csrNow(csr), certv1.CertificateApproved)
		Expect(approved).NotTo(BeNil())
		Expect(approved.Reason).To(Equal(reasonKubeletServingApproved))
		Expect(f.rec.Events).To(Receive(ContainSubstring("KubeletServingApproved")))

		cond := apimeta.FindStatusCondition(f.clusterNow().Status.Conditions, ichorv1alpha1.ConditionKubeletServingCertificates)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Message).To(ContainSubstring(f.machine.Name))
		Expect(apimeta.IsStatusConditionTrue(f.clusterNow().Status.Conditions, ichorv1alpha1.ConditionKubeletServingVerified)).To(BeFalse())

		issue(csr, time.Now().Add(24*time.Hour))
		res = f.reconcile()
		Expect(res.RequeueAfter).To(Equal(kubeletServingSteady))

		got := fetchMachine(f.machine)
		Expect(got.Status.KubeletServingCertificate).NotTo(BeNil())
		Expect(got.Status.KubeletServingCertificate.Request).To(Equal(csr.Name))

		conds := f.clusterNow().Status.Conditions
		Expect(apimeta.IsStatusConditionTrue(conds, ichorv1alpha1.ConditionKubeletServingCertificates)).To(BeTrue())
		Expect(apimeta.IsStatusConditionTrue(conds, ichorv1alpha1.ConditionKubeletServingVerified)).To(BeTrue())
	})

	It("denies a forged request from someone who is not the node, with the reason", func() {
		f := newServingFixture(false)
		forged := createServingCSR(nil, servingCSR{}) // the admin asks for the node's identity

		f.reconcile()

		denied := csrCondition(csrNow(forged), certv1.CertificateDenied)
		Expect(denied).NotTo(BeNil())
		Expect(denied.Reason).To(Equal(reasonKubeletServingDenied))
		Expect(denied.Message).To(ContainSubstring("is not the node"))
		Expect(csrCondition(csrNow(forged), certv1.CertificateApproved)).To(BeNil())
		Expect(f.idCalls).To(BeZero(), "a request that fails on its face never reaches the node")
		Expect(f.rec.Events).To(Receive(ContainSubstring("KubeletServingDenied")))
	})

	It("denies the node's own request when it names an address the node does not own", func() {
		f := newServingFixture(false)
		csr := createServingCSR(kubeletClient(servingNode), servingCSR{ips: []string{"10.9.9.9"}})

		f.reconcile()

		denied := csrCondition(csrNow(csr), certv1.CertificateDenied)
		Expect(denied).NotTo(BeNil())
		Expect(denied.Message).To(ContainSubstring("10.9.9.9"))
	})

	It("denies a request with the wrong usages", func() {
		f := newServingFixture(false)
		csr := createServingCSR(kubeletClient(servingNode), servingCSR{
			usages: []certv1.KeyUsage{certv1.UsageDigitalSignature, certv1.UsageServerAuth, certv1.UsageClientAuth},
		})

		f.reconcile()

		denied := csrCondition(csrNow(csr), certv1.CertificateDenied)
		Expect(denied).NotTo(BeNil())
		Expect(denied.Message).To(ContainSubstring(denyUsages))
	})

	It("leaves a request pending when the node cannot be read, then approves it once it can", func() {
		f := newServingFixture(false)
		f.idErr = errors.New("node unreachable")
		csr := createServingCSR(kubeletClient(servingNode), servingCSR{})

		f.reconcile()
		Expect(csrNow(csr).Status.Conditions).To(BeEmpty())

		f.idErr = nil
		f.reconcile()
		Expect(csrCondition(csrNow(csr), certv1.CertificateApproved)).NotTo(BeNil())
	})

	It("leaves a request for a node no machine of the cluster has pending, never denied", func() {
		f := newServingFixture(false)
		csr := createServingCSR(kubeletClient("node-z"), servingCSR{cn: nodeUserPrefix + "node-z", dns: []string{"node-z"}, ips: []string{"10.0.0.50"}})

		f.reconcile()
		Expect(csrNow(csr).Status.Conditions).To(BeEmpty())
		Expect(f.idCalls).To(BeZero())
	})

	It("never touches another signer's request", func() {
		f := newServingFixture(false)
		csr := createServingCSR(kubeletClient(servingNode), servingCSR{signer: "example.com/other"})

		f.reconcile()
		Expect(csrNow(csr).Status.Conditions).To(BeEmpty())
	})

	It("does nothing when the option is off and removes the conditions", func() {
		f := newServingFixture(false)
		csr := createServingCSR(kubeletClient(servingNode), servingCSR{})
		f.reconcile()
		Expect(apimeta.FindStatusCondition(f.clusterNow().Status.Conditions, ichorv1alpha1.ConditionKubeletServingCertificates)).NotTo(BeNil())

		off := f.clusterNow()
		off.Spec.Security.KubeletServingCertificates.Enabled = false
		Expect(k8sClient.Update(ctx, off)).To(Succeed())

		// A second request while off stays untouched.
		second := createServingCSR(kubeletClient(servingNode), servingCSR{})
		f.reconcile()

		Expect(csrNow(second).Status.Conditions).To(BeEmpty())
		Expect(apimeta.FindStatusCondition(f.clusterNow().Status.Conditions, ichorv1alpha1.ConditionKubeletServingCertificates)).To(BeNil())
		Expect(csrCondition(csrNow(csr), certv1.CertificateApproved)).NotTo(BeNil(), "what was decided stays decided")
	})

	It("keeps the latch when a machine joins afterwards", func() {
		f := newServingFixture(true)
		csr := createServingCSR(kubeletClient(servingNode), servingCSR{})
		f.reconcile()
		issue(csr, time.Now().Add(24*time.Hour))
		f.reconcile()
		Expect(apimeta.IsStatusConditionTrue(f.clusterNow().Status.Conditions, ichorv1alpha1.ConditionKubeletServingVerified)).To(BeTrue())

		joined := newTestMachine(f.cluster.Name, true)
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, joined) })
		f.reconcile()

		conds := f.clusterNow().Status.Conditions
		Expect(apimeta.IsStatusConditionTrue(conds, ichorv1alpha1.ConditionKubeletServingCertificates)).To(BeFalse())
		Expect(apimeta.IsStatusConditionTrue(conds, ichorv1alpha1.ConditionKubeletServingVerified)).To(BeTrue())
	})
})

// ensureNodeCSRRBAC lets the group system:nodes create CertificateSigningRequests: envtest's
// apiserver enforces RBAC on the impersonated kubelets, and a real cluster's Node authorizer does
// the same job.
func ensureNodeCSRRBAC() {
	GinkgoHelper()

	const name = "ichor-test-nodes-create-csr"

	role := &rbacv1.ClusterRole{
		Name: name,
		Rules: []rbacv1.PolicyRule{{
			APIGroups: []string{"certificates.k8s.io"}, Resources: []string{"certificatesigningrequests"}, Verbs: []string{"create", "get"},
		}},
	}
	binding := &rbacv1.ClusterRoleBinding{
		Name:     name,
		RoleRef:  rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: name},
		Subjects: []rbacv1.Subject{{APIGroup: rbacv1.GroupName, Kind: rbacv1.GroupKind, Name: nodesGroup}},
	}

	for _, o := range []client.Object{role, binding} {
		if err := k8sClient.Create(ctx, o); err != nil {
			Expect(apierrors.IsAlreadyExists(err)).To(BeTrue(), "creating %T: %v", o, err)
		}
	}
}
