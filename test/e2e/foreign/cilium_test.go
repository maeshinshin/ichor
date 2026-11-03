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
	trafficClient = `apiVersion: v1
kind: ServiceAccount
metadata:
  name: fa-client
  namespace: default
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: fa-client
  namespace: default
rules:
  - apiGroups: [""]
    resources: ["endpoints"]
    resourceNames: ["fa-srv"]
    verbs: ["get"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: fa-client
  namespace: default
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: fa-client
subjects:
  - {kind: ServiceAccount, name: fa-client, namespace: default}
---
apiVersion: apps/v1
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
      serviceAccountName: fa-client
      containers:
        - name: client
          image: busybox:1.37
          env:
            - {name: SVC_IP, value: "%s"}
          command:
            - sh
            - -c
            - |
              ok=0; fail=0; pok=0; pfail=0; pskip=0; rt=0; n=0; cur=""
              tok=/var/run/secrets/kubernetes.io/serviceaccount/token
              api="https://$KUBERNETES_SERVICE_HOST:$KUBERNETES_SERVICE_PORT/api/v1/namespaces/default/endpoints/fa-srv"
              # The server's Ready address from its Endpoints (notReadyAddresses is cut off first), or nothing.
              ready() {
                wget -q -O - --no-check-certificate --header "Authorization: Bearer $(cat $tok)" "$api" 2>/dev/null |
                  sed 's/"notReadyAddresses".*//' | grep -o '"ip":"[^"]*"' | head -n 1 | cut -d'"' -f4
              }
              while true; do
                if out=$(wget -q -T 2 -O /dev/null "http://$SVC_IP" 2>&1); then
                  ok=$((ok+1))
                else
                  fail=$((fail+1)); echo "FAIL $(date +%%T) http://$SVC_IP: $out"
                fi
                ip=$(ready)
                if [ -z "$ip" ]; then
                  pskip=$((pskip+1))
                else
                  if [ "$ip" != "$cur" ]; then
                    rt=$((rt+1)); echo "RETARGET $(date +%%T) ${cur:-none} -> $ip"; cur=$ip
                  fi
                  if out=$(wget -q -T 2 -O /dev/null "http://$ip:8080" 2>&1); then
                    pok=$((pok+1))
                  else
                    pfail=$((pfail+1)); echo "FAIL $(date +%%T) http://$ip:8080: $out"
                  fi
                fi
                n=$((n+1))
                if [ $((n %% 25)) -eq 0 ]; then
                  echo "COUNT $(date +%%T) ok=$ok fail=$fail pok=$pok pfail=$pfail pskip=$pskip retarget=$rt"
                fi
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

// trafficStart (A4) runs a server Deployment on the control plane and a client Deployment on the worker, so the
// traffic crosses nodes: every 0.2 s the client fetches the Service and, before each pod-IP probe, the server's Ready
// address from its Endpoints (a drain moves the server, so a fixed IP would go stale).
func trafficStart(t *testing.T, c *foreign.Cluster) {
	t.Helper()
	cp, w := c.Opts.Prefix+"-cp0", c.Opts.Prefix+"-w0"
	writeApply(t, c, "traffic-srv.yaml", fmt.Sprintf(trafficPods, cp))
	waitDeploy(t, c, "fa-srv")
	svcIP, _ := inner(c, "get", "svc", "fa-srv", "-o", "jsonpath={.spec.clusterIP}")
	writeApply(t, c, "traffic-client.yaml", fmt.Sprintf(trafficClient, w, strings.TrimSpace(svcIP)))
	waitDeploy(t, c, "fa-client")
	time.Sleep(10 * time.Second)
	t.Logf("traffic started (service %s)", strings.TrimSpace(svcIP))
}

var countRe = regexp.MustCompile(`COUNT (\S+) ok=(\d+) fail=(\d+)`)

// podLegFail reports whether a FAIL line is from the pod-IP leg (the URL ends in :8080) rather than the Service leg.
func podLegFail(l string) bool {
	f := strings.Fields(l)
	return len(f) > 2 && strings.HasSuffix(f[2], ":8080:")
}

// trafficReport logs the client tallies, retargets and failure lines (A4), and returns the pod-IP leg's tally summed
// over the client pods present now. The tally lives in the client pod's log, and a drain that replaces the client pod
// starts a new log at zero, and the old log is gone with the old pod. So the report takes the last tally of each client
// pod that exists now and says so; it does not sum across pods over time, because the replaced pods' tallies are lost.
func trafficReport(t *testing.T, c *foreign.Cluster, label string) foreign.PodLeg {
	t.Helper()
	pods := trafficPodNames(c, "fa-client")
	if len(pods) == 0 {
		t.Errorf("A4 traffic %s: no fa-client pod", label)
	}
	var fails, retargets []string
	var leg foreign.PodLeg
	for _, p := range pods {
		out, _ := inner(c, "logs", p)
		last := ""
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, "FAIL") {
				fails = append(fails, l)
			}
			if strings.HasPrefix(l, "RETARGET") {
				retargets = append(retargets, l)
			}
			if countRe.MatchString(l) {
				last = l
			}
		}
		pl, ok := foreign.ParsePodLeg(out)
		if !ok {
			t.Logf("A4 traffic %s: client pod %s has no pod-IP tally", label, p)
		}
		leg = leg.Add(pl)
		t.Logf("A4 traffic %s: client pod %s last tally %q", label, p, last)
	}
	svcFails := 0
	for _, f := range fails {
		if !podLegFail(f) {
			svcFails++
		}
	}
	t.Logf("A4 traffic %s: service leg %d failure lines; pod-IP leg ok=%d failed=%d skipped=%d retargets=%d",
		label, svcFails, leg.OK, leg.Fails, leg.Skipped, leg.Retargets)
	t.Logf("A4 traffic %s: tallies from the %d client pod(s) present now", label, len(pods))
	for _, r := range retargets {
		t.Logf("A4 %s", r)
	}
	for i, f := range fails {
		if i >= 60 {
			t.Logf("... %d more", len(fails)-i)
			break
		}
		t.Logf("A4 %s", f)
	}
	return leg
}

// probeDiag logs, on one line, where the A4 probe targets are now: the probe pods (UID, node, pod IP, restarts,
// deletion), each node's cordon state and Ready, and each Cilium agent's restarts. A pod-IP probe keeps the server's
// IP from before the upgrade, so a server that moved shows here as a new IP on another node.
func probeDiag(t *testing.T, c *foreign.Cluster, label string) {
	t.Helper()
	pods, _ := inner(c, "get", "pods", "-l", "app in (fa-srv,fa-client)", "-o",
		`jsonpath={range .items[*]}{.metadata.labels.app}/{.metadata.name}/{.metadata.uid}/{.spec.nodeName}/{.status.podIP}/{.status.phase}/restarts={.status.containerStatuses[0].restartCount}{.metadata.deletionTimestamp}{" "}{end}`)
	nodes, _ := inner(c, "get", "nodes", "-o",
		`jsonpath={range .items[*]}{.metadata.name}/unsched={.spec.unschedulable}/ready={.status.conditions[?(@.type=="Ready")].status}{" "}{end}`)
	cilium, _ := inner(c, "-n", "kube-system", "get", "pods", "-l", "k8s-app=cilium", "-o",
		`jsonpath={range .items[*]}{.spec.nodeName}/{.metadata.name}/restarts={.status.containerStatuses[0].restartCount}/ready={.status.containerStatuses[0].ready}{" "}{end}`)
	t.Logf("A4 diag %s %s: pods [%s] nodes [%s] cilium [%s]", time.Now().UTC().Format("15:04:05"), label,
		strings.TrimSpace(pods), strings.TrimSpace(nodes), strings.TrimSpace(cilium))
}

// ciliumDrops returns each Cilium agent's drop counters by reason and direction. They only grow, so the delta over a
// window says whether the datapath dropped packets on that node during it.
func ciliumDrops(c *foreign.Cluster) string {
	pods, _ := inner(c, "-n", "kube-system", "get", "pods", "-l", "k8s-app=cilium", "-o", "jsonpath={.items[*].metadata.name}")
	var b strings.Builder
	for _, p := range strings.Fields(pods) {
		out, _ := inner(c, "-n", "kube-system", "exec", p, "-c", "cilium-agent", "--", "cilium-dbg", "metrics", "list")
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, "drop_count_total") {
				fmt.Fprintf(&b, "%s %s\n", p, strings.TrimSpace(l))
			}
		}
	}
	return b.String()
}

// eventsTail returns the last events of default and kube-system (drains, evictions, kills, restarts) by time.
func eventsTail(c *foreign.Cluster) string {
	var b strings.Builder
	for _, ns := range []string{"default", "kube-system"} {
		out, _ := inner(c, "-n", ns, "get", "events", "--sort-by=.lastTimestamp", "-o",
			"custom-columns=LAST:.lastTimestamp,REASON:.reason,OBJ:.involvedObject.name,MSG:.message")
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) > 60 {
			lines = lines[len(lines)-60:]
		}
		fmt.Fprintf(&b, "%s:\n%s\n", ns, strings.Join(lines, "\n"))
	}
	return b.String()
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
	for _, ip := range c.Nodes() {
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

// TestA1Adopt: Cilium in a Talos inline manifest (Talos-guide values, 1.19) is taken over by a managed adopt.
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

// TestA1Upgrade: a Kubernetes upgrade to the release set afterwards does not roll Cilium back.
// It builds its own cluster at foreignK8sUpgradeStart, takes the inline Cilium over (the adopt that TestA1Adopt
// does at the default minor), then upgrades, so it runs standalone.
func TestA1Upgrade(t *testing.T) {
	c := build(t, foreign.Options{Workers: 1, CNI: foreign.CNICiliumInline, K8sVersion: foreignK8sUpgradeStart})
	trafficStart(t, c)
	logState(t, c, "before takeover")
	takeoverAdopt(t, c)
	waitHandover(t, c, "cilium")
	membership(t, c)
	before, recBefore := ciliumDS(c), helmRecords(c)
	logState(t, c, "before upgrade")
	// The controller re-detects the CNI when the hand-over completes, so the
	// upgrade gate sees the Cilium the takeover installed without a wait.
	t.Logf("status.cni.detectedVersion before the upgrade: %s", clusterJSONPath(c, "{.status.cni.detectedVersion}"))
	probeDiag(t, c, "before upgrade")
	dropsBefore := ciliumDrops(c)
	target := env("ICHOR_E2E_F_K8S_TARGET", "v1.36.6")
	out, err := ichorCLI(t, c, "upgrade", "kubernetes", target, "--yes")
	if err != nil {
		t.Fatalf("upgrade kubernetes: %v\n%s", err, out)
	}
	deadline := time.Now().Add(20 * time.Minute)
	for time.Now().Before(deadline) {
		ph, _ := inner(c, "-n", crNS, "get", "talosoperations.ichor.maesh.dev", "-o", "jsonpath={.items[*].status.phase}")
		t.Logf("operations phases: %q", ph)
		probeDiag(t, c, "upgrade")
		if strings.Contains(ph, "Completed") && !strings.Contains(ph, "InProgress") && !strings.Contains(ph, "Pending") {
			break
		}
		if strings.Contains(ph, "Failed") {
			t.Fatalf("a TalosOperation failed: %q", ph)
		}
		time.Sleep(15 * time.Second)
	}
	vers, _ := inner(c, "get", "nodes", "-o", "jsonpath={.items[*].status.nodeInfo.kubeletVersion}")
	t.Logf("kubelet versions: %s", vers)
	if strings.Contains(vers, foreignK8sUpgradeStart) {
		t.Errorf("a node is still on %s: %s", foreignK8sUpgradeStart, vers)
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
	probeDiag(t, c, "after upgrade")
	t.Logf("cilium drop counters, before the upgrade:\n%s", dropsBefore)
	t.Logf("cilium drop counters, after the upgrade:\n%s", ciliumDrops(c))
	t.Logf("events (default, kube-system):\n%s", eventsTail(c))
	// The pod-IP leg follows the server's Ready address, so its failures are only those where a
	// Ready server pod did not answer. The window since trafficStart includes the takeover and the upgrade.
	leg := trafficReport(t, c, "across A1 takeover and the Kubernetes upgrade")
	if err := leg.Check(foreign.PodLegBound); err != nil {
		t.Errorf("A4 pod-IP leg across the takeover and the upgrade: %v", err)
	}
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
func TestA2Adopt(t *testing.T) { takeoverHelmRecord(t, foreign.CNICiliumHelm) }

// TestA3Adopt: the same expectations for a Cilium installed with `cilium install` (the Cilium CLI writes the Helm
// release).
// Run `foreign up --cni cilium-cli --cilium-set operator.replicas=1,debug.enabled=true` first.
func TestA3Adopt(t *testing.T) { takeoverHelmRecord(t, foreign.CNICiliumCLI) }

func takeoverHelmRecord(t *testing.T, cni string) {
	c := build(t, foreign.Options{Workers: 1, CNI: cni})
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

// TestA6Refused: a Cilium more than one minor below the embedded 1.20.4 (here 1.16.x) is refused and nothing
// is written. One minor below (1.19, TestA6Accepted119) is taken over; two below (1.18) is refused by the same rule.
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

// TestA6Accepted119 (the other half of the rule): a Cilium one minor below the embedded 1.20.4 (the foreign
// default, chart 1.19.10, installed by `make foreign-cilium-chart`) is taken over and upgraded to 1.20.4.
func TestA6Accepted119(t *testing.T) {
	c := build(t, foreign.Options{Workers: 1, CNI: foreign.CNICiliumHelm})
	logState(t, c, "before")
	trafficStart(t, c)
	first, needed := takeoverAdopt(t, c)
	t.Logf("diff gate needed --accept-cilium-diff: %v", needed)
	if !strings.Contains(first, "1.19.10") {
		t.Errorf("the plan does not name the live version 1.19.10")
	}
	waitHandover(t, c, "")
	membership(t, c)
	if out, err := inner(c, "-n", "kube-system", "rollout", "status", "ds/cilium", "--timeout=300s"); err != nil {
		t.Errorf("rollout after takeover: %v\n%s", err, out)
	}
	if img := ciliumDS(c); !strings.Contains(img, "cilium:v1.20.4") {
		t.Errorf("the agent was not upgraded to 1.20.4: %s", img)
	}
	logState(t, c, "after takeover")
	time.Sleep(30 * time.Second)
	trafficReport(t, c, "across the A6 1.19 to 1.20 takeover")
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
