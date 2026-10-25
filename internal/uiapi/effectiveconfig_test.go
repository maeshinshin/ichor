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
	"encoding/base64"

	"connectrpc.com/connect"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// bundleSecretMaterial lists every sensitive scalar in bundle that
// machinery's own config.Provider.RedactSecrets commits to redacting
// (confirmed by reading config/types/v1alpha1/v1alpha1_redact_test.go in
// the vendored module). It is narrower, deliberately, than the list
// pkg/render's own TestRenderBundleLeak and internal/controller's
// leak_test.go use for the same bundle:
//
//   - bundle.Cluster.ID is left out. It is a discovery-service lookup key,
//     not a credential -- RedactSecrets redacts Cluster.Secret but never
//     Cluster.ID -- so asserting it never appears in a rendered,
//     correctly-masked config would be asserting a leak that the config
//     legitimately needs a Talos node to see in plain text.
//   - bundle.Certs.OS.Crt and bundle.Certs.K8s.Crt are left out: a
//     certificate is public by definition (RedactSecrets' own test:
//     "client cert is public and must not be redacted"); only the
//     matching .Key is secret.
//
// The other two tests' broader list is still right for what they check
// (an incidental secret turning up in an error string should never
// happen, full stop); this one is checking a deliberately rendered
// document, where only the fields machinery itself calls secret may be
// absent.
func bundleSecretMaterial(bundle *secrets.Bundle) []string {
	return []string{
		bundle.Secrets.BootstrapToken,
		bundle.TrustdInfo.Token,
		bundle.Cluster.Secret,
		string(bundle.Certs.OS.Key),
		string(bundle.Certs.K8s.Key),
	}
}

var _ = Describe("GetEffectiveConfig masking", func() {
	It("never sends bundle secret material unmasked, and still shows non-secret values plainly", func() {
		bundle := newTestBundle()
		secret := newTestSecret(bundle)
		cluster := newTestCluster(secret.Name)
		m := newTestMachine(cluster.Name, strongTestIdentity("uuid-effcfg"), roleControlPlane, ichorv1alpha1.MachineApprovalStateApproved)

		s := &Server{Client: k8sClient}
		resp, err := s.GetEffectiveConfig(ctx, connect.NewRequest(&pb.GetEffectiveConfigRequest{
			Machine: objectRef(m.Namespace, m.Name),
		}))
		Expect(err).NotTo(HaveOccurred())

		values := resp.Msg.GetConfig().GetValues()
		Expect(values).NotTo(BeEmpty())

		material := bundleSecretMaterial(bundle)

		// machinery's own YAML encoder renders a []byte-typed field (the CA
		// keys) as base64 text, not the raw PEM block -- confirmed by
		// observation, not documented -- so a leak of one of those two
		// scalars would not show up as the plain substring. Checking both
		// forms is what catches a leak that representation would otherwise
		// hide from a plain substring search.
		material = append(material,
			base64.StdEncoding.EncodeToString(bundle.Certs.OS.Key),
			base64.StdEncoding.EncodeToString(bundle.Certs.K8s.Key),
		)

		secretCount, plainCount := 0, 0

		for _, v := range values {
			for _, s := range material {
				if s == "" {
					continue
				}

				Expect(v.GetValue()).NotTo(ContainSubstring(s),
					"EffectiveConfigValue %q leaked bundle secret material (secret=%v)", v.GetPath(), v.GetSecret())
			}

			if v.GetSecret() {
				secretCount++
			} else {
				plainCount++
			}
		}

		Expect(secretCount).To(BeNumerically(">", 0), "expected at least one field (e.g. machine.token) to be masked")
		Expect(plainCount).To(BeNumerically(">", secretCount),
			"expected most fields to be sent unmasked, proving masking is selective rather than blanket")

		// machine.token is a known secret path (pkg/typedconfig's MachineToken,
		// bundle.TrustdInfo.Token's destination): assert it specifically, not
		// only "some field was masked."
		var tokenValue *pb.EffectiveConfigValue
		for _, v := range values {
			if v.GetPath() == "machine.token" {
				tokenValue = v
			}
		}
		Expect(tokenValue).NotTo(BeNil())
		Expect(tokenValue.GetSecret()).To(BeTrue())
		Expect(tokenValue.GetValue()).To(Equal(`"` + secretMask + `"`))
	})

	It("annotates values with the patch that set them, and the system layer for everything else", func() {
		bundle := newTestBundle()
		secret := newTestSecret(bundle)
		cluster := newTestCluster(secret.Name)
		m := newTestMachine(cluster.Name, strongTestIdentity("uuid-origin"), roleWorker, ichorv1alpha1.MachineApprovalStateApproved)

		patch := newTestPatch(cluster.Name, layerCluster, nil, 0, extraArgsConfig(roleWorker))

		s := &Server{Client: k8sClient}
		resp, err := s.GetEffectiveConfig(ctx, connect.NewRequest(&pb.GetEffectiveConfigRequest{
			Machine: objectRef(m.Namespace, m.Name),
		}))
		Expect(err).NotTo(HaveOccurred())

		var patched, system *pb.EffectiveConfigValue

		for _, v := range resp.Msg.GetConfig().GetValues() {
			switch v.GetPath() {
			case "cluster.apiServer.extraArgs.foo":
				patched = v
			case "DiscoveryIdentityConfig.clusterID":
				// A separate document the "Config"-kind patch above never
				// touches (render.DocumentFromPatch has no case that could
				// produce one), so it can only have come from base generation.
				system = v
			}
		}

		Expect(patched).NotTo(BeNil())
		Expect(patched.GetOrigin().GetLayer()).To(Equal(pb.ConfigLayer_CONFIG_LAYER_CLUSTER))
		Expect(patched.GetOrigin().GetSource()).To(Equal(patch.Name))

		Expect(system).NotTo(BeNil())
		Expect(system.GetOrigin().GetLayer()).To(Equal(pb.ConfigLayer_CONFIG_LAYER_SYSTEM))
		Expect(system.GetOrigin().GetSource()).To(Equal("system"))
	})

	It("refuses to render a machine with no role yet, rather than guessing one", func() {
		bundle := newTestBundle()
		secret := newTestSecret(bundle)
		cluster := newTestCluster(secret.Name)
		m := newTestMachine(cluster.Name, strongTestIdentity("uuid-norole-eff"), "", ichorv1alpha1.MachineApprovalStatePending)

		s := &Server{Client: k8sClient}
		_, err := s.GetEffectiveConfig(ctx, connect.NewRequest(&pb.GetEffectiveConfigRequest{
			Machine: objectRef(m.Namespace, m.Name),
		}))
		Expect(err).To(HaveOccurred())
		Expect(connect.CodeOf(err)).To(Equal(connect.CodeFailedPrecondition))
	})
})
