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

// This file implements the TalosOperationKindKubernetes AdvanceMachineFunc: the per-node step of
// docs/design/architecture.md's "Upgrades / Kubernetes". The per-node switch
// is not hand-written component by component: Ichor already renders a
// machine's configuration from spec.kubernetesVersion, so a node's step is
// "apply the freshly rendered configuration, whose only differences are the
// component images", then watch the node converge. The operation visits each
// machine in up to four stages, and the configuration rendered
// for a stage pins everything after that stage's component (controller-manager and scheduler,
// kubelet, kube-proxy) to the node's current version until its stage. TalosOperationReconciler supplies everything
// else.
package controller

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
	"github.com/maeshinshin/ichor/pkg/fieldmeta"
	"github.com/maeshinshin/ichor/pkg/render"
	"github.com/maeshinshin/ichor/pkg/storage"
	"github.com/maeshinshin/ichor/pkg/upgrade"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
)

const (
	// conditionKubernetesUpgrade is the TalosMachine marker kubernetesUpgradeAdvancer uses for
	// the verification clock: True from the moment the rendered configuration was applied (or
	// first found applied) until the node is verified on the target. Like conditionTalosUpgrade
	// it is only a resume marker and a timeout clock; every decision is re-derived from the
	// node's configuration and from what the Kubernetes API reports.
	conditionKubernetesUpgrade = "KubernetesUpgrade"

	// conditionRemovedAPIsInUse is set on the TalosOperation (not the machine: it is a
	// cluster-wide finding) when the API server reports requests to API versions the target
	// minor removes. It is a warning, never a gate (architecture.md: "Warn about removed APIs").
	conditionRemovedAPIsInUse = "RemovedAPIsInUse"

	reasonKubernetesApplied   = "ConfigApplied"
	reasonKubernetesConverged = "Converged"
	reasonRemovedAPIsInUse    = "RemovedAPIsInUse"
	reasonRemovedAPIsNone     = "NoneRequested"
	reasonRemovedAPIsUnknown  = "CheckFailed"

	// kubernetesVerifyTimeout bounds how long a node may take to converge after its
	// configuration was applied (static pods replaced, kubelet restarted, node Ready). Past it the
	// step fails and the operation pauses, instead of waiting forever.
	kubernetesVerifyTimeout = 20 * time.Minute

	// imagePullTimeout bounds one image pre-pull.
	imagePullTimeout = 10 * time.Minute

	kubeSystemNamespace = "kube-system"
	kubeProxyName       = "kube-proxy"
	labelK8sApp         = "k8s-app"
)

// +kubebuilder:rbac:groups="",resources=nodes;pods,verbs=get;list
// +kubebuilder:rbac:groups=apps,resources=daemonsets,verbs=get

// kubernetesImagePaths are the ChangedPaths entries a Kubernetes version change is expected to
// produce: one image per component, in the multi-document form (Talos 1.14 contract) and in the
// legacy v1alpha1 form. pkg/render derives all of them from spec.kubernetesVersion.
var kubernetesImagePaths = map[string]struct{}{
	"KubeAPIServerConfig.image":         {},
	"KubeControllerManagerConfig.image": {},
	"KubeSchedulerConfig.image":         {},
	"KubeProxyConfig.image":             {},
	"KubeletConfig.image":               {},
	"cluster.apiServer.image":           {},
	"cluster.controllerManager.image":   {},
	"cluster.scheduler.image":           {},
	"cluster.proxy.image":               {},
	"machine.kubelet.image":             {},
}

// staticPodComponents are the control-plane components Talos runs as static pods, kube-apiserver
// first (the APIServers stage verifies only that one).
var staticPodComponents = []string{"kube-apiserver", "kube-controller-manager", "kube-scheduler"}

// KubernetesAdmin is the two cluster-wide actions a Kubernetes upgrade needs that go through an
// admin kubeconfig issued by the Talos API (kubernetes_admin.go), so the controller itself needs
// no broad Kubernetes RBAC to write bootstrap manifests. A seam so tests never reach a cluster.
type KubernetesAdmin interface {
	// SyncBootstrapManifests applies talos's bootstrap manifest set to the cluster. Idempotent.
	SyncBootstrapManifests(ctx context.Context, talos TalosClient) error

	// RemovedAPIsInUse lists API versions that targetMinor ("X.Y") or an earlier release removes
	// and that something has requested from the API server.
	RemovedAPIsInUse(ctx context.Context, talos TalosClient, targetMinor string) ([]string, error)
}

// kubernetesUpgradeAdvancer is the Kubernetes-kind AdvanceMachineFunc. cmd/main.go selects it for
// TalosOperationKindKubernetes only.
type kubernetesUpgradeAdvancer struct {
	client.Client

	// Reader is an uncached reader for Nodes, Pods and the kube-proxy DaemonSet: verifying an
	// upgrade must see the live state, and the manager's cache would otherwise grow a cluster-wide
	// Pod informer for one comparison. Wired from mgr.GetAPIReader() in cmd/main.go.
	Reader client.Reader

	// ControllerNamespace is loadBundle's namespace (same meaning as in talosUpgradeAdvancer).
	ControllerNamespace string

	TalosClient func(
		ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, bundle *secrets.Bundle,
	) (TalosClient, error)

	Admin KubernetesAdmin

	// Evacuator and Kube make a kubelet roll leave its node first: the storage hook,
	// then cordon and drain, then the roll; afterwards uncordon and end the evacuation. nil
	// skips the respective step.
	Evacuator StorageEvacuator
	Kube      kubernetes.Interface

	// NodeName is the Kubernetes node this controller runs on ("" outside the cluster). It is
	// drained like any other, last (kubernetesBatch); the eviction restarts the controller, and
	// the step resumes from the node's state.
	NodeName string
}

// NewKubernetesUpgradeAdvancer returns the Kubernetes-kind AdvanceMachineFunc.
func NewKubernetesUpgradeAdvancer(
	c client.Client, reader client.Reader, controllerNamespace string,
	talosClient func(context.Context, *ichorv1alpha1.TalosCluster, *ichorv1alpha1.TalosMachine, *secrets.Bundle) (TalosClient, error),
	admin KubernetesAdmin, evacuator StorageEvacuator, kube kubernetes.Interface, nodeName string,
) AdvanceMachineFunc {
	a := &kubernetesUpgradeAdvancer{
		Client: c, Reader: reader, ControllerNamespace: controllerNamespace, TalosClient: talosClient, Admin: admin,
		Evacuator: evacuator, Kube: kube, NodeName: nodeName,
	}

	return a.Advance
}

// kubernetesStageOf is the stage of a Kubernetes upgrade the executor acts in. An
// operation with no recorded stage starts at the first one.
func kubernetesStageOf(op *ichorv1alpha1.TalosOperation) string {
	if op == nil || op.Status.Kubernetes == nil || op.Status.Kubernetes.Stage == "" {
		return ichorv1alpha1.KubernetesStageAPIServers
	}

	return op.Status.Kubernetes.Stage
}

// stagedFor is what the render pins for m in stage. The apiserver always follows the spec. Until
// the ControlPlaneComponents stage the controller-manager and scheduler stay on the version the
// node runs (they reach the apiservers through an endpoint that balances across all of them, and
// a new scheduler needs APIs only a new apiserver serves), until the Kubelets stage so does the
// kubelet (never newer than any API server), and kube-proxy stays on it until the KubeProxy
// stage. None of these versions is stored: until the Kubelets stage the node's kubelet has not
// been touched, so it is the same on every call, and a node's controller-manager and scheduler
// run its kubelet's version.
func stagedFor(stage string, node *corev1.Node) *render.StagedKubernetes {
	v := node.Status.NodeInfo.KubeletVersion

	switch stage {
	case ichorv1alpha1.KubernetesStageAPIServers:
		return &render.StagedKubernetes{ControllerManagerVersion: v, SchedulerVersion: v, KubeletVersion: v, KubeProxyVersion: v}
	case ichorv1alpha1.KubernetesStageControlPlaneComponents:
		return &render.StagedKubernetes{KubeletVersion: v, KubeProxyVersion: v}
	case ichorv1alpha1.KubernetesStageKubeProxy:
		return &render.StagedKubernetes{KubeletVersion: v}
	default:
		return nil
	}
}

// Advance performs one machine's step of a Kubernetes upgrade in the operation's stage. The decision is re-derived
// on every call from live state: if the node's configuration
// differs from the configuration rendered for this stage the step has not been applied yet
// (pre-checks, pre-pull, apply); if they are equal it is applied and the call only verifies, so a
// restarted controller resumes exactly where the node says it was.
func (a *kubernetesUpgradeAdvancer) Advance(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, op *ichorv1alpha1.TalosOperation,
) (bool, error) {
	stage := kubernetesStageOf(op)

	bundle, err := loadBundle(ctx, a.Client, a.ControllerNamespace, cluster)
	if err != nil {
		return false, fmt.Errorf("loading secrets bundle to upgrade %q: %w", m.Name, err)
	}

	var patches ichorv1alpha1.TalosConfigPatchList
	if err := a.List(ctx, &patches, client.InNamespace(m.Namespace)); err != nil {
		return false, err
	}

	dialCtx, cancel := context.WithTimeout(ctx, etcdDialTimeout)
	defer cancel()

	talos, err := a.TalosClient(dialCtx, cluster, m, bundle)
	if err != nil {
		if apimeta.IsStatusConditionTrue(m.Status.Conditions, conditionKubernetesUpgrade) {
			return false, a.waiting(ctx, m, cluster, "dialing the node: "+err.Error()) // restarting services
		}

		return false, fmt.Errorf("dialing %q: %w", m.Name, err)
	}
	defer closeTalosClient(talos)

	// The node is read first: what its kubelet runs is what the staged render pins.
	nodeName, err := resolveNodeName(ctx, a.Reader, talos, m)
	if err != nil {
		if apimeta.IsStatusConditionTrue(m.Status.Conditions, conditionKubernetesUpgrade) {
			return false, a.waiting(ctx, m, cluster, err.Error())
		}

		return false, err
	}

	var node corev1.Node
	if err := a.Reader.Get(ctx, types.NamespacedName{Name: nodeName}, &node); err != nil {
		if apimeta.IsStatusConditionTrue(m.Status.Conditions, conditionKubernetesUpgrade) {
			return false, a.waiting(ctx, m, cluster, fmt.Sprintf("reading node %q: %v", nodeName, err)) // API server restarting
		}

		return false, fmt.Errorf("reading %q's Kubernetes node: %w", m.Name, err)
	}

	sys, err := render.ResolveSystemInputs(ctx, a.Client, a.ControllerNamespace, cluster)
	if err != nil {
		return false, fmt.Errorf("rendering %q's configuration: %w", m.Name, err)
	}

	sys.Staged = stagedFor(stage, &node)

	rendered, hash, err := render.Render(cluster, m, bundle, patches.Items, sys)
	if err != nil {
		return false, fmt.Errorf("rendering %q's configuration: %w", m.Name, err)
	}

	current, err := talos.CurrentConfig(dialCtx)
	if err != nil {
		if apimeta.IsStatusConditionTrue(m.Status.Conditions, conditionKubernetesUpgrade) {
			return false, a.waiting(ctx, m, cluster, "reading the node's configuration: "+err.Error())
		}

		return false, fmt.Errorf("reading %q's configuration: %w", m.Name, err)
	}

	if render.Hash(current) != hash {
		return false, a.releaseOnError(ctx, cluster, m, nodeName, stage,
			a.begin(ctx, talos, cluster, m, bundle, op, &node, stage, current, rendered))
	}

	done, err := a.verify(ctx, talos, cluster, m, nodeName, stage)

	return done, a.releaseOnError(ctx, cluster, m, nodeName, stage, err)
}

// drainFirst is the Kubelets stage's leave-the-node step: the storage evacuation hook, then
// cordon and drain, both resumable from what the node and the machine's StorageEvacuation
// condition say. It reports whether the kubelet may be rolled now; false with no error means
// "still evacuating or draining, call again".
func (a *kubernetesUpgradeAdvancer) drainFirst(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, node string,
) (bool, error) {
	progress, err := evacuateMachine(ctx, a.Client, a.Evacuator, cluster, m, node, "rolling its kubelet")
	if err != nil {
		return false, err
	}

	switch progress {
	case storage.EvacuationWaiting:
		return false, nil
	case storage.EvacuationPaused:
		return false, fmt.Errorf("evacuating %q timed out with storage replicas still remaining", node)
	}

	if a.Kube == nil {
		return true, nil
	}

	return drainForReboot(ctx, a.Kube, node)
}

// release undoes drainFirst: uncordon, then end the evacuation. Both are no-ops for a node that
// was never drained or evacuated.
func (a *kubernetesUpgradeAdvancer) release(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, node string,
) error {
	if a.Kube != nil {
		if err := uncordonAfterReboot(ctx, a.Kube, node); err != nil {
			return err
		}
	}

	return endMachineEvacuation(ctx, a.Client, a.Evacuator, cluster, m, node)
}

// releaseOnError returns err unchanged, after giving the node back when a Kubelets-stage step
// failed: the operation pauses, and a paused operation must not leave its node unschedulable.
func (a *kubernetesUpgradeAdvancer) releaseOnError(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, node, stage string, err error,
) error {
	if err == nil || stage != ichorv1alpha1.KubernetesStageKubelets {
		return err
	}

	if relErr := a.release(ctx, cluster, m, node); relErr != nil {
		return fmt.Errorf("%w (and re-enabling scheduling failed: %v)", err, relErr)
	}

	return err
}

// begin runs the pre-checks for one node, pre-pulls the new images, and applies the rendered
// configuration without a reboot. It reports done=false on success: verification follows.
//
// Every Talos call gets its own deadline derived from ctx: a context shared with the dial would
// leave the apply whatever is left of etcdDialTimeout (15s) and fail it with DeadlineExceeded while
// Talos is still restarting the static pods.
func (a *kubernetesUpgradeAdvancer) begin(
	ctx context.Context, talos TalosClient, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine,
	bundle *secrets.Bundle, op *ichorv1alpha1.TalosOperation, node *corev1.Node, stage string,
	current, rendered []byte,
) error {
	target := cluster.Spec.KubernetesVersion

	from := node.Status.NodeInfo.KubeletVersion
	if from == "" {
		return fmt.Errorf("node %q has not reported a kubelet version", node.Name)
	}

	// One minor at a time, judged against what this node actually runs, so a spec edited past the
	// next minor (or a node left behind by an earlier, partial upgrade) is refused before
	// anything is changed.
	if err := upgrade.KubernetesSkew(from, target); err != nil {
		return fmt.Errorf("kubernetes %s -> %s on node %q is not allowed: %w", from, target, node.Name, err)
	}

	versionCtx, cancelVersion := context.WithTimeout(ctx, etcdDialTimeout)
	talosVersion, err := talos.Version(versionCtx)

	cancelVersion()

	if err != nil {
		return fmt.Errorf("reading %q's Talos version: %w", m.Name, err)
	}

	if err := upgrade.KubernetesPairing(target, talosVersion); err != nil {
		return fmt.Errorf("kubernetes %s is not supported with talos %s: %w", target, talosVersion, err)
	}

	if !nodeIsReady(node) {
		return fmt.Errorf("node %q is not Ready; refusing to start a Kubernetes upgrade", node.Name)
	}

	if msg := cniHandoverPending(cluster); msg != "" {
		return fmt.Errorf("the CNI hand-over is not finished (%s); the bootstrap-manifest sync would re-apply the "+
			"stale inline CNI manifest and roll the CNI back. Wait for the Cilium Helm Job to adopt Cilium "+
			"(see the CNIHandover condition) and create the operation again", msg)
	}

	scrapeCtx, cancelScrape := context.WithTimeout(ctx, etcdDialTimeout)
	a.warnRemovedAPIs(scrapeCtx, cluster, bundle, op, target)
	cancelScrape()

	paths, err := ChangedPaths(current, rendered)
	if err != nil {
		return fmt.Errorf("comparing %q's configuration: %w", m.Name, err)
	}

	contract, err := coreconfig.ParseContractFromVersion(cluster.Spec.VersionContract)
	if err != nil {
		return fmt.Errorf("invalid versionContract %q: %w", cluster.Spec.VersionContract, err)
	}

	// The apply below skips pkg/fieldmeta's reboot table on purpose: every image field is
	// applied without a reboot by talosctl upgrade-k8s itself (pkg/cluster/kubernetes/patch.go,
	// NO_REBOOT), but the table cannot know that for the multi-document kinds. In exchange, any
	// change that is not one of those image fields must be reboot-free by the table, or the step
	// fails: a pending unrelated change is never swept into a Kubernetes upgrade.
	var images []string

	for _, p := range paths {
		if _, ok := kubernetesImagePaths[p]; ok {
			images = append(images, p)

			continue
		}

		impact := fieldmeta.Assess([]string{p}, contract)
		if impact.RebootRequired || len(impact.InstallTimeOnly) > 0 || len(impact.Unsupported) > 0 {
			return fmt.Errorf("%q also has an unrelated pending configuration change at %q that a Kubernetes "+
				"upgrade will not apply; converge it first", m.Name, p)
		}
	}

	dryRunCtx, cancelDryRun := context.WithTimeout(ctx, etcdDialTimeout)
	_, err = talos.DryRunApply(dryRunCtx, rendered)

	cancelDryRun()

	if err != nil {
		return fmt.Errorf("dry-run apply to %q failed: %w", m.Name, err)
	}

	if err := a.prePull(ctx, talos, rendered, images); err != nil {
		return fmt.Errorf("pre-pulling images on %q: %w", m.Name, err)
	}

	// A kubelet roll takes the node's pods down with it: move storage and workloads off first.
	if stage == ichorv1alpha1.KubernetesStageKubelets {
		if ready, err := a.drainFirst(ctx, cluster, m, node.Name); err != nil || !ready {
			return err
		}
	}

	applyCtx, cancelApply := context.WithTimeout(ctx, upgradeCallTimeout)
	defer cancelApply()

	if err := talos.ApplyNoReboot(applyCtx, rendered); err != nil {
		return fmt.Errorf("applying the rendered configuration to %q: %w", m.Name, err)
	}

	if err := setMachineCondition(ctx, a.Reader, a.Client, m, metav1.Condition{
		Type: conditionKubernetesUpgrade, Status: metav1.ConditionTrue, Reason: reasonKubernetesApplied,
		Message: "applied the " + stage + " configuration for Kubernetes " + target,
	}); err != nil {
		return fmt.Errorf("recording the applied configuration for %q: %w", m.Name, err)
	}

	return nil
}

// warnRemovedAPIs records, on op, which APIs the target minor removes that clients still use.
// A failed check is recorded too (Unknown), never swallowed silently and never a gate.
func (a *kubernetesUpgradeAdvancer) warnRemovedAPIs(
	dialCtx context.Context, cluster *ichorv1alpha1.TalosCluster, bundle *secrets.Bundle,
	op *ichorv1alpha1.TalosOperation, target string,
) {
	if op == nil || a.Admin == nil {
		return
	}

	cond := metav1.Condition{Type: conditionRemovedAPIsInUse, Status: metav1.ConditionFalse, Reason: reasonRemovedAPIsNone}

	minor, err := upgrade.KubernetesMinorOf(target)
	if err == nil {
		var removed []string

		removed, err = a.removedAPIsInUse(dialCtx, cluster, bundle, minor)
		if err == nil && len(removed) > 0 {
			cond.Status = metav1.ConditionTrue
			cond.Reason = reasonRemovedAPIsInUse
			cond.Message = "requested but removed by Kubernetes " + minor + ": " + strings.Join(removed, ", ")
		}
	}

	if err != nil {
		cond.Status = metav1.ConditionUnknown
		cond.Reason = reasonRemovedAPIsUnknown
		cond.Message = err.Error()
	}

	apimeta.SetStatusCondition(&op.Status.Conditions, cond)
}

// removedAPIsInUse asks every control plane's API server for the removed APIs it has seen and
// returns their union, sorted. The request metric counts only what the serving apiserver handled,
// and only a control plane's Talos API issues the admin kubeconfig (a worker refuses it), so the
// check never runs against the node being upgraded. A control plane that cannot answer fails the
// whole check, so the warning is never a silent "none".
func (a *kubernetesUpgradeAdvancer) removedAPIsInUse(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, bundle *secrets.Bundle, minor string,
) ([]string, error) {
	planes, err := deleteguard.ApprovedMachines(ctx, a.Reader, cluster.Namespace, cluster.Name,
		deleteguard.MachineFilter{Role: roleControlPlane})
	if err != nil {
		return nil, err
	}

	if len(planes) == 0 {
		return nil, fmt.Errorf("cluster %q has no control plane to ask for removed APIs", cluster.Name)
	}

	var out []string

	for i := range planes {
		talos, err := a.TalosClient(ctx, cluster, &planes[i], bundle)
		if err != nil {
			return nil, fmt.Errorf("dialing control plane %q: %w", planes[i].Name, err)
		}

		removed, err := a.Admin.RemovedAPIsInUse(ctx, talos, minor)

		closeTalosClient(talos)

		if err != nil {
			return nil, fmt.Errorf("control plane %q: %w", planes[i].Name, err)
		}

		out = append(out, removed...)
	}

	slices.Sort(out)

	return slices.Compact(out), nil
}

// prePull pulls the new component images (changed image fields only) onto the node before its
// configuration is applied, so a registry problem fails the step with the old components still
// running instead of leaving a control plane without an API server.
func (a *kubernetesUpgradeAdvancer) prePull(ctx context.Context, talos TalosClient, rendered []byte, imagePaths []string) error {
	docs, err := decodeDocs(rendered)
	if err != nil {
		return err
	}

	for _, p := range imagePaths {
		// kube-proxy is a DaemonSet the manifest sync rolls out; Talos does not pre-pull it either.
		if p == "KubeProxyConfig.image" || p == "cluster.proxy.image" {
			continue
		}

		image, ok := valueAtPath(docs, p).(string)
		if !ok || image == "" {
			continue
		}

		pullCtx, cancel := context.WithTimeout(ctx, imagePullTimeout)
		err := talos.PullImage(pullCtx, image, p == "KubeletConfig.image" || p == "machine.kubelet.image")

		cancel()

		if err != nil {
			return fmt.Errorf("%s: %w", image, err)
		}
	}

	return nil
}

// valueAtPath resolves a ChangedPaths-style path in decoded docs.
func valueAtPath(docs map[string]configDoc, path string) any {
	parts := strings.Split(path, ".")

	doc, ok := docs[parts[0]+":"]
	if ok {
		parts = parts[1:]
	} else if doc, ok = docs[""]; !ok {
		return nil
	}

	var v any = doc.data

	for _, p := range parts {
		mm, ok := v.(map[string]any)
		if !ok {
			return nil
		}

		v = mm[p]
	}

	return v
}

// verify checks that the node converged in stage: its Ready condition, for a control plane its
// three static pods on the target, the kubelet on the target once the Kubelets stage applies it,
// and in the KubeProxy stage the bootstrap manifests and kube-proxy's rollout. Anything not yet
// true is "still settling" until kubernetesVerifyTimeout.
func (a *kubernetesUpgradeAdvancer) verify(
	ctx context.Context, talos TalosClient, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine,
	nodeName, stage string,
) (bool, error) {
	target := cluster.Spec.KubernetesVersion

	if reason := a.nodeNotConverged(ctx, m, nodeName, target, stage); reason != "" {
		return false, a.waiting(ctx, m, cluster, reason)
	}

	// The kubelet is back on the target and Ready: give the node back.
	if stage == ichorv1alpha1.KubernetesStageKubelets {
		if err := a.release(ctx, cluster, m, nodeName); err != nil {
			return false, a.waiting(ctx, m, cluster, "re-enabling scheduling: "+err.Error())
		}
	}

	if m.Spec.Role == roleControlPlane && stage == ichorv1alpha1.KubernetesStageKubeProxy {
		// Every control plane runs the target apiserver by now, and this one has just been given
		// the target kube-proxy image, so the manifests it generates are the ones to apply. Talos
		// does this after the kubelets; Ichor does it before them: kube-proxy may be newer than a
		// kubelet but not than the API server. Server-side apply is idempotent, so each control
		// plane may repeat it.
		if err := a.Admin.SyncBootstrapManifests(ctx, talos); err != nil {
			return false, fmt.Errorf("syncing the bootstrap manifests: %w", err)
		}

		if mode, _ := render.KubeProxyMode(cluster); mode != cni.KubeProxyReplaced {
			if reason := a.kubeProxyNotReady(ctx, target); reason != "" {
				return false, a.waiting(ctx, m, cluster, reason)
			}
		}
	}

	if cond := apimeta.FindStatusCondition(m.Status.Conditions, conditionKubernetesUpgrade); cond != nil && cond.Status == metav1.ConditionTrue {
		if err := setMachineCondition(ctx, a.Reader, a.Client, m, metav1.Condition{
			Type: conditionKubernetesUpgrade, Status: metav1.ConditionFalse, Reason: reasonKubernetesConverged,
			Message: "finished the " + stage + " stage for Kubernetes " + target,
		}); err != nil {
			return false, err
		}
	}

	return true, nil
}

// nodeNotConverged returns why m's node is not on target yet, or "".
func (a *kubernetesUpgradeAdvancer) nodeNotConverged(
	ctx context.Context, m *ichorv1alpha1.TalosMachine, nodeName, target, stage string,
) string {
	var node corev1.Node
	if err := a.Reader.Get(ctx, types.NamespacedName{Name: nodeName}, &node); err != nil {
		return fmt.Sprintf("reading node %q: %v", nodeName, err) // API server restarting
	}

	// Until the Kubelets stage the kubelet is deliberately left where it is.
	if v := node.Status.NodeInfo.KubeletVersion; stage == ichorv1alpha1.KubernetesStageKubelets && v != target {
		return fmt.Sprintf("node %q runs kubelet %s, want %s", node.Name, v, target)
	}

	if !nodeIsReady(&node) {
		return fmt.Sprintf("node %q is not Ready", node.Name)
	}

	if m.Spec.Role != roleControlPlane {
		return ""
	}

	components := staticPodComponents
	if stage == ichorv1alpha1.KubernetesStageAPIServers {
		// Controller-manager and scheduler are deliberately left where they are.
		components = staticPodComponents[:1]
	}

	for _, component := range components {
		pod, err := a.staticPod(ctx, component, nodeName)
		if err != nil {
			return err.Error()
		}

		if !podReady(pod) || imageTag(pod) != target {
			return fmt.Sprintf("%s on %q is not Ready on %s yet", component, nodeName, target)
		}
	}

	return ""
}

// staticPod finds component's mirror pod on hostname.
func (a *kubernetesUpgradeAdvancer) staticPod(ctx context.Context, component, hostname string) (*corev1.Pod, error) {
	pods, err := a.componentPods(ctx, component)
	if err != nil {
		return nil, err
	}

	for i := range pods {
		if pods[i].Spec.NodeName == hostname {
			return &pods[i], nil
		}
	}

	return nil, fmt.Errorf("no %s pod on %q yet", component, hostname)
}

func (a *kubernetesUpgradeAdvancer) componentPods(ctx context.Context, component string) ([]corev1.Pod, error) {
	var pods corev1.PodList
	if err := a.Reader.List(ctx, &pods, client.InNamespace(kubeSystemNamespace), client.MatchingLabels{labelK8sApp: component}); err != nil {
		return nil, fmt.Errorf("listing %s pods: %w", component, err)
	}

	// A terminating pod is the old generation of a static pod being replaced; judging the node by
	// it would report the old version.
	return slices.DeleteFunc(pods.Items, func(p corev1.Pod) bool { return !p.DeletionTimestamp.IsZero() }), nil
}

// kubeProxyNotReady returns why the kube-proxy DaemonSet has not finished rolling out target.
func (a *kubernetesUpgradeAdvancer) kubeProxyNotReady(ctx context.Context, target string) string {
	var ds appsv1.DaemonSet
	if err := a.Reader.Get(ctx, types.NamespacedName{Namespace: kubeSystemNamespace, Name: kubeProxyName}, &ds); err != nil {
		return fmt.Sprintf("reading the kube-proxy DaemonSet: %v", err)
	}

	for _, c := range ds.Spec.Template.Spec.Containers {
		if tagOf(c.Image) != target {
			return fmt.Sprintf("kube-proxy still specifies %s, want %s", c.Image, target)
		}
	}

	if ds.Status.UpdatedNumberScheduled < ds.Status.DesiredNumberScheduled || ds.Status.NumberReady < ds.Status.DesiredNumberScheduled {
		return fmt.Sprintf("kube-proxy is rolling out: %d/%d ready", ds.Status.NumberReady, ds.Status.DesiredNumberScheduled)
	}

	return ""
}

// waiting is the "not converged yet" outcome: it starts the verification clock if no marker exists
// (a crash between apply and marker, or a node found already carrying the rendered configuration)
// and fails the step once the clock passes kubernetesVerifyTimeout.
func (a *kubernetesUpgradeAdvancer) waiting(
	ctx context.Context, m *ichorv1alpha1.TalosMachine, cluster *ichorv1alpha1.TalosCluster, reason string,
) error {
	cond := apimeta.FindStatusCondition(m.Status.Conditions, conditionKubernetesUpgrade)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		return setMachineCondition(ctx, a.Reader, a.Client, m, metav1.Condition{
			Type: conditionKubernetesUpgrade, Status: metav1.ConditionTrue, Reason: reasonKubernetesApplied, Message: reason,
		})
	}

	if time.Since(cond.LastTransitionTime.Time) > kubernetesVerifyTimeout {
		return fmt.Errorf("machine %q did not converge on Kubernetes %s within %s: %s",
			m.Name, cluster.Spec.KubernetesVersion, kubernetesVerifyTimeout, reason)
	}

	return nil
}

// cniHandoverPending explains why a Kubernetes upgrade must not start because of an unfinished
// CNI hand-over, or returns "". The bootstrap-manifest sync re-applies the cluster's inline
// manifests; while the inline CNI entry is still rendered, that could roll the CNI back to the
// bootstrap version after Ichor's Helm release changed it. spec.cni.handover is
// not read: every value is automatic now. A CNI with nothing to hand over (any preset but cilium,
// which the controllers refuse) waits for nothing.
func cniHandoverPending(cluster *ichorv1alpha1.TalosCluster) string {
	if cluster.Spec.CNI.Preset != ciliumName {
		return ""
	}

	cond := apimeta.FindStatusCondition(cluster.Status.Conditions, render.ConditionCNIHandover)
	if cond != nil && cond.Status == metav1.ConditionTrue {
		return ""
	}

	if cond == nil {
		return "no " + render.ConditionCNIHandover + " condition yet"
	}

	return cond.Reason + ": " + cond.Message
}

func nodeIsReady(node *corev1.Node) bool {
	for _, c := range node.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}

	return false
}

// nodeReadyByName reads the Node's Ready condition live when kube is wired, else through reader.
func nodeReadyByName(ctx context.Context, kube kubernetes.Interface, reader client.Reader, name string) (bool, error) {
	var node corev1.Node
	if kube != nil {
		got, err := kube.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		return nodeIsReady(got), nil
	}

	if err := reader.Get(ctx, types.NamespacedName{Name: name}, &node); err != nil {
		return false, err
	}

	return nodeIsReady(&node), nil
}

// imageTag is the tag of pod's first container image.
func imageTag(pod *corev1.Pod) string {
	if len(pod.Spec.Containers) == 0 {
		return ""
	}

	return tagOf(pod.Spec.Containers[0].Image)
}

// tagOf extracts the tag of an image reference ("registry:5000/a/b:v1.2.3@sha256:..." -> "v1.2.3").
func tagOf(ref string) string {
	ref, _, _ = strings.Cut(ref, "@")

	i := strings.LastIndex(ref, ":")
	if i < 0 || strings.Contains(ref[i:], "/") {
		return ""
	}

	return ref[i+1:]
}
