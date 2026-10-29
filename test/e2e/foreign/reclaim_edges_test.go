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

// A controller restart mid-request, Failed, Expired and a request deleted early.

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/maeshinshin/ichor/test/internal/foreign"
	"github.com/maeshinshin/ichor/test/internal/kube"
	"github.com/maeshinshin/ichor/test/internal/talosctl"
)

func (r *rig) ikApply(t *testing.T, manifest string, verb ...string) string {
	t.Helper()
	cmd := exec.Command("kubectl", append([]string{"--kubeconfig", r.ichorKC}, append(verb, "-f", "-")...)...)
	cmd.Stdin = strings.NewReader(manifest)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("kubectl %v: %v\n%s", verb, err, out)
	}
	return strings.TrimSpace(string(out))
}

// adminReclaim writes a MachineReclaim and its credential Secret as the cluster admin (the UI API does not expose
// the TTL) and returns the request name and the Secret name.
func (r *rig) adminReclaim(t *testing.T, addr, ttl, talosconfig string) (string, string) {
	t.Helper()
	who := r.ikJSON(t, "auth", "whoami", "-o", "jsonpath={.status.userInfo.username}")
	secret := fmt.Sprintf("reclaim-lane2-%d", time.Now().UnixNano()%1e8)
	crOut := r.ikApply(t, fmt.Sprintf(`apiVersion: ichor.maesh.dev/v1alpha1
kind: MachineReclaim
metadata:
  generateName: reclaim-lane2-
  namespace: ichor-system
  annotations:
    ichor.maesh.dev/reclaim-requested-by: %q
spec:
  address: %s
  credentialSecretRef: %s
  ttl: %s
`, who, addr, secret, ttl), "create", "-o", "name")
	name := strings.TrimPrefix(crOut, "machinereclaim.ichor.maesh.dev/")
	uid := r.ikJSON(t, "-n", crNS, "get", "machinereclaims.ichor.maesh.dev", name, "-o", "jsonpath={.metadata.uid}")
	r.ikApply(t, fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: ichor-system
  labels:
    ichor.maesh.dev/reclaim: "true"
  ownerReferences:
    - apiVersion: ichor.maesh.dev/v1alpha1
      kind: MachineReclaim
      name: %s
      uid: %s
type: ichor.maesh.dev/foreign-talosconfig
stringData:
  talosconfig: |
%s
`, secret, name, uid, indent(talosconfig, "    ")), "create")
	return name, secret
}

func indent(s, p string) string {
	return p + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n"+p)
}

func (r *rig) gone(t *testing.T, kind, name string, d time.Duration) bool {
	t.Helper()
	_, err := kube.Poll(kind+" "+name+" gone", d, func() (string, bool) {
		return "", r.ikJSON(t, "-n", crNS, "get", kind, name, "--ignore-not-found", "-o", "name") == ""
	})
	return err == nil
}

// TestC2 (needs `foreign up --cps 3 --workers 0`): a control plane of a 3-member cluster is reclaimed gracefully and the
// old cluster keeps two healthy etcd members; a second one, of what is now a 2-member cluster, is refused.
func TestC2ControlPlanes(t *testing.T) {
	r := newRig(t, foreign.Options{ControlPlanes: 3, Workers: 0})
	tc := ftc(t, r.f)
	victim, other, keeper := r.f.CPs[2], r.f.CPs[1], r.f.CPs[0]
	uuid := uuidOf(t, victim, tc)
	members, _ := talosctl.EtcdMembers(ns, keeper, tc)
	t.Logf("etcd members before:\n%s", members)

	out, err := r.reclaim(t, victim, "--confirm", uuid)
	if err != nil {
		t.Fatalf("C2: graceful reclaim of one of three control planes failed: %v", err)
	}
	if !strings.Contains(out, "graceful: true") || !strings.Contains(out, "is in maintenance mode with UUID "+uuid) {
		t.Errorf("C2: not graceful or not in maintenance")
	}
	members, err = talosctl.EtcdMembers(ns, keeper, tc)
	t.Logf("etcd members after (err=%v):\n%s", err, members)
	if n := strings.Count(members, "ichor-e2e-fj-cp"); n != 2 {
		t.Errorf("C2: etcd lists %d members after the reclaim, want 2", n)
	}
	// talosctl health reaches every etcd check (healthy, consistent members) but then fails on the Kubernetes Node object
	// of the reclaimed machine, which a graceful reset leaves behind (cordoned, drained, not deleted).
	h, _ := talosctl.Health(ns, []string{keeper, other}, tc)
	if !strings.Contains(h, "waiting for etcd to be healthy: OK") || !strings.Contains(h, "waiting for etcd members to be consistent across nodes: OK") {
		t.Errorf("C2: etcd of the old cluster is not healthy with two members:\n%s", h)
	}
	t.Logf("old cluster nodes: %s", func() string { o, _ := inner(r.f, "get", "nodes", "--no-headers"); return o }())

	// A 2-member cluster: removing one leaves 1 of 2, no quorum.
	uuid2 := uuidOf(t, other, tc)
	out, err = r.reclaim(t, other, "--confirm", uuid2)
	if err == nil || !strings.Contains(out, "Refused") {
		t.Errorf("C2: a control plane of a 2-member cluster was not refused: %v", err)
	}
	if got := uuidOf(t, other, tc); got != uuid2 {
		t.Errorf("C2: the node changed after a refusal")
	}
}

// TestC3Force: the only control plane of a foreign cluster is reclaimed with --force, the cluster name typed a
// second time and --force-ungraceful. This destroys the foreign cluster; run it last.
func TestC3Force(t *testing.T) {
	r := newRig(t, foreign.Options{Workers: 1, DataDisk: true})
	cp := r.f.CPs[0]
	uuid := uuidOf(t, cp, ftc(t, r.f))
	// --force without the second typed confirmation is refused first.
	if out, err := r.reclaim(t, cp, "--confirm", uuid, "--force", "--force-ungraceful"); err == nil {
		t.Errorf("--force without --confirm-cluster was accepted: %s", out)
	}
	if got := uuidOf(t, cp, ftc(t, r.f)); got != uuid {
		t.Fatalf("control plane changed after a refusal")
	}
	out, err := r.reclaim(t, cp, "--confirm", uuid, "--force", "--confirm-cluster", r.f.Opts.Prefix, "--force-ungraceful")
	if err != nil {
		t.Fatalf("forced reclaim failed: %v", err)
	}
	if !strings.Contains(out, "is in maintenance mode with UUID "+uuid) {
		t.Errorf("no maintenance confirmation")
	}
}

// TestC9Restart: the controller is restarted while a request waits for confirmation; the request carries on, resets
// once and the Secret is deleted at Done.
func TestC9Restart(t *testing.T) {
	r := newRig(t, foreign.Options{Workers: 1, DataDisk: true})
	r.ensureForeign(t)
	tc := ftc(t, r.f)
	uuid := uuidOf(t, r.foreignW, tc)
	tcPath, secrets := r.markedTalosconfig(t, "c9r")
	_, _ = r.ikDelete("-n", crNS, "create", "rolebinding", "reclaim-lane2", "--clusterrole=ichor-machine-reclaimer", "--serviceaccount=ichor-system:ichor-ichor-ui")
	t.Cleanup(func() { _, _ = r.ikDelete("-n", crNS, "delete", "rolebinding", "reclaim-lane2", "--ignore-not-found") })

	name, _, err := r.newReclaim(t, tcPath, r.foreignW)
	if err != nil {
		t.Fatal(err)
	}
	r.waitPhase(t, name, 3*time.Minute, "AwaitingConfirmation")
	cred := r.ikJSON(t, "-n", crNS, "get", "machinereclaims.ichor.maesh.dev", name, "-o", "jsonpath={.spec.credentialSecretRef}")

	// Restart the controller mid-request.
	if out, err := r.ikDelete("-n", crNS, "delete", "pod", "-l", "control-plane=controller-manager", "--wait=true"); err != nil {
		t.Fatalf("deleting the controller pod: %v\n%s", err, out)
	}
	if _, err := kube.Poll("controller Ready again", 5*time.Minute, func() (string, bool) {
		v := r.ikJSON(t, "-n", crNS, "get", "deploy", "ichor-controller-manager", "-o", "jsonpath={.status.readyReplicas}")
		return v, v == "1"
	}); err != nil {
		t.Fatal(err)
	}
	if got := r.ikJSON(t, "-n", crNS, "get", "machinereclaims.ichor.maesh.dev", name, "-o", "jsonpath={.status.phase}"); got != "AwaitingConfirmation" {
		t.Errorf("phase after the restart = %q, want AwaitingConfirmation", got)
	}
	if got := uuidOf(t, r.foreignW, tc); got != uuid {
		t.Errorf("the node changed during the restart")
	}
	if out, err := r.ui(t, "confirm", "-name", name, "-uuid", uuid); err != nil {
		t.Fatalf("confirm after the restart: %v\n%s", err, out)
	}
	r.waitPhase(t, name, 6*time.Minute, "Done")
	if got := waitMaintenance(t, r.foreignW, 6*time.Minute); got != uuid {
		t.Errorf("maintenance UUID = %q, want %q", got, uuid)
	}
	if !r.gone(t, "secret", cred, time.Minute) {
		t.Errorf("credential Secret %s still exists after Done", cred)
	}
	noLeaks(t, secrets, map[string]string{
		"controller log": r.logs(t, "ichor-controller-manager"), "ichor-ui log": r.logs(t, "ichor-ichor-ui"),
		"events": r.ikJSON(t, "get", "events", "-A", "-o", "yaml"),
	})
}

// TestC9FailedExpiredDeleted: the credential Secret is gone when a request Fails (unreachable node), when it
// Expires (ttl 1m, never confirmed) and when the request is deleted early (owner reference).
func TestC9FailedExpiredDeleted(t *testing.T) {
	r := newRig(t, foreign.Options{Workers: 1, DataDisk: true})
	r.ensureForeign(t)
	tc := string(ftc(t, r.f))
	_, secrets := r.markedTalosconfig(t, "c9f")

	// Failed: nothing answers at the address.
	failed, fsec := r.adminReclaim(t, "10.244.1.250", "5m", tc)
	phase := r.waitPhase(t, failed, 4*time.Minute, "Failed", "Expired")
	t.Logf("unreachable node: phase %s, message %q", phase, r.ikJSON(t, "-n", crNS, "get", "machinereclaims.ichor.maesh.dev", failed, "-o", "jsonpath={.status.conditions[*].message}"))
	if !r.gone(t, "secret", fsec, time.Minute) {
		t.Errorf("C9: the Secret %s survives a Failed request", fsec)
	}

	// Expired: inspected, never confirmed.
	expired, esec := r.adminReclaim(t, r.foreignW, "1m", tc)
	r.waitPhase(t, expired, 3*time.Minute, "AwaitingConfirmation")
	r.waitPhase(t, expired, 4*time.Minute, "Expired")
	if !r.gone(t, "secret", esec, time.Minute) {
		t.Errorf("C9: the Secret %s survives an Expired request", esec)
	}
	if got := r.ikJSON(t, "-n", crNS, "get", "machinereclaims.ichor.maesh.dev", expired, "-o", "jsonpath={.status.audit}"); got == "" {
		t.Errorf("C10: no status.audit on an expired request")
	} else {
		t.Logf("expired request audit: %s", got)
	}
	// An expired request cannot be confirmed into a reset.
	uuid := uuidOf(t, r.foreignW, []byte(tc))
	_, _ = r.ui(t, "confirm", "-name", expired, "-uuid", uuid)
	time.Sleep(20 * time.Second)
	if got := uuidOf(t, r.foreignW, []byte(tc)); got != uuid {
		t.Errorf("C9: the node was reset by an expired request")
	}

	// Deleted early: the Secret goes with the request.
	early, osec := r.adminReclaim(t, r.foreignW, "30m", tc)
	r.waitPhase(t, early, 3*time.Minute, "AwaitingConfirmation")
	if out, err := r.ikDelete("-n", crNS, "delete", "machinereclaims.ichor.maesh.dev", early, "--wait=true"); err != nil {
		t.Fatalf("deleting the request: %v\n%s", err, out)
	}
	if !r.gone(t, "secret", osec, 2*time.Minute) {
		t.Errorf("C9: the Secret %s survives the deletion of its request", osec)
	}
	noLeaks(t, secrets, map[string]string{
		"controller log": r.logs(t, "ichor-controller-manager"), "events": r.ikJSON(t, "get", "events", "-A", "-o", "yaml"),
	})
}
