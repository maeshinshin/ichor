package v1alpha1

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// The ichor-ui credential policy (config/admission/reclaim-credential-policy.yaml), loaded as
// committed, with requests made as the ichor-ui ServiceAccount.
var _ = Describe("ichor-ui reclaim credential policy", Ordered, func() {
	const ns = "ichor-system"

	var admin, ui, other client.Client

	secret := func(name string, typ corev1.SecretType, labelled bool, data map[string][]byte) *corev1.Secret {
		s := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}, Type: typ, Data: data}
		if labelled {
			s.Labels = map[string]string{"ichor.maesh.dev/reclaim": "true"}
		}

		return s
	}

	as := func(user string) client.Client {
		cfg := rest.CopyConfig(testEnv.Config)
		cfg.Impersonate = rest.ImpersonationConfig{UserName: user, Groups: []string{"system:masters", "system:authenticated"}}

		c, err := client.New(cfg, client.Options{Scheme: clientgoscheme.Scheme})
		Expect(err).NotTo(HaveOccurred())

		return c
	}

	const credType = corev1.SecretType("ichor.maesh.dev/foreign-talosconfig")

	good := map[string][]byte{"talosconfig": []byte("x")}

	BeforeAll(func() {
		ctx := context.Background()
		admin = as("admin")
		Expect(client.IgnoreAlreadyExists(admin.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: ns},
		}))).To(Succeed())

		data, err := os.ReadFile("../../config/admission/reclaim-credential-policy.yaml")
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

			if obj != nil {
				Expect(admin.Create(ctx, &unstructured.Unstructured{Object: obj})).To(Succeed())
			}
		}

		ui = as("system:serviceaccount:ichor-system:ichor-ui")
		other = as("system:serviceaccount:ichor-system:someone-else")

		// The policy is bound asynchronously: wait until it denies.
		Eventually(func() error {
			// A fresh name each time: an early, unenforced attempt that succeeds must not turn
			// into AlreadyExists, which would look like a denial.
			p := secret("", corev1.SecretTypeOpaque, false, good)
			p.GenerateName = "probe-"

			return ui.Create(ctx, p)
		}, "30s", "100ms").ShouldNot(Succeed())
	})

	It("lets ichor-ui create the credential Secret", func() {
		Expect(ui.Create(context.Background(), secret("ok", credType, true, good))).To(Succeed())
	})

	It("refuses another type, a missing label and extra or missing keys from ichor-ui", func() {
		ctx := context.Background()

		Expect(ui.Create(ctx, secret("t", corev1.SecretTypeOpaque, true, good))).NotTo(Succeed())
		Expect(ui.Create(ctx, secret("l", credType, false, good))).To(MatchError(ContainSubstring("label")))
		Expect(ui.Create(ctx, secret("k", credType, true, map[string][]byte{"talosconfig": []byte("x"), "more": []byte("y")}))).NotTo(Succeed())
		Expect(ui.Create(ctx, secret("n", credType, true, map[string][]byte{"other": []byte("y")}))).NotTo(Succeed())
		Expect(ui.Create(ctx, secret("e", credType, true, nil))).NotTo(Succeed())
	})

	It("does not constrain any other identity", func() {
		Expect(other.Create(context.Background(), secret("free", corev1.SecretTypeOpaque, false, good))).To(Succeed())
	})
})
