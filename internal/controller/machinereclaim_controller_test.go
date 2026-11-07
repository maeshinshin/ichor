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
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/reclaim"
)

// reclaimMarker is built into every fake talosconfig below. It must appear in no log line, Event,
// status, error or panic (ADR 0103 section 2.2).
const (
	testNodeAddr = "192.0.2.10"
	testTalosVer = "v1.9.0"
	testDevice   = "/dev/sdb"
	testTrue     = "true"
)

const reclaimMarker = "TALOSCONFIG-MARKER-9f3c1d7e"

// fakeReclaimNode answers from a script. Its errors quote the marker, as a transport error could.
type fakeReclaimNode struct {
	in         reclaim.Inspection
	inspectErr error
	resetErr   error
	resets     atomic.Int32
	panics     bool
}

func (n *fakeReclaimNode) Inspect(context.Context) (reclaim.Inspection, error) {
	if n.panics {
		panic("boom " + reclaimMarker)
	}

	return n.in, n.inspectErr
}

func (n *fakeReclaimNode) Reset(context.Context, bool) error {
	n.resets.Add(1)

	return n.resetErr
}

func (*fakeReclaimNode) Close() error { return nil }

var _ = Describe("MachineReclaim Controller", func() {
	const ns = "ichor-system"

	var (
		node     *fakeReclaimNode
		rec      *record.FakeRecorder
		logs     *bytes.Buffer
		lctx     context.Context
		now      time.Time
		probeUID string
		r        *MachineReclaimReconciler
		seq      int
		secret   string
		sawCred  bool
	)

	newNode := func() *fakeReclaimNode {
		return &fakeReclaimNode{in: reclaim.Inspection{
			Address: testNodeAddr, UUID: "uuid-1", Hostname: "n1", ClusterName: testOldValue, ClusterID: "cid-old",
			Version: testTalosVer, ClusterReachable: true,
			Volumes: []reclaim.Volume{{Name: "u-data", Location: testDevice, Size: 10, Encrypted: true}},
		}}
	}

	BeforeEach(func() {
		Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &corev1.Namespace{
			Name: ns,
		}))).To(Succeed())

		seq++
		node, probeUID, sawCred = newNode(), "uuid-1", false
		rec = record.NewFakeRecorder(200)
		logs = &bytes.Buffer{}
		lctx = ctrlLogInto(ctx, logs)
		now = time.Now().Truncate(time.Second) // API times keep whole seconds; a fractional clock would skew timing assertions

		r = &MachineReclaimReconciler{
			Client: inNamespace{k8sClient, ns}, ControllerNamespace: ns, Recorder: rec,
			Dial: func(_ context.Context, cred reclaim.Credential, address, _ string) (reclaim.Node, error) {
				sawCred = strings.Contains(string(cred.Bytes()), reclaimMarker)
				if address != testNodeAddr {
					return nil, errors.New("dialled the wrong address")
				}

				return node, nil
			},
			Probe:        func(context.Context, string) (string, error) { return probeUID, nil },
			Now:          func() time.Time { return now },
			WaitWindow:   time.Second,
			WaitInterval: 10 * time.Millisecond,
		}
		secret = fmt.Sprintf("cred-%d", seq)
	})

	// create makes the credential Secret and the request, as the UI API would.
	create := func(mutate ...func(*ichorv1alpha1.MachineReclaim)) types.NamespacedName {
		Expect(k8sClient.Create(ctx, &corev1.Secret{
			Name: secret, Namespace: ns, Labels: map[string]string{ichorv1alpha1.ReclaimLabel: testTrue},
			Type: ichorv1alpha1.ReclaimCredentialSecretType,
			Data: map[string][]byte{ichorv1alpha1.ReclaimCredentialKey: []byte("context: x\nca: " + reclaimMarker + "\n")},
		})).To(Succeed())

		mr := &ichorv1alpha1.MachineReclaim{
			Name: fmt.Sprintf("mr-%d", seq), Namespace: ns,
			Annotations: map[string]string{ichorv1alpha1.ReclaimRequestedByAnnotation: reclaimRequester},
			Spec:        ichorv1alpha1.MachineReclaimSpec{Address: testNodeAddr, CredentialSecretRef: secret},
		}
		for _, m := range mutate {
			m(mr)
		}

		Expect(k8sClient.Create(ctx, mr)).To(Succeed())

		return client.ObjectKeyFromObject(mr)
	}

	// createBare makes only the request: no annotations and no credential Secret.
	createBare := func() types.NamespacedName {
		mr := &ichorv1alpha1.MachineReclaim{
			Name: fmt.Sprintf("mr-%d", seq), Namespace: ns,
			Spec: ichorv1alpha1.MachineReclaimSpec{Address: testNodeAddr, CredentialSecretRef: secret},
		}
		Expect(k8sClient.Create(ctx, mr)).To(Succeed())

		return client.ObjectKeyFromObject(mr)
	}

	reconcileOnce := func(key types.NamespacedName) (reconcile.Result, error) {
		return r.Reconcile(lctx, reconcile.Request{NamespacedName: key})
	}

	// reconcileOK is a reconcile that must not return an error.
	reconcileOK := func(key types.NamespacedName) {
		GinkgoHelper()

		_, err := reconcileOnce(key)
		Expect(err).NotTo(HaveOccurred())
	}

	get := func(key types.NamespacedName) *ichorv1alpha1.MachineReclaim {
		var mr ichorv1alpha1.MachineReclaim
		Expect(k8sClient.Get(ctx, key, &mr)).To(Succeed())

		return &mr
	}

	secretExists := func() bool {
		err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: secret}, &corev1.Secret{})
		if apierrors.IsNotFound(err) {
			return false
		}

		Expect(err).NotTo(HaveOccurred())

		return true
	}

	confirm := func(key types.NamespacedName, uuid string) {
		mr := get(key)
		mr.Spec.ConfirmUUID = uuid
		mr.Annotations[ichorv1alpha1.ReclaimConfirmedByAnnotation] = "bob"
		Expect(k8sClient.Update(ctx, mr)).To(Succeed())
	}

	// expectNoMarker is the mandatory test: the marker is nowhere a reader of the cluster or the
	// logs could see it.
	expectNoMarker := func(key types.NamespacedName, errs ...error) {
		GinkgoHelper()

		Expect(logs.String()).NotTo(ContainSubstring(reclaimMarker))

		for _, e := range errs {
			if e != nil {
				Expect(e.Error()).NotTo(ContainSubstring(reclaimMarker))
			}
		}

		for len(rec.Events) > 0 {
			Expect(<-rec.Events).NotTo(ContainSubstring(reclaimMarker))
		}

		raw, err := json.Marshal(get(key))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).NotTo(ContainSubstring(reclaimMarker))
	}

	It("inspects, waits for the typed UUID, resets once and reports maintenance mode", func() {
		key := create()

		reconcileOK(key)

		mr := get(key)
		Expect(mr.Status.Phase).To(Equal(ichorv1alpha1.MachineReclaimAwaitingConfirmation))
		Expect(mr.Status.ExpiresAt).NotTo(BeNil())
		Expect(mr.Status.ExpiresAt.Sub(mr.CreationTimestamp.Time)).To(Equal(15 * time.Minute))
		Expect(mr.Status.Inspection.UUID).To(Equal("uuid-1"))
		Expect(mr.Status.Inspection.Volumes).To(HaveLen(1))
		Expect(mr.Status.Audit.RequestedBy).To(Equal(reclaimRequester))
		Expect(sawCred).To(BeTrue(), "the controller must hand the credential to the dialler")
		Expect(node.resets.Load()).To(BeZero())

		var sec corev1.Secret
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: secret}, &sec)).To(Succeed())
		Expect(sec.OwnerReferences).To(HaveLen(1))

		// A spec edit without the UUID re-inspects and still sends nothing.
		reconcileOK(key)
		Expect(node.resets.Load()).To(BeZero())

		confirm(key, "uuid-1")

		reconcileOK(key)
		Expect(node.resets.Load()).To(Equal(int32(1)))
		Expect(secretExists()).To(BeFalse(), "the credential is deleted as soon as the reset is sent")

		mr = get(key)
		Expect(mr.Status.Phase).To(Equal(ichorv1alpha1.MachineReclaimWaitingForMaintenance))
		Expect(mr.Status.Audit.ConfirmedBy).To(Equal("bob"))
		Expect(mr.Status.Audit.ResetSentAt).NotTo(BeNil())
		Expect(mr.Status.Audit.CredentialDeletedAt).NotTo(BeNil())

		reconcileOK(key)

		mr = get(key)
		Expect(mr.Status.Phase).To(Equal(ichorv1alpha1.MachineReclaimDone))
		Expect(mr.Status.Result).To(ContainSubstring("192.0.2.10 is in maintenance mode"))

		// Done is final: no further reset.
		reconcileOK(key)
		Expect(node.resets.Load()).To(Equal(int32(1)))

		var reasons []string

		for len(rec.Events) > 0 {
			reasons = append(reasons, <-rec.Events)
		}

		joined := strings.Join(reasons, "\n")
		for _, want := range []string{"ReclaimRequested", "ReclaimInspected", "ReclaimConfirmed", "ReclaimReset", "CredentialDeleted", "ReclaimCompleted"} {
			Expect(joined).To(ContainSubstring(want))
		}

		Expect(joined).NotTo(ContainSubstring(reclaimMarker))
		expectNoMarker(key)
	})

	It("waits for a credential Secret that the UI API writes after the request", func() {
		key := createBare()
		res, err := reconcileOnce(key)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(BeNumerically(">", 0))
		Expect(get(key).Status.Phase).To(Equal(ichorv1alpha1.MachineReclaimInspecting))

		Expect(k8sClient.Create(ctx, &corev1.Secret{
			Name: secret, Namespace: ns, Labels: map[string]string{ichorv1alpha1.ReclaimLabel: testTrue},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: ichorv1alpha1.GroupVersion.String(), Kind: "MachineReclaim", Name: key.Name, UID: get(key).UID,
			}},
			Type: ichorv1alpha1.ReclaimCredentialSecretType,
			Data: map[string][]byte{ichorv1alpha1.ReclaimCredentialKey: []byte(reclaimMarker)},
		})).To(Succeed())

		reconcileOK(key)
		Expect(get(key).Status.Phase).To(Equal(ichorv1alpha1.MachineReclaimAwaitingConfirmation))
	})

	It("expires a request whose Secret never appears", func() {
		key := createBare()
		reconcileOK(key)

		now = get(key).Status.ExpiresAt.Add(time.Second)
		reconcileOK(key)
		Expect(get(key).Status.Phase).To(Equal(ichorv1alpha1.MachineReclaimExpired))
	})

	It("refuses a second reset from the same request", func() {
		key := create()
		reconcileOK(key)

		// The status says a reset was already sent (a controller restart, or a stale edit).
		mr := get(key)
		t := metav1.Now()
		mr.Status.Audit.ResetSentAt = &t
		Expect(k8sClient.Status().Update(ctx, mr)).To(Succeed())

		confirm(key, "uuid-1")

		reconcileOK(key)
		Expect(node.resets.Load()).To(BeZero())
		Expect(get(key).Status.Phase).To(Equal(ichorv1alpha1.MachineReclaimFailed))
		Expect(secretExists()).To(BeFalse())
	})

	It("does not reset when the freshly read UUID differs from the typed one", func() {
		key := create()
		reconcileOK(key)

		node.in.UUID = "uuid-other" // a different machine now answers
		confirm(key, "uuid-1")

		reconcileOK(key)
		Expect(node.resets.Load()).To(BeZero())
		Expect(get(key).Status.Phase).To(Equal(ichorv1alpha1.MachineReclaimFailed))
		Expect(secretExists()).To(BeFalse())
	})

	It("does not reset a machine the plan refuses, even when confirmed", func() {
		node.in.ControlPlane, node.in.EtcdKnown = true, true
		node.in.EtcdMembers = []reclaim.EtcdMember{{ID: 1, Unhealthy: false}, {ID: 2, Unhealthy: true}, {ID: 3, Unhealthy: true}}
		node.in.LocalMemberID = 1
		key := create()

		reconcileOK(key)
		Expect(get(key).Status.Inspection.Refusals).NotTo(BeEmpty())

		confirm(key, "uuid-1")

		reconcileOK(key)
		Expect(node.resets.Load()).To(BeZero())
		Expect(get(key).Status.Phase).To(Equal(ichorv1alpha1.MachineReclaimFailed))
	})

	It("expires at the TTL, deletes the credential and sends nothing", func() {
		key := create(func(mr *ichorv1alpha1.MachineReclaim) { mr.Spec.TTL = &metav1.Duration{Duration: time.Minute} })

		reconcileOK(key)

		confirm(key, "uuid-1")

		now = get(key).Status.ExpiresAt.Add(time.Second)

		reconcileOK(key)

		Expect(get(key).Status.Phase).To(Equal(ichorv1alpha1.MachineReclaimExpired))
		Expect(node.resets.Load()).To(BeZero())
		Expect(secretExists()).To(BeFalse())
	})

	// toWaiting drives a request to WaitingForMaintenance and returns when the reset was sent.
	toWaiting := func(key types.NamespacedName) time.Time {
		GinkgoHelper()

		reconcileOK(key)
		confirm(key, "uuid-1")
		reconcileOK(key)

		mr := get(key)
		Expect(mr.Status.Phase).To(Equal(ichorv1alpha1.MachineReclaimWaitingForMaintenance))

		return mr.Status.Audit.ResetSentAt.Time
	}

	It("fails a machine that never returns exactly reclaimMaintenanceWait after the reset", func() {
		key := create()
		sent := toWaiting(key)

		// Each failing probe costs 100 ms of clock; a reconcile therefore takes about the wait window.
		r.Probe = func(context.Context, string) (string, error) {
			now = now.Add(100 * time.Millisecond)

			return "", errors.New("no route to host")
		}

		var failedAt time.Time

		for range 200 {
			res, err := reconcileOnce(key)
			Expect(err).NotTo(HaveOccurred())

			if get(key).Status.Phase == ichorv1alpha1.MachineReclaimFailed {
				failedAt = now

				break
			}

			now = now.Add(res.RequeueAfter)
		}

		Expect(failedAt).NotTo(BeZero(), "the request never failed")
		Expect(failedAt.Sub(sent)).To(BeNumerically(">=", reclaimMaintenanceWait))
		Expect(failedAt.Sub(sent)).To(BeNumerically("<=", reclaimMaintenanceWait+time.Second))
		Expect(get(key).Status.Result).To(ContainSubstring("did not return to maintenance mode"))
	})

	It("does not let the request TTL cut short the wait for maintenance mode", func() {
		key := create(func(mr *ichorv1alpha1.MachineReclaim) { mr.Spec.TTL = &metav1.Duration{Duration: time.Minute} })
		sent := toWaiting(key)

		r.Probe = func(context.Context, string) (string, error) { return "", errors.New("down") }
		now = sent.Add(5 * time.Minute) // well past the 1 minute TTL, inside the 10 minute wait

		res, err := reconcileOnce(key)
		Expect(err).NotTo(HaveOccurred())
		Expect(get(key).Status.Phase).To(Equal(ichorv1alpha1.MachineReclaimWaitingForMaintenance))
		Expect(res.RequeueAfter).To(BeNumerically(">", 0))
	})

	It("stops a reconcile whose node call hangs, so one request cannot starve the others", func() {
		key := create()
		r.ReconcileTimeout = 200 * time.Millisecond
		r.Dial = func(ctx context.Context, _ reclaim.Credential, _, _ string) (reclaim.Node, error) {
			select {
			case <-ctx.Done():
			case <-time.After(30 * time.Second):
			}

			return nil, ctx.Err()
		}

		start := time.Now()
		_, _ = reconcileOnce(key) // the error (or retry) is fine; hanging is not
		Expect(time.Since(start)).To(BeNumerically("<", 5*time.Second))
	})

	It("keeps a finished request for seven days after its expiry, then deletes it", func() {
		key := create()

		reconcileOK(key)

		now = get(key).Status.ExpiresAt.Add(time.Second)
		reconcileOK(key)
		Expect(get(key).Status.Phase).To(Equal(ichorv1alpha1.MachineReclaimExpired))

		now = get(key).Status.ExpiresAt.Add(6 * 24 * time.Hour)
		reconcileOK(key)
		Expect(get(key)).NotTo(BeNil())

		now = get(key).Status.ExpiresAt.Add(7*24*time.Hour + time.Minute)
		reconcileOK(key)

		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key, &ichorv1alpha1.MachineReclaim{}))).To(BeTrue())
	})

	It("will not read or delete a Secret of another type named as the credential", func() {
		Expect(k8sClient.Create(ctx, &corev1.Secret{
			Name: secret, Namespace: ns,
			Data: map[string][]byte{"talosconfig": []byte("x")},
		})).To(Succeed())

		key := createBare()

		reconcileOK(key)
		Expect(get(key).Status.Phase).To(Equal(ichorv1alpha1.MachineReclaimFailed))
		Expect(secretExists()).To(BeTrue(), "an unrelated Secret must survive")
	})

	It("withholds the credential from errors, status, Events, logs and panics", func() {
		cases := map[string]func(){
			"inspect error quoting the credential": func() {
				node.inspectErr = fmt.Errorf("tls: bad certificate %s", reclaimMarker)
			},
			"dial error quoting the credential": func() {
				r.Dial = func(context.Context, reclaim.Credential, string, string) (reclaim.Node, error) {
					return nil, fmt.Errorf("dial failed with %s", reclaimMarker)
				}
			},
			"panic quoting the credential": func() { node.panics = true },
		}

		origDial := r.Dial

		for name, arrange := range cases {
			By(name)

			r.Dial = origDial

			seq++
			secret = fmt.Sprintf("cred-%d", seq)
			node = newNode()
			arrange()

			key := create()

			_, err := reconcileOnce(key)
			expectNoMarker(key, err)

			if name == "panic quoting the credential" {
				Expect(err).To(HaveOccurred())
			} else {
				Expect(get(key).Status.Phase).To(Equal(ichorv1alpha1.MachineReclaimFailed))
				Expect(get(key).Status.Result).To(ContainSubstring(testNodeAddr))
			}
		}

		By("a failing reset")

		seq++
		secret = fmt.Sprintf("cred-%d", seq)
		node = newNode()
		node.resetErr = fmt.Errorf("rpc: %s", reclaimMarker)
		key := create()

		reconcileOK(key)
		confirm(key, "uuid-1")

		_, err := reconcileOnce(key)
		expectNoMarker(key, err)
		Expect(get(key).Status.Phase).To(Equal(ichorv1alpha1.MachineReclaimFailed))
	})

	It("sweeps credentials older than the maximum TTL and nothing else", func() {
		mk := func(name string, typ corev1.SecretType, labelled bool) {
			s := &corev1.Secret{Name: name, Namespace: ns, Type: typ}
			if labelled {
				s.Labels = map[string]string{ichorv1alpha1.ReclaimLabel: testTrue}
			}

			Expect(k8sClient.Create(ctx, s)).To(Succeed())
		}

		old := fmt.Sprintf("sweep-old-%d", seq)
		mk(old, ichorv1alpha1.ReclaimCredentialSecretType, true)
		mk(old+"-wrongtype", corev1.SecretTypeOpaque, true)
		mk(old+"-nolabel", ichorv1alpha1.ReclaimCredentialSecretType, false)

		sw := &ReclaimCredentialSweeper{Client: k8sClient, Namespace: ns, Now: func() time.Time { return time.Now().Add(30 * time.Minute) }}
		n, err := sw.Sweep(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(BeZero(), "30 minutes is within the maximum")

		sw.Now = func() time.Time { return time.Now().Add(61 * time.Minute) }
		n, err = sw.Sweep(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(BeNumerically(">=", 1))

		exists := func(name string) bool {
			return k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, &corev1.Secret{}) == nil
		}
		Expect(exists(old)).To(BeFalse())
		Expect(exists(old + "-wrongtype")).To(BeTrue())
		Expect(exists(old + "-nolabel")).To(BeTrue())
	})
})

// inNamespace confines List to one namespace. The suite shares one API server, and other specs
// leave TalosClusters in "default" whose bundle Secrets are not in ns; reclaim's fail-closed
// LoadKnown would otherwise refuse because of them.
type inNamespace struct {
	client.Client
	ns string
}

func (c inNamespace) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	return c.Client.List(ctx, list, append(opts, client.InNamespace(c.ns))...)
}

// ctrlLogInto returns ctx carrying a logger that writes to w.
func ctrlLogInto(ctx context.Context, w *bytes.Buffer) context.Context {
	return logf.IntoContext(ctx, zap.New(zap.WriteTo(w), zap.UseDevMode(true)))
}
