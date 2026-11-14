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
	"connectrpc.com/connect"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/upgrade"
)

// An approval that names a disk and no installer image gets the installer of the cluster's
// schematic and Talos version: the SecureBoot one when the cluster's mode is not Off.
var _ = Describe("ApproveMachine installer default", func() {
	approve := func(mode, image string) *ichorv1alpha1.TalosMachine {
		cluster := newTestCluster("does-not-exist")

		if mode != "" {
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), cluster)).To(Succeed())
			cluster.Spec.Security = &ichorv1alpha1.TalosClusterSecurity{SecureBoot: &ichorv1alpha1.TalosClusterSecureBoot{Mode: mode}}
			Expect(k8sClient.Update(ctx, cluster)).To(Succeed())
		}

		m := newTestMachine(cluster.Name, strongTestIdentity("uuid-sb-"+mode+image), "", ichorv1alpha1.MachineApprovalStatePending)
		s := &Server{Client: k8sClient}

		_, err := s.ApproveMachine(ctx, connect.NewRequest(&pb.ApproveMachineRequest{
			Machine: objectRef(m.Namespace, m.Name), Role: pb.MachineRole_MACHINE_ROLE_WORKER,
			Install: &pb.MachineInstall{Disk: &pb.MachineDisk{DevPath: "/dev/sdz"}, Image: image},
		}))
		Expect(err).NotTo(HaveOccurred())

		return fetchTestMachine(m)
	}

	const secureBootPrefix = "factory.talos.dev/metal-installer-secureboot/"

	It("fills in the SecureBoot installer for Preferred and Required", func() {
		for _, mode := range []string{ichorv1alpha1.SecureBootPreferred, ichorv1alpha1.SecureBootRequired} {
			got := approve(mode, "")
			Expect(got.Spec.Install.Image).To(Equal(secureBootPrefix + upgrade.DefaultSchematic + ":" + fixtureTalos))
		}
	})

	It("names the plain installer when the mode is Off or unset, and keeps an image the user named", func() {
		const plain = "factory.talos.dev/metal-installer/" + upgrade.DefaultSchematic + ":" + fixtureTalos
		Expect(approve("", "").Spec.Install.Image).To(Equal(plain))
		Expect(approve(ichorv1alpha1.SecureBootOff, "").Spec.Install.Image).To(Equal(plain))
		Expect(approve(ichorv1alpha1.SecureBootRequired, "registry.example/installer:v1").Spec.Install.Image).To(Equal("registry.example/installer:v1"))
	})
})
