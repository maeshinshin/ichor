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
	"testing"
)

func TestParsePodLegTakesTheLastTally(t *testing.T) {
	log := "COUNT 10:00:01 ok=25 fail=0 pok=25 pfail=0 pskip=0 retarget=1\n" +
		"RETARGET 10:00:02 none -> 10.0.0.9\n" +
		"FAIL 10:00:03 http://10.0.0.9:8080: wget: timed out\n" +
		"COUNT 10:05:00 ok=1500 fail=2 pok=1400 pfail=3 pskip=7 retarget=2\n"
	got, ok := ParsePodLeg(log)
	if !ok {
		t.Fatal("ParsePodLeg found no tally in a log that has one")
	}
	want := PodLeg{OK: 1400, Fails: 3, Skipped: 7, Retargets: 2}
	if got != want {
		t.Fatalf("ParsePodLeg = %+v, want %+v", got, want)
	}
}

func TestParsePodLegWithoutTally(t *testing.T) {
	if _, ok := ParsePodLeg("FAIL 10:00:03 http://10.0.0.9:8080: wget: timed out\n"); ok {
		t.Fatal("ParsePodLeg reported a tally for a log with no COUNT line")
	}
}

func TestParsePodLegRejectsTheOldFormat(t *testing.T) {
	// A COUNT line without the pod-leg fields carries no pod-leg tally.
	if _, ok := ParsePodLeg("COUNT 10:00:01 ok=25 fail=0\n"); ok {
		t.Fatal("ParsePodLeg read a pod-leg tally from a COUNT line without pod-leg fields")
	}
}

func TestPodLegCheck(t *testing.T) {
	cases := []struct {
		name string
		leg  PodLeg
		want bool // true when Check passes
	}{
		{"under the bound", PodLeg{OK: 10, Fails: 4}, true},
		{"at the bound", PodLeg{OK: 10, Fails: PodLegBound}, true},
		{"over the bound", PodLeg{OK: 10, Fails: PodLegBound + 1}, false},
		{"never reached a Ready target", PodLeg{Skipped: 300}, false},
	}
	for _, tc := range cases {
		err := tc.leg.Check(PodLegBound)
		if (err == nil) != tc.want {
			t.Errorf("%s: Check(%d) on %+v = %v, want pass=%v", tc.name, PodLegBound, tc.leg, err, tc.want)
		}
	}
}

func TestPodLegAddSumsEveryField(t *testing.T) {
	a := PodLeg{OK: 1, Fails: 2, Skipped: 3, Retargets: 4}
	b := PodLeg{OK: 10, Fails: 20, Skipped: 30, Retargets: 40}
	if got := a.Add(b); got != (PodLeg{OK: 11, Fails: 22, Skipped: 33, Retargets: 44}) {
		t.Fatalf("Add = %+v", got)
	}
}
