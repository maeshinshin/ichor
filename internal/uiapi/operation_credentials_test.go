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
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1/ichorv1alpha1connect"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
)

const (
	credAdminUser  = "cred-admin"
	credEditorUser = "cred-editor"
	credReaderUser = "cred-reader"

	certTalos = "talos"
)

func newOperationClientFor(s *Server) (ichorv1alpha1connect.OperationServiceClient, func()) {
	mux := http.NewServeMux()
	path, handler := ichorv1alpha1connect.NewOperationServiceHandler(s, connect.WithInterceptors(testIdentity{}))
	mux.Handle(path, handler)

	srv := httptest.NewServer(mux)

	return ichorv1alpha1connect.NewOperationServiceClient(http.DefaultClient, srv.URL), srv.Close
}

func credentialEvents(c *ichorv1alpha1.TalosCluster) []corev1.Event {
	var list corev1.EventList
	Expect(k8sClient.List(ctx, &list, client.InNamespace(c.Namespace))).To(Succeed())

	var out []corev1.Event

	for _, e := range list.Items {
		if e.InvolvedObject.Name == c.Name && e.InvolvedObject.Kind == kindTalosCluster && e.Reason == "CredentialsDownload" {
			out = append(out, e)
		}
	}

	return out
}

var _ = Describe("CA rotation credentials", func() {
	var (
		cluster *ichorv1alpha1.TalosCluster
		op      *ichorv1alpha1.TalosOperation
		rot     *secretsbundle.Rotation
	)

	newOp := func(stage string) *ichorv1alpha1.TalosOperation {
		o := &ichorv1alpha1.TalosOperation{
			GenerateName: "ca-", Namespace: testNamespace,
			Spec: ichorv1alpha1.TalosOperationSpec{
				ClusterRef: corev1.LocalObjectReference{Name: cluster.Name}, Kind: ichorv1alpha1.TalosOperationKindCARotation,
			},
		}
		Expect(k8sClient.Create(ctx, o)).To(Succeed())

		o.Status.CARotation = &ichorv1alpha1.TalosOperationCARotationStatus{
			Stage: stage,
			Certificates: []ichorv1alpha1.CARotationCertificate{
				{Name: componentK8s, Previous: "oldk", Next: "newk"}, {Name: certTalos, Previous: "oldt", Next: "newt"},
			},
		}
		Expect(k8sClient.Status().Update(ctx, o)).To(Succeed())

		return o
	}

	BeforeEach(func() {
		read := rbacv1.PolicyRule{
			APIGroups: []string{ichorv1alpha1.GroupVersion.Group},
			Resources: []string{caOpsResource, resourceTalosClusters, resourceTalosMachines},
			Verbs:     []string{"get", "list"},
		}
		write := rbacv1.PolicyRule{
			APIGroups: []string{ichorv1alpha1.GroupVersion.Group}, Resources: []string{caOpsResource}, Verbs: []string{"update"},
		}
		download := rbacv1.PolicyRule{
			APIGroups: []string{ichorv1alpha1.GroupVersion.Group}, Resources: []string{resourceTalosClusters + "/" + CredentialsScope}, Verbs: []string{DownloadVerb},
		}

		// The webhook (not running in this suite) decides which purposes a creator may ask for; the RBAC
		// rule only lets the user create the request.
		mint := rbacv1.PolicyRule{APIGroups: []string{ichorv1alpha1.GroupVersion.Group}, Resources: []string{"taloscredentialrequests"}, Verbs: []string{cniVerbCreate}}

		// admin: reads, updates, downloads. editor: reads and updates only. reader: reads only.
		for user, rules := range map[string][]rbacv1.PolicyRule{
			credAdminUser: {read, write, download, mint}, credEditorUser: {read, write, mint}, credReaderUser: {read},
		} {
			name := "cred-test-" + user
			Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &rbacv1.ClusterRole{Name: name, Rules: rules}))).To(Succeed())
			Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &rbacv1.ClusterRoleBinding{
				Name:     name,
				RoleRef:  rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: reflect.TypeFor[rbacv1.ClusterRole]().Name(), Name: name},
				Subjects: []rbacv1.Subject{{APIGroup: rbacv1.GroupName, Kind: rbacv1.UserKind, Name: user}},
			}))).To(Succeed())
		}

		contract, err := coreconfig.ParseContractFromVersion(testVersionContract)
		Expect(err).NotTo(HaveOccurred())

		rot, err = secretsbundle.NewRotation(time.Now(), contract)
		Expect(err).NotTo(HaveOccurred())

		raw, err := rot.Marshal()
		Expect(err).NotTo(HaveOccurred())

		secret := newTestSecret(newTestBundle())
		secret.Data[secretsbundle.RotationKey] = raw
		Expect(k8sClient.Update(ctx, secret)).To(Succeed())

		cluster = newTestCluster(secret.Name)
		_ = newTestMachine(cluster.Name, strongTestIdentity("uuid-cred-cp"), roleControlPlane, ichorv1alpha1.MachineApprovalStateApproved)
		op = newOp(ichorv1alpha1.CARotationStageAwaitingCredentials)
	})

	server := func(c client.Client, verify func(context.Context, []byte) error) (ichorv1alpha1connect.OperationServiceClient, func()) {
		return newOperationClientFor(&Server{
			Client: c, ControllerNamespace: testNamespace, VerifyKubeconfig: verify,
			Impersonate: NewImpersonator(cfg, client.Options{Scheme: scheme.Scheme}),
		})
	}

	download := func(c ichorv1alpha1connect.OperationServiceClient, user string, o *ichorv1alpha1.TalosOperation) (string, []byte, error) {
		s, err := c.DownloadCARotationCredentials(ctx, asDiagUser(user, &pb.DownloadCARotationCredentialsRequest{Operation: objectRef(o.Namespace, o.Name)}))
		if err != nil {
			return "", nil, err
		}

		return collect(s)
	}

	annotation := func(o *ichorv1alpha1.TalosOperation) string {
		var got ichorv1alpha1.TalosOperation
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(o), &got)).To(Succeed())

		return got.Annotations[ichorv1alpha1.CredentialsHandoffAnnotation]
	}

	It("reports the credentials download permission per caller", func() {
		d, _, closeFn := newDiagnosticsServer(&fakeDownloadClient{}, k8sClient, nil)
		defer closeFn()

		for user, want := range map[string]bool{credAdminUser: true, credEditorUser: false, credReaderUser: false} {
			resp, err := d.GetMyPermissions(ctx, asDiagUser(user, &pb.GetMyPermissionsRequest{Cluster: objectRef(cluster.Namespace, cluster.Name)}))
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.Msg.GetClusterCredentials()).To(Equal(want), user)
		}
	})

	Describe("download", func() {
		It("refuses a caller without the verb and records no Event", func() {
			c, closeFn := server(k8sClient, nil)
			defer closeFn()

			_, data, err := download(c, credEditorUser, op)
			Expect(connect.CodeOf(err)).To(Equal(connect.CodePermissionDenied))
			Expect(data).To(BeEmpty())
			Expect(credentialEvents(cluster)).To(BeEmpty())
		})

		It("refuses outside AwaitingCredentials..Finalizing and records no Event", func() {
			c, closeFn := server(k8sClient, nil)
			defer closeFn()

			for _, stage := range []string{ichorv1alpha1.CARotationStageAccepting, ichorv1alpha1.CARotationStageDone, ""} {
				o := newOp(stage)
				_, data, err := download(c, credAdminUser, o)
				Expect(connect.CodeOf(err)).To(Equal(connect.CodeFailedPrecondition), stage)
				Expect(data).To(BeEmpty())
			}

			Expect(credentialEvents(cluster)).To(BeEmpty())
		})

		It("refuses an operation that is not a ca-rotation", func() {
			other := &ichorv1alpha1.TalosOperation{
				GenerateName: "other-", Namespace: testNamespace,
				Spec: ichorv1alpha1.TalosOperationSpec{
					ClusterRef: corev1.LocalObjectReference{Name: cluster.Name}, Kind: ichorv1alpha1.TalosOperationKindShutdown,
				},
			}
			Expect(k8sClient.Create(ctx, other)).To(Succeed())

			c, closeFn := server(k8sClient, nil)
			defer closeFn()

			_, _, err := download(c, credAdminUser, other)
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeInvalidArgument))
		})

		It("is refused, producing nothing, when its audit Event cannot be recorded", func() {
			c, closeFn := server(eventFailingClient{k8sClient}, nil)
			defer closeFn()

			_, data, err := download(c, credAdminUser, op)
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeInternal))
			Expect(data).To(BeEmpty())
			Expect(credentialEvents(cluster)).To(BeEmpty())
		})

		It("streams a zip of a kubeconfig and a talosconfig that trust both CAs, and records an Event without content", func() {
			c, closeFn := server(k8sClient, nil)
			defer closeFn()

			for _, stage := range []string{ichorv1alpha1.CARotationStageAwaitingCredentials, ichorv1alpha1.CARotationStageIssuing} {
				o := op
				if stage != ichorv1alpha1.CARotationStageAwaitingCredentials {
					o = newOp(stage)
				}

				name, data, err := download(c, credAdminUser, o)
				Expect(err).NotTo(HaveOccurred())
				Expect(name).To(HavePrefix("credentials-" + cluster.Name + "-"))
				Expect(name).To(HaveSuffix(".zip"))

				zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
				Expect(err).NotTo(HaveOccurred())

				files := map[string][]byte{}

				for _, f := range zr.File {
					rc, err := f.Open()
					Expect(err).NotTo(HaveOccurred())

					files[f.Name], err = io.ReadAll(rc)
					Expect(err).NotTo(HaveOccurred())
					Expect(rc.Close()).To(Succeed())
					Expect(f.Mode().Perm()).To(Equal(os.FileMode(0o600)))
				}

				Expect(files).To(HaveLen(2))

				kc, err := clientcmd.Load(files["kubeconfig"])
				Expect(err).NotTo(HaveOccurred())
				Expect(kc.Clusters[cluster.Name].Server).To(Equal("https://10.0.0.5:6443"))
				Expect(strings.Count(string(kc.Clusters[cluster.Name].CertificateAuthorityData), "BEGIN CERTIFICATE")).To(Equal(2))
				Expect(string(kc.Clusters[cluster.Name].CertificateAuthorityData)).To(ContainSubstring(strings.TrimSpace(string(rot.Kubernetes.Crt))))
				Expect(files["talosconfig"]).NotTo(BeEmpty())
			}

			events := credentialEvents(cluster)
			Expect(events).To(HaveLen(2))

			messages := make([]string, 0, len(events))

			for _, e := range events {
				Expect(e.Type).To(Equal(corev1.EventTypeWarning))
				Expect(e.Message).To(ContainSubstring(credAdminUser))
				Expect(e.Message).NotTo(ContainSubstring("PRIVATE KEY"))
				Expect(e.Message).NotTo(ContainSubstring("BEGIN"))

				messages = append(messages, e.Message)
			}

			Expect(strings.Join(messages, "\n")).To(ContainSubstring("operation " + op.Name + ")"))
		})
	})

	Describe("acknowledge", func() {
		ack := func(c ichorv1alpha1connect.OperationServiceClient, user string, o *ichorv1alpha1.TalosOperation) error {
			_, err := c.AcknowledgeCARotationCredentials(ctx, asDiagUser(user, &pb.AcknowledgeCARotationCredentialsRequest{Operation: objectRef(o.Namespace, o.Name)}))

			return err
		}

		It("verifies the new kubeconfig, then sets the hand-off token on the operation", func() {
			var (
				calls atomic.Int32
				got   []byte
			)

			c, closeFn := server(k8sClient, func(_ context.Context, kc []byte) error {
				calls.Add(1)

				got = kc

				return nil
			})
			defer closeFn()

			Expect(ack(c, credEditorUser, op)).To(Succeed())
			Expect(calls.Load()).To(Equal(int32(1)))
			Expect(annotation(op)).To(Equal("kubernetes=newk,talos=newt"))
			Expect(annotation(op)).To(Equal(ichorv1alpha1.CARotationHandoffToken(op.Status.CARotation)))

			// What was checked is a kubeconfig that trusts the old and the new Kubernetes CA.
			kc, err := clientcmd.Load(got)
			Expect(err).NotTo(HaveOccurred())
			Expect(strings.Count(string(kc.Clusters[cluster.Name].CertificateAuthorityData), "BEGIN CERTIFICATE")).To(Equal(2))

			// It does not need a prior download.
			Expect(credentialEvents(cluster)).To(BeEmpty())
		})

		It("sets nothing when the new kubeconfig does not authenticate", func() {
			c, closeFn := server(k8sClient, func(context.Context, []byte) error {
				return errors.New("x509: certificate signed by unknown authority")
			})
			defer closeFn()

			err := ack(c, credEditorUser, op)
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeFailedPrecondition))
			Expect(err.Error()).To(ContainSubstring("NOT continuing"))
			Expect(annotation(op)).To(BeEmpty())
		})

		It("refuses outside AwaitingCredentials without verifying", func() {
			var calls atomic.Int32

			c, closeFn := server(k8sClient, func(context.Context, []byte) error { calls.Add(1); return nil })
			defer closeFn()

			for _, stage := range []string{ichorv1alpha1.CARotationStageAccepting, ichorv1alpha1.CARotationStageIssuing} {
				o := newOp(stage)
				Expect(connect.CodeOf(ack(c, credEditorUser, o))).To(Equal(connect.CodeFailedPrecondition), stage)
				Expect(annotation(o)).To(BeEmpty())
			}

			Expect(calls.Load()).To(BeZero())
		})

		It("is refused for a caller who may not update operations", func() {
			c, closeFn := server(k8sClient, func(context.Context, []byte) error { return nil })
			defer closeFn()

			Expect(connect.CodeOf(ack(c, credReaderUser, op))).To(Equal(connect.CodePermissionDenied))
			Expect(annotation(op)).To(BeEmpty())
		})
	})
})
