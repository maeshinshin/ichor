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
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// The kubernetes kind's plan (ADR 0057, 0087): its stages, and the warning for a minor upgrade of
// a multi-control-plane cluster whose endpoint is one node.

func planKubernetes(t *testing.T, endpointType string, controlPlanes int, target string) *pb.PlanOperationResponse {
	t.Helper()

	objs := opObjects()
	objs[0].(*ichorv1alpha1.TalosCluster).Spec.Endpoint = ichorv1alpha1.TalosClusterEndpoint{Type: endpointType, Address: gatewayIP}

	for i := 2; i <= controlPlanes; i++ {
		objs = append(objs, opMachine("cp"+string(rune('0'+i)), "controlplane", nil, nil))
	}

	srv, _ := opServer(t, objs...)

	res, err := srv.PlanOperation(context.Background(), connect.NewRequest(&pb.PlanOperationRequest{
		Cluster: objectRef(opNS, opCluster), Kind: pb.OperationKind_OPERATION_KIND_KUBERNETES, Target: target,
	}))
	if err != nil {
		t.Fatal(err)
	}

	return res.Msg
}

func TestPlanKubernetesNamesTheFourStages(t *testing.T) {
	p := planKubernetes(t, "vip", 1, "v1.36.6")

	if got := strings.Join(p.GetStages(), "|"); !strings.Contains(got, "kube-apiserver") || !strings.Contains(got, "kube-controller-manager") ||
		!strings.Contains(got, "kube-proxy") || !strings.Contains(got, "kubelet") || len(p.GetStages()) != 4 {
		t.Fatalf("stages = %q", got)
	}
}

func TestPlanKubernetesWarnsAboutANodeEndpointOnAMinorUpgradeOfSeveralControlPlanes(t *testing.T) {
	warned := planKubernetes(t, "node", 3, "v1.36.6")
	if !strings.Contains(warned.GetWarning(), "virtual IP") || !strings.Contains(warned.GetWarning(), "single") || warned.GetBlocked() != "" {
		t.Fatalf("a minor upgrade through a node endpoint must warn, not refuse: %v", warned)
	}

	for name, p := range map[string]*pb.PlanOperationResponse{
		"a patch upgrade":          planKubernetes(t, "node", 3, "v1.35.9"),
		"a virtual IP":             planKubernetes(t, "vip", 3, "v1.36.6"),
		"a single control plane":   planKubernetes(t, "node", 1, "v1.36.6"),
		"a load balancer endpoint": planKubernetes(t, "loadBalancer", 3, "v1.36.6"),
	} {
		if p.GetWarning() != "" {
			t.Errorf("%s: unexpected warning %q", name, p.GetWarning())
		}
	}
}

func TestOperationToPBCarriesTheKubernetesStage(t *testing.T) {
	op := &ichorv1alpha1.TalosOperation{}
	op.Spec.Kind = ichorv1alpha1.TalosOperationKindKubernetes
	op.Status.Kubernetes = &ichorv1alpha1.TalosOperationKubernetesStatus{Stage: ichorv1alpha1.KubernetesStageKubeProxy}

	if got := operationToPB(op).GetKubernetesStage(); got != "KubeProxy" {
		t.Fatalf("kubernetes stage = %q, want KubeProxy", got)
	}
	if got := operationToPB(&ichorv1alpha1.TalosOperation{}).GetKubernetesStage(); got != "" {
		t.Fatalf("kubernetes stage without status = %q, want empty", got)
	}
}
