//nolint:goconst // the release fixtures repeat the same version strings on purpose
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

const (
	releaseCheckNS  = "release-check-test-ns"
	releaseCheckApp = "ichor"
)

// ensureCiliumPolicyCRD installs the CiliumNetworkPolicy CRD the envtest suite lacks (idempotent).
func ensureCiliumPolicyCRD() {
	const plural = "ciliumnetworkpolicies"

	crd := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apiextensions.k8s.io/v1", "kind": "CustomResourceDefinition",
		"metadata": map[string]any{labelName: plural + "." + ciliumPolicyGVK.Group},
		"spec": map[string]any{
			"group": ciliumPolicyGVK.Group, "scope": "Namespaced",
			"names": map[string]any{"kind": ciliumPolicyGVK.Kind, "listKind": ciliumPolicyGVK.Kind + "List", "plural": plural, "singular": "ciliumnetworkpolicy"},
			"versions": []any{map[string]any{labelName: ciliumPolicyGVK.Version, "served": true, "storage": true, "schema": map[string]any{
				"openAPIV3Schema": map[string]any{"type": "object", "x-kubernetes-preserve-unknown-fields": true},
			}}},
		},
	}}
	Expect(k8sClient.Create(ctx, crd)).To(Or(Succeed(), MatchError(apierrors.IsAlreadyExists, "already exists")))
}

var _ = Describe("ReleaseCheck", Ordered, func() {
	BeforeAll(func() {
		ensureCiliumPolicyCRD()
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

	installation := func() *ichorv1alpha1.IchorInstallation {
		var inst ichorv1alpha1.IchorInstallation
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}, &inst)).To(Succeed())

		return &inst
	}

	latest := func() *ichorv1alpha1.IchorLatestRelease { return installation().Status.LatestRelease }

	// switchOff turns the check off in the spec. The check wrote the status, so the object is read again first.
	switchOff := func() {
		inst := installation()
		inst.Spec.UpdateCheck = &ichorv1alpha1.IchorUpdateCheckSettings{Enabled: new(false)}
		Expect(k8sClient.Update(ctx, inst)).To(Succeed())
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

		switchOff()

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
		static.Object[keySpec] = map[string]any{"endpointSelector": map[string]any{"matchLabels": map[string]any{
			"control-plane": "controller-manager", nameLabel: releaseCheckApp,
		}}, keyEgress: []any{map[string]any{}}}
		Expect(k8sClient.Create(ctx, static)).To(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, static))).To(Succeed()) })

		Expect(newCheck(listing("0.2.0"), true).check(ctx)).To(BeTrue())

		rule := cnpGVK()
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: releaseCheckNS, Name: DefaultReleaseCheckPolicyName}, rule)).To(Succeed())

		egress, _, _ := unstructured.NestedSlice(rule.Object, keySpec, keyEgress)
		Expect(egress).To(HaveLen(1))

		host, err := releasecheck.Host(releasecheck.DefaultRepository)
		Expect(err).NotTo(HaveOccurred())

		toFQDNs, _, _ := unstructured.NestedSlice(egress[0].(map[string]any), "toFQDNs")
		Expect(toFQDNs).To(ConsistOf(map[string]any{keyMatchName: host}))

		Expect(newCheck(listing("0.2.0"), false).check(ctx)).To(BeTrue())
		err = k8sClient.Get(ctx, types.NamespacedName{Namespace: releaseCheckNS, Name: DefaultReleaseCheckPolicyName}, cnpGVK())
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "the rule goes with the switch")
	})

	// byRepo lists the given repositories; any other one fails, as an unreachable registry would.
	byRepo := func(tags map[string][]string) releasecheck.Lister {
		return func(repo string) ([]string, error) {
			if t, ok := tags[repo]; ok {
				return t, nil
			}

			return nil, errors.New("registry unreachable for " + repo)
		}
	}

	targets := func() *ichorv1alpha1.IchorUpgradeTargets { return installation().Status.UpgradeTargets }

	It("lists the Talos and Kubernetes releases at or above the floors, newest first", func() {
		createInstallation("v0.1.0")

		Expect(newCheck(byRepo(map[string][]string{
			releasecheck.DefaultRepository:           {"0.2.0"},
			releasecheck.DefaultTalosRepository:      {"v1.11.0", "v1.12.0", testTalosVersion, "v1.15.0-rc.1"},
			releasecheck.DefaultKubernetesRepository: {"v1.29.0", "v1.30.0", "v1.37.1", "v1.37.1-rc.1"},
		}), true).check(ctx)).To(BeTrue())

		ut := targets()
		Expect(ut).NotTo(BeNil())
		Expect(ut.Talos).To(Equal([]string{testTalosVersion, "v1.12.0"}))
		Expect(ut.Kubernetes).To(Equal([]string{"v1.37.1", "v1.30.0"}))
		Expect(ut.Error).To(BeEmpty())
		Expect(ut.CheckedAt).NotTo(BeNil())
	})

	It("keeps the last good list of a repository that fails, and says why", func() {
		createInstallation("v0.1.0")

		Expect(newCheck(byRepo(map[string][]string{
			releasecheck.DefaultRepository:           {"0.2.0"},
			releasecheck.DefaultTalosRepository:      {testTalosVersion},
			releasecheck.DefaultKubernetesRepository: {"v1.37.1"},
		}), true).check(ctx)).To(BeTrue())

		Expect(newCheck(byRepo(map[string][]string{
			releasecheck.DefaultRepository:      {"0.2.0"},
			releasecheck.DefaultTalosRepository: {"v1.14.4"},
		}), true).check(ctx)).To(BeTrue())

		ut := targets()
		Expect(ut.Talos).To(Equal([]string{"v1.14.4"}))
		Expect(ut.Kubernetes).To(Equal([]string{"v1.37.1"}), "the failed listing keeps its last good list")
		Expect(ut.Error).To(ContainSubstring("listing the Kubernetes releases"))
	})

	It("clears the upgrade lists while the switch is off", func() {
		createInstallation("v0.1.0")
		Expect(newCheck(byRepo(map[string][]string{
			releasecheck.DefaultRepository:           {"0.2.0"},
			releasecheck.DefaultTalosRepository:      {testTalosVersion},
			releasecheck.DefaultKubernetesRepository: {"v1.37.1"},
		}), true).check(ctx)).To(BeTrue())
		Expect(targets()).NotTo(BeNil())

		switchOff()

		Expect(newCheck(listing("0.9.0"), true).check(ctx)).To(BeTrue())
		Expect(targets()).To(BeNil())
	})

	It("waits for the singleton instead of losing the result", func() {
		Expect(newCheck(listing("0.2.0"), true).check(ctx)).To(BeFalse())
	})
})
