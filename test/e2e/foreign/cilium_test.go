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

// Group A (Cilium takeover). Each case is split into tests that run against a cluster
// `foreign up` brought up (ICHOR_E2E_F_ATTACH=1, ICHOR_E2E_F_KEEP=1), so every step stays inside one
// foreground tool call. A4 (data-plane continuity) is the traffic pods the A1 and A2 tests start.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/maeshinshin/ichor/test/internal/foreign"
)

const (
	handoverPath = "{.status.conditions[?(@.type==\"CNIHandover\")].status}"
	trafficPods  = `apiVersion: v1
kind: Service
metadata:
  name: fa-srv
  namespace: default
spec:
  selector:
    app: fa-srv
  ports:
    - port: 80
      targetPort: 8080
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: fa-srv
  namespace: default
spec:
  replicas: 1
  selector:
    matchLabels:
      app: fa-srv
  template:
    metadata:
      labels:
        app: fa-srv
    spec:
      # Preferred, not nodeName: a drain must be able to move the pod off the node.
      affinity:
        nodeAffinity:
          preferredDuringSchedulingIgnoredDuringExecution:
            - weight: 100
              preference:
                matchExpressions:
                  - {key: kubernetes.io/hostname, operator: In, values: ["%s"]}
      tolerations:
        - {key: node-role.kubernetes.io/control-plane, operator: Exists, effect: NoSchedule}
      containers:
        - name: srv
          image: busybox:1.37
          command: ["sh", "-c", "mkdir -p /www; echo ok > /www/index.html; exec httpd -f -p 8080 -h /www"]
`
	trafficClient = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: fa-client
  namespace: default
spec:
  replicas: 1
  selector:
    matchLabels:
      app: fa-client
  template:
    metadata:
      labels:
        app: fa-client
    spec:
      # Preferred, not nodeName: a drain must be able to move the pod off the node.
      affinity:
        nodeAffinity:
          preferredDuringSchedulingIgnoredDuringExecution:
            - weight: 100
              preference:
                matchExpressions:
                  - {key: kubernetes.io/hostname, operator: In, values: ["%s"]}
      tolerations:
        - {key: node-role.kubernetes.io/control-plane, operator: Exists, effect: NoSchedule}
      containers:
        - name: client
          image: busybox:1.37
          env:
            - {name: SVC_IP, value: "%s"}
            - {name: POD_IP, value: "%s"}
          command:
            - sh
            - -c
            - |
              ok=0; fail=0; n=0
              while true; do
                for u in "http://$SVC_IP" "http://$POD_IP:8080"; do
                  if out=$(wget -q -T 2 -O /dev/null "$u" 2>&1); then ok=$((ok+1)); else fail=$((fail+1)); echo "FAIL $(date +%%T) $u: $out"; fi
                done
                n=$((n+1)); [ $((n %% 25)) -eq 0 ] && echo "COUNT $(date +%%T) ok=$ok fail=$fail"
                sleep 0.2
              done
`
)

func writeApply(t *testing.T, c *foreign.Cluster, name, manifest string) {
	t.Helper()
	f := filepath.Join(c.Dir, name)
	if err := os.WriteFile(f, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := inner(c, "apply", "-f", f); err != nil {
		t.Fatalf("apply %s: %v\n%s", name, err, out)
	}
}

// waitDeploy waits until a traffic Deployment has its replica available.
func waitDeploy(t *testing.T, c *foreign.Cluster, name string) {
	t.Helper()
	if out, err := inner(c, "wait", "deployment/"+name, "--for=condition=Available", "--timeout=240s"); err != nil {
		t.Fatalf("deployment %s not Available: %v\n%s", name, err, out)
	}
}

// trafficPodNames returns the names of the pods of an A4 app, selected by label: a drain replaces them with new names.
func trafficPodNames(c *foreign.Cluster, app string) []string {
	out, _ := inner(c, "get", "pods", "-l", "app="+app, "-o", "jsonpath={.items[*].metadata.name}")
	return strings.Fields(out)
}

// trafficPodIP returns the pod IP of an A4 app's pod, selected by label.
func trafficPodIP(c *foreign.Cluster, app string) string {
	out, _ := inner(c, "get", "pod", "-l", "app="+app, "-o", "jsonpath={.items[0].status.podIP}")
	return strings.TrimSpace(out)
}

// trafficStart (A4) runs a server Deployment on the control plane and a client Deployment on the worker, so the
// traffic crosses nodes: every 0.2 s the client fetches the Service and the server's pod IP.
func trafficStart(t *testing.T, c *foreign.Cluster) {
	t.Helper()
	cp, w := c.Opts.Prefix+"-cp0", c.Opts.Prefix+"-w0"
	writeApply(t, c, "traffic-srv.yaml", fmt.Sprintf(trafficPods, cp))
	waitDeploy(t, c, "fa-srv")
	podIP := trafficPodIP(c, "fa-srv")
	svcIP, _ := inner(c, "get", "svc", "fa-srv", "-o", "jsonpath={.spec.clusterIP}")
	writeApply(t, c, "traffic-client.yaml", fmt.Sprintf(trafficClient, w, strings.TrimSpace(svcIP), podIP))
	waitDeploy(t, c, "fa-client")
	time.Sleep(10 * time.Second)
	t.Logf("traffic started (service %s, pod %s)", strings.TrimSpace(svcIP), podIP)
}

var countRe = regexp.MustCompile(`COUNT (\S+) ok=(\d+) fail=(\d+)`)

// podLegFail reports whether a FAIL line is from the pod-IP leg (the URL ends in :8080) rather than the Service leg.
func podLegFail(l string) bool {
	f := strings.Fields(l)
	return len(f) > 2 && strings.HasSuffix(f[2], ":8080:")
}

// trafficReport logs the client's tally and every failure line (A4: the pass criterion is set after the first run).
// The tally lives in the client pod's log, and a drain that replaces the client pod starts a new log at zero, and the
// old log is gone with the old pod. So the report takes the last tally of each client pod that exists now and says so;
// it does not sum across pods, because the replaced pods' tallies are lost.
func trafficReport(t *testing.T, c *foreign.Cluster, label string) {
	t.Helper()
	pods := trafficPodNames(c, "fa-client")
	if len(pods) == 0 {
		t.Errorf("A4 traffic %s: no fa-client pod", label)
	}
	var fails []string
	svcFails, podFails := 0, 0
	for _, p := range pods {
		out, _ := inner(c, "logs", p)
		last := ""
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, "FAIL") {
				fails = append(fails, l)
				if podLegFail(l) {
					podFails++
				} else {
					svcFails++
				}
			}
			if countRe.MatchString(l) {
				last = l
			}
		}
		t.Logf("A4 traffic %s: client pod %s last tally %q", label, p, last)
	}
	t.Logf("A4 traffic %s: %d failure lines (service leg %d, pod-IP leg %d) across %d client pod(s) present now",
		label, len(fails), svcFails, podFails, len(pods))
	for i, f := range fails {
		if i >= 60 {
			t.Logf("... %d more", len(fails)-i)
			break
		}
		t.Logf("A4 %s", f)
	}
}

// ciliumDS is what a takeover or an upgrade must not disturb: the DaemonSet's generation, image and pod UIDs.
func ciliumDS(c *foreign.Cluster) string {
	gen, _ := inner(c, "-n", "kube-system", "get", "ds", "cilium", "-o", "jsonpath={.metadata.generation} {.spec.template.spec.containers[0].image} {.status.numberReady}/{.status.desiredNumberScheduled}")
	uids, _ := inner(c, "-n", "kube-system", "get", "pods", "-l", "k8s-app=cilium", "-o", "jsonpath={.items[*].metadata.uid}")
	return strings.TrimSpace(gen) + " pods=" + strings.TrimSpace(uids)
}

// helmRecords lists the Helm release record Secrets of the foreign cluster with their resourceVersion.
func helmRecords(c *foreign.Cluster) string {
	out, _ := inner(c, "get", "secrets", "-A", "-l", "owner=helm", "-o",
		"jsonpath={range .items[*]}{.metadata.namespace}/{.metadata.name} rv={.metadata.resourceVersion} status={.metadata.labels.status}{\"\\n\"}{end}")
	return out
}

// inlineNames returns the names of the inline manifests in each node's machine config.
func inlineNames(t *testing.T, c *foreign.Cluster) map[string][]string {
	t.Helper()
	res := map[string][]string{}
	for _, ip := range append(append([]string{}, c.CPs...), c.Workers...) {
		doc, kubeDocs := configDocs(t, c, ip)
		names := []string{}
		cl, _ := doc["cluster"].(map[string]any)
		if list, ok := cl["inlineManifests"].([]any); ok {
			for _, e := range list {
				if m, ok := e.(map[string]any); ok {
					names = append(names, fmt.Sprint(m["name"]))
				}
			}
		}
		for _, d := range kubeDocs {
			if m, ok := d.(map[string]any); ok && m["kind"] == "KubeInlineManifestConfig" {
				names = append(names, fmt.Sprint(m["name"]))
			}
		}
		res[ip] = names
	}
	return res
}

func hasName(m map[string][]string, name string) (present, absent int) {
	for _, l := range m {
		found := false
		for _, n := range l {
			found = found || n == name
		}
		if found {
			present++
		} else {
			absent++
		}
	}
	return
}

// waitHandover polls until CNIHandover is True and asserts the inline entry (when there is one) was never gone from a
// node's machine config while the condition was not yet True ("Ordering").
func waitHandover(t *testing.T, c *foreign.Cluster, inline string) {
	t.Helper()
	start := time.Now()
	for time.Since(start) < 14*time.Minute {
		var present, absent int
		if inline != "" {
			present, absent = hasName(inlineNames(t, c), inline) // sampled first: a later True cannot make an earlier removal late
		}
		h := clusterJSONPath(c, handoverPath)
		t.Logf("%s CNIHandover=%q inline %q present on %d nodes, absent on %d", time.Since(start).Round(time.Second), h, inline, present, absent)
		if inline != "" && absent > 0 && h != "True" {
			t.Errorf("inline entry %q removed from %d node(s) before CNIHandover was True", inline, absent)
		}
		if h == "True" {
			return
		}
		time.Sleep(10 * time.Second)
	}
	t.Fatal("CNIHandover did not become True in 14 minutes")
}

func logState(t *testing.T, c *foreign.Cluster, label string) {
	t.Helper()
	t.Logf("%s: cilium DS %s\nhelm records:\n%s", label, ciliumDS(c), helmRecords(c))
}

// takeoverAdopt runs the plan, then adopt; a refusal naming --accept-cilium-diff is logged and retried with it.
// It returns the diff-gate verdict.
func takeoverAdopt(t *testing.T, c *foreign.Cluster, extra ...string) (first string, diffNeeded bool) {
	t.Helper()
	_, _ = adopt(t, c, append([]string{"--plan"}, extra...)...) // the plan's exit is logged by adopt
	out, err := adopt(t, c, extra...)
	first = out
	if err != nil && strings.Contains(out, "--accept-cilium-diff") {
		t.Logf("the diff gate refused without --accept-cilium-diff; retrying with it")
		diffNeeded = true
		out, err = adopt(t, c, append([]string{"--accept-cilium-diff"}, extra...)...)
	}
	if err != nil {
		t.Fatalf("adopt failed: %v\n%s", err, out)
	}
	return first, diffNeeded
}

// TestA1Adopt: Cilium in a Talos inline manifest (Talos-guide values, 1.18) is taken over by a managed adopt.
func TestA1Adopt(t *testing.T) {
	c := build(t, foreign.Options{Workers: 1, CNI: foreign.CNICiliumInline})
	logState(t, c, "before")
	trafficStart(t, c)
	before := inlineNames(t, c)
	t.Logf("inline manifests before: %v", before)
	_, needed := takeoverAdopt(t, c)
	t.Logf("diff gate needed --accept-cilium-diff: %v", needed)
	waitHandover(t, c, "cilium")
	membership(t, c)
	if got := clusterJSONPath(c, "{.status.cni.takenOverInlineManifest}"); got != "cilium" {
		t.Errorf("status.cni.takenOverInlineManifest = %q, want cilium", got)
	}
	if p, a := hasName(inlineNames(t, c), "cilium"); p != 0 {
		t.Errorf("inline entry still on %d nodes (absent on %d)", p, a)
	}
	jobs, _ := inner(c, "-n", crNS, "get", "jobs", "--no-headers")
	t.Logf("jobs in %s:\n%s", crNS, jobs)
	logs, _ := inner(c, "-n", crNS, "logs", "-l", "job-name", "--tail=40", "--prefix")
	t.Logf("ichor-cni job log:\n%s", logs)
	logState(t, c, "after takeover")
	nodesReady(t, c)
	trafficReport(t, c, "across A1 takeover")
}

// TestA1Upgrade: a Kubernetes patch upgrade afterwards does not roll Cilium back. Run after TestA1Adopt.
func TestA1Upgrade(t *testing.T) {
	c := build(t, foreign.Options{Workers: 1, CNI: foreign.CNICiliumInline})
	before, recBefore := ciliumDS(c), helmRecords(c)
	logState(t, c, "before upgrade")
	target := env("ICHOR_E2E_F_K8S_TARGET", "v1.37.1")
	out, err := ichorCLI(t, c, "upgrade", "kubernetes", target, "--yes")
	if err != nil {
		t.Fatalf("upgrade kubernetes: %v\n%s", err, out)
	}
	deadline := time.Now().Add(8 * time.Minute)
	for time.Now().Before(deadline) {
		ph, _ := inner(c, "-n", crNS, "get", "talosoperations.ichor.maesh.dev", "-o", "jsonpath={.items[*].status.phase}")
		t.Logf("operations phases: %q", ph)
		if strings.Contains(ph, "Completed") && !strings.Contains(ph, "InProgress") && !strings.Contains(ph, "Pending") {
			break
		}
		time.Sleep(15 * time.Second)
	}
	vers, _ := inner(c, "get", "nodes", "-o", "jsonpath={.items[*].status.nodeInfo.kubeletVersion}")
	t.Logf("kubelet versions: %s", vers)
	if strings.Contains(vers, "v1.37.0") {
		t.Errorf("a node is still on v1.37.0: %s", vers)
	}
	if p, _ := hasName(inlineNames(t, c), "cilium"); p != 0 {
		t.Errorf("the cilium inline entry is back on %d node(s)", p)
	}
	after, recAfter := ciliumDS(c), helmRecords(c)
	if after != before {
		t.Errorf("Cilium changed across the Kubernetes upgrade:\nbefore: %s\nafter:  %s", before, after)
	}
	if recAfter != recBefore {
		t.Errorf("Helm records changed across the Kubernetes upgrade:\nbefore:\n%s\nafter:\n%s", recBefore, recAfter)
	}
	trafficReport(t, c, "across A1 takeover and the Kubernetes upgrade")
}

// secretData returns the base64 values of a Secret's keys as the API serves them.
func secretData(c *foreign.Cluster, nsName, name string) map[string]string {
	out, err := inner(c, "-n", nsName, "get", "secret", name, "-o", "json")
	if err != nil {
		return nil
	}
	var s struct {
		Data map[string]string `json:"data"`
	}
	if json.Unmarshal([]byte(out), &s) != nil {
		return nil
	}
	return s.Data
}

// TestA2Adopt: `helm install cilium` in kube-system with user values is taken over; the values are
// lifted, the foreign record is backed up byte-for-byte and its Secrets (not the release) removed.
// Run `foreign up --cni cilium-helm --cilium-set operator.replicas=1,debug.enabled=true` first.
func TestA2Adopt(t *testing.T) {
	c := build(t, foreign.Options{Workers: 1, CNI: foreign.CNICiliumHelm})
	logState(t, c, "before")
	recs := map[string]string{} // record name -> base64 of data.release
	names, _ := inner(c, "-n", "kube-system", "get", "secrets", "-l", "owner=helm,name=cilium", "-o", "jsonpath={.items[*].metadata.name}")
	for _, n := range strings.Fields(names) {
		recs[n] = secretData(c, "kube-system", n)["release"]
	}
	if len(recs) == 0 {
		t.Fatal("no foreign cilium release record")
	}
	trafficStart(t, c)
	_, needed := takeoverAdopt(t, c)
	t.Logf("diff gate needed --accept-cilium-diff: %v", needed)
	waitHandover(t, c, "")
	membership(t, c)
	vals := clusterJSONPath(c, "{.spec.cni.cilium.values}")
	t.Logf("spec.cni.cilium.values = %s", vals)
	if !strings.Contains(vals, `"operator":{"replicas":1}`) || !strings.Contains(vals, `"debug":{"enabled":true}`) {
		t.Errorf("user values not lifted: %s", vals)
	}
	backup := secretData(c, crNS, "ichor-cilium-prior-release")
	for n, want := range recs {
		if got := backup["kube-system."+n+".release"]; got != want || got == "" {
			t.Errorf("backup of %s is not byte-identical (got %d bytes b64, want %d)", n, len(got), len(want))
		} else {
			t.Logf("backup of %s byte-identical (%d bytes b64)", n, len(got))
		}
	}
	left, _ := inner(c, "-n", "kube-system", "get", "secrets", "-l", "owner=helm", "-o", "jsonpath={.items[*].metadata.name}")
	if strings.TrimSpace(left) != "" {
		t.Errorf("release record Secrets remain in kube-system:\n%s", left)
	}
	if ds, _ := inner(c, "-n", "kube-system", "get", "ds", "cilium", "--no-headers"); !strings.Contains(ds, "cilium") {
		t.Errorf("the cilium DaemonSet is gone (helm uninstall?)")
	}
	logState(t, c, "after takeover")
	trafficReport(t, c, "across A2 takeover")
}

// TestA5Adopt: live-only residue. An extra pod-template annotation and an extra agent argument on the
// live DaemonSet, and an extra ConfigMap, exist before adopt. The diff gate must report the first two and require
// --accept-cilium-diff; the ConfigMap must stay. Run `foreign up --cni cilium-helm --cilium-set tunnelProtocol=vxlan,tunnelPort=4789` first.
func TestA5Adopt(t *testing.T) {
	c := build(t, foreign.Options{Workers: 1, CNI: foreign.CNICiliumHelm})
	patch := `[{"op":"add","path":"/spec/template/metadata/annotations/fa.test~1extra","value":"yes"},` +
		`{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--prometheus-serve-addr=:9962"}]`
	if out, err := inner(c, "-n", "kube-system", "patch", "ds", "cilium", "--type=json", "-p", patch); err != nil {
		t.Fatalf("patching the DaemonSet: %v\n%s", err, out)
	}
	if out, err := inner(c, "-n", "kube-system", "create", "configmap", "fa-extra", "--from-literal=a=b"); err != nil {
		t.Fatalf("creating the ConfigMap: %v\n%s", err, out)
	}
	if out, err := inner(c, "-n", "kube-system", "label", "configmap", "fa-extra", "app.kubernetes.io/part-of=cilium"); err != nil {
		t.Fatalf("labelling the ConfigMap: %v\n%s", err, out)
	}
	if out, err := inner(c, "-n", "kube-system", "rollout", "status", "ds/cilium", "--timeout=300s"); err != nil {
		t.Fatalf("rollout after the patch: %v\n%s", err, out)
	}
	logState(t, c, "before (patched)")
	trafficStart(t, c)
	first, needed := takeoverAdopt(t, c)
	if !needed {
		t.Errorf("the diff gate did not require --accept-cilium-diff")
	}
	for _, want := range []string{"fa.test/extra", "--prometheus-serve-addr"} {
		if !strings.Contains(first, want) {
			t.Errorf("the diff gate output does not report %q", want)
		}
	}
	waitHandover(t, c, "")
	membership(t, c)
	if out, err := inner(c, "-n", "kube-system", "get", "configmap", "fa-extra", "-o", "name"); err != nil {
		t.Errorf("the extra ConfigMap is gone after takeover: %v\n%s", err, out)
	}
	ds, _ := inner(c, "-n", "kube-system", "get", "ds", "cilium", "-o", "jsonpath={.spec.template.metadata.annotations.fa\\.test/extra}|{.spec.template.spec.containers[0].args}")
	t.Logf("live DaemonSet after takeover (annotation|args): %s", ds)
	if out, err := inner(c, "-n", "kube-system", "rollout", "status", "ds/cilium", "--timeout=300s"); err != nil {
		t.Errorf("rollout after takeover: %v\n%s", err, out)
	}
	logState(t, c, "after takeover")
	time.Sleep(30 * time.Second)
	trafficReport(t, c, "across A5 takeover")
}

// refusedWritesNothing asserts that a refused adopt left the machine configs, Ichor's namespace and the Helm records alone.
func refusedWritesNothing(t *testing.T, c *foreign.Cluster, hashes map[string]string, records string) {
	t.Helper()
	sameHashes(t, hashes, cniPaths(t, c))
	if got, err := inner(c, "get", "namespace", crNS, "--no-headers"); err == nil {
		t.Errorf("namespace %s exists after a refusal: %s", crNS, got)
	}
	if got := helmRecords(c); got != records {
		t.Errorf("Helm records changed:\nbefore:\n%s\nafter:\n%s", records, got)
	}
}

// TestA7Refused: ipam.mode cluster-pool is refused, and no accept flag changes that.
// Run `foreign up --workers 0 --cni cilium-helm --cilium-set ipam.mode=cluster-pool,tunnelProtocol=vxlan,tunnelPort=4789` first.
func TestA7Refused(t *testing.T) {
	c := build(t, foreign.Options{Workers: 0, CNI: foreign.CNICiliumHelm})
	hashes, records := cniPaths(t, c), helmRecords(c)
	if mode, _ := inner(c, "-n", "kube-system", "get", "cm", "cilium-config", "-o", "jsonpath={.data.ipam}"); mode != "cluster-pool" {
		t.Fatalf("the live ipam is %q, not cluster-pool", mode)
	}
	for _, flags := range [][]string{nil, {"--accept-cilium-changes", "--accept-cilium-diff"}} {
		out, err := adopt(t, c, flags...)
		if err == nil {
			t.Fatalf("adopt %v succeeded on a cluster-pool Cilium", flags)
		}
		if !strings.Contains(out, "ipam.mode") || !strings.Contains(out, "cannot be taken over") {
			t.Errorf("adopt %v: the refusal does not say ipam.mode cannot be taken over", flags)
		}
		refusedWritesNothing(t, c, hashes, records)
	}
}

// TestA6Refused: a Cilium two minors below the embedded 1.18 (here 1.16.x) is refused and nothing is written.
// (One minor below, 1.17, is accepted as an upgrade by the rule in 1.2; the test plan text and the brief said "1.17 refused".)
// Run `foreign up --workers 0 --cni cilium-helm --cilium-chart bin/foreign/chart116/cilium-1.16.19.tgz --cilium-set tunnelProtocol=vxlan,tunnelPort=4789` first.
func TestA6Refused(t *testing.T) {
	c := build(t, foreign.Options{Workers: 0, CNI: foreign.CNICiliumHelm})
	hashes, records := cniPaths(t, c), helmRecords(c)
	img, _ := inner(c, "-n", "kube-system", "get", "ds", "cilium", "-o", "jsonpath={.spec.template.spec.containers[0].image}")
	t.Logf("live agent image: %s", img)
	out, err := adopt(t, c, "--accept-cilium-changes", "--accept-cilium-diff")
	if err == nil {
		t.Fatal("adopt succeeded on Cilium 1.16")
	}
	if !strings.Contains(out, "more than one minor behind") {
		t.Errorf("the refusal is not the more-than-one-minor message")
	}
	refusedWritesNothing(t, c, hashes, records)
}

// TestA6Accepted117 (the other half of the rule): a Cilium one minor below the embedded 1.18 is taken over and
// upgraded to it. Run `foreign up --cni cilium-helm --cilium-chart bin/foreign/chart117/cilium-1.17.6.tgz --cilium-set tunnelProtocol=vxlan,tunnelPort=4789` first.
func TestA6Accepted117(t *testing.T) {
	c := build(t, foreign.Options{Workers: 1, CNI: foreign.CNICiliumHelm})
	logState(t, c, "before")
	trafficStart(t, c)
	first, needed := takeoverAdopt(t, c)
	t.Logf("diff gate needed --accept-cilium-diff: %v", needed)
	if !strings.Contains(first, "1.17.6") {
		t.Errorf("the plan does not name the live version 1.17.6")
	}
	waitHandover(t, c, "")
	membership(t, c)
	if out, err := inner(c, "-n", "kube-system", "rollout", "status", "ds/cilium", "--timeout=300s"); err != nil {
		t.Errorf("rollout after takeover: %v\n%s", err, out)
	}
	if img := ciliumDS(c); !strings.Contains(img, "cilium:v1.18.0") {
		t.Errorf("the agent was not upgraded to 1.18.0: %s", img)
	}
	logState(t, c, "after takeover")
	time.Sleep(30 * time.Second)
	trafficReport(t, c, "across the A6 1.17 to 1.18 takeover")
}

// TestA1AcceptAll: on the inline-manifest cluster, adopt with every accept flag is still refused for ipam.mode and writes nothing.
func TestA1AcceptAll(t *testing.T) {
	c := build(t, foreign.Options{Workers: 1, CNI: foreign.CNICiliumInline})
	before := cniPaths(t, c)
	out, err := adopt(t, c, "--accept-cilium-changes", "--accept-cilium-diff")
	if err == nil {
		t.Fatal("adopt with the accept flags succeeded")
	}
	if !strings.Contains(out, "ipam.mode") {
		t.Errorf("the refusal does not name ipam.mode")
	}
	sameHashes(t, before, cniPaths(t, c))
	if got, err := inner(c, "get", "namespace", crNS, "--no-headers"); err == nil {
		t.Errorf("namespace %s exists after a refusal: %s", crNS, got)
	}
}
