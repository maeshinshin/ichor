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
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	talosx509 "github.com/siderolabs/crypto/x509"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
)

const (
	// certExpiryRecheck is how often a healthy cluster's expiries are re-read.
	// CAs live for years; the bundle changes only when someone replaces it.
	certExpiryRecheck = time.Hour

	// certExpiryRetry is how soon an unreadable bundle is tried again.
	certExpiryRetry = 5 * time.Minute

	// DefaultCertificateExpiryWarning is how far ahead CertificatesExpiring turns True when the
	// controller is not given --certificate-expiry-warning-days.
	DefaultCertificateExpiryWarning = 90 * 24 * time.Hour
)

// Metric label names, shared so the series and its deletion cannot drift.
const (
	labelNamespace = "namespace"
	labelName      = "name"
)

// certExpiry is the metric for "expiry dates are visible in ... metrics"
// (design, "Backup, recovery, certificates"). It is a timestamp, not a
// remaining time, so Prometheus computes the remainder:
// ichor_certificate_expiry_timestamp_seconds - time() < 30 * 86400.
var certExpiry = prometheus.NewGaugeVec(prometheus.GaugeOpts{
	Name: "ichor_certificate_expiry_timestamp_seconds",
	Help: "Unix time at which a cluster certificate authority in the secrets bundle expires.",
}, []string{labelNamespace, labelName, "certificate"})

func init() {
	metrics.Registry.MustRegister(certExpiry)
}

// CertExpiryReconciler publishes when each CA in a TalosCluster's secrets
// bundle expires. It reads only the bundle: no node is dialed, so
// it works while nodes are down, and it never writes anything but
// status.certificates.
type CertExpiryReconciler struct {
	client.Client

	// ControllerNamespace is where the bundle is read.
	ControllerNamespace string

	// ExpiryWarning is how long before a CA expires CertificatesExpiring turns True.
	// Zero means DefaultCertificateExpiryWarning.
	ExpiryWarning time.Duration
}

// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",namespace=ichor-system,resources=secrets,verbs=get;list;watch

// Reconcile refreshes status.certificates and the metric for one cluster.
func (r *CertExpiryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var cluster ichorv1alpha1.TalosCluster
	if err := r.Get(ctx, req.NamespacedName, &cluster); err != nil {
		if client.IgnoreNotFound(err) == nil {
			certExpiry.DeletePartialMatch(prometheus.Labels{labelNamespace: req.Namespace, labelName: req.Name})
		}

		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	bundle, err := loadBundle(ctx, r.Client, r.ControllerNamespace, &cluster)
	if err != nil {
		// The machine controller reports an unreadable bundle on each machine; here
		// the stale expiry simply stays until the bundle can be read again.
		ctrl.LoggerFrom(ctx).Info("Cannot read the secrets bundle for certificate expiry", "reason", err.Error())

		return ctrl.Result{RequeueAfter: certExpiryRetry}, nil
	}

	if err := r.clearBundleBackupStale(ctx, &cluster, bundle); err != nil {
		return ctrl.Result{}, err
	}

	expiries, err := certificateExpiries(bundle)
	if err != nil {
		ctrl.LoggerFrom(ctx).Info("Cannot read a certificate in the secrets bundle", "reason", err.Error())

		return ctrl.Result{RequeueAfter: certExpiryRetry}, nil
	}

	for _, e := range expiries {
		certExpiry.WithLabelValues(cluster.Namespace, cluster.Name, e.Name).Set(float64(e.NotAfter.Unix()))
	}

	base := cluster.DeepCopy()
	changed := false

	if !slices.EqualFunc(cluster.Status.Certificates, expiries, func(a, b ichorv1alpha1.CertificateExpiry) bool {
		return a.Name == b.Name && a.NotAfter.Equal(&b.NotAfter)
	}) {
		cluster.Status.Certificates = expiries
		changed = true
	}

	if setCertificatesExpiring(&cluster, expiries, time.Now(), r.warningWindow()) {
		changed = true
	}

	if changed {
		if err := r.Status().Patch(ctx, &cluster, client.MergeFrom(base)); err != nil {
			return ctrl.Result{}, fmt.Errorf("publishing certificate expiry: %w", err)
		}
	}

	return ctrl.Result{RequeueAfter: certExpiryRecheck}, nil
}

// warningWindow is how long before a CA expires CertificatesExpiring turns True.
func (r *CertExpiryReconciler) warningWindow() time.Duration {
	if r.ExpiryWarning <= 0 {
		return DefaultCertificateExpiryWarning
	}

	return r.ExpiryWarning
}

// setCertificatesExpiring sets the CertificatesExpiring condition from expiries and reports whether
// the condition changed. The message names each CA and its date, not the days left, so it does not
// change (and cause a write) every hour; the dates are in UTC for the same reason.
func setCertificatesExpiring(
	cluster *ichorv1alpha1.TalosCluster, expiries []ichorv1alpha1.CertificateExpiry, now time.Time, window time.Duration,
) bool {
	var soon []string

	expired := false

	for _, e := range expiries {
		if !e.NotAfter.Time.Before(now.Add(window)) {
			continue
		}

		verb := "expires"
		if e.NotAfter.Time.Before(now) {
			verb, expired = "expired", true
		}

		soon = append(soon, fmt.Sprintf("the %s CA %s on %s", e.Name, verb, e.NotAfter.UTC().Format(time.DateOnly)))
	}

	cond := metav1.Condition{
		Type: ichorv1alpha1.ConditionCertificatesExpiring, Status: metav1.ConditionFalse, Reason: "NoneExpiring",
		Message:            fmt.Sprintf("no CA expires within %d days", int(window.Hours()/24)),
		ObservedGeneration: cluster.Generation,
	}

	if len(soon) > 0 {
		cond.Status, cond.Reason = metav1.ConditionTrue, "ExpiringSoon"
		if expired {
			cond.Reason = "Expired"
		}

		cond.Message = strings.Join(soon, "; ") + ". Rotate the CAs (ichor cluster rotate-ca; add --ca etcd or " +
			"--ca kubernetes-aggregator for those) before they expire."
	}

	before := apimeta.FindStatusCondition(cluster.Status.Conditions, cond.Type)
	if before != nil && before.Status == cond.Status && before.Reason == cond.Reason &&
		before.Message == cond.Message && before.ObservedGeneration == cond.ObservedGeneration {
		return false
	}

	apimeta.SetStatusCondition(&cluster.Status.Conditions, cond)

	return true
}

// clearBundleBackupStale lowers BundleBackupStale once the user has exported the live bundle: the
// annotation ichor sets after saving the file holds the fingerprint of what it saved, and only a
// match with the bundle in the cluster counts. An export of an older bundle, or none,
// leaves the condition up.
func (r *CertExpiryReconciler) clearBundleBackupStale(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, bundle *secrets.Bundle,
) error {
	if !apimeta.IsStatusConditionTrue(cluster.Status.Conditions, ichorv1alpha1.ConditionBundleBackupStale) {
		return nil
	}

	want, err := secretsbundle.ExportFingerprint(bundle)
	if err != nil || cluster.Annotations[ichorv1alpha1.BundleExportedAnnotation] != want {
		return nil //nolint:nilerr // an unreadable bundle keeps the condition up; the expiry path reports it
	}

	base := cluster.DeepCopy()
	apimeta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
		Type: ichorv1alpha1.ConditionBundleBackupStale, Status: metav1.ConditionFalse, Reason: "Exported",
		Message: "the current secrets bundle has been exported", ObservedGeneration: cluster.Generation,
	})

	if err := r.Status().Patch(ctx, cluster, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("clearing BundleBackupStale: %w", err)
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *CertExpiryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ichorv1alpha1.TalosCluster{}).
		Named("certexpiry").
		Complete(r)
}

// certificateExpiries lists the end of validity of every CA in bundle, in a
// fixed order. Errors name the certificate, never its contents.
func certificateExpiries(bundle *secrets.Bundle) ([]ichorv1alpha1.CertificateExpiry, error) {
	if bundle == nil || bundle.Certs == nil {
		return nil, errors.New("secrets bundle has no certificates")
	}

	var out []ichorv1alpha1.CertificateExpiry

	for _, ca := range []struct {
		name string
		crt  []byte
	}{
		{"talos", pemOf(bundle.Certs.OS)},
		{"kubernetes", pemOf(bundle.Certs.K8s)},
		{"kubernetes-aggregator", pemOf(bundle.Certs.K8sAggregator)},
		{"etcd", pemOf(bundle.Certs.Etcd)},
	} {
		if ca.crt == nil {
			continue
		}

		block, _ := pem.Decode(ca.crt)
		if block == nil {
			return nil, fmt.Errorf("the %s CA is not PEM", ca.name)
		}

		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("the %s CA does not parse as a certificate", ca.name)
		}

		out = append(out, ichorv1alpha1.CertificateExpiry{Name: ca.name, NotAfter: metav1.NewTime(cert.NotAfter)})
	}

	if len(out) == 0 {
		return nil, errors.New("secrets bundle has no certificate authorities")
	}

	return out, nil
}

// pemOf returns the certificate half of a CA, or nil when the bundle has none.
func pemOf(ca *talosx509.PEMEncodedCertificateAndKey) []byte {
	if ca == nil || len(ca.Crt) == 0 {
		return nil
	}

	return ca.Crt
}
