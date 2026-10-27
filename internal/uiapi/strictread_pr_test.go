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
	"testing"

	"connectrpc.com/connect"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

func TestFindPullRequestsRefusesACallerWhoCannotReadTheApplication(t *testing.T) {
	e := newPREnv(t, prOpts{tracked: true})
	e.host.open[prBranchName("approve", trackedTestNS, "m1")] = prPRURL
	e.impersonateWith(denyApplications(e.k8s))

	_, err := e.srv.FindPullRequests(asUser(context.Background()), findPRsReq(pb.GitOpsKind_GIT_OPS_KIND_MACHINE, "m1"))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("err = %v, want PermissionDenied", err)
	}

	if e.host.gets != 0 {
		t.Fatalf("the Git host was asked %d times for a refused caller", e.host.gets)
	}

	e.impersonateWith(e.k8s)

	res, err := e.srv.FindPullRequests(asUser(context.Background()), findPRsReq(pb.GitOpsKind_GIT_OPS_KIND_MACHINE, "m1"))
	if err != nil || len(res.Msg.GetPullRequests()) != 1 {
		t.Fatalf("a caller who may read the Application gets the pull requests: %v %v", res, err)
	}
}
