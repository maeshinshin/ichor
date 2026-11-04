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

// The etcd CA and the aggregator CA in the CA rotation (ADR 0120): which CAs a rotation replaces, the
// etcd member restarts after each pass, and the wait for extension API servers before the front-proxy
// certificate changes CA.
package controller

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/siderolabs/crypto/x509"
	"github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
)

const (
	// serviceEtcd is the Talos service id of etcd.
	serviceEtcd = "etcd"

	reasonCARotationRestartingEtcd     = "RestartingEtcd"
	reasonCARotationSettlingAggregator = "SettlingAggregator"

	// extensionAuthConfigMap is where kube-apiserver publishes the CAs extension API servers must accept
	// for requests it proxies (its --requestheader-client-ca-file).
	extensionAuthConfigMap = "extension-apiserver-authentication"
	extensionAuthCAKey     = "requestheader-client-ca-file"
)

// etcdRestartSettle is how long after one etcd member was restarted the next health check waits:
// Talos reports the old process healthy until it has stopped. A variable so tests need not wait.
var etcdRestartSettle = 15 * time.Second

// AggregatorCAPublishedFunc reports whether kube-apiserver already publishes the CA with the given
// fingerprint to extension API servers (the ConfigMap kube-system/extension-apiserver-authentication).
// A seam so tests never reach a cluster; cmd/main.go wires NewAggregatorCAPublished.
type AggregatorCAPublishedFunc func(ctx context.Context, talos TalosClient, fingerprint string) (published bool, err error)

// NewAggregatorCAPublished returns the AggregatorCAPublishedFunc cmd/main.go wires in. It reads the
// ConfigMap through the admin kubeconfig a Talos node issues, as NewRootCAPublished does.
func NewAggregatorCAPublished() AggregatorCAPublishedFunc {
	return func(ctx context.Context, talos TalosClient, fingerprint string) (bool, error) {
		cfg, err := restConfig(ctx, talos)
		if err != nil {
			return false, err
		}

		cs, err := kubernetes.NewForConfig(cfg)
		if err != nil {
			return false, fmt.Errorf("building a clientset from the admin kubeconfig: %w", err)
		}

		cm, err := cs.CoreV1().ConfigMaps(kubeSystemNamespace).Get(ctx, extensionAuthConfigMap, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("reading %s: %w", extensionAuthConfigMap, err)
		}

		return pemBundleHolds([]byte(cm.Data[extensionAuthCAKey]), fingerprint), nil
	}
}

func crtOrNil(ca *x509.PEMEncodedCertificateAndKey) []byte {
	if ca == nil {
		return nil
	}

	return ca.Crt
}

// rotationMaterial lists every secret string a rotation holds, for the scrubber.
func rotationMaterial(rot *secretsbundle.Rotation) []string {
	var out []string

	collectStrings(reflect.ValueOf(rot), &out)

	return out
}

// requestedTargets are the CAs spec.caRotation asks for; absent means Talos and Kubernetes.
func (c *caRun) requestedTargets() []string {
	if s := c.op.Spec.CARotation; s != nil && len(s.Targets) > 0 {
		return slices.Clone(s.Targets)
	}

	return ichorv1alpha1.DefaultCARotationTargets()
}

// hasTarget reports whether this rotation replaces the CA. Once the rotation has started the answer
// is the status's certificates (written with the first stage and never changed); before that, the
// new CAs saved for it or, failing that, the request.
func (c *caRun) hasTarget(name string) bool {
	if rs := c.op.Status.CARotation; rs != nil && len(rs.Certificates) > 0 {
		return slices.ContainsFunc(rs.Certificates, func(cert ichorv1alpha1.CARotationCertificate) bool { return cert.Name == name })
	}

	if c.rot != nil {
		return slices.Contains(c.rot.Targets(), name)
	}

	return slices.Contains(c.requestedTargets(), name)
}

// caPair is one CA being replaced: the certificate the nodes run now and the new one.
type caPair struct {
	name      string
	old, next []byte
}

// pairs lists the CAs this rotation replaces, in Rotation.Targets' order.
func (c *caRun) pairs() ([]caPair, error) {
	var out []caPair

	for _, n := range []struct {
		name string
		next func() []byte
	}{
		{talosCAName, func() []byte { return crtOrNil(c.rot.Talos) }},
		{kubernetesCAName, func() []byte { return crtOrNil(c.rot.Kubernetes) }},
		{etcdCAName, func() []byte { return crtOrNil(c.rot.Etcd) }},
		{aggregatorCAName, func() []byte { return crtOrNil(c.rot.Aggregator) }},
	} {
		next := n.next()
		if next == nil {
			continue
		}

		old := bundleCrt(c.bundle, n.name)
		if old == nil {
			return nil, fmt.Errorf("the secrets bundle has no %s CA to replace", n.name)
		}

		out = append(out, caPair{name: n.name, old: old, next: next})
	}

	return out, nil
}

// bundleCrt is the certificate of the named CA in bundle; nil when the bundle has none.
func bundleCrt(bundle *secrets.Bundle, name string) []byte {
	if bundle == nil || bundle.Certs == nil {
		return nil
	}

	switch name {
	case talosCAName:
		return crtOrNil(bundle.Certs.OS)
	case kubernetesCAName:
		return crtOrNil(bundle.Certs.K8s)
	case etcdCAName:
		return crtOrNil(bundle.Certs.Etcd)
	case aggregatorCAName:
		return crtOrNil(bundle.Certs.K8sAggregator)
	}

	return nil
}

// nextStage is the stage that follows a verified one. The credential hand-off sits between Accepting
// and Issuing only when the operator's talosconfig or kubeconfig depends on a CA being replaced.
func (c *caRun) nextStage(stage string) string {
	if stage == ichorv1alpha1.CARotationStageAccepting && !c.hasTarget(talosCAName) && !c.hasTarget(kubernetesCAName) {
		return ichorv1alpha1.CARotationStageIssuing
	}

	return nextStage(stage)
}

// checkTargets refuses a rotation the cluster cannot do safely, before anything is saved or changed.
func (c *caRun) checkTargets(targets []string, machines []*ichorv1alpha1.TalosMachine) error {
	contract, err := config.ParseContractFromVersion(c.cluster.Spec.VersionContract)
	if err != nil {
		return errors.New("invalid versionContract")
	}

	if slices.Contains(targets, aggregatorCAName) && !contract.MultidocKubernetesConfigSupported() {
		return fmt.Errorf("the aggregator CA has an accepted list only from version contract v1.14, and the cluster has %s",
			c.cluster.Spec.VersionContract)
	}

	if slices.Contains(targets, etcdCAName) && len(controlPlanes(machines)) == 2 {
		return errors.New("a two-member etcd has no quorum while one member restarts, so its CA is not rotated; " +
			"add a control plane or run with one")
	}

	return nil
}

// controlPlanes returns the control planes of machines, in the order given.
func controlPlanes(machines []*ichorv1alpha1.TalosMachine) []*ichorv1alpha1.TalosMachine {
	var out []*ichorv1alpha1.TalosMachine

	for _, m := range machines {
		if m.Spec.Role == roleControlPlane {
			out = append(out, m)
		}
	}

	return out
}

// etcdExpectation is the etcd PKI a stage's configuration gives a node.
type etcdExpectation struct {
	issuing string
	trusted []string
}

// etcdExpected reads, from the rotation's status, the etcd CAs the stage's nodes must trust and sign with.
func (c *caRun) etcdExpected() *etcdExpectation {
	for _, cert := range c.op.Status.CARotation.Certificates {
		if cert.Name == etcdCAName {
			return &etcdExpectation{issuing: cert.Issuing, trusted: append([]string{cert.Issuing}, cert.Accepted...)}
		}
	}

	return nil
}

// etcdHealthy fails unless etcd can lose one member: on every control plane the service is Running and
// healthy, the member answers and sees a leader, the member list has one member per control plane,
// kube-apiserver is ready and no alarm is set. With want, it also requires that the node's etcd PKI is
// the one the stage gives it (Talos picked up the configuration), so a member is restarted only to
// load what it is meant to.
func (c *caRun) etcdHealthy(
	ctx context.Context, cps []*ichorv1alpha1.TalosMachine, bundle *secrets.Bundle, want *etcdExpectation,
) error {
	for _, m := range cps {
		err := c.withNode(ctx, m, bundle, func(ctx context.Context, talos TalosClient) error {
			state, hasHealth, healthy, err := talos.ServiceState(ctx, serviceEtcd)
			if err != nil {
				return fmt.Errorf("reading the etcd service: %w", err)
			}

			if state != talosServiceRunning || (hasHealth && !healthy) {
				return fmt.Errorf("etcd is %s on %q and not healthy yet", state, m.Name)
			}

			st, err := talos.EtcdStatus(ctx)
			if err != nil {
				return fmt.Errorf("reading the etcd member of %q: %w", m.Name, err)
			}

			if st.LeaderID == 0 {
				return fmt.Errorf("the etcd member of %q sees no leader", m.Name)
			}

			members, err := talos.EtcdMemberList(ctx)
			if err != nil {
				return fmt.Errorf("listing the etcd members from %q: %w", m.Name, err)
			}

			if len(members) != len(cps) {
				return fmt.Errorf("%q sees %d etcd member(s) and the cluster has %d control plane(s)", m.Name, len(members), len(cps))
			}

			alarms, err := talos.EtcdAlarms(ctx)
			if err != nil {
				return fmt.Errorf("reading the etcd alarms from %q: %w", m.Name, err)
			}

			if len(alarms) > 0 {
				return fmt.Errorf("etcd reports the alarm(s) %v", alarms)
			}

			if err := talos.APIServerReady(ctx); err != nil {
				return fmt.Errorf("kube-apiserver of %q is not ready: %w", m.Name, err)
			}

			if want == nil {
				return nil
			}

			pki, err := talos.EtcdPKI(ctx)
			if err != nil {
				return fmt.Errorf("reading the etcd PKI of %q: %w", m.Name, err)
			}

			if !sameSet(pki.CAs, want.trusted) || pki.ServerSignedBy != want.issuing {
				return fmt.Errorf("%q does not hold the etcd PKI of this stage yet (trusts %d CA(s), expected %d)",
					m.Name, len(pki.CAs), len(want.trusted))
			}

			return nil
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func sameSet(a, b []string) bool {
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)

	return slices.Equal(x, y)
}

// rollEtcd restarts the etcd member of each control plane, once and one at a time, after a pass has
// put the stage's etcd CA field on every machine, so that each member loads the trust and the
// certificates of the stage (ADR 0120). wait is true while a member is being waited for or the roll
// is blocked; it is false once every member has been restarted and etcd is healthy again.
func (c *caRun) rollEtcd(
	ctx context.Context, stage string, machines []*ichorv1alpha1.TalosMachine, bundle *secrets.Bundle,
) (res ctrl.Result, wait bool, err error) {
	if !c.hasTarget(etcdCAName) {
		return ctrl.Result{}, false, nil
	}

	cps := controlPlanes(machines)
	rs := c.op.Status.CARotation

	if at := rs.EtcdRestartedAt; at != nil {
		if left := etcdRestartSettle - time.Since(at.Time); left > 0 {
			return ctrl.Result{RequeueAfter: left}, true, nil
		}
	}

	if err := c.etcdHealthy(ctx, cps, bundle, c.etcdExpected()); err != nil {
		res, rerr := c.retry(ctx, fmt.Sprintf("stage %s: etcd is not ready for the next step, so no member is restarted: %v", stage, err))

		return res, true, rerr
	}

	var next *ichorv1alpha1.TalosMachine

	for _, m := range cps {
		if !slices.Contains(rs.EtcdRestarted, m.Name) {
			next = m

			break
		}
	}

	if next == nil {
		return ctrl.Result{}, false, nil
	}

	err = c.withNode(ctx, next, bundle, func(ctx context.Context, talos TalosClient) error {
		// A leader that restarts costs an election; handing it over first makes it shorter. Best effort:
		// the restart is safe without it.
		if st, serr := talos.EtcdStatus(ctx); serr == nil && st.IsLeader() && len(cps) > 1 {
			_ = talos.EtcdForfeitLeadership(ctx)
		}

		callCtx, cancel := context.WithTimeout(ctx, rebootCallTimeout)
		defer cancel()

		return talos.ServiceRestart(callCtx, serviceEtcd)
	})
	if err != nil {
		res, rerr := c.retry(ctx, fmt.Sprintf("stage %s: restarting etcd on %q failed: %v", stage, next.Name, err))

		return res, true, rerr
	}

	now := metav1.Now()
	rs.EtcdRestarted = append(rs.EtcdRestarted, next.Name)
	rs.EtcdRestartedAt = &now

	return ctrl.Result{RequeueAfter: etcdRestartSettle}, true, c.status(ctx, ichorv1alpha1.TalosOperationPhaseInProgress,
		metav1.ConditionTrue, reasonCARotationRestartingEtcd,
		fmt.Sprintf("stage %s: restarted etcd on %q (%d of %d); the next member is restarted once etcd is healthy again",
			stage, next.Name, len(rs.EtcdRestarted), len(cps)))
}

// awaitAggregator holds the rotation, after Accepting, until kube-apiserver publishes the new aggregator
// CA to extension API servers and they have had ClientSettleDelay to load it: the front-proxy
// certificate is signed by the new CA from Issuing on, and an extension API server that does not trust
// it yet would refuse every request kube-apiserver proxies. Ichor restarts none of them. wait is false
// when the rotation may go on. The settle clock is the CARotation condition's transition time, so it
// survives a controller restart.
func (c *caRun) awaitAggregator(ctx context.Context, first *ichorv1alpha1.TalosMachine, bundle *secrets.Bundle) (ctrl.Result, bool, error) {
	if c.r.AggregatorCAPublished == nil || !c.hasTarget(aggregatorCAName) {
		return ctrl.Result{}, false, nil
	}

	fingerprint := ""

	for _, cert := range c.op.Status.CARotation.Certificates {
		if cert.Name == aggregatorCAName {
			fingerprint = cert.Next
		}
	}

	var published bool

	err := c.withNode(ctx, first, bundle, func(ctx context.Context, talos TalosClient) error {
		var err error

		published, err = c.r.AggregatorCAPublished(ctx, talos, fingerprint)

		return err
	})
	if err != nil {
		res, rerr := c.retry(ctx, "stage Accepting is not settled, so the aggregator CA stays: could not read "+extensionAuthConfigMap+": "+err.Error())

		return res, true, rerr
	}

	if !published {
		res, rerr := c.retry(ctx, "stage Accepting is not settled, so the aggregator CA stays: waiting for kube-apiserver to publish "+
			"the new aggregator CA in "+extensionAuthConfigMap)

		return res, true, rerr
	}

	delay := c.r.ClientSettleDelay
	if delay == 0 {
		delay = caClientSettleDelay
	}

	cond := apimeta.FindStatusCondition(c.op.Status.Conditions, conditionCARotation)
	if cond == nil || cond.Reason != reasonCARotationSettlingAggregator || cond.Status != metav1.ConditionTrue {
		apimeta.RemoveStatusCondition(&c.op.Status.Conditions, conditionCARotation)

		err := c.status(ctx, ichorv1alpha1.TalosOperationPhaseInProgress, metav1.ConditionTrue, reasonCARotationSettlingAggregator,
			fmt.Sprintf("kube-apiserver publishes the new aggregator CA; giving extension API servers %s to load it before the front-proxy "+
				"certificate is signed by it. An extension API server that reads the CA only once needs a restart by its owner", delay))

		return ctrl.Result{RequeueAfter: min(delay, operationRequeueInterval)}, true, err
	}

	if left := delay - time.Since(cond.LastTransitionTime.Time); left > 0 {
		return ctrl.Result{RequeueAfter: min(left, operationRequeueInterval)}, true, nil
	}

	return ctrl.Result{}, false, nil
}

// replacedCAs names what a completed rotation replaced, for its message.
func (c *caRun) replacedCAs() string {
	var names []string

	for _, cert := range c.op.Status.CARotation.Certificates {
		switch cert.Name {
		case talosCAName:
			names = append(names, "Talos API")
		case kubernetesCAName:
			names = append(names, "Kubernetes API")
		case etcdCAName:
			names = append(names, "etcd")
		case aggregatorCAName:
			names = append(names, "Kubernetes aggregator")
		}
	}

	if len(names) < 2 {
		return strings.Join(names, "")
	}

	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}
