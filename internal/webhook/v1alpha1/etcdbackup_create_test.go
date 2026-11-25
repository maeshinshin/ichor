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

	"github.com/maeshinshin/ichor/internal/etcdbackup"
)

// The objects `ichor create etcd-backup` builds are what the API server and the admission webhook admit.
var _ = Describe("The objects of ichor create etcd-backup", func() {
	const (
		creator    = "ebcreate-admin"
		ebEndpoint = "https://s3.example.org"
		ebResource = "talosetcdbackups"
		controller = "ichor-system"
	)

	newObjects := func(name string) etcdbackup.NewBackup {
		return etcdbackup.NewBackup{
			Name: name, Namespace: testNamespace, Cluster: testClusterName,
			Endpoint: ebEndpoint, Bucket: "ichor-backups", Prefix: testClusterName, SecretName: name + "-s3",
		}
	}

	BeforeEach(func() {
		bindRole("ebcreate-admin", creator, rbacv1.PolicyRule{
			APIGroups: []string{ichorGroup}, Resources: []string{ebResource}, Verbs: []string{verbGet, verbCreate},
		}, rbacv1.PolicyRule{
			APIGroups: []string{ichorGroup}, Resources: []string{"talosetcdbackups/secret-references"}, Verbs: []string{SecretReferencesVerb},
		})
		Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &corev1.Namespace{Name: controller}))).To(Succeed())
	})

	It("is admitted, and the Secret in the controller's namespace holds the keys the controller reads", func() {
		o := newObjects("eb-created")
		Expect(o.Validate()).To(Succeed())

		sec := etcdbackup.CredentialsSecret(controller, o.SecretName, etcdbackup.S3Config{
			AccessKeyID: "AKIAEXAMPLE", SecretAccessKey: "example-secret", SessionToken: "tok",
		})
		Expect(k8sClient.Create(ctx, sec)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, sec)).To(Succeed()) })

		bk := o.Backup()
		Expect(asUser(creator).Create(ctx, bk)).To(Succeed())
		DeferCleanup(func() { forceDelete(bk) })

		Expect(bk.Spec.Schedule).To(Equal("0 * * * *"))
		Expect(bk.Spec.Retention.KeepLast).To(BeEquivalentTo(24))

		var got corev1.Secret
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: controller, Name: bk.Spec.Destination.S3.CredentialsSecretRef.Name}, &got)).To(Succeed())
		Expect(got.Data).To(HaveKey("accessKeyID"))
		Expect(got.Data).To(HaveKey("secretAccessKey"))
		Expect(got.Data).To(HaveKey("sessionToken"))
	})

	It("refuses a second backup on the same endpoint, bucket and prefix: retention would prune the other's snapshots", func() {
		first := newObjects("eb-first")
		Expect(asUser(creator).Create(ctx, first.Backup())).To(Succeed())
		DeferCleanup(func() { forceDelete(first.Backup()) })

		second := newObjects("eb-second")
		Expect(asUser(creator).Create(ctx, second.Backup())).To(MatchError(ContainSubstring("already writes")))

		other := newObjects("eb-other-prefix")
		other.Prefix += "-daily"
		Expect(asUser(creator).Create(ctx, other.Backup())).To(Succeed())
		DeferCleanup(func() { forceDelete(other.Backup()) })
	})

	It("needs the secret-references verb: an operator with only create is refused", func() {
		bindRole("ebcreate-operator", "ebcreate-operator", rbacv1.PolicyRule{
			APIGroups: []string{ichorGroup}, Resources: []string{ebResource}, Verbs: []string{verbGet, verbCreate},
		})

		err := asUser("ebcreate-operator").Create(ctx, newObjects("eb-no-verb").Backup())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("talosetcdbackups/secret-references"))
	})

	// The CLI refuses these first (Validate); built without it, the API server's layers refuse them too.
	DescribeTable("is refused by the API server when the CLI's own check is skipped",
		func(mutate func(*etcdbackup.NewBackup), want string) {
			o := newObjects("eb-refused")
			mutate(&o)

			err := asUser(creator).Create(ctx, o.Backup())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(want))
		},
		Entry("an http:// endpoint without insecure", func(o *etcdbackup.NewBackup) { o.Endpoint = "http://minio.example.org:9000" }, "insecure"),
		Entry("a missing bucket", func(o *etcdbackup.NewBackup) { o.Bucket = "" }, "bucket"),
		Entry("a prefix with a leading slash", func(o *etcdbackup.NewBackup) { o.Prefix = "/prod" }, "prefix"),
	)

	It("is admitted over http with insecure", func() {
		o := newObjects("eb-http")
		o.Endpoint, o.Insecure = "http://minio.example.org:9000", true
		Expect(o.Validate()).To(Succeed())

		bk := o.Backup()
		Expect(asUser(creator).Create(ctx, bk)).To(Succeed())
		DeferCleanup(func() { forceDelete(bk) })
	})
})
