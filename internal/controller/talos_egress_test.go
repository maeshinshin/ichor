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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/endpointguard"
)

// A TalosStorage makes the controller register a schematic at the cluster's Image Factory (spec.imageFactoryURL, else
// factory.talos.dev), so under the default deny a policy must open that host, and only for the controller: ichor-ui
// never dials it. A cluster with no TalosStorage never needs it.
func TestTalosEgressOpensTheImageFactoryForTheControllerOnly(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := ichorv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	cluster := func(name, factory string) *ichorv1alpha1.TalosCluster {
		c := &ichorv1alpha1.TalosCluster{Name: name, Namespace: trustNamespace}
		c.Spec.ImageFactoryURL = factory

		return c
	}
	storage := func(name, cluster string) *ichorv1alpha1.TalosStorage {
		s := &ichorv1alpha1.TalosStorage{Name: name, Namespace: trustNamespace}
		s.Spec.ClusterRef.Name = cluster

		return s
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		cluster("default", ""), cluster("custom", "https://factory.example.test:8443"), cluster("none", "https://unused.example.test"),
		storage("a", "default"), storage("b", "custom"), staticControllerPolicy(trustNamespace, "ichor")).Build()

	e := &TalosEgress{Client: c, Namespace: trustNamespace}
	if _, err := e.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}

	read := func(name string) string {
		u := cnpGVK()
		if err := c.Get(t.Context(), types.NamespacedName{Namespace: trustNamespace, Name: name}, u); err != nil {
			t.Fatal(err)
		}

		raw, _ := json.Marshal(u.Object[keySpec])

		return string(raw)
	}

	factory := read(DefaultTalosEgressPolicyName + "-image-factory")
	for _, want := range []string{`"matchName":"factory.talos.dev"`, `"matchName":"factory.example.test"`, `"port":"8443"`} {
		if !strings.Contains(factory, want) {
			t.Errorf("the factory policy lacks %s: %s", want, factory)
		}
	}

	if strings.Contains(factory, "unused.example.test") || strings.Contains(factory, "ui-backend") {
		t.Errorf("the factory policy names a cluster without storage or selects ichor-ui: %s", factory)
	}

	if main := read(DefaultTalosEgressPolicyName); strings.Contains(main, "factory") {
		t.Errorf("the policy that selects ichor-ui must not name an Image Factory: %s", main)
	}
}

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
		Expect(err).To(MatchError(apierrors.IsNotFound, "the static policy is absent, so no Talos egress rule is written"))
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
		cluster.Spec.Endpoint = ichorv1alpha1.TalosClusterEndpoint{Type: "node", Address: "egress-k8s.example.test"}
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

// A graceful reclaim from a cluster Ichor has no object for dials that cluster's endpoint and etcd
// peers, which only the node's own configuration names. The policy must allow exactly those, each on its
// own port as a /32, while the reclaim is live, and drop them when it ends.
func TestTalosEgressOpensTheForeignProbeTargetsOfALiveReclaim(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := ichorv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	mr := &ichorv1alpha1.MachineReclaim{Name: "r", Namespace: trustNamespace}
	mr.Spec.Address = "192.0.2.10"
	mr.Status.Phase = ichorv1alpha1.MachineReclaimAwaitingConfirmation
	mr.Status.Inspection = &ichorv1alpha1.MachineReclaimInspection{
		ProbeTargets: []string{testForeignEndpoint, "10.244.2.147:2380"},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(mr, staticControllerPolicy(trustNamespace, "other")).Build()
	e := &TalosEgress{Client: c, Namespace: trustNamespace}

	read := func() string {
		if _, err := e.Sync(t.Context()); err != nil {
			t.Fatal(err)
		}

		u := cnpGVK()
		if err := c.Get(t.Context(), types.NamespacedName{Namespace: trustNamespace, Name: DefaultTalosEgressPolicyName}, u); err != nil {
			t.Fatal(err)
		}

		raw, _ := json.Marshal(u.Object[keySpec])

		return string(raw)
	}

	live := read()
	for _, want := range []string{
		`"toCIDRSet":[{"cidr":"10.244.2.146/32"}],"toPorts":[{"ports":[{"port":"6443","protocol":"TCP"}]}]`,
		`"toCIDRSet":[{"cidr":"10.244.2.147/32"}],"toPorts":[{"ports":[{"port":"2380","protocol":"TCP"}]}]`,
	} {
		if !strings.Contains(live, want) {
			t.Errorf("the policy of a live reclaim lacks %s: %s", want, live)
		}
	}

	mr.Status.Phase = ichorv1alpha1.MachineReclaimDone
	if err := c.Update(t.Context(), mr); err != nil {
		t.Fatal(err)
	}

	if done := read(); strings.Contains(done, "10.244.2.146") || strings.Contains(done, "10.244.2.147") {
		t.Errorf("a finished reclaim must not keep its probe targets open: %s", done)
	}
}
