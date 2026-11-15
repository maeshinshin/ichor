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

package cilium

import (
	"context"
	"errors"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
)

// operatorName is the Deployment of the cilium operator in the chart.
const operatorName = "cilium-operator"

// CheckAgentsReady is ADR 0103's health precondition, first half: the cilium DaemonSet has an
// agent Ready on every node it is scheduled to, and the operator is Ready. The error names the
// nodes whose pod is not Ready (notReady); err is a check that could not run.
func CheckAgentsReady(ctx context.Context, kube kubernetes.Interface) (notReady, err error) {
	ds, err := kube.AppsV1().DaemonSets(ReleaseNamespace).Get(ctx, ReleaseName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("cilium: reading the cilium DaemonSet: %w", err)
	}

	var errs []error

	bad, err := notReadyPods(ctx, kube, ds.Spec.Selector)
	if err != nil {
		return nil, err
	}

	if st := ds.Status; st.DesiredNumberScheduled == 0 || st.NumberReady != st.DesiredNumberScheduled || len(bad) > 0 {
		errs = append(errs, fmt.Errorf("the DaemonSet %s is not Ready on all nodes (%d of %d agents Ready%s)",
			ReleaseName, st.NumberReady, st.DesiredNumberScheduled, listed(bad)))
	}

	op, err := kube.AppsV1().Deployments(ReleaseNamespace).Get(ctx, operatorName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("cilium: reading the %s Deployment: %w", operatorName, err)
	}

	if bad, err = notReadyPods(ctx, kube, op.Spec.Selector); err != nil {
		return nil, err
	}

	if want := ptr.Deref(op.Spec.Replicas, 1); op.Status.ReadyReplicas < want || len(bad) > 0 {
		errs = append(errs, fmt.Errorf("the operator %s is not Ready (%d of %d replicas Ready%s)",
			operatorName, op.Status.ReadyReplicas, want, listed(bad)))
	}

	if len(errs) > 0 {
		notReady = fmt.Errorf("cilium: %w; taking over restarts the agents, "+
			"so wait until Cilium is healthy (or repair it), then run adopt again", errors.Join(errs...))
	}

	return notReady, nil
}

func listed(bad []string) string {
	if len(bad) == 0 {
		return ""
	}

	return "; not Ready: " + strings.Join(bad, ", ")
}

// notReadyPods lists the pods the selector matches that are not Ready, as "pod X on node Y".
func notReadyPods(ctx context.Context, kube kubernetes.Interface, sel *metav1.LabelSelector) ([]string, error) {
	s, err := metav1.LabelSelectorAsSelector(sel)
	if err != nil {
		return nil, fmt.Errorf("cilium: reading a pod selector: %w", err)
	}

	pods, err := kube.CoreV1().Pods(ReleaseNamespace).List(ctx, metav1.ListOptions{LabelSelector: s.String()})
	if err != nil {
		return nil, fmt.Errorf("cilium: listing the pods of %s: %w", s, err)
	}

	var bad []string

	for i := range pods.Items {
		p := &pods.Items[i]
		if !podReady(p) {
			bad = append(bad, fmt.Sprintf("pod %s on node %s", p.Name, p.Spec.NodeName))
		}
	}

	return bad, nil
}

func podReady(p *corev1.Pod) bool {
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}

	return false
}
