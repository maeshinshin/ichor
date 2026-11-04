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

package uiapi

import (
	"fmt"
	"slices"
	"strings"
	"time"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// The key-rotation kind (ADR 0121): the request, its plan and its description.

// keyRotationSpec turns a request into the spec of the operation. No target means both keys; no duration the
// controller's default.
func keyRotationSpec(req *pb.KeyRotationRequest) (*ichorv1alpha1.TalosOperationKeyRotationSpec, error) {
	targets := req.GetTargets()
	if len(targets) == 0 {
		targets = []string{ichorv1alpha1.KeyRotationTargetServiceAccount, ichorv1alpha1.KeyRotationTargetSecretbox}
	}

	seen := map[string]bool{}

	for _, t := range targets {
		if t != ichorv1alpha1.KeyRotationTargetServiceAccount && t != ichorv1alpha1.KeyRotationTargetSecretbox {
			return nil, invalid("%q is not a key this rotation replaces (%s, %s)", t,
				ichorv1alpha1.KeyRotationTargetServiceAccount, ichorv1alpha1.KeyRotationTargetSecretbox)
		}

		if seen[t] {
			return nil, invalid("the key %q is named twice", t)
		}

		seen[t] = true
	}

	spec := &ichorv1alpha1.TalosOperationKeyRotationSpec{Targets: slices.Clone(targets), IgnoreLegacyTokens: req.GetIgnoreLegacyTokens()}

	if s := req.GetTokenSettle(); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil || d < 0 {
			return nil, invalid("token_settle %q is not a duration of zero or more (like 90m)", s)
		}

		spec.TokenSettle = &metav1.Duration{Duration: d}
	}

	return spec, nil
}

// keyRotationStages are the stages a rotation of these targets passes through, in order, without the last two
// (Finalizing and Done), which the plan does not list for a CA rotation either.
func keyRotationStages(spec *ichorv1alpha1.TalosOperationKeyRotationSpec) []string {
	stages := []string{ichorv1alpha1.KeyRotationStageAccepting, ichorv1alpha1.KeyRotationStageIssuing}

	if slices.Contains(spec.Targets, ichorv1alpha1.KeyRotationTargetSecretbox) {
		stages = append(stages, ichorv1alpha1.KeyRotationStageReencrypting)
	}

	if slices.Contains(spec.Targets, ichorv1alpha1.KeyRotationTargetServiceAccount) {
		stages = append(stages, ichorv1alpha1.KeyRotationStageSettlingTokens)
	}

	return append(stages, ichorv1alpha1.KeyRotationStageDropping)
}

// planKeyRotation fills what the plan of a key rotation says, and keeps the spec for CreateOperation.
func planKeyRotation(p *opPlan, req *pb.KeyRotationRequest) error {
	spec, err := keyRotationSpec(req)
	if err != nil {
		return err
	}

	p.keys = spec
	p.resp.Effect = pb.OperationEffect_OPERATION_EFFECT_KEY_ROTATION
	p.resp.Stages = keyRotationStages(spec)
	p.resp.ReplacesSecretsBundle = true

	return nil
}

// keyRotationBlocked is why a key rotation cannot start on cluster, or "".
func keyRotationBlocked(cluster *ichorv1alpha1.TalosCluster) string {
	contract, err := coreconfig.ParseContractFromVersion(cluster.Spec.VersionContract)
	if err != nil {
		return fmt.Sprintf("spec.versionContract %q is not a version contract", cluster.Spec.VersionContract)
	}

	if !contract.MultidocKubernetesConfigSupported() {
		return fmt.Sprintf("spec.versionContract %q keeps the service-account key and the secretbox secret in single fields that "+
			"cannot hold two keys at once; raise the contract to v1.14 or later before rotating them", cluster.Spec.VersionContract)
	}

	return ""
}

// describeKeyRotation adds the summary and the facts of a key-rotation operation.
func describeKeyRotation(out *pb.Operation, op *ichorv1alpha1.TalosOperation, add func(key, value string)) {
	out.Summary = "Rotate the service-account and secretbox keys"

	kr := op.Spec.KeyRotation
	if kr == nil {
		return
	}

	add("Keys", strings.Join(kr.Targets, ", "))

	if kr.TokenSettle != nil {
		add("Token settle", kr.TokenSettle.Duration.String())
	}

	if kr.IgnoreLegacyTokens {
		add("Legacy tokens", "ignored")
	}
}
