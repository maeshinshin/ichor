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
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// Golden tests of the human output of `ichor get patches` and `ichor apply`, on a pipe: no colour codes.

const (
	hpSysctlName  = "sysctl-tuning"
	hpModulesName = "cp-modules"
	hpSysctlKind  = "SysctlConfig"
	hpModulesKind = "KernelModuleConfig"
	hpSysctlDoc   = `{"params":{"net.core.somaxconn":"1024"}}`
	hpRole        = "control-plane"
)

func patchRef(name string) *pb.ObjectRef {
	return &pb.ObjectRef{Namespace: ichorControllerNamespace, Name: name}
}

func patchesFixture() []*pb.Patch {
	cluster := patchRef(humanCluster)

	return []*pb.Patch{
		{
			Ref: patchRef(hpSysctlName), Cluster: cluster, Layer: pb.ConfigLayer_CONFIG_LAYER_CLUSTER, CreatedAt: ago(2 * 24 * time.Hour),
			Documents: []*pb.PatchDocument{{Kind: hpSysctlKind, Document: hpSysctlDoc}},
		},
		{
			Ref: patchRef(hpModulesName), Cluster: cluster, Layer: pb.ConfigLayer_CONFIG_LAYER_ROLE, Priority: 10, CreatedAt: ago(3 * time.Hour),
			Target: &pb.PatchTarget{Role: hpRole},
			Documents: []*pb.PatchDocument{
				{Kind: hpModulesKind, Document: `{"modules":[{"name":"br_netfilter"}]}`},
				{Kind: hpSysctlKind, Document: hpSysctlDoc},
			},
		},
		{
			Ref: patchRef("one-node"), Cluster: cluster, Layer: pb.ConfigLayer_CONFIG_LAYER_MACHINE, CreatedAt: ago(5 * time.Minute),
			Target:    &pb.PatchTarget{MachineRef: cpUUID},
			Documents: []*pb.PatchDocument{{Kind: hpSysctlKind, Document: hpSysctlDoc}},
		},
		{
			Ref: patchRef("db-tier"), Cluster: cluster, Layer: pb.ConfigLayer_CONFIG_LAYER_SELECTOR, CreatedAt: ago(40 * time.Second),
			Target: &pb.PatchTarget{LabelSelector: &pb.LabelSelector{
				MatchLabels:      map[string]string{"tier": "db"},
				MatchExpressions: []*pb.LabelSelectorRequirement{{Key: "zone", Operator: "In", Values: []string{"a", "b"}}},
			}},
			Documents: []*pb.PatchDocument{{Kind: hpSysctlKind, Document: hpSysctlDoc}},
		},
	}
}

func TestPatchListHumanOutput(t *testing.T) {
	setHumanTestFlags(t, outputHuman)

	var out bytes.Buffer
	if err := renderPatchList(newHuman(&out), &pb.ListPatchesResponse{Patches: patchesFixture()}); err != nil {
		t.Fatal(err)
	}

	want := "NAME           TARGET                                        PRIORITY  DOCUMENTS                         AGE\n" +
		"sysctl-tuning  cluster                                       0         SysctlConfig                      2d\n" +
		"cp-modules     role control-plane                            10        KernelModuleConfig, SysctlConfig  3h\n" +
		"one-node       machine " + cpUUID + "  0         SysctlConfig                      5m\n" +
		"db-tier        selector tier=db, zone in (a,b)               0         SysctlConfig                      40s\n"
	wantText(t, out.String(), want)

	out.Reset()

	if err := renderPatchList(newHuman(&out), &pb.ListPatchesResponse{}); err != nil || out.String() != "no patches\n" {
		t.Errorf("empty list: %q, %v", out.String(), err)
	}
}

func TestPatchListNamesTheClusterWhenThereAreSeveral(t *testing.T) {
	setHumanTestFlags(t, outputHuman)

	ps := patchesFixture()[:2]
	ps[1].Cluster = patchRef("other")

	var out bytes.Buffer
	if err := renderPatchList(newHuman(&out), &pb.ListPatchesResponse{Patches: ps}); err != nil {
		t.Fatal(err)
	}

	if first, _, _ := strings.Cut(out.String(), "\n"); !strings.HasPrefix(first, "NAME") || !strings.Contains(first, "CLUSTER") {
		t.Errorf("no CLUSTER column:\n%s", out.String())
	}
}

func TestPatchGetHumanOutput(t *testing.T) {
	setHumanTestFlags(t, outputHuman)

	var out bytes.Buffer
	if err := renderPatchGet(newHuman(&out), &pb.GetPatchResponse{Patch: patchesFixture()[1]}); err != nil {
		t.Fatal(err)
	}

	want := "cp-modules · ichor-system\n" +
		"  Cluster      initshow\n" +
		"  Target       role control-plane\n" +
		"  Priority     10\n" +
		"  Created      3h ago\n" +
		"\n" +
		"Documents\n" +
		"  KernelModuleConfig\n" +
		"    modules:\n" +
		"    - name: br_netfilter\n" +
		"  SysctlConfig\n" +
		"    params:\n" +
		"      net.core.somaxconn: \"1024\"\n"
	wantText(t, out.String(), want)
}

func TestPatchImpactHumanOutput(t *testing.T) {
	setHumanTestFlags(t, outputHuman)

	for _, tc := range []struct {
		name string
		resp *pb.PreviewImpactResponse
		want string
	}{
		{"clean", &pb.PreviewImpactResponse{}, "Impact\n  ✓ no reboot needed; nothing install-time-only or too new for the cluster's Talos\n"},
		{
			"everything",
			&pb.PreviewImpactResponse{RebootRequired: true, InstallTimeOnly: []string{"machine.install.disk"}, Unsupported: []string{"machine.new"}},
			"Impact\n  ! applying it needs a reboot\n" +
				"  ! only takes effect at install time (a reset and reinstall): machine.install.disk\n" +
				"  ! needs a newer Talos than the cluster is pinned to: machine.new\n",
		},
	} {
		var out bytes.Buffer
		if err := renderPatchImpact(newHuman(&out), tc.resp); err != nil {
			t.Fatal(err)
		}

		if out.String() != tc.want {
			t.Errorf("%s: got:\n%s\nwant:\n%s", tc.name, out.String(), tc.want)
		}
	}
}

type patchEnv struct {
	c       *apiClients
	cluster string
	patch   ichorv1alpha1.TalosConfigPatch
}

func newPatchEnv(t *testing.T) *patchEnv {
	t.Helper()

	cluster := newTestCluster()
	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cluster).Build()

	p := sysctlPatch(cluster.Name, map[string]string{testSysctlKey: "1"})
	p.Name, p.Spec.Layer = testPatchName, layerAll

	return &patchEnv{c: localClients(apiOpts, apiDeps{K8s: k8s}), cluster: cluster.Name, patch: p}
}

func (e *patchEnv) write(t *testing.T, update bool) string {
	t.Helper()

	var out bytes.Buffer
	if err := runPatchWrite(context.Background(), e.c, strings.NewReader(""), &out, &bytes.Buffer{}, &e.patch, update); err != nil {
		t.Fatal(err)
	}

	return out.String()
}

func (e *patchEnv) delete(t *testing.T) string {
	t.Helper()

	var out bytes.Buffer
	if err := runPatchDelete(context.Background(), e.c, strings.NewReader(""), &out, &bytes.Buffer{}, testPatchName); err != nil {
		t.Fatal(err)
	}

	return out.String()
}

func (e *patchEnv) list(t *testing.T) string {
	t.Helper()

	var out bytes.Buffer
	if err := runPatchList(context.Background(), e.c, &out, e.cluster); err != nil {
		t.Fatal(err)
	}

	return out.String()
}

// TestPatchCommandsPrintOneResultLine runs the commands against the local fallback: a person gets one
// ✓ line for a write, a table for the list and the impact for a preview.
func TestPatchCommandsPrintOneResultLine(t *testing.T) {
	setHumanTestFlags(t, outputHuman)

	e := newPatchEnv(t)
	ref := testPatchName + " · " + testNamespace

	if got, want := e.write(t, false), "✓ created patch "+ref+"\n"; got != want {
		t.Errorf("create: %q, want %q", got, want)
	}

	e.patch.Spec.Priority = 5

	if got, want := e.write(t, true), "✓ updated patch "+ref+"\n"; got != want {
		t.Errorf("update: %q, want %q", got, want)
	}

	var out bytes.Buffer
	if err := runPatchPreview(context.Background(), e.c, &out, &e.patch); err != nil || !strings.HasPrefix(out.String(), "Impact\n  ") {
		t.Errorf("preview: %q, %v", out.String(), err)
	}

	if got := e.list(t); !strings.HasPrefix(got, "NAME") || !strings.Contains(strings.Join(strings.Fields(got), " "), testPatchName+" cluster 5 SysctlConfig") {
		t.Errorf("list: %q", got)
	}

	if got, want := e.delete(t), "✓ deleted patch "+ref+"\n"; got != want {
		t.Errorf("delete: %q, want %q", got, want)
	}
}

// TestPatchMachineOutputIsUnchanged: with -o yaml or -o json the commands print the wire message
// byte for byte, as they did before human output existed.
func TestPatchMachineOutputIsUnchanged(t *testing.T) {
	for _, format := range []string{outputYAML, outputJSON} {
		setHumanTestFlags(t, format)

		e := newPatchEnv(t)

		created := e.write(t, false)
		if !strings.Contains(created, "audit") || strings.Contains(created, "✓") {
			t.Errorf("-o %s create: %q", format, created)
		}

		var want bytes.Buffer

		resp, err := e.c.Patch.ListPatches(context.Background(), connect.NewRequest(&pb.ListPatchesRequest{Cluster: objectRef(testNamespace, e.cluster)}))
		if err != nil {
			t.Fatal(err)
		}

		if err := writeMessage(&want, format, resp.Msg); err != nil {
			t.Fatal(err)
		}

		if got := e.list(t); got != want.String() || !strings.Contains(got, testPatchName) {
			t.Errorf("-o %s list:\n%s\nwant:\n%s", format, got, want.String())
		}

		var preview bytes.Buffer
		if err := runPatchPreview(context.Background(), e.c, &preview, &e.patch); err != nil || strings.Contains(preview.String(), "Impact") {
			t.Errorf("-o %s preview: %q, %v", format, preview.String(), err)
		}

		if deleted := e.delete(t); strings.Contains(deleted, "✓") {
			t.Errorf("-o %s delete: %q", format, deleted)
		}
	}
}
