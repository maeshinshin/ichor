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
	"errors"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// An Ichor upgrade swaps ichor-ui and its RBAC, so for a minute ichor-ui answers that its ServiceAccount
// cannot impersonate users. That is the rollout, not an answer about the operation.
var errImpersonate = connect.NewError(connect.CodePermissionDenied,
	errors.New(`forbidden: User "system:serviceaccount:ichor-system:ichor-ichor-ui" cannot impersonate resource "users" in API group ""`))

func watchKind(ops *flakyOps, kind pb.OperationKind) (string, error) {
	var out, errw bytes.Buffer

	c := &apiClients{Operation: ops, Close: func() {}}
	err := watchOperationKind(context.Background(), c, objectRef(testNamespace, "x"), &out, &errw, 0, kind)

	return errw.String(), err
}

func TestIchorUpgradeFollowRidesOutTheImpersonationDenialOfTheRollout(t *testing.T) {
	shrinkFollowTimers(t)
	setAPITestFlags(t, true)

	ops := &flakyOps{errs: []error{errImpersonate, errImpersonate}}

	stderr, err := watchKind(ops, pb.OperationKind_OPERATION_KIND_ICHOR)
	if err != nil {
		t.Fatalf("the follow died on the rollout's denial: %v\n%s", err, stderr)
	}

	if ops.calls != 3 || !strings.Contains(stderr, "retrying") {
		t.Fatalf("calls = %d, want 3 and a retry line:\n%s", ops.calls, stderr)
	}
}

// Only an Ichor upgrade has a rollout of ichor-ui to wait out, and only the impersonation denial is
// treated so: any other kind, and any other permission error, is still an answer.
func TestImpersonationDenialIsAnAnswerOutsideAnIchorUpgrade(t *testing.T) {
	shrinkFollowTimers(t)
	setAPITestFlags(t, true)

	ops := &flakyOps{errs: []error{errImpersonate}}
	if _, err := watchKind(ops, pb.OperationKind_OPERATION_KIND_KUBERNETES); err == nil || ops.calls != 1 {
		t.Fatalf("a Kubernetes upgrade follow: err = %v, calls = %d", err, ops.calls)
	}

	other := connect.NewError(connect.CodePermissionDenied, errors.New(`forbidden: User "x" cannot get resource "talosoperations"`))

	ops = &flakyOps{errs: []error{other}}
	if _, err := watchKind(ops, pb.OperationKind_OPERATION_KIND_ICHOR); err == nil || ops.calls != 1 {
		t.Fatalf("a real Forbidden in an Ichor upgrade follow: err = %v, calls = %d", err, ops.calls)
	}
}

// A denial that never clears (RBAC really missing) ends the follow once the window is spent.
func TestIchorUpgradeFollowGivesUpOnAnImpersonationDenialThatStays(t *testing.T) {
	shrinkFollowTimers(t)
	setAPITestFlags(t, true)

	followRetryWindow = 50 * time.Millisecond

	errs := make([]error, 100000)
	for i := range errs {
		errs[i] = errImpersonate
	}

	ops := &flakyOps{errs: errs}
	if _, err := watchKind(ops, pb.OperationKind_OPERATION_KIND_ICHOR); err == nil || !strings.Contains(err.Error(), "impersonate") {
		t.Fatalf("err = %v", err)
	}
}
