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

import (
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maeshinshin/ichor/test/internal/foreign"
)

// foreignRecs returns the foreign cilium release record Secrets of kube-system (name -> base64 data.release).
func foreignRecs(c *foreign.Cluster) map[string]string {
	recs := map[string]string{}
	names, _ := inner(c, "-n", "kube-system", "get", "secrets", "-l", "owner=helm,name=cilium", "-o", "jsonpath={.items[*].metadata.name}")
	for _, n := range strings.Fields(names) {
		recs[n] = secretData(c, "kube-system", n)["release"]
	}
	return recs
}

// ichorRecords lists Ichor's own Helm release records (in ichor-system) as name=status.
func ichorRecords(c *foreign.Cluster) string {
	out, _ := inner(c, "-n", crNS, "get", "secrets", "-l", "owner=helm", "-o",
		"jsonpath={range .items[*]}{.metadata.name}={.metadata.labels.status}{\"\\n\"}{end}")
	return strings.TrimSpace(out)
}

// cniJobPods returns the pods of the ichor-cni Jobs (pods are named after their Job).
func cniJobPods(c *foreign.Cluster) []string {
	var res []string
	for _, p := range names(c, "-n", "kube-system", "get", "pods", "-l", "job-name") {
		if strings.HasPrefix(p, "ichor-cni") {
			res = append(res, p)
		}
	}
	return res
}

// innerDelete deletes objects of the foreign cluster; the harness helper refuses deletes by design, and this cluster
// is ours (its kubeconfig is the only credential passed).
func innerDelete(c *foreign.Cluster, args ...string) (string, error) {
	out, err := exec.Command("kubectl", append([]string{"--kubeconfig", c.LocalKube}, args...)...).CombinedOutput()
	return string(out), err
}

// TestA8KillJob (ADR 0103 A8): the takeover ichor-cni Job's pod is deleted once Ichor's Helm record exists (during Helm's
// rollout wait, so a pending-* record may be left) and, best effort, once more when Ichor's record is deployed while a
// foreign record is still there. The Job retries; the hand-over must still complete once, with the backup byte-identical.
// Run `foreign up --cni cilium-helm --cilium-set operator.replicas=1,debug.enabled=true` first.
func TestA8KillJob(t *testing.T) {
	c := build(t, foreign.Options{Workers: 1, CNI: foreign.CNICiliumHelm})
	recs := foreignRecs(c)
	if len(recs) == 0 {
		t.Fatal("no foreign cilium release record")
	}
	logState(t, c, "before")
	var mu sync.Mutex
	var events []string
	note := func(f string, a ...any) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, time.Now().Format("15:04:05")+" "+fmt.Sprintf(f, a...))
	}
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		kills := 0
		for kills < 2 {
			select {
			case <-stop:
				return
			case <-time.After(1500 * time.Millisecond):
			}
			mine, pods := ichorRecords(c), cniJobPods(c)
			if mine == "" || len(pods) == 0 {
				continue
			}
			foreignLeft := len(foreignRecs(c))
			deployed := strings.Contains(mine, "=deployed")
			if kills == 0 || (deployed && foreignLeft > 0) {
				pre, _ := inner(c, "-n", "kube-system", "logs", pods[0], "--tail=10")
				note("log of %s before the kill: %s", pods[0], strings.ReplaceAll(strings.TrimSpace(pre), "\n", " | "))
				out, err := innerDelete(c, "-n", "kube-system", "delete", "pod", pods[0], "--wait=false")
				kills++
				note("kill %d of %s: ichor records [%s], foreign records left %d: %v %s", kills, pods[0], strings.ReplaceAll(mine, "\n", "; "), foreignLeft, err, strings.TrimSpace(out))
				time.Sleep(20 * time.Second)
			}
		}
	}()
	start := time.Now()
	_, needed := takeoverAdopt(t, c)
	t.Logf("adopt took %s; diff gate needed --accept-cilium-diff: %v", time.Since(start).Round(time.Second), needed)
	waitHandover(t, c, "")
	close(stop)
	<-done
	for _, e := range events {
		t.Logf("A8 %s", e)
	}
	if len(events) == 0 {
		t.Errorf("the Job pod was never killed")
	}
	membership(t, c)
	recsNow := ichorRecords(c)
	t.Logf("ichor Helm records: %s", strings.ReplaceAll(recsNow, "\n", "; "))
	if strings.Count(recsNow, "=deployed") != 1 || strings.Contains(recsNow, "pending") {
		t.Errorf("want exactly one deployed Ichor record and no pending-* record, got:\n%s", recsNow)
	}
	backup := secretData(c, crNS, "ichor-cilium-prior-release")
	for n, want := range recs {
		if got := backup["kube-system."+n+".release"]; got != want || got == "" {
			t.Errorf("backup of %s is not byte-identical", n)
		} else {
			t.Logf("backup of %s byte-identical (%d bytes b64)", n, len(got))
		}
	}
	if left, _ := inner(c, "-n", "kube-system", "get", "secrets", "-l", "owner=helm", "-o", "jsonpath={.items[*].metadata.name}"); strings.TrimSpace(left) != "" {
		t.Errorf("foreign release record Secrets remain: %s", left)
	}
	jobs, _ := inner(c, "-n", "kube-system", "get", "jobs,pods", "--no-headers")
	t.Logf("jobs and pods in kube-system:\n%s", jobs)
	for _, p := range cniJobPods(c) {
		logs, _ := inner(c, "-n", "kube-system", "logs", p, "--tail=30")
		t.Logf("log of %s:\n%s", p, logs)
	}
	cl, _ := inner(c, "-n", crNS, "logs", "deploy/ichor-controller-manager", "--since=30m")
	for _, l := range strings.Split(cl, "\n") {
		if ll := strings.ToLower(l); strings.Contains(ll, "cni") || strings.Contains(ll, "handover") {
			t.Logf("controller: %.400s", l)
		}
	}
	if h := clusterJSONPath(c, handoverPath); h != "True" {
		t.Errorf("CNIHandover = %q", h)
	}
	nodesReady(t, c)
}
