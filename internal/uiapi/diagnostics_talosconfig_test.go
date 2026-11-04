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
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"reflect"
	"time"

	"connectrpc.com/connect"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

const (
	readerTCUser   = "rtc-holder"
	readerTCNoUser = "rtc-other"
)

// ADR 0124: the UI download of the os:reader talosconfig. The webhook (not running in this suite) makes the
// final check and records the Event; the handler asks the same question first to give a clear refusal.
var _ = Describe("Reader talosconfig download (ADR 0124)", func() {
	var cluster *ichorv1alpha1.TalosCluster

	BeforeEach(func() {
		group := ichorv1alpha1.GroupVersion.Group
		mint := rbacv1.PolicyRule{APIGroups: []string{group}, Resources: []string{"taloscredentialrequests"}, Verbs: []string{cniVerbCreate}}
		verb := rbacv1.PolicyRule{
			APIGroups: []string{group}, Resources: []string{resourceTalosClusters + "/" + TalosconfigReaderScope}, Verbs: []string{DownloadVerb},
		}

		for user, rules := range map[string][]rbacv1.PolicyRule{readerTCUser: {verb, mint}, readerTCNoUser: {mint}} {
			name := "rtc-test-" + user
			Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &rbacv1.ClusterRole{Name: name, Rules: rules}))).To(Succeed())
			Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &rbacv1.ClusterRoleBinding{
				Name:     name,
				RoleRef:  rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: reflect.TypeFor[rbacv1.ClusterRole]().Name(), Name: name},
				Subjects: []rbacv1.Subject{{APIGroup: rbacv1.GroupName, Kind: rbacv1.UserKind, Name: user}},
			}))).To(Succeed())
		}

		secret := newTestSecret(newTestBundle())
		cluster = newTestCluster(secret.Name)
		_ = newTestMachine(cluster.Name, strongTestIdentity("uuid-rtc-cp"), roleControlPlane, ichorv1alpha1.MachineApprovalStateApproved)
	})

	It("reports the permission per caller", func() {
		d, _, closeFn := newDiagnosticsServer(&fakeDownloadClient{}, k8sClient, nil)
		defer closeFn()

		for user, want := range map[string]bool{readerTCUser: true, readerTCNoUser: false} {
			resp, err := d.GetMyPermissions(ctx, asDiagUser(user, &pb.GetMyPermissionsRequest{Cluster: objectRef(cluster.Namespace, cluster.Name)}))
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.Msg.GetTalosconfigReader()).To(Equal(want), user)
		}
	})

	It("streams a talosconfig whose certificate is os:reader and lasts an hour by default", func() {
		d, _, closeFn := newDiagnosticsServer(&fakeDownloadClient{}, k8sClient, nil)
		defer closeFn()

		stream, err := d.DownloadReaderTalosconfig(ctx, asDiagUser(readerTCUser, &pb.DownloadReaderTalosconfigRequest{
			Cluster: objectRef(cluster.Namespace, cluster.Name),
		}))
		Expect(err).NotTo(HaveOccurred())

		name, data, err := collect(stream)
		Expect(err).NotTo(HaveOccurred())
		Expect(name).To(HavePrefix("talosconfig-reader-" + cluster.Name + "-"))

		cfg, err := clientconfig.FromBytes(data)
		Expect(err).NotTo(HaveOccurred())

		c := cfg.Contexts[cfg.Context]
		Expect(c.Endpoints).NotTo(BeEmpty())
		Expect(c.Key).NotTo(BeEmpty())

		crtPEM, err := base64.StdEncoding.DecodeString(c.Crt)
		Expect(err).NotTo(HaveOccurred())

		block, _ := pem.Decode(crtPEM)
		Expect(block).NotTo(BeNil())

		crt, err := x509.ParseCertificate(block.Bytes)
		Expect(err).NotTo(HaveOccurred())
		Expect(crt.Subject.Organization).To(ConsistOf("os:reader"))
		Expect(crt.NotAfter).To(BeTemporally("~", time.Now().Add(time.Hour), time.Minute))
	})

	It("honours a shorter lifetime and refuses more than twelve hours", func() {
		d, _, closeFn := newDiagnosticsServer(&fakeDownloadClient{}, k8sClient, nil)
		defer closeFn()

		stream, err := d.DownloadReaderTalosconfig(ctx, asDiagUser(readerTCUser, &pb.DownloadReaderTalosconfigRequest{
			Cluster: objectRef(cluster.Namespace, cluster.Name), LifetimeSeconds: 600,
		}))
		Expect(err).NotTo(HaveOccurred())

		_, data, err := collect(stream)
		Expect(err).NotTo(HaveOccurred())

		cfg, err := clientconfig.FromBytes(data)
		Expect(err).NotTo(HaveOccurred())

		crtPEM, _ := base64.StdEncoding.DecodeString(cfg.Contexts[cfg.Context].Crt)
		block, _ := pem.Decode(crtPEM)
		crt, err := x509.ParseCertificate(block.Bytes)
		Expect(err).NotTo(HaveOccurred())
		Expect(crt.NotAfter).To(BeTemporally("~", time.Now().Add(10*time.Minute), time.Minute))

		stream, err = d.DownloadReaderTalosconfig(ctx, asDiagUser(readerTCUser, &pb.DownloadReaderTalosconfigRequest{
			Cluster: objectRef(cluster.Namespace, cluster.Name), LifetimeSeconds: 13 * 3600,
		}))
		Expect(err).NotTo(HaveOccurred())

		_, data, err = collect(stream)
		Expect(connect.CodeOf(err)).To(Equal(connect.CodeInvalidArgument))
		Expect(data).To(BeEmpty())
	})

	It("refuses a caller without the verb and asks the controller for nothing", func() {
		d, _, closeFn := newDiagnosticsServer(&fakeDownloadClient{}, k8sClient, nil)
		defer closeFn()

		before := credentialRequestCount()

		stream, err := d.DownloadReaderTalosconfig(ctx, asDiagUser(readerTCNoUser, &pb.DownloadReaderTalosconfigRequest{
			Cluster: objectRef(cluster.Namespace, cluster.Name),
		}))
		Expect(err).NotTo(HaveOccurred())

		_, data, err := collect(stream)
		Expect(connect.CodeOf(err)).To(Equal(connect.CodePermissionDenied))
		Expect(data).To(BeEmpty())
		Expect(credentialRequestCount()).To(Equal(before), "a refused caller must not create a credential request")
	})
})

func credentialRequestCount() int {
	var list ichorv1alpha1.TalosCredentialRequestList
	Expect(k8sClient.List(ctx, &list)).To(Succeed())

	return len(list.Items)
}
