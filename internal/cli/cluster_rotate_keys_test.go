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

	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// rotate-keys goes through OperationService like every other start (docs/adr/0121, 0068).

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

	c, k8s := freshClients(t, keyTestObjects())

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

	k8s := allowStarts(t, keyTestObjects())
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

func TestRotateKeysRefusesATokenSettleShorterThanTheKubeletRefresh(t *testing.T) {
	sa := []string{ichorv1alpha1.KeyRotationTargetServiceAccount}

	for _, settle := range []string{"5m", "0s", "74m"} {
		_, err := keyRotationStart(opTestCluster, sa, settle, false)
		if err == nil || !strings.Contains(err.Error(), "--token-settle "+settle) || !strings.Contains(err.Error(), "kubelet") {
			t.Errorf("--token-settle %s: want a refusal that explains the kubelet refresh, got %v", settle, err)
		}
	}

	// No target means both keys, so the service-account key is among them.
	if _, err := keyRotationStart(opTestCluster, nil, "5m", false); err == nil {
		t.Error("--token-settle 5m without --only must be refused")
	}

	for _, ok := range []string{"", "75m", "90m", "24h"} {
		if _, err := keyRotationStart(opTestCluster, sa, ok, false); err != nil {
			t.Errorf("--token-settle %q: %v", ok, err)
		}
	}

	// The secretbox key alone has no tokens.
	if _, err := keyRotationStart(opTestCluster, []string{ichorv1alpha1.KeyRotationTargetSecretbox}, "5m", false); err != nil {
		t.Errorf("--only secretbox --token-settle 5m: %v", err)
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
