//go:build e2e
// +build e2e

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

package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/test/internal/kube"
	"github.com/maeshinshin/ichor/test/internal/talosctl"
	"github.com/maeshinshin/ichor/test/utils"
)

// namespace is the only namespace this suite creates anything in (ADR 0007).
const namespace = "ichor-e2e"

// e2eLabel marks every CEL-fixture object this run creates, so AfterAll can find and
// delete exactly what it made.
const e2eLabel = "ichor.maesh.dev/e2e-run"

// talosVersion is the Talos release the suite's CEL fixtures declare.
const talosVersion = "v1.14.5"

func testObjectMeta(runID string) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		GenerateName: "e2e-" + runID + "-",
		Namespace:    namespace,
		Labels:       map[string]string{e2eLabel: runID},
	}
}

func newTestCluster(runID string, count int32) *v1alpha1.TalosCluster {
	return &v1alpha1.TalosCluster{
		ObjectMeta: testObjectMeta(runID),
		Spec: v1alpha1.TalosClusterSpec{
			Endpoint:          v1alpha1.TalosClusterEndpoint{Type: "vip", Address: "10.0.0.1"},
			TalosVersion:      talosVersion,
			KubernetesVersion: "v1.36.0",
			VersionContract:   "v1.14",
			SecretsRef:        corev1.LocalObjectReference{Name: "ichor-e2e-secrets"},
			ControlPlane:      v1alpha1.TalosClusterControlPlane{Count: count, Schedulable: "true"},
		},
	}
}

func newTestPatch(runID, layer string, target *v1alpha1.TalosConfigPatchTarget) *v1alpha1.TalosConfigPatch {
	return &v1alpha1.TalosConfigPatch{
		ObjectMeta: testObjectMeta(runID),
		Spec: v1alpha1.TalosConfigPatchSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "cluster"},
			Layer:      layer,
			Target:     target,
		},
	}
}

// getAll reads every object of one resource type from the node, through the authenticated API.
func getAll(resource string) []map[string]any {
	out, err := talosctl.GetAuthenticated(namespace, talosNode(), resource, talosconfigBytes)
	Expect(err).NotTo(HaveOccurred())
	objs, err := talosctl.DecodeAll(out)
	Expect(err).NotTo(HaveOccurred())
	return objs
}

var _ = Describe("short", Label("short"), Ordered, func() {
	runID := fmt.Sprintf("short-%d", time.Now().Unix())
	var k8sClient client.Client

	BeforeAll(func() {
		// The CRD and CEL checks below must land on the inner cluster the harness stood up
		// on talosNode(), not on whatever KUBECONFIG points at - which may be production
		// (ADR 0007, P2-8).
		cfg, err := clientcmd.BuildConfigFromFlags("", innerKubeconfig)
		Expect(err).NotTo(HaveOccurred(), "could not load the inner kubeconfig at %s=%s", innerKubeconfigEnvVar, innerKubeconfig)

		scheme := runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
		Expect(err).NotTo(HaveOccurred())

		// The harness only brings up the node and the CRDs; the inner cluster is otherwise
		// empty, so the namespace the fixtures below land in has to be created here.
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		Expect(client.IgnoreAlreadyExists(k8sClient.Create(context.Background(), ns))).To(Succeed())
	})

	AfterAll(func() {
		By("deleting the CEL validation fixtures created by this run")
		ctx := context.Background()
		Expect(client.IgnoreNotFound(k8sClient.DeleteAllOf(ctx, &v1alpha1.TalosConfigPatch{},
			client.InNamespace(namespace), client.MatchingLabels{e2eLabel: runID}))).To(Succeed())
		Expect(client.IgnoreNotFound(k8sClient.DeleteAllOf(ctx, &v1alpha1.TalosCluster{},
			client.InNamespace(namespace), client.MatchingLabels{e2eLabel: runID}))).To(Succeed())
	})

	It("confirms the node is configured and running, and reads systeminformation, disks and links", func() {
		// The harness has already applied a control-plane config and bootstrapped etcd
		// (ADR 0007, P2-8), so the node is no longer in maintenance mode by the time the
		// suite runs: it is authenticated-only from here, via talosconfigBytes.
		By("machinestatus: the node the harness handed us is running and ready")
		objs := getAll("machinestatus")
		Expect(objs).To(HaveLen(1))
		Expect(talosctl.SpecField(objs[0], "stage")).To(Equal("running"))
		Expect(talosctl.MachineStatusReady(objs[0])).To(BeTrue())

		By("systeminformation: SMBIOS UUID and serial, the primary key TalosMachine.spec.identity is built on")
		objs = getAll("systeminformation")
		Expect(objs).To(HaveLen(1))
		Expect(talosctl.SpecField(objs[0], "uuid")).NotTo(BeEmpty())
		Expect(talosctl.SpecField(objs[0], "manufacturer")).To(Equal("KubeVirt"))

		By("disks: a writable install target")
		objs = getAll("disks")
		devPaths := make([]any, 0, len(objs))
		for _, obj := range objs {
			devPaths = append(devPaths, talosctl.SpecField(obj, "dev_path"))
		}
		Expect(devPaths).To(ContainElement(ContainSubstring("vd")))

		By("links: a real NIC plus the bond and dummy devices Talos always creates")
		objs = getAll("links")
		ids := make([]any, 0, len(objs))
		for _, obj := range objs {
			ids = append(ids, talosctl.MetadataID(obj))
		}
		Expect(ids).To(ContainElement("bond0"))
	})

	It("installs the CRDs and they reach Established", func() {
		// make install and kubectl wait both resolve their cluster from the KUBECONFIG
		// env var; point it at the inner cluster for the duration of this spec only, so the
		// CRDs land there and not on whatever the ambient KUBECONFIG points at (ADR 0007,
		// P2-8) - restored immediately after so nothing downstream is affected.
		outerKubeconfig, hadOuter := os.LookupEnv("KUBECONFIG")
		Expect(os.Setenv("KUBECONFIG", innerKubeconfig)).To(Succeed())
		defer func() {
			if hadOuter {
				_ = os.Setenv("KUBECONFIG", outerKubeconfig)
			} else {
				_ = os.Unsetenv("KUBECONFIG")
			}
		}()

		_, err := utils.Run(exec.Command("make", "install"))
		Expect(err).NotTo(HaveOccurred())

		for _, crd := range []string{
			"talosclusters.ichor.maesh.dev",
			"talosmachines.ichor.maesh.dev",
			"talosconfigpatches.ichor.maesh.dev",
		} {
			_, err := utils.Run(kube.InnerCommand(innerKubeconfig, "wait", "crd/"+crd,
				"--for=condition=Established", "--timeout=60s"))
			Expect(err).NotTo(HaveOccurred(), "CRD %s did not reach Established", crd)
		}
	})

	It("enforces the CEL validation rules server-side", func() {
		ctx := context.Background()

		By("rejecting controlPlane.count: 2")
		Expect(k8sClient.Create(ctx, newTestCluster(runID, 2))).NotTo(Succeed())

		By("accepting a valid TalosCluster")
		Expect(k8sClient.Create(ctx, newTestCluster(runID, 3))).To(Succeed())

		By("rejecting a TalosConfigPatch with two of three target fields set")
		Expect(k8sClient.Create(ctx, newTestPatch(runID, "role", &v1alpha1.TalosConfigPatchTarget{
			Role: "worker", MachineRef: &corev1.LocalObjectReference{Name: "m"},
		}))).NotTo(Succeed())

		By("rejecting layer cluster with a target")
		Expect(k8sClient.Create(ctx, newTestPatch(runID, "cluster", &v1alpha1.TalosConfigPatchTarget{
			Role: "controlplane",
		}))).NotTo(Succeed())

		By("accepting a valid TalosConfigPatch")
		Expect(k8sClient.Create(ctx, newTestPatch(runID, "role", &v1alpha1.TalosConfigPatchTarget{
			Role: "worker",
		}))).To(Succeed())
	})
})
