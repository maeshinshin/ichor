package v1alpha1

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ADR 0108: the controller's right to create Jobs is the right to run the installer, whose
// ServiceAccounts can create roles. The policy (config/admission/controller-jobs-policy.yaml), loaded
// as committed, limits what the controller's ServiceAccount may create to the two Jobs it builds.
var _ = Describe("controller Job policy", Ordered, func() {
	const (
		controllerUser = "system:serviceaccount:ichor-system:ichor-controller-manager"
		installerRepo  = "registry.example.org/ichor-installer"
		installerImage = installerRepo + "@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		cniImage       = installerRepo + ":v0.0.1"
		nsInstaller    = "ichor-system"
		nsCNI          = "kube-system"
		cniSub         = "cni"
		defaultName    = "default"
	)

	var admin, controller, other client.Client

	as := func(user string) client.Client {
		cfg := rest.CopyConfig(testEnv.Config)
		cfg.Impersonate = rest.ImpersonationConfig{UserName: user, Groups: []string{"system:masters", "system:authenticated"}}

		c, err := client.New(cfg, client.Options{Scheme: clientgoscheme.Scheme})
		Expect(err).NotTo(HaveOccurred())

		return c
	}

	hardened := func() *corev1.SecurityContext {
		return &corev1.SecurityContext{
			AllowPrivilegeEscalation: ptr.To(false), ReadOnlyRootFilesystem: ptr.To(true),
			Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		}
	}

	job := func(ns, sa string, ctr corev1.Container, deadline int64) *batchv1.Job {
		ctr.SecurityContext = hardened()

		return &batchv1.Job{
			GenerateName: "j-", Namespace: ns,
			Spec: batchv1.JobSpec{
				ActiveDeadlineSeconds: ptr.To(deadline),
				BackoffLimit:          ptr.To[int32](0),
				Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever, ServiceAccountName: sa, Containers: []corev1.Container{ctr},
				}},
			},
		}
	}

	installer := func() *batchv1.Job {
		return job(nsInstaller, "ichor-installer", corev1.Container{
			Name: "installer", Image: installerImage, Args: []string{"installer", "run", "--namespace=ichor-system"},
		}, 1800)
	}

	cniJob := func() *batchv1.Job {
		j := job(nsCNI, "ichor-cni", corev1.Container{
			Name: cniSub, Image: cniImage, Args: []string{cniSub, "helm", "apply", "--cluster=c"},
			Env:          []corev1.EnvVar{{Name: "KUBERNETES_SERVICE_HOST", Value: "localhost"}, {Name: "HOME", Value: "/tmp"}},
			VolumeMounts: []corev1.VolumeMount{{Name: "tmp", MountPath: "/tmp"}},
		}, 1500)
		j.Spec.Template.Spec.HostNetwork = true
		j.Spec.Template.Spec.Volumes = []corev1.Volume{{Name: "tmp", EmptyDir: &corev1.EmptyDirVolumeSource{}}}

		return j
	}

	BeforeAll(func() {
		ctx := context.Background()
		admin = as("admin")

		for _, ns := range []string{nsInstaller} {
			Expect(client.IgnoreAlreadyExists(admin.Create(ctx, &corev1.Namespace{Name: ns}))).To(Succeed())
		}

		data, err := os.ReadFile("../../config/admission/controller-jobs-policy.yaml")
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

		controller = as(controllerUser)
		other = as("system:serviceaccount:ichor-system:someone-else")

		// The policy is bound asynchronously: wait until it denies.
		Eventually(func() error {
			j := installer()
			j.Spec.Template.Spec.ServiceAccountName = defaultName

			return controller.Create(ctx, j)
		}, "30s", "100ms").ShouldNot(Succeed())
	})

	It("lets the controller create the installer Job and the Cilium Job", func() {
		Expect(controller.Create(context.Background(), installer())).To(Succeed())
		Expect(controller.Create(context.Background(), cniJob())).To(Succeed())
	})

	DescribeTable("refuses a Job that is not one the controller builds",
		func(mutate func(j *batchv1.Job), what string) {
			j := installer()
			mutate(j)

			err := controller.Create(context.Background(), j)
			Expect(err).To(HaveOccurred(), what)
			Expect(err.Error()).To(ContainSubstring("denied request"), what)
		},
		Entry("another ServiceAccount", func(j *batchv1.Job) { j.Spec.Template.Spec.ServiceAccountName = defaultName }, "serviceAccountName"),
		Entry("the controller's own ServiceAccount", func(j *batchv1.Job) { j.Spec.Template.Spec.ServiceAccountName = "ichor-controller-manager" }, "serviceAccountName"),
		Entry("the CNI ServiceAccount in the installer namespace", func(j *batchv1.Job) { j.Spec.Template.Spec.ServiceAccountName = "ichor-cni" }, "namespace pairing"),
		Entry("a command override", func(j *batchv1.Job) { j.Spec.Template.Spec.Containers[0].Command = []string{"/bin/sh"} }, "command"),
		Entry("other arguments", func(j *batchv1.Job) { j.Spec.Template.Spec.Containers[0].Args = []string{cniSub, "helm", "apply"} }, "args"),
		Entry("no arguments", func(j *batchv1.Job) { j.Spec.Template.Spec.Containers[0].Args = nil }, "args"),
		Entry("a second container", func(j *batchv1.Job) {
			j.Spec.Template.Spec.Containers = append(j.Spec.Template.Spec.Containers, corev1.Container{Name: "x", Image: installerImage})
		}, "containers"),
		Entry("an init container", func(j *batchv1.Job) {
			j.Spec.Template.Spec.InitContainers = []corev1.Container{{Name: "x", Image: installerImage}}
		}, "initContainers"),
		Entry("a hostPath volume", func(j *batchv1.Job) {
			j.Spec.Template.Spec.Volumes = []corev1.Volume{{Name: "h", HostPath: &corev1.HostPathVolumeSource{Path: "/"}}}
		}, "hostPath"),
		Entry("host networking for the installer", func(j *batchv1.Job) { j.Spec.Template.Spec.HostNetwork = true }, "hostNetwork"),
		Entry("hostPID", func(j *batchv1.Job) { j.Spec.Template.Spec.HostPID = true }, "hostPID"),
		Entry("privileged", func(j *batchv1.Job) {
			j.Spec.Template.Spec.Containers[0].SecurityContext.Privileged = ptr.To(true)
			j.Spec.Template.Spec.Containers[0].SecurityContext.AllowPrivilegeEscalation = ptr.To(true)
		}, "privileged"),
		Entry("privilege escalation", func(j *batchv1.Job) {
			j.Spec.Template.Spec.Containers[0].SecurityContext.AllowPrivilegeEscalation = ptr.To(true)
		}, "allowPrivilegeEscalation"),
		Entry("added capabilities", func(j *batchv1.Job) {
			j.Spec.Template.Spec.Containers[0].SecurityContext.Capabilities.Add = []corev1.Capability{"NET_ADMIN"}
		}, "capabilities"),
		Entry("an unexpected environment variable", func(j *batchv1.Job) {
			j.Spec.Template.Spec.Containers[0].Env = []corev1.EnvVar{{Name: "LD_PRELOAD", Value: "/x"}}
		}, "env"),
		Entry("no active deadline", func(j *batchv1.Job) { j.Spec.ActiveDeadlineSeconds = nil }, "deadline"),
		Entry("a day-long deadline", func(j *batchv1.Job) { j.Spec.ActiveDeadlineSeconds = ptr.To[int64](86400) }, "deadline"),
		Entry("a namespace of its own", func(j *batchv1.Job) { j.Namespace = defaultName }, "namespace"),
	)

	It("refuses a Cilium Job in the wrong namespace or with an extra volume kind", func() {
		j := cniJob()
		j.Namespace = nsInstaller
		Expect(controller.Create(context.Background(), j)).NotTo(Succeed())

		j = cniJob()
		j.Spec.Template.Spec.Volumes = append(j.Spec.Template.Spec.Volumes,
			corev1.Volume{Name: "s", Secret: &corev1.SecretVolumeSource{SecretName: "x"}})
		Expect(controller.Create(context.Background(), j)).NotTo(Succeed())
	})

	It("does not constrain any other identity", func() {
		j := installer()
		j.Spec.Template.Spec.ServiceAccountName = defaultName
		Expect(other.Create(context.Background(), j)).To(Succeed())
	})

	// The image follows IchorInstallation.spec (a mirror or private registry is a documented setting
	// that needs ichorinstallations/version); without one the structural rules above still hold.
	// The installer Job runs by digest (ADR 0122); the Cilium Job keeps repository:version.
	Context("with an IchorInstallation", func() {
		var inst *IchorInstallation

		BeforeAll(func() {
			inst = &IchorInstallation{Spec: IchorInstallationSpec{Version: "v0.0.1", InstallerRepository: installerRepo}}
			inst.Name = IchorInstallationName
			Expect(k8sClient.Create(context.Background(), inst)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), inst) })

			Eventually(func() error {
				j := installer()
				j.Spec.Template.Spec.Containers[0].Image = "evil.example/x@sha256:" + strings.Repeat("a", 64)

				return controller.Create(context.Background(), j)
			}, "30s", "100ms").ShouldNot(Succeed())
		})

		It("admits the configured repository by digest, for the installer", func() {
			Expect(controller.Create(context.Background(), installer())).To(Succeed())

			for _, image := range []string{
				"evil.example/x@sha256:" + strings.Repeat("a", 64),
				installerRepo + ":v0.0.1", // a tag, even the right one, can move
				installerRepo + ":v9.9.9",
				installerRepo,
				installerRepo + "@sha256:abc",
				installerRepo + "@sha256:" + strings.Repeat("a", 64) + "x",
			} {
				j := installer()
				j.Spec.Template.Spec.Containers[0].Image = image
				Expect(controller.Create(context.Background(), j)).NotTo(Succeed(), image)
			}
		})

		It("admits the Cilium Job at repository:version only", func() {
			Expect(controller.Create(context.Background(), cniJob())).To(Succeed())

			for _, image := range []string{"evil.example/x:v0.0.1", installerRepo + ":v9.9.9", installerRepo} {
				j := cniJob()
				j.Spec.Template.Spec.Containers[0].Image = image
				Expect(controller.Create(context.Background(), j)).NotTo(Succeed(), image)
			}
		})

		It("admits only spec.installerDigest once it is set", func() {
			pin := "sha256:" + strings.Repeat("b", 64)

			Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(inst), inst)).To(Succeed())
			inst.Spec.InstallerDigest = pin
			Expect(k8sClient.Update(context.Background(), inst)).To(Succeed())

			Eventually(func() error {
				return controller.Create(context.Background(), installer())
			}, "30s", "100ms").ShouldNot(Succeed())

			j := installer()
			j.Spec.Template.Spec.Containers[0].Image = installerRepo + "@" + pin
			Expect(controller.Create(context.Background(), j)).To(Succeed())
		})
	})
})
