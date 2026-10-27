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
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("IchorUser validation", func() {
	newUser := func(name string, roles ...string) *IchorUser {
		u := &IchorUser{Spec: IchorUserSpec{DisplayName: "Test", Roles: roles}}
		u.Name, u.Namespace = name, testNamespace
		return u
	}

	It("accepts a DNS-label name and built-in and custom roles", func() {
		u := newUser("alice-1", "admin", "ops-team")
		Expect(k8sClient.Create(context.Background(), u)).To(Succeed())
		Expect(k8sClient.Delete(context.Background(), u)).To(Succeed())
	})

	DescribeTable("rejects a name that is not a lower-case DNS label",
		func(name string) {
			Expect(k8sClient.Create(context.Background(), newUser(name))).NotTo(Succeed())
		},
		Entry("with a dot", "alice.example"),
		Entry("with a digit first", "1alice"),
		Entry("longer than 63 characters", "a"+strings.Repeat("b", 63)),
	)

	DescribeTable("rejects a role outside ^[a-z][a-z0-9-]{0,30}$",
		func(role string) {
			Expect(k8sClient.Create(context.Background(), newUser("bob", role))).NotTo(Succeed())
		},
		Entry("upper case", "Admin"),
		Entry("colon (would forge a prefixed group)", "ichor:admin"),
		Entry("system prefix", "system:masters"),
		Entry("empty", ""),
		Entry("too long", "a"+strings.Repeat("b", 31)),
	)

	It("rejects more than 16 roles", func() {
		roles := make([]string, 17)
		for i := range roles {
			roles[i] = "r" + strings.Repeat("a", i)
		}
		Expect(k8sClient.Create(context.Background(), newUser("carol", roles...))).NotTo(Succeed())
	})
})
