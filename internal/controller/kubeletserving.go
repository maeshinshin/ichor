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
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/siderolabs/talos/pkg/machinery/resources/k8s"
	"github.com/siderolabs/talos/pkg/machinery/resources/network"
	certv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
	"github.com/maeshinshin/ichor/pkg/render"
)

const (
	// kubeletServingSigner is the only signer this reconciler ever decides on.
	kubeletServingSigner = certv1.KubeletServingSignerName

	// Reasons on the CSR condition Ichor writes; the first marks a certificate as Ichor's own.
	reasonKubeletServingApproved = "IchorKubeletServing"
	reasonKubeletServingDenied   = "IchorKubeletServingDenied"

	nodeUserPrefix = "system:node:"
	nodesGroup     = "system:nodes"

	// kubeletServingPending is the pause while some machine has no certificate yet; kubeletServingSteady
	// is the pause once all have (renewals arrive as events, this only backs them up).
	kubeletServingPending = 30 * time.Second
	kubeletServingSteady  = 10 * time.Minute
)

// ServingIdentity is what a node owns, as the node itself says over the authenticated Talos API: the
// names it registers and serves under and the addresses it gives its kubelet. It is the only source
// of truth for what a kubelet serving certificate may name.
type ServingIdentity struct {
	Names     []string
	Addresses []netip.Addr
}

// KubeletServingReconciler approves the kubelets' serving-certificate requests for the machines Ichor
// manages and reports whether every machine has one. It decides only on CSRs of the
// kubernetes.io/kubelet-serving signer, only for a TalosCluster that enabled
// spec.security.kubeletServingCertificates, and only ever approves a request it has checked in full
// or denies one that asks for more than the node's own identity. A request it cannot place is left
// pending, never denied.
type KubeletServingReconciler struct {
	client.Client

	// ControllerNamespace is where the secrets bundle is read.
	ControllerNamespace string

	// Identity reads a node's identity over its authenticated Talos API (NewMachineryServingIdentity).
	// nil leaves every request pending.
	Identity func(
		ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, bundle *secrets.Bundle,
	) (ServingIdentity, error)

	Recorder record.EventRecorder

	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosmachines,verbs=get;list;watch
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosmachines/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",namespace=ichor-system,resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=certificates.k8s.io,resources=certificatesigningrequests,verbs=get;list;watch
// +kubebuilder:rbac:groups=certificates.k8s.io,resources=certificatesigningrequests/approval,verbs=update
// +kubebuilder:rbac:groups=certificates.k8s.io,resources=signers,resourceNames=kubernetes.io/kubelet-serving,verbs=approve

func (r *KubeletServingReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}

	return time.Now()
}

// Reconcile decides the open requests of one cluster, records the certificates issued, and updates the
// cluster's conditions.
func (r *KubeletServingReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var cluster ichorv1alpha1.TalosCluster
	if err := r.Get(ctx, req.NamespacedName, &cluster); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !render.KubeletServingEnabled(&cluster) {
		return ctrl.Result{}, r.patchConditions(ctx, &cluster, nil, false)
	}

	// A cached read is enough: nothing disruptive hangs on it, and a machine that joins re-wakes this.
	items, err := deleteguard.ApprovedMachines(ctx, r.Client, cluster.Namespace, cluster.Name, deleteguard.MachineFilter{})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("listing machines: %w", err)
	}

	owned := make([]*ichorv1alpha1.TalosMachine, len(items))
	for i := range items {
		owned[i] = &items[i]
	}

	var csrs certv1.CertificateSigningRequestList
	if err := r.List(ctx, &csrs); err != nil {
		return ctrl.Result{}, fmt.Errorf("listing certificate signing requests: %w", err)
	}

	r.decideOpen(ctx, &cluster, owned, csrs.Items)

	if err := r.recordIssued(ctx, owned, csrs.Items); err != nil {
		return ctrl.Result{}, err
	}

	covered, missing := r.coverage(owned)

	verify := cluster.Spec.Security.KubeletServingCertificates.VerifyFromAPIServer

	if err := r.patchConditions(ctx, &cluster, &coverageReport{covered: covered, missing: missing}, verify); err != nil {
		return ctrl.Result{}, err
	}

	if covered {
		return ctrl.Result{RequeueAfter: kubeletServingSteady}, nil
	}

	return ctrl.Result{RequeueAfter: kubeletServingPending}, nil
}

// nodeNameOf is the Kubernetes node name of m as Ichor knows it, "" when it does not yet.
func nodeNameOf(m *ichorv1alpha1.TalosMachine) string {
	if m.Status.NodeName != "" {
		return m.Status.NodeName
	}

	return m.Spec.Hostname
}

// decideOpen decides every request that is still open. Errors are per request and only logged: the
// next pass (an event or the timer) retries, and a request left open does no harm.
func (r *KubeletServingReconciler) decideOpen(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, owned []*ichorv1alpha1.TalosMachine,
	csrs []certv1.CertificateSigningRequest,
) {
	log := ctrl.LoggerFrom(ctx)

	var bundle *secrets.Bundle

	for i := range csrs {
		csr := &csrs[i]
		if !isOpenServingCSR(csr) {
			continue
		}

		// Everything that can be decided from the request alone does not need the node.
		parsed, denial := parseServingRequest(csr)
		if denial != "" {
			r.finish(ctx, cluster, csr, false, denial)

			continue
		}

		m := machineForNode(owned, parsed.nodeName)
		if m == nil {
			log.V(1).Info("Leaving a kubelet serving request pending: no machine of this cluster has that node name",
				"csr", csr.Name, "node", parsed.nodeName)

			continue
		}

		if r.Identity == nil {
			continue
		}

		if bundle == nil {
			b, err := loadBundle(ctx, r.Client, r.ControllerNamespace, cluster)
			if err != nil {
				log.Info("Cannot read the secrets bundle to check a kubelet serving request", "reason", err.Error())

				return
			}

			bundle = b
		}

		id, err := r.Identity(ctx, cluster, m, bundle)
		if err != nil {
			log.Info("Leaving a kubelet serving request pending: the node cannot be read",
				"csr", csr.Name, "node", parsed.nodeName, "reason", err.Error())

			continue
		}

		if denial := checkServingIdentity(parsed, id); denial != "" {
			r.finish(ctx, cluster, csr, false, denial)

			continue
		}

		r.finish(ctx, cluster, csr, true, fmt.Sprintf("the kubelet of node %s asked for a serving certificate naming only that node's own names and addresses", parsed.nodeName))
	}
}

// isOpenServingCSR is a request of the kubelet-serving signer that nobody has decided on yet.
func isOpenServingCSR(csr *certv1.CertificateSigningRequest) bool {
	if csr.Spec.SignerName != kubeletServingSigner || len(csr.Status.Certificate) > 0 {
		return false
	}

	for _, c := range csr.Status.Conditions {
		switch c.Type {
		case certv1.CertificateApproved, certv1.CertificateDenied, certv1.CertificateFailed:
			return false
		}
	}

	return true
}

// machineForNode is the machine whose node name is name.
func machineForNode(owned []*ichorv1alpha1.TalosMachine, name string) *ichorv1alpha1.TalosMachine {
	for _, m := range owned {
		if nodeNameOf(m) == name {
			return m
		}
	}

	return nil
}

// finish writes the decision to the request's approval subresource and tells the cluster's Events.
func (r *KubeletServingReconciler) finish(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, csr *certv1.CertificateSigningRequest, approve bool, message string,
) {
	cond := certv1.CertificateSigningRequestCondition{
		Type: certv1.CertificateApproved, Status: corev1.ConditionTrue, Reason: reasonKubeletServingApproved,
		Message: message, LastUpdateTime: metav1.NewTime(r.now()),
	}
	event, eventType := "KubeletServingApproved", corev1.EventTypeNormal

	if !approve {
		cond.Type, cond.Reason = certv1.CertificateDenied, reasonKubeletServingDenied
		event, eventType = "KubeletServingDenied", corev1.EventTypeWarning
	}

	updated := csr.DeepCopy()
	updated.Status.Conditions = append(updated.Status.Conditions, cond)

	log := ctrl.LoggerFrom(ctx)

	if err := r.SubResource("approval").Update(ctx, updated); err != nil {
		log.Info("Could not write the decision on a kubelet serving request", "csr", csr.Name, "reason", err.Error())

		return
	}

	log.Info("Decided a kubelet serving request", "csr", csr.Name, "approved", approve, "message", message)

	if r.Recorder != nil {
		r.Recorder.Eventf(cluster, eventType, event, "CertificateSigningRequest %s: %s", csr.Name, message)
	}
}

// servingRequest is the part of a kubelet serving request the checks need.
type servingRequest struct {
	nodeName string
	csr      *x509.CertificateRequest
}

// parseServingRequest checks everything that can be checked from the request alone. A non-empty
// denial is the reason to deny it.
func parseServingRequest(csr *certv1.CertificateSigningRequest) (servingRequest, string) {
	block, _ := pem.Decode(csr.Spec.Request)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return servingRequest{}, "the request is not a PEM CERTIFICATE REQUEST"
	}

	req, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return servingRequest{}, "the request does not parse as a PKCS#10 request"
	}

	if err := req.CheckSignature(); err != nil {
		return servingRequest{}, "the request's signature is invalid"
	}

	cn := req.Subject.CommonName

	nodeName, ok := strings.CutPrefix(cn, nodeUserPrefix)
	if !ok || nodeName == "" {
		return servingRequest{}, fmt.Sprintf("the common name %q is not system:node:<name>", cn)
	}

	if len(req.Subject.Names) != 2 || !slices.Equal(req.Subject.Organization, []string{nodesGroup}) {
		return servingRequest{}, "the subject must be exactly CN=system:node:<name>, O=system:nodes"
	}

	if csr.Spec.Username != cn {
		return servingRequest{}, fmt.Sprintf("the requester %q is not the node %q", csr.Spec.Username, cn)
	}

	if !slices.Contains(csr.Spec.Groups, nodesGroup) {
		return servingRequest{}, "the requester is not in the group system:nodes"
	}

	if !validServingUsages(csr.Spec.Usages) {
		return servingRequest{}, "the key usages must be exactly digital signature and server auth (and optionally key encipherment)"
	}

	for _, e := range req.Extensions {
		if !e.Id.Equal(oidSubjectAltName) {
			return servingRequest{}, fmt.Sprintf("the request carries the extension %s; only the subject alternative names are allowed", e.Id)
		}
	}

	if len(req.EmailAddresses) > 0 || len(req.URIs) > 0 {
		return servingRequest{}, "the request names an email address or URI; only DNS names and IP addresses are allowed"
	}

	if len(req.DNSNames) == 0 && len(req.IPAddresses) == 0 {
		return servingRequest{}, "the request names no DNS name or IP address"
	}

	return servingRequest{nodeName: nodeName, csr: req}, ""
}

// oidSubjectAltName is the X.509 subjectAltName extension (2.5.29.17).
var oidSubjectAltName = []int{2, 5, 29, 17}

// validServingUsages is one of the two sets the kubernetes.io/kubelet-serving signer permits.
func validServingUsages(usages []certv1.KeyUsage) bool {
	set := map[certv1.KeyUsage]bool{}
	for _, u := range usages {
		set[u] = true
	}

	if len(set) != len(usages) || !set[certv1.UsageDigitalSignature] || !set[certv1.UsageServerAuth] {
		return false
	}

	return len(set) == 2 || (len(set) == 3 && set[certv1.UsageKeyEncipherment])
}

// checkServingIdentity denies a request that names anything the node does not own. The node's own
// name (from the common name, which a machine of this cluster has) is always allowed.
func checkServingIdentity(req servingRequest, id ServingIdentity) string {
	names := append([]string{req.nodeName}, id.Names...)

	for _, d := range req.csr.DNSNames {
		if !slices.ContainsFunc(names, func(n string) bool { return strings.EqualFold(n, d) }) {
			return fmt.Sprintf("the DNS name %q is not one of the node's names", d)
		}
	}

	for _, ip := range req.csr.IPAddresses {
		a, ok := netip.AddrFromSlice(ip)
		if !ok || !slices.Contains(id.Addresses, a.Unmap()) {
			return fmt.Sprintf("the IP address %s is not one of the node's addresses", ip)
		}
	}

	return ""
}

// recordIssued stores, on each machine, the newest certificate Ichor approved for its node.
func (r *KubeletServingReconciler) recordIssued(
	ctx context.Context, owned []*ichorv1alpha1.TalosMachine, csrs []certv1.CertificateSigningRequest,
) error {
	for _, m := range owned {
		name := nodeNameOf(m)
		if name == "" {
			continue
		}

		var best *ichorv1alpha1.KubeletServingCertificateStatus

		for i := range csrs {
			if st := issuedBy(&csrs[i], name); st != nil && (best == nil || st.NotAfter.After(best.NotAfter.Time)) {
				best = st
			}
		}

		have := m.Status.KubeletServingCertificate
		if best == nil || (have != nil && !best.NotAfter.After(have.NotAfter.Time)) {
			continue
		}

		base := m.DeepCopy()
		m.Status.KubeletServingCertificate = best

		if err := r.Status().Patch(ctx, m, client.MergeFrom(base)); err != nil {
			return fmt.Errorf("recording the serving certificate of %s: %w", m.Name, err)
		}
	}

	return nil
}

// issuedBy is the certificate csr carries when Ichor approved it for node and the CA signed it.
func issuedBy(csr *certv1.CertificateSigningRequest, node string) *ichorv1alpha1.KubeletServingCertificateStatus {
	if csr.Spec.SignerName != kubeletServingSigner || len(csr.Status.Certificate) == 0 ||
		csr.Spec.Username != nodeUserPrefix+node {
		return nil
	}

	ours := slices.ContainsFunc(csr.Status.Conditions, func(c certv1.CertificateSigningRequestCondition) bool {
		return c.Type == certv1.CertificateApproved && c.Reason == reasonKubeletServingApproved
	})
	if !ours {
		return nil
	}

	block, _ := pem.Decode(csr.Status.Certificate)
	if block == nil {
		return nil
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil
	}

	return &ichorv1alpha1.KubeletServingCertificateStatus{
		Request: csr.Name, IssuedAt: metav1.NewTime(cert.NotBefore), NotAfter: metav1.NewTime(cert.NotAfter),
	}
}

// coverage reports whether every machine has an unexpired certificate, and the names of those that
// do not.
func (r *KubeletServingReconciler) coverage(owned []*ichorv1alpha1.TalosMachine) (bool, []string) {
	var missing []string

	for _, m := range owned {
		if st := m.Status.KubeletServingCertificate; st == nil || !st.NotAfter.After(r.now()) {
			missing = append(missing, m.Name)
		}
	}

	slices.Sort(missing)

	return len(owned) > 0 && len(missing) == 0, missing
}

// coverageReport is the input of the two conditions.
type coverageReport struct {
	covered bool
	missing []string
}

// patchConditions sets the conditions of the option, or removes them when report is nil.
func (r *KubeletServingReconciler) patchConditions(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, report *coverageReport, verify bool,
) error {
	base := cluster.DeepCopy()

	if report == nil {
		apimeta.RemoveStatusCondition(&cluster.Status.Conditions, ichorv1alpha1.ConditionKubeletServingCertificates)
		apimeta.RemoveStatusCondition(&cluster.Status.Conditions, ichorv1alpha1.ConditionKubeletServingVerified)
	} else {
		setServingConditions(cluster, report, verify)
	}

	if apiequalityConditions(base.Status.Conditions, cluster.Status.Conditions) {
		return nil
	}

	if err := r.Status().Patch(ctx, cluster, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil {
		return fmt.Errorf("patching the kubelet serving conditions: %w", err)
	}

	return nil
}

// setServingConditions sets KubeletServingCertificates from report and KubeletServingVerified, the
// latch, from it and the option. The latch is never lowered by a machine that has no certificate yet.
func setServingConditions(cluster *ichorv1alpha1.TalosCluster, report *coverageReport, verify bool) {
	cond := metav1.Condition{
		Type: ichorv1alpha1.ConditionKubeletServingCertificates, Status: metav1.ConditionTrue, Reason: "AllNodesIssued",
		Message: "every machine's kubelet serves a certificate signed by the Kubernetes CA", ObservedGeneration: cluster.Generation,
	}

	if !report.covered {
		cond.Status, cond.Reason = metav1.ConditionFalse, "Pending"
		cond.Message = "waiting for a serving certificate for: " + strings.Join(report.missing, ", ")

		if len(report.missing) == 0 {
			cond.Message = "no machine of the cluster is approved yet"
		}
	}

	apimeta.SetStatusCondition(&cluster.Status.Conditions, cond)

	if !verify {
		apimeta.RemoveStatusCondition(&cluster.Status.Conditions, ichorv1alpha1.ConditionKubeletServingVerified)

		return
	}

	if apimeta.IsStatusConditionTrue(cluster.Status.Conditions, ichorv1alpha1.ConditionKubeletServingVerified) {
		return
	}

	latch := metav1.Condition{
		Type: ichorv1alpha1.ConditionKubeletServingVerified, Status: metav1.ConditionFalse, Reason: "Waiting",
		Message: "the kube-apiserver is told to verify the kubelets once every machine has a serving certificate", ObservedGeneration: cluster.Generation,
	}

	if report.covered {
		latch.Status, latch.Reason = metav1.ConditionTrue, "AllNodesIssued"
		latch.Message = "the kube-apiserver verifies the kubelets against the Kubernetes CA"
	}

	apimeta.SetStatusCondition(&cluster.Status.Conditions, latch)
}

// apiequalityConditions is true when the two lists carry the same type, status, reason and message
// (not times, which SetStatusCondition keeps when nothing else changed).
func apiequalityConditions(a, b []metav1.Condition) bool {
	return slices.EqualFunc(a, b, func(x, y metav1.Condition) bool {
		return x.Type == y.Type && x.Status == y.Status && x.Reason == y.Reason && x.Message == y.Message &&
			x.ObservedGeneration == y.ObservedGeneration
	})
}

// SetupWithManager wakes the reconciler on a cluster change, on a machine's status (a node name
// appearing) and on every kubelet-serving request.
func (r *KubeletServingReconciler) SetupWithManager(mgr ctrl.Manager) error {
	servingOnly := predicate.NewPredicateFuncs(func(o client.Object) bool {
		csr, ok := o.(*certv1.CertificateSigningRequest)

		return ok && csr.Spec.SignerName == kubeletServingSigner
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(&ichorv1alpha1.TalosCluster{}).
		Watches(&certv1.CertificateSigningRequest{}, handler.EnqueueRequestsFromMapFunc(r.enabledClusters), builder.WithPredicates(servingOnly)).
		Watches(&ichorv1alpha1.TalosMachine{}, handler.EnqueueRequestsFromMapFunc(r.clusterOfMachine)).
		Named("kubeletserving").
		Complete(r)
}

// enabledClusters maps a request to every cluster that enabled the option.
func (r *KubeletServingReconciler) enabledClusters(ctx context.Context, _ client.Object) []reconcile.Request {
	var clusters ichorv1alpha1.TalosClusterList
	if err := r.List(ctx, &clusters); err != nil {
		return nil
	}

	var out []reconcile.Request

	for i := range clusters.Items {
		if c := &clusters.Items[i]; render.KubeletServingEnabled(c) {
			out = append(out, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(c)})
		}
	}

	return out
}

// clusterOfMachine maps a machine to its cluster.
func (r *KubeletServingReconciler) clusterOfMachine(_ context.Context, obj client.Object) []reconcile.Request {
	m, ok := obj.(*ichorv1alpha1.TalosMachine)
	if !ok || m.Spec.ClusterRef.Name == "" {
		return nil
	}

	return []reconcile.Request{{Namespace: m.Namespace, Name: m.Spec.ClusterRef.Name}}
}

// NewMachineryServingIdentity dials m's Talos API and reads the node's names and kubelet addresses.
func NewMachineryServingIdentity(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, bundle *secrets.Bundle,
) (ServingIdentity, error) {
	ctx, cancel := context.WithTimeout(ctx, etcdDialTimeout)
	defer cancel()

	c, err := dialAdmin(ctx, cluster, m, bundle)
	if err != nil {
		return ServingIdentity{}, err
	}

	defer func() { _ = c.Close() }()

	var id ServingIdentity

	ip, err := safe.StateGetByID[*k8s.NodeIP](ctx, c.COSI, k8s.KubeletID)
	if err != nil {
		return ServingIdentity{}, fmt.Errorf("reading the kubelet's node addresses: %w", err)
	}

	for _, a := range ip.TypedSpec().Addresses {
		id.Addresses = append(id.Addresses, a.Unmap())
	}

	nn, err := safe.StateGetByID[*k8s.Nodename](ctx, c.COSI, k8s.NodenameID)
	if err != nil {
		return ServingIdentity{}, fmt.Errorf("reading the Kubernetes node name: %w", err)
	}

	id.Names = append(id.Names, nn.TypedSpec().Nodename)

	hosts, err := safe.StateListAll[*network.HostnameStatus](ctx, c.COSI)
	if err != nil {
		return ServingIdentity{}, fmt.Errorf("reading the hostname: %w", err)
	}

	for h := range hosts.All() {
		id.Names = append(id.Names, h.TypedSpec().Hostname)
		if h.TypedSpec().Domainname != "" {
			id.Names = append(id.Names, h.TypedSpec().FQDN())
		}
	}

	return id, nil
}
