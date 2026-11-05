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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/siderolabs/talos/pkg/machinery/resources/runtime"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// The Talos details view shows the node's SecurityState and the lockdown level derived
// from its kernel command line.
var _ = Describe("securityFromSpec", func() {
	It("copies the SecurityState fields and derives the lockdown level", func() {
		out := &pb.TalosSystemSection{}
		securityFromSpec(out, &runtime.SecurityStateSpec{
			SecureBoot: true, BootedWithUKI: true, ModuleSignatureEnforced: true, UKISigningKeyFingerprint: "ab:cd", PCRSigningKeyFingerprint: "ef:01",
		}, "talos.platform=metal lockdown=confidentiality")

		Expect(out.GetSecureBoot()).To(BeTrue())
		Expect(out.GetBootedWithUki()).To(BeTrue())
		Expect(out.GetModuleSignatureEnforced()).To(BeTrue())
		Expect(out.GetUkiSigningKeyFingerprint()).To(Equal("ab:cd"))
		Expect(out.GetPcrSigningKeyFingerprint()).To(Equal("ef:01"))
		Expect(out.GetKernelLockdown()).To(Equal("confidentiality"))
	})

	It("is integrity under SecureBoot without an argument, none without both", func() {
		out := &pb.TalosSystemSection{}
		securityFromSpec(out, &runtime.SecurityStateSpec{SecureBoot: true}, "")
		Expect(out.GetKernelLockdown()).To(Equal("integrity"))

		out = &pb.TalosSystemSection{}
		securityFromSpec(out, &runtime.SecurityStateSpec{}, "quiet")
		Expect(out.GetSecureBoot()).To(BeFalse())
		Expect(out.GetKernelLockdown()).To(Equal("none"))
	})

	It("leaves the section alone for a node that has no SecurityState", func() {
		out := &pb.TalosSystemSection{}
		securityFromSpec(out, nil, "lockdown=integrity")
		Expect(out.GetKernelLockdown()).To(BeEmpty())
	})
})
