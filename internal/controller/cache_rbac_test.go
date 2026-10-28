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
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	batchv1 "k8s.io/api/batch/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// The manager's cached client starts a cluster-wide list and watch the first time any kind is
// read through it. envtest's kube-apiserver enforces RBAC, so these specs build the manager's
// cache (CacheOptions) as the controller's identity bound to the generated role
// (config/rbac/role.yaml, from the +kubebuilder:rbac markers) and demand that every kind the
// controller reads through the cached client syncs. A namespaced-only grant read through an
// unscoped informer ("jobs.batch is forbidden ... at the cluster scope", ADR 0081) fails here
// instead of on a live cluster.
const clusterRoleKind = "ClusterRole"

var _ = Describe("manager cache against the generated RBAC", func() {
	const (
		user       = "system:serviceaccount:ichor-system:ichor-controller-manager"
		controlNS  = "ichor-system"
		syncBudget = 15 * time.Second
	)

	var ca cache.Cache

	BeforeEach(func() {
		Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &corev1.Namespace{
			Name: controlNS,
		}))).To(Succeed())

		raw, err := os.ReadFile("../../config/rbac/role.yaml")
		Expect(err).NotTo(HaveOccurred())

		// The controller's namespaced Role for the Cilium Helm Job in kube-system (hand-written).
		jobRole, err := os.ReadFile("../../config/rbac/ichor-cni/controller_role.yaml")
		Expect(err).NotTo(HaveOccurred())

		for doc := range strings.SplitSeq(string(raw)+"\n---\n"+string(jobRole), "\n---\n") {
			var meta struct{ Kind string }
			Expect(yaml.Unmarshal([]byte(doc), &meta)).To(Succeed())

			subject := []rbacv1.Subject{{APIGroup: rbacv1.GroupName, Kind: "User", Name: user}}

			switch meta.Kind {
			case clusterRoleKind:
				var cr rbacv1.ClusterRole
				Expect(yaml.Unmarshal([]byte(doc), &cr)).To(Succeed())
				cr.Name = "manager-role-test"
				Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &cr))).To(Succeed())
				Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &rbacv1.ClusterRoleBinding{
					Name:     "manager-binding-test",
					RoleRef:  rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: clusterRoleKind, Name: cr.Name},
					Subjects: subject,
				}))).To(Succeed())
			case "RoleBinding":
			case "Role":
				var r rbacv1.Role
				Expect(yaml.Unmarshal([]byte(doc), &r)).To(Succeed())
				r.Name = "manager-role-test-" + r.Namespace
				Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &r))).To(Succeed())
				Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &rbacv1.RoleBinding{
					Name: "manager-binding-test", Namespace: r.Namespace,
					RoleRef:  rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: r.Name},
					Subjects: subject,
				}))).To(Succeed())
			}
		}

		c := rest.CopyConfig(cfg)
		c.Impersonate = rest.ImpersonationConfig{UserName: user}

		opts := CacheOptions(controlNS)
		opts.Scheme = scheme.Scheme

		ca, err = cache.New(c, opts)
		Expect(err).NotTo(HaveOccurred())

		cacheCtx, stop := context.WithCancel(ctx)
		DeferCleanup(stop)

		go func() { _ = ca.Start(cacheCtx) }()
	})

	// Every kind the reconcilers and executors read through mgr.GetClient(). Pods, DaemonSets
	// and Deployments are deliberately absent: they are read through the uncached
	// APIReader or a direct client, which only needs get.
	DescribeTable("syncs an informer for",
		func(obj client.Object) {
			sctx, cancelSync := context.WithTimeout(ctx, syncBudget)
			defer cancelSync()

			_, err := ca.GetInformer(sctx, obj)
			Expect(err).NotTo(HaveOccurred())
		},
		Entry("Job (installer, namespaced-only grant)", &batchv1.Job{}),
		Entry("Secret (namespaced-only grant)", &corev1.Secret{}),
		Entry("ConfigMap (namespaced-only grant)", &corev1.ConfigMap{}),
		Entry("Service (namespaced-only grant)", &corev1.Service{}),
		Entry("IchorUser", &ichorv1alpha1.IchorUser{}),
		Entry("Node", &corev1.Node{}),
		Entry("Lease", &coordinationv1.Lease{}),
		Entry("TalosCluster", &ichorv1alpha1.TalosCluster{}),
		Entry("TalosMachine", &ichorv1alpha1.TalosMachine{}),
		Entry("TalosOperation", &ichorv1alpha1.TalosOperation{}),
		Entry("TalosConfigPatch", &ichorv1alpha1.TalosConfigPatch{}),
		Entry("TalosStorage", &ichorv1alpha1.TalosStorage{}),
		Entry("TalosEtcdBackup", &ichorv1alpha1.TalosEtcdBackup{}),
		Entry("IchorInstallation", &ichorv1alpha1.IchorInstallation{}),
	)
})
