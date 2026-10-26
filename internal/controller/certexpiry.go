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
	"time"

	"github.com/prometheus/client_golang/prometheus"
	talosx509 "github.com/siderolabs/crypto/x509"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const (
	// certExpiryRecheck is how often a healthy cluster's expiries are re-read.
	// CAs live for years; the bundle changes only when someone replaces it.
	certExpiryRecheck = time.Hour

	// certExpiryRetry is how soon an unreadable bundle is tried again.
	certExpiryRetry = 5 * time.Minute
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
// bundle expires (ADR 0061). It reads only the bundle: no node is dialed, so
// it works while nodes are down, and it never writes anything but
// status.certificates.
type CertExpiryReconciler struct {
	client.Client

	// ControllerNamespace is where the bundle is read (ADR 0011).
	ControllerNamespace string
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

	expiries, err := certificateExpiries(bundle)
	if err != nil {
		ctrl.LoggerFrom(ctx).Info("Cannot read a certificate in the secrets bundle", "reason", err.Error())

		return ctrl.Result{RequeueAfter: certExpiryRetry}, nil
	}

	for _, e := range expiries {
		certExpiry.WithLabelValues(cluster.Namespace, cluster.Name, e.Name).Set(float64(e.NotAfter.Unix()))
	}

	if !slices.EqualFunc(cluster.Status.Certificates, expiries, func(a, b ichorv1alpha1.CertificateExpiry) bool {
		return a.Name == b.Name && a.NotAfter.Equal(&b.NotAfter)
	}) {
		base := cluster.DeepCopy()
		cluster.Status.Certificates = expiries

		if err := r.Status().Patch(ctx, &cluster, client.MergeFrom(base)); err != nil {
			return ctrl.Result{}, fmt.Errorf("publishing certificate expiry: %w", err)
		}
	}

	return ctrl.Result{RequeueAfter: certExpiryRecheck}, nil
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
