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

// C8-C10: the UI/controller reclaim path. See reclaim_test.go for the setup.

import (
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

// markedTalosconfig copies the foreign talosconfig with a comment marker and returns the file (inside the Ichor
// tools pod) plus the strings that must never show up anywhere: the marker and fragments of the client key and
// certificate.
func (r *rig) markedTalosconfig(t *testing.T, name string) (podPath string, secrets []string) {
	t.Helper()
	raw := string(ftc(t, r.f))
	marker := "ICHOR-LANE2-MARKER-" + name
	for _, k := range []string{"key", "crt"} {
		m := regexp.MustCompile(`(?m)^\s+` + k + `:\s*(\S{40,})`).FindStringSubmatch(raw)
		if m == nil {
			t.Fatalf("talosconfig has no %s", k)
		}
		secrets = append(secrets, m[1][:40])
	}
	secrets = append(secrets, marker)
	local := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(local, []byte("# "+marker+"\n"+raw), 0o600); err != nil {
		t.Fatal(err)
	}
	podPath = "/tmp/marked-" + name
	if _, err := utils.Run(kube.Command("-n", ns, "cp", local, ichorTools+":"+podPath)); err != nil {
		t.Fatal(err)
	}
	return podPath, secrets
}

// leaks returns which of the secrets appear in text.
func leaks(text string, secrets []string) []string {
	var out []string
	for _, s := range secrets {
		if strings.Contains(text, s) {
			out = append(out, s[:min(len(s), 12)]+"...")
		}
	}
	return out
}

// ensureForeign makes sure the worker is configured by the foreign cluster, undoing an earlier reclaim.
func (r *rig) ensureForeign(t *testing.T) {
	t.Helper()
	if _, err := talosctl.GetAuthenticated(ns, r.foreignW, "machinestatus", ftc(t, r.f)); err == nil {
		return
	}
	nodes := r.ikJSON(t, "get", "nodes", "-o", "name")
	// Forget the Ichor-side objects of that machine first (a pending or approved worker), so the controller does not act on it.
	for _, mname := range strings.Fields(r.ikJSON(t, "-n", crNS, "get", "talosmachines.ichor.maesh.dev", "-o", "jsonpath={.items[*].metadata.name}")) {
		role := r.ikJSON(t, "-n", crNS, "get", "talosmachines.ichor.maesh.dev", mname, "-o", "jsonpath={.spec.role}")
		if !strings.Contains(strings.ToLower(role), "controlplane") {
			_, _ = r.ikDelete("-n", crNS, "annotate", "talosmachines.ichor.maesh.dev", mname, "ichor.maesh.dev/allow-delete=true", "--overwrite")
			out, err := r.ikDelete("-n", crNS, "delete", "talosmachines.ichor.maesh.dev", mname, "--timeout=2m")
			t.Logf("deleting TalosMachine %s: %v %s", mname, err, out)
		}
	}
	r.refoereignize(t, r.ichorTC, "ichor-e2e-fj-w0")
	if strings.Contains(nodes, "ichor-e2e-r-w") {
		_, _ = r.ikDelete("delete", "node", "ichor-e2e-r-w", "--wait=false")
	}
}

func (r *rig) logs(t *testing.T, deploy string) string {
	t.Helper()
	out, err := r.ik("-n", crNS, "logs", "deploy/"+deploy, "--since=3h", "--all-containers=true")
	if err != nil {
		t.Logf("logs %s: %v", deploy, err)
	}
	return out
}

// TestC8C9C10 runs the UI/controller path end to end: off, denied, held at AwaitingConfirmation, wrong UUID, right
// UUID, then the credential hygiene and audit checks on the finished request.
func TestC8C9C10(t *testing.T) {
	r := newRig(t, foreign.Options{Workers: 1, DataDisk: true})
	if on := r.ikJSON(t, "get", "ichorinstallations.ichor.maesh.dev", "ichor", "-o", "jsonpath={.spec.reclaim.enabled}"); on != "true" {
		t.Fatalf("reclaim is not enabled (spec.reclaim.enabled = %q); enable it with the confirm annotation first", on)
	}
	r.ensureForeign(t)
	tc := ftc(t, r.f)
	uuid := uuidOf(t, r.foreignW, tc)
	tcPath, secrets := r.markedTalosconfig(t, "c9")

	// C8: without the reclaimer binding the webhook denies (ichor-ui acts as its ServiceAccount here: users are off).
	_, _ = r.ikDelete("-n", crNS, "delete", "rolebinding", "reclaim-lane2", "--ignore-not-found")
	_, out, err := r.newReclaim(t, tcPath, r.foreignW)
	if err == nil || !strings.Contains(out, "reclaim") {
		t.Errorf("C8: a caller without the reclaim verb was not denied: %v / %s", err, out)
	}
	if got := r.ikJSON(t, "-n", crNS, "get", "machinereclaims.ichor.maesh.dev", "-o", "name"); got != "" && !strings.Contains(got, "reclaim-") {
		t.Logf("MachineReclaims after the denial: %s", got)
	}

	if _, err := r.ik("-n", crNS, "create", "rolebinding", "reclaim-lane2", "--clusterrole=ichor-machine-reclaimer", "--serviceaccount=ichor-system:ichor-ichor-ui"); err != nil {
		t.Fatalf("binding the reclaimer role: %v", err)
	}
	t.Cleanup(func() { _, _ = r.ikDelete("-n", crNS, "delete", "rolebinding", "reclaim-lane2", "--ignore-not-found") })

	name, _, err := r.newReclaim(t, tcPath, r.foreignW)
	if err != nil {
		t.Fatalf("C8: create with the binding: %v", err)
	}
	r.waitPhase(t, name, 3*time.Minute, "AwaitingConfirmation")
	mr := r.ikJSON(t, "-n", crNS, "get", "machinereclaims.ichor.maesh.dev", name, "-o", "json")
	t.Logf("held request status:\n%s", jsonField(t, mr, "status.inspection"))
	if got := jsonField(t, mr, "status.inspection.uuid"); got != uuid {
		t.Errorf("inspection uuid = %q, want %q", got, uuid)
	}
	if got := jsonField(t, mr, "status.inspection.clusterName"); got != r.f.Opts.Prefix {
		t.Errorf("inspection clusterName = %q, want %q", got, r.f.Opts.Prefix)
	}
	if !strings.Contains(mr, "\"encrypted\": true") {
		t.Errorf("the encrypted volume is not flagged in the inspection")
	}
	if got := uuidOf(t, r.foreignW, tc); got != uuid { // untouched while held
		t.Errorf("node changed while held")
	}
	if got := jsonField(t, mr, "spec.credentialSecretRef"); got == "" || r.ikJSON(t, "-n", crNS, "get", "secret", got, "-o", "name") == "" {
		t.Errorf("credential Secret %q missing while the request is held", got)
	}

	// Wrong UUID: rejected by the webhook; right one resets.
	if out, err := r.ui(t, "confirm", "-name", name, "-uuid", "00000000-0000-0000-0000-000000000000"); err == nil {
		t.Errorf("C8: a wrong UUID was accepted: %s", out)
	}
	if p := r.ikJSON(t, "-n", crNS, "get", "machinereclaims.ichor.maesh.dev", name, "-o", "jsonpath={.status.phase}"); p != "AwaitingConfirmation" {
		t.Errorf("phase after a wrong UUID = %q", p)
	}
	if got := uuidOf(t, r.foreignW, tc); got != uuid {
		t.Errorf("node changed after a wrong UUID")
	}
	if out, err := r.ui(t, "confirm", "-name", name, "-uuid", uuid); err != nil {
		t.Fatalf("C8: the right UUID was refused: %v\n%s", err, out)
	}
	r.waitPhase(t, name, 6*time.Minute, "Done")
	if got := waitMaintenance(t, r.foreignW, 6*time.Minute); got != uuid {
		t.Errorf("maintenance UUID = %q, want %q", got, uuid)
	}

	// C9: the Secret is gone at Done; the marker and the key fragments are nowhere.
	mr = r.ikJSON(t, "-n", crNS, "get", "machinereclaims.ichor.maesh.dev", name, "-o", "json")
	credName := jsonField(t, mr, "spec.credentialSecretRef")
	if got := r.ikJSON(t, "-n", crNS, "get", "secret", credName, "--ignore-not-found", "-o", "name"); got != "" {
		t.Errorf("C9: the credential Secret %s still exists at Done", credName)
	}
	events := r.ikJSON(t, "get", "events", "-A", "-o", "yaml")
	noLeaks(t, secrets, map[string]string{
		"controller log": r.logs(t, "ichor-controller-manager"), "ichor-ui log": r.logs(t, "ichor-ichor-ui"),
		"events": events, "MachineReclaim": mr,
	})
	// The same request cannot reset twice.
	_, _ = r.ui(t, "confirm", "-name", name, "-uuid", uuid)
	time.Sleep(30 * time.Second)
	resets := strings.Count(r.ikJSON(t, "get", "events", "-A", "-o", "jsonpath={range .items[*]}{.involvedObject.name}{\" \"}{.reason}{\"\\n\"}{end}"), name+" ReclaimReset")
	t.Logf("ReclaimReset events for %s after a second confirm: %d", name, resets)
	if resets != 1 {
		t.Errorf("C9: %d ReclaimReset events, want exactly 1", resets)
	}

	// C10: status.audit and the Events carry user, UUID, foreign cluster and outcome.
	t.Logf("status.audit: %s", jsonField(t, mr, "status.audit"))
	for _, w := range []string{uiSA, uuid} {
		if !strings.Contains(jsonField(t, mr, "status.audit"), w) && !strings.Contains(events, w) {
			t.Errorf("C10: neither status.audit nor Events carry %q", w)
		}
	}
	for _, reason := range []string{"ReclaimRequested", "ReclaimInspected", "ReclaimConfirmed", "ReclaimReset", "ReclaimCompleted", "CredentialDeleted"} {
		if !strings.Contains(events, "reason: "+reason) {
			t.Errorf("C10: no %s Event", reason)
		}
	}
	t.Logf("C10: events for the request:\n%s", r.ikJSON(t, "-n", crNS, "get", "events", "--field-selector", "involvedObject.name="+name,
		"-o", "custom-columns=REASON:.reason,MESSAGE:.message"))
}

// TestEnsureForeign only puts the worker back into the foreign cluster (a long step of its own, so that a case
// fits the tool's time limit).
func TestEnsureForeign(t *testing.T) {
	r := newRig(t, foreign.Options{Workers: 1, DataDisk: true})
	r.ensureForeign(t)
}

// ikDelete deletes objects of the Ichor-made cluster; the inner-cluster helper refuses deletes by design, and this
// cluster is ours (its kubeconfig is the only credential passed).
func (r *rig) ikDelete(args ...string) (string, error) {
	out, err := exec.Command("kubectl", append([]string{"--kubeconfig", r.ichorKC}, args...)...).CombinedOutput()
	return string(out), err
}
