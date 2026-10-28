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
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/render"
)

const (
	// defaultStackWait bounds the wait for Kokumi and Argo CD: the controller launches two Jobs
	// that pull images, then Kokumi renders and Argo CD syncs both bundles.
	defaultStackWait = 30 * time.Minute

	// defaultHandoverWait bounds the wait for the Cilium Helm Job after the stack is up.
	defaultHandoverWait = 20 * time.Minute

	// conditionStackReady is the IchorInstallation condition the controller sets.
	conditionStackReady = "StackReady"

	// kokumiAdminSecret and kokumiAdminKey name the Secret the controller generates in the
	// controller's namespace; it holds the Kokumi admin password in clear.
	kokumiAdminSecret = "ichor-kokumi-admin"
	kokumiAdminKey    = "password"
)

// waitForStack polls the IchorInstallation until StackReady is True. A False condition is
// retried: the controller keeps working on it and reports why.
func waitForStack(ctx context.Context, c client.Client, timeout, interval time.Duration, progress io.Writer) error {
	return pollWhile(ctx, timeout, interval, progress, "Kokumi and Argo CD (StackReady)", func(ctx context.Context) error {
		var inst ichorv1alpha1.IchorInstallation
		if err := c.Get(ctx, types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}, &inst); err != nil {
			return err
		}

		cond := apimeta.FindStatusCondition(inst.Status.Conditions, conditionStackReady)
		if cond == nil {
			return fmt.Errorf("the controller has not reported %s yet", conditionStackReady)
		}

		if cond.Status != metav1.ConditionTrue {
			return fmt.Errorf("%s is %s (%s): %s%s", conditionStackReady, cond.Status, cond.Reason, cond.Message, stackSummary(inst.Status.Stack))
		}

		return nil
	}, func(error) bool { return true })
}

// stackSummary is the per-component part of a progress line.
func stackSummary(s *ichorv1alpha1.IchorStackStatus) string {
	if s == nil {
		return ""
	}

	var out strings.Builder

	for _, c := range []struct {
		name string
		st   *ichorv1alpha1.IchorStackComponentStatus
	}{{"kokumi", s.Kokumi}, {"argocd", s.ArgoCD}} {
		if c.st == nil {
			continue
		}

		fmt.Fprintf(&out, " [%s: published=%t orderReady=%t application=%q]", c.name, c.st.PublishedTag != "", c.st.OrderReady, c.st.ApplicationHealth)
	}

	return out.String()
}

// waitForCNIHandover polls the TalosCluster until CNIHandover is True: Cilium is then a Helm
// release owned by Ichor and the inline manifest is gone from the rendered configuration.
func waitForCNIHandover(
	ctx context.Context, c client.Client, key types.NamespacedName, timeout, interval time.Duration, progress io.Writer,
) error {
	return pollWhile(ctx, timeout, interval, progress, "Cilium to be handed over to Ichor ("+render.ConditionCNIHandover+")", func(ctx context.Context) error {
		var cluster ichorv1alpha1.TalosCluster
		if err := c.Get(ctx, key, &cluster); err != nil {
			return err
		}

		cond := apimeta.FindStatusCondition(cluster.Status.Conditions, render.ConditionCNIHandover)
		if cond == nil {
			return fmt.Errorf("the controller has not reported %s yet", render.ConditionCNIHandover)
		}

		if cond.Status != metav1.ConditionTrue {
			return fmt.Errorf("%s is %s (%s): %s", render.ConditionCNIHandover, cond.Status, cond.Reason, cond.Message)
		}

		return nil
	}, func(error) bool { return true })
}

// printKokumiAdminPassword reads the generated Kokumi admin password and prints it to stdout,
// once. It goes nowhere else: not to stderr, not into an error, not to a file.
func printKokumiAdminPassword(ctx context.Context, c client.Client, stdout io.Writer) error {
	var sec corev1.Secret

	key := types.NamespacedName{Namespace: ichorControllerNamespace, Name: kokumiAdminSecret}
	if err := c.Get(ctx, key, &sec); err != nil {
		return fmt.Errorf("init: reading the Kokumi admin password from Secret %s/%s: %w", key.Namespace, key.Name, err)
	}

	pw := sec.Data[kokumiAdminKey]
	if len(pw) == 0 {
		return fmt.Errorf("init: Secret %s/%s has no %q key", key.Namespace, key.Name, kokumiAdminKey)
	}

	_, _ = fmt.Fprintf(stdout,
		"\nKokumi admin login (shown once; it stays in Secret %s/%s):\n  user:     admin\n  password: %s\n",
		key.Namespace, key.Name, pw)

	return nil
}
