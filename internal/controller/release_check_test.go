package controller

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/releasecheck"
	"github.com/maeshinshin/ichor/internal/settings"
)

const releaseCheckNS = "release-check-test-ns"

var _ = Describe("ReleaseCheck", Ordered, func() {
	BeforeAll(func() {
		crd := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "apiextensions.k8s.io/v1", "kind": "CustomResourceDefinition",
			"metadata": map[string]any{labelName: "ciliumnetworkpolicies.cilium.io"},
			"spec": map[string]any{
				"group": "cilium.io", "scope": "Namespaced",
				"names": map[string]any{"kind": "CiliumNetworkPolicy", "listKind": "CiliumNetworkPolicyList", "plural": "ciliumnetworkpolicies", "singular": "ciliumnetworkpolicy"},
				"versions": []any{map[string]any{labelName: "v2", "served": true, "storage": true, "schema": map[string]any{
					"openAPIV3Schema": map[string]any{"type": "object", "x-kubernetes-preserve-unknown-fields": true},
				}}},
			},
		}}
		Expect(k8sClient.Create(ctx, crd)).To(Or(Succeed(), MatchError(apierrors.IsAlreadyExists, "already exists")))
		Expect(k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metaOf(releaseCheckNS)})).To(Or(Succeed(), MatchError(apierrors.IsAlreadyExists, "already exists")))
	})

	// newCheck is the controller with the lister given; the switch falls back to the flag value.
	newCheck := func(list releasecheck.Lister, flag bool) *ReleaseCheck {
		return &ReleaseCheck{
			Client: k8sClient, Reader: k8sClient,
			Settings:  &settings.Reader{Client: k8sClient, Fallback: settings.Fallback{UpdateCheck: flag}},
			Namespace: releaseCheckNS, Repository: releasecheck.DefaultRepository, List: list,
		}
	}

	listing := func(tags ...string) releasecheck.Lister {
		return func(string) ([]string, error) { return tags, nil }
	}

	failing := func(string) ([]string, error) { return nil, errors.New("registry unreachable") }

	latest := func() *ichorv1alpha1.IchorLatestRelease {
		var inst ichorv1alpha1.IchorInstallation
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}, &inst)).To(Succeed())

		return inst.Status.LatestRelease
	}

	It("records the newest final release and when it was checked", func() {
		createInstallation("v0.1.0")

		Expect(newCheck(listing("0.2.0", "0.3.0-rc.1", "0.1.0"), true).check(ctx)).To(BeTrue())

		rel := latest()
		Expect(rel).NotTo(BeNil())
		Expect(rel.Version).To(Equal("v0.2.0"))
		Expect(rel.CheckedAt).NotTo(BeNil())
		Expect(rel.Error).To(BeEmpty())
	})

	It("keeps the last good version when a check fails, and says why", func() {
		createInstallation("v0.1.0")

		Expect(newCheck(listing("0.2.0"), true).check(ctx)).To(BeTrue())
		Expect(newCheck(failing, true).check(ctx)).To(BeTrue())

		rel := latest()
		Expect(rel.Version).To(Equal("v0.2.0"))
		Expect(rel.Error).To(ContainSubstring("registry unreachable"))
	})

	It("clears the result and removes the egress rule while the switch is off", func() {
		createInstallation("v0.1.0")
		Expect(newCheck(listing("0.2.0"), true).check(ctx)).To(BeTrue())
		Expect(latest()).NotTo(BeNil())

		// The check wrote the status, so read the object again before the spec change.
		var inst ichorv1alpha1.IchorInstallation
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}, &inst)).To(Succeed())
		inst.Spec.UpdateCheck = &ichorv1alpha1.IchorUpdateCheckSettings{Enabled: new(false)}
		Expect(k8sClient.Update(ctx, &inst)).To(Succeed())

		Expect(newCheck(listing("0.9.0"), true).check(ctx)).To(BeTrue())
		Expect(latest()).To(BeNil())
	})

	It("writes the registry rule only beside Ichor's controller policy", func() {
		createInstallation("v0.1.0")

		Expect(newCheck(listing("0.2.0"), true).check(ctx)).To(BeTrue())

		err := k8sClient.Get(ctx, types.NamespacedName{Namespace: releaseCheckNS, Name: DefaultReleaseCheckPolicyName}, cnpGVK())
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "no controller policy, so no rule")

		static := cnpGVK()
		static.SetNamespace(releaseCheckNS)
		static.SetName("ichor-controller-manager")
		static.Object["spec"] = map[string]any{"endpointSelector": map[string]any{"matchLabels": map[string]any{
			"control-plane": "controller-manager", "app.kubernetes.io/name": "ichor",
		}}, "egress": []any{map[string]any{}}}
		Expect(k8sClient.Create(ctx, static)).To(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, static))).To(Succeed()) })

		Expect(newCheck(listing("0.2.0"), true).check(ctx)).To(BeTrue())

		rule := cnpGVK()
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: releaseCheckNS, Name: DefaultReleaseCheckPolicyName}, rule)).To(Succeed())
		Expect(rule.Object).To(HaveKeyWithValue("spec", HaveKey("egress")))

		egress, _, _ := unstructured.NestedSlice(rule.Object, "spec", "egress")
		Expect(egress).To(HaveLen(1))

		toFQDNs, _, _ := unstructured.NestedSlice(egress[0].(map[string]any), "toFQDNs")
		Expect(toFQDNs).To(ConsistOf(map[string]any{"matchName": "ghcr.io"}))

		Expect(newCheck(listing("0.2.0"), false).check(ctx)).To(BeTrue())
		err = k8sClient.Get(ctx, types.NamespacedName{Namespace: releaseCheckNS, Name: DefaultReleaseCheckPolicyName}, cnpGVK())
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "the rule goes with the switch")
	})

	It("waits for the singleton instead of losing the result", func() {
		Expect(newCheck(listing("0.2.0"), true).check(ctx)).To(BeFalse())
	})
})
