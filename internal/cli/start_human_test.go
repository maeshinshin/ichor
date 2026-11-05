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
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// The commands that start an operation (etcd defrag and forfeit-leadership, cni kube-proxy, config
// apply, restore --live) print for a person when -o is unset, with and without --follow, and the
// wire message on -o yaml. They share runNodeAction, runStartOperation and
// watchOperation, so these tests pin that each one really goes through them.

// freshClients is the in-process server over a new fake cluster of objs.
func freshClients(t *testing.T, objs []client.Object) (*apiClients, client.Client) {
	t.Helper()

	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(objs...).Build()

	return localClients(apiOpts, apiDeps{K8s: k8s}), k8s
}

// completingClients is freshClients whose operations finish the moment they are created, so a
// --follow ends on its first poll.
func completingClients(t *testing.T, objs []client.Object) *apiClients {
	t.Helper()

	setWatchInterval(t, time.Millisecond)

	useWatchClock(t)

	base := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(objs...).
		WithStatusSubresource(&ichorv1alpha1.TalosOperation{}).Build()
	k8s := interceptor.NewClient(reviewAs(true, base), interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if err := c.Create(ctx, obj, opts...); err != nil {
				return err
			}

			if op, ok := obj.(*ichorv1alpha1.TalosOperation); ok && op.Name != "" { // a dry-run create names nothing
				op.Status.Phase = ichorv1alpha1.TalosOperationPhaseCompleted

				return c.Status().Update(ctx, op)
			}

			return nil
		},
	})

	return localClients(apiOpts, apiDeps{K8s: k8s})
}

// startedBlock is what the human output of a started operation shows, whatever its generated name.
func startedBlock(t *testing.T, out string, ops []ichorv1alpha1.TalosOperation, kind string) {
	t.Helper()

	if len(ops) != 1 {
		t.Fatalf("operations = %+v", ops)
	}

	name := ops[0].Name
	if !strings.HasPrefix(out, "Started "+name+" · default\n") || !strings.Contains(out, "  Kind         "+kind+"\n") ||
		!strings.Contains(out, "  Follow       ichor operation get "+name+" --watch\n") || strings.Contains(out, "operation:") {
		t.Errorf("human stdout:\n%s", out)
	}
}

func TestEtcdNodeActionsPrintHumanOutputAndTheWireMessageOnYAML(t *testing.T) {
	for _, action := range []pb.NodeAction{pb.NodeAction_NODE_ACTION_ETCD_DEFRAG, pb.NodeAction_NODE_ACTION_ETCD_FORFEIT_LEADERSHIP} {
		t.Run(action.String(), func(t *testing.T) {
			c, k8s := nodeActionClients(t, true)
			outputFormat = outputHuman

			fixedHumanClock(t)

			spec := nodeActionSpec{Machine: opCP, Action: action, AllowLeader: action == pb.NodeAction_NODE_ACTION_ETCD_DEFRAG}

			var out, errw bytes.Buffer

			if err := runNodeAction(context.Background(), c, strings.NewReader(""), &out, &errw, spec); err != nil {
				t.Fatal(err)
			}

			startedBlock(t, out.String(), opTestOps(t, k8s), "node")

			c, _ = nodeActionClients(t, true)
			outputFormat = outputYAML

			out.Reset()

			if err := runNodeAction(context.Background(), c, strings.NewReader(""), &out, &errw, spec); err != nil {
				t.Fatal(err)
			}

			if !strings.Contains(out.String(), "operation:\n") || strings.Contains(out.String(), "Started") {
				t.Errorf("-o yaml stdout:\n%s", out.String())
			}
		})
	}
}

func TestEtcdNodeActionFollowUsesTheSharedWatch(t *testing.T) {
	setAPITestFlags(t, true)

	outputFormat = outputHuman

	fixedHumanClock(t)

	c := completingClients(t, opTestObjects())

	var out, errw bytes.Buffer

	if err := runNodeAction(context.Background(), c, strings.NewReader(""), &out, &errw,
		nodeActionSpec{Machine: opCP, Action: pb.NodeAction_NODE_ACTION_ETCD_DEFRAG, AllowLeader: true, Follow: true}); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(errw.String(), "created operation") || !strings.HasSuffix(errw.String(), " — Completed\n") ||
		!strings.Contains(out.String(), "  Phase        ✓ Completed\n") {
		t.Errorf("stderr:\n%s\nstdout:\n%s", errw.String(), out.String())
	}
}

func TestCniKubeProxyAndConfigApplyPrintHumanOutputAndTheWireMessageOnYAML(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec startSpec
	}{
		{"cni kube-proxy", startSpec{Kind: pb.OperationKind_OPERATION_KIND_CNI, Cluster: opTestCluster, Target: kubeProxyReplaced}},
		{"config apply", startSpec{Kind: pb.OperationKind_OPERATION_KIND_CONFIG, Cluster: opTestCluster}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setAPITestFlags(t, true)

			outputFormat = outputHuman

			fixedHumanClock(t)

			c, k8s := freshClients(t, configApplyObjects())

			var out, errw bytes.Buffer

			if err := runStartOperation(context.Background(), c, strings.NewReader(""), &out, &errw, tc.spec); err != nil {
				t.Fatal(err)
			}

			startedBlock(t, out.String(), opTestOps(t, k8s), kindName(tc.spec.Kind))

			c, _ = freshClients(t, configApplyObjects())
			outputFormat = outputYAML

			out.Reset()

			if err := runStartOperation(context.Background(), c, strings.NewReader(""), &out, &errw, tc.spec); err != nil {
				t.Fatal(err)
			}

			if !strings.Contains(out.String(), "operation:\n") || strings.Contains(out.String(), "Started") {
				t.Errorf("-o yaml stdout:\n%s", out.String())
			}
		})
	}
}

func TestStartFollowUsesTheSharedWatch(t *testing.T) {
	for _, spec := range []startSpec{
		{Kind: pb.OperationKind_OPERATION_KIND_CNI, Cluster: opTestCluster, Target: kubeProxyReplaced, Follow: true},
		{Kind: pb.OperationKind_OPERATION_KIND_CONFIG, Cluster: opTestCluster, Follow: true},
	} {
		t.Run(kindName(spec.Kind), func(t *testing.T) {
			setAPITestFlags(t, true)

			outputFormat = outputHuman

			fixedHumanClock(t)

			c := completingClients(t, configApplyObjects())

			var out, errw bytes.Buffer

			if err := runStartOperation(context.Background(), c, strings.NewReader(""), &out, &errw, spec); err != nil {
				t.Fatal(err)
			}

			if strings.Contains(errw.String(), "created operation") || !strings.HasSuffix(errw.String(), " — Completed\n") ||
				!strings.Contains(out.String(), "  Phase        ✓ Completed\n") {
				t.Errorf("stderr:\n%s\nstdout:\n%s", errw.String(), out.String())
			}
		})
	}
}

func TestRestoreLiveFollowUsesTheSharedWatchAndYAMLKeepsTheAnnouncement(t *testing.T) {
	liveClients(t, true, true) // the flags: --override, the reason

	s := liveSpec()
	s.Confirm, s.Follow = opTestCluster, true

	c := completingClients(t, append(opTestObjects(), liveBackupObject(liveBackup)))

	outputFormat = outputHuman

	fixedHumanClock(t)

	var out, errw bytes.Buffer

	if err := runLiveRestore(context.Background(), c, strings.NewReader(""), &out, &errw, s); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(errw.String(), "created operation") || !strings.HasSuffix(errw.String(), " — Completed\n") ||
		!strings.Contains(out.String(), "  Phase        ✓ Completed\n") {
		t.Errorf("human stderr:\n%s\nstdout:\n%s", errw.String(), out.String())
	}

	c = completingClients(t, append(opTestObjects(), liveBackupObject(liveBackup)))
	outputFormat = outputYAML

	out.Reset()
	errw.Reset()

	if err := runLiveRestore(context.Background(), c, strings.NewReader(""), &out, &errw, s); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(errw.String(), "created operation etcd-restore") || !strings.HasPrefix(out.String(), "operation:\n") {
		t.Errorf("-o yaml stderr:\n%s\nstdout:\n%s", errw.String(), out.String())
	}
}
