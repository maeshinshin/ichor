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

	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

const (
	liveBackup = "nightly"
	liveNewest = "etcd-20261001T020000Z.snapshot.age"
	liveOlder  = "etcd-20260930T020000Z.snapshot.age"
	liveReason = "a bad change deleted the namespace"
)

var liveSnapTime = time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC)

func liveBackupObject(name, cluster string) *ichorv1alpha1.TalosEtcdBackup {
	return &ichorv1alpha1.TalosEtcdBackup{
		Name: name, Namespace: testNamespace,
		Spec: ichorv1alpha1.TalosEtcdBackupSpec{ClusterRef: corev1.LocalObjectReference{Name: cluster}, Schedule: "0 2 * * *"},
		Status: ichorv1alpha1.TalosEtcdBackupStatus{Snapshots: []ichorv1alpha1.TalosEtcdBackupStored{
			{Name: liveNewest, Time: metav1.NewTime(liveSnapTime), Size: 4096},
			{Name: liveOlder, Time: metav1.NewTime(liveSnapTime.Add(-24 * time.Hour)), Size: 4000},
		}},
	}
}

// liveClients is the in-process server over a fake cluster whose override review answers allowed.
func liveClients(t *testing.T, yes, allowed bool, extra ...client.Object) (*apiClients, client.Client) {
	t.Helper()

	setAPITestFlags(t, yes)

	apiOpts.Override = liveReason

	objs := append(opTestObjects(), liveBackupObject(liveBackup, opTestCluster))
	base := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(append(objs, extra...)...).Build()
	k8s := interceptor.NewClient(base, interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if sar, ok := obj.(*authv1.SelfSubjectAccessReview); ok {
				sar.Status.Allowed = allowed

				return nil
			}

			return c.Create(ctx, obj, opts...)
		},
	})

	return localClients(apiOpts, apiDeps{K8s: k8s}), k8s
}

func liveSpec() liveRestoreSpec { return liveRestoreSpec{Cluster: opTestCluster} }

func TestRestoreLiveIsRegisteredBesideRecoverCluster(t *testing.T) {
	for _, name := range []string{"live", "backup", "confirm", "follow", flagSnapshot, flagCluster, "recover-cluster"} {
		if restoreCmd.Flags().Lookup(name) == nil {
			t.Errorf("ichor restore has no --%s", name)
		}
	}

	if restoreCmd.PersistentFlags().Lookup("override") == nil {
		t.Error("ichor restore has no --override")
	}
}

func TestChooseSnapshot(t *testing.T) {
	const snapOlder = "snap-older"

	one := &pb.EtcdBackup{Ref: objectRef("ns", "a"), Snapshots: []*pb.EtcdSnapshot{{Name: "snap-new"}, {Name: snapOlder}}}
	two := &pb.EtcdBackup{Ref: objectRef("ns", "b"), Snapshots: []*pb.EtcdSnapshot{{Name: "snap-b"}}}
	empty := &pb.EtcdBackup{Ref: objectRef("ns", "c")}

	cases := []struct {
		name    string
		backups []*pb.EtcdBackup
		spec    liveRestoreSpec
		want    string
		wantErr string
	}{
		{"the only backup, newest snapshot", []*pb.EtcdBackup{one}, liveRestoreSpec{}, "snap-new", ""},
		{"a named snapshot", []*pb.EtcdBackup{one}, liveRestoreSpec{Snapshot: snapOlder}, snapOlder, ""},
		{"a snapshot the backup does not list", []*pb.EtcdBackup{one}, liveRestoreSpec{Snapshot: "gone"}, "", "does not list snapshot \"gone\""},
		{"several backups need a choice", []*pb.EtcdBackup{one, two}, liveRestoreSpec{}, "", "choose one with --backup"},
		{"the named one of several", []*pb.EtcdBackup{one, two}, liveRestoreSpec{Backup: "b"}, "snap-b", ""},
		{"a backup that does not exist", []*pb.EtcdBackup{one}, liveRestoreSpec{Backup: "zzz"}, "", "no TalosEtcdBackup named"},
		{"no backup at all", nil, liveRestoreSpec{}, "", "nothing to restore from"},
		{"a backup with no snapshot yet", []*pb.EtcdBackup{empty}, liveRestoreSpec{}, "", "lists no snapshot"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, snap, err := chooseSnapshot(tc.backups, tc.spec)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
				}

				return
			}

			if err != nil || snap.GetName() != tc.want {
				t.Fatalf("snapshot = %v, %v; want %q", snap, err, tc.want)
			}
		})
	}
}

func TestRestoreLiveShowsThePlanAndAsksForTheClusterName(t *testing.T) {
	c, k8s := liveClients(t, false, true)

	var prompt bytes.Buffer

	// A wrong name stops it, and nothing is written.
	err := runLiveRestore(context.Background(), c, strings.NewReader("wrong\n"), &bytes.Buffer{}, &prompt, liveSpec())
	if err == nil || !strings.Contains(err.Error(), "did not match") {
		t.Fatalf("wrong name = %v", err)
	}

	for _, want := range []string{"RESTORE cluster \"" + opTestCluster + "\"", liveNewest, "2026-10-01T02:00:00Z", "Wipe etcd data", "Everything written since is lost", "Type the cluster name"} {
		if !strings.Contains(prompt.String(), want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt.String())
		}
	}

	if ops := opTestOps(t, k8s); len(ops) != 0 {
		t.Fatalf("a refused confirmation wrote %+v", ops)
	}

	// The right name starts it, with the newest snapshot and the reason.
	var out bytes.Buffer

	if err := runLiveRestore(context.Background(), c, strings.NewReader(opTestCluster+"\n"), &out, &bytes.Buffer{}, liveSpec()); err != nil {
		t.Fatal(err)
	}

	ops := opTestOps(t, k8s)
	if len(ops) != 1 || ops[0].Spec.Kind != ichorv1alpha1.TalosOperationKindEtcdRestore {
		t.Fatalf("operations = %+v", ops)
	}

	e := ops[0].Spec.EtcdRestore
	if e == nil || e.BackupRef.Name != liveBackup || e.Snapshot != liveNewest || e.Confirm != opTestCluster || e.Reason != liveReason {
		t.Fatalf("spec.etcdRestore = %+v", e)
	}

	if !strings.Contains(out.String(), "create-override") {
		t.Fatalf("the audit record is printed: %s", out.String())
	}
}

func TestRestoreLiveYesStillNeedsTheClusterName(t *testing.T) {
	c, k8s := liveClients(t, true, true)

	err := runLiveRestore(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, liveSpec())
	if err == nil || !strings.Contains(err.Error(), "--confirm") {
		t.Fatalf("--yes alone must not restore: %v", err)
	}

	if ops := opTestOps(t, k8s); len(ops) != 0 {
		t.Fatalf("wrote %+v", ops)
	}

	var errw bytes.Buffer

	s := liveSpec()
	s.Confirm, s.Snapshot = opTestCluster, liveOlder

	if err := runLiveRestore(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &errw, s); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(errw.String(), "RESTORE cluster") || strings.Contains(errw.String(), "Type the cluster name") {
		t.Fatalf("--confirm prints the plan and does not ask:\n%s", errw.String())
	}

	if ops := opTestOps(t, k8s); len(ops) != 1 || ops[0].Spec.EtcdRestore.Snapshot != liveOlder {
		t.Fatalf("operations = %+v", ops)
	}
}

func TestRestoreLiveNeedsAnOverrideReasonAndTheRight(t *testing.T) {
	c, k8s := liveClients(t, true, true)
	apiOpts.Override = ""

	s := liveSpec()
	s.Confirm = opTestCluster

	err := runLiveRestore(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, s)
	if err == nil || !strings.Contains(err.Error(), "--override") {
		t.Fatalf("no reason = %v", err)
	}

	denied, k8sDenied := liveClients(t, true, false)

	err = runLiveRestore(context.Background(), denied, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, s)
	// The start right (ADR 0094) is asked before override, and the same answer denies both.
	if err == nil || !strings.Contains(err.Error(), "RBAC does not grant") {
		t.Fatalf("RBAC says no = %v", err)
	}

	if len(opTestOps(t, k8s)) != 0 || len(opTestOps(t, k8sDenied)) != 0 {
		t.Fatal("nothing may be written without the right")
	}
}

func TestRestoreLiveRefusesWhatThePlanBlocks(t *testing.T) {
	c, k8s := liveClients(t, true, true)

	s := liveSpec()
	s.Confirm, s.Snapshot = opTestCluster, "etcd-20200101T000000Z.snapshot.age"

	// The CLI checks the snapshot against the backup's list before it plans.
	if err := runLiveRestore(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, s); err == nil ||
		!strings.Contains(err.Error(), "does not list snapshot") {
		t.Fatalf("unlisted snapshot = %v", err)
	}

	// An unfinished operation blocks it in the plan.
	busy := &ichorv1alpha1.TalosOperation{
		Name: "busy", Namespace: testNamespace,
		Spec:   ichorv1alpha1.TalosOperationSpec{ClusterRef: corev1.LocalObjectReference{Name: opTestCluster}, Kind: ichorv1alpha1.TalosOperationKindTalos},
		Status: ichorv1alpha1.TalosOperationStatus{Phase: ichorv1alpha1.TalosOperationPhaseInProgress},
	}
	c2, k8s2 := liveClients(t, true, true, busy)

	s.Snapshot = ""
	if err := runLiveRestore(context.Background(), c2, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, s); err == nil ||
		!strings.Contains(err.Error(), "not finished") {
		t.Fatalf("busy cluster = %v", err)
	}

	if len(opTestOps(t, k8s)) != 0 || len(opTestOps(t, k8s2)) != 1 {
		t.Fatal("a blocked restore writes nothing")
	}
}

func TestRestoreLiveRefusesABackupStoreInsideTheCluster(t *testing.T) {
	for _, endpoint := range []string{"http://minio.minio.svc:9000", "http://minio.minio.svc.cluster.local:9000", "http://10.96.7.7:9000"} {
		inside := liveBackupObject("inside", opTestCluster)
		inside.Spec.Destination.S3.Endpoint = endpoint

		c, k8s := liveClients(t, true, true, inside)

		s := liveSpec()
		s.Backup, s.Confirm = "inside", opTestCluster

		// --override is given (liveClients sets a reason) and does not lift it.
		err := runLiveRestore(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, s)
		if err == nil || !strings.Contains(err.Error(), "the backup store must live outside the cluster being restored") {
			t.Fatalf("%s: %v", endpoint, err)
		}

		if len(opTestOps(t, k8s)) != 0 {
			t.Fatalf("%s: a blocked restore writes nothing", endpoint)
		}
	}
}

func TestRestoreRequiresAMode(t *testing.T) {
	old := restoreLiveOpts
	t.Cleanup(func() { restoreLiveOpts = old })

	restoreLiveOpts.Live = false
	restoreOpts.RecoverCluster = false

	if err := restoreCmd.RunE(restoreCmd, nil); err == nil || !strings.Contains(err.Error(), "--live") {
		t.Fatalf("no mode = %v", err)
	}

	restoreLiveOpts.Live, restoreOpts.RecoverCluster = true, true
	t.Cleanup(func() { restoreOpts.RecoverCluster = false })

	if err := restoreCmd.RunE(restoreCmd, nil); err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("both modes = %v", err)
	}
}

func TestProgressLineShowsTheRestoreStep(t *testing.T) {
	op := &pb.Operation{
		Ref: objectRef("ns", "etcd-restore-x"), Kind: pb.OperationKind_OPERATION_KIND_ETCD_RESTORE, Phase: ichorv1alpha1.TalosOperationPhaseInProgress,
		EtcdRestoreStep: ichorv1alpha1.EtcdRestoreStepRecovering,
	}

	if line := progressLine(op); !strings.Contains(line, "etcd-restore etcd-restore-x") || !strings.Contains(line, "step Recovering") {
		t.Fatalf("line = %q", line)
	}
}

// The in-process server (what --override forces) names the controller's control plane in the plan, as
// ichor-ui does: it looks the pod up in --ichor-namespace.
func TestRestoreLivePlanNamesTheControllersNodeInLocalMode(t *testing.T) {
	const ownNode = "node-cp2"

	cp2 := newTestMachine(opTestCluster)
	cp2.Name, cp2.Spec.Role, cp2.Spec.Hostname = "cp2", roleControlPlane, ownNode

	pod := &corev1.Pod{
		Name: "ichor-controller", Namespace: ichorControllerNamespace,
		Labels: map[string]string{"control-plane": "controller-manager"},
		Spec:   corev1.PodSpec{NodeName: ownNode},
	}

	c, _ := liveClients(t, false, true, cp2, &corev1.Node{Name: ownNode}, pod)

	var prompt bytes.Buffer

	_ = runLiveRestore(context.Background(), c, strings.NewReader("wrong\n"), &bytes.Buffer{}, &prompt, liveSpec())

	if !strings.Contains(prompt.String(), "then cp2 (where this controller runs)") {
		t.Fatalf("plan does not name the controller's node:\n%s", prompt.String())
	}
}

// A kubeconfig that names one control plane gets the others as failover servers from the machines, with
// the real ListMachines of the in-process server.
func TestEnableFailoverFromTheLocalMachines(t *testing.T) {
	setAPITestFlags(t, false)

	mk := func(name, ep string) *ichorv1alpha1.TalosMachine {
		m := newTestMachine(opTestCluster)
		m.Name, m.Spec.Role, m.Spec.Endpoint = name, roleControlPlane, ep

		return m
	}

	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).
		WithObjects(newTestCluster(), mk("cp1", "10.0.0.1:50000"), mk("cp2", "10.0.0.2:50000"), mk("cp3", "10.0.0.3")).Build()
	c := localClients(apiOpts, apiDeps{K8s: k8s})
	c.Server = cp1URL
	c.Dial = func(_ context.Context, server string) (*apiClients, error) {
		return &apiClients{Server: server, Close: func() {}}, nil
	}

	enableFailover(context.Background(), c, &bytes.Buffer{}, objectRef(testNamespace, opTestCluster))

	if c.Failover == nil {
		t.Fatal("no failover armed")
	}

	name, _, err := c.Failover(context.Background())
	if err != nil || name != cp2URL {
		t.Fatalf("first failover = %q, %v", name, err)
	}
}

// enableFailover's ring, driven through the real retry: the first server refuses, the next answers.
func TestEnableFailoverThenWatchMovesToTheNextServer(t *testing.T) {
	shrinkFollowTimers(t)
	setAPITestFlags(t, false)

	mk := func(name, ep string) *ichorv1alpha1.TalosMachine {
		m := newTestMachine(opTestCluster)
		m.Name, m.Spec.Role, m.Spec.Endpoint = name, roleControlPlane, ep

		return m
	}

	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).
		WithObjects(newTestCluster(), mk("cp1", "10.0.0.1:50000"), mk("cp2", "10.0.0.2:50000")).Build()
	c := localClients(apiOpts, apiDeps{K8s: k8s})
	c.Operation = &serverOps{dead: true}
	c.Server = cp1URL
	c.Dial = func(_ context.Context, server string) (*apiClients, error) {
		return &apiClients{Operation: &serverOps{dead: server == cp1URL}, Server: server, Close: func() {}}, nil
	}

	var errw bytes.Buffer

	enableFailover(context.Background(), c, &errw, objectRef(testNamespace, opTestCluster))

	if err := watchOperation(context.Background(), c, objectRef(testNamespace, "x"), &bytes.Buffer{}, &errw); err != nil {
		t.Fatalf("%v\n%s", err, errw.String())
	}

	if !strings.Contains(errw.String(), "trying API server "+cp2URL) || c.Server != cp2URL {
		t.Fatalf("server %s, stderr %q", c.Server, errw.String())
	}
}
