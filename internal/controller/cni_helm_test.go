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
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/version"
	"github.com/maeshinshin/ichor/pkg/cni"
)

const cniTestName = "home"

func cniTestInstallation() *ichorv1alpha1.IchorInstallation {
	inst := &ichorv1alpha1.IchorInstallation{Name: ichorv1alpha1.IchorInstallationName}
	inst.Spec.Version = "v0.9.0"
	inst.Spec.InstallerRepository = "ghcr.io/maeshinshin/ichor-installer"

	return inst
}

// specMode is the kube-proxy mode cluster's spec names: what a test that has no cilium-config to
// read stands in for the mode Cilium runs, and what a switch's target is.
func specMode(c *ichorv1alpha1.TalosCluster) cni.KubeProxyMode {
	return cni.KubeProxyMode(c.Spec.CNI.KubeProxy)
}

func cniTestCluster(values string) *ichorv1alpha1.TalosCluster {
	c := &ichorv1alpha1.TalosCluster{Namespace: "clusters", Name: cniTestName}
	c.Spec.CNI.Preset = ciliumName

	if values != "" {
		c.Spec.CNI.Cilium = &ichorv1alpha1.TalosClusterCNICilium{Values: &apiextensionsv1.JSON{Raw: []byte(values)}}
	}

	return c
}

func TestCNIHelmJobShape(t *testing.T) {
	cluster := cniTestCluster("")

	hash, err := CNIValuesHash(cluster, specMode(cluster), 0)
	if err != nil {
		t.Fatal(err)
	}

	job := newCNIHelmJob("ichor-system", cniHelmJobName(cluster, hash), cluster, cni.KubeProxyEnabled, cniTestInstallation(), hash, false, 0)
	pod := job.Spec.Template.Spec

	// kube-system: hostNetwork is refused by Talos's default Pod Security admission (baseline) in
	// any other namespace; the release record is stored in the controller's namespace.
	if job.Namespace != kubeSystemNamespace {
		t.Errorf("Job namespace = %q, want kube-system (Talos exempts it from Pod Security admission)", job.Namespace)
	}

	if pod.ServiceAccountName != "ichor-cni" {
		t.Errorf("ServiceAccountName = %q, want ichor-cni (config/rbac/ichor-cni)", pod.ServiceAccountName)
	}

	if !pod.HostNetwork || pod.DNSPolicy != corev1.DNSClusterFirstWithHostNet {
		t.Errorf("hostNetwork=%t dnsPolicy=%s, want the host network with ClusterFirstWithHostNet", pod.HostNetwork, pod.DNSPolicy)
	}

	c := pod.Containers[0]
	if c.Image != "ghcr.io/maeshinshin/ichor-installer:v0.9.0" {
		t.Errorf("image = %q, want the installer image at spec.version", c.Image)
	}

	if want := []string{"cni", "helm", "apply", "--cluster=home", "--namespace=clusters", "--kube-proxy=enabled", "--storage-namespace=ichor-system", "--exclusive"}; !slices.Equal(c.Args, want) {
		t.Errorf("args = %v, want %v", c.Args, want)
	}

	env := map[string]string{}
	for _, e := range c.Env {
		env[e.Name] = e.Value
	}

	if env["KUBERNETES_SERVICE_HOST"] != "localhost" || env["KUBERNETES_SERVICE_PORT"] != "7445" {
		t.Errorf("API server endpoint = %s:%s, want KubePrism localhost:7445", env["KUBERNETES_SERVICE_HOST"], env["KUBERNETES_SERVICE_PORT"])
	}

	if *job.Spec.BackoffLimit != 0 || *job.Spec.TTLSecondsAfterFinished != jobTTL {
		t.Errorf("backoffLimit=%d ttl=%d, want 0 and %d", *job.Spec.BackoffLimit, *job.Spec.TTLSecondsAfterFinished, jobTTL)
	}

	if len(pod.Tolerations) != 1 || pod.Tolerations[0].Operator != corev1.TolerationOpExists {
		t.Errorf("tolerations = %v, want to tolerate every taint", pod.Tolerations)
	}

	if job.Annotations[CNIValuesHashAnnotation] != hash {
		t.Errorf("values hash annotation = %q, want %q", job.Annotations[CNIValuesHashAnnotation], hash)
	}
}

// The pod keeps a name label, but no Cilium policy selects it: it runs on the host network, which
// CiliumNetworkPolicy does not govern (ADR 0100).
func TestCNIHelmJobHasNoNetworkPolicy(t *testing.T) {
	policies, err := os.ReadFile("../../config/cilium-network-policy/policies.yaml")
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(policies), "ichor-cni") {
		t.Error("config/cilium-network-policy selects the ichor-cni pod, which runs on the host network and is not policed")
	}
}

func TestCNIValuesHashFollowsTheEffectiveValues(t *testing.T) {
	base, err := CNIValuesHash(cniTestCluster(""), "", 0)
	if err != nil {
		t.Fatal(err)
	}

	same, _ := CNIValuesHash(cniTestCluster("{}"), "", 0)
	if same != base {
		t.Error("empty user values changed the hash")
	}

	changed, err := CNIValuesHash(cniTestCluster(`{"debug":{"enabled":true}}`), "", 0)
	if err != nil || changed == base {
		t.Errorf("a user value did not change the hash (err %v)", err)
	}

	if _, err := CNIValuesHash(cniTestCluster(`{"ipam":{"mode":"x"}}`), "", 0); err == nil {
		t.Error("an owned key was hashed instead of refused")
	}
}

// The operator's replicas follow the Nodes the cluster has, so the hash does too: an HA cluster's
// first Node and its second hash differently, its second and third do not, and the Job is handed the
// number it was hashed with (ADR 0113 amendment 2026-11-04).
func TestCNIValuesHashFollowsTheNodeCount(t *testing.T) {
	ha := cniTestCluster("")
	ha.Spec.ControlPlane.Count = 3

	hash := func(c *ichorv1alpha1.TalosCluster, nodes int) string {
		h, err := CNIValuesHash(c, "", nodes)
		if err != nil {
			t.Fatal(err)
		}

		return h
	}

	if hash(ha, 1) == hash(ha, 2) {
		t.Error("an HA cluster's first and second Node hash the same: the operator would stay at one replica")
	}

	if hash(ha, 2) != hash(ha, 3) || hash(ha, 2) != hash(ha, 0) {
		t.Error("two or more Nodes, or an unknown count, changed the hash of an HA cluster")
	}

	single := cniTestCluster("")
	single.Spec.ControlPlane.Count = 1

	if hash(single, 1) != hash(single, 4) || hash(single, 1) != hash(single, 0) {
		t.Error("a one-control-plane cluster's hash follows its Node count; it stays at one replica")
	}

	job := func(nodes int) string {
		j := newCNIHelmJob("ichor-system", "j", ha, cni.KubeProxyEnabled, cniTestInstallation(), "h", false, nodes)

		return strings.Join(j.Spec.Template.Spec.Containers[0].Args, " ")
	}

	if !strings.Contains(job(1), "--nodes=1") {
		t.Errorf("the Job's args %q do not carry the node count", job(1))
	}

	if strings.Contains(job(0), "--nodes") {
		t.Errorf("the Job's args %q carry an unknown node count", job(0))
	}
}

// A Helm Job that keeps failing with the same values waits twice as long after each failure, up to 30
// minutes, so it does not hold the cluster operation lock in a near-continuous loop (ADR 0113 amendment
// 2026-11-04).
func TestCNIRetryBackoffDoublesUpToACap(t *testing.T) {
	for _, tc := range []struct {
		failures int32
		want     time.Duration
	}{
		{0, time.Minute}, // no failure counted yet: the base interval
		{1, time.Minute},
		{2, 2 * time.Minute},
		{3, 4 * time.Minute},
		{5, 16 * time.Minute},
		{6, 30 * time.Minute},
		{7, 30 * time.Minute},
		{1000, 30 * time.Minute}, // no overflow
	} {
		if got := cniRetryBackoff(time.Minute, tc.failures); got != tc.want {
			t.Errorf("after %d failures: wait %s, want %s", tc.failures, got, tc.want)
		}
	}

	if got := cniRetryBackoff(time.Nanosecond, 3); got != 4*time.Nanosecond {
		t.Errorf("a test's tiny interval doubles too: got %s", got)
	}
}

func TestClusterNodeCountCountsEveryNode(t *testing.T) {
	notReady := &corev1.Node{Name: "a"}
	ready := &corev1.Node{Name: "b", Status: corev1.NodeStatus{
		Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}}}

	got, err := clusterNodeCount(t.Context(), cniFakeClient(t, notReady, ready))
	if err != nil || got != 2 {
		t.Fatalf("clusterNodeCount = %d, %v; want both Nodes, Ready or not", got, err)
	}

	if got, err := clusterNodeCount(t.Context(), cniFakeClient(t)); err != nil || got != 0 {
		t.Fatalf("clusterNodeCount with no Nodes = %d, %v; want 0", got, err)
	}
}

// A Node coming or going re-checks every cluster's Cilium values.
func TestANodeEventEnqueuesEveryCluster(t *testing.T) {
	a, b := cniTestCluster(""), cniTestCluster("")
	b.Namespace, b.Name = "other", "second"

	r := &CNIHandoverReconciler{Client: cniFakeClient(t, a, b)}

	got := r.allClusters(t.Context(), &corev1.Node{})
	if len(got) != 2 {
		t.Fatalf("allClusters = %v, want both clusters", got)
	}
}

func cniFakeClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := batchv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	if err := ichorv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).WithStatusSubresource(&batchv1.Job{}).Build()
}

func setCNIJobCondition(t *testing.T, c client.Client, ns, name string, ct batchv1.JobConditionType) {
	t.Helper()

	var job batchv1.Job
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &job); err != nil {
		t.Fatal(err)
	}

	job.Status.Conditions = []batchv1.JobCondition{{Type: ct, Status: corev1.ConditionTrue, Reason: "Test"}}
	if err := c.Status().Update(context.Background(), &job); err != nil {
		t.Fatal(err)
	}
}

// First call creates the Job, later calls report it, and a values change is a new Job.
func TestAdvanceCNIHelmJob(t *testing.T) {
	ctx := context.Background()
	c := cniFakeClient(t)
	inst := cniTestInstallation()
	cluster := cniTestCluster("")

	hash, _ := CNIValuesHash(cluster, specMode(cluster), 0)
	name := cniHelmJobName(cluster, hash)

	if done, err := AdvanceCNIHelmJob(ctx, c, "ichor-system", cluster, specMode(cluster), 0, inst, "", time.Time{}); done || err != nil {
		t.Fatalf("first call = %t, %v; want it to create the Job and not be done", done, err)
	}

	if done, err := AdvanceCNIHelmJob(ctx, c, "ichor-system", cluster, specMode(cluster), 0, inst, "", time.Time{}); done || err != nil {
		t.Fatalf("running Job = %t, %v; want not done, no error", done, err)
	}

	setCNIJobCondition(t, c, kubeSystemNamespace, name, batchv1.JobComplete)

	if done, err := AdvanceCNIHelmJob(ctx, c, "ichor-system", cluster, specMode(cluster), 0, inst, "", time.Time{}); !done || err != nil {
		t.Fatalf("completed Job = %t, %v; want done", done, err)
	}

	// Other values: the completed Job proves nothing for them, a new Job is created.
	edited := cniTestCluster(`{"debug":{"enabled":true}}`)
	if done, err := AdvanceCNIHelmJob(ctx, c, "ichor-system", edited, specMode(edited), 0, inst, "", time.Time{}); done || err != nil {
		t.Fatalf("edited values = %t, %v; want a new Job and not done", done, err)
	}

	var jobs batchv1.JobList
	if err := c.List(ctx, &jobs); err != nil || len(jobs.Items) != 2 {
		t.Fatalf("%d Jobs (%v), want 2", len(jobs.Items), err)
	}
}

func TestAdvanceCNIHelmJobReportsAFailedJob(t *testing.T) {
	ctx := context.Background()
	c := cniFakeClient(t)
	cluster := cniTestCluster("")

	hash, _ := CNIValuesHash(cluster, specMode(cluster), 0)

	if _, err := AdvanceCNIHelmJob(ctx, c, "ichor-system", cluster, specMode(cluster), 0, cniTestInstallation(), "", time.Time{}); err != nil {
		t.Fatal(err)
	}

	setCNIJobCondition(t, c, kubeSystemNamespace, cniHelmJobName(cluster, hash), batchv1.JobFailed)

	_, err := AdvanceCNIHelmJob(ctx, c, "ichor-system", cluster, specMode(cluster), 0, cniTestInstallation(), "", time.Time{})
	if err == nil || !strings.Contains(err.Error(), "failed") {
		t.Fatalf("error = %v, want the Job's failure", err)
	}
}

// A Job that never got a pod (admission refused it) is reported and removed once the grace period
// is over, instead of holding the caller's lock until the deadline.
func TestAdvanceCNIHelmJobFailsFastWhenNoPodIsCreated(t *testing.T) {
	ctx := context.Background()
	c := cniFakeClient(t)
	cluster := cniTestCluster("")

	hash, _ := CNIValuesHash(cluster, specMode(cluster), 0)
	name := cniHelmJobName(cluster, hash)

	if _, err := AdvanceCNIHelmJob(ctx, c, "ichor-system", cluster, specMode(cluster), 0, cniTestInstallation(), "", time.Time{}); err != nil {
		t.Fatal(err)
	}

	var job batchv1.Job
	if err := c.Get(ctx, types.NamespacedName{Namespace: kubeSystemNamespace, Name: name}, &job); err != nil {
		t.Fatal(err)
	}

	// Within the grace period a Job without a pod is just starting.
	if done, err := AdvanceCNIHelmJob(ctx, c, "ichor-system", cluster, specMode(cluster), 0, cniTestInstallation(), "", time.Time{}); done || err != nil {
		t.Fatalf("young Job = %t, %v; want not done, no error", done, err)
	}

	// Age it past the grace period.
	job.CreationTimestamp = metav1.NewTime(time.Now().Add(-cniPodGrace - time.Second))
	if err := c.Update(ctx, &job); err != nil {
		t.Fatal(err)
	}

	_, err := AdvanceCNIHelmJob(ctx, c, "ichor-system", cluster, specMode(cluster), 0, cniTestInstallation(), "", time.Time{})

	var failed *CNIHelmJobFailedError
	if !errors.As(err, &failed) || !failed.NoPod || failed.JobName != name {
		t.Fatalf("error = %v, want a NoPod failure of %s", err, name)
	}

	if err := c.Get(ctx, types.NamespacedName{Namespace: kubeSystemNamespace, Name: name}, &job); !apierrors.IsNotFound(err) {
		t.Fatalf("the stuck Job was not deleted (get: %v)", err)
	}

	// The next pass starts a fresh Job.
	if done, err := AdvanceCNIHelmJob(ctx, c, "ichor-system", cluster, specMode(cluster), 0, cniTestInstallation(), "", time.Time{}); done || err != nil {
		t.Fatalf("after removal = %t, %v; want a new Job and not done", done, err)
	}
}

// A Helm-installed Ichor has no IchorInstallation (ADR 0104 4.3); the Cilium Helm Job then runs
// the installer image of the controller's own version.
func TestInstallerSourceWithoutAnIchorInstallation(t *testing.T) {
	ctx := context.Background()

	got, err := installerSource(ctx, cniFakeClient(t))
	if err != nil {
		t.Fatalf("installerSource without an IchorInstallation: %v", err)
	}

	if got.Spec.InstallerRepository != defaultInstallerRepository || got.Spec.Version != version.Version {
		t.Fatalf("stand-in = %+v; want the default repository at version %q", got.Spec, version.Version)
	}

	job := newCNIHelmJob("ichor-system", "j", cniTestCluster(""), cni.KubeProxyEnabled, got, "h", false, 0)
	if img := job.Spec.Template.Spec.Containers[0].Image; img != defaultInstallerRepository+":"+version.Version {
		t.Fatalf("Job image = %q", img)
	}

	real := cniTestInstallation()

	got, err = installerSource(ctx, cniFakeClient(t, real))
	if err != nil || got.Spec.Version != "v0.9.0" {
		t.Fatalf("with an IchorInstallation = %+v, %v; want its own spec", got, err)
	}
}
