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

package v1alpha1

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

var _ = Describe("IchorInstallation runtime settings", func() {
	dur := func(d time.Duration) *metav1.Duration { return &metav1.Duration{Duration: d} }
	newInst := func(mutate func(*IchorInstallationSpec)) *IchorInstallation {
		i := &IchorInstallation{Spec: IchorInstallationSpec{Version: "v0.4.0"}}
		i.Name = IchorInstallationName
		if mutate != nil {
			mutate(&i.Spec)
		}
		return i
	}
	create := func(i *IchorInstallation) error {
		err := k8sClient.Create(context.Background(), i)
		if err == nil {
			DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), i) })
		}
		return err
	}

	It("leaves every setting unset when omitted, so today's behaviour holds", func() {
		i := newInst(nil)
		Expect(create(i)).To(Succeed())
		Expect(i.Spec.Deployment).To(BeNil())
		Expect(i.Spec.Reclaim).To(BeNil())
		Expect(i.Spec.Users).To(BeNil())
		Expect(i.Spec.Defaults).To(BeNil())
		Expect(i.Status.Effective).To(BeNil())
	})

	It("accepts valid settings", func() {
		i := newInst(func(s *IchorInstallationSpec) {
			s.Deployment = &IchorDeploymentSpec{Manager: IchorManagerHelm}
			s.Reclaim = &IchorReclaimSettings{Enabled: ptr.To(true)}
			s.Users = &IchorUserSettings{TokenTTL: dur(time.Hour), RefreshLifetime: dur(720 * time.Hour)}
			s.Defaults = &IchorDefaultSettings{DriftPolicy: "autoCorrect"}
		})
		Expect(create(i)).To(Succeed())
		Expect(i.Spec.Users.TokenTTL.Duration).To(Equal(time.Hour))
	})

	DescribeTable("rejects invalid settings",
		func(mutate func(*IchorInstallationSpec)) {
			Expect(create(newInst(mutate))).NotTo(Succeed())
		},
		Entry("unknown manager", func(s *IchorInstallationSpec) { s.Deployment = &IchorDeploymentSpec{Manager: "argo"} }),
		Entry("tokenTTL below 1m", func(s *IchorInstallationSpec) { s.Users = &IchorUserSettings{TokenTTL: dur(30 * time.Second)} }),
		Entry("tokenTTL above 1h", func(s *IchorInstallationSpec) { s.Users = &IchorUserSettings{TokenTTL: dur(2 * time.Hour)} }),
		Entry("refreshLifetime below 1h", func(s *IchorInstallationSpec) { s.Users = &IchorUserSettings{RefreshLifetime: dur(30 * time.Minute)} }),
		Entry("refreshLifetime above 720h", func(s *IchorInstallationSpec) { s.Users = &IchorUserSettings{RefreshLifetime: dur(721 * time.Hour)} }),
		Entry("unknown driftPolicy", func(s *IchorInstallationSpec) { s.Defaults = &IchorDefaultSettings{DriftPolicy: "enforce"} }),
	)

	It("accepts tokenTTL equal to refreshLifetime", func() {
		Expect(create(newInst(func(s *IchorInstallationSpec) {
			s.Users = &IchorUserSettings{TokenTTL: dur(time.Hour), RefreshLifetime: dur(time.Hour)}
		}))).To(Succeed())
	})
})
