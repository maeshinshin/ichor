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

// B3 (recovered bundle), B4 (KubePrism, enabled mode) and B6 (hold) on one adoption.

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maeshinshin/ichor/test/internal/foreign"
	"github.com/maeshinshin/ichor/test/internal/kube"
	"github.com/maeshinshin/ichor/test/internal/talosctl"
	"github.com/maeshinshin/ichor/test/utils"
)

// sample is one observation of the hold annotation on every TalosMachine and the machineconfig version on every node.
type sample struct {
	at       time.Time
	held     map[string]bool // machine -> hold-apply annotation present
	versions map[string]string
}

// sampler records samples until finish is called.
type sampler struct {
	mu      sync.Mutex
	samples []sample
	stop    chan struct{}
	done    chan struct{}
}

func (s *sampler) start(t *testing.T, c *foreign.Cluster) {
	s.stop, s.done = make(chan struct{}), make(chan struct{})
	tc := ftc(t, c)
	ips := c.Nodes()
	go func() {
		defer close(s.done)
		for {
			select {
			case <-s.stop:
				return
			default:
			}
			smp := sample{at: time.Now(), held: map[string]bool{}, versions: map[string]string{}}
			out, _ := inner(c, "-n", crNS, "get", "talosmachines.ichor.maesh.dev", "-o",
				"jsonpath={range .items[*]}{.metadata.name} {.metadata.annotations.ichor\\.maesh\\.dev/hold-apply}{\"\\n\"}{end}")
			for _, l := range strings.Split(out, "\n") {
				if f := strings.Fields(l); len(f) >= 1 {
					smp.held[f[0]] = len(f) == 2 && f[1] == "true"
				}
			}
			for _, ip := range ips {
				raw, err := talosctl.GetAuthenticated(ns, ip, "machineconfig", tc)
				if err != nil {
					continue
				}
				if objs, _ := talosctl.DecodeAll(raw); len(objs) > 0 {
					md, _ := objs[0]["metadata"].(map[string]any)
					smp.versions[ip] = fmt.Sprint(md["version"])
				}
			}
			s.mu.Lock()
			s.samples = append(s.samples, smp)
			s.mu.Unlock()
		}
	}()
}

func (s *sampler) finish() []sample {
	close(s.stop)
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.samples
}

func allHeld(s sample) bool {
	if len(s.held) == 0 {
		return false
	}
	for _, h := range s.held {
		if !h {
			return false
		}
	}
	return true
}

// analyseHold (B6): no machineconfig version changes between two samples in which every machine was held; logs
// when each machine was first seen released and when each node's config version first changed.
func analyseHold(t *testing.T, smp []sample) {
	t.Helper()
	if len(smp) < 2 {
		t.Fatalf("only %d samples", len(smp))
	}
	t0 := smp[0].at
	heldSamples, violations := 0, 0
	released := map[string]time.Time{}
	changed := map[string]time.Time{}
	for i, s := range smp {
		if allHeld(s) {
			heldSamples++
		}
		for m, h := range s.held {
			if _, ok := released[m]; !h && !ok {
				released[m] = s.at
			}
		}
		if i == 0 {
			continue
		}
		p := smp[i-1]
		for ip, v := range s.versions {
			if pv, ok := p.versions[ip]; ok && pv != v {
				if _, seen := changed[ip]; !seen {
					changed[ip] = s.at
				}
				if allHeld(p) && allHeld(s) {
					violations++
					t.Errorf("node %s config version %s -> %s between two samples with every machine held", ip, pv, v)
				}
			}
		}
	}
	t.Logf("B6: %d samples, %d with every machine held, %d apply-while-held violations", len(smp), heldSamples, violations)
	for m, at := range released {
		t.Logf("B6: machine %s first seen released at +%s", m, at.Sub(t0).Round(time.Second))
	}
	for ip, at := range changed {
		t.Logf("B6: node %s machineconfig version first changed at +%s", ip, at.Sub(t0).Round(time.Second))
	}
	if heldSamples == 0 {
		t.Errorf("never observed every machine held: the hold window was not sampled")
	}
}

// TestB3B4B6EnabledAdopt (B2 enabled, B3 recovery, B4 enabled-mode KubePrism, B6 hold): adopts the cluster of
// TestB2RefusedEnabledReplacementOn after its replacement was switched off (TestHelmSetKPR).
func TestB3B4B6EnabledAdopt(t *testing.T) {
	c := build(t, bOpts(foreign.CNICiliumHelm, false, true, "kubeProxyReplacement=true"))
	b3Canary(t, c)
	prism := kubePrismState(t, c)
	t.Logf("KubePrism before adoption: %v", prism)
	plan, err := adoptArgs(t, c, append(recoverFlags(c), "--plan", "--accept-cilium-diff")...)
	if err != nil {
		t.Fatalf("adopt --plan: %v", err)
	}
	if strings.Contains(plan, "drift found") {
		t.Errorf("B3: the recovered bundle left drift")
	}
	prismOff := true
	for _, v := range prism {
		prismOff = prismOff && v == ""
	}
	if prismOff && !strings.Contains(plan, "KubePrismConfig") && !strings.Contains(plan, "kubePrism") {
		t.Errorf("B4: the plan does not list KubePrism as an Ichor-owned (class B) change")
	}
	var smp sampler
	smp.start(t, c)
	_, needed := takeoverAdopt(t, c)
	t.Logf("diff gate needed --accept-cilium-diff: %v", needed)
	waitHandover(t, c, "")
	membership(t, c)
	time.Sleep(2 * time.Minute)
	analyseHold(t, smp.finish())

	if got := kubeProxyMode(c); got != "enabled" {
		t.Errorf("B2: spec.cni.kubeProxy = %q, want enabled", got)
	}
	after := kubePrismState(t, c)
	t.Logf("KubePrism after adoption: %v", after)
	for ip, v := range after {
		if !prismOff {
			break
		}
		if v == prism[ip] {
			t.Errorf("B4: KubePrism of %s unchanged by the first render: %q", ip, v)
		}
		if !strings.Contains(v, "7445") && !strings.Contains(v, "true") {
			t.Errorf("B4: KubePrism of %s is not enabled after the first render: %q", ip, v)
		}
	}
	// B3: the controller reaches the nodes with a certificate minted from the recovered bundle.
	_, _ = kube.Poll("TalosMachines Ready", 3*time.Minute, func() (string, bool) {
		st, _ := inner(c, "-n", crNS, "get", "talosmachines.ichor.maesh.dev", "-o",
			"jsonpath={.items[*].status.conditions[?(@.type==\"Ready\")].status}")
		f := strings.Fields(st)
		return "", len(f) == 2 && f[0] == "True" && f[1] == "True"
	})
	st, _ := inner(c, "-n", crNS, "get", "talosmachines.ichor.maesh.dev", "-o",
		"jsonpath={range .items[*]}{.metadata.name}={.status.conditions[?(@.type==\"Ready\")].status}/{.status.conditions[?(@.type==\"Ready\")].reason} {end}")
	t.Logf("TalosMachines Ready: %s", st)
	if strings.Contains(st, "=False") || strings.Contains(st, "=/") {
		t.Errorf("B3: a TalosMachine is not Ready (the controller must reach the nodes): %s", st)
	}
	b3CanaryReadable(t, c)
	if out, err := utils.Run(kube.Command("-n", ns, "exec", c.Opts.Prefix+"-tools", "--", "ls", "-l", "/tmp/home/.ichor/clusters/"+c.Opts.Prefix)); err == nil {
		t.Logf("recovered bundle files:\n%s", out)
	}
}
