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
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/resource/meta"
	"github.com/cosi-project/runtime/pkg/state"
	"github.com/cosi-project/runtime/pkg/state/impl/inmem"
	"github.com/siderolabs/talos/pkg/machinery/resources/k8s"
	"github.com/siderolabs/talos/pkg/machinery/resources/network"
	"github.com/siderolabs/talos/pkg/machinery/resources/runtime"
	"github.com/siderolabs/talos/pkg/machinery/resources/secrets"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/credentials"
)

func TestParseBPFFilter(t *testing.T) {
	t.Parallel()

	// `tcpdump -dd 'tcp'` on Ethernet.
	valid := "{ 0x28, 0, 0, 0x0000000c },\n{ 0x15, 0, 3, 0x00000800 },\n{ 0x30, 0, 0, 0x00000017 },\n" +
		"{ 0x15, 0, 1, 0x00000006 },\n{ 0x6, 0, 0, 0x00040000 },\n{ 0x6, 0, 0, 0x00000000 },\n"

	if got, err := ParseBPFFilter(valid); err != nil || len(got) != 6 {
		t.Fatalf("valid filter: got %d instructions (want 6), err %v", len(got), err)
	}

	if got, err := ParseBPFFilter("  \n"); err != nil || got != nil {
		t.Fatalf("empty filter must mean none, got %v, %v", got, err)
	}

	for name, in := range map[string]string{
		"an expression":         "tcp port 80",
		"a missing field":       "{ 0x28, 0, 0 },",
		"a jump out of range":   "{ 0x15, 9, 0, 0x00000006 },\n{ 0x6, 0, 0, 0x0 },",
		"no return":             "{ 0x28, 0, 0, 0x0000000c },",
		"an unknown opcode":     "{ 0xffff, 0, 0, 0x0 },\n{ 0x6, 0, 0, 0x0 },",
		"a jump above 255":      "{ 0x15, 300, 0, 0x6 },\n{ 0x6, 0, 0, 0x0 },",
		"shell in a field":      "{ 0x28, 0, 0, $(id) },",
		"too many instructions": strings.Repeat("{ 0x28, 0, 0, 0x0000000c },\n", maxBPFInstructions+1),
	} {
		if _, err := ParseBPFFilter(in); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestRedactKernelLog(t *testing.T) {
	t.Parallel()

	in := "[0.0] Linux version 6.1\n[0.0] Command line: talos.platform=metal talos.config=https://x?token=" + diagSecret + " quiet\n[1.0] ok"
	out := string(redactKernelLog([]byte(in)))

	if strings.Contains(out, diagSecret) {
		t.Fatalf("a credential survived: %s", out)
	}

	if !strings.Contains(out, "talos.platform=metal") || !strings.Contains(out, "[1.0] ok") {
		t.Fatalf("redaction removed too much: %s", out)
	}
}

func newResourceState(t *testing.T, sensitive bool) state.State {
	t.Helper()

	st := state.WrapCore(inmem.NewState())
	ctx := context.Background()

	put := func(def meta.ResourceDefinitionSpec) {
		d, err := meta.NewResourceDefinition(def)
		if err != nil {
			t.Fatal(err)
		}

		if err := st.Create(ctx, d); err != nil {
			t.Fatal(err)
		}
	}

	hn := network.HostnameStatusExtension{}.ResourceDefinition()
	if sensitive {
		hn.Sensitivity = meta.Sensitive
	}

	put(hn)
	put(runtime.KernelCmdlineExtension{}.ResourceDefinition())
	put(secrets.APIExtension{}.ResourceDefinition())

	// Talos fills Manifests with a bootstrap token Secret and does not mark the type sensitive.
	mf := k8s.ManifestExtension{}.ResourceDefinition()
	if sensitive {
		mf.Sensitivity = meta.Sensitive
	}

	put(mf)

	mustCreate := func(r resource.Resource) {
		if err := st.Create(ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	h := network.NewHostnameStatus(network.NamespaceName, "hostname")
	h.TypedSpec().Hostname = "node-1"
	mustCreate(h)

	c := runtime.NewKernelCmdline()
	c.TypedSpec().Cmdline = "talos.config=https://x?token=" + diagSecret
	mustCreate(c)

	mustCreate(secrets.NewAPI())

	m := k8s.NewManifest(k8s.ControlPlaneNamespaceName, "00-test")
	m.TypedSpec().Items = []k8s.SingleManifest{{Object: map[string]any{
		"kind":       "Secret",
		"metadata":   map[string]any{"name": "bootstrap-token-abc"},
		"stringData": map[string]any{"token-secret": diagSecret},
	}}}
	mustCreate(m)

	return st
}

func TestCollectResourcesLeavesSensitiveOnesOut(t *testing.T) {
	t.Parallel()

	var problems []string

	files, err := collectResources(context.Background(), newResourceState(t, false), &problems)
	if err != nil {
		t.Fatal(err)
	}

	got := files["resources/network/HostnameStatuses.net.talos.dev.yaml"]
	if !strings.Contains(string(got), "node-1") {
		t.Fatalf("a plain resource must be written, got files %v", slices.Collect(maps.Keys(files)))
	}

	for name, data := range files {
		if strings.Contains(string(data), diagSecret) || strings.Contains(name, "cmdline") || strings.Contains(name, "secrets") {
			t.Errorf("%s must not be in the bundle", name)
		}
	}

	files, err = collectResources(context.Background(), newResourceState(t, true), &problems)
	if err != nil {
		t.Fatal(err)
	}

	if len(files) != 0 {
		t.Fatalf("a type Talos marks sensitive must be skipped, got %v", slices.Collect(maps.Keys(files)))
	}
}

func TestTalosRolePerScope(t *testing.T) {
	t.Parallel()

	// The node's RBAC table (Talos v1.14): reader for the bundle's calls, etcd:backup for EtcdSnapshot,
	// operator for PacketCapture. The controller picks the role from the purpose, which is the scope's name.
	want := map[DownloadScope]string{ScopeSupportBundle: "os:reader", ScopeEtcdSnapshot: "os:etcd:backup", ScopePacketCapture: "os:operator"}
	for scope, role := range want {
		roles, err := credentials.TalosRoles(ichorv1alpha1.TalosCredentialPurpose(scope), "")
		if err != nil {
			t.Fatalf("%s: %v", scope, err)
		}

		if got := roles.Strings(); len(got) != 1 || got[0] != role {
			t.Errorf("%s: role %v, want %s", scope, got, role)
		}
	}
}

// machineryResourceTypes reads the resource definitions of the Talos machinery
// module at the go.mod version from source: the COSI registry is only filled on a
// node, so the module's ResourceDefinitionSpec literals are the offline list.
func machineryResourceTypes(t *testing.T) (nonSensitive []string) {
	t.Helper()

	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/siderolabs/talos/pkg/machinery").Output()
	if err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(strings.TrimSpace(string(out)), "resources")
	fset := token.NewFileSet()

	err = filepath.WalkDir(root, func(dir string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}

		names, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			return err
		}

		var files []*ast.File

		for _, name := range names {
			if strings.HasSuffix(name, "_test.go") {
				continue
			}

			f, err := parser.ParseFile(fset, name, nil, 0)
			if err != nil {
				return err
			}

			files = append(files, f)
		}

		{
			consts := map[string]string{}

			for _, f := range files {
				ast.Inspect(f, func(n ast.Node) bool {
					if vs, ok := n.(*ast.ValueSpec); ok && len(vs.Values) == len(vs.Names) {
						for i, name := range vs.Names {
							if call, ok := vs.Values[i].(*ast.CallExpr); ok && len(call.Args) == 1 {
								if lit, ok := call.Args[0].(*ast.BasicLit); ok {
									consts[name.Name], _ = strconv.Unquote(lit.Value)
								}
							}
						}
					}

					return true
				})
			}

			for _, f := range files {
				ast.Inspect(f, func(n ast.Node) bool {
					cl, ok := n.(*ast.CompositeLit)
					if !ok {
						return true
					}

					if sel, ok := cl.Type.(*ast.SelectorExpr); !ok || sel.Sel.Name != "ResourceDefinitionSpec" {
						return true
					}

					typ, sensitive := "", false

					for _, e := range cl.Elts {
						kv := e.(*ast.KeyValueExpr)

						switch kv.Key.(*ast.Ident).Name {
						case "Type":
							if id, ok := kv.Value.(*ast.Ident); ok {
								typ = consts[id.Name]
							}
						case "Sensitivity":
							sel, ok := kv.Value.(*ast.SelectorExpr)
							sensitive = ok && sel.Sel.Name == "Sensitive"
						}
					}

					if typ == "" {
						t.Errorf("%s: a resource definition whose Type is not a local constant; teach the test to read it", fset.Position(cl.Pos()))
					} else if !sensitive {
						nonSensitive = append(nonSensitive, typ)
					}

					return true
				})
			}
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	return nonSensitive
}

func TestReviewedBundleTypesCoverTalos(t *testing.T) {
	t.Parallel()

	got := machineryResourceTypes(t)
	if len(got) < 100 {
		t.Fatalf("found only %d resource types, the source reader is broken", len(got))
	}

	for _, typ := range got {
		if _, ok := reviewedBundleTypes[typ]; !ok {
			t.Errorf("new Talos resource type %s: review it for secrets and add it to reviewedBundleTypes", typ)
		}
	}

	if !excludedTypes[k8s.ManifestType] {
		t.Errorf("%s must be excluded", k8s.ManifestType)
	}
}
