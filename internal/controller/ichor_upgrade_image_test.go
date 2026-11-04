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
	"errors"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/imagesig"
)

const (
	// testInstallerRepo is the installer repository createInstallation sets.
	testInstallerRepo = "registry.example/ichor-installer"
	testGHCR          = "ghcr.io"

	keyEndpointSelector = "endpointSelector"
	keyMatchLabels      = "matchLabels"
)

// testInstallerDigest is the digest the fake registry resolves every installer tag to.
const testInstallerDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// fakeInstallerImages is the registry and the verifier of an Ichor upgrade test.
type fakeInstallerImages struct {
	digest     string
	resolveErr error
	verifyErr  error

	resolved []string // repository:tag asked for
	verified []imagesig.Image
	identity imagesig.Identity
}

func (f *fakeInstallerImages) Resolve(_ context.Context, repository, tag string) (string, error) {
	f.resolved = append(f.resolved, repository+":"+tag)

	return f.digest, f.resolveErr
}

func (f *fakeInstallerImages) Verify(_ context.Context, img imagesig.Image, id imagesig.Identity) error {
	f.verified, f.identity = append(f.verified, img), id

	return f.verifyErr
}

func currentInstallation() *ichorv1alpha1.IchorInstallation {
	var got ichorv1alpha1.IchorInstallation
	Expect(k8sClient.Get(ctx, client.ObjectKey{Name: ichorv1alpha1.IchorInstallationName}, &got)).To(Succeed())

	return &got
}

var _ = Describe("kind: ichor executor, installer image", func() {
	const jobNs = testIchorNamespace

	op := func() *ichorv1alpha1.TalosOperation {
		return &ichorv1alpha1.TalosOperation{Namespace: "ns", Name: "upgrade-image"}
	}

	jobs := func() []batchv1.Job {
		var l batchv1.JobList
		Expect(k8sClient.List(ctx, &l, client.InNamespace(jobNs),
			client.MatchingLabels{nameLabel: IchorInstallerServiceAccount})).To(Succeed())

		return l.Items
	}

	installation := currentInstallation

	AfterEach(func() {
		Expect(k8sClient.DeleteAllOf(ctx, &batchv1.Job{}, client.InNamespace(jobNs),
			client.PropagationPolicy(metav1.DeletePropagationBackground))).To(Succeed())
	})

	It("resolves the tag, verifies the digest for the release workflow of spec.version, and runs the digest", func() {
		withVersion("v0.1.0")
		createInstallation("v0.2.0")

		images := &fakeInstallerImages{digest: testInstallerDigest}
		rec := record.NewFakeRecorder(5)

		done, err := NewIchorUpgradeAdvancer(k8sClient, k8sClient, jobNs, false, images, rec)(ctx, nil, nil, op())
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())

		Expect(images.resolved).To(Equal([]string{testInstallerRepo + ":v0.2.0"}))
		Expect(images.verified).To(Equal([]imagesig.Image{{Repository: testInstallerRepo, Digest: testInstallerDigest}}))
		Expect(images.identity).To(Equal(imagesig.IchorIdentity("v0.2.0")))

		Expect(jobs()).To(HaveLen(1))
		job := jobs()[0]
		Expect(job.Spec.Template.Spec.Containers[0].Image).To(Equal(testInstallerRepo + "@" + testInstallerDigest))
		Expect(job.Annotations).To(HaveKeyWithValue(IchorInstallerDigestAnnotation, testInstallerDigest))

		img := installation().Status.InstallerImage
		Expect(img).NotTo(BeNil())
		Expect(img.Version).To(Equal("v0.2.0"))
		Expect(img.Digest).To(Equal(testInstallerDigest))
		Expect(img.Verification).To(Equal(ichorv1alpha1.InstallerImageVerified))
		Expect(img.Signer).To(Equal(imagesig.IchorIdentity("v0.2.0").SANRegexp))
		Expect(img.ResolvedAt).NotTo(BeNil())
		Expect(rec.Events).To(BeEmpty())

		// A later call finds the Job and asks the registry nothing.
		_, err = NewIchorUpgradeAdvancer(k8sClient, k8sClient, jobNs, false, images, rec)(ctx, nil, nil, op())
		Expect(err).NotTo(HaveOccurred())
		Expect(images.resolved).To(HaveLen(1))
		Expect(images.verified).To(HaveLen(1))
	})

	It("uses spec.installerDigest without resolving the tag, and still verifies it", func() {
		withVersion("v0.1.0")
		inst := createInstallation("v0.2.0")
		pin := "sha256:" + strings.Repeat("b", 64)
		inst.Spec.InstallerDigest = pin
		Expect(k8sClient.Update(ctx, inst)).To(Succeed())

		images := &fakeInstallerImages{digest: testInstallerDigest}

		_, err := NewIchorUpgradeAdvancer(k8sClient, k8sClient, jobNs, false, images, nil)(ctx, nil, nil, op())
		Expect(err).NotTo(HaveOccurred())

		Expect(images.resolved).To(BeEmpty())
		Expect(images.verified).To(Equal([]imagesig.Image{{Repository: testInstallerRepo, Digest: pin}}))
		Expect(jobs()).To(HaveLen(1))
		Expect(jobs()[0].Spec.Template.Spec.Containers[0].Image).To(Equal(testInstallerRepo + "@" + pin))
	})

	It("pauses with InstallerImageUnresolved when the tag does not resolve, and creates no Job", func() {
		withVersion("v0.1.0")
		createInstallation("v0.2.0")

		images := &fakeInstallerImages{resolveErr: errors.New("connection refused")}

		_, err := NewIchorUpgradeAdvancer(k8sClient, k8sClient, jobNs, false, images, nil)(ctx, nil, nil, op())
		Expect(err).To(MatchError(ContainSubstring("connection refused")))
		Expect(pauseReason(err)).To(Equal(reasonInstallerImageUnresolved))
		Expect(jobs()).To(BeEmpty())
		Expect(installation().Status.InstallerImage).To(BeNil())
		Expect(images.verified).To(BeEmpty())
	})

	It("pauses with InstallerImageUnverified when the signature does not verify, and creates no Job", func() {
		withVersion("v0.1.0")
		createInstallation("v0.2.0")

		images := &fakeInstallerImages{digest: testInstallerDigest, verifyErr: errors.New("has no cosign signature")}

		_, err := NewIchorUpgradeAdvancer(k8sClient, k8sClient, jobNs, false, images, nil)(ctx, nil, nil, op())
		Expect(err).To(MatchError(ContainSubstring("has no cosign signature")))
		Expect(err).To(MatchError(ContainSubstring("--skip-signature-check")))
		Expect(pauseReason(err)).To(Equal(reasonInstallerImageUnverified))
		Expect(jobs()).To(BeEmpty())
		Expect(installation().Status.InstallerImage).To(BeNil())
	})

	It("refuses to run an installer image when there is no verifier at all", func() {
		withVersion("v0.1.0")
		createInstallation("v0.2.0")

		_, err := NewIchorUpgradeAdvancer(k8sClient, k8sClient, jobNs, false, nil, nil)(ctx, nil, nil, op())
		Expect(pauseReason(err)).To(Equal(reasonInstallerImageUnverified))
		Expect(jobs()).To(BeEmpty())
	})

	It("runs an unsigned image under skip, records Skipped with the reason, and warns", func() {
		withVersion("v0.1.0")
		inst := createInstallation("v0.2.0")
		inst.Spec.InstallerSignature = &ichorv1alpha1.IchorInstallerSignature{
			Mode: ichorv1alpha1.InstallerSignatureSkip, Reason: "demo build",
		}
		Expect(k8sClient.Update(ctx, inst)).To(Succeed())

		images := &fakeInstallerImages{digest: testInstallerDigest, verifyErr: errors.New("must not be called")}
		rec := record.NewFakeRecorder(5)

		_, err := NewIchorUpgradeAdvancer(k8sClient, k8sClient, jobNs, false, images, rec)(ctx, nil, nil, op())
		Expect(err).NotTo(HaveOccurred())

		Expect(images.verified).To(BeEmpty())
		Expect(jobs()).To(HaveLen(1))
		Expect(jobs()[0].Spec.Template.Spec.Containers[0].Image).To(Equal(testInstallerRepo + "@" + testInstallerDigest))

		img := installation().Status.InstallerImage
		Expect(img.Verification).To(Equal(ichorv1alpha1.InstallerImageSkipped))
		Expect(img.Reason).To(Equal("demo build"))
		Expect(img.Signer).To(BeEmpty())
		Expect(img.Digest).To(Equal(testInstallerDigest))

		Expect(rec.Events).To(Receive(And(
			ContainSubstring("Warning "+reasonInstallerSignatureSkipped),
			ContainSubstring("NOT checked"), ContainSubstring("demo build"), ContainSubstring(testInstallerDigest))))
	})

	It("still pauses when the tag does not resolve under skip", func() {
		withVersion("v0.1.0")
		inst := createInstallation("v0.2.0")
		inst.Spec.InstallerSignature = &ichorv1alpha1.IchorInstallerSignature{Mode: ichorv1alpha1.InstallerSignatureSkip, Reason: "x"}
		Expect(k8sClient.Update(ctx, inst)).To(Succeed())

		_, err := NewIchorUpgradeAdvancer(k8sClient, k8sClient, jobNs, false,
			&fakeInstallerImages{resolveErr: errors.New("no such tag")}, nil)(ctx, nil, nil, op())
		Expect(pauseReason(err)).To(Equal(reasonInstallerImageUnresolved))
		Expect(jobs()).To(BeEmpty())
	})

	It("pauses when spec.installerDigest changes while the Job exists", func() {
		withVersion("v0.1.0")
		createInstallation("v0.2.0")

		images := &fakeInstallerImages{digest: testInstallerDigest}
		advance := NewIchorUpgradeAdvancer(k8sClient, k8sClient, jobNs, false, images, nil)

		_, err := advance(ctx, nil, nil, op())
		Expect(err).NotTo(HaveOccurred())

		inst := installation()
		inst.Spec.InstallerDigest = "sha256:" + strings.Repeat("c", 64)
		Expect(k8sClient.Update(ctx, inst)).To(Succeed())

		_, err = advance(ctx, nil, nil, op())
		Expect(err).To(MatchError(ContainSubstring("spec.installerDigest is now")))
	})

	It("refuses a digest that is not sha256 and a skip without a reason (the CRD)", func() {
		inst := createInstallation("v0.2.0")

		inst.Spec.InstallerDigest = "sha512:abc"
		Expect(k8sClient.Update(ctx, inst)).NotTo(Succeed())

		inst = installation()
		inst.Spec.InstallerSignature = &ichorv1alpha1.IchorInstallerSignature{Mode: ichorv1alpha1.InstallerSignatureSkip}
		Expect(k8sClient.Update(ctx, inst)).To(MatchError(ContainSubstring("reason is required when mode is skip")))
	})
})

var _ = Describe("installer image registry egress", func() {
	BeforeEach(func() {
		ensureCiliumPolicyCRD()
		Expect(k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metaOf(releaseCheckNS)})).To(Or(Succeed(), MatchError(apierrors.IsAlreadyExists, "already exists")))
	})

	rule := func() (*unstructured.Unstructured, error) {
		u := cnpGVK()

		return u, k8sClient.Get(ctx, types.NamespacedName{Namespace: releaseCheckNS, Name: DefaultInstallerImagePolicyName}, u)
	}

	reconcileNow := func() {
		_, err := (&IchorInstallationReconciler{Client: k8sClient, Namespace: releaseCheckNS}).Reconcile(ctx,
			reconcile.Request{Name: ichorv1alpha1.IchorInstallationName})
		Expect(err).NotTo(HaveOccurred())
	}

	It("names the installer repository's host, and ghcr.io's blob host, only beside Ichor's controller policy", func() {
		inst := createInstallation("v0.1.0")
		inst.Spec.InstallerRepository = testGHCR + "/maeshinshin/ichor-installer"
		Expect(k8sClient.Update(ctx, inst)).To(Succeed())

		reconcileNow()

		_, err := rule()
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "no controller policy, so no rule")

		static := cnpGVK()
		static.SetNamespace(releaseCheckNS)
		static.SetName("ichor-controller-manager")
		static.Object[keySpec] = map[string]any{keyEndpointSelector: map[string]any{keyMatchLabels: map[string]any{
			controlPlaneLabel: valueControllerMgr, nameLabel: releaseCheckApp,
		}}, keyEgress: []any{map[string]any{}}}
		Expect(k8sClient.Create(ctx, static)).To(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, static))).To(Succeed()) })

		reconcileNow()

		u, err := rule()
		Expect(err).NotTo(HaveOccurred())

		egress, _, _ := unstructured.NestedSlice(u.Object, keySpec, keyEgress)
		Expect(egress).To(HaveLen(1))

		toFQDNs, _, _ := unstructured.NestedSlice(egress[0].(map[string]any), "toFQDNs")
		Expect(toFQDNs).To(ConsistOf(
			map[string]any{keyMatchName: testGHCR}, map[string]any{keyMatchName: ghcrBlobHost}))

		// A mirror names its own host and nothing else; the controller's own policies do not keep each other alive.
		inst = currentInstallation()
		inst.Spec.InstallerRepository = "registry.example.org/ichor/installer"
		Expect(k8sClient.Update(ctx, inst)).To(Succeed())
		reconcileNow()

		u, err = rule()
		Expect(err).NotTo(HaveOccurred())

		egress, _, _ = unstructured.NestedSlice(u.Object, keySpec, keyEgress)
		toFQDNs, _, _ = unstructured.NestedSlice(egress[0].(map[string]any), "toFQDNs")
		Expect(toFQDNs).To(ConsistOf(map[string]any{keyMatchName: "registry.example.org"}))

		Expect(k8sClient.Delete(ctx, static)).To(Succeed())
		reconcileNow()

		_, err = rule()
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "the rule goes when the controller policy does")
	})
})
