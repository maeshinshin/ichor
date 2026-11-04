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

package cli

import (
	"fmt"
	"io"
	"strings"
)

// Human output of ichor adopt, the way ichor init shows its steps. With -o unset,
// adoptDeps.Steps renders a header, one line per step, the findings of the plan as notices (a
// warning is a ! line, nothing to act on a dim line) and a Done/Next ending. With -o yaml|json
// Steps is nil and adopt prints the "adopt: ..." lines on Stderr it always printed.

// adoptLevel is how much a finding of adopt matters.
type adoptLevel int

const (
	// adoptInfo is something the reader may want to know.
	adoptInfo adoptLevel = iota
	// adoptWarn is something the reader has to look at or act on.
	adoptWarn
)

// adoptSay reports one finding. The message has no "adopt: " prefix: the plain mode adds it.
type adoptSay func(lvl adoptLevel, format string, args ...any)

// say reports a finding: a notice of the steps, or the "adopt: " line it has always been.
func (d adoptDeps) say(lvl adoptLevel, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)

	if d.Steps == nil {
		_, _ = fmt.Fprintf(d.Stderr, "adopt: %s\n", msg)

		return
	}

	msg = strings.TrimPrefix(msg, "warning: ")

	if lvl == adoptWarn {
		d.Steps.Warn(msg)

		return
	}

	d.Steps.Info(msg)
}

// findings is a writer for a helper that reports its findings as lines ("adopt: ..."): Stderr
// itself, or in the human mode a notice per line.
func (d adoptDeps) findings(lvl adoptLevel) io.Writer {
	if d.Steps == nil {
		return d.Stderr
	}

	return lineWriter(func(line string) { d.say(lvl, "%s", strings.TrimPrefix(line, "adopt: ")) })
}

// run runs fn as the step name. fn gets deps whose Stderr is the step, so what the wait helpers
// write becomes its detail; an empty detail returned is the last one written. Without Steps it is
// just fn.
func (d adoptDeps) run(name string, fn func(adoptDeps) (string, error)) error {
	if d.Steps == nil {
		_, err := fn(d)

		return err
	}

	return d.Steps.Run(name, func(s *step) (string, error) {
		in := d
		in.Stderr = s.Rewrite(adoptDetail)

		detail, err := fn(in)
		if detail == "" {
			detail = s.Detail()
		}

		return detail, err
	})
}

// adoptDetail turns a message of the wait helpers into the short detail of a step: init's, and the
// bare "not finished" of the hand-over poll.
func adoptDetail(msg string) string {
	if msg == "not finished" {
		return "waiting for the controller"
	}

	return initDetail(msg)
}

// adoptEnd is how adopt ended: complete when it got to the end of the release phase.
type adoptEnd struct {
	complete bool
	blocked  []string
}

// epilogue ends a run that did not fail: how long it took, what was left out, the advice to rotate
// the CAs, and the commands to run next.
func (d adoptDeps) epilogue(cluster, namespace string, e adoptEnd) {
	if d.Steps != nil {
		d.Steps.Done()
	}

	if !e.complete {
		return
	}

	if len(e.blocked) > 0 {
		d.say(adoptWarn, "blocked machines were left out: %s", strings.Join(e.blocked, ", "))
	}

	// A machine added to a cluster Ichor already manages brings no new administrator with it.
	if len(d.AddMachines) == 0 {
		d.say(adoptWarn, "%s", caRotationAdvice)
	}

	if d.Steps == nil {
		return
	}

	d.Steps.Next("ichor cluster status "+cluster+nsFlag(namespace), "ichor machine list"+nsFlag(namespace))
}

// adoptSummary is the second line of adopt's header.
func adoptSummary(d adoptDeps, namespace string) string {
	parts := []string{"namespace " + namespace}

	if d.Seed != "" {
		parts = append(parts, "discovering from "+d.Seed)
	}

	if d.CNIUnmanaged {
		parts = append(parts, "CNI left as it is")
	}

	if d.PlanOnly {
		parts = append(parts, "plan only, nothing is written")
	}

	return strings.Join(parts, " · ")
}

// readSummary is the detail of the step that read the cluster.
func (st *adoptState) readSummary() string {
	read, skipped := 0, 0

	for _, n := range st.nodes {
		if n.Skipped != "" {
			skipped++
		} else {
			read++
		}
	}

	parts := []string{"Talos " + st.cluster.Spec.TalosVersion, "Kubernetes " + st.cluster.Spec.KubernetesVersion, fmt.Sprintf("%d read", read)}
	if skipped > 0 {
		parts = append(parts, fmt.Sprintf("%d skipped", skipped))
	}

	if st.fresh {
		parts = append(parts, "no TalosCluster yet")
	}

	return strings.Join(parts, " · ")
}

// summary is the detail of the plan step.
func (pl *adoptPlanned) summary() string {
	if pl.nothing {
		return "nothing to adopt"
	}

	s := "no patch needed"
	if len(pl.patches) > 0 {
		s = fmt.Sprintf("%d patch(es) ready", len(pl.patches))
	}

	if len(pl.blocked) > 0 {
		s += fmt.Sprintf(" · %d blocked", len(pl.blocked))
	}

	return s
}

// bundleNagWriter turns nagBackup's message about a recovered secrets bundle into one warning: the
// path is its only line that starts with four spaces.
type bundleNagWriter struct{ p *progress }

func (w bundleNagWriter) Write(b []byte) (int, error) {
	for l := range strings.SplitSeq(string(b), "\n") {
		if path, ok := strings.CutPrefix(l, "    "); ok && path != "" {
			w.p.Warn("the cluster's secrets bundle was written to " + tildePath(path) +
				". It is the cluster's only root of trust: back it up now, somewhere other than this machine")
		}
	}

	return len(b), nil
}
