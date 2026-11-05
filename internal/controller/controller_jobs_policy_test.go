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
	"bytes"
	"errors"
	"io"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni"
)

// The admission policy that limits the controller's Jobs (config/admission) must admit
// the Jobs the controller really builds. This loads the committed policy into the apiserver and
// creates the real Job objects as the controller's ServiceAccount.
var _ = Describe("the controller Job policy against the real Job builders", Ordered, func() {
	const controllerUser = "system:serviceaccount:ichor-system:ichor-controller-manager"

	var (
		controller client.Client
		inst       *ichorv1alpha1.IchorInstallation
	)

	BeforeAll(func() {
		Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &corev1.Namespace{Name: "ichor-system"}))).To(Succeed())

		data, err := os.ReadFile("../../config/admission/controller-jobs-policy.yaml")
		Expect(err).NotTo(HaveOccurred())

		dec := k8syaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
		for {
			var obj map[string]any
			if err := dec.Decode(&obj); err != nil {
				if errors.Is(err, io.EOF) {
					break
				}

				Expect(err).NotTo(HaveOccurred())
			}

			if obj == nil {
				continue
			}

			u := &unstructured.Unstructured{Object: obj}
			Expect(k8sClient.Create(ctx, u)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, u) })
		}

		c := rest.CopyConfig(cfg)
		c.Impersonate = rest.ImpersonationConfig{UserName: controllerUser, Groups: []string{"system:masters", "system:authenticated"}}
		controller, err = client.New(c, client.Options{Scheme: scheme.Scheme})
		Expect(err).NotTo(HaveOccurred())

		inst = &ichorv1alpha1.IchorInstallation{Spec: ichorv1alpha1.IchorInstallationSpec{
			Version: "v0.0.7", InstallerRepository: "registry.example.org/ichor-installer",
		}}

		// The policy is bound asynchronously: wait until it denies a Job under another ServiceAccount.
		Eventually(func() error {
			j := newInstallerJob("ichor-system", "probe-installer", &ichorv1alpha1.TalosOperation{}, inst, testInstallerDigest, "cilium", false)
			j.Name, j.Spec.Template.Spec.ServiceAccountName = "", "other"
			j.GenerateName = "probe-"

			return controller.Create(ctx, j)
		}, "30s", "100ms").ShouldNot(Succeed())
	})

	// create admits j as the controller and removes it afterwards: other specs list the Jobs of these
	// namespaces and must not find these.
	create := func(j *batchv1.Job) {
		Expect(controller.Create(ctx, j)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, j, client.PropagationPolicy(metav1.DeletePropagationBackground)) })
	}

	It("admits the installer Job as built", func() {
		j := newInstallerJob("ichor-system", "ichor-installer-policy", &ichorv1alpha1.TalosOperation{}, inst, testInstallerDigest, "cilium", false)
		create(j)

		j = newInstallerJob("ichor-system", "ichor-installer-policy-2", &ichorv1alpha1.TalosOperation{}, inst, testInstallerDigest, "cilium", true)
		create(j)
	})

	It("admits the Cilium Helm Job as built, with and without takeover", func() {
		cluster := &ichorv1alpha1.TalosCluster{Name: "c", Namespace: testIchorNamespace}

		for i, takeover := range []bool{false, true} {
			j := newCNIHelmJob("ichor-system", "ichor-cni-policy-"+string(rune('a'+i)), cluster, cni.KubeProxyReplaced, inst.Spec.InstallerRepository+"@"+testInstallerDigest, "hash", takeover, 0)
			create(j)
		}
	})
})
