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
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/endpointguard"
)

func TestBuildEgressCoversEveryDialedPort(t *testing.T) {
	rules := buildEgress(egressTargets{
		cidrs: []string{"10.0.0.1/32"},
		names: []string{"k8s.example.com"},
		s3: []urlTarget{
			{host: "s3.example.com", port: 443}, {host: "minio.minio.svc", port: 9000}, {host: "192.0.2.9", port: 80},
		},
	})

	raw, _ := json.Marshal(rules)
	text := string(raw)

	// The ports the controller dials on a machine or cluster endpoint: apid (internal/controller
	// talosclient_*, discovery), the API server (APIServerReady, the admin kubeconfig) and etcd's
	// peer port (pkg/reclaim). Each is a Talos constant, so a change there shows up here.
	for _, p := range talosEgressPorts {
		if !strings.Contains(text, `"port":"`+strconv.Itoa(p)+`"`) {
			t.Errorf("port %d is dialed but no rule allows it: %s", p, text)
		}
	}

	for _, want := range []string{`"matchName":"k8s.example.com"`, `"matchName":"s3.example.com"`,
		`"k8sService":{"namespace":"minio","serviceName":"minio"}`, `"cidr":"192.0.2.9/32"`, `"port":"9000"`, `"port":"80"`} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %s in %s", want, text)
		}
	}

	if got := buildEgress(egressTargets{}); len(got) != 1 || len(got[0].(map[string]any)) != 0 {
		t.Errorf("no targets must give the single empty (deny) rule, got %v", got)
	}
}

// failingLister fails every List, as an API server that is down would.
type failingLister struct{ client.Client }

func (failingLister) List(context.Context, client.ObjectList, ...client.ListOption) error {
	return errors.New("api down")
}

func TestHostParsing(t *testing.T) {
	s := &targetSet{cidrs: map[string]bool{}, names: map[string]bool{}}
	for _, h := range []string{"10.0.0.5:50000", "10.0.0.5", "[fd00::1]:50000", "Node.Example.com.", "bad host!", ""} {
		s.host(h)
	}

	s.cidr("10.9.9.7/24")

	if got := slices.Sorted(maps.Keys(s.cidrs)); len(got) != 3 || got[0] != "10.0.0.5/32" || got[1] != "10.9.9.0/24" || got[2] != "fd00::1/128" {
		t.Errorf("cidrs: %v", got)
	}

	if got := slices.Sorted(maps.Keys(s.names)); len(got) != 1 || got[0] != "node.example.com" {
		t.Errorf("names: %v", got)
	}

	if st, ok := parseURLTarget("http://minio.minio.svc:9000"); !ok || st.port != 9000 || st.host != "minio.minio.svc" {
		t.Errorf("s3: %v %v", st, ok)
	}

	if st, ok := parseURLTarget("https://s3.eu-west-1.amazonaws.com"); !ok || st.port != 443 {
		t.Errorf("s3 https default: %v %v", st, ok)
	}
}

const egressNS = "egress-test-ns"

func metaOf(name string) metav1.ObjectMeta { return metav1.ObjectMeta{Name: name} }

func cnpGVK() *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(ciliumPolicyGVK)

	return u
}

// staticControllerPolicy stands in for the installer's static policy of the controller pods, without
// which the egress policies write nothing.
func staticControllerPolicy(ns, app string) *unstructured.Unstructured {
	u := cnpGVK()
	u.SetNamespace(ns)
	u.SetName("ichor-controller-manager")
	u.Object[keySpec] = map[string]any{keySelector: map[string]any{keyMatchLabels: map[string]any{
		controlPlaneLabel: valueControllerMgr, nameLabel: app,
	}}, keyEgress: []any{map[string]any{}}}

	return u
}

var _ = Describe("TalosEgress", Ordered, func() {
	var egress *TalosEgress

	BeforeAll(func() {
		ensureCiliumPolicyCRD()
		Expect(k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metaOf(egressNS)})).To(Or(Succeed(), MatchError(apierrors.IsAlreadyExists, "already exists")))

		Eventually(func() error {
			var l unstructured.UnstructuredList
			l.SetGroupVersionKind(ciliumPolicyGVK)

			return k8sClient.List(ctx, &l, client.InNamespace(egressNS))
		}, "20s", "200ms").Should(Succeed())

		egress = &TalosEgress{Client: k8sClient, Namespace: egressNS}
	})

	get := func() *unstructured.Unstructured {
		u := cnpGVK()
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: egressNS, Name: DefaultTalosEgressPolicyName}, u)).To(Succeed())

		return u
	}

	specText := func() string {
		raw, err := json.Marshal(get().Object["spec"])
		Expect(err).NotTo(HaveOccurred())

		return string(raw)
	}

	It("does nothing while Ichor's static policies are not installed", func() {
		changed, err := egress.Sync(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeFalse())

		err = k8sClient.Get(ctx, types.NamespacedName{Namespace: egressNS, Name: DefaultTalosEgressPolicyName}, cnpGVK())
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	// Every spec that needs the static policy calls this, so none depends on an earlier spec having created it.
	installStatic := func() {
		Expect(k8sClient.Create(ctx, staticControllerPolicy(egressNS, "ichor"))).To(Or(Succeed(), MatchError(apierrors.IsAlreadyExists, "already exists")))
	}

	It("does nothing when disabled even with static policies", func() {
		installStatic()

		off := &TalosEgress{Client: k8sClient, Namespace: egressNS, Disabled: true}
		changed, err := off.Sync(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeFalse())
	})

	It("writes exactly what the CRs name, and only rewrites on a change", func() {
		installStatic()

		cluster := newTestCluster("does-not-exist", "v1.14")
		cluster.Spec.Endpoint = ichorv1alpha1.TalosClusterEndpoint{Type: "loadBalancer", Address: "egress-k8s.example.test"}
		cluster.Spec.Discovery = &ichorv1alpha1.TalosClusterDiscovery{Addresses: []string{"198.51.100.7"}, CIDRs: []string{"203.0.113.0/24"}}
		Expect(k8sClient.Update(ctx, cluster)).To(Succeed())

		m := newTestMachine(cluster.Name, true)
		m.Spec.Endpoint = "198.51.100.20:50000"
		Expect(k8sClient.Update(ctx, m)).To(Succeed())

		bk := &ichorv1alpha1.TalosEtcdBackup{
			GenerateName: "egress-bk-", Namespace: tmNamespace,
			Spec: ichorv1alpha1.TalosEtcdBackupSpec{
				ClusterRef: corev1.LocalObjectReference{Name: cluster.Name},
				Destination: ichorv1alpha1.TalosEtcdBackupDestination{S3: ichorv1alpha1.TalosEtcdBackupS3{
					Endpoint: "https://egress-s3.example.test", Bucket: "egress-bucket", Prefix: "egress",
					CredentialsSecretRef: corev1.LocalObjectReference{Name: "s3"},
				}},
			},
		}
		Expect(k8sClient.Create(ctx, bk)).To(Succeed())

		changed, err := egress.Sync(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeTrue())

		text := specText()
		for _, want := range []string{`"cidr":"198.51.100.7/32"`, `"cidr":"203.0.113.0/24"`, `"cidr":"198.51.100.20/32"`,
			`"matchName":"egress-k8s.example.test"`, `"matchName":"egress-s3.example.test"`,
			`"port":"50000"`, `"port":"6443"`, `"port":"2380"`, `"control-plane"`, `"ui-backend"`} {
			Expect(text).To(ContainSubstring(want))
		}

		Expect(text).NotTo(ContainSubstring("world"))

		changed, err = egress.Sync(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeFalse(), "an unchanged set must not be rewritten")

		Expect(k8sClient.Delete(ctx, bk)).To(Succeed())

		changed, err = egress.Sync(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeTrue())
		Expect(specText()).NotTo(ContainSubstring("egress-s3.example.test"))
	})

	It("lets a newly added discovery address through before the first probe", func() {
		installStatic()

		cluster := newTestCluster("does-not-exist", "v1.14")
		cluster.Spec.Discovery = &ichorv1alpha1.TalosClusterDiscovery{Addresses: []string{"192.0.2.77"}, CIDRs: []string{"192.0.2.128/30"}}
		Expect(k8sClient.Update(ctx, cluster)).To(Succeed())

		var (
			mu     sync.Mutex
			seen   = map[string]bool{}
			missed []string
		)

		factory := func(c context.Context, endpoint string) (MaintenanceClient, error) {
			u := cnpGVK()
			err := k8sClient.Get(c, types.NamespacedName{Namespace: egressNS, Name: DefaultTalosEgressPolicyName}, u)
			raw, _ := json.Marshal(u.Object["spec"])
			host := endpointguard.HostOf(endpoint)

			mu.Lock()
			defer mu.Unlock()

			seen[host] = true

			// An address in the /30 is covered by its CIDR, the direct address by its /32.
			if err != nil || (!strings.Contains(string(raw), `"`+host+`/32"`) && !strings.Contains(string(raw), `"192.0.2.128/30"`)) {
				missed = append(missed, host)
			}

			return nil, errFactoryMustNotBeCalled
		}

		r := &TalosClusterReconciler{
			Client:    k8sClient,
			Discovery: &DiscoveryReconciler{Client: k8sClient, MaintenanceClient: factory},
			Egress:    egress,
		}

		_, err := reconcileCluster(r, cluster)
		Expect(err).NotTo(HaveOccurred())

		mu.Lock()
		defer mu.Unlock()

		Expect(seen).To(HaveKey("192.0.2.77"))
		Expect(seen).To(HaveKey("192.0.2.129"))
		Expect(missed).To(BeEmpty(), "every probe must find its address already in the policy")
	})

	It("retries a write that conflicts with the other reconciler's", func() {
		installStatic()

		// The write under test is an Update, so the policy must already exist.
		_, err := egress.Sync(ctx)
		Expect(err).NotTo(HaveOccurred())

		cluster := newTestCluster("does-not-exist", "v1.14")
		cluster.Spec.Discovery = &ichorv1alpha1.TalosClusterDiscovery{Addresses: []string{"192.0.2.201"}}
		Expect(k8sClient.Update(ctx, cluster)).To(Succeed())

		c := &conflictOnce{Client: k8sClient}
		racing := &TalosEgress{Client: c, Namespace: egressNS}

		changed, err := racing.Sync(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeTrue())
		Expect(c.n).To(Equal(2))
		Expect(specText()).To(ContainSubstring("192.0.2.201/32"))
	})

	It("requeues soon while a new address finds nothing, and at the normal interval after the window", func() {
		installStatic()

		cluster := newTestCluster("does-not-exist", "v1.14")
		cluster.Spec.Discovery = &ichorv1alpha1.TalosClusterDiscovery{Addresses: []string{"192.0.2.202"}}
		Expect(k8sClient.Update(ctx, cluster)).To(Succeed())

		now := time.Now()
		r := &TalosClusterReconciler{
			Client: k8sClient, Egress: egress, now: func() time.Time { return now },
			Discovery: &DiscoveryReconciler{Client: k8sClient, MaintenanceClient: func(context.Context, string) (MaintenanceClient, error) {
				return nil, errFactoryMustNotBeCalled
			}},
		}

		res, err := reconcileCluster(r, cluster)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(Equal(discoveryFreshInterval))

		now = now.Add(discoveryFreshWindow + time.Second)

		res, err = reconcileCluster(r, cluster)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(Equal(discoveryRequeueInterval))
	})

	It("does not probe when the policy cannot be written", func() {
		cluster := newTestCluster("does-not-exist", "v1.14")
		cluster.Spec.Discovery = &ichorv1alpha1.TalosClusterDiscovery{Addresses: []string{"192.0.2.88"}}
		Expect(k8sClient.Update(ctx, cluster)).To(Succeed())

		broken := &TalosEgress{Client: failingLister{k8sClient}, Namespace: egressNS}

		var calls int
		r := &TalosClusterReconciler{
			Client: k8sClient, Egress: broken,
			Discovery: &DiscoveryReconciler{Client: k8sClient, MaintenanceClient: func(context.Context, string) (MaintenanceClient, error) {
				calls++

				return nil, errFactoryMustNotBeCalled
			}},
		}

		_, err := reconcileCluster(r, cluster)
		Expect(err).To(HaveOccurred())
		Expect(calls).To(BeZero())
	})
})
