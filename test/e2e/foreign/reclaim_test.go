//go:build e2e
// +build e2e

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

package foreign

// Group C (reclaim). These cases attach to two clusters that already exist:
//
//   - the foreign cluster (`go run ./test/harness/foreign up --prefix ichor-e2e-fj --workers 1 --data-disk`),
//     named by ICHOR_E2E_F_PREFIX;
//   - an Ichor-made cluster (`ichor init` run in the pod ichor-e2e-r-tools, see test/harness/ui-demo/README.md)
//     whose kubeconfig reaches the workstation through `foreign relay` at bin/foreign/ichor-e2e-fj-r/kubeconfig.local.
//
// Run one case at a time, for example:
//
//	go test -tags=e2e ./test/e2e/foreign/ -run TestC1C5C7 -v -timeout 40m

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/maeshinshin/ichor/test/internal/foreign"
	"github.com/maeshinshin/ichor/test/internal/kube"
	"github.com/maeshinshin/ichor/test/internal/talosctl"
	"github.com/maeshinshin/ichor/test/utils"
)

const (
	ichorTools = "ichor-e2e-r-tools"
	ichorName  = "ichor-e2e-r"
	podKC      = "/tmp/home/.ichor/clusters/ichor-e2e-r/kubeconfig"
	uiNodePort = "30080"
	uiSA       = "system:serviceaccount:ichor-system:ichor-ichor-ui"
)

type rig struct {
	f        *foreign.Cluster
	ichorKC  string // workstation kubeconfig of the Ichor-made cluster (through the relay)
	ichorCP  string // pod IP of its control plane
	ichorTC  []byte
	foreignW string
}

func newRig(t *testing.T, opts foreign.Options) *rig {
	t.Helper()
	if os.Getenv(kube.ContextEnv) == "" {
		t.Skip(kube.ContextEnv + " must be set")
	}
	opts.Prefix = env(prefixE, "ichor-e2e-fj")
	if opts.ControlPlanes == 0 {
		opts.ControlPlanes = 1
	}
	f, err := foreign.Attach(opts)
	if err != nil {
		t.Fatal(err)
	}
	root, err := foreign.Root()
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{f: f, ichorKC: filepath.Join(root, "bin/foreign/ichor-e2e-fj-r/kubeconfig.local")}
	out, err := utils.Run(kube.Command("-n", ns, "get", "vmi", ichorName+"-cp", "-o", "jsonpath={.status.interfaces[0].ipAddress}"))
	if err != nil {
		t.Fatal(err)
	}
	r.ichorCP = strings.TrimSpace(out)
	if len(f.Workers) > 0 {
		r.foreignW = f.Workers[0]
	}
	tcFile := filepath.Join(root, "bin/foreign/ichor-e2e-fj-r/tc/talosconfig")
	if r.ichorTC, err = os.ReadFile(tcFile); err != nil {
		t.Fatalf("the Ichor cluster's talosconfig (generate it from its secrets bundle): %v", err)
	}
	r.stage(t)
	return r
}

// stage copies the two talosconfigs and the helper binaries into the Ichor tools pod.
func (r *rig) stage(t *testing.T) {
	t.Helper()
	root, _ := foreign.Root()
	for _, cp := range [][2]string{
		{r.f.Talosconfig, "/tmp/foreign-talosconfig"},
		{filepath.Join(root, "bin/foreign/ichor-e2e-fj-r/tc/talosconfig"), "/tmp/ichor-talosconfig"},
		{filepath.Join(root, "bin/reclaimui"), "/tmp/reclaimui"},
	} {
		if _, err := utils.Run(kube.Command("-n", ns, "cp", cp[0], ichorTools+":"+cp[1])); err != nil {
			t.Fatalf("staging %s: %v", cp[1], err)
		}
	}
}

// pod runs a command in the Ichor tools pod and logs its output (no secret is ever in it).
func (r *rig) pod(t *testing.T, args ...string) (string, error) {
	t.Helper()
	a := append([]string{"--context", os.Getenv(kube.ContextEnv), "-n", ns, "exec", ichorTools, "--"}, args...)
	out, err := exec.Command("kubectl", a...).CombinedOutput()
	t.Logf("$ %s\n%s", strings.Join(args, " "), out)
	return string(out), err
}

// machine runs `ichor <verb> node <args>` (reclaim, approve) in the Ichor tools pod.
func (r *rig) machine(t *testing.T, verb string, args ...string) (string, error) {
	t.Helper()
	return r.pod(t, append([]string{"env", "HOME=/tmp/home", "/tmp/ichor", verb, "node"}, args...)...)
}

func (r *rig) reclaim(t *testing.T, addr string, extra ...string) (string, error) {
	t.Helper()
	return r.machine(t, append([]string{"reclaim", addr, "--talosconfig", "/tmp/foreign-talosconfig", "--kubeconfig", podKC}, extra...)...)
}

func (r *rig) ui(t *testing.T, op string, extra ...string) (string, error) {
	t.Helper()
	return r.pod(t, append([]string{"/tmp/reclaimui", op, "-url", "https://" + r.ichorCP + ":" + uiNodePort}, extra...)...)
}

func (r *rig) ik(args ...string) (string, error) {
	return utils.Run(kube.InnerCommand(r.ichorKC, args...))
}

func (r *rig) ikJSON(t *testing.T, args ...string) string {
	t.Helper()
	out, err := r.ik(args...)
	if err != nil {
		t.Logf("ik %v: %v", args, err)
		return ""
	}
	return strings.TrimSpace(out)
}

func ftc(t *testing.T, f *foreign.Cluster) []byte {
	t.Helper()
	b, err := os.ReadFile(f.Talosconfig)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// firstUUID returns spec.uuid of the first object of a systeminformation listing ("" when there is none).
func firstUUID(raw string) string {
	objs, _ := talosctl.DecodeAll(raw)
	if len(objs) == 0 {
		return ""
	}
	u, _ := talosctl.SpecField(objs[0], "uuid").(string)
	return u
}

// uuidOf reads a configured node's UUID through its authenticated API.
func uuidOf(t *testing.T, ip string, tc []byte) string {
	t.Helper()
	out, err := talosctl.GetAuthenticated(ns, ip, "systeminformation", tc)
	if err != nil {
		t.Fatalf("systeminformation %s: %v", ip, err)
	}
	u := firstUUID(out)
	if u == "" {
		t.Fatalf("no uuid in systeminformation of %s", ip)
	}
	return u
}

// maintenanceUUID returns the UUID an unconfigured node reports on its insecure API ("" when it is not in maintenance).
func maintenanceUUID(ip string) string {
	out, err := talosctl.Get(ns, ip, "machinestatus")
	if err != nil {
		return ""
	}
	objs, err := talosctl.DecodeAll(out)
	if err != nil || len(objs) != 1 || talosctl.SpecField(objs[0], "stage") != "maintenance" {
		return ""
	}
	out, err = talosctl.Get(ns, ip, "systeminformation")
	if err != nil {
		return ""
	}
	return firstUUID(out)
}

func waitMaintenance(t *testing.T, ip string, d time.Duration) string {
	t.Helper()
	u, err := kube.Poll("maintenance mode of "+ip, d, func() (string, bool) {
		u := maintenanceUUID(ip)
		return u, u != ""
	})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// refoereignize puts a worker that left the foreign cluster back into it: reset by whoever owns it now (the
// credential is passed), wait for maintenance, apply the foreign worker config, wait for the foreign Node.
func (r *rig) refoereignize(t *testing.T, ownerTC []byte, nodeName string) {
	t.Helper()
	start := time.Now()
	if u := maintenanceUUID(r.foreignW); u == "" {
		if out, err := talosctl.Reset(ns, r.foreignW, ownerTC); err != nil {
			t.Fatalf("reset %s: %v: %s", r.foreignW, err, out)
		}
	}
	waitMaintenance(t, r.foreignW, 8*time.Minute)
	cfg, err := os.ReadFile(filepath.Join(r.f.Dir, "worker.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if out, err := talosctl.Apply(ns, r.foreignW, cfg); err != nil {
		t.Fatalf("apply-config %s: %v: %s", r.foreignW, err, out)
	}
	if _, err := kube.Poll("foreign API of "+r.foreignW, 10*time.Minute, func() (string, bool) {
		_, err := talosctl.GetAuthenticated(ns, r.foreignW, "machinestatus", ftc(t, r.f))
		return "", err == nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := kube.Poll("foreign node Ready", 10*time.Minute, func() (string, bool) {
		out, err := inner(r.f, "get", "node", nodeName, "-o", "jsonpath={.status.conditions[?(@.type==\"Ready\")].status}")
		return out, err == nil && strings.TrimSpace(out) == "True"
	}); err != nil {
		t.Fatal(err)
	}
	t.Logf("worker back in the foreign cluster after %s", time.Since(start).Round(time.Second))
}

func (r *rig) waitPhase(t *testing.T, name string, d time.Duration, phases ...string) string {
	t.Helper()
	p, err := kube.Poll("MachineReclaim "+name+" in "+strings.Join(phases, "|"), d, func() (string, bool) {
		p := r.ikJSON(t, "-n", crNS, "get", "machinereclaims.ichor.maesh.dev", name, "-o", "jsonpath={.status.phase}")
		for _, w := range phases {
			if p == w {
				return p, true
			}
		}
		return p, false
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

var reclaimName = regexp.MustCompile(`"name":\s*"(reclaim-[a-z0-9]+)"`)

// newReclaim creates a request through ichor-ui and returns its name.
func (r *rig) newReclaim(t *testing.T, tcPath, addr string, extra ...string) (string, string, error) {
	t.Helper()
	out, err := r.ui(t, "create", append([]string{"-address", addr, "-talosconfig", tcPath, "-cluster", ichorName}, extra...)...)
	if err != nil {
		return "", out, err
	}
	m := reclaimName.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no reclaim name in %s", out)
	}
	return m[1], out, nil
}

// TestC1C5C7: a worker of another cluster is reclaimed by the CLI into the Ichor cluster (C1), its user and
// encrypted volumes are listed and survive (C5), a wrong typed UUID sends nothing (C4), and the address after the
// reboot is recorded (C7).
func TestC1C5C7(t *testing.T) {
	r := newRig(t, foreign.Options{Workers: 1, DataDisk: true})
	r.ensureForeign(t)
	tc := ftc(t, r.f)
	uuid := uuidOf(t, r.foreignW, tc)
	t.Logf("worker %s UUID %s", r.foreignW, uuid)

	// C4: a wrong typed UUID sends nothing.
	out, err := r.reclaim(t, r.foreignW, "--cluster", ichorName, "--confirm", "00000000-0000-0000-0000-000000000000")
	if err == nil || !strings.Contains(out, "nothing was sent") {
		t.Errorf("wrong UUID was not refused with 'nothing was sent': %v", err)
	}
	if got := uuidOf(t, r.foreignW, tc); got != uuid {
		t.Fatalf("node answered with a different UUID %q after a refused reclaim", got)
	}
	// C5: both volumes are listed, the encrypted one called out.
	if !strings.Contains(out, "plain on /dev/vdb") || !strings.Contains(out, "secret on /dev/vdb") || !strings.Contains(out, "ENCRYPTED") {
		t.Errorf("the inspection does not list the user volume and the encrypted volume")
	}

	start := time.Now()
	out, err = r.reclaim(t, r.foreignW, "--cluster", ichorName, "--confirm", uuid)
	if err != nil {
		t.Fatalf("reclaim failed: %v", err)
	}
	t.Logf("reclaim returned after %s", time.Since(start).Round(time.Second))
	if !strings.Contains(out, "is in maintenance mode with UUID "+uuid) {
		t.Errorf("no maintenance confirmation with the same UUID")
	}
	// C7: the pod network keeps the address across the reboot, so there is no hint to see here.
	if strings.Contains(out, "changed") {
		t.Logf("address changed after the reset: %s", out)
	} else {
		t.Logf("C7: the address %s did not change across the reset (KubeVirt pod network); the hint path is not reachable in this harness", r.foreignW)
	}
	if got := maintenanceUUID(r.foreignW); got != uuid {
		t.Errorf("maintenance UUID = %q, want %q", got, uuid)
	}

	// Registered as pending, then approved and joined.
	// Discovery probes on its own interval (about 5 minutes); a newly added address is probed at once, a known one waits.
	if _, err := kube.Poll("pending TalosMachine", 10*time.Minute, func() (string, bool) {
		return "", r.ikJSON(t, "-n", crNS, "get", "talosmachines.ichor.maesh.dev", uuid, "-o", "name") != ""
	}); err != nil {
		t.Fatal(err)
	}
	if out, err := r.machine(t, "approve", uuid, "--role", "worker", "--install-disk", "/dev/vda",
		"--hostname", "ichor-e2e-r-w", "--yes", "--local", "-n", crNS, "--kubeconfig", podKC); err != nil {
		t.Fatalf("approve: %v\n%s", err, out)
	}
	if _, err := kube.Poll("joined Ichor cluster", 12*time.Minute, func() (string, bool) {
		ready := r.ikJSON(t, "get", "node", "ichor-e2e-r-w", "-o", "jsonpath={.status.conditions[?(@.type==\"Ready\")].status}")
		return ready, ready == "True"
	}); err != nil {
		t.Fatal(err)
	}
	t.Logf("C1: reclaimed, approved and joined after %s", time.Since(start).Round(time.Second))

	// C5: the user disk survived (partitions still there); the encrypted one is still LUKS.
	vol, err := talosctl.GetAuthenticated(ns, r.foreignW, "discoveredvolumes", r.ichorTC)
	if err != nil {
		t.Fatalf("discoveredvolumes: %v", err)
	}
	for _, want := range []string{"vdb1", "vdb2", "luks"} {
		if !strings.Contains(vol, want) {
			t.Errorf("discoveredvolumes after the join does not mention %q", want)
		}
	}
	// And the foreign cluster no longer has the node answering as one of its own.
	if _, err := talosctl.GetAuthenticated(ns, r.foreignW, "machinestatus", tc); err == nil {
		t.Errorf("the foreign talosconfig still works on the reclaimed node")
	}
}

// TestC3C4: the only control plane of a foreign cluster is refused (C3), a node already in the Ichor cluster is
// refused even with every override (C4). Nothing is sent in either case.
func TestC3C4Refusals(t *testing.T) {
	r := newRig(t, foreign.Options{Workers: 1, DataDisk: true})
	cp := r.f.CPs[0]
	uuid := uuidOf(t, cp, ftc(t, r.f))
	out, err := r.reclaim(t, cp, "--cluster", ichorName, "--confirm", uuid)
	if err == nil || !strings.Contains(out, "last control plane of cluster") || !strings.Contains(out, "pass --force") {
		t.Errorf("C3: the only control plane was not refused as the last one: %v", err)
	}
	if got := uuidOf(t, cp, ftc(t, r.f)); got != uuid {
		t.Errorf("control plane changed after a refusal")
	}

	// C4: the Ichor control plane, with --force, --confirm-cluster and --force-ungraceful all given.
	iuuid := r.ikJSON(t, "-n", crNS, "get", "talosmachines.ichor.maesh.dev", "-o", "jsonpath={.items[0].metadata.name}")
	out, err = r.machine(t, "reclaim", r.ichorCP, "--talosconfig", "/tmp/ichor-talosconfig",
		"--kubeconfig", podKC, "--confirm", iuuid, "--force", "--confirm-cluster", ichorName, "--force-ungraceful")
	if err == nil || !strings.Contains(out, "is already an Ichor node") {
		t.Errorf("C4: an Ichor machine was not refused: %v", err)
	}
	if got := r.ikJSON(t, "get", "node", ichorName+"-cp", "-o", "jsonpath={.status.conditions[?(@.type==\"Ready\")].status}"); got != "True" {
		t.Errorf("Ichor control plane not Ready after the refusal: %q", got)
	}
}

// noLeaks fails for every text that contains credential material.
func noLeaks(t *testing.T, secrets []string, texts map[string]string) {
	t.Helper()
	for what, text := range texts {
		if l := leaks(text, secrets); len(l) > 0 {
			t.Errorf("C9: %s contains credential material %v", what, l)
		}
	}
}

// jsonField reads a dotted path out of kubectl -o json output.
func jsonField(t *testing.T, raw, path string) string {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	for _, k := range strings.Split(path, ".") {
		m, ok := v.(map[string]any)
		if !ok {
			return ""
		}
		v = m[k]
	}
	return fmt.Sprint(v)
}
