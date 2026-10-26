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

package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/render"
)

// TestAdoptNeverLeaksSecrets forces nine routes through runAdopt/
// buildAdoptPlan -- every one this test can reach that has real bundle
// material loaded nearby, including the two routes that succeed and
// actually produce a TalosConfigPatch -- and asserts none of it appears in
// stdout, stderr or any error message. This is the same discipline
// pkg/render's TestRenderBundleLeak and internal/controller's
// TestTalosMachineBundleLeak apply to their own boundaries; adopt is the
// one place in the CLI that holds a real secrets.Bundle in memory, so it
// gets the same treatment.
func TestAdoptNeverLeaksSecrets(t *testing.T) {
	bundle := newTestBundle(t)
	material := bundleSecretMaterial(bundle)

	assertNoLeak := func(t *testing.T, stdout, stderr string, err error) {
		t.Helper()

		haystacks := []string{stdout, stderr}
		if err != nil {
			haystacks = append(haystacks, err.Error())
		}

		for _, s := range material {
			if s == "" {
				continue
			}

			for _, h := range haystacks {
				if strings.Contains(h, s) {
					t.Fatalf("output contains bundle secret material:\nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
				}
			}
		}
	}

	cluster := newTestCluster()
	machine := newTestMachine(cluster.Name)

	t.Run("cluster not found", func(t *testing.T) {
		k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()

		var stdout, stderr bytes.Buffer

		deps := adoptDeps{
			K8s: k8sClient, Bundle: bundle,
			DialTalos: dialerFor(&fakeTalosReader{}, nil),
			Stdout:    &stdout, Stderr: &stderr, Stdin: strings.NewReader(""),
			AssumeYes: true, Output: outputYAML,
		}

		err := runAdopt(context.Background(), cluster.Name, testNamespace, deps)
		if err == nil {
			t.Fatal("expected an error, got nil")
		}

		assertNoLeak(t, stdout.String(), stderr.String(), err)
	})

	t.Run("dial failed", func(t *testing.T) {
		k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cluster, machine).Build()

		var stdout, stderr bytes.Buffer

		deps := adoptDeps{
			K8s: k8sClient, Bundle: bundle,
			DialTalos: dialerFor(nil, errors.New("connection refused")),
			Stdout:    &stdout, Stderr: &stderr, Stdin: strings.NewReader(""),
			AssumeYes: true, Output: outputYAML,
		}

		err := runAdopt(context.Background(), cluster.Name, testNamespace, deps)
		if err == nil {
			t.Fatal("expected an error, got nil")
		}

		assertNoLeak(t, stdout.String(), stderr.String(), err)
	})

	t.Run("current config read failed", func(t *testing.T) {
		k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cluster, machine).Build()

		var stdout, stderr bytes.Buffer

		deps := adoptDeps{
			K8s: k8sClient, Bundle: bundle,
			DialTalos: dialerFor(&fakeTalosReader{currentErr: errors.New("read failed")}, nil),
			Stdout:    &stdout, Stderr: &stderr, Stdin: strings.NewReader(""),
			AssumeYes: true, Output: outputYAML,
		}

		err := runAdopt(context.Background(), cluster.Name, testNamespace, deps)
		if err == nil {
			t.Fatal("expected an error, got nil")
		}

		assertNoLeak(t, stdout.String(), stderr.String(), err)
	})

	t.Run("current config malformed", func(t *testing.T) {
		k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cluster, machine).Build()

		var stdout, stderr bytes.Buffer

		deps := adoptDeps{
			K8s: k8sClient, Bundle: bundle,
			DialTalos: dialerFor(&fakeTalosReader{current: []byte("{{{not valid yaml")}, nil),
			Stdout:    &stdout, Stderr: &stderr, Stdin: strings.NewReader(""),
			AssumeYes: true, Output: outputYAML,
		}

		err := runAdopt(context.Background(), cluster.Name, testNamespace, deps)
		if err == nil {
			t.Fatal("expected an error, got nil")
		}

		assertNoLeak(t, stdout.String(), stderr.String(), err)
	})

	t.Run("zero-drift success (sysctl)", func(t *testing.T) {
		k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cluster, machine).Build()

		fixture := sysctlPatch(cluster.Name, map[string]string{testSysctlKey: "1"})

		currentCfg, _, err := render.Render(cluster, machine, bundle, []ichorv1alpha1.TalosConfigPatch{fixture}, nil)
		if err != nil {
			t.Fatalf("rendering current-config fixture: %v", err)
		}

		var stdout, stderr bytes.Buffer

		deps := adoptDeps{
			K8s: k8sClient, Bundle: bundle,
			DialTalos: dialerFor(&fakeTalosReader{current: currentCfg}, nil),
			Stdout:    &stdout, Stderr: &stderr, Stdin: strings.NewReader(""),
			AssumeYes: true, Output: outputYAML,
		}

		err = runAdopt(context.Background(), cluster.Name, testNamespace, deps)
		if err != nil {
			t.Fatalf("runAdopt: %v", err)
		}

		assertNoLeak(t, stdout.String(), stderr.String(), err)
	})

	t.Run("zero-drift success (json output)", func(t *testing.T) {
		k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cluster, machine).Build()

		fixture := sysctlPatch(cluster.Name, map[string]string{testSysctlKey: "1"})

		currentCfg, _, err := render.Render(cluster, machine, bundle, []ichorv1alpha1.TalosConfigPatch{fixture}, nil)
		if err != nil {
			t.Fatalf("rendering current-config fixture: %v", err)
		}

		var stdout, stderr bytes.Buffer

		deps := adoptDeps{
			K8s: k8sClient, Bundle: bundle,
			DialTalos: dialerFor(&fakeTalosReader{current: currentCfg}, nil),
			Stdout:    &stdout, Stderr: &stderr, Stdin: strings.NewReader(""),
			AssumeYes: true, Output: outputJSON,
		}

		err = runAdopt(context.Background(), cluster.Name, testNamespace, deps)
		if err != nil {
			t.Fatalf("runAdopt: %v", err)
		}

		assertNoLeak(t, stdout.String(), stderr.String(), err)
	})

	t.Run("legacy diff reported, not patched", func(t *testing.T) {
		k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cluster, machine).Build()

		fixture := legacyConfigPatch(cluster.Name)

		currentCfg, _, err := render.Render(cluster, machine, bundle, []ichorv1alpha1.TalosConfigPatch{fixture}, nil)
		if err != nil {
			t.Fatalf("rendering current-config fixture: %v", err)
		}

		var stdout, stderr bytes.Buffer

		deps := adoptDeps{
			K8s: k8sClient, Bundle: bundle,
			DialTalos: dialerFor(&fakeTalosReader{current: currentCfg}, nil),
			Stdout:    &stdout, Stderr: &stderr, Stdin: strings.NewReader(""),
			AssumeYes: true, Output: outputYAML,
		}

		err = runAdopt(context.Background(), cluster.Name, testNamespace, deps)
		if err != nil {
			t.Fatalf("runAdopt: %v", err)
		}

		assertNoLeak(t, stdout.String(), stderr.String(), err)
	})

	t.Run("confirmation declined", func(t *testing.T) {
		k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cluster, machine).Build()

		fixture := sysctlPatch(cluster.Name, map[string]string{testSysctlKey: "1"})

		currentCfg, _, err := render.Render(cluster, machine, bundle, []ichorv1alpha1.TalosConfigPatch{fixture}, nil)
		if err != nil {
			t.Fatalf("rendering current-config fixture: %v", err)
		}

		var stdout, stderr bytes.Buffer

		deps := adoptDeps{
			K8s: k8sClient, Bundle: bundle,
			DialTalos: dialerFor(&fakeTalosReader{current: currentCfg}, nil),
			Stdout:    &stdout, Stderr: &stderr, Stdin: strings.NewReader("n\n"),
			AssumeYes: false, Output: outputYAML,
		}

		err = runAdopt(context.Background(), cluster.Name, testNamespace, deps)
		if err == nil {
			t.Fatal("expected an error, got nil")
		}

		assertNoLeak(t, stdout.String(), stderr.String(), err)
	})

	t.Run("invalid output format", func(t *testing.T) {
		k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cluster, machine).Build()

		fixture := sysctlPatch(cluster.Name, map[string]string{testSysctlKey: "1"})

		currentCfg, _, err := render.Render(cluster, machine, bundle, []ichorv1alpha1.TalosConfigPatch{fixture}, nil)
		if err != nil {
			t.Fatalf("rendering current-config fixture: %v", err)
		}

		var stdout, stderr bytes.Buffer

		deps := adoptDeps{
			K8s: k8sClient, Bundle: bundle,
			DialTalos: dialerFor(&fakeTalosReader{current: currentCfg}, nil),
			Stdout:    &stdout, Stderr: &stderr, Stdin: strings.NewReader(""),
			AssumeYes: true, Output: "toml",
		}

		err = runAdopt(context.Background(), cluster.Name, testNamespace, deps)
		if err == nil {
			t.Fatal("expected an error, got nil")
		}

		assertNoLeak(t, stdout.String(), stderr.String(), err)
	})
}
