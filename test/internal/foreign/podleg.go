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
	"regexp"
	"strconv"
)

// PodLegBound is the most pod-IP failures the traffic window may show while a Ready server pod existed. The client
// counts a pod-IP probe only when the Service's Endpoints listed a Ready server pod at the
// time of the probe, so the failures it counts are the window where a Ready target did not answer: a dead IP still
// listed after its pod was deleted, or a Ready pod that Cilium had not programmed yet. A failed probe takes up to the
// 2 s wget timeout plus the 0.2 s round, so one 10 s stale window yields at most 5 failures. The server moves once in
// TestA1Upgrade (one drain), so two such windows fit: 2 x 5 = 10. This is a judgement; the first run's counts tune it.
const PodLegBound = 10

// podLegRe matches the pod-leg fields of the client's COUNT line (after the service-leg ok and fail).
var podLegRe = regexp.MustCompile(`COUNT \S+ ok=\d+ fail=\d+ pok=(\d+) pfail=(\d+) pskip=(\d+) retarget=(\d+)`)

// PodLeg is the pod-IP leg's cumulative tally in one client pod. OK and Fails count only probes made against a Ready
// server pod; Skipped counts the rounds where the Endpoints had no Ready address.
type PodLeg struct {
	OK, Fails, Skipped, Retargets int
}

// Add returns the sum of two tallies, for a report across the client pods present now.
func (p PodLeg) Add(q PodLeg) PodLeg {
	return PodLeg{OK: p.OK + q.OK, Fails: p.Fails + q.Fails, Skipped: p.Skipped + q.Skipped, Retargets: p.Retargets + q.Retargets}
}

// ParsePodLeg returns the pod-leg tally of the last COUNT line of a client log. ok is false when the log has none,
// or when its COUNT lines predate the pod-leg fields.
func ParsePodLeg(log string) (leg PodLeg, ok bool) {
	m := podLegRe.FindAllStringSubmatch(log, -1)
	if len(m) == 0 {
		return PodLeg{}, false
	}
	last := m[len(m)-1]
	var v [4]int
	for i := range v {
		n, err := strconv.Atoi(last[i+1])
		if err != nil {
			return PodLeg{}, false
		}
		v[i] = n
	}
	return PodLeg{OK: v[0], Fails: v[1], Skipped: v[2], Retargets: v[3]}, true
}

// Check returns an error when the pod-IP leg never reached a Ready server pod, or when it failed more than bound times
// while one existed. The first guards against a leg that passes because it never probed (for example an Endpoints
// lookup that always fails).
func (p PodLeg) Check(bound int) error {
	if p.OK == 0 {
		return fmt.Errorf("pod-IP leg never got a successful probe (%d rounds had no Ready target, %d failed)", p.Skipped, p.Fails)
	}
	if p.Fails > bound {
		return fmt.Errorf("pod-IP leg failed %d times while a Ready server pod existed, bound %d", p.Fails, bound)
	}
	return nil
}
