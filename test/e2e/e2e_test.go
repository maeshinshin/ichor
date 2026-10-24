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
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	"github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/test/utils"
)

// namespace is the only namespace this suite creates anything in.
const namespace = "ichor-e2e"

// e2eLabel marks every object this run creates, so AfterAll can find and delete exactly
// what it made without touching the pre-existing talos-probe VM or netprobe pod.
const e2eLabel = "ichor.maesh.dev/e2e-run"

// talosFactorySchematic is the bare, no-extensions Image Factory schematic for the
// metal-amd64 ISO.
const talosFactorySchematic = "376567988ad370138ad8b2698212367b8edcb69b5fd68c80be1f2ec7d603b4ba"

// talosVersion is the Talos release the e2e VM boots.
const talosVersion = "v1.14.3"

// talosctlImage runs talosctl for us from inside the cluster. The workstation running
// `go test` cannot route to the VM's pod IP, and plain `kubectl port-forward` to the
// virt-launcher pod can't reach it either: with KubeVirt's bridge binding the pod's own
// network namespace has no listener on localhost, only the guest does (bridge delegates
// the pod IP to the VM itself, it does not proxy it onto the pod's loopback - confirmed
// by hand against the real talos-probe VM, where port-forward to virt-launcher gets
// "connection refused" on localhost while a pod-to-pod connection to the same IP
// succeeds). So every talosctl call below runs as a one-shot Pod inside ichor-e2e,
// which can reach the VM's pod IP directly, and we read its result back with
// `kubectl logs`.
const talosctlImage = "ghcr.io/siderolabs/talosctl:" + talosVersion

const vmManifestTmpl = `apiVersion: kubevirt.io/v1
kind: VirtualMachine
metadata:
  name: %[1]s
  namespace: ichor-e2e
  labels:
    ichor.maesh.dev/e2e-run: %[2]s
spec:
  runStrategy: Always
  dataVolumeTemplates:
    - metadata:
        name: %[1]s-iso
      spec:
        source:
          http:
            url: https://factory.talos.dev/image/%[3]s/%[4]s/metal-amd64.iso
        storage:
          accessModes: [ReadWriteOnce]
          volumeMode: Filesystem
          resources:
            requests:
              storage: 1Gi
          storageClassName: ceph-block
    - metadata:
        name: %[1]s-system
      spec:
        source:
          blank: {}
        storage:
          accessModes: [ReadWriteOnce]
          volumeMode: Filesystem
          resources:
            requests:
              storage: 10Gi
          storageClassName: ceph-block
  template:
    metadata:
      labels:
        ichor.maesh.dev/e2e-run: %[2]s
    spec:
      domain:
        cpu:
          cores: 2
        memory:
          guest: 2560Mi
        devices:
          disks:
            - name: iso
              cdrom:
                bus: sata
              bootOrder: 1
            - name: system
              disk:
                bus: virtio
              bootOrder: 2
          interfaces:
            - name: default
              bridge: {}
      networks:
        - name: default
          pod: {}
      volumes:
        - name: iso
          dataVolume:
            name: %[1]s-iso
        - name: system
          dataVolume:
            name: %[1]s-system
`

// talosctlPodManifestTmpl runs a single talosctl command insecurely against a node and exits.
const talosctlPodManifestTmpl = `apiVersion: v1
kind: Pod
metadata:
  name: %[1]s
  namespace: ichor-e2e
spec:
  restartPolicy: Never
  containers:
    - name: talosctl
      image: %[2]s
      command: ["/talosctl", "get", "%[3]s", "-o", "json", "--insecure", "--nodes", "%[4]s"]
`

// applyManifest applies a YAML manifest via stdin, matching the kubectl-exec style the
// rest of this suite uses instead of adding a typed client for resources (KubeVirt, CDI)
// this repo has no Go dependency on.
func applyManifest(manifest string) error {
	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	_, err := utils.Run(cmd)
	return err
}

// talosctlGet runs `talosctl get <resource> -o json --insecure` against ip from a
// one-shot Pod inside the cluster and returns its raw (possibly multi-object) JSON output.
func talosctlGet(ip, resource string) (string, error) {
	podName := fmt.Sprintf("talosctl-%s-%d", resource, time.Now().UnixNano())
	manifest := fmt.Sprintf(talosctlPodManifestTmpl, podName, talosctlImage, resource, ip)
	defer func() {
		_, _ = utils.Run(exec.Command("kubectl", "-n", namespace, "delete", "pod", podName,
			"--wait=false", "--ignore-not-found"))
	}()

	if err := applyManifest(manifest); err != nil {
		return "", err
	}
	if _, err := utils.Run(exec.Command("kubectl", "-n", namespace, "wait", "pod/"+podName,
		"--for=jsonpath={.status.phase}=Succeeded", "--timeout=60s")); err != nil {
		return "", err
	}
	return utils.Run(exec.Command("kubectl", "-n", namespace, "logs", podName))
}

// decodeAll decodes the concatenated JSON objects talosctl prints for multi-instance
// resources (it is not a JSON array) into a slice of maps.
func decodeAll(raw string) ([]map[string]any, error) {
	dec := json.NewDecoder(strings.NewReader(raw))
	var out []map[string]any
	for {
		var obj map[string]any
		if err := dec.Decode(&obj); err != nil {
			if err == io.EOF {
				return out, nil
			}
			return nil, err
		}
		out = append(out, obj)
	}
}

func specField(obj map[string]any, field string) any {
	spec, _ := obj["spec"].(map[string]any)
	return spec[field]
}

func metadataID(obj map[string]any) any {
	metadata, _ := obj["metadata"].(map[string]any)
	return metadata["id"]
}

// testObjectMeta names a CEL-fixture object for this run's namespace and labels it so
// AfterAll can delete exactly what this run created.
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
			ControlPlane:      v1alpha1.TalosClusterControlPlane{Count: count},
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

var _ = Describe("short", Label("short"), Ordered, func() {
	runID := fmt.Sprintf("short-%d", time.Now().Unix())
	vmName := "ichor-e2e-" + runID
	var vmPodIP string
	var k8sClient client.Client

	BeforeAll(func() {
		cfg, err := config.GetConfig()
		Expect(err).NotTo(HaveOccurred(), "could not load a kubeconfig; set KUBECONFIG")

		scheme := runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
		Expect(err).NotTo(HaveOccurred())

		By("creating the Talos node as a KubeVirt VM")
		Expect(applyManifest(fmt.Sprintf(vmManifestTmpl, vmName, runID, talosFactorySchematic, talosVersion))).
			To(Succeed())
	})

	AfterAll(func() {
		By("tearing down the VM and its DataVolumes")
		_, _ = utils.Run(exec.Command("kubectl", "-n", namespace, "delete", "vm", vmName,
			"--wait=true", "--timeout=120s", "--ignore-not-found"))
		_, _ = utils.Run(exec.Command("kubectl", "-n", namespace, "delete", "dv",
			vmName+"-iso", vmName+"-system", "--wait=true", "--timeout=120s", "--ignore-not-found"))

		By("deleting the CEL validation fixtures created by this run")
		ctx := context.Background()
		Expect(client.IgnoreNotFound(k8sClient.DeleteAllOf(ctx, &v1alpha1.TalosConfigPatch{},
			client.InNamespace(namespace), client.MatchingLabels{e2eLabel: runID}))).To(Succeed())
		Expect(client.IgnoreNotFound(k8sClient.DeleteAllOf(ctx, &v1alpha1.TalosCluster{},
			client.InNamespace(namespace), client.MatchingLabels{e2eLabel: runID}))).To(Succeed())
	})

	It("boots the VM and reaches Talos maintenance mode", func() {
		By("waiting for the VMI to report a pod IP")
		Eventually(func(g Gomega) {
			out, err := utils.Run(exec.Command("kubectl", "-n", namespace, "get", "vmi", vmName,
				"-o", "jsonpath={.status.interfaces[0].ipAddress}"))
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(out).NotTo(BeEmpty())
			vmPodIP = strings.TrimSpace(out)
		}, 3*time.Minute, 5*time.Second).Should(Succeed())

		By("waiting for MachineStatus stage to reach maintenance")
		Eventually(func(g Gomega) {
			out, err := talosctlGet(vmPodIP, "machinestatus")
			g.Expect(err).NotTo(HaveOccurred())
			objs, err := decodeAll(out)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(objs).To(HaveLen(1))
			g.Expect(specField(objs[0], "stage")).To(Equal("maintenance"))
		}, 5*time.Minute, 10*time.Second).Should(Succeed())
	})

	It("reads systeminformation, disks and links over the insecure maintenance API", func() {
		By("systeminformation: SMBIOS UUID and serial, the primary key TalosMachine.spec.identity is built on")
		out, err := talosctlGet(vmPodIP, "systeminformation")
		Expect(err).NotTo(HaveOccurred())
		objs, err := decodeAll(out)
		Expect(err).NotTo(HaveOccurred())
		Expect(objs).To(HaveLen(1))
		Expect(specField(objs[0], "uuid")).NotTo(BeEmpty())
		Expect(specField(objs[0], "manufacturer")).To(Equal("KubeVirt"))

		By("disks: the blank system disk shows up as a writable vda")
		out, err = talosctlGet(vmPodIP, "disks")
		Expect(err).NotTo(HaveOccurred())
		objs, err = decodeAll(out)
		Expect(err).NotTo(HaveOccurred())
		devPaths := make([]any, 0, len(objs))
		for _, obj := range objs {
			devPaths = append(devPaths, specField(obj, "dev_path"))
		}
		Expect(devPaths).To(ContainElement("/dev/vda"))

		By("links: a real NIC plus the bond and dummy devices Talos always creates")
		out, err = talosctlGet(vmPodIP, "links")
		Expect(err).NotTo(HaveOccurred())
		objs, err = decodeAll(out)
		Expect(err).NotTo(HaveOccurred())
		ids := make([]any, 0, len(objs))
		for _, obj := range objs {
			ids = append(ids, metadataID(obj))
		}
		Expect(ids).To(ContainElement("bond0"))
	})

	It("installs the CRDs and they reach Established", func() {
		_, err := utils.Run(exec.Command("make", "install"))
		Expect(err).NotTo(HaveOccurred())

		for _, crd := range []string{
			"talosclusters.ichor.maesh.dev",
			"talosmachines.ichor.maesh.dev",
			"talosconfigpatches.ichor.maesh.dev",
		} {
			_, err := utils.Run(exec.Command("kubectl", "wait", "crd/"+crd,
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
