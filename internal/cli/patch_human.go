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
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"

	sigsyaml "sigs.k8s.io/yaml"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// Words of the patch result lines and the layer column.
const (
	kindPatch  = "patch"
	verbCreate = "created"
	verbUpdate = "updated"
	verbDelete = "deleted"
	layerAll   = "cluster"
)

// patchTarget is what a patch applies to: "cluster", "role worker", "machine node-1" or
// "selector tier=db".
func patchTarget(p *pb.Patch) string {
	layer := humanEnum(p.GetLayer().String())
	t := p.GetTarget()

	switch {
	case t.GetRole() != "":
		return layer + " " + t.GetRole()
	case t.GetMachineRef() != "":
		return layer + " " + t.GetMachineRef()
	case t.GetLabelSelector() != nil:
		return layer + " " + selectorText(t.GetLabelSelector())
	default:
		return cmp.Or(layer, layerAll)
	}
}

// selectorText is a label selector as "a=b, tier in (db,cache)".
func selectorText(s *pb.LabelSelector) string {
	parts := make([]string, 0, len(s.GetMatchLabels())+len(s.GetMatchExpressions()))

	for _, k := range slices.Sorted(maps.Keys(s.GetMatchLabels())) {
		parts = append(parts, k+"="+s.GetMatchLabels()[k])
	}

	for _, e := range s.GetMatchExpressions() {
		part := e.GetKey() + " " + strings.ToLower(e.GetOperator())
		if len(e.GetValues()) > 0 {
			part += " (" + strings.Join(e.GetValues(), ",") + ")"
		}

		parts = append(parts, part)
	}

	return strings.Join(parts, ", ")
}

// documentKinds are the kinds of a patch's documents, in order.
func documentKinds(p *pb.Patch) string {
	kinds := make([]string, len(p.GetDocuments()))
	for i, d := range p.GetDocuments() {
		kinds[i] = d.GetKind()
	}

	return strings.Join(kinds, ", ")
}

func renderPatchList(h *human, resp *pb.ListPatchesResponse) error {
	ps := resp.GetPatches()
	if len(ps) == 0 {
		h.Note("no patches")

		return h.Err()
	}

	clusters := map[string]bool{}
	for _, p := range ps {
		clusters[p.GetCluster().GetName()] = true
	}

	withCluster := len(clusters) > 1
	header := []string{"name", "target", "priority", "documents", "age"}

	if withCluster {
		header = slices.Insert(header, 1, "cluster")
	}

	rows := make([][]string, len(ps))

	for i, p := range ps {
		row := []string{p.GetRef().GetName(), patchTarget(p), fmt.Sprint(p.GetPriority()), documentKinds(p), humanAge(p.GetCreatedAt())}
		if withCluster {
			row = slices.Insert(row, 1, p.GetCluster().GetName())
		}

		rows[i] = row
	}

	h.Table(header, rows)

	return h.Err()
}

func renderPatchGet(h *human, resp *pb.GetPatchResponse) error {
	p := resp.GetPatch()
	h.Block(nsName(p.GetRef()), kv{"Cluster", p.GetCluster().GetName()}, kv{"Target", patchTarget(p)},
		kv{"Priority", fmt.Sprint(p.GetPriority())}, kv{"Created", agoOrEmpty(p.GetCreatedAt())})

	if len(p.GetDocuments()) == 0 {
		return h.Err()
	}

	h.Title("Documents")

	for _, d := range p.GetDocuments() {
		h.Line(h.Paint(sgrBold, d.GetKind()))

		body, err := sigsyaml.JSONToYAML([]byte(d.GetDocument()))
		if err != nil {
			body = []byte(d.GetDocument())
		}

		for line := range strings.SplitSeq(strings.TrimRight(string(body), "\n"), "\n") {
			h.Line("  " + line)
		}
	}

	return h.Err()
}

// renderPatchImpact is the preview: one line per consequence, ! for the ones that need attention.
func renderPatchImpact(h *human, r *pb.PreviewImpactResponse) error {
	h.Title("Impact")

	clean := true

	if r.GetRebootRequired() {
		clean = false

		h.Line(h.Mark(markWarning) + " applying it needs a reboot")
	}

	if f := r.GetInstallTimeOnly(); len(f) > 0 {
		clean = false

		h.Line(h.Mark(markWarning) + " only takes effect at install time (a reset and reinstall): " + strings.Join(f, ", "))
	}

	if f := r.GetUnsupported(); len(f) > 0 {
		clean = false

		h.Line(h.Mark(markWarning) + " needs a newer Talos than the cluster is pinned to: " + strings.Join(f, ", "))
	}

	if clean {
		h.Line(h.Mark(markOK) + " no reboot needed; nothing install-time-only or too new for the cluster's Talos")
	}

	return h.Err()
}
