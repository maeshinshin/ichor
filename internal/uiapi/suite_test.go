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
	"context"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/envtestbin"
)

// This suite follows internal/controller/suite_test.go's own shape, per the
// task's instruction to match the existing envtest form rather than invent
// a second one.

var (
	ctx       context.Context
	cancel    context.CancelFunc
	testEnv   *envtest.Environment
	cfg       *rest.Config
	k8sClient client.Client
)

func TestUIAPI(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "UI API Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.TODO())

	var err error
	err = ichorv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: false,
	}

	dir, err := envtestbin.Dir(filepath.Join("..", ".."))
	if err != nil {
		logf.Log.Error(err, "no envtest binaries for the Makefile's Kubernetes version under bin/k8s")
	}
	testEnv.BinaryAssetsDirectory = dir

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

	startFakeController(ctx, k8sClient)
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	cancel()
	Eventually(func() error {
		return testEnv.Stop()
	}, time.Minute, time.Second).Should(Succeed())
})
