package controller

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const (
	reasonCAClientsBack = "ClientsBack"

	// caClientsRecheck is how often a still-raised CAClientsNotRestarted is looked at again.
	caClientsRecheck = time.Minute
)

// notBackName finds the workloads the condition message names: "Kind namespace/name", as caClient.String writes it.
// shortcut: the message is the only carrier of the names; give it a structured home if the message wording ever changes.
var notBackName = regexp.MustCompile(`\b(Deployment|DaemonSet) ([a-z0-9.-]+)/([a-z0-9.-]+)`)

// clearCAClients lowers CAClientsNotRestarted once every workload it names is available on its current template,
// so the warning does not outlive the problem. It reports whether the condition is still raised. A workload that
// no longer exists is not waited for.
func (r *TalosClusterReconciler) clearCAClients(ctx context.Context, cluster *ichorv1alpha1.TalosCluster) (bool, error) {
	cond := apimeta.FindStatusCondition(cluster.Status.Conditions, ichorv1alpha1.ConditionCAClientsNotRestarted)
	if cond == nil || cond.Status != metav1.ConditionTrue || r.KubeClient == nil {
		return false, nil
	}

	names := notBackName.FindAllStringSubmatch(cond.Message, -1)

	for _, m := range names {
		var back bool

		var err error

		if m[1] == kindDeployment {
			d, gerr := r.KubeClient.AppsV1().Deployments(m[2]).Get(ctx, m[3], metav1.GetOptions{})
			back, err = gerr == nil && deploymentAvailable(d), gerr
		} else {
			d, gerr := r.KubeClient.AppsV1().DaemonSets(m[2]).Get(ctx, m[3], metav1.GetOptions{})
			back, err = gerr == nil && daemonSetAvailable(d), gerr
		}

		if apierrors.IsNotFound(err) {
			continue
		}

		if err != nil {
			return true, fmt.Errorf("checking %s %s/%s: %w", m[1], m[2], m[3], err)
		}

		if !back {
			return true, nil
		}
	}

	listed := make([]string, 0, len(names))
	for _, m := range names {
		listed = append(listed, m[1]+" "+m[2]+"/"+m[3])
	}

	err := patchClusterConditions(ctx, r.Client, cluster, func(c *ichorv1alpha1.TalosCluster) {
		apimeta.SetStatusCondition(&c.Status.Conditions, metav1.Condition{
			Type: ichorv1alpha1.ConditionCAClientsNotRestarted, Status: metav1.ConditionFalse, Reason: reasonCAClientsBack,
			Message:            "the clients not back after the last CA rotation are available again: " + strings.Join(listed, ", "),
			ObservedGeneration: c.Generation,
		})
	})
	if err != nil {
		return true, fmt.Errorf("lowering CAClientsNotRestarted: %w", err)
	}

	return false, nil
}
