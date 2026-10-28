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
	"slices"
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const applyVerb = "apply"

func TestStackJobSpec(t *testing.T) {
	inst := &ichorv1alpha1.IchorInstallation{Spec: ichorv1alpha1.IchorInstallationSpec{
		Version: "v0.3.0", InstallerRepository: "registry.example/ichor-installer",
	}}

	for _, tc := range []struct {
		comp       *stackComponent
		wantArgs   []string
		wantEnvVar string
		wantSecret string
	}{
		{&stackComponent{name: componentKokumi}, []string{"stack", applyVerb, "kokumi"}, "ICHOR_KOKUMI_ADMIN_HASH", kokumiAdminSecret},
		{
			&stackComponent{name: componentArgoCD, valuesJSON: `{"a":1}`},
			[]string{"stack", applyVerb, componentArgoCD, `--values={"a":1}`}, "ICHOR_ARGOCD_REDIS_PASSWORD", argoRedisSecret,
		},
	} {
		job := newStackJob("ichor-system", tc.comp, inst)
		pod := job.Spec.Template.Spec

		if job.Name != "ichor-stack-"+tc.comp.name || job.Namespace != "ichor-system" {
			t.Errorf("%s: Job is %s/%s", tc.comp.name, job.Namespace, job.Name)
		}

		// The SA and the pod label are what config/rbac/ichor-stack and the Cilium policy select.
		if pod.ServiceAccountName != "ichor-stack" {
			t.Errorf("%s: ServiceAccount %q", tc.comp.name, pod.ServiceAccountName)
		}

		if got := job.Spec.Template.Labels["app.kubernetes.io/name"]; got != "ichor-stack" {
			t.Errorf("%s: pod label %q", tc.comp.name, got)
		}

		if pod.HostNetwork {
			t.Errorf("%s: the Job runs after Cilium is Ready and needs no hostNetwork", tc.comp.name)
		}

		c := pod.Containers[0]
		if c.Image != "registry.example/ichor-installer:v0.3.0" {
			t.Errorf("%s: image %q", tc.comp.name, c.Image)
		}

		if !slices.Equal(c.Args, tc.wantArgs) {
			t.Errorf("%s: args %v, want %v", tc.comp.name, c.Args, tc.wantArgs)
		}

		if len(c.Env) != 1 || c.Env[0].Name != tc.wantEnvVar || c.Env[0].ValueFrom.SecretKeyRef.Name != tc.wantSecret {
			t.Errorf("%s: env %v", tc.comp.name, c.Env)
		}

		if *job.Spec.BackoffLimit != 0 || *c.SecurityContext.ReadOnlyRootFilesystem != true {
			t.Errorf("%s: one attempt and a read-only root are expected", tc.comp.name)
		}
	}
}
