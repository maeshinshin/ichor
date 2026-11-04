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

// TestRunAdopt_ZeroDrift is the headline guarantee (architecture.md,
// "Bootstrap"): a node with configuration no TalosConfigPatch yet describes
// ends up, after adopt, with a patch that -- applied on top of a fresh
// render -- reproduces that exact configuration. It is checked twice: once
// by buildAdoptPlan's own self-check (which runAdopt would have refused to
// create a patch over), and once more independently here by re-rendering
// the created patch and comparing bytes, so the test does not simply trust
// the code under test to grade itself.
func TestRunAdopt_ZeroDrift(t *testing.T) {
	bundle := newTestBundle(t)
	cluster := newTestCluster()
	machine := newTestMachine(cluster.Name)

	// What this node already has, from before Ichor ever knew about it.
	fixture := sysctlPatch(cluster.Name, map[string]string{testSysctlKey: "1"})

	currentCfg, _, err := render.Render(cluster, machine, bundle, []ichorv1alpha1.TalosConfigPatch{fixture}, nil)
	if err != nil {
		t.Fatalf("rendering current-config fixture: %v", err)
	}

	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cluster, machine).Build()

	var stdout, stderr bytes.Buffer

	deps := adoptDeps{
		K8s:       k8sClient,
		Bundle:    bundle,
		DialTalos: dialerFor(&fakeTalosReader{current: currentCfg}, nil),
		Stdout:    &stdout,
		Stderr:    &stderr,
		Stdin:     strings.NewReader(""),
		AssumeYes: true,
		Output:    outputYAML,
	}

	if err := runAdopt(context.Background(), cluster.Name, testNamespace, deps); err != nil {
		t.Fatalf("runAdopt: %v (stderr: %s)", err, stderr.String())
	}

	var created ichorv1alpha1.TalosConfigPatchList
	if err := k8sClient.List(context.Background(), &created); err != nil {
		t.Fatalf("listing created patches: %v", err)
	}

	if len(created.Items) != 1 {
		t.Fatalf("expected exactly one created TalosConfigPatch, got %d: %+v", len(created.Items), created.Items)
	}

	got := created.Items[0]
	if got.Name != "adopt-node-a" {
		t.Fatalf("created patch name = %q, want %q", got.Name, "adopt-node-a")
	}

	if got.Spec.Target == nil || got.Spec.Target.MachineRef == nil || got.Spec.Target.MachineRef.Name != machine.Name {
		t.Fatalf("created patch does not target machine %q: %+v", machine.Name, got.Spec.Target)
	}

	// Zero drift, re-verified independently of buildAdoptPlan's own check.
	rendered, _, err := render.Render(cluster, machine, bundle, []ichorv1alpha1.TalosConfigPatch{got}, nil)
	if err != nil {
		t.Fatalf("re-rendering with the created patch: %v", err)
	}

	if !bytes.Equal(rendered, currentCfg) {
		t.Fatalf("adopt did not achieve zero drift:\n--- current ---\n%s\n--- rendered with created patch ---\n%s",
			currentCfg, rendered)
	}

	if !strings.Contains(stdout.String(), "SysctlConfig") {
		t.Fatalf("expected the planned patch to be printed to stdout, got: %s", stdout.String())
	}
}

// TestRunAdopt_AlreadyInSync covers the other end: nothing to do when the
// node already matches a fresh render, and no TalosConfigPatch is created.
func TestRunAdopt_AlreadyInSync(t *testing.T) {
	bundle := newTestBundle(t)
	cluster := newTestCluster()
	machine := newTestMachine(cluster.Name)

	currentCfg, _, err := render.Render(cluster, machine, bundle, nil, nil)
	if err != nil {
		t.Fatalf("rendering current-config fixture: %v", err)
	}

	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cluster, machine).Build()

	var stdout, stderr bytes.Buffer

	deps := adoptDeps{
		K8s:       k8sClient,
		Bundle:    bundle,
		DialTalos: dialerFor(&fakeTalosReader{current: currentCfg}, nil),
		Stdout:    &stdout,
		Stderr:    &stderr,
		Stdin:     strings.NewReader(""),
		AssumeYes: true,
		Output:    outputYAML,
	}

	if err := runAdopt(context.Background(), cluster.Name, testNamespace, deps); err != nil {
		t.Fatalf("runAdopt: %v", err)
	}

	var created ichorv1alpha1.TalosConfigPatchList
	if err := k8sClient.List(context.Background(), &created); err != nil {
		t.Fatalf("listing created patches: %v", err)
	}

	if len(created.Items) != 0 {
		t.Fatalf("expected no TalosConfigPatch to be created, got %+v", created.Items)
	}

	if stdout.String() != "" {
		t.Fatalf("expected nothing printed to stdout when there is nothing to adopt, got: %s", stdout.String())
	}
}

// TestRunAdopt_Confirmation exercises --yes's gate: declining aborts and
// creates nothing, confirming creates.
func TestRunAdopt_Confirmation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		input     string
		wantErr   bool
		wantCount int
	}{
		{name: "declined", input: "n\n", wantErr: true, wantCount: 0},
		{name: "confirmed", input: "y\n", wantErr: false, wantCount: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bundle := newTestBundle(t)
			cluster := newTestCluster()
			machine := newTestMachine(cluster.Name)

			fixture := sysctlPatch(cluster.Name, map[string]string{testSysctlKey: "1"})

			currentCfg, _, err := render.Render(cluster, machine, bundle, []ichorv1alpha1.TalosConfigPatch{fixture}, nil)
			if err != nil {
				t.Fatalf("rendering current-config fixture: %v", err)
			}

			k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cluster, machine).Build()

			var stdout, stderr bytes.Buffer

			deps := adoptDeps{
				K8s:       k8sClient,
				Bundle:    bundle,
				DialTalos: dialerFor(&fakeTalosReader{current: currentCfg}, nil),
				Stdout:    &stdout,
				Stderr:    &stderr,
				Stdin:     strings.NewReader(tc.input),
				AssumeYes: false,
				Output:    outputYAML,
			}

			err = runAdopt(context.Background(), cluster.Name, testNamespace, deps)
			if tc.wantErr && err == nil {
				t.Fatal("runAdopt: expected an error, got nil")
			}

			if !tc.wantErr && err != nil {
				t.Fatalf("runAdopt: %v", err)
			}

			var created ichorv1alpha1.TalosConfigPatchList
			if err := k8sClient.List(context.Background(), &created); err != nil {
				t.Fatalf("listing created patches: %v", err)
			}

			if len(created.Items) != tc.wantCount {
				t.Fatalf("created %d patches, want %d", len(created.Items), tc.wantCount)
			}
		})
	}
}

// TestRunAdopt_OutputFormats covers both -o yaml and -o json.
func TestRunAdopt_OutputFormats(t *testing.T) {
	for _, format := range []string{outputYAML, outputJSON} {
		t.Run(format, func(t *testing.T) {
			bundle := newTestBundle(t)
			cluster := newTestCluster()
			machine := newTestMachine(cluster.Name)

			fixture := sysctlPatch(cluster.Name, map[string]string{testSysctlKey: "1"})

			currentCfg, _, err := render.Render(cluster, machine, bundle, []ichorv1alpha1.TalosConfigPatch{fixture}, nil)
			if err != nil {
				t.Fatalf("rendering current-config fixture: %v", err)
			}

			k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cluster, machine).Build()

			var stdout, stderr bytes.Buffer

			deps := adoptDeps{
				K8s:       k8sClient,
				Bundle:    bundle,
				DialTalos: dialerFor(&fakeTalosReader{current: currentCfg}, nil),
				Stdout:    &stdout,
				Stderr:    &stderr,
				Stdin:     strings.NewReader(""),
				AssumeYes: true,
				Output:    format,
			}

			if err := runAdopt(context.Background(), cluster.Name, testNamespace, deps); err != nil {
				t.Fatalf("runAdopt: %v", err)
			}

			if !strings.Contains(stdout.String(), "adopt-node-a") {
				t.Fatalf("expected output in %s format to mention the created patch's name, got: %s", format, stdout.String())
			}
		})
	}
}

// TestBuildAdoptPlan_LegacyDiffIsNeverAutoPatched proves the scope decision
// in adopt_plan.go's doc comment: a difference confined to the legacy
// Config document (here, cluster.apiServer.extraArgs) is reported as
// unrepresentable, not folded into a patch, because doing so would force
// restating machine.token (secret material) in the CR.
func TestBuildAdoptPlan_LegacyDiffIsNeverAutoPatched(t *testing.T) {
	bundle := newTestBundle(t)
	cluster := newTestCluster()
	machine := newTestMachine(cluster.Name)

	fixture := legacyConfigPatch(cluster.Name)

	currentCfg, _, err := render.Render(cluster, machine, bundle, []ichorv1alpha1.TalosConfigPatch{fixture}, nil)
	if err != nil {
		t.Fatalf("rendering current-config fixture: %v", err)
	}

	plan, err := buildAdoptPlan(cluster, machine, bundle, nil, currentCfg, nil, ownership{})
	if err != nil {
		t.Fatalf("buildAdoptPlan: %v", err)
	}

	if plan.Patch != nil {
		t.Fatalf("expected no patch for a legacy-document-only diff, got %+v", plan.Patch)
	}

	if len(plan.UnrepresentablePaths) == 0 {
		t.Fatal("expected the legacy diff's field paths to be reported as unrepresentable")
	}

	found := false

	for _, p := range plan.UnrepresentablePaths {
		if strings.HasPrefix(p, "cluster.") {
			found = true
		}
	}

	if !found {
		t.Fatalf("expected a cluster.* path among unrepresentable paths, got %v", plan.UnrepresentablePaths)
	}
}

func TestGroupChangedPaths(t *testing.T) {
	wantSysctl, wantKernelModule, legacy := groupChangedPaths([]string{
		"SysctlConfig.params.net\\.ipv4\\.ip_forward",
		"KernelModuleConfig.name",
		"cluster.network.cni.name",
		"version",
	})

	if !wantSysctl {
		t.Error("expected wantSysctl = true")
	}

	if !wantKernelModule {
		t.Error("expected wantKernelModule = true")
	}

	if len(legacy) != 2 {
		t.Errorf("legacy = %v, want 2 entries", legacy)
	}
}

// TestRunAdopt_SkipsMachinesThatAreNotApproved: a pending or rejected machine is in maintenance
// mode (or otherwise has no credentials), so dialing it fails with a TLS error. adopt must not
// dial it, must say it skipped it, and must still adopt the approved machines.
func TestRunAdopt_SkipsMachinesThatAreNotApproved(t *testing.T) {
	bundle := newTestBundle(t)
	cluster := newTestCluster()
	machine := newTestMachine(cluster.Name)

	pending := newTestMachine(cluster.Name)
	pending.Name = "node-pending"
	pending.Spec.Endpoint = "10.0.0.99:50000"
	pending.Spec.Role = ""
	pending.Spec.ApprovalState = ichorv1alpha1.MachineApprovalStatePending

	rejected := newTestMachine(cluster.Name)
	rejected.Name = "node-rejected"
	rejected.Spec.Endpoint = "10.0.0.98:50000"
	rejected.Spec.ApprovalState = ichorv1alpha1.MachineApprovalStateRejected

	currentCfg, _, err := render.Render(cluster, machine, bundle, nil, nil)
	if err != nil {
		t.Fatalf("rendering current-config fixture: %v", err)
	}

	dialed := map[string]bool{}
	dial := func(_ context.Context, endpoint string) (talosConfigReader, error) {
		dialed[endpoint] = true

		if endpoint != machine.Spec.Endpoint {
			return nil, errors.New("tls: certificate required (maintenance mode)")
		}

		return &fakeTalosReader{current: currentCfg}, nil
	}

	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cluster, machine, pending, rejected).Build()

	var stdout, stderr bytes.Buffer

	deps := adoptDeps{
		K8s: k8sClient, Bundle: bundle, DialTalos: dial,
		Stdout: &stdout, Stderr: &stderr, Stdin: strings.NewReader(""), AssumeYes: true, Output: outputYAML,
	}

	if err := runAdopt(context.Background(), cluster.Name, testNamespace, deps); err != nil {
		t.Fatalf("runAdopt: %v (stderr: %s)", err, stderr.String())
	}

	if dialed[pending.Spec.Endpoint] || dialed[rejected.Spec.Endpoint] {
		t.Fatalf("adopt dialed a machine that is not approved: %v", dialed)
	}

	for _, name := range []string{"node-pending", "node-rejected"} {
		if !strings.Contains(stderr.String(), `machine "`+name+`": skipped`) {
			t.Fatalf("stderr does not say %s was skipped:\n%s", name, stderr.String())
		}
	}
}

// TestReportPlans_ExplainsUnrepresentablePaths: a v1.14 field that lives in its own document is
// named with that document and why adopt cannot capture it, not just listed as "cannot patch".
func TestReportPlans_ExplainsUnrepresentablePaths(t *testing.T) {
	var out bytes.Buffer

	reportPlans(adoptDeps{Stderr: &out}.say, []*adoptPlan{{
		Machine: "node-a",
		UnrepresentablePaths: []string{
			"KubeNetworkConfig.podSubnets", "KubeNetworkConfig.serviceSubnets", "UnattendedInstallConfig.installer.image",
			"KubeFlannelCNIConfig.backendType",
			"machine.time.servers",
		},
	}})

	for _, want := range []string{
		"KubeNetworkConfig.podSubnets: set TalosCluster spec.network.podSubnets",
		"KubeNetworkConfig.serviceSubnets: set TalosCluster spec.network.serviceSubnets",
		"UnattendedInstallConfig.installer.image: set TalosMachine spec.install",
		"KubeFlannelCNIConfig.backendType: the CNI selection has no spec field",
		"write a TalosConfigPatch for the other fields by hand",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, out.String())
		}
	}
}

// The legacy paths of a pre-1.14 node name the same spec fields.
func TestExplainUnrepresentable_LegacyPaths(t *testing.T) {
	got := strings.Join(explainUnrepresentable([]string{
		"cluster.network.podSubnets", "cluster.network.serviceSubnets", "machine.install.disk", "cluster.network.cni.name",
	}), "\n")

	for _, want := range []string{"spec.network.podSubnets", "spec.network.serviceSubnets", "spec.install", "ADR 0103"} {
		if !strings.Contains(got, want) {
			t.Errorf("legacy report lacks %q:\n%s", want, got)
		}
	}

	if strings.Contains(got, "by hand") {
		t.Errorf("every path above has a spec field, but the report says to write a patch:\n%s", got)
	}
}
