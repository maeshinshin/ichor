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
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
	authzv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	// +kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var (
	ctx       context.Context
	cancel    context.CancelFunc
	k8sClient client.Client
	cfg       *rest.Config
	testEnv   *envtest.Environment
)

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Webhook Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.TODO())

	var err error
	err = ichorv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	// +kubebuilder:scaffold:scheme

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: false,

		WebhookInstallOptions: envtest.WebhookInstallOptions{
			Paths: []string{filepath.Join("..", "..", "..", "config", "webhook")},
		},
	}

	// Retrieve the first found binary directory to allow running tests from IDEs
	if getFirstFoundEnvTestBinaryDir() != "" {
		testEnv.BinaryAssetsDirectory = getFirstFoundEnvTestBinaryDir()
	}

	// cfg is defined in this file globally.
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred(),
		"envtest found no etcd or kube-apiserver to run (KUBEBUILDER_ASSETS is unset or wrong). "+
			"Run `make setup-envtest` from the repository root, then rerun with "+
			"KUBEBUILDER_ASSETS=$(pwd)/bin/k8s/<version>-linux-amd64, for example 1.36.2-linux-amd64. "+
			"Use that version directory, not bin/k8s, which holds only version directories.")
	Expect(cfg).NotTo(BeNil())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	// start webhook server using Manager.
	webhookInstallOptions := &testEnv.WebhookInstallOptions
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
		WebhookServer: webhook.NewServer(webhook.Options{
			Host:    webhookInstallOptions.LocalServingHost,
			Port:    webhookInstallOptions.LocalServingPort,
			CertDir: webhookInstallOptions.LocalServingCertDir,
		}),
		LeaderElection: false,
		Metrics:        metricsserver.Options{BindAddress: "0"},
	})
	Expect(err).NotTo(HaveOccurred())

	err = SetupTalosClusterWebhookWithManager(mgr)
	Expect(err).NotTo(HaveOccurred())

	err = SetupTalosConfigPatchWebhookWithManager(mgr, testNamespace)
	Expect(err).NotTo(HaveOccurred())

	err = SetupTalosMachineWebhookWithManager(mgr)
	Expect(err).NotTo(HaveOccurred())

	err = SetupOverrideWebhookWithManager(mgr, OverrideOptions{
		ExemptUsers: []string{testControllerUser},
	})
	Expect(err).NotTo(HaveOccurred())

	err = SetupMachineReclaimWebhookWithManager(mgr)
	Expect(err).NotTo(HaveOccurred())

	err = SetupIchorUserWebhookWithManager(mgr, []string{testControllerUser})
	Expect(err).NotTo(HaveOccurred())

	// +kubebuilder:scaffold:webhook

	go func() {
		defer GinkgoRecover()
		err = mgr.Start(ctx)
		Expect(err).NotTo(HaveOccurred())
	}()

	// wait for the webhook server to get ready.
	dialer := &net.Dialer{Timeout: time.Second}
	addrPort := fmt.Sprintf("%s:%d", webhookInstallOptions.LocalServingHost, webhookInstallOptions.LocalServingPort)
	Eventually(func() error {
		conn, err := tls.DialWithDialer(dialer, "tcp", addrPort, &tls.Config{InsecureSkipVerify: true})
		if err != nil {
			return err
		}

		return conn.Close()
	}).Should(Succeed())
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	cancel()
	Eventually(func() error {
		return testEnv.Stop()
	}, time.Minute, time.Second).Should(Succeed())
})

// getFirstFoundEnvTestBinaryDir returns the bin/k8s directory of the Kubernetes minor that the
// Makefile's ENVTEST_K8S_VERSION names, the highest patch of it, as `setup-envtest use` would pick.
// It returns "" when there is none: envtest then fails and names KUBEBUILDER_ASSETS, rather than
// running another minor (1.34.0 sorts before 1.36.2, so the name order is not a choice of version).
//
// This function streamlines the process by finding the required binaries, similar to
// setting the 'KUBEBUILDER_ASSETS' environment variable. To ensure the binaries are
// properly set up, run 'make setup-envtest' beforehand.
func getFirstFoundEnvTestBinaryDir() string {
	basePath := filepath.Join("..", "..", "..", "bin", "k8s")
	entries, err := os.ReadDir(basePath)
	if err != nil {
		logf.Log.Error(err, "Failed to read directory", "path", basePath)
		return ""
	}
	want, err := envTestMinor(filepath.Join("..", "..", "..", "go.mod"))
	if err != nil {
		logf.Log.Error(err, "Failed to read the Kubernetes version from go.mod")
		return ""
	}
	best, bestVer := "", ""
	for _, entry := range entries {
		ver, _, _ := strings.Cut(entry.Name(), "-")
		v := "v" + ver
		if !entry.IsDir() || semver.MajorMinor(v) != want || (best != "" && semver.Compare(v, bestVer) <= 0) {
			continue
		}
		best, bestVer = entry.Name(), v
	}
	if best == "" {
		return ""
	}
	return filepath.Join(basePath, best)
}

// envTestMinor returns the minor of the Kubernetes version the Makefile's ENVTEST_K8S_VERSION names, as
// "v1.36". The Makefile takes it from the k8s.io/api version in go.mod (v0.36.6 gives 1.36), and so does
// this, following a replace the way `go list -m` does.
func envTestMinor(goMod string) (string, error) {
	data, err := os.ReadFile(goMod)
	if err != nil {
		return "", err
	}
	f, err := modfile.Parse(goMod, data, nil)
	if err != nil {
		return "", err
	}
	var v string
	for _, r := range f.Require {
		if r.Mod.Path == "k8s.io/api" {
			v = r.Mod.Version
		}
	}
	for _, r := range f.Replace {
		if r.Old.Path == "k8s.io/api" {
			v = r.New.Version
		}
	}
	if !semver.IsValid(v) {
		return "", fmt.Errorf("k8s.io/api in %s has no version to read the Kubernetes minor from (%q)", goMod, v)
	}
	return "v1." + strings.TrimPrefix(semver.MajorMinor(v), "v0."), nil
}

// schemeOf is a fresh scheme with the Ichor types and those of any adders named.
func schemeOf(t *testing.T, add ...func(*runtime.Scheme) error) *runtime.Scheme {
	t.Helper()

	s := runtime.NewScheme()
	for _, f := range append(add, ichorv1alpha1.AddToScheme) {
		if err := f(s); err != nil {
			t.Fatal(err)
		}
	}

	return s
}

// sarClient is a fake client holding objs that answers every SubjectAccessReview with answer(sar)
// and creates anything else as usual.
func sarClient(t *testing.T, answer func(*authzv1.SubjectAccessReview) bool, objs ...client.Object) client.Client {
	t.Helper()

	return fake.NewClientBuilder().WithScheme(schemeOf(t, scheme.AddToScheme)).WithObjects(objs...).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			sar, ok := obj.(*authzv1.SubjectAccessReview)
			if !ok {
				return cl.Create(ctx, obj, opts...)
			}

			sar.Status.Allowed = answer(sar)

			return nil
		},
	}).Build()
}
