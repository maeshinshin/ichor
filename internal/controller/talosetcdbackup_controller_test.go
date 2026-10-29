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

package controller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/robfig/cron/v3"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/etcdbackup"
)

const (
	testS3Bucket   = "test-bucket"
	testBackupBase = "clusters/test"
)

var testBackupNow = time.Date(2026, 6, 1, 12, 30, 0, 0, time.UTC)

// validTestSnapshot has etcd's snapshot shape: 512-byte-aligned data plus its sha256.
func validTestSnapshot(body string) []byte {
	db := []byte(body + strings.Repeat("x", 512-len(body)%512))
	sum := sha256.Sum256(db)

	return append(db, sum[:]...)
}

type fakeEtcdNode struct {
	status     EtcdNodeStatus
	statusErr  error
	snapshot   []byte
	snapshotEr error
	snapshots  int
}

func TestRefreshStaleness(t *testing.T) {
	sched, err := cron.ParseStandard("0 * * * *")
	if err != nil {
		t.Fatal(err)
	}

	r := &TalosEtcdBackupReconciler{}
	success := metav1.NewTime(testBackupNow)
	mk := func() *ichorv1alpha1.TalosEtcdBackup {
		return &ichorv1alpha1.TalosEtcdBackup{Status: ichorv1alpha1.TalosEtcdBackupStatus{LastSuccessTime: &success}}
	}

	fresh := mk()
	r.refreshStaleness(fresh, sched, testBackupNow.Add(90*time.Minute))

	if readyOf(fresh) != nil {
		t.Fatalf("a snapshot 90m old on an hourly schedule is not stale: %+v", readyOf(fresh))
	}

	stale := mk()
	r.refreshStaleness(stale, sched, testBackupNow.Add(4*time.Hour))

	if c := readyOf(stale); c == nil || c.Reason != ichorv1alpha1.TalosEtcdBackupReasonStale {
		t.Fatalf("a snapshot 4h old on an hourly schedule must be stale: %+v", c)
	}
}

// fakeEtcdNodes is an EtcdSnapshotClientFactory keyed by TalosMachine endpoint.
type fakeEtcdNodes struct {
	mu    sync.Mutex
	nodes map[string]*fakeEtcdNode
}

func (f *fakeEtcdNodes) factory(
	_ context.Context, _ *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, _ *secrets.Bundle,
) (EtcdSnapshotClient, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	n, ok := f.nodes[m.Spec.Endpoint]
	if !ok {
		return nil, errors.New("dial refused")
	}

	return &fakeEtcdClient{f: f, n: n}, nil
}

func (f *fakeEtcdNodes) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	t := 0
	for _, n := range f.nodes {
		t += n.snapshots
	}

	return t
}

type fakeEtcdClient struct {
	f *fakeEtcdNodes
	n *fakeEtcdNode
}

func (c *fakeEtcdClient) Close() error { return nil }

func (c *fakeEtcdClient) EtcdStatus(context.Context) (EtcdNodeStatus, error) {
	return c.n.status, c.n.statusErr
}

func (c *fakeEtcdClient) EtcdSnapshot(context.Context) (io.ReadCloser, error) {
	c.f.mu.Lock()
	defer c.f.mu.Unlock()

	c.n.snapshots++

	if c.n.snapshotEr != nil {
		return nil, c.n.snapshotEr
	}

	return io.NopCloser(bytes.NewReader(c.n.snapshot)), nil
}

// memStore is an in-memory etcdbackup.Store.
type memStore struct {
	objects map[string][]byte
	putErr  error
}

func (s *memStore) Put(_ context.Context, key string, r io.Reader, size int64) error {
	if s.putErr != nil {
		return s.putErr
	}

	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	if int64(len(b)) != size {
		return errors.New("size mismatch")
	}

	s.objects[key] = b

	return nil
}

func (s *memStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	b, ok := s.objects[key]
	if !ok {
		return nil, fmt.Errorf("%w: %s", etcdbackup.ErrNotFound, key)
	}

	return io.NopCloser(bytes.NewReader(b)), nil
}

func (s *memStore) Delete(_ context.Context, key string) error {
	delete(s.objects, key)

	return nil
}

func (s *memStore) List(_ context.Context, prefix string) ([]etcdbackup.Object, error) {
	var out []etcdbackup.Object

	for k, v := range s.objects {
		name, ok := strings.CutPrefix(k, prefix+"/")
		if !ok {
			continue
		}

		if t, ok := etcdbackup.ParseObjectName(name); ok {
			out = append(out, etcdbackup.Object{Key: k, Name: name, Time: t, Size: int64(len(v))})
		}
	}

	slices.SortFunc(out, func(a, b etcdbackup.Object) int { return b.Time.Compare(a.Time) })

	return out, nil
}

type backupFixture struct {
	bundle  *secrets.Bundle
	cluster *ichorv1alpha1.TalosCluster
	store   *memStore
	nodes   *fakeEtcdNodes
	cfgSeen *etcdbackup.S3Config
	r       *TalosEtcdBackupReconciler
}

// newBackupFixture creates a bundle Secret and a one-cluster setup in tmNamespace,
// plus the S3 credentials Secret, all in the controller namespace (tmNamespace).
func newBackupFixture() *backupFixture {
	bundle := newTestBundle("v1.14")
	secret := newTestSecret(bundle)
	cluster := newTestCluster(secret.Name, "v1.14")

	Expect(k8sClient.Create(ctx, &corev1.Secret{
		Name: "s3-" + cluster.Name, Namespace: tmNamespace,
		Data: map[string][]byte{
			s3KeyAccessKeyID: []byte("AKIATESTACCESSKEY"), s3KeySecretAccessKey: []byte("test-secret-access-key-value"),
		},
	})).To(Succeed())

	f := &backupFixture{
		bundle: bundle, cluster: cluster,
		store: &memStore{objects: map[string][]byte{}},
		nodes: &fakeEtcdNodes{nodes: map[string]*fakeEtcdNode{}},
	}
	f.r = &TalosEtcdBackupReconciler{
		Client: k8sClient, ControllerNamespace: tmNamespace,
		SnapshotClient: f.nodes.factory,
		SpoolDir:       GinkgoT().TempDir(),
		Now:            func() time.Time { return testBackupNow },
		NewStore: func(c etcdbackup.S3Config) (etcdbackup.Store, error) {
			f.cfgSeen = &c

			return f.store, nil
		},
	}

	return f
}

// addControlPlane creates an approved control plane and registers its fake etcd node.
func (f *backupFixture) addControlPlane(suffix string, memberID uint64) *fakeEtcdNode {
	m := newTestControlPlane(f.cluster.Name, suffix)
	n := &fakeEtcdNode{status: EtcdNodeStatus{MemberID: memberID, LeaderID: 1}, snapshot: validTestSnapshot("etcd-" + suffix)}
	f.nodes.nodes[m.Spec.Endpoint] = n

	return n
}

func (f *backupFixture) newBackup(mutate func(*ichorv1alpha1.TalosEtcdBackup)) *ichorv1alpha1.TalosEtcdBackup {
	bk := &ichorv1alpha1.TalosEtcdBackup{
		GenerateName: "bk-", Namespace: tmNamespace,
		Spec: ichorv1alpha1.TalosEtcdBackupSpec{
			ClusterRef: corev1.LocalObjectReference{Name: f.cluster.Name},
			Destination: ichorv1alpha1.TalosEtcdBackupDestination{S3: ichorv1alpha1.TalosEtcdBackupS3{
				Endpoint: "https://s3.example.test", Bucket: testS3Bucket, Prefix: testBackupBase,
				CredentialsSecretRef: corev1.LocalObjectReference{Name: "s3-" + f.cluster.Name},
			}},
		},
	}
	if mutate != nil {
		mutate(bk)
	}

	Expect(k8sClient.Create(ctx, bk)).To(Succeed())

	return bk
}

func (f *backupFixture) reconcile(bk *ichorv1alpha1.TalosEtcdBackup) (ctrl.Result, *ichorv1alpha1.TalosEtcdBackup) {
	res, err := f.r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(bk)})
	Expect(err).NotTo(HaveOccurred())

	var got ichorv1alpha1.TalosEtcdBackup
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bk), &got)).To(Succeed())

	return res, &got
}

func readyOf(bk *ichorv1alpha1.TalosEtcdBackup) *metav1.Condition {
	return apimeta.FindStatusCondition(bk.Status.Conditions, ichorv1alpha1.TalosEtcdBackupConditionReady)
}

var _ = Describe("TalosEtcdBackupReconciler", func() {
	It("snapshots a follower, stores it encrypted and restorable from the bundle alone, and defaults retention", func() {
		f := newBackupFixture()
		follower := f.addControlPlane("11", 2)
		leader := f.addControlPlane("12", 1)
		bk := f.newBackup(nil)

		Expect(bk.Spec.Retention.KeepLast).To(Equal(int32(24)))
		Expect(bk.Spec.Schedule).To(Equal("0 * * * *"))

		res, got := f.reconcile(bk)

		Expect(follower.snapshots).To(Equal(1))
		Expect(leader.snapshots).To(Equal(0), "the leader must not be read when a healthy follower exists")
		Expect(res.RequeueAfter).To(Equal(30 * time.Minute)) // next full hour after 12:30
		Expect(readyOf(got).Status).To(Equal(metav1.ConditionTrue))
		Expect(got.Status.ConsecutiveFailures).To(BeZero())

		key := testBackupBase + "/" + etcdbackup.ObjectName(testBackupNow)
		Expect(got.Status.LastSnapshot).NotTo(BeNil())
		Expect(got.Status.LastSnapshot.Key).To(Equal(key))
		Expect(got.Status.LastSnapshot.Size).To(Equal(int64(len(f.store.objects[key]))))

		// "Bundle + snapshot is sufficient": only the bundle is used to open it.
		pass, err := etcdbackup.Passphrase(f.bundle)
		Expect(err).NotTo(HaveOccurred())
		plain, err := etcdbackup.Decrypt(bytes.NewReader(f.store.objects[key]), pass)
		Expect(err).NotTo(HaveOccurred())
		body, err := io.ReadAll(plain)
		Expect(err).NotTo(HaveOccurred())
		Expect(body).To(Equal(follower.snapshot))
		Expect(f.store.objects[key]).NotTo(ContainSubstring("etcd-11"), "plaintext must never reach the bucket")
	})

	It("falls back to the leader when it is the only healthy member, and skips unhealthy ones", func() {
		f := newBackupFixture()
		sick := f.addControlPlane("21", 2)
		sick.status.Errors = []string{"NOSPACE"}
		leader := f.addControlPlane("22", 1)

		_, got := f.reconcile(f.newBackup(nil))

		Expect(sick.snapshots).To(BeZero())
		Expect(leader.snapshots).To(Equal(1))
		Expect(readyOf(got).Status).To(Equal(metav1.ConditionTrue))
	})

	It("never uploads a snapshot that fails verification and tries the next control plane", func() {
		f := newBackupFixture()
		bad := f.addControlPlane("31", 2)
		bad.snapshot[5] ^= 0xff // corrupt a database byte; the trailing checksum no longer matches
		good := f.addControlPlane("32", 3)

		_, got := f.reconcile(f.newBackup(nil))

		// Which one is tried first depends on list order; either way the corrupt one
		// is never what ends up stored.
		Expect(good.snapshots).To(Equal(1))
		Expect(bad.snapshots).To(BeNumerically("<=", 1))
		Expect(f.store.objects).To(HaveLen(1))

		pass, err := etcdbackup.Passphrase(f.bundle)
		Expect(err).NotTo(HaveOccurred())
		plain, err := etcdbackup.Decrypt(bytes.NewReader(f.store.objects[got.Status.LastSnapshot.Key]), pass)
		Expect(err).NotTo(HaveOccurred())
		stored, err := io.ReadAll(plain)
		Expect(err).NotTo(HaveOccurred())
		Expect(stored).To(Equal(good.snapshot))
		Expect(got.Status.LastSnapshot.SourceMachine).NotTo(BeEmpty())

		// All corrupt: nothing is stored, the failure is reported, a retry is scheduled.
		good.snapshot[5] ^= 0xff
		g2 := newBackupFixture()
		only := g2.addControlPlane("33", 2)
		only.snapshot[7] ^= 0xff

		res, got2 := g2.reconcile(g2.newBackup(nil))
		Expect(g2.store.objects).To(BeEmpty())
		Expect(readyOf(got2).Status).To(Equal(metav1.ConditionFalse))
		Expect(readyOf(got2).Reason).To(Equal(ichorv1alpha1.TalosEtcdBackupReasonFailed))
		Expect(got2.Status.ConsecutiveFailures).To(Equal(int32(1)))
		Expect(got2.Status.LastSuccessTime).To(BeNil())
		Expect(res.RequeueAfter).To(Equal(backupRetryInterval))
	})

	It("is a no-op until the next scheduled tick, and prunes to retention.keepLast", func() {
		f := newBackupFixture()
		f.addControlPlane("41", 2)

		for i := range 4 {
			old := testBackupNow.Add(-time.Duration(i+2) * time.Hour)
			f.store.objects[testBackupBase+"/"+etcdbackup.ObjectName(old)] = []byte(testOldValue)
		}

		f.store.objects[testBackupBase+"/README.txt"] = []byte("not ours")

		bk := f.newBackup(func(b *ichorv1alpha1.TalosEtcdBackup) { b.Spec.Retention.KeepLast = 2 })
		_, _ = f.reconcile(bk)

		Expect(f.store.List(ctx, testBackupBase)).To(HaveLen(2))
		Expect(f.store.objects).To(HaveKey(testBackupBase + "/README.txt"))
		Expect(f.nodes.total()).To(Equal(1))

		// Same clock: not due, no second snapshot.
		res, _ := f.reconcile(bk)
		Expect(res.RequeueAfter).To(BeNumerically(">", 0))
		Expect(f.nodes.total()).To(Equal(1))

		// Past the next tick: due again.
		f.r.Now = func() time.Time { return testBackupNow.Add(30 * time.Minute) }
		_, got := f.reconcile(bk)
		Expect(f.nodes.total()).To(Equal(2))
		Expect(got.Status.LastSnapshot.Key).To(Equal(testBackupBase + "/" + etcdbackup.ObjectName(testBackupNow.Add(30*time.Minute))))
	})

	It("takes one snapshot outside the schedule for a backup-now request newer than lastManualBackup, and ignores a stale one", func() {
		f := newBackupFixture()
		f.addControlPlane("51", 2)
		bk := f.newBackup(nil)

		_, got := f.reconcile(bk)
		Expect(f.nodes.total()).To(Equal(1))
		Expect(got.Status.LastManualBackup).To(BeNil())

		// Same clock, mid-schedule: nothing is due until a request arrives.
		f.r.Now = func() time.Time { return testBackupNow.Add(5 * time.Minute) }
		_, _ = f.reconcile(bk)
		Expect(f.nodes.total()).To(Equal(1))

		at := testBackupNow.Add(5 * time.Minute)
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bk), bk)).To(Succeed())
		bk.Annotations = map[string]string{ichorv1alpha1.BackupNowAnnotation: at.Format(time.RFC3339)}
		Expect(k8sClient.Update(ctx, bk)).To(Succeed())

		_, got = f.reconcile(bk)
		Expect(f.nodes.total()).To(Equal(2))
		Expect(got.Status.LastManualBackup).NotTo(BeNil())
		Expect(got.Status.LastManualBackup.Unix()).To(Equal(at.Unix()))
		Expect(got.Status.LastSnapshot.Key).To(Equal(testBackupBase + "/" + etcdbackup.ObjectName(at)))

		// The same request again, and an older one, are not acted on.
		_, _ = f.reconcile(bk)
		Expect(f.nodes.total()).To(Equal(2))

		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bk), bk)).To(Succeed())
		bk.Annotations[ichorv1alpha1.BackupNowAnnotation] = at.Add(-time.Minute).Format(time.RFC3339)
		Expect(k8sClient.Update(ctx, bk)).To(Succeed())
		_, _ = f.reconcile(bk)
		Expect(f.nodes.total()).To(Equal(2))
	})

	It("takes nothing while suspended and rejects a bad schedule without retrying", func() {
		f := newBackupFixture()
		f.addControlPlane("51", 2)

		res, got := f.reconcile(f.newBackup(func(b *ichorv1alpha1.TalosEtcdBackup) { b.Spec.Suspend = true }))
		Expect(f.nodes.total()).To(BeZero())
		Expect(readyOf(got).Reason).To(Equal(ichorv1alpha1.TalosEtcdBackupReasonSuspended))
		Expect(res.RequeueAfter).To(BeZero())

		res, got = f.reconcile(f.newBackup(func(b *ichorv1alpha1.TalosEtcdBackup) { b.Spec.Schedule = "every tuesday" }))
		Expect(f.nodes.total()).To(BeZero())
		Expect(readyOf(got).Status).To(Equal(metav1.ConditionFalse))
		Expect(res.RequeueAfter).To(BeZero())
	})

	It("warns with StoreInCluster while the destination is inside the cluster, and keeps backing up", func() {
		f := newBackupFixture()
		f.addControlPlane("61", 2)

		_, got := f.reconcile(f.newBackup(func(b *ichorv1alpha1.TalosEtcdBackup) {
			b.Spec.Destination.S3.Endpoint = "http://minio.minio.svc:9000"
		}))

		cond := apimeta.FindStatusCondition(got.Status.Conditions, ichorv1alpha1.TalosEtcdBackupConditionStoreInCluster)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Message).To(ContainSubstring("the backup store must live outside the cluster being restored"))
		Expect(readyOf(got).Status).To(Equal(metav1.ConditionTrue))

		// Pointing it outside clears the warning.
		got.Spec.Destination.S3.Endpoint = "https://s3.example.test"
		Expect(k8sClient.Update(ctx, got)).To(Succeed())

		_, got = f.reconcile(got)
		Expect(apimeta.FindStatusCondition(got.Status.Conditions, ichorv1alpha1.TalosEtcdBackupConditionStoreInCluster)).To(BeNil())
	})

	It("fails clearly when the credentials Secret is missing or incomplete", func() {
		f := newBackupFixture()
		f.addControlPlane("71", 2)

		var sec corev1.Secret
		key := types.NamespacedName{Namespace: tmNamespace, Name: "s3-" + f.cluster.Name}
		Expect(k8sClient.Get(ctx, key, &sec)).To(Succeed())
		delete(sec.Data, s3KeySecretAccessKey)
		Expect(k8sClient.Update(ctx, &sec)).To(Succeed())

		_, got := f.reconcile(f.newBackup(nil))
		Expect(readyOf(got).Status).To(Equal(metav1.ConditionFalse))
		Expect(readyOf(got).Message).To(ContainSubstring(s3KeySecretAccessKey))
		Expect(f.nodes.total()).To(BeZero())
	})

	It("reads S3 credentials from the controller namespace, not the backup's own (ADR 0011)", func() {
		other := &corev1.Namespace{Name: "backup-elsewhere"}
		Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, other))).To(Succeed())

		f := newBackupFixture()

		// The cluster, its control plane and the backup all live in "backup-elsewhere"; the
		// bundle Secret and the S3 credentials Secret live only in tmNamespace. A decoy
		// credentials Secret of the same name sits next to the backup with other values.
		cluster := &ichorv1alpha1.TalosCluster{
			GenerateName: "c-", Namespace: other.Name, Spec: *f.cluster.Spec.DeepCopy(),
		}
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

		m := &ichorv1alpha1.TalosMachine{
			GenerateName: "cp-", Namespace: other.Name,
			Spec: ichorv1alpha1.TalosMachineSpec{
				ClusterRef: corev1.LocalObjectReference{Name: cluster.Name}, Identity: ichorv1alpha1.TalosMachineIdentity{UUID: "uuid-cp-" + cluster.Name},
				Endpoint: "10.0.9.1:50000", Role: roleControlPlane, ApprovalState: ichorv1alpha1.MachineApprovalStateApproved,
			},
		}
		Expect(k8sClient.Create(ctx, m)).To(Succeed())
		f.nodes.nodes[m.Spec.Endpoint] = &fakeEtcdNode{status: EtcdNodeStatus{MemberID: 2, LeaderID: 1}, snapshot: validTestSnapshot("x")}

		credsName := "s3-" + f.cluster.Name
		Expect(k8sClient.Create(ctx, &corev1.Secret{
			Name: credsName, Namespace: other.Name,
			Data: map[string][]byte{s3KeyAccessKeyID: []byte("DECOY"), s3KeySecretAccessKey: []byte("DECOY")},
		})).To(Succeed())

		bk := &ichorv1alpha1.TalosEtcdBackup{
			GenerateName: "bk-", Namespace: other.Name,
			Spec: ichorv1alpha1.TalosEtcdBackupSpec{
				ClusterRef: corev1.LocalObjectReference{Name: cluster.Name},
				Destination: ichorv1alpha1.TalosEtcdBackupDestination{S3: ichorv1alpha1.TalosEtcdBackupS3{
					Endpoint: "https://s3.example.test", Bucket: testS3Bucket, Prefix: testBackupBase,
					CredentialsSecretRef: corev1.LocalObjectReference{Name: credsName},
				}},
			},
		}
		Expect(k8sClient.Create(ctx, bk)).To(Succeed())

		_, got := f.reconcile(bk)
		Expect(readyOf(got).Status).To(Equal(metav1.ConditionTrue), readyOf(got).Message)
		Expect(f.cfgSeen).NotTo(BeNil())
		Expect(f.cfgSeen.AccessKeyID).To(Equal("AKIATESTACCESSKEY"))
	})

	Describe("leak discipline", func() {
		// Whatever S3 or Talos echo back must not reach status, whatever it contains.
		assertClean := func(f *backupFixture, got *ichorv1alpha1.TalosEtcdBackup) {
			pass, err := etcdbackup.Passphrase(f.bundle)
			Expect(err).NotTo(HaveOccurred())

			material := append(bundleSecretMaterial(f.bundle), pass, "AKIATESTACCESSKEY", "test-secret-access-key-value")
			cond := readyOf(got)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))

			for _, s := range material {
				Expect(cond.Message).NotTo(ContainSubstring(s), "Ready condition leaked secret material")
			}

			Expect(cond.Message).To(ContainSubstring(redacted))
		}

		It("redacts credentials, the derived key and bundle material from an upload error", func() {
			f := newBackupFixture()
			f.addControlPlane("91", 2)

			pass, err := etcdbackup.Passphrase(f.bundle)
			Expect(err).NotTo(HaveOccurred())

			f.store.putErr = errors.New("PUT denied for AKIATESTACCESSKEY signed with test-secret-access-key-value key " + pass +
				" " + f.bundle.Cluster.Secret)

			_, got := f.reconcile(f.newBackup(nil))
			assertClean(f, got)
			Expect(f.store.objects).To(BeEmpty())
		})

		It("redacts bundle material from a Talos snapshot error", func() {
			f := newBackupFixture()
			n := f.addControlPlane("92", 2)
			n.snapshotEr = errors.New("rpc error: client cert " + string(f.bundle.Certs.OS.Key) + " rejected, token " + f.bundle.Cluster.Secret)

			_, got := f.reconcile(f.newBackup(nil))
			assertClean(f, got)
		})
	})
})
