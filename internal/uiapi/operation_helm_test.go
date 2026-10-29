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

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// A Helm-installed Ichor has no IchorInstallation (ADR 0104 4.3): planning an Ichor upgrade says
// to use Helm instead of failing with a bare not-found, and the read-only answer stays empty.
func TestIchorUpgradeOnAHelmInstalledIchorPointsToHelm(t *testing.T) {
	srv, _ := opServer(t, opObjects()...)

	_, err := srv.PlanOperation(context.Background(), connect.NewRequest(&pb.PlanOperationRequest{
		Cluster: objectRef(opNS, opCluster), Kind: pb.OperationKind_OPERATION_KIND_ICHOR,
	}))
	if err == nil || connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "helm upgrade") {
		t.Fatalf("PlanOperation = %v; want FailedPrecondition naming helm upgrade", err)
	}

	res, err := srv.GetIchorInstallation(context.Background(), connect.NewRequest(&pb.GetIchorInstallationRequest{}))
	if err != nil || res.Msg.GetInstalledVersion() != "" {
		t.Fatalf("GetIchorInstallation = %v, %v; want empty and no error", res, err)
	}
}
