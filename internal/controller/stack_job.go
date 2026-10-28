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
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/stackapply"
	"github.com/maeshinshin/ichor/internal/version"
)

// The one-time apply of Kokumi and Argo CD runs in a Job under its own ServiceAccount, so the controller holds none
// of the rights that
// applying RBAC, CRDs and workloads takes.

const (
	stackJobPrefix = "ichor-stack-"

	// stackJobDeadline bounds the whole Job: the images of both components are pulled in it.
	stackJobDeadline = 30 * 60
)

func stackJobName(component string) string { return stackJobPrefix + component }

// stackJobImage is the image the Job runs: this controller's own version of the installer
// image, so the Job renders the very bytes the controller publishes. A development build has no
// such tag, so the version the installation asks for stands in.
func stackJobImage(inst *ichorv1alpha1.IchorInstallation) string {
	tag := version.Version
	if tag == "" || tag == "dev" {
		tag = inst.Spec.Version
	}

	return inst.Spec.InstallerRepository + ":" + tag
}

// newStackJob builds the Job for one component. The generated values arrive through
// secretKeyRef, resolved by the kubelet, so the Job's identity has no rights on the
// controller's namespace; the user's Argo CD values arrive as an argument.
func newStackJob(ns string, c *stackComponent, inst *ichorv1alpha1.IchorInstallation) *batchv1.Job {
	args := []string{"stack", "apply", c.name}
	if c.valuesJSON != "" {
		args = append(args, "--values="+c.valuesJSON)
	}

	secretEnv := func(name, secret, key string) corev1.EnvVar {
		return corev1.EnvVar{Name: name, ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			Name: secret, Key: key,
		}}}
	}

	env := []corev1.EnvVar{secretEnv(stackapply.EnvKokumiAdminHash, kokumiAdminSecret, secretKeyHash)}
	if c.name == componentArgoCD {
		env = []corev1.EnvVar{secretEnv(stackapply.EnvArgoRedisPasswd, argoRedisSecret, secretKeyPassword)}
	}

	return newJob(ns, stackJobName(c.name), stackapply.JobLabel, stackapply.ServiceAccount, stackJobDeadline,
		map[string]string{managedLabel: managedTrue}, nil,
		corev1.Container{Name: "stack", Image: stackJobImage(inst), Args: args, Env: env})
}

// ensureStackJob creates the component's Job if it does not exist and reports whether it has
// completed. A failed Job is an error, not a wait: it will not retry by itself.
func (r *IchorInstallationReconciler) ensureStackJob(
	ctx context.Context, c *stackComponent, inst *ichorv1alpha1.IchorInstallation,
) (done bool, err error) {
	key := types.NamespacedName{Namespace: r.ControllerNamespace, Name: stackJobName(c.name)}

	var job batchv1.Job

	switch err := r.Direct.Get(ctx, key, &job); {
	case apierrors.IsNotFound(err):
		if err := r.Direct.Create(ctx, newStackJob(key.Namespace, c, inst)); err != nil {
			return false, fmt.Errorf("creating Job %s: %w", key.Name, err)
		}

		return false, nil
	case err != nil:
		return false, fmt.Errorf("reading Job %s: %w", key.Name, err)
	}

	done, failed := jobOutcome(&job)
	if failed != nil {
		return false, fmt.Errorf("job %s failed (%s: %s); read its pod log, then delete the Job to try again",
			key.Name, failed.Reason, failed.Message)
	}

	return done, nil
}
