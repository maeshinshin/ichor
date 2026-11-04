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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/upgrade"
)

// ADR 0123: the installer variant of a Talos upgrade follows the mode and what the node says.
var _ = Describe("talosUpgradeAdvancer installer variant", func() {
	const (
		stock  = "factory.talos.dev/metal-installer/"
		signed = "factory.talos.dev/metal-installer-secureboot/"
	)

	upgradeImage := func(mode string, node SecurityState, suffix string) string {
		secret := newTestSecret(newTestBundle("v1.14"))
		cluster := newTestCluster(secret.Name, "v1.14")

		if mode != "" {
			cluster.Spec.Security = &ichorv1alpha1.TalosClusterSecurity{SecureBoot: &ichorv1alpha1.TalosClusterSecureBoot{Mode: mode}}
			Expect(k8sClient.Update(ctx, cluster)).To(Succeed())
		}

		cp := newTestControlPlane(cluster.Name, suffix)
		setHostname(cp, "node-"+suffix)
		newTestNode("node-"+suffix, true)

		fake := &fakeTalosClient{version: testOldTalosVersion, bootID: testBootIDBefore, securityState: node}

		_, err := newUpgradeAdvancer(fake, nil).Advance(ctx, cluster, fetchMachine(cp), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.upgradeCalls).To(HaveLen(1))

		return fake.upgradeCalls[0]
	}

	tail := ":" + "v1.14.3"

	It("keeps the stock installer when the mode is unset", func() {
		Expect(upgradeImage("", SecurityState{BootedWithUKI: true}, "sb1")).To(Equal(stock + upgrade.DefaultSchematic + tail))
	})

	It("uses the SecureBoot installer for a UKI node when the mode is Preferred", func() {
		Expect(upgradeImage(ichorv1alpha1.SecureBootPreferred, SecurityState{BootedWithUKI: true}, "sb2")).
			To(Equal(signed + upgrade.DefaultSchematic + tail))
	})

	It("keeps the stock installer for a GRUB node whatever the mode, since Talos cannot convert it", func() {
		Expect(upgradeImage(ichorv1alpha1.SecureBootRequired, SecurityState{}, "sb3")).To(Equal(stock + upgrade.DefaultSchematic + tail))
	})

	It("never gives a node that runs SecureBoot the unsigned installer, even with the mode Off", func() {
		Expect(upgradeImage(ichorv1alpha1.SecureBootOff, SecurityState{SecureBoot: true, BootedWithUKI: true}, "sb4")).
			To(Equal(signed + upgrade.DefaultSchematic + tail))
	})
})
