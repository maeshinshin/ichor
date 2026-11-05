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
	"slices"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/siderolabs/crypto/x509"
	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	yaml "go.yaml.in/yaml/v4"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/etcdbackup"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
	"github.com/maeshinshin/ichor/pkg/render"
)

const keyOpPrefix = "key-op-"

var (
	bothTargets = []string{ichorv1alpha1.KeyRotationTargetServiceAccount, ichorv1alpha1.KeyRotationTargetSecretbox}
	saOnly      = []string{ichorv1alpha1.KeyRotationTargetServiceAccount}
	boxOnly     = []string{ichorv1alpha1.KeyRotationTargetSecretbox}

	ecdsaBundleOnce sync.Once
	ecdsaBundle     *secrets.Bundle
)

// fastBundle is a v1.14 bundle with an ECDSA service-account key: generating the RSA one costs seconds,
// and every test that is not about the key's algorithm only needs a key.
func fastBundle() *secrets.Bundle {
	ecdsaBundleOnce.Do(func() {
		contract, err := coreconfig.ParseContractFromVersion("v1.14")
		Expect(err).NotTo(HaveOccurred())

		ecdsaBundle, err = secrets.NewBundle(secrets.NewFixedClock(time.Now()), contract, secrets.WithECDSAServiceAccountKey())
		Expect(err).NotTo(HaveOccurred())
	})

	return ecdsaBundle
}

// keyNode is a fake control plane that behaves like a real one in what a key rotation depends on: its
// kube-apiserver restarts into a new container when the configuration changes and is not ready for a while.
type keyNode struct {
	// controlPlane is false for a worker, whose configuration carries neither document.
	controlPlane bool
	cfg          []byte
	down         bool
	instance     int
	notReady     int
}

type keyWorld struct {
	nodes    map[string]*keyNode
	notReady []string

	applies               []applyRecord
	keysSavedAtFirstApply *bool
	secretName            string

	failApply  func(node string, n int) error
	versionErr map[string]error

	// instanceErr is what reading a node's kube-apiserver container fails with; noInstance makes Talos list none.
	instanceErr map[string]error
	noInstance  bool
	dialLeak    string

	// restartDelay is how many /readyz probes fail after each apply; noRestart keeps the container as it was.
	restartDelay int
	noRestart    bool

	// violations collects every moment the dual-key safety rule was broken: some apiserver signed or encrypted with
	// a key another could not verify or decrypt, or a control plane was restarted while another was still down.
	violations []string

	// sweeps is what each re-encryption sweep reports, in order; the last one repeats.
	sweeps      []ReencryptResult
	sweepCalls  int
	sweepErr    error
	sweepConfig []keyState

	// legacy is what the legacy-token check reports; legacyCalls how often it ran.
	legacy      []string
	legacyCalls int
	legacyKey   []byte
}

// keyState is what a control plane's configuration makes its kube-apiserver do with the two keys.
type keyState struct {
	issuer string
	pubs   []string
	boxes  []string
}

func stateOf(cfg []byte) keyState {
	provider, err := configloader.NewFromBytes(cfg)
	Expect(err).NotTo(HaveOccurred())

	sa := provider.K8sServiceAccountConfig()
	Expect(sa).NotTo(BeNil())

	st := keyState{issuer: string(sa.IssuingKey().Key)}
	for _, k := range sa.AcceptedKeys() {
		st.pubs = append(st.pubs, string(k.Key))
	}

	obj := provider.K8sEtcdEncryptionConfig().EtcdEncryptionConfig()
	resources, _ := obj["resources"].([]any)
	Expect(resources).To(HaveLen(1))

	providers, _ := resources[0].(map[string]any)["providers"].([]any)
	for _, p := range providers {
		box, _ := p.(map[string]any)["secretbox"].(map[string]any)
		keys, _ := box["keys"].([]any)
		Expect(keys).To(HaveLen(1))

		st.boxes = append(st.boxes, keys[0].(map[string]any)["secret"].(string))
	}

	return st
}

// pubOfKey is the public half, PEM encoded, of a service-account private key.
func pubOfKey(k *x509.PEMEncodedKey) string {
	key, err := k.GetKey()
	Expect(err).NotTo(HaveOccurred())

	return string(key.GetPublicKeyPEM())
}

func ctrlRequest(key types.NamespacedName) ctrl.Request { return ctrl.Request{NamespacedName: key} }

// checkSafety records a violation when some control plane would sign with a key another does not accept.
func (w *keyWorld) checkSafety(when string) {
	for a, na := range w.nodes {
		if !na.controlPlane {
			continue
		}

		sa := stateOf(na.cfg)

		signer := pubOfKey(&x509.PEMEncodedKey{Key: []byte(sa.issuer)})

		for b, nb := range w.nodes {
			if !nb.controlPlane {
				continue
			}

			sb := stateOf(nb.cfg)

			if !slices.Contains(sb.pubs, signer) {
				w.violations = append(w.violations, fmt.Sprintf("%s: %s signs with a key %s does not verify", when, a, b))
			}

			if !slices.Contains(sb.boxes, sa.boxes[0]) {
				w.violations = append(w.violations, fmt.Sprintf("%s: %s encrypts with a key %s cannot decrypt", when, a, b))
			}
		}
	}
}

func (w *keyWorld) factory(
	_ context.Context, _ *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, _ *secrets.Bundle,
) (TalosClient, error) {
	node := w.nodes[m.Name]
	if node == nil || node.down {
		return nil, fmt.Errorf("dialing Talos API: connection refused %s", w.dialLeak)
	}

	return &keyClient{w: w, name: m.Name, node: node}, nil
}

type keyClient struct {
	fakeTalosClient
	w    *keyWorld
	name string
	node *keyNode
}

func (c *keyClient) CurrentConfig(context.Context) ([]byte, error) { return c.node.cfg, nil }

func (c *keyClient) DryRunApply(_ context.Context, cfg []byte) ([]string, error) {
	if _, err := configloader.NewFromBytes(cfg); err != nil {
		return nil, fmt.Errorf("the node would refuse this configuration: %w", err)
	}

	return nil, nil
}

func (c *keyClient) ApplyNoReboot(ctx context.Context, cfg []byte) error {
	w := c.w

	if w.keysSavedAtFirstApply == nil {
		var secret corev1.Secret
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: tmNamespace, Name: w.secretName}, &secret)).To(Succeed())

		_, saved := secret.Data[secretsbundle.KeyRotationKey]
		w.keysSavedAtFirstApply = &saved
	}

	if w.failApply != nil {
		if err := w.failApply(c.name, len(w.applies)); err != nil {
			return err
		}
	}

	for name, other := range w.nodes {
		if name != c.name && other.notReady > 0 {
			w.violations = append(w.violations, fmt.Sprintf("%s was applied while the kube-apiserver of %s was still restarting", c.name, name))
		}
	}

	w.applies = append(w.applies, applyRecord{node: c.name, hash: render.Hash(cfg)})
	c.node.cfg = bytes.Clone(cfg)

	if !w.noRestart {
		c.node.instance++
		c.node.notReady = w.restartDelay
	}

	w.checkSafety("after applying to " + c.name)

	return nil
}

func (c *keyClient) Version(context.Context) (string, error) {
	return testTalosVersion, c.w.versionErr[c.name]
}

func (c *keyClient) APIServerInstance(context.Context) (string, error) {
	if err := c.w.instanceErr[c.name]; err != nil {
		return "", err
	}

	if c.w.noInstance {
		return "", nil
	}

	return fmt.Sprintf("containerd://%s-%d", c.name, c.node.instance), nil
}

func (c *keyClient) APIServerReady(context.Context) error {
	if c.node.notReady > 0 {
		c.node.notReady--

		return errors.New("kube-apiserver /readyz answered 503 Service Unavailable")
	}

	return nil
}

func (w *keyWorld) reencrypt(context.Context, TalosClient) (ReencryptResult, error) {
	w.sweepCalls++

	// The sweep must run on the configuration where the new key already encrypts everywhere.
	for _, n := range w.nodes {
		if n.controlPlane {
			w.sweepConfig = append(w.sweepConfig, stateOf(n.cfg))
		}
	}

	if w.sweepErr != nil {
		return ReencryptResult{}, w.sweepErr
	}

	if len(w.sweeps) == 0 {
		return ReencryptResult{Scanned: 10}, nil
	}

	res := w.sweeps[0]
	if len(w.sweeps) > 1 {
		w.sweeps = w.sweeps[1:]
	}

	return res, nil
}

func (w *keyWorld) legacyTokens(_ context.Context, _ TalosClient, oldPublicKey []byte) ([]string, error) {
	w.legacyCalls++
	w.legacyKey = oldPublicKey

	return slices.Clone(w.legacy), nil
}

// keyRotFixture is a cluster of three control planes and two workers running their rendered configuration,
// plus a key-rotation operation.
type keyRotFixture struct {
	cluster  *ichorv1alpha1.TalosCluster
	secret   *corev1.Secret
	old      *secrets.Bundle
	machines []*ichorv1alpha1.TalosMachine
	world    *keyWorld
	recorder *record.FakeRecorder
	// reconcilers counts the fresh reconcilers that ran: every step builds a new one, so nothing the executor needs
	// can live in memory.
	reconcilers int
}

func newKeyRotFixture(bundle *secrets.Bundle) *keyRotFixture {
	f := &keyRotFixture{recorder: record.NewFakeRecorder(200), old: bundle}
	f.secret = newTestSecret(f.old)
	f.cluster = newTestClusterWithCount(3, f.secret.Name)
	f.world = &keyWorld{nodes: map[string]*keyNode{}, secretName: f.secret.Name, versionErr: map[string]error{}, instanceErr: map[string]error{}}

	for _, n := range []string{"cp1", "cp2", "cp3"} {
		f.machines = append(f.machines, newOpTestMachine(f.cluster.Name, roleControlPlane, "key-"+n))
	}

	for _, n := range []string{"w1", "w2"} {
		f.machines = append(f.machines, newOpTestMachine(f.cluster.Name, roleWorker, "key-"+n))
	}

	for _, m := range f.machines {
		cfg, _, err := render.Render(f.cluster, m, f.old, nil, nil)
		Expect(err).NotTo(HaveOccurred())

		f.world.nodes[m.Name] = &keyNode{cfg: cfg, controlPlane: m.Spec.Role == roleControlPlane}
	}

	return f
}

func (f *keyRotFixture) controlPlanes() []*ichorv1alpha1.TalosMachine { return f.machines[:3] }

func (f *keyRotFixture) newReconciler() *TalosOperationReconciler {
	f.reconcilers++

	return &TalosOperationReconciler{
		Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace,
		TalosClient: f.world.factory,
		NodesReady:  func(context.Context, TalosClient) ([]string, error) { return f.world.notReady, nil },
		Recorder:    f.recorder,
		Reencrypt:   f.world.reencrypt, LegacyTokens: f.world.legacyTokens,
	}
}

func (f *keyRotFixture) newOp(targets []string) *ichorv1alpha1.TalosOperation {
	return f.newOpSpec(&ichorv1alpha1.TalosOperationKeyRotationSpec{Targets: targets, TokenSettle: &metav1.Duration{}})
}

func (f *keyRotFixture) newOpSpec(spec *ichorv1alpha1.TalosOperationKeyRotationSpec) *ichorv1alpha1.TalosOperation {
	op := &ichorv1alpha1.TalosOperation{}
	op.GenerateName, op.Namespace = keyOpPrefix, tmNamespace
	op.Spec.ClusterRef.Name, op.Spec.Kind = f.cluster.Name, ichorv1alpha1.TalosOperationKindKeyRotation
	op.Spec.KeyRotation = spec
	Expect(k8sClient.Create(ctx, op)).To(Succeed())

	return op
}

func (f *keyRotFixture) step(op *ichorv1alpha1.TalosOperation) *ichorv1alpha1.TalosOperation {
	_, err := reconcileOperation(f.newReconciler(), op)
	Expect(err).NotTo(HaveOccurred())

	return fetchOperation(op)
}

func (f *keyRotFixture) runUntil(op *ichorv1alpha1.TalosOperation, done func(*ichorv1alpha1.TalosOperation) bool) (*ichorv1alpha1.TalosOperation, []string) {
	return runRotationUntil(f.step, op, done)
}

func (f *keyRotFixture) secretData() map[string][]byte { return secretData(f.secret.Name) }

func (f *keyRotFixture) storedKeys() *secretsbundle.KeyRotation {
	raw, ok := f.secretData()[secretsbundle.KeyRotationKey]
	if !ok {
		return nil
	}

	kr, err := secretsbundle.ParseKeyRotation(raw)
	Expect(err).NotTo(HaveOccurred())

	return kr
}

func (f *keyRotFixture) storedBundle() *secrets.Bundle {
	var bundle secrets.Bundle
	Expect(yaml.Unmarshal(f.secretData()[secretsbundle.SecretKey], &bundle)).To(Succeed())

	return &bundle
}

func (f *keyRotFixture) storedRetired() *secretsbundle.Retired {
	retired, err := secretsbundle.ParseRetired(f.secretData()[secretsbundle.RetiredKeysKey])
	Expect(err).NotTo(HaveOccurred())

	return retired
}

// controlPlanesRun asserts what every control plane's configuration makes its kube-apiserver do.
func (f *keyRotFixture) controlPlanesRun(issuer string, pubs, boxes []string) {
	GinkgoHelper()

	for _, m := range f.controlPlanes() {
		st := stateOf(f.world.nodes[m.Name].cfg)
		Expect(st.issuer).To(Equal(issuer), "%s signs with the wrong key", m.Name)
		Expect(st.pubs).To(Equal(pubs), "%s verifies with the wrong keys", m.Name)
		Expect(st.boxes).To(Equal(boxes), "%s's secretbox providers", m.Name)
	}
}

func (f *keyRotFixture) applyCountPerNodeAndHash() map[applyRecord]int {
	counts := map[applyRecord]int{}
	for _, a := range f.world.applies {
		counts[a]++
	}

	return counts
}

var _ = Describe("TalosOperation key-rotation", func() {
	It("saves the new keys before any control plane is told to use them", func() {
		f := newKeyRotFixture(fastBundle())
		op := f.newOp(bothTargets)
		f.runUntil(op, atStage(ichorv1alpha1.KeyRotationStageIssuing))

		Expect(f.world.keysSavedAtFirstApply).NotTo(BeNil())
		Expect(*f.world.keysSavedAtFirstApply).To(BeTrue(), "the Secret did not hold keyrotation.yaml when the first control plane was told anything")
		Expect(f.storedKeys()).NotTo(BeNil())
	})

	It("rotates both keys through every stage in the dual-key period, one control plane at a time", func() {
		f := newKeyRotFixture(newTestBundle("v1.14"))
		f.world.restartDelay = 2
		f.world.sweeps = []ReencryptResult{{Scanned: 40, Rewritten: 12}, {Scanned: 40}}

		op := f.newOp(bothTargets)
		op, _ = f.runUntil(op, atStage(ichorv1alpha1.KeyRotationStageAccepting))

		kr := f.storedKeys()
		Expect(kr).NotTo(BeNil())
		Expect(kr.ServiceAccount).NotTo(BeNil())

		oldKey := string(f.old.Certs.K8sServiceAccount.Key)
		newKey := string(kr.ServiceAccount.Key)
		oldPub, newPub := pubOfKey(f.old.Certs.K8sServiceAccount), pubOfKey(kr.ServiceAccount)
		oldBox, newBox := f.old.Secrets.SecretboxEncryptionSecret, kr.Secretbox

		// Status names the keys by public identifier and says which one is active in the stage.
		Expect(op.Status.KeyRotation.Keys).To(HaveLen(2))

		for _, k := range op.Status.KeyRotation.Keys {
			Expect(k.Active).To(Equal(k.Previous))
			Expect(k.Accepted).To(Equal([]string{k.Next}))
			Expect(k.Next).NotTo(Equal(k.Previous))
		}

		var stages []string

		check := map[string]func(){
			ichorv1alpha1.KeyRotationStageIssuing: func() {
				// Every control plane verifies and decrypts with the new keys, and still signs and encrypts with the old.
				f.controlPlanesRun(oldKey, []string{oldPub, newPub}, []string{oldBox, newBox})
			},
			ichorv1alpha1.KeyRotationStageReencrypting: func() {
				f.controlPlanesRun(newKey, []string{newPub, oldPub}, []string{newBox, oldBox})
			},
			ichorv1alpha1.KeyRotationStageSettlingTokens: func() {
				f.controlPlanesRun(newKey, []string{newPub, oldPub}, []string{newBox, oldBox})
			},
			ichorv1alpha1.KeyRotationStageDropping: func() {
				f.controlPlanesRun(newKey, []string{newPub, oldPub}, []string{newBox, oldBox})
			},
			ichorv1alpha1.KeyRotationStageFinalizing: func() {
				f.controlPlanesRun(newKey, []string{newPub}, []string{newBox})
			},
		}

		for range 400 {
			op = f.step(op)

			s := rotationStage(op)
			if len(stages) == 0 || stages[len(stages)-1] != s {
				stages = append(stages, s)

				if c := check[s]; c != nil && op.Status.Phase != ichorv1alpha1.TalosOperationPhaseCompleted {
					c()
				}
			}

			if op.Status.Phase == ichorv1alpha1.TalosOperationPhaseCompleted {
				break
			}
		}

		Expect(op.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseCompleted))
		Expect(stages).To(Equal([]string{
			ichorv1alpha1.KeyRotationStageAccepting, ichorv1alpha1.KeyRotationStageIssuing, ichorv1alpha1.KeyRotationStageReencrypting,
			ichorv1alpha1.KeyRotationStageSettlingTokens, ichorv1alpha1.KeyRotationStageDropping, ichorv1alpha1.KeyRotationStageFinalizing,
			ichorv1alpha1.KeyRotationStageDone,
		}))

		// Dual-key safety held at every moment: no apiserver signed or encrypted with a key another could not read.
		Expect(f.world.violations).To(BeEmpty())

		// Three control plane applies per applying stage (Accepting, Issuing, Dropping), no worker was touched.
		Expect(f.world.applies).To(HaveLen(9))

		controlPlanes := map[string]bool{}
		for _, m := range f.controlPlanes() {
			controlPlanes[m.Name] = true
		}

		for rec, n := range f.applyCountPerNodeAndHash() {
			Expect(n).To(Equal(1), "%s received the same configuration twice", rec.node)
			Expect(controlPlanes).To(HaveKey(rec.node), "a worker was applied to")
		}

		// The sweep ran on the dual-key configuration of every control plane, twice (one rewrote, the second proved).
		Expect(f.world.sweepCalls).To(Equal(2))

		for _, st := range f.world.sweepConfig {
			Expect(st.boxes).To(Equal([]string{newBox, oldBox}), "a sweep ran before the new key encrypted everywhere")
		}

		Expect(op.Status.KeyRotation.Reencryption.Sweeps).To(Equal(int32(2)))
		Expect(op.Status.KeyRotation.Reencryption.Rewritten).To(BeZero())

		// The legacy-token check was asked about the old public key.
		Expect(f.world.legacyCalls).To(Equal(1))
		Expect(string(f.world.legacyKey)).To(Equal(oldPub))

		// The Secret holds the new keys and the retired ones, and nothing else of the rotation.
		Expect(f.secretData()).NotTo(HaveKey(secretsbundle.KeyRotationKey))

		stored := f.storedBundle()
		Expect(string(stored.Certs.K8sServiceAccount.Key)).To(Equal(newKey))
		Expect(stored.Secrets.SecretboxEncryptionSecret).To(Equal(newBox))
		Expect(stored.Cluster).To(Equal(f.old.Cluster), "nothing else the bundle holds may change")
		Expect(stored.Certs.K8s).To(Equal(f.old.Certs.K8s))
		Expect(stored.Certs.OS).To(Equal(f.old.Certs.OS))

		retired := f.storedRetired()
		Expect(retired.Keys).To(HaveLen(1))
		Expect(retired.Keys[0].Secretbox).To(Equal(oldBox))

		oldFP, err := secretsbundle.ServiceAccountFingerprint(f.old.Certs.K8sServiceAccount)
		Expect(err).NotTo(HaveOccurred())
		Expect(retired.Keys[0].ServiceAccount).To(Equal(oldFP))

		// Every node runs exactly what the replaced bundle renders: the machine controller sees no drift.
		for _, m := range f.machines {
			_, want, err := render.Render(f.cluster, m, stored, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(render.Hash(f.world.nodes[m.Name].cfg)).To(Equal(want), "%s drifts from the replaced bundle", m.Name)
		}

		// The cluster lock is released.
		mr := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient}
		lock, err := mr.acquireDisruptiveLock(ctx, f.cluster, f.machines[0], "key-rotation-test", false)
		Expect(err).NotTo(HaveOccurred())
		Expect(lock.Release(ctx, "key-rotation-test")).To(Succeed())

		// The user's saved bundle is stale until it is exported again, with the retired keys.
		var cluster ichorv1alpha1.TalosCluster
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: tmNamespace, Name: f.cluster.Name}, &cluster)).To(Succeed())
		Expect(apimeta.IsStatusConditionTrue(cluster.Status.Conditions, ichorv1alpha1.ConditionBundleBackupStale)).To(BeTrue())
		Expect(strings.Join(drainEvents(f.recorder), "\n")).To(ContainSubstring(ichorv1alpha1.ConditionBundleBackupStale))

		// The finalizer goes once the rotation is done.
		op = f.step(op)
		Expect(op.Finalizers).To(BeEmpty())
		Expect(op.Status.FinishedAt).NotTo(BeNil())
	})

	It("rotates only the secretbox key, skipping the token wait", func() {
		f := newKeyRotFixture(fastBundle())
		op := f.newOp(boxOnly)
		op, stages := f.runUntil(op, completed)

		Expect(stages).To(Equal([]string{
			ichorv1alpha1.KeyRotationStageAccepting, ichorv1alpha1.KeyRotationStageIssuing, ichorv1alpha1.KeyRotationStageReencrypting,
			ichorv1alpha1.KeyRotationStageDropping, ichorv1alpha1.KeyRotationStageFinalizing, ichorv1alpha1.KeyRotationStageDone,
		}))
		Expect(op.Status.KeyRotation.Keys).To(HaveLen(1))
		Expect(f.world.legacyCalls).To(BeZero())
		Expect(f.world.violations).To(BeEmpty())

		stored := f.storedBundle()
		Expect(stored.Certs.K8sServiceAccount).To(Equal(f.old.Certs.K8sServiceAccount), "the service-account key must not change")
		Expect(stored.Secrets.SecretboxEncryptionSecret).NotTo(Equal(f.old.Secrets.SecretboxEncryptionSecret))

		retired := f.storedRetired()
		Expect(retired.Keys).To(HaveLen(1))
		Expect(retired.Keys[0].ServiceAccount).To(BeEmpty())
		Expect(retired.Keys[0].Secretbox).To(Equal(f.old.Secrets.SecretboxEncryptionSecret))
	})

	It("rotates only the service-account key, skipping the re-encryption", func() {
		f := newKeyRotFixture(fastBundle())
		op := f.newOp(saOnly)
		_, stages := f.runUntil(op, completed)

		Expect(stages).To(Equal([]string{
			ichorv1alpha1.KeyRotationStageAccepting, ichorv1alpha1.KeyRotationStageIssuing, ichorv1alpha1.KeyRotationStageSettlingTokens,
			ichorv1alpha1.KeyRotationStageDropping, ichorv1alpha1.KeyRotationStageFinalizing, ichorv1alpha1.KeyRotationStageDone,
		}))
		Expect(f.world.sweepCalls).To(BeZero())
		Expect(f.world.violations).To(BeEmpty())

		stored := f.storedBundle()
		Expect(stored.Secrets.SecretboxEncryptionSecret).To(Equal(f.old.Secrets.SecretboxEncryptionSecret))
		Expect(string(stored.Certs.K8sServiceAccount.Key)).NotTo(Equal(string(f.old.Certs.K8sServiceAccount.Key)))
		Expect(f.storedRetired().Keys[0].Secretbox).To(BeEmpty())
	})

	It("keeps repeating the sweep until one rewrites nothing", func() {
		f := newKeyRotFixture(fastBundle())
		f.world.sweeps = []ReencryptResult{{Scanned: 9, Rewritten: 5}, {Scanned: 9, Conflicts: 2}, {Scanned: 9}}

		op := f.newOp(boxOnly)
		op, _ = f.runUntil(op, atStage(ichorv1alpha1.KeyRotationStageDropping))

		Expect(f.world.sweepCalls).To(Equal(3))
		Expect(op.Status.KeyRotation.Reencryption.Sweeps).To(Equal(int32(3)))
	})

	It("holds the old secretbox key while a sweep cannot finish, and resumes when it can", func() {
		f := newKeyRotFixture(fastBundle())
		f.world.sweepErr = errors.New("kube-apiserver: context deadline exceeded")

		op := f.newOp(boxOnly)
		op, _ = f.runUntil(op, atStage(ichorv1alpha1.KeyRotationStageReencrypting))

		op = repeatStep(f.step, op, 4)

		Expect(rotationStage(op)).To(Equal(ichorv1alpha1.KeyRotationStageReencrypting))
		Expect(op.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		Expect(apimeta.FindStatusCondition(op.Status.Conditions, conditionKeyRotation).Message).To(ContainSubstring("old secretbox key stays"))

		f.world.sweepErr = nil
		f.runUntil(op, completed)
	})

	It("applies to one control plane at a time and waits for each kube-apiserver to restart and answer", func() {
		f := newKeyRotFixture(fastBundle())
		f.world.restartDelay = 3

		op := f.newOp(saOnly)
		op, _ = f.runUntil(op, atStage(ichorv1alpha1.KeyRotationStageAccepting))

		// Step until exactly one apply has happened: the next control plane waits for it.
		for len(f.world.applies) < 1 {
			op = f.step(op)
		}

		first := f.world.applies[0].node

		op = f.step(op)
		Expect(f.world.applies).To(HaveLen(1), "the next control plane was applied to while the first kube-apiserver was restarting")
		cond := apimeta.FindStatusCondition(op.Status.Conditions, conditionKeyRotation)
		Expect(cond.Reason).To(Equal(reasonKeyRotationRestarting))
		Expect(op.Status.KeyRotation.Restart).NotTo(BeNil())
		Expect(op.Status.KeyRotation.Restart.Machine).To(Equal(first))

		f.runUntil(op, completed)
		Expect(f.world.violations).To(BeEmpty())
	})

	It("never advances while a kube-apiserver does not restart after the apply", func() {
		f := newKeyRotFixture(fastBundle())
		f.world.noRestart = true

		op := f.newOp(saOnly)
		op, _ = f.runUntil(op, atStage(ichorv1alpha1.KeyRotationStageAccepting))

		op = repeatStep(f.step, op, 12)

		Expect(rotationStage(op)).To(Equal(ichorv1alpha1.KeyRotationStageAccepting))
		Expect(f.world.applies).To(HaveLen(1), "the other control planes were applied to although the first never restarted")
		cond := apimeta.FindStatusCondition(op.Status.Conditions, conditionKeyRotation)
		Expect(cond.Message).To(ContainSubstring("still runs in the container it ran before the apply"))
		Expect(cond.Message).To(ContainSubstring("last checked"), "a stalled controller must show when the message was last true")
		Expect(cond.Message).NotTo(ContainSubstring("refused"))

		// It goes on once the container is replaced.
		f.world.noRestart = false
		f.world.nodes[f.world.applies[0].node].instance++
		f.runUntil(op, completed)
	})

	// Hardware: a rotation stalled for 17 minutes under "still runs in the container
	// it ran before the apply" although the container had changed; the real cause was credentials refused.
	It("says so when the state of the kube-apiserver cannot be read because credentials were refused, not that the container is unchanged", func() {
		f := newKeyRotFixture(fastBundle())
		f.world.restartDelay = 3

		op := f.newOp(saOnly)
		op, _ = f.runUntil(op, atStage(ichorv1alpha1.KeyRotationStageAccepting))

		for op.Status.KeyRotation.Restart == nil {
			op = f.step(op)
		}

		victim := op.Status.KeyRotation.Restart.Machine

		for name, refused := range map[string]error{
			"the Talos API":      status.Error(codes.Unauthenticated, "certificate rejected"),
			"the Kubernetes API": apierrors.NewUnauthorized("the server has asked for the client to provide credentials"),
			"a wrapped refusal":  fmt.Errorf("listing static pod statuses: %w", status.Error(codes.PermissionDenied, "not allowed")),
		} {
			f.world.instanceErr[victim] = refused
			op = f.step(op)

			cond := apimeta.FindStatusCondition(op.Status.Conditions, conditionKeyRotation)
			Expect(cond.Message).To(ContainSubstring("refused the credentials"), name)
			Expect(cond.Message).To(ContainSubstring("not a kube-apiserver that has not restarted"), name)
			Expect(cond.Message).NotTo(ContainSubstring("still runs in the container"), name)
		}

		// An unrelated failure is reported as it is, without the claim.
		f.world.instanceErr[victim] = errors.New("rpc error: Unavailable")
		op = f.step(op)
		Expect(apimeta.FindStatusCondition(op.Status.Conditions, conditionKeyRotation).Message).NotTo(ContainSubstring("refused"))

		delete(f.world.instanceErr, victim)
		f.runUntil(op, completed)
	})

	It("does not call a missing kube-apiserver container an unchanged one", func() {
		f := newKeyRotFixture(fastBundle())
		f.world.restartDelay = 2

		op := f.newOp(saOnly)
		op, _ = f.runUntil(op, atStage(ichorv1alpha1.KeyRotationStageAccepting))

		for op.Status.KeyRotation.Restart == nil {
			op = f.step(op)
		}

		f.world.noInstance = true
		op = f.step(op)

		msg := apimeta.FindStatusCondition(op.Status.Conditions, conditionKeyRotation).Message
		Expect(msg).To(ContainSubstring("reports no kube-apiserver container"))
		Expect(msg).NotTo(ContainSubstring("still runs in the container"))

		f.world.noInstance = false
		f.runUntil(op, completed)
	})

	for _, stage := range []string{
		ichorv1alpha1.KeyRotationStageAccepting, ichorv1alpha1.KeyRotationStageIssuing, ichorv1alpha1.KeyRotationStageDropping,
	} {
		It("resumes "+stage+" after the controller dies in the middle of the pass, without repeating an apply", func() {
			f := newKeyRotFixture(fastBundle())
			f.world.restartDelay = 1
			op := f.newOp(bothTargets)

			op, _ = f.runUntil(op, atStage(stage))
			before := len(f.world.applies)

			// Die in the middle of the second control plane's apply, after its restart was recorded.
			f.world.failApply = func(_ string, n int) error {
				if n >= before+1 {
					return errors.New("the controller was killed here")
				}

				return nil
			}

			op, _ = f.runUntil(op, func(o *ichorv1alpha1.TalosOperation) bool {
				return o.Status.Phase == ichorv1alpha1.TalosOperationPhasePaused
			})
			Expect(rotationStage(op)).To(Equal(stage), "the stage must not advance on a half-done pass")
			Expect(op.Status.KeyRotation.Restart).NotTo(BeNil(), "the restart is recorded before the apply, so a dead controller finds it")

			// A brand-new controller picks the stage up from status.
			f.world.failApply = nil
			f.runUntil(op, completed)

			for rec, n := range f.applyCountPerNodeAndHash() {
				Expect(n).To(Equal(1), "%s received the same configuration twice across the restart", rec.node)
			}

			Expect(f.world.applies).To(HaveLen(9))
			Expect(f.world.violations).To(BeEmpty())
		})
	}

	It("resumes after the Secret was replaced but the operation never learned of it", func() {
		f := newKeyRotFixture(fastBundle())
		op := f.newOp(bothTargets)
		op, _ = f.runUntil(op, atStage(ichorv1alpha1.KeyRotationStageFinalizing))

		// What finalize does first, done by the "previous process" that then died.
		kr := f.storedKeys()
		replaced, err := yaml.Marshal(kr.Apply(f.old))
		Expect(err).NotTo(HaveOccurred())

		retired := &secretsbundle.Retired{}
		retired.Add(secretsbundle.RetiredKey{RetiredAt: time.Now().UTC(), Secretbox: f.old.Secrets.SecretboxEncryptionSecret})
		rawRetired, err := retired.Marshal()
		Expect(err).NotTo(HaveOccurred())

		editSecret(f.secret.Name, func(data map[string][]byte) {
			data[secretsbundle.SecretKey] = replaced
			data[secretsbundle.RetiredKeysKey] = rawRetired
			delete(data, secretsbundle.KeyRotationKey)
		})

		f.runUntil(op, completed)
		Expect(string(f.storedBundle().Certs.K8sServiceAccount.Key)).To(Equal(string(kr.ServiceAccount.Key)))
		Expect(f.storedRetired().Keys).To(HaveLen(1), "the retired keys must be recorded once")
	})

	It("adopts a keyrotation.yaml left by a start that died before any control plane was told", func() {
		f := newKeyRotFixture(fastBundle())

		kr, err := secretsbundle.NewKeyRotation(f.old, bothTargets)
		Expect(err).NotTo(HaveOccurred())

		raw, err := kr.Marshal()
		Expect(err).NotTo(HaveOccurred())

		editSecret(f.secret.Name, func(data map[string][]byte) { data[secretsbundle.KeyRotationKey] = raw })

		op := f.newOp(bothTargets)
		f.runUntil(op, completed)
		Expect(string(f.storedBundle().Certs.K8sServiceAccount.Key)).To(Equal(string(kr.ServiceAccount.Key)), "the saved keys must be the ones adopted, not regenerated")
		Expect(f.storedBundle().Secrets.SecretboxEncryptionSecret).To(Equal(kr.Secretbox))
	})

	It("refuses an adopted keyrotation.yaml that holds other keys than the operation names", func() {
		f := newKeyRotFixture(fastBundle())

		kr, err := secretsbundle.NewKeyRotation(f.old, boxOnly)
		Expect(err).NotTo(HaveOccurred())

		raw, _ := kr.Marshal()

		editSecret(f.secret.Name, func(data map[string][]byte) { data[secretsbundle.KeyRotationKey] = raw })

		op := f.step(f.newOp(bothTargets))
		Expect(rotationStage(op)).To(BeEmpty())
		Expect(op.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		Expect(f.world.applies).To(BeEmpty())
	})

	It("waits tokenSettle after the new key signs before dropping the old one, and keeps its clock across a restart", func() {
		f := newKeyRotFixture(fastBundle())
		op := f.newOpSpec(&ichorv1alpha1.TalosOperationKeyRotationSpec{Targets: saOnly, TokenSettle: &metav1.Duration{Duration: 2 * time.Hour}})

		op, _ = f.runUntil(op, atStage(ichorv1alpha1.KeyRotationStageSettlingTokens))
		applied := len(f.world.applies)

		op = repeatStep(f.step, op, 6)

		Expect(rotationStage(op)).To(Equal(ichorv1alpha1.KeyRotationStageSettlingTokens), "the old key was dropped before the tokens were re-issued")
		Expect(f.world.applies).To(HaveLen(applied))
		Expect(op.Status.KeyRotation.SettlingSince).NotTo(BeNil())
		Expect(apimeta.FindStatusCondition(op.Status.Conditions, conditionKeyRotation).Reason).To(Equal(reasonKeyRotationSettling))
		Expect(f.world.legacyCalls).To(BeZero(), "the legacy tokens are looked for once the wait is over")

		// The clock is in status: it moves back by the wait and the next reconcile, a new controller, carries on.
		since := metav1.NewTime(time.Now().Add(-3 * time.Hour))
		op.Status.KeyRotation.SettlingSince = &since
		Expect(k8sClient.Status().Update(ctx, op)).To(Succeed())

		f.runUntil(fetchOperation(op), completed)
		Expect(f.world.legacyCalls).To(Equal(1))
	})

	It("waits for token Secrets signed by the old key to be gone, naming them, unless told to ignore them", func() {
		f := newKeyRotFixture(fastBundle())
		f.world.legacy = []string{"b/ci-token", "a/old-robot"}

		op := f.newOp(saOnly)
		op, _ = f.runUntil(op, atStage(ichorv1alpha1.KeyRotationStageSettlingTokens))

		op = repeatStep(f.step, op, 4)

		Expect(rotationStage(op)).To(Equal(ichorv1alpha1.KeyRotationStageSettlingTokens))
		Expect(op.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))

		cond := apimeta.FindStatusCondition(op.Status.Conditions, conditionKeyRotation)
		Expect(cond.Reason).To(Equal(reasonKeyRotationLegacyTokens))
		Expect(cond.Message).To(ContainSubstring("a/old-robot, b/ci-token"))
		Expect(cond.Message).To(ContainSubstring("2 Secret(s)"))

		// It continues by itself once they are gone.
		f.world.legacy = nil
		f.runUntil(op, completed)
	})

	It("drops the old service-account key without looking for token Secrets when told to ignore them", func() {
		f := newKeyRotFixture(fastBundle())
		f.world.legacy = []string{"a/old-robot"}

		op := f.newOpSpec(&ichorv1alpha1.TalosOperationKeyRotationSpec{Targets: saOnly, TokenSettle: &metav1.Duration{}, IgnoreLegacyTokens: true})

		f.runUntil(op, completed)
		Expect(f.world.legacyCalls).To(BeZero())
	})

	It("does not advance, and pauses, while a control plane's kube-apiserver is not ready or Ichor cannot reach it", func() {
		f := newKeyRotFixture(fastBundle())
		op := f.newOp(saOnly)
		op, _ = f.runUntil(op, atStage(ichorv1alpha1.KeyRotationStageIssuing))

		victim := f.controlPlanes()[1].Name
		f.world.versionErr[victim] = errors.New("rpc error: Unavailable")

		op = repeatStep(f.step, op, 5)

		Expect(rotationStage(op)).To(Equal(ichorv1alpha1.KeyRotationStageIssuing), "an unverified stage must not advance")
		Expect(op.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))

		cond := apimeta.FindStatusCondition(op.Status.Conditions, conditionKeyRotation)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Message).To(ContainSubstring(victim))

		delete(f.world.versionErr, victim)
		f.runUntil(op, completed)
	})

	It("does not advance while a Kubernetes Node is not Ready", func() {
		f := newKeyRotFixture(fastBundle())
		op := f.newOp(saOnly)
		op, _ = f.runUntil(op, atStage(ichorv1alpha1.KeyRotationStageDropping))

		f.world.notReady = []string{"some-worker"}

		op = repeatStep(f.step, op, 4)

		Expect(rotationStage(op)).To(Equal(ichorv1alpha1.KeyRotationStageDropping))
		Expect(apimeta.FindStatusCondition(op.Status.Conditions, conditionKeyRotation).Message).To(ContainSubstring("some-worker"))

		f.world.notReady = nil
		f.runUntil(op, completed)
	})

	It("refuses to start while a control plane is down, and writes nothing", func() {
		f := newKeyRotFixture(fastBundle())
		f.world.nodes[f.controlPlanes()[2].Name].down = true

		op := f.newOp(bothTargets)
		op = repeatStep(f.step, op, 3)

		Expect(rotationStage(op)).To(BeEmpty())
		Expect(op.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		Expect(f.secretData()).NotTo(HaveKey(secretsbundle.KeyRotationKey), "no key may be generated for a rotation that cannot start")
		Expect(f.world.applies).To(BeEmpty())

		f.world.nodes[f.controlPlanes()[2].Name].down = false
		f.runUntil(op, completed)
	})

	DescribeTable("refuses to start, naming why, and writes nothing",
		func(setup func() (*keyRotFixture, []string), want string) {
			f, targets := setup()

			op := f.step(f.newOp(targets))
			Expect(rotationStage(op)).To(BeEmpty())
			Expect(apimeta.FindStatusCondition(op.Status.Conditions, conditionKeyRotation).Message).To(ContainSubstring(want))
			Expect(f.secretData()).NotTo(HaveKey(secretsbundle.KeyRotationKey))
			Expect(f.world.applies).To(BeEmpty())
		},
		Entry("a control plane does not run the configuration Ichor renders for it", func() (*keyRotFixture, []string) {
			f := newKeyRotFixture(fastBundle())
			node := f.world.nodes[f.controlPlanes()[0].Name]
			node.cfg = append(node.cfg, []byte("# edited by hand\n")...)

			return f, bothTargets
		}, "converge"),
		Entry("a kube-apiserver is not ready", func() (*keyRotFixture, []string) {
			f := newKeyRotFixture(fastBundle())
			f.world.nodes[f.controlPlanes()[1].Name].notReady = 100

			return f, bothTargets
		}, "not ready"),
		Entry("the contract keeps the keys in single fields, naming the contract that works", func() (*keyRotFixture, []string) {
			f := newKeyRotFixture(fastBundle())

			var cluster ichorv1alpha1.TalosCluster
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: tmNamespace, Name: f.cluster.Name}, &cluster)).To(Succeed())
			cluster.Spec.VersionContract = "v1.13"
			Expect(k8sClient.Update(ctx, &cluster)).To(Succeed())

			return f, bothTargets
		}, "v1.14"),
		Entry("a secretbox rotation of a bundle that has no secretbox secret", func() (*keyRotFixture, []string) {
			bare := *fastBundle()
			secs := *bare.Secrets
			secs.SecretboxEncryptionSecret = ""
			secs.AESCBCEncryptionSecret = "legacy-aescbc"
			bare.Secrets = &secs

			return newKeyRotFixture(&bare), boxOnly
		}, "no secretbox secret"),
	)

	It("keeps the machine controller from rendering, dialing or applying, and refuses other operations, while a rotation is under way", func() {
		f := newKeyRotFixture(fastBundle())
		op := f.newOp(bothTargets)
		op, _ = f.runUntil(op, atStage(ichorv1alpha1.KeyRotationStageIssuing))

		dials := 0
		mr := &TalosMachineReconciler{
			Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace,
			TalosClient: func(context.Context, *ichorv1alpha1.TalosCluster, *ichorv1alpha1.TalosMachine, *secrets.Bundle) (TalosClient, error) {
				dials++

				return &fakeTalosClient{}, nil
			},
		}

		for range 2 {
			reconcileOK(mr, f.machines[0])
		}

		Expect(dials).To(BeZero())
		Expect(readyCondition(fetchMachine(f.machines[0])).Reason).To(Equal(reasonKeyRotationInProgress))

		other := &ichorv1alpha1.TalosOperation{}
		other.GenerateName, other.Namespace = "key-other-", tmNamespace
		other.Spec.ClusterRef.Name, other.Spec.Kind = f.cluster.Name, ichorv1alpha1.TalosOperationKindTalos
		Expect(k8sClient.Create(ctx, other)).To(Succeed())

		advancer := &fakeAdvancer{}
		r := f.newReconciler()
		r.Advance = advancer.Advance

		mustReconcileOperation(r, other)
		Expect(fetchOperation(other).Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		expectPausedBecause(fetchOperation(other), reasonOperationKeyRotating)
		Expect(advancer.calls).To(BeEmpty())

		// Once the rotation is done the machine controller reconciles again, and finds no drift.
		f.runUntil(op, completed)

		mr.TalosClient = func(_ context.Context, _ *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, _ *secrets.Bundle) (TalosClient, error) {
			return &keyClient{w: f.world, name: m.Name, node: f.world.nodes[m.Name]}, nil
		}

		applied := len(f.world.applies)
		reconcileOK(mr, f.machines[0])
		Expect(f.world.applies).To(HaveLen(applied), "the machine controller applied something after the rotation")
		Expect(readyCondition(fetchMachine(f.machines[0])).Status).To(Equal(metav1.ConditionTrue))
	})

	It("holds the operation's deletion until the rotation is done", func() {
		f := newKeyRotFixture(fastBundle())
		op := f.newOp(bothTargets)
		op, _ = f.runUntil(op, atStage(ichorv1alpha1.KeyRotationStageIssuing))
		Expect(op.Finalizers).To(ContainElement(keyRotationFinalizer))

		Expect(k8sClient.Delete(ctx, op)).To(Succeed())

		op = f.step(op)
		Expect(op.DeletionTimestamp.IsZero()).To(BeFalse())
		Expect(rotationStage(op)).NotTo(BeEmpty(), "the stage is the only record of the nodes' state and must survive a delete")

		for range 200 {
			mustReconcileOperation(f.newReconciler(), op)

			var got ichorv1alpha1.TalosOperation

			if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: op.Namespace, Name: op.Name}, &got); err != nil {
				return // gone: the finalizer was released after the rotation finished
			}
		}

		Fail("the operation was never released after the rotation finished")
	})

	It("keeps older backups readable and clears BundleBackupStale only for an export that carries the retired keys", func() {
		f := newKeyRotFixture(fastBundle())

		// A backup sealed before the rotation, with the key derived from the bundle as it was.
		oldKey, err := etcdbackup.DeriveKey(f.old)
		Expect(err).NotTo(HaveOccurred())

		var sealed bytes.Buffer

		w, err := etcdbackup.Encrypt(&sealed, oldKey)
		Expect(err).NotTo(HaveOccurred())
		_, err = w.Write([]byte("etcd snapshot"))
		Expect(err).NotTo(HaveOccurred())
		Expect(w.Close()).To(Succeed())

		op := f.newOp(bothTargets)
		f.runUntil(op, completed)

		// The key backups are sealed with now is derived from the new secret; the old backup opens through the retired list.
		var scrub scrubber

		now, err := backupKey(ctx, k8sClient, tmNamespace, f.cluster, f.storedBundle(), &scrub)
		Expect(err).NotTo(HaveOccurred())
		Expect(now.Current).NotTo(Equal(oldKey.Current))

		r, err := etcdbackup.Decrypt(bytes.NewReader(sealed.Bytes()), now)
		Expect(err).NotTo(HaveOccurred(), "a backup sealed before the rotation must stay readable")
		got := new(bytes.Buffer)
		_, err = got.ReadFrom(r)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.String()).To(Equal("etcd snapshot"))

		// New backups are not sealed with a retired key.
		_, err = etcdbackup.Decrypt(bytes.NewReader(sealed.Bytes()), etcdbackup.Key{Current: now.Current, Legacy: now.Legacy})
		Expect(err).To(HaveOccurred())

		key := types.NamespacedName{Namespace: tmNamespace, Name: f.cluster.Name}
		expiry := &CertExpiryReconciler{Client: k8sClient, ControllerNamespace: tmNamespace}

		stale := func() bool {
			var got ichorv1alpha1.TalosCluster
			Expect(k8sClient.Get(ctx, key, &got)).To(Succeed())

			return apimeta.IsStatusConditionTrue(got.Status.Conditions, ichorv1alpha1.ConditionBundleBackupStale)
		}

		export := func(fingerprint string) {
			var got ichorv1alpha1.TalosCluster
			Expect(k8sClient.Get(ctx, key, &got)).To(Succeed())

			if got.Annotations == nil {
				got.Annotations = map[string]string{}
			}

			got.Annotations[ichorv1alpha1.BundleExportedAnnotation] = fingerprint
			Expect(k8sClient.Update(ctx, &got)).To(Succeed())

			_, err := expiry.Reconcile(ctx, ctrlRequest(key))
			Expect(err).NotTo(HaveOccurred())
		}

		Expect(stale()).To(BeTrue())

		// A copy of the bundle from before the rotation does not clear it, nor does the new bundle without the retired keys.
		before, err := secretsbundle.ExportFingerprint(f.old, &secretsbundle.Retired{})
		Expect(err).NotTo(HaveOccurred())
		export(before)
		Expect(stale()).To(BeTrue())

		retired := f.storedRetired()
		withoutRetired, err := secretsbundle.ExportFingerprint(f.storedBundle(), &secretsbundle.Retired{})
		Expect(err).NotTo(HaveOccurred())
		export(withoutRetired)
		Expect(stale()).To(BeTrue(), "an export without the retired keys cannot open the older backups")

		live, err := secretsbundle.ExportFingerprint(f.storedBundle(), retired)
		Expect(err).NotTo(HaveOccurred())
		export(live)
		Expect(stale()).To(BeFalse())
	})

	It("never puts bundle or key material into a status, an Event or an error", func() {
		f := newKeyRotFixture(fastBundle())
		f.world.dialLeak = ""

		op := f.newOp(bothTargets)
		op, _ = f.runUntil(op, atStage(ichorv1alpha1.KeyRotationStageIssuing))

		kr := f.storedKeys()
		material := append(bundleSecretMaterial(f.old), string(kr.ServiceAccount.Key), kr.Secretbox)

		// Make the next dial fail with the new keys in its error text.
		f.world.dialLeak = string(kr.ServiceAccount.Key) + " " + kr.Secretbox
		f.world.nodes[f.controlPlanes()[1].Name].down = true

		errs := make([]error, 0, 6)

		var snapshots []string

		snap := func() {
			j, err := json.Marshal(fetchOperation(op))
			Expect(err).NotTo(HaveOccurred())

			snapshots = append(snapshots, string(j))
		}

		for range 3 {
			_, err := reconcileOperation(f.newReconciler(), op)
			errs = append(errs, err)

			snap()
		}

		op = fetchOperation(op)

		f.world.dialLeak = ""
		f.world.nodes[f.controlPlanes()[1].Name].down = false
		f.world.versionErr[f.controlPlanes()[2].Name] = errors.New("echo " + string(f.old.Certs.K8sServiceAccount.Key) + f.old.Secrets.SecretboxEncryptionSecret)

		for range 3 {
			_, err := reconcileOperation(f.newReconciler(), op)
			errs = append(errs, err)

			snap()
		}

		delete(f.world.versionErr, f.controlPlanes()[2].Name)
		f.world.sweepErr = errors.New("sweep failed: " + kr.Secretbox)

		for range 40 {
			op = f.step(op)

			if rotationStage(op) == ichorv1alpha1.KeyRotationStageReencrypting {
				break
			}
		}

		for range 3 {
			_, err := reconcileOperation(f.newReconciler(), op)
			errs = append(errs, err)

			snap()
		}

		f.world.sweepErr = nil
		op, _ = f.runUntil(fetchOperation(op), completed)

		var cluster ichorv1alpha1.TalosCluster
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: tmNamespace, Name: f.cluster.Name}, &cluster)).To(Succeed())

		opJSON, err := json.Marshal(op)
		Expect(err).NotTo(HaveOccurred())

		clusterJSON, err := json.Marshal(cluster)
		Expect(err).NotTo(HaveOccurred())

		var events []string

		close(f.recorder.Events)
		for e := range f.recorder.Events {
			events = append(events, e)
		}

		everything := slices.Concat([]string{string(opJSON), string(clusterJSON)}, snapshots, events)
		for _, e := range errs {
			if e != nil {
				everything = append(everything, e.Error())
			}
		}

		// The injected errors reached a status message, scrubbed.
		Expect(strings.Join(snapshots, "\n")).To(ContainSubstring(redacted))

		for _, s := range material {
			if s == "" {
				continue
			}

			for _, where := range everything {
				Expect(strings.Contains(where, s)).To(BeFalse(), "key material leaked into a status, Event or error")
			}
		}
	})
})
