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
	"os"
	"slices"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

func cniTestInstallation() *ichorv1alpha1.IchorInstallation {
	inst := &ichorv1alpha1.IchorInstallation{ObjectMeta: metav1.ObjectMeta{Name: ichorv1alpha1.IchorInstallationName}}
	inst.Spec.Version = "v0.9.0"
	inst.Spec.InstallerRepository = "ghcr.io/maeshinshin/ichor-installer"

	return inst
}

func cniTestCluster(values string) *ichorv1alpha1.TalosCluster {
	c := &ichorv1alpha1.TalosCluster{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "home"}}
	c.Spec.CNI.Preset = "cilium"

	if values != "" {
		c.Spec.CNI.Cilium = &ichorv1alpha1.TalosClusterCNICilium{Values: &apiextensionsv1.JSON{Raw: []byte(values)}}
	}

	return c
}

func TestCNIHelmJobShape(t *testing.T) {
	cluster := cniTestCluster("")

	hash, err := CNIValuesHash(cluster)
	if err != nil {
		t.Fatal(err)
	}

	job := newCNIHelmJob("ichor-system", cniHelmJobName(cluster, hash), cluster, cniTestInstallation(), hash)
	pod := job.Spec.Template.Spec

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

	if want := []string{"cni", "helm", "apply", "--cluster=home", "--namespace=clusters", "--storage-namespace=ichor-system"}; !slices.Equal(c.Args, want) {
		t.Errorf("args = %v, want %v", c.Args, want)
	}

	env := map[string]string{}
	for _, e := range c.Env {
		env[e.Name] = e.Value
	}

	if env["KUBERNETES_SERVICE_HOST"] != "localhost" || env["KUBERNETES_SERVICE_PORT"] != "7445" {
		t.Errorf("API server endpoint = %s:%s, want KubePrism localhost:7445", env["KUBERNETES_SERVICE_HOST"], env["KUBERNETES_SERVICE_PORT"])
	}

	if *job.Spec.BackoffLimit != 0 || *job.Spec.TTLSecondsAfterFinished != cniHelmJobTTL {
		t.Errorf("backoffLimit=%d ttl=%d, want 0 and %d", *job.Spec.BackoffLimit, *job.Spec.TTLSecondsAfterFinished, cniHelmJobTTL)
	}

	if len(pod.Tolerations) != 1 || pod.Tolerations[0].Operator != corev1.TolerationOpExists {
		t.Errorf("tolerations = %v, want to tolerate every taint", pod.Tolerations)
	}

	if job.Annotations[CNIValuesHashAnnotation] != hash {
		t.Errorf("values hash annotation = %q, want %q", job.Annotations[CNIValuesHashAnnotation], hash)
	}
}

// The network policies select the Job's pod by this label; without it the default
// deny would cut the Job off from the API server. The label differs from the installer's.
func TestCNIHelmJobPodCarriesTheLabelItsNetworkPolicySelects(t *testing.T) {
	cluster := cniTestCluster("")
	job := newCNIHelmJob("ichor-system", "x", cluster, cniTestInstallation(), "h")

	got := job.Spec.Template.Labels["app.kubernetes.io/name"]
	if got != "ichor-cni" {
		t.Errorf("pod label app.kubernetes.io/name = %q, want ichor-cni", got)
	}

	policies, err := os.ReadFile("../../config/cilium-network-policy/policies.yaml")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(policies), "app.kubernetes.io/name: "+got) {
		t.Errorf("config/cilium-network-policy selects no pod labelled %s", got)
	}
}

func TestCNIValuesHashFollowsTheEffectiveValues(t *testing.T) {
	base, err := CNIValuesHash(cniTestCluster(""))
	if err != nil {
		t.Fatal(err)
	}

	same, _ := CNIValuesHash(cniTestCluster("{}"))
	if same != base {
		t.Error("empty user values changed the hash")
	}

	changed, err := CNIValuesHash(cniTestCluster(`{"debug":{"enabled":true}}`))
	if err != nil || changed == base {
		t.Errorf("a user value did not change the hash (err %v)", err)
	}

	if _, err := CNIValuesHash(cniTestCluster(`{"ipam":{"mode":"x"}}`)); err == nil {
		t.Error("an owned key was hashed instead of refused")
	}
}

func cniFakeClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := batchv1.AddToScheme(scheme); err != nil {
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

	hash, _ := CNIValuesHash(cluster)
	name := cniHelmJobName(cluster, hash)

	if done, err := AdvanceCNIHelmJob(ctx, c, "ichor-system", cluster, inst); done || err != nil {
		t.Fatalf("first call = %t, %v; want it to create the Job and not be done", done, err)
	}

	if done, err := AdvanceCNIHelmJob(ctx, c, "ichor-system", cluster, inst); done || err != nil {
		t.Fatalf("running Job = %t, %v; want not done, no error", done, err)
	}

	setCNIJobCondition(t, c, "ichor-system", name, batchv1.JobComplete)

	if done, err := AdvanceCNIHelmJob(ctx, c, "ichor-system", cluster, inst); !done || err != nil {
		t.Fatalf("completed Job = %t, %v; want done", done, err)
	}

	// Other values: the completed Job proves nothing for them, a new Job is created.
	edited := cniTestCluster(`{"debug":{"enabled":true}}`)
	if done, err := AdvanceCNIHelmJob(ctx, c, "ichor-system", edited, inst); done || err != nil {
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

	hash, _ := CNIValuesHash(cluster)

	if _, err := AdvanceCNIHelmJob(ctx, c, "ichor-system", cluster, cniTestInstallation()); err != nil {
		t.Fatal(err)
	}

	setCNIJobCondition(t, c, "ichor-system", cniHelmJobName(cluster, hash), batchv1.JobFailed)

	_, err := AdvanceCNIHelmJob(ctx, c, "ichor-system", cluster, cniTestInstallation())
	if err == nil || !strings.Contains(err.Error(), "failed") {
		t.Fatalf("error = %v, want the Job's failure", err)
	}
}
