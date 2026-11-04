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
	"time"

	authv1 "k8s.io/api/authorization/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// rotate-keys goes through OperationService like every other start.

const (
	keyTestContract    = "v1.14"
	onlyServiceAccount = "service-account"
)

// keyTestObjects is opTestObjects on a contract the key rotation can run under.
func keyTestObjects() []client.Object {
	objs := opTestObjects()
	objs[0].(*ichorv1alpha1.TalosCluster).Spec.VersionContract = keyTestContract

	return objs
}

func TestRotateKeysPromptSaysWhatHappensToTheKeysAndDeclineWritesNothing(t *testing.T) {
	setAPITestFlags(t, false)

	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(keyTestObjects()...).Build()
	c := localClients(apiOpts, apiDeps{K8s: k8s})

	var prompt bytes.Buffer

	err := runStartOperation(context.Background(), c, strings.NewReader("n\n"), &bytes.Buffer{}, &prompt,
		startSpec{Kind: pb.OperationKind_OPERATION_KIND_KEY_ROTATION, Cluster: opTestCluster})
	if err == nil || !strings.Contains(err.Error(), "aborted") {
		t.Fatalf("want aborted, got %v", err)
	}

	for _, want := range []string{
		onlyServiceAccount, ichorv1alpha1.KeyRotationTargetSecretbox, "Accepting, Issuing, Reencrypting, SettlingTokens, Dropping",
		"kube-apiserver", "one at a time", "1. " + opCP + " (controlplane)", "ichor cluster bundle-export " + opTestCluster,
		"every Secret", "legacy", "90 minutes",
	} {
		if !strings.Contains(prompt.String(), want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt.String())
		}
	}

	// Workers carry neither key and are not listed.
	if strings.Contains(prompt.String(), "(worker)") {
		t.Errorf("the plan lists a worker:\n%s", prompt.String())
	}

	if len(opTestOps(t, k8s)) != 0 {
		t.Fatal("a declined rotation wrote something")
	}
}

func TestRotateKeysWithTheFlagsCreatesTheOperationTheyDescribe(t *testing.T) {
	setAPITestFlags(t, true)

	k8s := interceptor.NewClient(fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(keyTestObjects()...).Build(), interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if sar, ok := obj.(*authv1.SelfSubjectAccessReview); ok {
				sar.Status.Allowed = true

				return nil
			}

			return c.Create(ctx, obj, opts...)
		},
	})
	c := localClients(apiOpts, apiDeps{K8s: k8s})

	spec, err := keyRotationStart(opTestCluster, []string{ichorv1alpha1.KeyRotationTargetSecretbox}, "2h", true)
	if err != nil {
		t.Fatal(err)
	}

	if err := runStartOperation(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, spec); err != nil {
		t.Fatal(err)
	}

	ops := opTestOps(t, k8s)
	if len(ops) != 1 || ops[0].Spec.Kind != ichorv1alpha1.TalosOperationKindKeyRotation {
		t.Fatalf("ops = %+v", ops)
	}

	kr := ops[0].Spec.KeyRotation
	if kr == nil || !slices.Equal(kr.Targets, []string{ichorv1alpha1.KeyRotationTargetSecretbox}) || kr.TokenSettle == nil || kr.TokenSettle.Duration != 2*time.Hour || !kr.IgnoreLegacyTokens {
		t.Fatalf("spec.keyRotation = %+v", kr)
	}
}

func TestRotateKeysOnlyAcceptsTheKeysItKnows(t *testing.T) {
	for only, want := range map[string][]string{
		"":                          nil,
		onlyServiceAccount:          {ichorv1alpha1.KeyRotationTargetServiceAccount},
		"serviceAccount":            {ichorv1alpha1.KeyRotationTargetServiceAccount},
		"secretbox":                 {ichorv1alpha1.KeyRotationTargetSecretbox},
		"secretbox,service-account": {ichorv1alpha1.KeyRotationTargetSecretbox, ichorv1alpha1.KeyRotationTargetServiceAccount},
	} {
		got, err := keyRotationTargets(only)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("--only %q = %v, %v; want %v", only, got, err, want)
		}
	}

	if _, err := keyRotationTargets("etcd"); err == nil || !strings.Contains(err.Error(), "etcd") {
		t.Errorf("an unknown key must be refused, naming it: %v", err)
	}

	if _, err := keyRotationStart(opTestCluster, nil, "soon", false); err == nil {
		t.Error("a --token-settle that is not a duration must be refused")
	}
}

func TestRotateKeysIsOneCommandThroughTheEntryPoint(t *testing.T) {
	if _, ok := clusterRotateKeysCmd.Annotations[writesAnnotation]; !ok {
		t.Error("rotate-keys is not marked as a write")
	}

	if got := kindName(pb.OperationKind_OPERATION_KIND_KEY_ROTATION); got != "key-rotation" {
		t.Errorf("kindName = %q", got)
	}

	line := progressLine(&pb.Operation{
		Ref: objectRef(testNamespace, "x"), Kind: pb.OperationKind_OPERATION_KIND_KEY_ROTATION, Phase: ichorv1alpha1.TalosOperationPhaseInProgress, KeyRotationStage: "Reencrypting",
	})
	if !strings.Contains(line, "stage Reencrypting") {
		t.Errorf("progress line lacks the stage: %q", line)
	}
}
