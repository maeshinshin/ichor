package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"

	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Timing of the diagnostics of a bounded wait.
const (
	waitDiagEvery  = 30 * time.Second
	waitDiagGiveUp = 2 * time.Minute

	// Per-wait timeouts. The webhook wait of init is defaultWebhookWait (30 minutes), that of adopt 15; --wait-timeout sets either.
	defaultAdoptWebhookWait = 15 * time.Minute
)

// errNoPodNetwork ends a wait early: nothing can be scheduled because the cluster has no CNI.
var errNoPodNetwork = errors.New("the pods of Ichor cannot start because this cluster has no pod network " +
	"(its nodes are NotReady: cni plugin not initialized). Install a CNI, or let `ichor adopt` install Cilium (a cluster with no CNI)")

// waitDiag explains a slow wait for Ichor: every waitDiagEvery it prints why each pod of the
// namespace is not running, and it fails once every pod has been unschedulable for waitDiagGiveUp
// while every node is NotReady with the network-plugin message.
type waitDiag struct {
	c        client.Reader
	ns       string
	w        io.Writer
	now      func() time.Time
	lastShow time.Time
	stuck    time.Time // when "all pods unschedulable on a NotReady cluster" first held
}

func newWaitDiag(c client.Reader, w io.Writer) *waitDiag {
	return &waitDiag{c: c, ns: ichorControllerNamespace, w: w, now: time.Now}
}

// tick is called after a failed attempt. It returns errNoPodNetwork when the wait cannot succeed.
func (d *waitDiag) tick(ctx context.Context) error {
	now := d.now()

	var pods corev1.PodList
	if d.c.List(ctx, &pods, client.InNamespace(d.ns)) != nil || len(pods.Items) == 0 {
		d.stuck = time.Time{}

		return nil
	}

	reasons, allUnschedulable := podReasons(pods.Items)

	if d.lastShow.IsZero() || now.Sub(d.lastShow) >= waitDiagEvery {
		d.lastShow = now
		for _, r := range reasons {
			_, _ = fmt.Fprintf(d.w, "wait: %s\n", r)
		}
	}

	if !allUnschedulable || !d.nodesNoNetwork(ctx) || d.ciliumInstalling(ctx) {
		d.stuck = time.Time{}

		return nil
	}

	if d.stuck.IsZero() {
		d.stuck = now
	}

	if now.Sub(d.stuck) >= waitDiagGiveUp {
		return errNoPodNetwork
	}

	return nil
}

// summary lists what is still not running, for the message of a timeout.
func (d *waitDiag) summary(ctx context.Context) string {
	var pods corev1.PodList
	if d.c.List(ctx, &pods, client.InNamespace(d.ns)) != nil {
		return ""
	}

	reasons, _ := podReasons(pods.Items)

	return strings.Join(reasons, "; ")
}

// podReasons returns one line per pod that is Pending or waiting in a failure state, and whether
// every pod is Pending because the scheduler found no node.
func podReasons(pods []corev1.Pod) (lines []string, allUnschedulable bool) {
	allUnschedulable = len(pods) > 0

	for i := range pods {
		p := &pods[i]

		unschedulable := false

		if p.Status.Phase == corev1.PodPending {
			msg := "Pending"

			for _, c := range p.Status.Conditions {
				if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse {
					unschedulable = c.Reason == corev1.PodReasonUnschedulable
					msg = "Pending: " + c.Message
				}
			}

			lines = append(lines, fmt.Sprintf("pod %s/%s %s", p.Namespace, p.Name, msg))
		}

		for _, cs := range p.Status.ContainerStatuses {
			if w := cs.State.Waiting; w != nil && (w.Reason == "ImagePullBackOff" || w.Reason == "CrashLoopBackOff" || w.Reason == "ErrImagePull") {
				lines = append(lines, fmt.Sprintf("pod %s/%s container %s waiting: %s", p.Namespace, p.Name, cs.Name, w.Reason))
			}
		}

		if !unschedulable {
			allUnschedulable = false
		}
	}

	slices.Sort(lines)

	return lines, allUnschedulable
}

// ciliumInstalling reports whether a Cilium agent pod exists: the CNI is on its way (Talos applied
// it as an inline manifest, as `ichor init` does), so the nodes' missing network is not final.
func (d *waitDiag) ciliumInstalling(ctx context.Context) bool {
	var pods corev1.PodList

	return d.c.List(ctx, &pods, client.InNamespace("kube-system"), client.MatchingLabels{"k8s-app": "cilium"}) == nil &&
		len(pods.Items) > 0
}

// nodesNoNetwork reports whether there is at least one node and every one is NotReady because
// its network plugin is not ready.
func (d *waitDiag) nodesNoNetwork(ctx context.Context) bool {
	var nodes corev1.NodeList
	if d.c.List(ctx, &nodes) != nil || len(nodes.Items) == 0 {
		return false
	}

	for i := range nodes.Items {
		ok := false

		for _, c := range nodes.Items[i].Status.Conditions {
			if c.Type == corev1.NodeReady && c.Status != corev1.ConditionTrue &&
				(c.Reason == "NetworkPluginNotReady" || strings.Contains(c.Message, "cni plugin not initialized") ||
					strings.Contains(c.Message, "NetworkPluginNotReady")) {
				ok = true
			}
		}

		if !ok {
			return false
		}
	}

	return true
}

// pollBounded is pollWhile for a wait on Ichor's own pods: it prints why they are not running,
// stops early when the cluster has no pod network, and on timeout names what is left. d may be nil.
func pollBounded(
	ctx context.Context, timeout, interval time.Duration, progress io.Writer, what string, d *waitDiag,
	attempt func(context.Context) error, retryable func(error) bool,
) error {
	return pollDiag(ctx, timeout, interval, progress, what, d, attempt, retryable, what+" did not finish within "+timeout.String())
}

// pollDiag is pollBounded with the start of the timeout message given: timedOut, then what is left
// in brackets, then the last error.
func pollDiag(
	ctx context.Context, timeout, interval time.Duration, progress io.Writer, what string, d *waitDiag,
	attempt func(context.Context) error, retryable func(error) bool, timedOut string,
) error {
	err := pollWhile(ctx, timeout, interval, progress, what, func(ctx context.Context) error {
		err := attempt(ctx)
		if err == nil || d == nil {
			return err
		}

		if fatal := d.tick(ctx); fatal != nil {
			return fatal
		}

		return err
	}, func(err error) bool { return !errors.Is(err, errNoPodNetwork) && retryable(err) })
	if err == nil || ctx.Err() != nil || errors.Is(err, errNoPodNetwork) || !retryable(err) {
		return err
	}

	if d != nil {
		if s := d.summary(ctx); s != "" {
			timedOut += " (" + s + ")"
		}
	}

	return fmt.Errorf("%s: %w", timedOut, err)
}
