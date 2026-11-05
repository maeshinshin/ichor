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
	"context"
	"errors"
	"fmt"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	sx509 "github.com/siderolabs/crypto/x509"
	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	apimeta "k8s.io/apimachinery/pkg/api/meta"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/etcdbackup"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
	"github.com/maeshinshin/ichor/pkg/render"
)

// etcdRestart is one restart of the etcd service, with the etcd PKI the node's configuration gave it.
type etcdRestart struct {
	node    string
	trusted []string
	signer  string
}

// etcdCA reads the etcd CA field of the node's applied configuration.
func (n *rotNode) etcdCA() *sx509.PEMEncodedCertificateAndKey {
	provider, err := configloader.NewFromBytes(n.cfg)
	Expect(err).NotTo(HaveOccurred())

	if provider.Cluster() == nil || provider.Cluster().Etcd() == nil {
		return nil
	}

	return provider.Cluster().Etcd().CA()
}

// pki is what Talos does with that field: the leaf is made by the first certificate and the key.
func (n *rotNode) pki() (EtcdPKI, error) {
	ca := n.etcdCA()
	if ca == nil {
		return EtcdPKI{}, errors.New("the node has no etcd CA")
	}

	authority, err := sx509.NewCertificateAuthorityFromCertificateAndKey(ca)
	if err != nil {
		return EtcdPKI{}, err
	}

	pair, err := sx509.NewKeyPair(authority, sx509.CommonName(serviceEtcd), sx509.ECDSA(true))
	if err != nil {
		return EtcdPKI{}, err
	}

	return etcdPKIFrom(ca.Crt, sx509.NewCertificateAndKeyFromKeyPair(pair).Crt)
}

func (c *rotClient) EtcdPKI(context.Context) (EtcdPKI, error) {
	if c.w.pkiStale[c.name] {
		const previous = "the-previous-stage"

		return EtcdPKI{CAs: []string{previous}, ServerSignedBy: previous}, nil
	}

	return c.node.pki()
}

func (c *rotClient) EtcdMemberList(context.Context) ([]EtcdMember, error) {
	return make([]EtcdMember, c.w.memberCount), nil
}

func (c *rotClient) EtcdStatus(context.Context) (EtcdStatus, error) {
	if c.w.leader[c.name] {
		return EtcdStatus{MemberID: 1, LeaderID: 1}, nil
	}

	return EtcdStatus{MemberID: 1, LeaderID: 2}, nil
}

func (c *rotClient) EtcdForfeitLeadership(context.Context) error {
	c.w.calls = append(c.w.calls, "forfeit:"+c.name)
	c.w.leader[c.name] = false

	return nil
}

func (c *rotClient) ServiceState(context.Context, string) (string, bool, bool, error) {
	return talosServiceRunning, true, !c.w.etcdUnhealthy[c.name], nil
}

// ServiceRestart must never be called: Talos v1.14's etcd service has no restart operation through the
// API (hardware); a member loads a new CA with a node reboot.
func (c *rotClient) ServiceRestart(_ context.Context, id string) error {
	Fail("a CA rotation restarted the service " + id + " through the API; etcd can only be reloaded by a reboot")

	return nil
}

func (c *rotClient) NodeName(context.Context) (string, error) { return c.w.nodeNames[c.name], nil }

func (c *rotClient) BootID(context.Context) (string, error) {
	return fmt.Sprintf("boot-%d", c.w.bootCount[c.name]), nil
}

func (c *rotClient) Reboot(context.Context) error {
	pki, err := c.node.pki()
	Expect(err).NotTo(HaveOccurred())

	c.w.restarts = append(c.w.restarts, etcdRestart{node: c.name, trusted: pki.CAs, signer: pki.ServerSignedBy})
	c.w.calls = append(c.w.calls, "reboot:"+c.name)

	if !c.w.frozenBoot[c.name] {
		c.w.bootCount[c.name]++
	}

	if c.w.afterRestart != nil {
		c.w.afterRestart(c.name)
	}

	return nil
}

func fp(crt []byte) string {
	out, err := secretsbundle.Fingerprint(crt)
	Expect(err).NotTo(HaveOccurred())

	return out
}

// controlPlaneNames returns the control planes' names in the order the executor visits them.
func (f *caRotFixture) controlPlaneNames() []string {
	var names []string

	for _, m := range f.machines {
		if m.Spec.Role == roleControlPlane {
			names = append(names, m.Name)
		}
	}

	slices.Sort(names)

	return names
}

func (f *caRotFixture) appliedTo(name string) int {
	n := 0

	for _, a := range f.world.applies {
		if a.node == name {
			n++
		}
	}

	return n
}

// aggregatorOf reads a control plane's aggregator CA document: the issuing certificate and everything
// the API server trusts.
func (n *rotNode) aggregatorOf() (issuing []byte, trusted [][]byte) {
	provider, err := configloader.NewFromBytes(n.cfg)
	Expect(err).NotTo(HaveOccurred())

	doc := provider.K8sAggregatorCAConfig()
	Expect(doc).NotTo(BeNil())

	for _, a := range doc.AcceptedCAs() {
		trusted = append(trusted, a.Crt)
	}

	return doc.IssuingCA().Crt, trusted
}

var _ = Describe("TalosOperation ca-rotation of the etcd CA and the aggregator CA", func() {
	stagesOfEtcdOnly := []string{
		ichorv1alpha1.CARotationStageAccepting, ichorv1alpha1.CARotationStageIssuing, ichorv1alpha1.CARotationStageDropping,
		ichorv1alpha1.CARotationStageFinalizing, ichorv1alpha1.CARotationStageDone,
	}

	It("rotates the etcd CA alone through the three stages, rebooting each control plane once per stage and restarting no service", func() {
		f := newCARotFixture()
		f.targets = []string{ichorv1alpha1.CARotationTargetEtcd}
		op := f.newOp()

		op, _ = f.runUntil(op, atStage(ichorv1alpha1.CARotationStageAccepting))
		rot := f.storedRotation()
		Expect(rot.Targets()).To(Equal([]string{ichorv1alpha1.CARotationTargetEtcd}), "only the etcd CA is generated")
		Expect(op.Status.CARotation.Certificates).To(HaveLen(1))
		Expect(op.Status.CARotation.Certificates[0].Name).To(Equal(ichorv1alpha1.CARotationTargetEtcd))

		op, stages := f.runUntil(op, completed)
		Expect(stages).To(Equal(stagesOfEtcdOnly), "no credential hand-off: nothing the operator holds changes")
		Expect(op.Status.CARotation.EtcdRestarted).To(BeEmpty())
		Expect(op.Status.CARotation.EtcdRebooting).To(BeEmpty())

		oldFP, newFP := fp(f.old.Certs.Etcd.Crt), fp(rot.Etcd.Crt)
		cps := f.controlPlaneNames()
		Expect(f.world.restarts).To(HaveLen(3 * len(cps)))

		for stage, want := range []etcdRestart{
			{trusted: []string{oldFP, newFP}, signer: oldFP}, // Accepting: both trusted, the old CA still signs
			{trusted: []string{newFP, oldFP}, signer: newFP}, // Issuing: the new CA signs, both trusted
			{trusted: []string{newFP}, signer: newFP},        // Dropping: the new CA alone
		} {
			for i, name := range cps {
				got := f.world.restarts[stage*len(cps)+i]
				Expect(got.node).To(Equal(name), "control planes are rebooted one at a time, in order")
				Expect(got.trusted).To(Equal(want.trusted), "stage %d: the member loads the stage's trust", stage)
				Expect(got.signer).To(Equal(want.signer), "stage %d: the member loads the stage's signer", stage)
			}
		}

		// Nothing else was touched: the Talos API CA is as it was, workers were never applied to.
		f.nodesIssueAndAccept(f.old.Certs.OS.Crt)

		for _, m := range f.machines {
			if m.Spec.Role == roleWorker {
				Expect(f.appliedTo(m.Name)).To(BeZero(), "a worker has no etcd")
			}
		}

		// The bundle holds the new etcd CA and the same everything else, so the backup key is the same.
		stored := f.storedBundle()
		Expect(stored.Certs.Etcd.Crt).To(Equal(rot.Etcd.Crt))
		Expect(stored.Certs.OS.Crt).To(Equal(f.old.Certs.OS.Crt))
		Expect(stored.Certs.K8s.Crt).To(Equal(f.old.Certs.K8s.Crt))
		Expect(stored.Certs.K8sAggregator.Crt).To(Equal(f.old.Certs.K8sAggregator.Crt))
		Expect(stored.Cluster).To(Equal(f.old.Cluster))
		Expect(f.storedRotation()).To(BeNil())

		oldKey, err := etcdbackup.DeriveKey(f.old)
		Expect(err).NotTo(HaveOccurred())

		newKey, err := etcdbackup.DeriveKey(stored)
		Expect(err).NotTo(HaveOccurred())
		Expect(newKey).To(Equal(oldKey), "snapshots taken before the rotation stay readable")

		// Every control plane ends on exactly what the replaced bundle renders.
		for _, m := range f.machines {
			_, hash, err := render.Render(f.cluster, m, stored, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(render.Hash(f.world.nodes[m.Name].cfg)).To(Equal(hash))
		}

		cond := apimeta.FindStatusCondition(op.Status.Conditions, conditionCARotation)
		Expect(cond.Message).To(ContainSubstring("the etcd CAs are replaced"))
	})

	It("reboots no control plane before the node holds the etcd PKI of the stage", func() {
		f := newCARotFixture()
		f.targets = []string{ichorv1alpha1.CARotationTargetEtcd}
		f.world.pkiStale[f.controlPlaneNames()[0]] = true

		op := f.newOp()

		for range 10 {
			op = f.step(op)
		}

		Expect(f.world.restarts).To(BeEmpty())
		Expect(rotationStage(op)).To(Equal(ichorv1alpha1.CARotationStageAccepting))

		cond := apimeta.FindStatusCondition(op.Status.Conditions, conditionCARotation)
		Expect(cond.Message).To(ContainSubstring("does not hold the etcd PKI of this stage"))

		f.world.pkiStale = map[string]bool{}
		f.runUntil(op, completed)
		Expect(f.world.restarts).To(HaveLen(9))
	})

	It("does not start with an etcd that is missing a member, and saves no CA", func() {
		f := newCARotFixture()
		f.targets = []string{ichorv1alpha1.CARotationTargetEtcd}
		f.world.memberCount = 2

		op := f.newOp()
		for range 3 {
			op = f.step(op)
		}

		Expect(rotationStage(op)).To(BeEmpty())
		Expect(f.storedRotation()).To(BeNil())
		Expect(f.world.applies).To(BeEmpty())

		cond := apimeta.FindStatusCondition(op.Status.Conditions, conditionCARotation)
		Expect(cond.Message).To(ContainSubstring("etcd member(s)"))
	})

	It("is not started on two control planes (the cluster lock refuses an even count), saving and changing nothing", func() {
		f := newCARotFixtureCPs(2)
		f.targets = []string{ichorv1alpha1.CARotationTargetEtcd}

		op := f.newOp()
		for range 3 {
			op = f.step(op)
		}

		Expect(rotationStage(op)).To(BeEmpty())
		Expect(f.storedRotation()).To(BeNil())
		Expect(f.world.applies).To(BeEmpty())
		Expect(f.world.restarts).To(BeEmpty())

		Expect(op.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		Expect(op.Status.Conditions).To(ContainElement(HaveField("Message", ContainSubstring("even"))))
	})

	It("rotates the etcd CA of a single control plane", func() {
		f := newCARotFixtureCPs(1)
		f.targets = []string{ichorv1alpha1.CARotationTargetEtcd}

		_, _ = f.runUntil(f.newOp(), completed)
		Expect(f.world.restarts).To(HaveLen(3))
	})

	It("rotates the aggregator CA alone: accepted first, issuing second, and waits for the extension API servers", func() {
		f := newCARotFixture()
		f.targets = []string{ichorv1alpha1.CARotationTargetAggregator}
		f.aggregatorPublished = false

		op := f.newOp()
		op, _ = f.runUntil(op, atStage(ichorv1alpha1.CARotationStageAccepting))
		rot := f.storedRotation()
		Expect(rot.Targets()).To(Equal([]string{ichorv1alpha1.CARotationTargetAggregator}))

		// Accepted, but not published: the rotation stays in Accepting and says what it waits for.
		for range 8 {
			op = f.step(op)
		}

		Expect(rotationStage(op)).To(Equal(ichorv1alpha1.CARotationStageAccepting))
		Expect(op.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		Expect(apimeta.FindStatusCondition(op.Status.Conditions, conditionCARotation).Message).To(ContainSubstring(extensionAuthConfigMap))
		Expect(f.aggregatorChecks).NotTo(BeEmpty())
		Expect(f.aggregatorChecks[0]).To(Equal(fp(rot.Aggregator.Crt)))

		for _, name := range f.controlPlaneNames() {
			issuing, trusted := f.world.nodes[name].aggregatorOf()
			Expect(issuing).To(Equal(f.old.Certs.K8sAggregator.Crt))
			Expect(trusted).To(ConsistOf(f.old.Certs.K8sAggregator.Crt, rot.Aggregator.Crt))
		}

		f.aggregatorPublished = true
		op, stages := f.runUntil(op, completed)
		Expect(stages).To(Equal(stagesOfEtcdOnly))
		Expect(f.world.restarts).To(BeEmpty(), "etcd is not restarted for the aggregator CA")
		Expect(f.rootCAChecks).To(BeEmpty(), "the Kubernetes API CA is not rotated")
		Expect(apimeta.FindStatusCondition(op.Status.Conditions, conditionCARotation).Message).To(ContainSubstring("Kubernetes aggregator"))

		for _, name := range f.controlPlaneNames() {
			issuing, trusted := f.world.nodes[name].aggregatorOf()
			Expect(issuing).To(Equal(rot.Aggregator.Crt))
			Expect(trusted).To(Equal([][]byte{rot.Aggregator.Crt}))
		}

		Expect(f.storedBundle().Certs.K8sAggregator.Crt).To(Equal(rot.Aggregator.Crt))
		f.nodesIssueAndAccept(f.old.Certs.OS.Crt)
	})

	It("rotates all four CAs in one operation, with the credential hand-off between Accepting and Issuing", func() {
		f := newCARotFixture()
		f.targets = []string{
			ichorv1alpha1.CARotationTargetTalos, ichorv1alpha1.CARotationTargetKubernetes,
			ichorv1alpha1.CARotationTargetEtcd, ichorv1alpha1.CARotationTargetAggregator,
		}
		f.holdHandoff = true

		op := f.newOp()
		op, _ = f.runUntil(op, atStage(ichorv1alpha1.CARotationStageAwaitingCredentials))
		rot := f.storedRotation()
		Expect(rot.Targets()).To(HaveLen(4))
		Expect(op.Status.CARotation.Certificates).To(HaveLen(4))
		Expect(f.world.restarts).To(HaveLen(3), "the Accepting roll is done before the operator is asked for credentials")

		f.holdHandoff = false
		op, stages := f.runUntil(op, completed)
		Expect(stages).To(ContainElements(ichorv1alpha1.CARotationStageIssuing, ichorv1alpha1.CARotationStageDropping, ichorv1alpha1.CARotationStageDone))
		Expect(f.world.restarts).To(HaveLen(9))

		stored := f.storedBundle()
		Expect(stored.Certs.OS.Crt).To(Equal(rot.Talos.Crt))
		Expect(stored.Certs.K8s.Crt).To(Equal(rot.Kubernetes.Crt))
		Expect(stored.Certs.Etcd.Crt).To(Equal(rot.Etcd.Crt))
		Expect(stored.Certs.K8sAggregator.Crt).To(Equal(rot.Aggregator.Crt))

		Expect(apimeta.FindStatusCondition(op.Status.Conditions, conditionCARotation).Message).
			To(ContainSubstring("Talos API, Kubernetes API, etcd and Kubernetes aggregator CAs are replaced"))
	})

	It("keeps a rotation that holds the Talos and Kubernetes CAs only as before: no etcd call, no aggregator wait", func() {
		f := newCARotFixture()
		op := f.newOp()
		f.runUntil(op, completed)

		Expect(f.world.restarts).To(BeEmpty())
		Expect(f.aggregatorChecks).To(BeEmpty())
		Expect(f.storedBundle().Certs.Etcd.Crt).To(Equal(f.old.Certs.Etcd.Crt))
	})

	It("never puts the new etcd CA's key into a status message", func() {
		f := newCARotFixture()
		f.targets = []string{ichorv1alpha1.CARotationTargetEtcd}

		op := f.newOp()
		op, _ = f.runUntil(op, atStage(ichorv1alpha1.CARotationStageIssuing))

		// A node stops answering and the error echoes the new etcd CA's key.
		f.world.dialLeak = string(f.storedRotation().Etcd.Key)
		f.world.nodes[f.controlPlaneNames()[1]].down = true

		op = f.step(op)
		op = f.step(op)

		cond := apimeta.FindStatusCondition(op.Status.Conditions, conditionCARotation)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Message).NotTo(ContainSubstring("PRIVATE KEY"))
		Expect(cond.Message).To(ContainSubstring(redacted))
	})
})
