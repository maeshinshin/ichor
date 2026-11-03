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

// ADR 0103 amendment 5: the webhook refusal during an adopt that takes Cilium over. TestWHAdopt is the takeover
// of TestA1Upgrade (v1.35.10, inline Cilium 1.19.10) with a sampler alongside. Every few seconds the sampler writes
// the controller pods, the webhook Service's endpoints, the CiliumNetworkPolicies and the Cilium agents' pods to a
// log, and the drops of ports 9443 and 8081 that the agents' monitors see. The Cilium endpoint list and the policy
// selectors of the controller's node are dumped whenever they change. The log is <cluster dir>/wh.log.

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maeshinshin/ichor/test/internal/foreign"
	"github.com/maeshinshin/ichor/test/internal/kube"
)

const (
	whSampleEvery = 5 * time.Second
	whReady       = "true" // a custom-columns READY value
	// The custom-columns the sampler prints: name, node, IP, phase, ready, restarts, waiting reason, start time.
	whPodColumns = "NAME:.metadata.name,NODE:.spec.nodeName,IP:.status.podIP,PHASE:.status.phase," +
		"READY:.status.containerStatuses[*].ready,RESTARTS:.status.containerStatuses[*].restartCount," +
		"WAITING:.status.containerStatuses[*].state.waiting.reason,STARTED:.status.startTime"
	whAgentColumns = "NAME:.metadata.name,NODE:.spec.nodeName,UID:.metadata.uid,READY:.status.containerStatuses[*].ready," +
		"RESTARTS:.status.containerStatuses[*].restartCount,STARTED:.status.startTime"
)

// whLog is the sampler's log. Each write is one whole line or block, so the drop streams never interleave with the samples.
type whLog struct {
	mu    sync.Mutex
	f     *os.File
	start time.Time
}

func (w *whLog) printf(format string, args ...any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, _ = fmt.Fprintf(w.f, "%7s "+format+"\n", append([]any{time.Since(w.start).Round(time.Second)}, args...)...)
}

func (w *whLog) block(title, text string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, _ = fmt.Fprintf(w.f, "%7s === %s ===\n%s\n", time.Since(w.start).Round(time.Second), title, strings.TrimSpace(text))
}

// oneLine joins a multi-line kubectl or cilium-dbg answer for a summary line.
func oneLine(s string) string {
	var parts []string
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			parts = append(parts, l)
		}
	}
	return strings.Join(parts, " ; ")
}

// whDrops follows the drops one agent's monitor reports for ports 9443 and 8081 until the command is killed.
func whDrops(c *foreign.Cluster, w *whLog, pod string) *exec.Cmd {
	cmd := kube.InnerCommand(c.LocalKube, "-n", "kube-system", "exec", pod, "--", "cilium-dbg", "monitor", "--type", "drop")
	out, err := cmd.StdoutPipe()
	if err != nil || cmd.Start() != nil {
		w.printf("drop stream %s did not start: %v", pod, err)

		return nil
	}
	w.printf("drop stream %s started", pod)
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			if l := sc.Text(); strings.Contains(l, "9443") || strings.Contains(l, "8081") {
				w.printf("drop %s: %s", pod, l)
			}
		}
		w.printf("drop stream %s ended", pod)
	}()

	return cmd
}

// whSample writes one summary line per tick until stop is closed.
func whSample(c *foreign.Cluster, w *whLog, stop <-chan struct{}) {
	streams := map[string]*exec.Cmd{}
	last := map[string]string{}
	defer func() {
		for _, cmd := range streams {
			if cmd != nil && cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
		}
	}()
	tick := time.NewTicker(whSampleEvery)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
		}
		pods, _ := inner(c, "-n", crNS, "get", "pods", "-l", "app.kubernetes.io/name=ichor", "--no-headers",
			"-o", "custom-columns="+whPodColumns)
		ep, _ := inner(c, "-n", crNS, "get", "endpoints", "ichor-webhook-service", "-o",
			"jsonpath=ready={.subsets[*].addresses[*].ip} notReady={.subsets[*].notReadyAddresses[*].ip}")
		cnp, _ := inner(c, "-n", crNS, "get", "ciliumnetworkpolicies", "--no-headers", "-o", "custom-columns=NAME:.metadata.name")
		cils, _ := inner(c, "-n", "kube-system", "get", "pods", "-l", "k8s-app=cilium", "--no-headers",
			"-o", "custom-columns="+whAgentColumns)
		w.printf("pods {%s} | webhook %s | cnp {%s} | cilium {%s}", oneLine(pods), oneLine(ep), oneLine(cnp), oneLine(cils))

		var ctrlNode, ctrlIP string
		var ctrlReady bool
		for _, l := range strings.Split(pods, "\n") {
			if f := strings.Fields(l); len(f) >= 5 && strings.Contains(f[0], "controller-manager") {
				ctrlNode, ctrlIP, ctrlReady = f[1], f[2], f[4] == whReady

				break
			}
		}
		if ctrlNode != "" && !ctrlReady {
			ev, _ := inner(c, "-n", crNS, "get", "events", "--sort-by=.lastTimestamp", "--no-headers")
			if lines := strings.Split(strings.TrimSpace(ev), "\n"); len(lines) > 15 {
				ev = strings.Join(lines[len(lines)-15:], "\n")
			}
			if ev != last["events"] {
				last["events"] = ev
				w.block("events in ichor-system while the controller is not Ready", ev)
			}
		}
		for _, l := range strings.Split(cils, "\n") {
			f := strings.Fields(l)
			if len(f) == 0 {
				continue
			}
			if _, ok := streams[f[0]]; !ok {
				streams[f[0]] = whDrops(c, w, f[0])
			}
		}
		for _, l := range strings.Split(cils, "\n") {
			f := strings.Fields(l)
			if len(f) < 2 || f[1] != ctrlNode || ctrlNode == "" {
				continue
			}
			agent := f[0]
			if out, err := inner(c, "-n", "kube-system", "exec", agent, "--", "cilium-dbg", "endpoint", "list"); err == nil {
				if out != last["endpoints"] {
					last["endpoints"] = out
					w.block(fmt.Sprintf("endpoint list on %s (controller %s %s)", agent, ctrlNode, ctrlIP), out)
				}
			}
			if out, err := inner(c, "-n", "kube-system", "exec", agent, "--", "cilium-dbg", "policy", "selectors"); err == nil {
				if out != last["selectors"] {
					last["selectors"] = out
					w.block("policy selectors on "+agent, out)
				}
			}
		}
	}
}

// TestWHAdopt (ADR 0103 amendment 5): the takeover of TestA1Upgrade with the webhook sampler alongside. Run it
// attached to a cluster `foreign up --cps 1 --workers 1 --cni cilium-inline --k8s v1.35.10` brought up
// (ICHOR_E2E_F_ATTACH=1, ICHOR_E2E_F_KEEP=1).
func TestWHAdopt(t *testing.T) {
	c := build(t, foreign.Options{Workers: 1, CNI: foreign.CNICiliumInline, K8sVersion: foreignK8sUpgradeStart})
	trafficStart(t, c)
	logState(t, c, "before takeover")
	f, err := os.OpenFile(filepath.Join(c.Dir, "wh.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck
	w := &whLog{f: f, start: time.Now()}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() { whSample(c, w, stop) })
	defer func() { close(stop); wg.Wait() }()
	takeoverAdopt(t, c)
	waitHandover(t, c, "cilium")
	membership(t, c)
}
