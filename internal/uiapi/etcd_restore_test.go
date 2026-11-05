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
	"time"

	"connectrpc.com/connect"
	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// Etcd restore over a fake client: what the plan says, what is refused before
// anything is written, and what the TalosOperation looks like.

const (
	restoreBackup  = "nightly"
	restoreSnap    = "etcd-20261001T020000Z.snapshot.age"
	restoreOlder   = "etcd-20260930T020000Z.snapshot.age"
	restoreCP2Node = "node-cp2"
)

var restoreSnapTime = time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC)

func restoreBackupObject() *ichorv1alpha1.TalosEtcdBackup {
	return &ichorv1alpha1.TalosEtcdBackup{
		Name: restoreBackup, Namespace: opNS,
		Spec: ichorv1alpha1.TalosEtcdBackupSpec{ClusterRef: corev1.LocalObjectReference{Name: opCluster}, Schedule: "0 2 * * *"},
		Status: ichorv1alpha1.TalosEtcdBackupStatus{
			Snapshots: []ichorv1alpha1.TalosEtcdBackupStored{
				{Name: restoreSnap, Time: metav1.NewTime(restoreSnapTime), Size: 4096},
				{Name: restoreOlder, Time: metav1.NewTime(restoreSnapTime.Add(-24 * time.Hour)), Size: 4000},
			},
			Conditions: []metav1.Condition{{
				Type: ichorv1alpha1.TalosEtcdBackupConditionReady, Status: metav1.ConditionTrue, Reason: ichorv1alpha1.TalosEtcdBackupReasonSucceeded,
			}},
		},
	}
}

func restoreObjects() []client.Object {
	return append(opObjects(), restoreBackupObject(), opMachine("cp2", "controlplane", nil, nil), opMachine("cp3", "controlplane", nil, nil))
}

func restoreReq(snapshot, confirm, reason string) *pb.EtcdRestoreRequest {
	r := &pb.EtcdRestoreRequest{Backup: objectRef(opNS, restoreBackup), Snapshot: snapshot, Confirm: confirm}
	if reason != "" {
		r.Override = &pb.Override{Reason: reason}
	}

	return r
}

func restorePlanReq(r *pb.EtcdRestoreRequest) *connect.Request[pb.PlanOperationRequest] {
	return connect.NewRequest(&pb.PlanOperationRequest{
		Cluster: objectRef(opNS, opCluster), Kind: pb.OperationKind_OPERATION_KIND_ETCD_RESTORE, EtcdRestore: r,
	})
}

func restoreCreateReq(r *pb.EtcdRestoreRequest) *connect.Request[pb.CreateOperationRequest] {
	return connect.NewRequest(&pb.CreateOperationRequest{
		Cluster: objectRef(opNS, opCluster), Kind: pb.OperationKind_OPERATION_KIND_ETCD_RESTORE, EtcdRestore: r,
	})
}

func TestEtcdRestorePlanSaysWhatIsLost(t *testing.T) {
	s, _ := opServer(t, restoreObjects()...)

	res, err := s.PlanOperation(context.Background(), restorePlanReq(restoreReq(restoreSnap, "", "")))
	if err != nil {
		t.Fatal(err)
	}

	p := res.Msg
	if p.GetBlocked() != "" {
		t.Fatalf("blocked: %q", p.GetBlocked())
	}

	if p.GetConfirmName() != opCluster || !p.GetOverrideRequired() || !p.GetKubernetesApiUnavailable() {
		t.Fatalf("plan = %+v", p)
	}

	if got := p.GetSnapshotTime().AsTime(); !got.Equal(restoreSnapTime) {
		t.Fatalf("snapshot time = %v", got)
	}

	w := p.GetWarning()
	for _, want := range []string{"2026-10-01 02:00:00 UTC", "Everything written since is lost", "Kubernetes API is unavailable", "reconciles against the restored"} {
		if !strings.Contains(w, want) {
			t.Errorf("warning does not say %q: %s", want, w)
		}
	}

	if len(p.GetSteps()) != 6 || !strings.Contains(p.GetSteps()[2], "cp1, cp2, cp3") {
		t.Fatalf("steps = %q", p.GetSteps())
	}

	if len(p.GetMachines()) != 3 {
		t.Fatalf("machines = %+v", p.GetMachines())
	}
}

// The controller pod is found by its control-plane label alone: the chart's app.kubernetes.io/name
// follows nameOverride, so a pod named for anything else still says where the controller runs.
func TestEtcdRestorePlanNamesTheControllersNodeWhateverTheAppNameIs(t *testing.T) {
	for _, appName := range []string{"ichor", "renamed-by-name-override", ""} {
		cp2 := opMachine("cp2", "controlplane", nil, nil)
		cp2.Spec.Hostname = restoreCP2Node

		labels := map[string]string{testControllerLabelKey: testControllerLabelVal}
		if appName != "" {
			labels["app.kubernetes.io/name"] = appName
		}

		pod := &corev1.Pod{
			Name: testControllerPodName, Namespace: opNS, Labels: labels,
			Spec: corev1.PodSpec{NodeName: restoreCP2Node},
		}
		node := &corev1.Node{Name: restoreCP2Node}

		objs := append(opObjects(), restoreBackupObject(), cp2, opMachine("cp3", "controlplane", nil, nil), node, pod)
		s, _ := opServer(t, objs...)
		s.ControllerNamespace = opNS

		res, err := s.PlanOperation(context.Background(), restorePlanReq(restoreReq(restoreSnap, "", "")))
		if err != nil {
			t.Fatal(err)
		}

		if got := res.Msg.GetSteps()[2]; !strings.Contains(got, "then cp2 (where this controller runs)") {
			t.Fatalf("app name %q: wipe step = %q", appName, got)
		}
	}
}

func TestEtcdRestorePlanRefusals(t *testing.T) {
	ctx := context.Background()

	otherCluster := restoreBackupObject()
	otherCluster.Spec.ClusterRef.Name = "elsewhere"

	finished := metav1.NewTime(restoreSnapTime.Add(time.Hour))
	opOf := func(name, kind string, mutate func(*ichorv1alpha1.TalosOperation)) *ichorv1alpha1.TalosOperation {
		op := &ichorv1alpha1.TalosOperation{
			Name: name, Namespace: opNS, CreationTimestamp: metav1.NewTime(restoreSnapTime.Add(time.Hour)),
			Spec: ichorv1alpha1.TalosOperationSpec{ClusterRef: corev1.LocalObjectReference{Name: opCluster}, Kind: kind},
		}
		mutate(op)

		return op
	}

	type refusal struct {
		name, snapshot, want string
		objs                 []client.Object
		createRefused        bool // override does not lift it: starting the restore is refused as well
	}

	inside := func(endpoint string) refusal {
		backup := restoreBackupObject()
		backup.Spec.Destination.S3.Endpoint = endpoint

		return refusal{
			name: "a backup store inside the cluster: " + endpoint, snapshot: restoreSnap, createRefused: true,
			objs: append(opObjects(), backup, opMachine("cp2", "controlplane", nil, nil)),
			want: "the backup store must live outside the cluster being restored",
		}
	}

	tests := []refusal{
		{name: "a snapshot the backup does not list", snapshot: "etcd-20200101T000000Z.snapshot.age", objs: restoreObjects(), want: "not among the snapshots"},
		{name: "a backup of another cluster", snapshot: restoreSnap, objs: append(opObjects(), otherCluster), want: "backs up cluster"},
		{
			name: "an unfinished operation", snapshot: restoreSnap, want: "not finished",
			objs: append(restoreObjects(), opOf("upgrade", ichorv1alpha1.TalosOperationKindTalos, func(op *ichorv1alpha1.TalosOperation) {
				op.Status.Phase = ichorv1alpha1.TalosOperationPhaseInProgress
			})),
		},
		{
			name: "a key rotation that finished after the snapshot", snapshot: restoreSnap, want: "service-account or secretbox key",
			objs: append(restoreObjects(), opOf("key-rotation", ichorv1alpha1.TalosOperationKindKeyRotation, func(op *ichorv1alpha1.TalosOperation) {
				op.Spec.KeyRotation = &ichorv1alpha1.TalosOperationKeyRotationSpec{Targets: []string{ichorv1alpha1.KeyRotationTargetSecretbox}}
				op.Status = ichorv1alpha1.TalosOperationStatus{Phase: ichorv1alpha1.TalosOperationPhaseCompleted, FinishedAt: &finished}
			})),
		},
		{
			name: "a CA rotation after the snapshot", snapshot: restoreSnap, want: "rotated the cluster's CAs",
			objs: append(restoreObjects(), opOf("rotation", ichorv1alpha1.TalosOperationKindCARotation, func(op *ichorv1alpha1.TalosOperation) {
				op.Status.Phase = ichorv1alpha1.TalosOperationPhaseCompleted
			})),
		},
		inside("http://minio.minio.svc:9000"),
		inside("http://minio.minio.svc.cluster.local:9000"),
		inside("http://10.96.4.2:9000"),
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := opServer(t, tc.objs...)

			res, err := s.PlanOperation(ctx, restorePlanReq(restoreReq(tc.snapshot, "", "")))
			if err != nil || !strings.Contains(res.Msg.GetBlocked(), tc.want) {
				t.Fatalf("blocked = %q, %v", res.Msg.GetBlocked(), err)
			}

			if tc.createRefused {
				if _, err := s.CreateOperation(ctx, restoreCreateReq(restoreReq(restoreSnap, opCluster, bgReason))); connect.CodeOf(err) != connect.CodeFailedPrecondition {
					t.Fatalf("create: %v", err)
				}
			}
		})
	}

	t.Run("no backup or snapshot named", func(t *testing.T) {
		s, _ := opServer(t, restoreObjects()...)

		if _, err := s.PlanOperation(ctx, restorePlanReq(&pb.EtcdRestoreRequest{})); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("empty request: %v", err)
		}

		if _, err := s.PlanOperation(ctx, restorePlanReq(nil)); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("no request: %v", err)
		}
	})
}

// An override with no reason is allowed: the CRD's reason, which the restore records, says no reason was
// given.
func TestEtcdRestoreOverrideWithoutAReason(t *testing.T) {
	s, k8s := opServer(t, restoreObjects()...)
	var asked []authv1.ResourceAttributes
	s.Impersonate = func(Identity) (client.Client, error) { return reviewing(k8s, true, &asked), nil }
	user := context.WithValue(context.Background(), identityKey{}, Identity{User: bgUser})

	req := restoreReq(restoreSnap, opCluster, "")
	req.Override = &pb.Override{}

	res, err := s.CreateOperation(user, restoreCreateReq(req))
	if err != nil {
		t.Fatalf("override without a reason: %v", err)
	}

	if a := res.Msg.GetAudit(); a.GetAction() != createOverrideAction || a.GetReason() != noReasonGiven {
		t.Fatalf("audit = %+v", a)
	}

	ops := listOps(t, k8s)
	if len(ops) != 1 || ops[0].Spec.EtcdRestore == nil || ops[0].Spec.EtcdRestore.Reason != noReasonGiven {
		t.Fatalf("operations = %+v", ops)
	}
}

func TestEtcdRestoreNeedsTheClusterNameAndOverride(t *testing.T) {
	s, k8s := opServer(t, restoreObjects()...)
	bg := context.Background()

	if _, err := s.CreateOperation(bg, restoreCreateReq(restoreReq(restoreSnap, "not-the-name", bgReason))); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("wrong name: %v", err)
	}

	if _, err := s.CreateOperation(bg, restoreCreateReq(restoreReq(restoreSnap, opCluster, ""))); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("no override: %v", err)
	}

	// With no signed-in user nobody can be asked whether they may override.
	if _, err := s.CreateOperation(bg, restoreCreateReq(restoreReq(restoreSnap, opCluster, bgReason))); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("no user: %v", err)
	}

	var asked []authv1.ResourceAttributes

	s.Impersonate = func(Identity) (client.Client, error) { return reviewing(k8s, false, &asked), nil }
	user := context.WithValue(bg, identityKey{}, Identity{User: bgUser})

	if _, err := s.CreateOperation(user, restoreCreateReq(restoreReq(restoreSnap, opCluster, bgReason))); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("RBAC says no: %v", err)
	}

	// The start right is asked first, and denied here, so override is not reached.
	if len(asked) == 0 || asked[0].Verb != "start" || asked[0].Subresource != "etcd-restore" {
		t.Fatalf("start is asked first: %+v", asked)
	}

	if ops := listOps(t, k8s); len(ops) != 0 {
		t.Fatalf("nothing may be written: %+v", ops)
	}

	s.Impersonate = func(Identity) (client.Client, error) { return reviewing(k8s, true, &asked), nil }

	res, err := s.CreateOperation(user, restoreCreateReq(restoreReq(restoreSnap, opCluster, bgReason)))
	if err != nil {
		t.Fatalf("allowed: %v", err)
	}

	if a := res.Msg.GetAudit(); a.GetAction() != createOverrideAction || a.GetReason() != bgReason {
		t.Fatalf("audit = %+v", a)
	}

	ops := listOps(t, k8s)
	if len(ops) != 1 || ops[0].Spec.Kind != ichorv1alpha1.TalosOperationKindEtcdRestore {
		t.Fatalf("operations = %+v", ops)
	}

	e := ops[0].Spec.EtcdRestore
	if e == nil || e.BackupRef.Name != restoreBackup || e.Snapshot != restoreSnap || e.Confirm != opCluster || e.Reason != bgReason {
		t.Fatalf("spec.etcdRestore = %+v", e)
	}

	if got := res.Msg.GetOperation(); got.GetKind() != pb.OperationKind_OPERATION_KIND_ETCD_RESTORE ||
		got.GetEtcdRestoreSnapshot() != restoreSnap || got.GetEtcdRestoreBackup().GetName() != restoreBackup {
		t.Fatalf("response = %+v", got)
	}

	// A second restore is refused while the first has not finished.
	if _, err := s.CreateOperation(user, restoreCreateReq(restoreReq(restoreOlder, opCluster, bgReason))); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("second restore: %v", err)
	}
}

func TestEtcdRestoreRefusedPlanWritesNothing(t *testing.T) {
	s, k8s := opServer(t, restoreObjects()...)

	if _, err := s.CreateOperation(context.Background(), restoreCreateReq(restoreReq("etcd-20200101T000000Z.snapshot.age", opCluster, bgReason))); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("unlisted snapshot: %v", err)
	}

	if ops := listOps(t, k8s); len(ops) != 0 {
		t.Fatalf("nothing may be written: %+v", ops)
	}
}

func TestListEtcdBackupsOffersTheSnapshotsOfTheCluster(t *testing.T) {
	other := restoreBackupObject()
	other.Name, other.Spec.ClusterRef.Name = "someone-elses", "elsewhere"

	s, _ := opServer(t, append(restoreObjects(), other)...)

	res, err := s.ListEtcdBackups(context.Background(), connect.NewRequest(&pb.ListEtcdBackupsRequest{Cluster: objectRef(opNS, opCluster)}))
	if err != nil {
		t.Fatal(err)
	}

	if len(res.Msg.GetBackups()) != 1 {
		t.Fatalf("backups = %+v", res.Msg.GetBackups())
	}

	b := res.Msg.GetBackups()[0]
	if b.GetRef().GetName() != restoreBackup || !b.GetReady() || b.GetSchedule() != "0 2 * * *" || len(b.GetSnapshots()) != 2 {
		t.Fatalf("backup = %+v", b)
	}

	if b.GetSnapshots()[0].GetName() != restoreSnap || b.GetSnapshots()[0].GetSize() != 4096 || !b.GetSnapshots()[0].GetTime().AsTime().Equal(restoreSnapTime) {
		t.Fatalf("snapshots = %+v", b.GetSnapshots())
	}

	if _, err := s.ListEtcdBackups(context.Background(), connect.NewRequest(&pb.ListEtcdBackupsRequest{})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("no cluster: %v", err)
	}
}

func TestAnEtcdRestoreThatStartedCannotBePaused(t *testing.T) {
	running := &ichorv1alpha1.TalosOperation{
		Name: "etcd-restore-x", Namespace: opNS,
		Spec: ichorv1alpha1.TalosOperationSpec{
			ClusterRef: corev1.LocalObjectReference{Name: opCluster}, Kind: ichorv1alpha1.TalosOperationKindEtcdRestore,
			EtcdRestore: &ichorv1alpha1.TalosOperationEtcdRestoreSpec{BackupRef: corev1.LocalObjectReference{Name: restoreBackup}, Snapshot: restoreSnap},
		},
		Status: ichorv1alpha1.TalosOperationStatus{
			Phase: ichorv1alpha1.TalosOperationPhaseInProgress,
			EtcdRestore: &ichorv1alpha1.TalosOperationEtcdRestoreStatus{
				Step: ichorv1alpha1.EtcdRestoreStepWiping, Message: "wiping", SnapshotTime: &metav1.Time{Time: restoreSnapTime},
			},
		},
	}
	s, _ := opServer(t, append(restoreObjects(), running)...)

	_, err := s.SetOperationPaused(context.Background(), connect.NewRequest(&pb.SetOperationPausedRequest{Operation: objectRef(opNS, "etcd-restore-x"), Paused: true}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("pause: %v", err)
	}

	got, err := s.GetOperation(context.Background(), connect.NewRequest(&pb.GetOperationRequest{Operation: objectRef(opNS, "etcd-restore-x")}))
	if err != nil {
		t.Fatal(err)
	}

	op := got.Msg.GetOperation()
	if op.GetEtcdRestoreStep() != ichorv1alpha1.EtcdRestoreStepWiping || op.GetEtcdRestoreMessage() != "wiping" ||
		!op.GetEtcdRestoreSnapshotTime().AsTime().Equal(restoreSnapTime) || op.GetKind() != pb.OperationKind_OPERATION_KIND_ETCD_RESTORE {
		t.Fatalf("operation = %+v", op)
	}
}

// A controller that does not run in ControllerNamespace (the in-process server of a CLI has no
// POD_NAMESPACE) is found in the cluster's namespace, and a node that moved is matched by its observed address.
func TestEtcdRestorePlanFindsTheControllerWithoutItsNamespaceOrAMatchingSpecEndpoint(t *testing.T) {
	cp2 := opMachine("cp2", "controlplane", nil, nil)
	cp2.Spec.Endpoint = "10.9.9.9:50000"
	cp2.Status.ObservedEndpoint, cp2.Status.ObservedEndpointFor = "10.0.0.22:50000", cp2.Spec.Endpoint

	pod := &corev1.Pod{
		Name: testControllerPodName, Namespace: opNS, Labels: map[string]string{testControllerLabelKey: testControllerLabelVal},
		Spec: corev1.PodSpec{NodeName: "some-node"},
	}
	node := &corev1.Node{Name: "some-node", Status: corev1.NodeStatus{Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "10.0.0.22"}}}}

	objs := append(opObjects(), restoreBackupObject(), cp2, opMachine("cp3", "controlplane", nil, nil), node, pod)
	s, _ := opServer(t, objs...)
	s.ControllerNamespace = ""

	res, err := s.PlanOperation(context.Background(), restorePlanReq(restoreReq(restoreSnap, "", "")))
	if err != nil {
		t.Fatal(err)
	}

	if got := res.Msg.GetSteps()[2]; !strings.Contains(got, "then cp2 (where this controller runs)") {
		t.Fatalf("wipe step = %q", got)
	}
}

func TestRequestEtcdBackupSetsTheAnnotationAndRefusesWhatCannotRun(t *testing.T) {
	get := func(k8s client.Client) *ichorv1alpha1.TalosEtcdBackup {
		var bk ichorv1alpha1.TalosEtcdBackup
		if err := k8s.Get(context.Background(), types.NamespacedName{Namespace: opNS, Name: restoreBackup}, &bk); err != nil {
			t.Fatal(err)
		}

		return &bk
	}
	call := func(s *Server) (*connect.Response[pb.RequestEtcdBackupResponse], error) {
		return s.RequestEtcdBackup(context.Background(), connect.NewRequest(&pb.RequestEtcdBackupRequest{Backup: objectRef(opNS, restoreBackup)}))
	}

	s, k8s := opServer(t, restoreBackupObject())

	res, err := call(s)
	if err != nil {
		t.Fatal(err)
	}

	at, err := time.Parse(time.RFC3339, get(k8s).Annotations[ichorv1alpha1.BackupNowAnnotation])
	if err != nil || time.Since(at) > time.Minute {
		t.Fatalf("annotation = %v, %v", at, err)
	}

	if !res.Msg.GetBackup().GetManualBackupPending() || res.Msg.GetAudit().GetAction() != "backup-now" {
		t.Fatalf("response = %+v", res.Msg)
	}

	if _, err := call(s); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("a request still waiting: %v", err)
	}

	// Once the controller acted on it, the next request is newer than lastManualBackup.
	bk := get(k8s)
	last := metav1.NewTime(at.Add(time.Hour))
	bk.Status.LastManualBackup = &last

	if err := k8s.Update(context.Background(), bk); err != nil {
		t.Fatal(err)
	}

	if _, err := call(s); err != nil {
		t.Fatal(err)
	}

	if next, _ := time.Parse(time.RFC3339, get(k8s).Annotations[ichorv1alpha1.BackupNowAnnotation]); !next.After(last.Time) {
		t.Fatalf("next = %v, not after %v", next, last)
	}

	susp := restoreBackupObject()
	susp.Spec.Suspend = true

	s, _ = opServer(t, susp)
	if _, err := call(s); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("suspended: %v", err)
	}

	if _, err := s.RequestEtcdBackup(context.Background(), connect.NewRequest(&pb.RequestEtcdBackupRequest{})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("no backup: %v", err)
	}
}
