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

package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/kubectl/pkg/drain"
)

// Talos 1.14.3's Reboot does not cordon or drain (Sequencer.Reboot has no drain phase,
// and the controller that cordons reacts only to the shutting-down, upgrading and resetting
// stages), so a reboot that Ichor starts is preceded by Ichor's own cordon and drain, the same
// kubectl drain helper the removal path uses. Upgrade and Shutdown(force=false) do drain in
// Talos itself and are left to it.
const (
	// drainStartedAnnotation records when a rollout began draining a node, so the overall bound
	// survives a controller restart and the drain can be resumed from one reconcile to the next.
	drainStartedAnnotation = "ichor.maesh.dev/drain-started-at"

	// cordonedByIchorAnnotation marks a cordon this drain made, so a node an administrator
	// cordoned beforehand is not uncordoned behind their back.
	cordonedByIchorAnnotation = "ichor.maesh.dev/cordoned-by-ichor"

	// drainTimeout bounds the whole drain of one node. Past it the rollout pauses, the node is
	// uncordoned, and nothing is rebooted.
	drainTimeout = 10 * time.Minute
)

// drainStepTimeout bounds one reconcile's evictions. The cluster lock is renewed once per
// reconcile and lasts five minutes (clusterlock.DefaultDuration), so a step has to end well
// inside it; a drain that needs longer carries on in the next reconcile. A variable only so a
// test can shorten it.
var drainStepTimeout = 90 * time.Second

// drainEvictRetryDelay is how long the helper waits after a refused eviction (429, a
// PodDisruptionBudget) before asking again; left at 0 it asks in a tight loop for the whole step.
// A variable only so a test can shorten it.
var drainEvictRetryDelay = 5 * time.Second

// newDrainHelper is the one place the drain policy lives: evictions through the Eviction API (so
// PodDisruptionBudgets are respected and a blocked eviction is retried until timeout), DaemonSet
// pods ignored, mirror pods skipped by the helper's own filters, emptyDir data deleted with its
// pod, and pods that no controller owns refused rather than lost. timeout 0 means unbounded,
// so every drain inside a reconcile passes drainStepTimeout.
func newDrainHelper(ctx context.Context, kube kubernetes.Interface, timeout time.Duration) *drain.Helper {
	return &drain.Helper{
		Ctx: ctx, Client: kube, Timeout: timeout,
		GracePeriodSeconds: -1, IgnoreAllDaemonSets: true, DeleteEmptyDirData: true,
		Out: io.Discard, ErrOut: io.Discard, EvictErrorRetryDelay: drainEvictRetryDelay,
	}
}

// cordonAndDrain marks node unschedulable and evicts its pods.
func cordonAndDrain(h *drain.Helper, node *corev1.Node) error {
	if err := drain.RunCordonOrUncordon(h, node, true); err != nil {
		return fmt.Errorf("cordoning node %q: %w", node.Name, err)
	}

	if err := drain.RunNodeDrain(h, node.Name); err != nil {
		return fmt.Errorf("draining node %q: %w", node.Name, err)
	}

	return nil
}

// drainForReboot cordons the named node and evicts its pods for up to drainStepTimeout. It
// reports done once nothing evictable is left; not done with no error means "evictions are still
// going (a PodDisruptionBudget is holding some back), call again". Every decision is read from
// the Node, so a restarted controller resumes. After drainTimeout in total it returns an error
// and the caller pauses the rollout.
func drainForReboot(ctx context.Context, kube kubernetes.Interface, name string) (bool, error) {
	nodes := kube.CoreV1().Nodes()

	node, err := nodes.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return false, fmt.Errorf("reading node %q to drain it: %w", name, err)
	}

	started := time.Now()

	if v, ok := node.Annotations[drainStartedAnnotation]; ok {
		if t, perr := time.Parse(time.RFC3339, v); perr == nil {
			started = t
		}
	} else {
		annotations := map[string]string{drainStartedAnnotation: started.UTC().Format(time.RFC3339)}
		if !node.Spec.Unschedulable {
			annotations[cordonedByIchorAnnotation] = "rollout"
		}

		if node, err = patchNodeAnnotations(ctx, kube, name, annotations); err != nil {
			return false, err
		}
	}

	if elapsed := time.Since(started); elapsed > drainTimeout {
		return false, fmt.Errorf("draining node %q did not finish within %s (a PodDisruptionBudget or a pod that will not "+
			"terminate is holding it back); it was uncordoned and not rebooted", name, drainTimeout)
	}

	if err := cordonAndDrain(newDrainHelper(ctx, kube, drainStepTimeout), node); err != nil {
		// kubectl's helper reports its own timeout only as text.
		if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "global timeout reached") {
			return false, nil
		}

		return false, err
	}

	return true, nil
}

// uncordonAfterReboot undoes drainForReboot: it uncordons the node if the drain cordoned it and
// removes the drain's annotations. A node that is gone, or was never drained, is not an error.
func uncordonAfterReboot(ctx context.Context, kube kubernetes.Interface, name string) error {
	node, err := kube.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("reading node %q to uncordon it: %w", name, err)
	}

	_, started := node.Annotations[drainStartedAnnotation]
	_, cordoned := node.Annotations[cordonedByIchorAnnotation]

	if !started && !cordoned {
		return nil
	}

	if cordoned && node.Spec.Unschedulable {
		if err := drain.RunCordonOrUncordon(newDrainHelper(ctx, kube, 0), node, false); err != nil {
			return fmt.Errorf("uncordoning node %q: %w", name, err)
		}
	}

	_, err = patchNodeAnnotations(ctx, kube, name, map[string]string{drainStartedAnnotation: "", cordonedByIchorAnnotation: ""})

	return err
}

// patchNodeAnnotations merge-patches the node's annotations; an empty value removes the key.
func patchNodeAnnotations(ctx context.Context, kube kubernetes.Interface, name string, set map[string]string) (*corev1.Node, error) {
	var b strings.Builder

	b.WriteString(`{"metadata":{"annotations":{`)

	first := true

	for k, v := range set {
		if !first {
			b.WriteString(",")
		}

		first = false

		if v == "" {
			fmt.Fprintf(&b, "%q:null", k)
		} else {
			fmt.Fprintf(&b, "%q:%q", k, v)
		}
	}

	b.WriteString("}}}")

	node, err := kube.CoreV1().Nodes().Patch(ctx, name, types.MergePatchType, []byte(b.String()), metav1.PatchOptions{})
	if err != nil {
		return nil, fmt.Errorf("annotating node %q: %w", name, err)
	}

	return node, nil
}
