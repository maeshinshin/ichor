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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/controller"
	"github.com/maeshinshin/ichor/internal/installer"
	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
	"github.com/maeshinshin/ichor/pkg/render"
)

// ciliumPreflight runs the read-only takeover preconditions (cilium.Preflight).
// nil means "not available": the plan prints "Cilium takeover checks: pending" and the machines
// stay held after the write phase.
type ciliumPreflight func(ctx context.Context, in cilium.PreflightInput) (*cilium.PreflightReport, error)

// adoptDeps bundles adopt's side effects -- the Kubernetes API, each node's
// Talos API, and the terminal -- behind small interfaces/funcs, so
// runAdopt can be driven by a test with no real cluster, node or file on
// either end (unit rule 2's spirit, extended to the CLI).
type adoptDeps struct {
	K8s    client.Client
	Bundle *secrets.Bundle
	// SaveBundle stores a recovered bundle on disk; nil when none was recovered. Called after consent.
	SaveBundle func() error
	DialTalos  func(ctx context.Context, endpoint string) (talosConfigReader, error)

	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader

	AssumeYes bool
	// PlanOnly (--plan) stops after the report: nothing is written anywhere.
	PlanOnly bool
	Output   string

	// Target reads the cluster being adopted (the CNI and kube-proxy facts); nil
	// skips the CNI inspection. CNIUnmanaged is --cni-unmanaged; TalosCNI is the Talos-side CNI
	// selection, filled from the first node's facts when empty.
	Target       client.Reader
	CNIUnmanaged bool
	TalosCNI     talosCNIHint

	// Seed is the address of one control plane (--node), used to discover the machines of a
	// cluster that has no TalosCluster yet.
	Seed string
	// ContractOverride is --version-contract: it replaces the probed contract.
	ContractOverride string
	// ReadIdentity reads a node's hardware identity over the authenticated Talos API.
	ReadIdentity func(ctx context.Context, address string) (controller.HardwareIdentity, error)
	// Ichor, when set, is installed if Ichor is not running yet. nil skips the install.
	Ichor *adoptWriteIchor
	// Preflight runs the takeover preconditions; nil means pending. AcceptCiliumChanges and
	// AcceptCiliumDiff are --accept-cilium-changes and --accept-cilium-diff.
	Preflight                             ciliumPreflight
	AcceptCiliumChanges, AcceptCiliumDiff bool

	// ReleaseWait bounds the wait for one machine to be Ready after its hold is released;
	// Interval is the poll interval of that wait and of the webhook wait. Zero uses the defaults.
	ReleaseWait, Interval time.Duration
}

// adoptState is everything the read-only phase established.
type adoptState struct {
	cluster  *ichorv1alpha1.TalosCluster
	fresh    bool // the TalosCluster does not exist yet and will be created
	nodes    []*adoptNode
	existing []ichorv1alpha1.TalosConfigPatch
	sys      *render.SystemInputs

	rep       *cniReport // nil when the CNI was not inspected
	unmanaged bool
	kubeProxy string
}

// own is the class B definition for one node.
func (st *adoptState) own(n *adoptNode) ownership {
	o := ownership{Managed: !st.unmanaged, KubeProxyReplaced: st.kubeProxy == kubeProxyReplaced}

	if n.Facts != nil {
		name, _ := cilium.FindInlineManifest(inlineEntries(n.Facts))
		o.CiliumInline = name != ""
	}

	return o
}

// needsTakeover: a managed Cilium that Ichor did not install itself.
func (st *adoptState) needsTakeover() bool {
	return !st.unmanaged && st.rep != nil && st.rep.Kind == detectedCilium && st.rep.Provenance != provenanceIchor
}

// runAdopt implements `ichor adopt` (docs/design/architecture.md, "Bootstrap") in the four phases: plan (reads
// only), write, Cilium takeover, release of the hold. Nothing is
// written before the operator consented to the plan, and nothing is applied to a node by adopt.
func runAdopt(ctx context.Context, clusterName, namespace string, deps adoptDeps) error {
	st, err := loadAdoption(ctx, clusterName, namespace, deps)
	if err != nil {
		return err
	}

	// Phase 1: plan.
	if err := st.inspect(ctx, deps); err != nil {
		return err
	}

	if err := st.chooseContract(deps); err != nil {
		return err
	}

	plans, err := st.plan(deps)
	if err != nil {
		return err
	}

	toCreate := reportPlans(deps.Stderr, plans)
	owned := reportOwned(deps.Stderr, plans)

	writable, blocked := splitBlocked(st.nodes, plans)

	held := st.heldNodes(writable)
	if !st.fresh && len(toCreate) == 0 && len(held) == 0 {
		_, _ = fmt.Fprintln(deps.Stderr, "adopt: nothing to adopt, every machine already matches a fresh render")

		return nil
	}

	if st.fresh && len(writable) == 0 {
		return fmt.Errorf("adopt: every machine is blocked by configuration Ichor cannot represent (%s); nothing was written", strings.Join(blocked, ", "))
	}

	patches := make([]ichorv1alpha1.TalosConfigPatch, 0, len(toCreate))
	for _, p := range toCreate {
		patches = append(patches, *p.Patch)
	}

	if len(patches) > 0 {
		if err := writeOutput(deps.Stdout, deps.Output, patches); err != nil {
			return err
		}
	}

	tp, err := st.takeoverPlan(ctx, deps)
	if err != nil {
		return err
	}

	if deps.PlanOnly {
		_, _ = fmt.Fprintln(deps.Stderr, "adopt: --plan: nothing was written")

		return nil
	}

	if err := st.consent(deps, clusterName, len(patches), len(writable), owned, tp.deleteRecord); err != nil {
		return err
	}

	// Phase 2: write.
	if deps.SaveBundle != nil {
		if err := deps.SaveBundle(); err != nil {
			return err
		}
	}

	if _, err := writeAdopt(ctx, deps.K8s, st.writeInput(ctx, deps, namespace, writable, patches, tp)); err != nil {
		return err
	}

	// Phase 3: takeover (never in unmanaged mode, nothing to take over from Ichor's own Cilium).
	if st.needsTakeover() {
		if deps.Preflight == nil {
			_, _ = fmt.Fprintln(deps.Stderr, "adopt: Cilium takeover: pending; the machines stay held, nothing was applied to any node")

			return nil
		}

		// The controller's ichor-cni Job takes the Cilium over; adopt waits for CNIHandover.
		if err := waitHandover(ctx, deps, types.NamespacedName{Namespace: namespace, Name: clusterName}); err != nil {
			return err
		}
	}

	// Phase 4: release the hold, machine by machine.
	if _, err := releaseHold(ctx, deps, clusterName, namespace, held, deps.Bundle, st.sys, st.own); err != nil {
		return err
	}

	if len(blocked) > 0 {
		_, _ = fmt.Fprintf(deps.Stderr, "adopt: blocked machines were left out: %s\n", strings.Join(blocked, ", "))
	}

	_, _ = fmt.Fprintf(deps.Stderr, "adopt: %s\n", caRotationAdvice)

	return nil
}

// loadAdoption reads the TalosCluster and machines (or discovers them), applies the Talos v1.12
// gate before anything else is read, and reads each approved node's configuration and facts.
func loadAdoption(ctx context.Context, clusterName, namespace string, deps adoptDeps) (*adoptState, error) {
	st := &adoptState{}

	var cluster ichorv1alpha1.TalosCluster

	err := deps.K8s.Get(ctx, types.NamespacedName{Namespace: namespace, Name: clusterName}, &cluster)

	var machines []*ichorv1alpha1.TalosMachine

	switch {
	case err == nil:
		st.cluster = &cluster

		var list ichorv1alpha1.TalosMachineList
		if err := deps.K8s.List(ctx, &list, client.InNamespace(namespace)); err != nil {
			return nil, fmt.Errorf("adopt: listing TalosMachines: %w", err)
		}

		for i := range list.Items {
			if list.Items[i].Spec.ClusterRef.Name == clusterName {
				machines = append(machines, &list.Items[i])
			}
		}
	case apierrors.IsNotFound(err) || apimeta.IsNoMatchError(err):
		// Ichor is not installed yet: its kinds are not served, which is as fresh as not found.
		st.fresh = true

		if machines, err = discoverMachines(ctx, deps, clusterName, namespace); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("adopt: getting TalosCluster %q: %w", clusterName, err)
	}

	var approved []*ichorv1alpha1.TalosMachine

	for _, m := range machines {
		if m.Spec.ApprovalState == ichorv1alpha1.MachineApprovalStateApproved {
			approved = append(approved, m)
		}
	}

	// The Talos v1.12 gate runs before anything is read or written.
	if err := gateTalosVersions(ctx, approved, deps.DialTalos); err != nil {
		return nil, err
	}

	for _, m := range machines {
		n := &adoptNode{M: m}

		// A pending or rejected machine has no credentials in the cluster's bundle yet (it is in
		// maintenance mode, where the Talos API accepts no client certificate), so dialing it
		// can only fail with a TLS error. Say so instead.
		if m.Spec.ApprovalState != ichorv1alpha1.MachineApprovalStateApproved {
			n.Skipped = "approval state is " + cmp.Or(m.Spec.ApprovalState, ichorv1alpha1.MachineApprovalStatePending)
			st.nodes = append(st.nodes, n)

			continue
		}

		if err := readNode(ctx, deps, n, st.fresh); err != nil {
			return nil, err
		}

		st.nodes = append(st.nodes, n)
	}

	if st.fresh {
		version, err := firstControlPlaneVersion(ctx, deps, st.nodes)
		if err != nil {
			return nil, err
		}

		if st.cluster, err = clusterFromNodes(clusterName, namespace, version, st.nodes); err != nil {
			return nil, err
		}
	} else if st.existing, err = clusterPatches(ctx, deps.K8s, namespace, clusterName); err != nil {
		return nil, err
	}

	// The controller's fixed namespace, not --namespace: that is where the controller reads
	// authSecretRef Secrets from, so the CLI resolves the same ones.
	if st.sys, err = render.ResolveSystemInputs(ctx, deps.K8s, ichorControllerNamespace, st.cluster); err != nil {
		return nil, fmt.Errorf("adopt: %w", err)
	}

	return st, nil
}

func readNode(ctx context.Context, deps adoptDeps, n *adoptNode, fresh bool) error {
	talos, err := deps.DialTalos(ctx, n.M.DialEndpoint())
	if err != nil {
		return fmt.Errorf("adopt: dialing machine %q at %q: %w", n.M.Name, n.M.DialEndpoint(), err)
	}

	if n.Current, err = talos.CurrentConfig(ctx); err != nil {
		return fmt.Errorf("adopt: reading machine %q's current configuration: %w", n.M.Name, err)
	}

	if n.Facts, err = ReadTalosFacts(n.Current); err != nil {
		return fmt.Errorf("adopt: machine %q: %w", n.M.Name, err)
	}

	if fresh {
		return completeNode(n)
	}

	return nil
}

func firstControlPlaneVersion(ctx context.Context, deps adoptDeps, nodes []*adoptNode) (string, error) {
	for _, n := range nodes {
		if n.Skipped != "" || !n.controlPlane() {
			continue
		}

		talos, err := deps.DialTalos(ctx, n.M.DialEndpoint())
		if err != nil {
			return "", fmt.Errorf("adopt: dialing machine %q: %w", n.M.Name, err)
		}

		vr, ok := talos.(talosVersionReader)
		if !ok {
			return "", errors.New("adopt: the node cannot report its Talos version")
		}

		return vr.TalosVersion(ctx)
	}

	return "", errors.New("adopt: no control plane among the cluster's machines")
}

// firstNode returns the first read node, a control plane when there is one.
func (st *adoptState) firstNode() *adoptNode {
	var first *adoptNode

	for _, n := range st.nodes {
		if n.Skipped != "" {
			continue
		}

		if n.controlPlane() {
			return n
		}

		if first == nil {
			first = n
		}
	}

	return first
}

// hintFromFacts is the Talos-side CNI selection in the form the CNI inspection takes.
func hintFromFacts(f *TalosFacts) talosCNIHint {
	switch f.CNI.Kind {
	case CNINone:
		return talosCNIHint{Name: string(detectedNone)}
	case CNICustom:
		return talosCNIHint{Name: string(CNICustom), URLs: f.CNI.URLs}
	case CNIFlannel:
		return talosCNIHint{Name: string(detectedFlannel)}
	default:
		return talosCNIHint{Name: f.CNI.Name}
	}
}

// inspect runs the read-only CNI and kube-proxy inspection, applies the refusals,
// 1.5 and 1.6, and settles the CNI fields of a TalosCluster adopt is going to create.
func (st *adoptState) inspect(ctx context.Context, deps adoptDeps) error {
	first := st.firstNode()
	if first == nil {
		return errors.New("adopt: no approved machine to adopt")
	}

	st.unmanaged = deps.CNIUnmanaged || st.cluster.Spec.CNI.Preset == cniUnmanaged

	var k8s *k8sFacts

	if deps.Target != nil {
		hint := deps.TalosCNI
		if hint.Name == "" && len(hint.URLs) == 0 {
			hint = hintFromFacts(first.Facts)
		}

		var err error

		if st.rep, k8s, err = inspectCNI(ctx, deps, hint, st.unmanaged); err != nil {
			return err
		}
	}

	switch {
	case !st.fresh:
		st.kubeProxy = cmp.Or(st.cluster.Spec.CNI.KubeProxy, kubeProxyEnabled)
	case k8s != nil:
		kp, err := detectKubeProxy(k8s, first.Facts.KubeProxyEnabled, st.unmanaged)
		if err != nil {
			return fmt.Errorf("adopt: %w", err)
		}

		st.kubeProxy = kp.Mode
	case first.Facts.KubeProxyEnabled:
		st.kubeProxy = kubeProxyEnabled
	default:
		st.kubeProxy = kubeProxyReplaced
	}

	// 1.6: replaced mode needs KubePrism on every node, and turning it on and moving Cilium onto it
	// cannot be ordered safely in one step.
	if !st.unmanaged && st.kubeProxy == kubeProxyReplaced {
		for _, n := range st.nodes {
			if n.Skipped == "" && !n.Facts.KubePrism.Enabled {
				return fmt.Errorf("adopt: kube-proxy is replaced by Cilium but KubePrism is off on machine %q; Ichor's Cilium needs KubePrism "+
					"on port 7445, and turning it on while moving Cilium onto it cannot be ordered safely in one step. "+
					"Enable KubePrism with your usual tool, then run adopt again", n.M.Name)
			}
		}
	}

	if st.unmanaged {
		detected := "not inspected"
		if st.rep != nil {
			detected = string(st.rep.Kind)
		}

		_, _ = fmt.Fprintf(deps.Stderr, "adopt: cni: unmanaged (%s, not managed by Ichor)\n", detected)
	}

	if st.fresh {
		st.cluster.Spec.CNI.KubeProxy = st.kubeProxy
		st.cluster.Spec.CNI.Preset = cniCilium

		if st.unmanaged {
			st.cluster.Spec.CNI.Preset = cniUnmanaged
			st.cluster.Spec.CNI.Unmanaged = &ichorv1alpha1.TalosClusterCNIUnmanaged{TalosCNI: "default"}

			if first.Facts.CNI.Kind == CNINone {
				st.cluster.Spec.CNI.Unmanaged.TalosCNI = string(detectedNone)
			}
		}
	}

	return nil
}

// chooseContract settles spec.versionContract of a cluster adopt creates: the --version-contract
// override, or the probed contract with the fewest unrepresentable paths.
func (st *adoptState) chooseContract(deps adoptDeps) error {
	if !st.fresh {
		return nil
	}

	if deps.ContractOverride != "" {
		st.cluster.Spec.VersionContract = deps.ContractOverride

		return nil
	}

	// ponytail: probed on one control plane, not on every node; a node that differs shows up as
	// class C in its own plan.
	n := st.firstNode()

	chosen, probes, err := probeVersionContract(st.cluster, n.M, deps.Bundle, st.existing, n.Current, st.sys, st.cluster.Spec.TalosVersion)
	if err != nil {
		return err
	}

	parts := make([]string, 0, len(probes))

	for _, p := range probes {
		if p.Err != nil {
			parts = append(parts, p.Contract+" does not render")

			continue
		}

		parts = append(parts, fmt.Sprintf("%s: %d unrepresentable path(s)", p.Contract, len(p.UnrepresentablePaths)))
	}

	_, _ = fmt.Fprintf(deps.Stderr, "adopt: version contract %s chosen (%s); override with --version-contract\n", chosen, strings.Join(parts, "; "))

	st.cluster.Spec.VersionContract = chosen

	return nil
}

// plan builds each machine's three-class plan.
func (st *adoptState) plan(deps adoptDeps) ([]*adoptPlan, error) {
	plans := make([]*adoptPlan, 0, len(st.nodes))

	for _, n := range st.nodes {
		if n.Skipped != "" {
			plans = append(plans, &adoptPlan{Machine: n.M.Name, Skipped: n.Skipped})

			continue
		}

		if st.fresh && n.M.Spec.Install == nil && n.Facts.Install.DiskSelector != "" {
			return nil, fmt.Errorf("adopt: machine %q installs to a disk chosen by a selector; Ichor needs a plain device path in "+
				"machine.install.disk to record spec.install", n.M.Name)
		}

		p, err := buildAdoptPlanOwned(st.cluster, n.M, deps.Bundle, st.existing, n.Current, st.sys, st.own(n))
		if err != nil {
			return nil, err
		}

		plans = append(plans, p)
	}

	return plans, nil
}

// splitBlocked returns the nodes adopt may write and release, and the names of those blocked by
// class C paths.
func splitBlocked(nodes []*adoptNode, plans []*adoptPlan) (writable []*adoptNode, blocked []string) {
	for i, n := range nodes {
		switch {
		case n.Skipped != "":
		case len(plans[i].UnrepresentablePaths) > 0:
			blocked = append(blocked, n.M.Name)
		default:
			writable = append(writable, n)
		}
	}

	return writable, blocked
}

// heldNodes are the nodes whose hold adopt will release: all of a new cluster's, and the ones of
// an existing cluster that carry the annotation.
func (st *adoptState) heldNodes(writable []*adoptNode) []*adoptNode {
	var out []*adoptNode

	for _, n := range writable {
		if st.fresh || n.M.Annotations[ichorv1alpha1.TalosMachineHoldApplyAnnotation] == annotationTrue {
			out = append(out, n)
		}
	}

	return out
}

// reportOwned prints the class B list and returns its length in paths.
func reportOwned(w io.Writer, plans []*adoptPlan) int {
	total := 0

	for _, p := range plans {
		if len(p.Owned) == 0 {
			continue
		}

		total += len(p.Owned)

		_, _ = fmt.Fprintf(w, "adopt: machine %q: Ichor-owned settings its first render will change (need consent): %s\n",
			p.Machine, strings.Join(p.Owned, ", "))
	}

	return total
}

// takeoverPlan is what the takeover section of the plan settled.
type takeoverPlan struct {
	deleteRecord bool           // a foreign Helm release record will be deleted
	userValues   map[string]any // spec.cni.cilium.values of a cluster adopt creates
	inline       string         // name of the Cilium entry in cluster.inlineManifests, "" if none
}

func inlineEntries(f *TalosFacts) map[string]string {
	m := make(map[string]string, len(f.InlineManifests))
	for _, im := range f.InlineManifests {
		m[im.Name] = im.Contents
	}

	return m
}

// takeoverPlan prints the takeover section of the plan: the preflight report (names and paths
// only, never record data) and the foreign Helm record deletion the consent covers. It returns an
// error when the preflight says no-go. Managed mode only.
func (st *adoptState) takeoverPlan(ctx context.Context, deps adoptDeps) (takeoverPlan, error) {
	var tp takeoverPlan

	if st.unmanaged {
		return tp, nil
	}

	for _, n := range st.nodes {
		if n.Skipped != "" {
			continue
		}

		name, err := cilium.FindInlineManifest(inlineEntries(n.Facts))
		if err != nil {
			return tp, fmt.Errorf("adopt: machine %q: %w", n.M.Name, err)
		}

		tp.inline = cmp.Or(tp.inline, name)
	}

	if !st.needsTakeover() {
		return tp, nil
	}

	w := deps.Stderr

	if deps.Preflight == nil {
		_, _ = fmt.Fprintln(w, "adopt: Cilium takeover checks: pending")

		return tp, nil
	}

	rep, err := st.preflight(ctx, deps, nil)
	if err != nil {
		return tp, err
	}

	// The operator's own values come from the foreign release; the diff gate must see them.
	if len(rep.UserValues) > 0 {
		tp.userValues = rep.UserValues

		if rep, err = st.preflight(ctx, deps, rep.UserValues); err != nil {
			return tp, err
		}
	}

	printPreflight(w, rep)

	if rep.Foreign != nil && st.rep.Provenance == provenanceHelm {
		tp.deleteRecord = true

		_, _ = fmt.Fprintf(w, "adopt: the Helm release record(s) of the existing Cilium will be copied to Secret ichor-cilium-prior-release in %s "+
			"and then deleted (the record Secrets only; helm uninstall is never run)\n", ichorControllerNamespace)
	}

	if err := rep.Err(deps.AcceptCiliumChanges, deps.AcceptCiliumDiff); err != nil {
		return tp, fmt.Errorf("adopt: Cilium takeover checks failed, nothing was written: %w", err)
	}

	return tp, nil
}

func (st *adoptState) preflight(ctx context.Context, deps adoptDeps, user map[string]any) (*cilium.PreflightReport, error) {
	mode := cni.KubeProxyMode(st.kubeProxy)

	trial := st.cluster.DeepCopy()

	if len(user) > 0 {
		raw, err := json.Marshal(user)
		if err != nil {
			return nil, fmt.Errorf("adopt: %w", err)
		}

		trial.Spec.CNI.Cilium = &ichorv1alpha1.TalosClusterCNICilium{Values: &apiextensionsv1.JSON{Raw: raw}}
	}

	effective, err := cilium.EffectiveValuesForMode(trial, mode)
	if err != nil {
		return nil, fmt.Errorf("adopt: %w", err)
	}

	rep, err := deps.Preflight(ctx, cilium.PreflightInput{
		IchorNamespace: ichorControllerNamespace, Effective: effective, KubeProxy: mode, Tunnel: st.cluster.Spec.CNI.Tunnel,
	})
	if err != nil {
		return nil, fmt.Errorf("adopt: Cilium takeover checks: %w", err)
	}

	return rep, nil
}

func printPreflight(w io.Writer, rep *cilium.PreflightReport) {
	if rep.Foreign != nil {
		names := make([]string, 0, len(rep.Foreign.Records))
		for _, r := range rep.Foreign.Records {
			names = append(names, r.Secret.Name)
		}

		_, _ = fmt.Fprintf(w, "adopt: Cilium takeover: Helm release records in %s: %s\n", rep.Foreign.Namespace, strings.Join(names, ", "))
	}

	if rep.VersionErr == nil {
		verb := "equal to"
		if rep.Version.Upgrade {
			verb = "one minor below (adoption is an upgrade)"
		}

		_, _ = fmt.Fprintf(w, "adopt: Cilium takeover: live version %s, chart %s: %s\n", rep.Version.Live, rep.Version.Target, verb)
	}

	for _, e := range []error{rep.ForeignErr, rep.VersionErr, rep.CRDsErr} {
		if e != nil {
			_, _ = fmt.Fprintf(w, "adopt: Cilium takeover: %v\n", e)
		}
	}

	for _, c := range rep.Conflicts {
		_, _ = fmt.Fprintf(w, "adopt: Cilium takeover: Ichor-owned key %s\n", c)
	}

	for _, d := range rep.Differences {
		_, _ = fmt.Fprintf(w, "adopt: Cilium takeover: live differs from the chart: %s\n", d)
	}
}

// waitHandover is phase 3 for an adopted cluster: the controller's ichor-cni Job
// takes Cilium over; adopt waits for CNIHandover to be True before it releases any hold.
func waitHandover(ctx context.Context, deps adoptDeps, key types.NamespacedName) error {
	wait := cmp.Or(deps.ReleaseWait, defaultReleaseWait)
	interval := cmp.Or(deps.Interval, defaultReleaseInterval)

	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()

	for {
		var c ichorv1alpha1.TalosCluster

		if err := deps.K8s.Get(ctx, key, &c); err == nil && apimeta.IsStatusConditionTrue(c.Status.Conditions, render.ConditionCNIHandover) {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("adopt: the Cilium takeover (condition %s) did not finish within %s; the machines stay held",
				render.ConditionCNIHandover, wait)
		case <-time.After(interval):
		}
	}
}

// consent is the gate before anything is written: --yes, or an interactive yes. The prompt names
// the class B changes and the record deletion, which the answer accepts.
func (st *adoptState) consent(deps adoptDeps, clusterName string, patches, machines, owned int, helmDeletion bool) error {
	if deps.AssumeYes {
		return nil
	}

	what := fmt.Sprintf("Create %d TalosConfigPatch object(s) to adopt cluster %q with zero drift", patches, clusterName)
	if st.fresh {
		what = fmt.Sprintf("Create the Secret, TalosCluster %q, %d held TalosMachine(s) and %d TalosConfigPatch object(s)", clusterName, machines, patches)
	}

	if owned > 0 {
		what += fmt.Sprintf(", and let Ichor change the %d Ichor-owned setting(s) listed above", owned)
	}

	if helmDeletion {
		what += ", and delete the foreign Cilium Helm record(s)"
	}

	return confirmOrAbort(deps.Stdin, deps.Stderr, "adopt", what+"? [y/N]: ", "aborted, nothing was created")
}

// writeInput assembles the write phase's input from what the plan established.
func (st *adoptState) writeInput(ctx context.Context, deps adoptDeps, namespace string, writable []*adoptNode, patches []ichorv1alpha1.TalosConfigPatch, tp takeoverPlan) adoptWriteInput {
	in := adoptWriteInput{Namespace: namespace, Patches: patches, Stderr: deps.Stderr, Interval: deps.Interval}

	if !st.fresh {
		return in
	}

	c := st.cluster
	in.Bundle = deps.Bundle
	in.ReadIdentity = deps.ReadIdentity
	in.Cluster = &adoptWriteCluster{
		Name: c.Name, Endpoint: c.Spec.Endpoint, TalosVersion: c.Spec.TalosVersion, KubernetesVersion: c.Spec.KubernetesVersion,
		VersionContract: c.Spec.VersionContract, ControlPlaneCount: c.Spec.ControlPlane.Count, Schedulable: c.Spec.ControlPlane.Schedulable,
		CNIPreset: c.Spec.CNI.Preset, KubeProxy: c.Spec.CNI.KubeProxy,
		CNIInlineManifest: tp.inline, CiliumValues: tp.userValues,
	}

	in.Cluster.CertSANs, in.Cluster.Registries = c.Spec.CertSANs, c.Spec.Registries

	if c.Spec.Network != nil {
		in.Cluster.PodSubnets, in.Cluster.ServiceSubnets = c.Spec.Network.PodSubnets, c.Spec.Network.ServiceSubnets
	}

	if c.Spec.CNI.Unmanaged != nil {
		in.Cluster.TalosCNI = c.Spec.CNI.Unmanaged.TalosCNI
	}

	for _, n := range writable {
		m := adoptWriteMachine{Address: n.M.Spec.Endpoint, Role: n.M.Spec.Role, InstallDisk: n.InstallDisk}
		if inst := n.M.Spec.Install; inst != nil {
			m.InstallImage, m.InstallWipe = inst.Image, inst.Wipe
		}

		in.Machines = append(in.Machines, m)
	}

	if deps.Ichor != nil {
		ichor := *deps.Ichor
		detected := ""

		if st.rep != nil {
			detected = string(st.rep.Kind)
		}

		ichor.IncludeCiliumPolicies = installer.IncludeCiliumPolicies(detected, ciliumPolicyCRDPresent(ctx, deps.Target))
		in.Ichor = &ichor
	}

	return in
}

// ciliumPolicyCRDPresent reports whether the CiliumNetworkPolicy CRD exists (the second input of
// the network-policy gate).
func ciliumPolicyCRDPresent(ctx context.Context, r client.Reader) bool {
	if r == nil {
		return false
	}

	crd := &unstructured.Unstructured{}
	crd.SetGroupVersionKind(crdGVK)

	return r.Get(ctx, types.NamespacedName{Name: "ciliumnetworkpolicies.cilium.io"}, crd) == nil
}

// reportPlans writes a one-line-per-machine summary to w and returns the
// plans that have a patch ready to create.
func reportPlans(w io.Writer, plans []*adoptPlan) []*adoptPlan {
	var toCreate []*adoptPlan

	for _, p := range plans {
		switch {
		case p.Skipped != "":
			_, _ = fmt.Fprintf(w, "adopt: machine %q: skipped, %s (only approved machines are read; approve it and re-run adopt to include it)\n",
				p.Machine, p.Skipped)
		case p.Patch != nil:
			_, _ = fmt.Fprintf(w, "adopt: machine %q: drift found, patch ready (zero drift verified)\n", p.Machine)

			toCreate = append(toCreate, p)
		case len(p.UnrepresentablePaths) > 0:
			_, _ = fmt.Fprintf(w, "adopt: machine %q: drift found in %d field(s) that adopt cannot patch automatically: %s\n",
				p.Machine, len(p.UnrepresentablePaths), strings.Join(p.UnrepresentablePaths, ", "))
			for _, line := range explainUnrepresentable(p.UnrepresentablePaths) {
				_, _ = fmt.Fprintf(w, "adopt: machine %q: %s\n", p.Machine, line)
			}
		case len(p.Owned) > 0:
			_, _ = fmt.Fprintf(w, "adopt: machine %q: no drift beyond the Ichor-owned settings listed below\n", p.Machine)
		default:
			_, _ = fmt.Fprintf(w, "adopt: machine %q: already matches a fresh render, nothing to do\n", p.Machine)
		}
	}

	return toCreate
}

// specFieldHints say which spec field carries a value adopt cannot put in a patch.
// The v1.14 documents and the legacy fields they replaced both map, because the same value
// reaches Ichor through the same field whichever contract the node was generated for. First
// match wins, so the specific prefixes come before the general ones.
const cniHint = "the CNI selection has no spec field; Ichor leaves it as the node has it only on a cluster adopted with --cni-unmanaged"

const (
	installHint = "set TalosMachine spec.install (disk, image, wipe) for this machine; a disk selector that is not one wwid, serial, " +
		"by-id, bus-path or device-path match, extra kernel arguments, extensions and the bootloader options have no spec field"
	mirrorHint = "set TalosCluster spec.registries (host, endpoints, skipFallback, overridePath) to the node's value; every node must state the " +
		"same mirrors, and one overridePath value for all of a host's endpoints"
	registryAuthHint = "registry credentials are never copied into Ichor: create the TalosCluster yourself with spec.registries[].authSecretRef " +
		"(a Secret in the controller's namespace holding password, auth or identityToken), then run adopt again"
	certSANsHint = "set TalosCluster spec.certSANs to the node's list; every node must state the same machine.certSANs, and every control plane " +
		"the same kube-apiserver SANs"
)

var specFieldHints = []struct{ prefix, hint string }{
	{"KubeNetworkConfig.podSubnets", "set TalosCluster spec.network.podSubnets to the node's value (it cannot change once set)"},
	{"cluster.network.podSubnets", "set TalosCluster spec.network.podSubnets to the node's value (it cannot change once set)"},
	{"KubeNetworkConfig.serviceSubnets", "set TalosCluster spec.network.serviceSubnets to the node's value (it cannot change once set)"},
	{"cluster.network.serviceSubnets", "set TalosCluster spec.network.serviceSubnets to the node's value (it cannot change once set)"},
	{"KubeNetworkConfig", "the DNS domain has no spec field; Ichor keeps Talos's default"},
	{"UnattendedInstallConfig", installHint},
	{"machine.install", installHint},
	{"RegistryMirrorConfig", mirrorHint},
	{"machine.registries.mirrors", mirrorHint},
	{"RegistryAuthConfig", registryAuthHint},
	{"machine.registries.config", registryAuthHint},
	{"RegistryTLSConfig", "registry TLS settings have no spec field; keep them as the node has them or write a TalosConfigPatch for them"},
	{"machine.certSANs", certSANsHint},
	{"cluster.apiServer.certSANs", certSANsHint},
	{"KubeAPIServerConfig.certExtraSANs", certSANsHint},
	{"KubeFlannelCNIConfig", cniHint},
	{"cluster.network.cni", cniHint},
}

// explainUnrepresentable turns the paths adopt left alone into the lines a user can act on: the
// spec field that carries each value Ichor models, and, for what remains, that a patch
// has to be written by hand.
func explainUnrepresentable(paths []string) []string {
	var (
		hints  []string
		byHint = map[string][]string{}
		manual bool
	)

	for _, p := range paths {
		hint := ""

		for _, h := range specFieldHints {
			if p == h.prefix || strings.HasPrefix(p, h.prefix+".") || strings.HasPrefix(p, h.prefix+"[") {
				hint = h.hint

				break
			}
		}

		if hint == "" {
			manual = true

			continue
		}

		if _, seen := byHint[hint]; !seen {
			hints = append(hints, hint)
		}

		byHint[hint] = append(byHint[hint], p)
	}

	lines := make([]string, 0, len(hints)+1)
	for _, hint := range hints {
		lines = append(lines, fmt.Sprintf("%s: %s", strings.Join(byHint[hint], ", "), hint))
	}

	if manual {
		lines = append(lines, "write a TalosConfigPatch for the other fields by hand, then re-run adopt")
	}

	return lines
}

// confirm reads a yes/no answer from r, defaulting to no on anything but a
// leading 'y' or 'Y' (architecture.md, "CLI and UI": interactive
// confirmation with --yes to skip it).
func confirm(r io.Reader, w io.Writer, prompt string) (bool, error) {
	line, err := readLine(r, w, prompt)

	return err == nil && (line == "y" || line == "Y" || line == "yes" || line == "Yes"), err
}

// inspectCNI runs the read-only CNI inspection, prints the report, and returns the refusal of
// When the cluster may not be adopted in the requested mode. Nothing is written.
func inspectCNI(ctx context.Context, deps adoptDeps, hint talosCNIHint, unmanaged bool) (*cniReport, *k8sFacts, error) {
	facts, err := gatherK8sFacts(ctx, deps.Target)
	if err != nil {
		return nil, nil, fmt.Errorf("adopt: inspecting the CNI: %w", err)
	}

	rep := detectCNI(facts, hint)

	_, _ = fmt.Fprintf(deps.Stderr, "adopt: CNI: %s\n", rep)

	if err := checkCNIAdoption(rep, unmanaged); err != nil {
		return nil, nil, fmt.Errorf("adopt: %w", err)
	}

	return rep, facts, nil
}
