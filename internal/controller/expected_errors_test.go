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
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-logr/logr/funcr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus/testutil"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// These specs drive the real failures against the envtest API server: a ValidatingWebhookConfiguration
// that points at a closed port (failurePolicy Fail) gives the same "failed calling webhook" error the
// controller gets while its own webhook is not up, and an update with a stale resourceVersion gives the
// conflict. They check what a manager logs, which is where controller-runtime writes the line.
var _ = Describe("quietExpected", func() {
	var (
		ns   string
		logs *syncBuffer
	)

	// run starts a manager whose one controller updates the ConfigMap it is asked about, with the stale
	// resourceVersion when stale is set, behind quietExpectedSince(start). It returns the number of
	// reconciles so far.
	run := func(start time.Time, stale bool) (calls func() int32) {
		var n atomic.Int32

		mgr, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme:     scheme.Scheme,
			Metrics:    metricsserver.Options{BindAddress: "0"},
			Logger:     funcr.New(logs.write, funcr.Options{}),
			Cache:      cache.Options{DefaultNamespaces: map[string]cache.Config{ns: {}}},
			Controller: config.Controller{SkipNameValidation: ptr.To(true)},
		})
		Expect(err).NotTo(HaveOccurred())

		r := reconcile.Func(func(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
			n.Add(1)

			var cm corev1.ConfigMap
			if err := mgr.GetClient().Get(ctx, req.NamespacedName, &cm); err != nil {
				return reconcile.Result{}, client.IgnoreNotFound(err)
			}

			if stale {
				cm.ResourceVersion = "1"
			}

			cm.Data = map[string]string{"n": "x"}

			return reconcile.Result{}, mgr.GetClient().Update(ctx, &cm)
		})
		Expect(ctrl.NewControllerManagedBy(mgr).For(&corev1.ConfigMap{}).Named(ns).
			Complete(quietExpectedSince(r, ns, start, time.Now))).To(Succeed())

		mctx, stop := context.WithCancel(ctx)
		DeferCleanup(stop)

		go func() { defer GinkgoRecover(); Expect(mgr.Start(mctx)).To(Succeed()) }()

		Expect(k8sClient.Create(ctx, &corev1.ConfigMap{Name: "cm", Namespace: ns})).To(Succeed())

		return n.Load
	}

	BeforeEach(func() {
		logs = &syncBuffer{}
		DeferCleanup(func() { GinkgoWriter.Print(logs.String()) })
		namespace := &corev1.Namespace{GenerateName: "quiet-"}
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
		ns = namespace.Name
	})

	withWebhookDown := func() {
		cfgName := "quiet-" + ns
		Expect(k8sClient.Create(ctx, &admissionregistrationv1.ValidatingWebhookConfiguration{
			Name: cfgName,
			Webhooks: []admissionregistrationv1.ValidatingWebhook{{
				Name:                    "quiet.ichor.maesh.dev",
				AdmissionReviewVersions: []string{"v1"},
				SideEffects:             ptr.To(admissionregistrationv1.SideEffectClassNone),
				FailurePolicy:           ptr.To(admissionregistrationv1.Fail),
				TimeoutSeconds:          ptr.To(int32(2)),
				ClientConfig:            admissionregistrationv1.WebhookClientConfig{URL: ptr.To("https://127.0.0.1:1/validate")},
				NamespaceSelector:       &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": ns}},
				Rules: []admissionregistrationv1.RuleWithOperations{{
					Operations: []admissionregistrationv1.OperationType{admissionregistrationv1.Update},
					APIGroups:  []string{""}, APIVersions: []string{"v1"}, Resources: []string{"configmaps"},
				}},
			}},
		})).To(Succeed())
		DeferCleanup(func() {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &admissionregistrationv1.ValidatingWebhookConfiguration{
				Name: cfgName}))).To(Succeed())
		})
	}

	It("logs a webhook that cannot be called as expected within the startup window", func() {
		withWebhookDown()

		calls := run(time.Now(), false)

		Eventually(logs.String, 20*time.Second, 100*time.Millisecond).Should(ContainSubstring("Admission webhook not reachable yet"))
		Expect(logs.String()).To(ContainSubstring("failed calling webhook"))
		Expect(logs.String()).NotTo(ContainSubstring("Reconciler error"))
		Eventually(calls, 20*time.Second, 100*time.Millisecond).Should(BeNumerically(">=", 2), "the reconcile is retried")
		Expect(testutil.ToFloat64(expectedErrors.WithLabelValues(ns, "webhook_startup"))).To(BeNumerically(">=", 1))
	})

	It("logs the same failure as a reconciler error once the startup window is over", func() {
		withWebhookDown()

		run(time.Now().Add(-startupWindow-time.Second), false)

		Eventually(logs.String, 20*time.Second, 100*time.Millisecond).Should(ContainSubstring("Reconciler error"))
		Expect(logs.String()).To(ContainSubstring("failed calling webhook"))
		Expect(logs.String()).NotTo(ContainSubstring("not reachable yet"))
		Expect(testutil.ToFloat64(expectedErrors.WithLabelValues(ns, "webhook_startup"))).To(BeZero(), "a real failure is not counted as expected")
	})

	It("logs a conflict as expected, whatever the time", func() {
		calls := run(time.Now().Add(-startupWindow-time.Second), true)

		Eventually(logs.String, 20*time.Second, 100*time.Millisecond).Should(ContainSubstring("Write conflicted with a newer version"))
		Expect(logs.String()).To(ContainSubstring("the object has been modified"))
		Expect(logs.String()).NotTo(ContainSubstring("Reconciler error"))
		Eventually(calls, 20*time.Second, 100*time.Millisecond).Should(BeNumerically(">=", 2), "the reconcile is retried")
		Expect(testutil.ToFloat64(expectedErrors.WithLabelValues(ns, "conflict"))).To(BeNumerically(">=", 1))
	})

	It("returns any other error, and a webhook that answered, unchanged", func() {
		for _, err := range []error{
			errors.New("boom"),
			apierrors.NewForbidden(corev1.Resource("configmaps"), "cm", errors.New(`admission webhook "x" denied the request: failed calling webhook`)),
			apierrors.NewInvalid(corev1.SchemeGroupVersion.WithKind("ConfigMap").GroupKind(), "cm", field.ErrorList{field.Invalid(field.NewPath("spec"), "x", "failed calling webhook")}),
		} {
			r := quietExpectedSince(reconcile.Func(func(context.Context, reconcile.Request) (reconcile.Result, error) {
				return reconcile.Result{}, err
			}), "x", time.Now(), time.Now)

			_, got := r.Reconcile(ctx, reconcile.Request{})
			Expect(got).To(MatchError(err))
		}
	})
})

// syncBuffer collects log lines from a manager's goroutines.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) write(prefix, args string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.b.WriteString(prefix + args + "\n")
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.b.String()
}
