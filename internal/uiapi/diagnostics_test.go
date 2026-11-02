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
	"strings"
	"testing"

	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/resource/meta"
	"github.com/cosi-project/runtime/pkg/state"
	"github.com/cosi-project/runtime/pkg/state/impl/inmem"
	"github.com/cosi-project/runtime/pkg/state/impl/namespaced"
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

	st := state.WrapCore(namespaced.NewState(inmem.Build))
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
		t.Fatalf("a plain resource must be written, got files %v", keys(files))
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
		t.Fatalf("a type Talos marks sensitive must be skipped, got %v", keys(files))
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	return out
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
