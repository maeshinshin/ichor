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
	stdx509 "crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/siderolabs/talos/pkg/machinery/role"
	yaml "go.yaml.in/yaml/v4"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/kubetrust"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
	"github.com/maeshinshin/ichor/pkg/render"
)

// rotNode is a fake Talos node that behaves like a real one in the one respect a CA rotation
// depends on: it serves a certificate from the CA in its applied machine configuration and accepts
// client certificates signed by that CA or by one of its accepted CAs. Ichor can reach it only if
// its dial trusts the serving CA and its client certificate is signed by an accepted one.
type rotNode struct {
	cfg  []byte
	down bool
	// instance numbers the kube-apiserver container; restartIn counts the reads of it left before the
	// restart an apply caused happens (rotWorld.apiRestartReads).
	instance, restartIn int
}

type applyRecord struct{ node, hash string }

type rotWorld struct {
	nodes    map[string]*rotNode
	notReady []string

	// applies is every ApplyNoReboot, in order.
	applies []applyRecord
	// rotationSavedAtFirstApply records whether the Secret held rotation.yaml when the first node
	// was told anything.
	rotationSavedAtFirstApply *bool
	secretName                string

	// failApply, when it returns an error for (node, nth apply overall), that apply fails.
	failApply func(node string, n int) error
	// versionErr injects a failure into Version for a node.
	versionErr map[string]error
	// dialLeak is text that every dial error echoes, as a real error could.
	dialLeak string
	// dials counts successful dials.
	dials int

	// apiRestartReads, when set, makes every node report its kube-apiserver container (APIServerInstance) and
	// restart it that many reads after an apply, as Talos does a while after a configuration change; apiNoRestart
	// reports a container that no apply replaces.
	apiRestartReads int
	apiNoRestart    bool

	// etcd fields (ADR 0120): restarts is every etcd service restart in order, with the PKI the node's
	// configuration gave it at that moment; etcdUnhealthy marks nodes whose etcd does not report healthy;
	// afterRestart runs after each restart; pkiStale makes a node report the PKI it had before the stage.
	restarts      []etcdRestart
	etcdUnhealthy map[string]bool
	afterRestart  func(node string)
	pkiStale      map[string]bool
	// memberCount is how many etcd members every node's member list reports.
	memberCount int
	// bootCount is each node's boot number (its boot ID is "boot-<n>"); frozenBoot keeps a node's boot ID
	// from changing when it is rebooted, as a reboot that has not happened yet; leader marks the node
	// that leads etcd; calls is every forfeit and reboot in order ("forfeit:<node>", "reboot:<node>").
	bootCount  map[string]int
	frozenBoot map[string]bool
	leader     map[string]bool
	nodeNames  map[string]string
	calls      []string
}

func parseCerts(crt []byte) []*stdx509.Certificate {
	var out []*stdx509.Certificate

	rest := crt

	for {
		var block *pem.Block

		block, rest = pem.Decode(rest)
		if block == nil {
			return out
		}

		if cert, err := stdx509.ParseCertificate(block.Bytes); err == nil {
			out = append(out, cert)
		}
	}
}

// nodeCAs returns the CA that signs the node's serving certificate and every CA whose client
// certificates it accepts.
func (n *rotNode) nodeCAs() (issuer *stdx509.Certificate, accepted []*stdx509.Certificate) {
	provider, err := configloader.NewFromBytes(n.cfg)
	Expect(err).NotTo(HaveOccurred())

	sec := provider.Machine().Security()
	issuer = parseCerts(sec.IssuingCA().Crt)[0]
	accepted = append(accepted, issuer)

	for _, a := range sec.AcceptedCAs() {
		accepted = append(accepted, parseCerts(a.Crt)...)
	}

	return issuer, accepted
}

func (w *rotWorld) factory(
	_ context.Context, _ *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, bundle *secrets.Bundle,
) (TalosClient, error) {
	node := w.nodes[m.Name]

	if node.down {
		return nil, fmt.Errorf("dialing Talos API: connection refused %s", w.dialLeak)
	}

	issuer, accepted := node.nodeCAs()

	// The dial trusts the server only if the serving CA is among the CAs it was given.
	trusted := false

	for _, c := range parseCerts(bundle.Certs.OS.Crt) {
		if c.Equal(issuer) {
			trusted = true
		}
	}

	if !trusted {
		return nil, fmt.Errorf("dialing Talos API: x509: certificate signed by unknown authority %s", w.dialLeak)
	}

	// The node accepts the client only if its certificate chains to an accepted CA.
	client, err := bundle.GenerateTalosAPIClientCertificate(role.MakeSet(role.Admin))
	if err != nil {
		return nil, err
	}

	leaf := parseCerts(client.Crt)[0]
	pool := stdx509.NewCertPool()

	for _, a := range accepted {
		pool.AddCert(a)
	}

	if _, err := leaf.Verify(stdx509.VerifyOptions{Roots: pool, KeyUsages: []stdx509.ExtKeyUsage{stdx509.ExtKeyUsageClientAuth}}); err != nil {
		return nil, fmt.Errorf("rpc error: tls: bad certificate %s", w.dialLeak)
	}

	w.dials++

	return &rotClient{w: w, name: m.Name, node: node}, nil
}

type rotClient struct {
	fakeTalosClient
	w    *rotWorld
	name string
	node *rotNode
}

func (c *rotClient) CurrentConfig(context.Context) ([]byte, error) { return c.node.cfg, nil }

func (c *rotClient) DryRunApply(_ context.Context, cfg []byte) ([]string, error) {
	if _, err := configloader.NewFromBytes(cfg); err != nil {
		return nil, fmt.Errorf("the node would refuse this configuration: %w", err)
	}

	return nil, nil
}

func (c *rotClient) ApplyNoReboot(ctx context.Context, cfg []byte) error {
	w := c.w
	if w.rotationSavedAtFirstApply == nil {
		var secret corev1.Secret
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: tmNamespace, Name: w.secretName}, &secret)).To(Succeed())

		_, saved := secret.Data[secretsbundle.RotationKey]
		w.rotationSavedAtFirstApply = &saved
	}

	if w.failApply != nil {
		if err := w.failApply(c.name, len(w.applies)); err != nil {
			return err
		}
	}

	w.applies = append(w.applies, applyRecord{node: c.name, hash: render.Hash(cfg)})
	c.node.cfg = bytes.Clone(cfg)
	c.node.restartIn = w.apiRestartReads

	return nil
}

func (c *rotClient) APIServerInstance(context.Context) (string, error) {
	n := c.node
	if c.w.apiRestartReads == 0 && !c.w.apiNoRestart {
		return "", nil
	}

	if n.restartIn > 0 && !c.w.apiNoRestart {
		if n.restartIn--; n.restartIn == 0 {
			n.instance++
		}
	}

	return fmt.Sprintf("containerd://%s-%d", c.name, n.instance), nil
}

func (c *rotClient) Version(context.Context) (string, error) {
	return testTalosVersion, c.w.versionErr[c.name]
}

// caRotFixture is a cluster of three control planes and two workers running their rendered
// configuration, plus a rotation operation.
type caRotFixture struct {
	cluster  *ichorv1alpha1.TalosCluster
	secret   *corev1.Secret
	old      *secrets.Bundle
	machines []*ichorv1alpha1.TalosMachine
	world    *rotWorld
	recorder *record.FakeRecorder
	// reconcilers counts how many fresh reconcilers ran: every step builds a new one, so nothing
	// the executor needs can live in memory.
	reconcilers int
	// holdHandoff keeps step from acknowledging the credential hand-off, as an operator who has not
	// run the CLI yet.
	holdHandoff bool
	// rootCAStale is what the fake kube-root-ca.crt check reports; rootCAChecks the fingerprints
	// it was asked about; settleDelay the reconciler's ClientSettleDelay (0 is the real default).
	rootCAStale  []string
	rootCAChecks []string
	settleDelay  time.Duration
	// nodeChecks counts the live Node reads the executor asked for.
	nodeChecks int
	// kube is the in-cluster API the restarts go through; nil leaves the reconciler without one.
	kube kubernetes.Interface
	// targets is spec.caRotation.targets of the operation newOp creates (empty: the default).
	targets []string
	// aggregatorPublished is what the fake extension-apiserver-authentication check reports;
	// aggregatorChecks the fingerprints it was asked about.
	aggregatorPublished bool
	aggregatorChecks    []string
	// aggregatorAuthPending is how many more times the fake reset of extension-apiserver-authentication
	// answers "not settled yet"; aggregatorAuthCalls the fingerprints it was asked about.
	aggregatorAuthPending int
	aggregatorAuthCalls   []string
}

func newCARotFixture() *caRotFixture { return newCARotFixtureCPs(3) }

// newCARotFixtureCPs is a cluster with the given number of control planes and two workers.
func newCARotFixtureCPs(controlPlanes int) *caRotFixture {
	f := &caRotFixture{recorder: record.NewFakeRecorder(100), settleDelay: time.Nanosecond, aggregatorPublished: true}
	f.old = newTestBundle("v1.14")
	f.secret = newTestSecret(f.old)
	// The CRD allows 1, 3 or 5 control planes: two machines are a three-machine cluster that lost one.
	count := controlPlanes
	if count == 2 {
		count = 3
	}

	f.cluster = newTestClusterWithCount(int32(count), f.secret.Name) //nolint:gosec // a handful
	f.world = &rotWorld{
		nodes: map[string]*rotNode{}, secretName: f.secret.Name, versionErr: map[string]error{},
		etcdUnhealthy: map[string]bool{}, pkiStale: map[string]bool{}, memberCount: controlPlanes,
		bootCount: map[string]int{}, frozenBoot: map[string]bool{}, leader: map[string]bool{}, nodeNames: map[string]string{},
	}

	for i := 1; i <= controlPlanes; i++ {
		f.machines = append(f.machines, newOpTestMachine(f.cluster.Name, roleControlPlane, fmt.Sprintf("rot-cp%d", i)))
	}

	for _, n := range []string{"w1", "w2"} {
		f.machines = append(f.machines, newOpTestMachine(f.cluster.Name, roleWorker, "rot-"+n))
	}

	for _, m := range f.machines {
		cfg, _, err := render.Render(f.cluster, m, f.old, nil, nil)
		Expect(err).NotTo(HaveOccurred())

		f.world.nodes[m.Name] = &rotNode{cfg: cfg}
	}

	return f
}

func (f *caRotFixture) newReconciler() *TalosOperationReconciler {
	f.reconcilers++

	return &TalosOperationReconciler{
		Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace,
		KubeClient:  f.kube,
		TalosClient: f.world.factory,
		NodesReady: func(context.Context, TalosClient) ([]string, error) {
			f.nodeChecks++

			return f.world.notReady, nil
		},
		Recorder: f.recorder,
		RootCAPublished: func(_ context.Context, _ TalosClient, fp string) ([]string, error) {
			f.rootCAChecks = append(f.rootCAChecks, fp)

			return f.rootCAStale, nil
		},
		AggregatorCAPublished: func(_ context.Context, _ TalosClient, fp string) (bool, error) {
			f.aggregatorChecks = append(f.aggregatorChecks, fp)

			return f.aggregatorPublished, nil
		},
		AggregatorAuthReset: func(_ context.Context, _ TalosClient, fp string) (bool, error) {
			f.aggregatorAuthCalls = append(f.aggregatorAuthCalls, fp)

			if f.aggregatorAuthPending > 0 {
				f.aggregatorAuthPending--

				return false, nil
			}

			return true, nil
		},
		ClientSettleDelay: f.settleDelay,
	}
}

func (f *caRotFixture) newOp() *ichorv1alpha1.TalosOperation {
	op := &ichorv1alpha1.TalosOperation{}
	op.GenerateName, op.Namespace = "rot-op-", tmNamespace
	op.Spec.ClusterRef.Name, op.Spec.Kind = f.cluster.Name, ichorv1alpha1.TalosOperationKindCARotation

	if len(f.targets) > 0 {
		op.Spec.CARotation = &ichorv1alpha1.TalosOperationCARotationSpec{Targets: f.targets}
	}

	Expect(k8sClient.Create(ctx, op)).To(Succeed())

	return op
}

// step runs one reconcile on a brand-new reconciler (a controller restarted between any two).
func (f *caRotFixture) step(op *ichorv1alpha1.TalosOperation) *ichorv1alpha1.TalosOperation {
	if !f.holdHandoff {
		f.acknowledge(op)
	}

	_, err := reconcileOperation(f.newReconciler(), op)
	Expect(err).NotTo(HaveOccurred())

	return fetchOperation(op)
}

// acknowledge does what the CLI does once it has written the operator's credentials: it sets the
// hand-off annotation on the operation, if the operation is waiting for it.
func (f *caRotFixture) acknowledge(op *ichorv1alpha1.TalosOperation) {
	cur := fetchOperation(op)
	if stageOf(cur) != ichorv1alpha1.CARotationStageAwaitingCredentials {
		return
	}

	base := cur.DeepCopy()
	if cur.Annotations == nil {
		cur.Annotations = map[string]string{}
	}

	cur.Annotations[ichorv1alpha1.CredentialsHandoffAnnotation] = ichorv1alpha1.CARotationHandoffToken(cur.Status.CARotation)
	Expect(k8sClient.Patch(ctx, cur, ctrlclient.MergeFrom(base))).To(Succeed())
}

func stageOf(op *ichorv1alpha1.TalosOperation) string {
	if op.Status.CARotation == nil {
		return ""
	}

	return op.Status.CARotation.Stage
}

// runUntil steps until done(op), at most 80 times, and returns every distinct stage seen in order.
func (f *caRotFixture) runUntil(op *ichorv1alpha1.TalosOperation, done func(*ichorv1alpha1.TalosOperation) bool) (*ichorv1alpha1.TalosOperation, []string) {
	var stages []string

	for range 80 {
		op = f.step(op)

		if s := stageOf(op); s != "" && (len(stages) == 0 || stages[len(stages)-1] != s) {
			stages = append(stages, s)
		}

		if done(op) {
			return op, stages
		}
	}

	Fail(fmt.Sprintf("the rotation did not reach the wanted state; stage %q phase %q, condition %v",
		stageOf(op), op.Status.Phase, apimeta.FindStatusCondition(op.Status.Conditions, conditionCARotation)))

	return op, stages
}

func atStage(stage string) func(*ichorv1alpha1.TalosOperation) bool {
	return func(op *ichorv1alpha1.TalosOperation) bool { return stageOf(op) == stage }
}

func completed(op *ichorv1alpha1.TalosOperation) bool {
	return op.Status.Phase == ichorv1alpha1.TalosOperationPhaseCompleted
}

func (f *caRotFixture) secretData() map[string][]byte {
	var secret corev1.Secret
	Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: tmNamespace, Name: f.secret.Name}, &secret)).To(Succeed())

	return secret.Data
}

func (f *caRotFixture) storedRotation() *secretsbundle.Rotation {
	raw, ok := f.secretData()[secretsbundle.RotationKey]
	if !ok {
		return nil
	}

	rot, err := secretsbundle.ParseRotation(raw)
	Expect(err).NotTo(HaveOccurred())

	return rot
}

func (f *caRotFixture) storedBundle() *secrets.Bundle {
	var bundle secrets.Bundle
	Expect(yaml.Unmarshal(f.secretData()[secretsbundle.SecretKey], &bundle)).To(Succeed())

	return &bundle
}

// nodesIssueAndAccept checks that every machine issues from the given Talos CA and accepts exactly the others.
func (f *caRotFixture) nodesIssueAndAccept(issuer []byte, accepted ...[]byte) {
	for name, node := range f.world.nodes {
		provider, err := configloader.NewFromBytes(node.cfg)
		Expect(err).NotTo(HaveOccurred())

		sec := provider.Machine().Security()
		Expect(sec.IssuingCA().Crt).To(Equal(issuer), "%s issues from the wrong CA", name)

		var got [][]byte
		for _, a := range sec.AcceptedCAs() {
			got = append(got, a.Crt)
		}

		Expect(got).To(HaveLen(len(accepted)), "%s accepted CAs", name)

		for _, a := range accepted {
			Expect(got).To(ContainElement(a), "%s misses an accepted CA", name)
		}
	}
}

func (f *caRotFixture) applyCountPerNodeAndHash() map[applyRecord]int {
	counts := map[applyRecord]int{}
	for _, a := range f.world.applies {
		counts[a]++
	}

	return counts
}

var _ = Describe("TalosOperation ca-rotation", func() {
	It("saves the new CAs' keys before any node is told to trust them", func() {
		f := newCARotFixture()
		op := f.newOp()

		// The first reconcile does the preflight and saves the new CAs; no node has been touched.
		op = f.step(op)
		Expect(stageOf(op)).To(Equal(ichorv1alpha1.CARotationStageAccepting))
		Expect(f.world.applies).To(BeEmpty(), "no node may be told anything in the reconcile that saves the CAs")

		rot := f.storedRotation()
		Expect(rot).NotTo(BeNil(), "rotation.yaml must exist before the first stage")
		Expect(rot.Talos.Key).NotTo(BeEmpty())
		Expect(rot.Kubernetes.Key).NotTo(BeEmpty())

		// And it is still there, unchanged, when the first node is told to trust the new CA.
		f.runUntil(op, atStage(ichorv1alpha1.CARotationStageIssuing))
		Expect(f.world.rotationSavedAtFirstApply).NotTo(BeNil())
		Expect(*f.world.rotationSavedAtFirstApply).To(BeTrue(), "the first node was told to trust a CA whose key was not yet saved")
		Expect(f.storedRotation().Talos.Key).To(Equal(rot.Talos.Key))
	})

	It("publishes only the new Kubernetes CA's certificate to the controller's trust file", func() {
		prev := kubetrust.TrustFile
		kubetrust.TrustFile = filepath.Join(GinkgoT().TempDir(), "trust", "kubernetes-ca.crt")
		DeferCleanup(func() { kubetrust.TrustFile = prev })

		f := newCARotFixture()
		op := f.step(f.newOp())
		f.runUntil(op, atStage(ichorv1alpha1.CARotationStageIssuing))

		got, err := os.ReadFile(kubetrust.TrustFile)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(got)).To(ContainSubstring(strings.TrimSpace(string(f.storedRotation().Kubernetes.Crt))))
		Expect(string(got)).NotTo(ContainSubstring("PRIVATE KEY"))
	})

	It("drops the extra trusted CA at the end once the projected ca.crt carries the new one, and not before", func() {
		prev := kubetrust.TrustFile
		kubetrust.TrustFile = filepath.Join(GinkgoT().TempDir(), "trust", "kubernetes-ca.crt")
		base := filepath.Join(GinkgoT().TempDir(), "ca.crt")
		Expect(os.WriteFile(base, []byte("old\n"), 0o600)).To(Succeed())
		applyTrustBase(kubetrust.Shared, base)
		DeferCleanup(func() {
			Expect(kubetrust.Shared.Clear(kubetrust.TrustFile)).To(Succeed())
			kubetrust.TrustFile = prev
		})

		f := newCARotFixture()
		op := f.step(f.newOp())
		op, _ = f.runUntil(op, atStage(ichorv1alpha1.CARotationStageIssuing))
		newCA := f.storedRotation().Kubernetes.Crt

		// Until the rotation is over the extra CA is kept.
		Expect(kubetrust.TrustFile).To(BeAnExistingFile())

		// The kubelet projects the new CA; the terminal step then drops the extra one.
		Expect(os.WriteFile(base, newCA, 0o600)).To(Succeed())
		op, _ = f.runUntil(op, completed)
		Expect(op.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseCompleted))
		Expect(kubetrust.TrustFile).NotTo(BeAnExistingFile())
	})

	It("waits after Accepting for the operator's credentials and proceeds only once they are acknowledged", func() {
		f := newCARotFixture()
		f.holdHandoff = true
		op := f.newOp()

		op, _ = f.runUntil(op, atStage(ichorv1alpha1.CARotationStageAwaitingCredentials))
		rot := f.storedRotation()
		f.nodesIssueAndAccept(f.old.Certs.OS.Crt, rot.Talos.Crt)

		applied := len(f.world.applies)

		// Many reconciles later, nothing has moved: the old CA still issues and nothing else was applied.
		for range 6 {
			op = f.step(op)
			Expect(stageOf(op)).To(Equal(ichorv1alpha1.CARotationStageAwaitingCredentials))
			Expect(op.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		}

		Expect(f.world.applies).To(HaveLen(applied), "no node may be told anything while the hand-off is pending")
		f.nodesIssueAndAccept(f.old.Certs.OS.Crt, rot.Talos.Crt)

		cond := apimeta.FindStatusCondition(op.Status.Conditions, conditionCARotation)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Reason).To(Equal(reasonCARotationAwaitingCredentials))
		Expect(cond.Message).To(ContainSubstring("ichor cluster credentials"))

		// An acknowledgement for some other rotation does not release this one.
		base := op.DeepCopy()
		op.Annotations = map[string]string{ichorv1alpha1.CredentialsHandoffAnnotation: "talos=other,kubernetes=other"}
		Expect(k8sClient.Patch(ctx, op, ctrlclient.MergeFrom(base))).To(Succeed())

		op = f.step(op)
		Expect(stageOf(op)).To(Equal(ichorv1alpha1.CARotationStageAwaitingCredentials))

		// The right one does.
		f.holdHandoff = false
		op = f.step(op)
		op = f.step(op)
		Expect(stageOf(op)).To(Equal(ichorv1alpha1.CARotationStageIssuing))
		Expect(op.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress))

		op, _ = f.runUntil(op, completed)
		Expect(stageOf(op)).To(Equal(ichorv1alpha1.CARotationStageDone))
	})

	It("keeps waiting across a controller restart, with the acknowledgement read from the operation", func() {
		f := newCARotFixture()
		f.holdHandoff = true
		op := f.newOp()

		op, _ = f.runUntil(op, atStage(ichorv1alpha1.CARotationStageAwaitingCredentials))
		before := f.reconcilers

		op = f.step(op)
		Expect(f.reconcilers).To(BeNumerically(">", before), "every step is a fresh reconciler")
		Expect(stageOf(op)).To(Equal(ichorv1alpha1.CARotationStageAwaitingCredentials))
	})

	It("rotates through every stage, and each stage's status names the issuing and the accepted CA", func() {
		f := newCARotFixture()
		op := f.newOp()

		var seen []string

		for _, stage := range []string{
			ichorv1alpha1.CARotationStageAccepting, ichorv1alpha1.CARotationStageAwaitingCredentials,
			ichorv1alpha1.CARotationStageIssuing, ichorv1alpha1.CARotationStageDropping, ichorv1alpha1.CARotationStageFinalizing,
		} {
			var s []string

			op, s = f.runUntil(op, atStage(stage))
			for _, st := range s {
				if len(seen) == 0 || seen[len(seen)-1] != st {
					seen = append(seen, st)
				}
			}

			rs := op.Status.CARotation
			Expect(rs.Certificates).To(HaveLen(2))

			for _, cert := range rs.Certificates {
				switch stage {
				case ichorv1alpha1.CARotationStageAccepting, ichorv1alpha1.CARotationStageAwaitingCredentials:
					Expect(cert.Issuing).To(Equal(cert.Previous))
					Expect(cert.Accepted).To(Equal([]string{cert.Next}))
				case ichorv1alpha1.CARotationStageIssuing:
					Expect(cert.Issuing).To(Equal(cert.Next))
					Expect(cert.Accepted).To(Equal([]string{cert.Previous}))
				default:
					Expect(cert.Issuing).To(Equal(cert.Next))
					Expect(cert.Accepted).To(BeEmpty())
				}
			}

			rot := f.storedRotation()
			switch stage {
			case ichorv1alpha1.CARotationStageAccepting:
				// Entering Accepting: nothing applied yet.
				Expect(f.world.applies).To(BeEmpty())
			case ichorv1alpha1.CARotationStageAwaitingCredentials, ichorv1alpha1.CARotationStageIssuing:
				// Accepting verified: every node issues from the old CA and accepts the new one.
				f.nodesIssueAndAccept(f.old.Certs.OS.Crt, rot.Talos.Crt)
			case ichorv1alpha1.CARotationStageDropping:
				f.nodesIssueAndAccept(rot.Talos.Crt, f.old.Certs.OS.Crt)
			case ichorv1alpha1.CARotationStageFinalizing:
				f.nodesIssueAndAccept(rot.Talos.Crt)
			}
		}

		Expect(seen).To(Equal([]string{
			ichorv1alpha1.CARotationStageAccepting, ichorv1alpha1.CARotationStageAwaitingCredentials,
			ichorv1alpha1.CARotationStageIssuing, ichorv1alpha1.CARotationStageDropping, ichorv1alpha1.CARotationStageFinalizing,
		}))

		rot := f.storedRotation()

		// The reconcile that reaches Done completes the operation too: nothing waits for a later one.
		op = f.step(op)
		Expect(stageOf(op)).To(Equal(ichorv1alpha1.CARotationStageDone))
		Expect(op.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseCompleted))

		// The Secret now holds the new CAs and nothing else of the rotation.
		Expect(f.secretData()).NotTo(HaveKey(secretsbundle.RotationKey))

		stored := f.storedBundle()
		Expect(stored.Certs.OS.Crt).To(Equal(rot.Talos.Crt))
		Expect(stored.Certs.OS.Key).To(Equal(rot.Talos.Key))
		Expect(stored.Certs.K8s.Crt).To(Equal(rot.Kubernetes.Crt))
		Expect(stored.Cluster.Secret).To(Equal(f.old.Cluster.Secret), "nothing the etcd backup key derives from may change")
		Expect(stored.Cluster.ID).To(Equal(f.old.Cluster.ID))
		Expect(stored.Certs.Etcd).To(Equal(f.old.Certs.Etcd))

		// Every node runs exactly what the replaced bundle renders: the machine controller sees no drift.
		for _, m := range f.machines {
			_, want, err := render.Render(f.cluster, m, stored, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(render.Hash(f.world.nodes[m.Name].cfg)).To(Equal(want))
		}

		// Nothing was applied twice, and every node was applied once per stage.
		for rec, n := range f.applyCountPerNodeAndHash() {
			Expect(n).To(Equal(1), "%s received the same configuration twice", rec.node)
		}

		Expect(f.world.applies).To(HaveLen(3 * len(f.machines)))

		// The lock is released.
		mr := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient}
		lock, err := mr.acquireDisruptiveLock(ctx, f.cluster, f.machines[0], "ca-rotation-test", false)
		Expect(err).NotTo(HaveOccurred())
		Expect(lock.Release(ctx, "ca-rotation-test")).To(Succeed())

		// The user's saved bundle is stale until it is exported again.
		var cluster ichorv1alpha1.TalosCluster
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: tmNamespace, Name: f.cluster.Name}, &cluster)).To(Succeed())
		Expect(apimeta.IsStatusConditionTrue(cluster.Status.Conditions, ichorv1alpha1.ConditionBundleBackupStale)).To(BeTrue())
		Expect(strings.Join(drainEvents(f.recorder), "\n")).To(ContainSubstring(ichorv1alpha1.ConditionBundleBackupStale))

		// The finalizer goes once the rotation is done.
		op = f.step(op)
		Expect(op.Finalizers).To(BeEmpty())
	})

	for _, stage := range []string{
		ichorv1alpha1.CARotationStageAccepting, ichorv1alpha1.CARotationStageIssuing, ichorv1alpha1.CARotationStageDropping,
	} {
		It("resumes "+stage+" after the controller dies halfway through the pass, without repeating an apply", func() {
			f := newCARotFixture()
			op := f.newOp()

			// Run until the pass of this stage has begun, then die after exactly two applies of it.
			op, _ = f.runUntil(op, atStage(stage))
			before := len(f.world.applies)
			f.world.failApply = func(_ string, n int) error {
				if n >= before+2 {
					return errors.New("the controller was killed here")
				}

				return nil
			}

			op, _ = f.runUntil(op, func(o *ichorv1alpha1.TalosOperation) bool {
				return len(f.world.applies) == before+2
			})
			Expect(stageOf(op)).To(Equal(stage), "the stage must not advance on a half-done pass")
			Expect(op.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))

			// A brand-new controller (every step builds one) picks the stage up from status.
			f.world.failApply = nil
			op, _ = f.runUntil(op, completed)

			for rec, n := range f.applyCountPerNodeAndHash() {
				Expect(n).To(Equal(1), "%s received the same configuration twice across the restart", rec.node)
			}

			Expect(f.world.applies).To(HaveLen(3 * len(f.machines)))
			Expect(f.storedBundle().Certs.OS.Crt).To(Equal(parseRotationCrt(f, op)))
		})
	}

	It("resumes after the Secret was replaced but the operation never learned of it", func() {
		f := newCARotFixture()
		op := f.newOp()
		op, _ = f.runUntil(op, atStage(ichorv1alpha1.CARotationStageFinalizing))

		// What finalize does first, done by the "previous process" that then died.
		rot := f.storedRotation()
		replaced, err := yaml.Marshal(rot.Apply(f.old))
		Expect(err).NotTo(HaveOccurred())

		var secret corev1.Secret
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: tmNamespace, Name: f.secret.Name}, &secret)).To(Succeed())
		secret.Data[secretsbundle.SecretKey] = replaced
		delete(secret.Data, secretsbundle.RotationKey)
		Expect(k8sClient.Update(ctx, &secret)).To(Succeed())

		f.runUntil(op, completed)
		Expect(f.storedBundle().Certs.OS.Crt).To(Equal(rot.Talos.Crt))
	})

	It("adopts a rotation.yaml left by a start that died before any node was told", func() {
		f := newCARotFixture()

		contract, err := coreconfig.ParseContractFromVersion("v1.14")
		Expect(err).NotTo(HaveOccurred())
		rot, err := secretsbundle.NewRotation(f.old.Clock.Now(), contract)
		Expect(err).NotTo(HaveOccurred())

		raw, err := rot.Marshal()
		Expect(err).NotTo(HaveOccurred())

		var secret corev1.Secret
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: tmNamespace, Name: f.secret.Name}, &secret)).To(Succeed())
		secret.Data[secretsbundle.RotationKey] = raw
		Expect(k8sClient.Update(ctx, &secret)).To(Succeed())

		op := f.newOp()
		f.runUntil(op, completed)
		Expect(f.storedBundle().Certs.OS.Crt).To(Equal(rot.Talos.Crt), "the saved CAs must be the ones adopted, not regenerated")
	})

	It("does not advance, and pauses, while Ichor cannot reach a machine with the stage's credentials", func() {
		f := newCARotFixture()
		op := f.newOp()
		op, _ = f.runUntil(op, atStage(ichorv1alpha1.CARotationStageIssuing))

		victim := f.machines[3].Name
		f.world.versionErr[victim] = errors.New("rpc error: Unavailable")

		op = repeatStep(f.step, op, 5)

		Expect(stageOf(op)).To(Equal(ichorv1alpha1.CARotationStageIssuing), "an unverified stage must not advance")
		Expect(op.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))

		cond := apimeta.FindStatusCondition(op.Status.Conditions, conditionCARotation)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Message).To(ContainSubstring(victim))

		// It resumes by itself once the machine answers again.
		delete(f.world.versionErr, victim)
		f.runUntil(op, completed)
	})

	It("does not drop the old CAs until kube-root-ca.crt carries the new Kubernetes CA, then lets pods settle", func() {
		f := newCARotFixture()
		f.rootCAStale = []string{"argocd", "kube-system"}
		op := f.newOp()

		op, _ = f.runUntil(op, atStage(ichorv1alpha1.CARotationStageIssuing))
		op = repeatStep(f.step, op, 8)

		Expect(stageOf(op)).To(Equal(ichorv1alpha1.CARotationStageIssuing), "Dropping started while pods lack the new CA")
		cond := apimeta.FindStatusCondition(op.Status.Conditions, conditionCARotation)
		Expect(cond.Message).To(ContainSubstring("kube-root-ca.crt"))
		Expect(cond.Message).To(ContainSubstring("argocd"))
		Expect(f.rootCAChecks).NotTo(BeEmpty())
		Expect(f.rootCAChecks[0]).To(Equal(op.Status.CARotation.Certificates[1].Next), "asked about the new Kubernetes CA")

		f.rootCAStale = nil
		op = f.step(op)
		Expect(stageOf(op)).To(Equal(ichorv1alpha1.CARotationStageIssuing), "the settle time must pass before Dropping")
		Expect(apimeta.FindStatusCondition(op.Status.Conditions, conditionCARotation).Reason).To(Equal(reasonCARotationSettling))

		op, _ = f.runUntil(op, completed)
		Expect(stageOf(op)).To(Equal(ichorv1alpha1.CARotationStageDone))
	})

	It("does not advance while a Kubernetes Node is not Ready", func() {
		f := newCARotFixture()
		op := f.newOp()
		op, _ = f.runUntil(op, atStage(ichorv1alpha1.CARotationStageDropping))

		f.world.notReady = []string{"worker-1"}

		op = repeatStep(f.step, op, 4)

		Expect(stageOf(op)).To(Equal(ichorv1alpha1.CARotationStageDropping))
		Expect(apimeta.FindStatusCondition(op.Status.Conditions, conditionCARotation).Message).To(ContainSubstring("worker-1"))

		f.world.notReady = nil
		f.runUntil(op, completed)
	})

	It("refuses to start while a machine is down, and writes nothing", func() {
		f := newCARotFixture()
		f.world.nodes[f.machines[4].Name].down = true

		op := f.newOp()
		op = repeatStep(f.step, op, 3)

		Expect(stageOf(op)).To(BeEmpty())
		Expect(op.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		Expect(f.secretData()).NotTo(HaveKey(secretsbundle.RotationKey), "no CA may be generated for a rotation that cannot start")
		Expect(f.world.applies).To(BeEmpty())

		f.world.nodes[f.machines[4].Name].down = false
		f.runUntil(op, completed)
	})

	It("refuses to start while a machine does not run the configuration Ichor renders for it", func() {
		f := newCARotFixture()

		node := f.world.nodes[f.machines[0].Name]
		node.cfg = append(node.cfg, []byte("# edited by hand\n")...)

		op := f.newOp()
		op = f.step(op)

		Expect(stageOf(op)).To(BeEmpty())
		Expect(apimeta.FindStatusCondition(op.Status.Conditions, conditionCARotation).Message).To(ContainSubstring("converge"))
		Expect(f.secretData()).NotTo(HaveKey(secretsbundle.RotationKey))
	})

	It("keeps the machine controller from rendering, dialing or applying while a rotation is under way", func() {
		f := newCARotFixture()
		op := f.newOp()
		op, _ = f.runUntil(op, atStage(ichorv1alpha1.CARotationStageIssuing))

		dials := 0
		mr := &TalosMachineReconciler{
			Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace,
			TalosClient: func(context.Context, *ichorv1alpha1.TalosCluster, *ichorv1alpha1.TalosMachine, *secrets.Bundle) (TalosClient, error) {
				dials++

				return &fakeTalosClient{}, nil
			},
		}

		// A control plane's first reconcile only adds its removal finalizer.
		for range 2 {
			reconcileOK(mr, f.machines[0])
		}

		Expect(dials).To(BeZero())
		Expect(readyCondition(fetchMachine(f.machines[0])).Reason).To(Equal(reasonCARotationInProgress))

		// Another disruptive operation is refused too.
		other := &ichorv1alpha1.TalosOperation{}
		other.GenerateName, other.Namespace = "rot-other-", tmNamespace
		other.Spec.ClusterRef.Name, other.Spec.Kind = f.cluster.Name, ichorv1alpha1.TalosOperationKindTalos
		Expect(k8sClient.Create(ctx, other)).To(Succeed())

		advancer := &fakeAdvancer{}
		r := f.newReconciler()
		r.Advance = advancer.Advance

		mustReconcileOperation(r, other)
		Expect(fetchOperation(other).Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		Expect(advancer.calls).To(BeEmpty())

		// Once the rotation is done the machine controller reconciles again, and finds no drift.
		f.runUntil(op, completed)

		mr.TalosClient = func(_ context.Context, _ *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, _ *secrets.Bundle) (TalosClient, error) {
			return &rotClient{w: f.world, name: m.Name, node: f.world.nodes[m.Name]}, nil
		}

		applied := len(f.world.applies)
		reconcileOK(mr, f.machines[0])
		Expect(f.world.applies).To(HaveLen(applied), "the machine controller applied something after the rotation")
		Expect(readyCondition(fetchMachine(f.machines[0])).Status).To(Equal(metav1.ConditionTrue))
	})

	It("holds the operation's deletion until the rotation is done", func() {
		f := newCARotFixture()
		op := f.newOp()
		op, _ = f.runUntil(op, atStage(ichorv1alpha1.CARotationStageIssuing))
		Expect(op.Finalizers).To(ContainElement(caRotationFinalizer))

		Expect(k8sClient.Delete(ctx, op)).To(Succeed())

		op = f.step(op)
		Expect(op.DeletionTimestamp.IsZero()).To(BeFalse())
		Expect(stageOf(op)).NotTo(BeEmpty(), "the stage is the only record of the nodes' state and must survive a delete")

		for range 80 {
			_, err := reconcileOperation(f.newReconciler(), op)
			Expect(err).NotTo(HaveOccurred())

			var got ichorv1alpha1.TalosOperation

			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: op.Namespace, Name: op.Name}, &got)
			if err != nil {
				return // gone: the finalizer was released after the rotation finished
			}
		}

		Fail("the operation was never released after the rotation finished")
	})

	It("never puts bundle or CA material into a status, an Event or an error", func() {
		f := newCARotFixture()
		op := f.newOp()
		op, _ = f.runUntil(op, atStage(ichorv1alpha1.CARotationStageIssuing))

		rot := f.storedRotation()
		material := append(bundleSecretMaterial(f.old), string(rot.Talos.Key), string(rot.Kubernetes.Key),
			string(rot.Talos.Crt), string(rot.Kubernetes.Crt))

		// Make the next dial fail with the new CA key in its error text.
		f.world.dialLeak = string(rot.Talos.Key)
		f.world.nodes[f.machines[1].Name].down = true

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
		f.world.nodes[f.machines[1].Name].down = false
		f.world.versionErr[f.machines[2].Name] = errors.New("echo " + string(f.old.Certs.OS.Key))

		for range 3 {
			_, err := reconcileOperation(f.newReconciler(), op)
			errs = append(errs, err)

			snap()
		}

		delete(f.world.versionErr, f.machines[2].Name)
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
				Expect(strings.Contains(where, s)).To(BeFalse(), "secret material leaked into a status, Event or error")
			}
		}
	})
})

// parseRotationCrt returns the new Talos CA certificate the operation's status says it moved to,
// as the bytes the replaced bundle must carry.
func parseRotationCrt(f *caRotFixture, op *ichorv1alpha1.TalosOperation) []byte {
	var want string

	for _, c := range op.Status.CARotation.Certificates {
		if c.Name == talosCAName {
			want = c.Next
		}
	}

	got, err := secretsbundle.Fingerprint(f.storedBundle().Certs.OS.Crt)
	Expect(err).NotTo(HaveOccurred())
	Expect(got).To(Equal(want))

	return f.storedBundle().Certs.OS.Crt
}
