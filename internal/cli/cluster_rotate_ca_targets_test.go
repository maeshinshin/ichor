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
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	authv1 "k8s.io/api/authorization/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// rotate-ca --ca and --all (docs/adr/0120).

const (
	caTalos      = ichorv1alpha1.CARotationTargetTalos
	caKubernetes = ichorv1alpha1.CARotationTargetKubernetes
	caEtcd       = ichorv1alpha1.CARotationTargetEtcd
	caAggregator = ichorv1alpha1.CARotationTargetAggregator
)

func TestRotateCATargetsFromTheFlags(t *testing.T) {
	for _, tt := range []struct {
		name    string
		ca      []string
		all     bool
		want    []string
		wantErr string
	}{
		{name: "none", want: nil},
		{name: "one", ca: []string{caEtcd}, want: []string{caEtcd}},
		{name: "several", ca: []string{caEtcd, caAggregator}, want: []string{caEtcd, caAggregator}},
		{name: "all", all: true, want: []string{caTalos, caKubernetes, caEtcd, caAggregator}},
		{name: "all and ca", ca: []string{caEtcd}, all: true, wantErr: "alternatives"},
		{name: "not a CA", ca: []string{"service-account"}, wantErr: "the choices are talos, kubernetes, etcd, kubernetes-aggregator"},
	} {
		got, err := rotateCATargets(tt.ca, tt.all)
		if tt.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("%s: want error %q, got %v", tt.name, tt.wantErr, err)
			}

			continue
		}

		if err != nil || !slices.Equal(got, tt.want) {
			t.Errorf("%s: got %v, %v; want %v", tt.name, got, err, tt.want)
		}
	}
}

func TestOnlyTheTalosAndKubernetesCAsNeedTheCredentialHandoff(t *testing.T) {
	for _, tt := range []struct {
		targets []string
		want    bool
	}{
		{nil, true}, {[]string{caTalos}, true}, {[]string{caKubernetes}, true}, {[]string{caEtcd, caTalos}, true},
		{[]string{caEtcd}, false}, {[]string{caAggregator}, false}, {[]string{caEtcd, caAggregator}, false},
	} {
		if got := needsCredentialHandoff(tt.targets); got != tt.want {
			t.Errorf("targets %v: got %v, want %v", tt.targets, got, tt.want)
		}
	}
}

func allowStarts(t *testing.T, objs []client.Object) client.Client {
	t.Helper()

	// The start right (ADR 0094) is asked as the caller; here Kubernetes says yes.
	return interceptor.NewClient(fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(objs...).Build(), interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if sar, ok := obj.(*authv1.SelfSubjectAccessReview); ok {
				sar.Status.Allowed = true

				return nil
			}

			return c.Create(ctx, obj, opts...)
		},
	})
}

func TestRotateCAPlanOfEtcdAloneHasNoHandoffAndWarnsAboutRestartsAndTheSnapshot(t *testing.T) {
	setAPITestFlags(t, false)

	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(opTestObjects()...).Build()
	c := localClients(apiOpts, apiDeps{K8s: k8s})

	var prompt bytes.Buffer

	err := runStartOperation(context.Background(), c, strings.NewReader("n\n"), &bytes.Buffer{}, &prompt,
		startSpec{Kind: pb.OperationKind_OPERATION_KIND_CA_ROTATION, Cluster: opTestCluster, CATargets: []string{caEtcd}})
	if err == nil || !strings.Contains(err.Error(), "aborted") {
		t.Fatalf("want aborted, got %v", err)
	}

	for _, want := range []string{
		"Rotate the etcd certificate authorities", "no credential hand-off", "rebooted once in each of the three stages",
		"the control planes REBOOT in each pass", "ichor etcd backup now", "ichor cluster bundle-export " + opTestCluster,
	} {
		if !strings.Contains(prompt.String(), want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt.String())
		}
	}

	for _, not := range []string{"STOP WORKING", "WAITS", "kubelet RESTARTS", "kube-root-ca.crt", "without a reboot"} {
		if strings.Contains(prompt.String(), not) {
			t.Errorf("an etcd rotation's prompt must not say %q:\n%s", not, prompt.String())
		}
	}

	if len(opTestOps(t, k8s)) != 0 {
		t.Fatal("a declined rotation wrote something")
	}
}

func TestRotateCAPlanOfAllFourNamesEachAndKeepsTheHandoff(t *testing.T) {
	setAPITestFlags(t, false)

	objs := opTestObjects()

	// The fixture's cluster has an old version contract, where the aggregator CA cannot be rotated.
	c := localClients(apiOpts, apiDeps{K8s: fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(objs...).Build()})

	err := runStartOperation(context.Background(), c, strings.NewReader("n\n"), &bytes.Buffer{}, &bytes.Buffer{},
		startSpec{Kind: pb.OperationKind_OPERATION_KIND_CA_ROTATION, Cluster: opTestCluster, CATargets: []string{caAggregator}})
	if err == nil || !strings.Contains(err.Error(), "accepted list") {
		t.Fatalf("want the refusal of an old contract, got %v", err)
	}

	objs[0].(*ichorv1alpha1.TalosCluster).Spec.VersionContract = kubePrismDocContract
	c = localClients(apiOpts, apiDeps{K8s: fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(objs...).Build()})

	var prompt bytes.Buffer

	err = runStartOperation(context.Background(), c, strings.NewReader("n\n"), &bytes.Buffer{}, &prompt,
		startSpec{Kind: pb.OperationKind_OPERATION_KIND_CA_ROTATION, Cluster: opTestCluster, CATargets: allCARotationTargets})
	if err == nil || !strings.Contains(err.Error(), "aborted") {
		t.Fatalf("want aborted, got %v", err)
	}

	for _, want := range []string{
		"Talos API, Kubernetes API, etcd and Kubernetes aggregator certificate authorities", "STOP WORKING", "Extension API servers",
	} {
		if !strings.Contains(prompt.String(), want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt.String())
		}
	}
}

func TestRotateCAOfEtcdAloneWritesTheTargetsAndFollowsNothingItDoesNotNeed(t *testing.T) {
	setAPITestFlags(t, true)

	outputFormat = outputHuman // restored by setAPITestFlags

	k8s := allowStarts(t, opTestObjects())
	c := localClients(apiOpts, apiDeps{K8s: k8s})

	cmd := &cobra.Command{}

	var out, errw bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&errw)

	err := runStartOperation(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, startSpec{
		Kind: pb.OperationKind_OPERATION_KIND_CA_ROTATION, Cluster: opTestCluster, CATargets: []string{caEtcd},
		AfterCreate: func(ctx context.Context, ref *pb.ObjectRef, created *pb.CreateOperationResponse) error {
			return afterRotateCAWithoutHandoff(ctx, cmd, opTestCluster, ref, created)
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ops := opTestOps(t, k8s)
	if len(ops) != 1 || ops[0].Spec.CARotation == nil || !slices.Equal(ops[0].Spec.CARotation.Targets, []string{caEtcd}) {
		t.Fatalf("ops = %+v", ops)
	}

	for _, want := range []string{"started ca-rotation operation", "ichor operation get", "ichor cluster bundle-export " + opTestCluster} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}

	if strings.Contains(out.String(), "--kubeconfig") {
		t.Errorf("no new kubeconfig is written, so none is named:\n%s", out.String())
	}
}
