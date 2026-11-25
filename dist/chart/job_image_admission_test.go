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

package chart

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

const (
	kAPIVer   = "apiVersion"
	kMeta     = "metadata"
	kAPIGroup = "apiGroup"
	kCNI      = "cni"
	kKindKey  = "kind"
	kNameKey  = "name"
	kDefault  = "default"
)

// TestControllerJobImageFollowsTheIchorInstallation guards: with the chart's default values, a mirror
// set only on IchorInstallation.spec.installerRepository admits the controller's Cilium Job, and
// with no IchorInstallation the controller creates no Job at all (the policy's parameterNotFoundAction).
// It runs against a real kube-apiserver, which evaluates the rendered policies.
func TestControllerJobImageFollowsTheIchorInstallation(t *testing.T) {
	ctx := context.Background()
	crd := "../../config/crd/bases/ichor.maesh.dev_ichorinstallations.yaml"
	env := &envtest.Environment{CRDDirectoryPaths: []string{crd}, ErrorIfCRDPathMissing: true}

	cfg, err := env.Start()
	if err != nil {
		t.Fatalf("starting envtest: %v", err)
	}

	t.Cleanup(func() { _ = env.Stop() })

	admin, err := client.New(cfg, client.Options{})
	if err != nil {
		t.Fatal(err)
	}

	docs := renderChart(t, "ichor")
	for _, name := range []string{"ichor-controller-jobs", "ichor-controller-job-image"} {
		objs := map[string]string{kindValidatingAdmissionPolicy: name, kindValidatingAdmissionPolicyBind: name + "-binding"}
		for kind, n := range objs {
			if err := admin.Create(ctx, &unstructured.Unstructured{Object: find(docs, kind, n)}); err != nil {
				t.Fatalf("creating %s %s: %v", kind, n, err)
			}
		}
	}

	policy := find(docs, kindValidatingAdmissionPolicy, "ichor-controller-jobs")
	conds := policy[specKey].(map[string]any)["matchConditions"].([]any)
	expr := conds[0].(map[string]any)["expression"].(string)
	sa := regexp.MustCompile(`== '(system:serviceaccount:[^']+)'`).FindStringSubmatch(expr)[1]

	// RBAC is not under test: let the user create Jobs, so a refusal below is the policy's.
	crb := &unstructured.Unstructured{Object: map[string]any{
		kAPIVer: "rbac.authorization.k8s.io/v1", kKindKey: "ClusterRoleBinding",
		kMeta:      map[string]any{kNameKey: "controller-test"},
		"roleRef":  map[string]any{kAPIGroup: rbacGroup, kKindKey: "ClusterRole", kNameKey: "cluster-admin"},
		"subjects": []any{map[string]any{kAPIGroup: rbacGroup, kKindKey: "User", kNameKey: sa}},
	}}
	if err := admin.Create(ctx, crb); err != nil {
		t.Fatal(err)
	}

	as := &rest.Config{Host: cfg.Host, TLSClientConfig: cfg.TLSClientConfig}
	as.Impersonate.UserName = sa
	ctrl, err := client.New(as, client.Options{})
	if err != nil {
		t.Fatal(err)
	}

	job := func(name, image string) *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{
			kAPIVer: "batch/v1", kKindKey: "Job",
			kMeta: map[string]any{kNameKey: name, "namespace": "kube-system"},
			specKey: map[string]any{
				"activeDeadlineSeconds": int64(1500),
				"template": map[string]any{specKey: map[string]any{
					"serviceAccountName": "ichor-cni", "restartPolicy": "Never", "hostNetwork": true,
					"containers": []any{map[string]any{
						kNameKey: kCNI, "image": image, "args": []any{kCNI, "helm", "apply"},
						"securityContext": map[string]any{
							"allowPrivilegeEscalation": false,
							"capabilities":             map[string]any{"drop": []any{"ALL"}},
						},
					}},
				}},
			},
		}}
	}
	mirror := "mirror.example/ichor-installer"
	image := mirror + "@sha256:" + strings.Repeat("a", 64)

	inst := &unstructured.Unstructured{Object: map[string]any{
		kAPIVer: "ichor.maesh.dev/v1alpha1", kKindKey: "IchorInstallation",
		kMeta:   map[string]any{kNameKey: "ichor"},
		specKey: map[string]any{"version": "v0.0.1", "installerRepository": mirror},
	}}

	if err := admin.Create(ctx, inst); err != nil {
		t.Fatalf("creating the IchorInstallation: %v", err)
	}

	// Policies take a moment to compile: wait until the structural one refuses a stray Job.
	stray := func(n int) *unstructured.Unstructured {
		j := job(fmt.Sprintf("stray-%d", n), image)
		j.Object[kMeta].(map[string]any)["namespace"] = kDefault

		return j
	}

	deadline := time.Now().Add(30 * time.Second)
	for n := 0; ; n++ {
		err := ctrl.Create(ctx, stray(n))
		if isPolicyDenial(err, "ichor-controller-jobs'") {
			break
		}

		if time.Now().After(deadline) {
			t.Fatalf("the Job policy never became active: %v", err)
		}

		time.Sleep(500 * time.Millisecond)
	}

	if err := ctrl.Create(ctx, job("with-installation", image)); err != nil {
		t.Errorf("a conforming Job from the installerRepository set on the IchorInstallation: %v", err)
	}

	// The mirror is the only repository: another one is still refused.
	other := "ghcr.io/other/ichor-installer@sha256:" + strings.Repeat("a", 64)
	if err := ctrl.Create(ctx, job("other-repo", other)); !isPolicyDenial(err, "installerRepository") {
		t.Errorf("a Job from another repository: got %v, want Forbidden", err)
	}

	// Negative control: with no IchorInstallation the controller creates no Job.
	if err := admin.Delete(ctx, inst); err != nil {
		t.Fatal(err)
	}

	deadline = time.Now().Add(30 * time.Second)
	for n := 0; ; n++ {
		err := ctrl.Create(ctx, job(fmt.Sprintf("no-installation-%d", n), image))
		if isPolicyDenial(err, "no params found") {
			break
		}

		if time.Now().After(deadline) {
			t.Fatalf("a conforming Job with no IchorInstallation: got %v, want a denial for the missing parameter", err)
		}

		time.Sleep(500 * time.Millisecond)
	}
}

func isPolicyDenial(err error, mentions string) bool {
	if err == nil {
		return false
	}

	return strings.Contains(err.Error(), "ValidatingAdmissionPolicy") && strings.Contains(err.Error(), mentions)
}
