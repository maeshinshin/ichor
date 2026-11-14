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
	"os"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/controller"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
	"github.com/maeshinshin/ichor/pkg/release"
)

func withTempHome(t *testing.T) string {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)

	return home
}

// TestRunInit_Success exercises steps 1-5 end to end against fakes: the
// node is applied to insecurely, etcd is bootstrapped, a kubeconfig is
// fetched and saved, and the TalosCluster, TalosMachine, secrets bundle
// Secret and the CNI's bootstrap-manifest TalosConfigPatch all land in
// the (fake) Kubernetes API.
func TestRunInit_Success(t *testing.T) {
	home := withTempHome(t)

	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()
	deps, maint, boot := newFakeInitDeps(t, k8sClient)

	opts := validInitOptions()

	if err := runInit(context.Background(), opts, deps); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	if maint.callCount != 1 || len(maint.applied) == 0 {
		t.Fatalf("expected exactly one non-empty insecure apply, got callCount=%d len=%d", maint.callCount, len(maint.applied))
	}

	// Ichor's own install manifest (testIchorManifest) was applied through
	// kubeconfig, not inlined into the node's boot config.
	var ns corev1.Namespace
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: ichorControllerNamespace}, &ns); err != nil {
		t.Fatalf("expected Ichor's own install manifest to have been applied: %v", err)
	}

	if !boot.bootstrapCalled {
		t.Fatal("expected Bootstrap to be called")
	}

	var cluster ichorv1alpha1.TalosCluster
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: testInitClusterName}, &cluster); err != nil {
		t.Fatalf("getting created TalosCluster: %v", err)
	}

	var machine ichorv1alpha1.TalosMachine
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: testInitMachineUUID}, &machine); err != nil {
		t.Fatalf("getting created TalosMachine: %v", err)
	}

	if machine.Spec.Role != roleControlPlane || !machine.Spec.IsApproved() {
		t.Fatalf("machine spec = %+v, want role controlplane and approved", machine.Spec)
	}

	var secret corev1.Secret
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: cluster.Spec.SecretsRef.Name}, &secret); err != nil {
		t.Fatalf("getting created secrets bundle Secret: %v", err)
	}

	if len(secret.Data[secretsbundle.SecretKey]) == 0 {
		t.Fatal("expected the secrets bundle Secret to carry data under the secretsbundle.SecretKey")
	}

	var patches ichorv1alpha1.TalosConfigPatchList
	if err := k8sClient.List(context.Background(), &patches); err != nil {
		t.Fatalf("listing TalosConfigPatches: %v", err)
	}

	if len(patches.Items) == 0 {
		t.Fatal("expected at least one TalosConfigPatch for the cilium CNI's bootstrap manifest")
	}

	// Local files: the secrets bundle and the kubeconfig, both 0600.
	for _, name := range []string{"secrets.yaml", "kubeconfig"} {
		path := home + "/.ichor/clusters/" + testInitClusterName + "/" + name
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}

		if got := fi.Mode().Perm(); got != 0o600 {
			t.Fatalf("%s mode = %o, want 0600", path, got)
		}
	}
}

// TestRunInit_DiskRoles proves the optional disk-role selectors
// (architecture.md's "Nodes": each disk gets a role) land in the same
// TalosConfigPatch as the install disk, as VolumeConfig/RawVolumeConfig/
// UserVolumeConfig documents.
func TestRunInit_DiskRoles(t *testing.T) {
	withTempHome(t)

	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()
	deps, _, _ := newFakeInitDeps(t, k8sClient)

	opts := validInitOptions()
	opts.DiskRoles = diskRoleOptions{
		EphemeralDiskSelector:  testEphemeralDiskSelector,
		StorageDiskSelector:    testStorageDiskSelector,
		UserVolumeDiskSelector: `disk.wwid == "data"`,
	}

	if err := runInit(context.Background(), opts, deps); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	var patches ichorv1alpha1.TalosConfigPatchList
	if err := k8sClient.List(context.Background(), &patches); err != nil {
		t.Fatalf("listing TalosConfigPatches: %v", err)
	}

	var found struct{ ephemeral, storage, userVolume bool }

	for _, p := range patches.Items {
		for _, d := range p.Spec.Documents {
			switch d.Kind {
			case "VolumeConfig":
				found.ephemeral = true
			case "RawVolumeConfig":
				found.storage = true
			case "UserVolumeConfig":
				found.userVolume = true
			}
		}
	}

	if !found.ephemeral || !found.storage || !found.userVolume {
		t.Fatalf("expected all three disk-role documents across the created patches, got %+v", patches.Items)
	}
}

// TestRunInit_ConfirmationDeclined proves --yes's gate: declining aborts
// before the irreversible apply, and nothing in the (fake) cluster API
// is ever created.
func TestRunInit_ConfirmationDeclined(t *testing.T) {
	withTempHome(t)

	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()
	deps, maint, boot := newFakeInitDeps(t, k8sClient)
	deps.AssumeYes = false
	deps.Stdin = strings.NewReader("n\n")

	opts := validInitOptions()

	if err := runInit(context.Background(), opts, deps); err == nil {
		t.Fatal("expected an error, got nil")
	}

	if maint.callCount != 0 {
		t.Fatalf("expected ApplyInsecure to never be called, got callCount=%d", maint.callCount)
	}

	if boot.bootstrapCalled {
		t.Fatal("expected Bootstrap to never be called")
	}

	var clusters ichorv1alpha1.TalosClusterList
	if err := k8sClient.List(context.Background(), &clusters); err != nil {
		t.Fatalf("listing TalosClusters: %v", err)
	}

	if len(clusters.Items) != 0 {
		t.Fatalf("expected no TalosCluster to be created, got %+v", clusters.Items)
	}
}

// TestRunInit_KeepsAnExistingBundle proves init leaves a secrets bundle already stored for the cluster
// untouched and stops before it contacts the node.
func TestRunInit_KeepsAnExistingBundle(t *testing.T) {
	home := withTempHome(t)

	dir := home + "/.ichor/clusters/" + testInitClusterName
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	path := dir + "/secrets.yaml"
	if err := os.WriteFile(path, []byte("earlier bundle"), 0o600); err != nil {
		t.Fatal(err)
	}

	deps, maint, _ := newFakeInitDeps(t, fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build())

	err := runInit(context.Background(), validInitOptions(), deps)
	if err == nil || !strings.Contains(err.Error(), "~/.ichor/clusters/"+testInitClusterName+"/secrets.yaml") {
		t.Fatalf("runInit err = %v, want a refusal naming the existing bundle", err)
	}

	if got, _ := os.ReadFile(path); string(got) != "earlier bundle" {
		t.Fatalf("the existing bundle was replaced: %q", got)
	}

	if maint.callCount != 0 {
		t.Fatalf("expected the node to be left alone, got callCount=%d", maint.callCount)
	}
}

// TestRunInit_BundleFileFollowsTheApply proves a failed run leaves no secrets bundle behind as long as
// the node was never sent the configuration, and keeps it once ApplyInsecure has been called, whatever
// that call returned.
func TestRunInit_BundleFileFollowsTheApply(t *testing.T) {
	for name, tc := range map[string]struct {
		setup    func(*initDeps, *fakeMaintenanceApplier)
		wantFile bool
	}{
		"prompt declined": {
			setup: func(d *initDeps, _ *fakeMaintenanceApplier) {
				d.AssumeYes = false
				d.Stdin = strings.NewReader("n\n")
			},
		},
		"node not reachable": {
			setup: func(d *initDeps, _ *fakeMaintenanceApplier) {
				d.DialMaintenance = func(context.Context, string) (maintenanceApplier, error) {
					return nil, errors.New("connection refused")
				}
			},
		},
		"apply failed": {
			setup:    func(_ *initDeps, m *fakeMaintenanceApplier) { m.applyErr = errors.New("apply failed") },
			wantFile: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			home := withTempHome(t)

			deps, maint, _ := newFakeInitDeps(t, fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build())
			tc.setup(&deps, maint)

			if err := runInit(context.Background(), validInitOptions(), deps); err == nil {
				t.Fatal("expected init to fail")
			}

			_, err := os.Stat(home + "/.ichor/clusters/" + testInitClusterName + "/secrets.yaml")
			if got := err == nil; got != tc.wantFile {
				t.Fatalf("bundle file present = %v (stat err %v), want %v", got, err, tc.wantFile)
			}
		})
	}
}

// TestRunInit_WeakIdentityRejected proves init refuses to bootstrap a
// node whose hardware identity is weak (no SMBIOS UUID), the same rule
// Applies to discovery.
func TestRunInit_WeakIdentityRejected(t *testing.T) {
	withTempHome(t)

	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()
	deps, maint, _ := newFakeInitDeps(t, k8sClient)
	deps.ReadNode = func(context.Context, string, bool) (nodeReading, error) {
		return nodeReading{}, nil
	}

	opts := validInitOptions()

	if err := runInit(context.Background(), opts, deps); err == nil {
		t.Fatal("expected an error, got nil")
	}

	if maint.callCount != 0 {
		t.Fatalf("expected ApplyInsecure to never be called, got callCount=%d", maint.callCount)
	}
}

// TestRunInit_RefusesAnOlderTalos proves init refuses a node whose Talos is older than the release the
// configuration is rendered for, before the secrets bundle is written or anything is sent to the node:
// a node that already has Talos installed is not reinstalled by a new configuration, so the apply would
// fail later on a field the old release does not know.
func TestRunInit_RefusesAnOlderTalos(t *testing.T) {
	home := withTempHome(t)

	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()
	deps, maint, _ := newFakeInitDeps(t, k8sClient)
	deps.ReadNode = func(context.Context, string, bool) (nodeReading, error) {
		return nodeReading{
			Identity:     controller.HardwareIdentity{UUID: testInitMachineUUID, Serial: "serial-1"},
			Links:        []ichorv1alpha1.TalosMachineDiscoveredLink{testInitLink},
			TalosVersion: "v1.13.1",
		}, nil
	}

	err := runInit(context.Background(), validInitOptions(), deps)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}

	for _, want := range []string{"v1.13.1", release.Pinned.Talos, "upgrade"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not contain %q", err, want)
		}
	}

	if maint.callCount != 0 {
		t.Fatalf("expected ApplyInsecure to never be called, got callCount=%d", maint.callCount)
	}

	if _, statErr := os.Stat(home + "/.ichor/clusters/" + testInitClusterName + "/secrets.yaml"); !os.IsNotExist(statErr) {
		t.Fatalf("expected no secrets bundle to be written, stat err = %v", statErr)
	}
}

// TestRunInit_MergeKubeconfig: with --merge-kubeconfig the admin context lands in the first file of
// $KUBECONFIG and the closing lines name it; without it they give the export line and the hint, and no
// file outside ~/.ichor is written.
func TestRunInit_MergeKubeconfig(t *testing.T) {
	for _, merge := range []bool{true, false} {
		withTempHome(t)

		target := t.TempDir() + "/config"
		t.Setenv("KUBECONFIG", target)

		deps, _, boot := newFakeInitDeps(t, fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build())
		boot.kubeconfig = adminKubeconfig(t, testInitClusterName, "https://10.0.0.1:6443")

		opts := validInitOptions()
		opts.MergeKubeconfig = merge

		if err := runInit(context.Background(), opts, deps); err != nil {
			t.Fatalf("merge=%v: runInit: %v", merge, err)
		}

		out := deps.Stderr.(*bytes.Buffer).String()
		_, statErr := os.Stat(target)

		if merge {
			want := "admin@" + testInitClusterName
			if statErr != nil || mustLoad(t, target).CurrentContext != want || !strings.Contains(out, "kubectl and ichor now use context "+want) {
				t.Fatalf("merge: stat %v, output:\n%s", statErr, out)
			}

			if !strings.Contains(out, "--merge-kubeconfig: will add context "+want) {
				t.Fatalf("plan line missing:\n%s", out)
			}
		} else if statErr == nil || !strings.Contains(out, "export KUBECONFIG=~/.ichor/clusters/"+testInitClusterName+"/kubeconfig") ||
			!strings.Contains(out, "--merge-kubeconfig") {
			t.Fatalf("no merge: stat %v, output:\n%s", statErr, out)
		}
	}
}

// The first machine names its installer. An empty image installs only from a boot asset Image
// Factory built, which a node reset from an existing Talos install is not.
func TestRunInit_FirstMachineNamesTheStockInstaller(t *testing.T) {
	withTempHome(t)

	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()
	deps, _, _ := newFakeInitDeps(t, k8sClient)

	opts := validInitOptions()
	opts.Namespace = ichorControllerNamespace

	if err := runInit(context.Background(), opts, deps); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	var machines ichorv1alpha1.TalosMachineList
	if err := k8sClient.List(context.Background(), &machines); err != nil {
		t.Fatalf("listing TalosMachines: %v", err)
	}

	if len(machines.Items) != 1 || machines.Items[0].Spec.Install == nil {
		t.Fatalf("want one TalosMachine with spec.install, got %d machines", len(machines.Items))
	}

	const want = "factory.talos.dev/metal-installer/376567988ad370138ad8b2698212367b8edcb69b5fd68c80be1f2ec7d603b4ba:" + testTalosVersion
	if got := machines.Items[0].Spec.Install.Image; got != want {
		t.Errorf("spec.install.image = %q, want %q", got, want)
	}
}
