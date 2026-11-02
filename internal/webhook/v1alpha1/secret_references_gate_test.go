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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
)

const (
	resClusters   = "talosclusters"
	verbCreate    = "create"
	verbPatch     = "patch"
	verbUpdate    = "update"
	lwCluster     = "west"
	verbDelete    = "delete"
	resOperations = "talosoperations"
	secrefNewK8s  = "v1.36.1"
)

// Setting or changing a field that names a Secret the controller reads with its own
// privileges needs update on <resource>/secret-references.
var _ = Describe("Secret-references gate", func() {
	const (
		operator = "secref-operator"
		admin    = "secref-admin"
	)

	BeforeEach(func() {
		edit := rbacv1.PolicyRule{
			APIGroups: []string{ichorGroup}, Resources: []string{resClusters, "talosetcdbackups"},
			Verbs: []string{verbGet, verbCreate, SecretReferencesVerb, verbPatch},
		}
		bindRole("secref-operator", operator, edit)
		bindRole("secref-admin", admin, edit, rbacv1.PolicyRule{
			APIGroups: []string{ichorGroup}, Verbs: []string{SecretReferencesVerb},
			Resources: []string{"talosclusters/secret-references", "talosetcdbackups/secret-references"},
		})
		bindRole("secref-controller", testControllerUser, edit)
	})

	Describe("on TalosCluster", func() {
		var cluster *ichorv1alpha1.TalosCluster

		cleanup := func(c *ichorv1alpha1.TalosCluster) {
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(c), c); err != nil {
				return
			}

			c.Annotations = map[string]string{deleteguard.AllowDeleteAnnotation: deleteguard.AllowDeleteValue}
			Expect(k8sClient.Update(ctx, c)).To(Succeed())
			Expect(k8sClient.Delete(ctx, c)).To(Succeed())
		}

		BeforeEach(func() {
			cluster = newTestCluster()
			cluster.Spec.Registries = []ichorv1alpha1.TalosClusterRegistry{{
				Host: "registry.example.org", Endpoints: []string{"https://registry.example.org"},
				AuthSecretRef: &corev1.LocalObjectReference{Name: "reg-a"},
			}}
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
		})

		AfterEach(func() { cleanup(cluster) })

		change := func(user string, mutate func(*ichorv1alpha1.TalosCluster)) error {
			c := &ichorv1alpha1.TalosCluster{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), c)).To(Succeed())
			mutate(c)

			return asUser(user).Update(ctx, c)
		}

		fields := map[string]func(*ichorv1alpha1.TalosCluster){
			"spec.secretsRef":                 func(c *ichorv1alpha1.TalosCluster) { c.Spec.SecretsRef.Name = "other" },
			"spec.registries[].authSecretRef": func(c *ichorv1alpha1.TalosCluster) { c.Spec.Registries[0].AuthSecretRef.Name = "reg-b" },
			// The credential goes to whatever endpoint the mirror names.
			"spec.registries[].endpoints": func(c *ichorv1alpha1.TalosCluster) {
				c.Spec.Registries[0].Endpoints = []string{"https://evil.example"}
			},
		}

		for name, mutate := range fields {
			It("refuses an operator who changes "+name, func() {
				err := change(operator, mutate)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("talosclusters/secret-references"))
				Expect(err.Error()).To(ContainSubstring(name))
			})

			It("admits an admin who changes "+name, func() {
				Expect(change(admin, mutate)).To(Succeed())
			})

			It("admits the controller changing "+name, func() {
				Expect(change(testControllerUser, mutate)).To(Succeed())
			})
		}

		It("admits an operator who leaves every reference as it was", func() {
			Expect(change(operator, func(c *ichorv1alpha1.TalosCluster) { c.Spec.KubernetesVersion = secrefNewK8s })).To(Succeed())
		})

		It("admits an operator who removes a registry's authSecretRef", func() {
			Expect(change(operator, func(c *ichorv1alpha1.TalosCluster) { c.Spec.Registries[0].AuthSecretRef = nil })).To(Succeed())
		})

		It("refuses an operator who creates a cluster with a secretsRef", func() {
			err := asUser(operator).Create(ctx, newTestCluster())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("talosclusters/secret-references"))
		})

		It("admits an admin who creates a cluster with a secretsRef", func() {
			c := newTestCluster()
			Expect(asUser(admin).Create(ctx, c)).To(Succeed())
			DeferCleanup(func() { cleanup(c) })
		})
	})

	Describe("on TalosEtcdBackup", func() {
		var backup *ichorv1alpha1.TalosEtcdBackup

		newBackup := func(secret string) *ichorv1alpha1.TalosEtcdBackup {
			return &ichorv1alpha1.TalosEtcdBackup{
				GenerateName: "backup-", Namespace: testNamespace,
				Spec: ichorv1alpha1.TalosEtcdBackupSpec{
					ClusterRef: corev1.LocalObjectReference{Name: testClusterName},
					Destination: ichorv1alpha1.TalosEtcdBackupDestination{S3: ichorv1alpha1.TalosEtcdBackupS3{
						Endpoint: "https://s3.example.org", Bucket: "bucket", Prefix: "etcd",
						CredentialsSecretRef: corev1.LocalObjectReference{Name: secret},
					}},
				},
			}
		}

		BeforeEach(func() {
			backup = newBackup("s3-a")
			Expect(k8sClient.Create(ctx, backup)).To(Succeed())
			DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, backup))).To(Succeed()) })
		})

		change := func(user, secret string) error {
			b := &ichorv1alpha1.TalosEtcdBackup{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(backup), b)).To(Succeed())
			b.Spec.Destination.S3.CredentialsSecretRef.Name = secret

			return asUser(user).Update(ctx, b)
		}

		It("refuses an operator who repoints credentialsSecretRef", func() {
			err := change(operator, "s3-b")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("talosetcdbackups/secret-references"))
		})

		It("admits an admin, the controller, and an unchanged value", func() {
			Expect(change(admin, "s3-b")).To(Succeed())
			Expect(change(testControllerUser, "s3-c")).To(Succeed())
			Expect(change(operator, "s3-c")).To(Succeed())
		})

		// The endpoint receives the credentials, so it is gated like the Secret name.
		mutateS3 := map[string]func(*ichorv1alpha1.TalosEtcdBackupS3){
			"endpoint": func(s *ichorv1alpha1.TalosEtcdBackupS3) { s.Endpoint = "https://evil.example.org" },
			"bucket":   func(s *ichorv1alpha1.TalosEtcdBackupS3) { s.Bucket = "other-bucket" },
		}

		for name, mutate := range mutateS3 {
			It("refuses an operator who changes s3."+name+", and admits an admin", func() {
				for user, ok := range map[string]bool{operator: false, admin: true, testControllerUser: true} {
					b := &ichorv1alpha1.TalosEtcdBackup{}
					Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(backup), b)).To(Succeed())
					mutate(&b.Spec.Destination.S3)

					err := asUser(user).Update(ctx, b)
					if ok {
						Expect(err).NotTo(HaveOccurred(), user)
					} else {
						Expect(err).To(HaveOccurred(), user)
						Expect(err.Error()).To(ContainSubstring("talosetcdbackups/secret-references"))
					}
				}
			})
		}

		It("refuses a plain http endpoint unless insecure is set, even for an admin", func() {
			b := newBackup("s3-x")
			b.Spec.Destination.S3.Endpoint = "http://minio.minio.svc:9000"

			err := asUser(admin).Create(ctx, b)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("insecure"))

			b = newBackup("s3-x")
			b.Spec.Destination.S3.Endpoint = "http://minio.minio.svc:9000"
			b.Spec.Destination.S3.Insecure = true
			Expect(asUser(admin).Create(ctx, b)).To(Succeed())
			Expect(k8sClient.Delete(ctx, b)).To(Succeed())
		})

		It("refuses switching an existing backup to http without insecure, and leaves an untouched http backup editable", func() {
			b := &ichorv1alpha1.TalosEtcdBackup{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(backup), b)).To(Succeed())
			b.Spec.Destination.S3.Endpoint = "http://s3.example.org"
			Expect(asUser(admin).Update(ctx, b)).NotTo(Succeed())

			b.Spec.Destination.S3.Insecure = true
			Expect(asUser(admin).Update(ctx, b)).To(Succeed())

			// Only the keep-last style edit: nothing to gate, nothing to refuse.
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(backup), b)).To(Succeed())
			b.Spec.Destination.S3.Insecure = true
			Expect(asUser(operator).Update(ctx, b)).To(Succeed())
		})

		It("refuses an operator who creates a backup, and admits an admin", func() {
			err := asUser(operator).Create(ctx, newBackup("s3-x"))
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("talosetcdbackups/secret-references"))

			b := newBackup("s3-x")
			Expect(asUser(admin).Create(ctx, b)).To(Succeed())
			Expect(k8sClient.Delete(ctx, b)).To(Succeed())
		})
	})
})
