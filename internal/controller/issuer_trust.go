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
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/oidctrust"
)

const (
	// DefaultUIServiceName is the ClusterIP Service in front of ichor-ui (dist/chart/templates/ui).
	DefaultUIServiceName = "ichor-ui-service"

	issuerRotateEvery     = 30 * 24 * time.Hour
	issuerActivationDelay = 10 * time.Minute
	issuerRetireGrace     = 2 * time.Hour

	// DefaultRefreshLifetime is ichorUI.users.refreshLifetime's default.
	DefaultRefreshLifetime = 12 * time.Hour

	// issuerRecheck bounds how long the reconciler sleeps, whatever the schedule says.
	issuerRecheck = time.Hour

	// issuerOrphanGrace is how long a credentials entry must have been without an IchorUser, over
	// consecutive reconciles, before it is deleted: the CLI and the UI write the CR and the entry in
	// separate calls, in either order.
	issuerOrphanGrace = 2 * time.Minute

	// ConditionIssuerCertificatesExpiring is True on a TalosCluster while the issuer's CA or leaf
	// is within the warning window of its end.
	ConditionIssuerCertificatesExpiring = "IssuerCertificatesExpiring"

	managedByLabel   = "app.kubernetes.io/managed-by"
	managedByValue   = "ichor-controller"
	credentialsShape = "$argon2id$"
)

var issuerCertExpiry = prometheus.NewGaugeVec(prometheus.GaugeOpts{
	Name: "ichor_oidc_certificate_expiry_timestamp_seconds",
	Help: "Unix time at which the issuer's internal CA or serving certificate expires.",
}, []string{"certificate"})

var issuerSigningKeys = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "ichor_oidc_signing_keys",
	Help: "Number of issuer signing keys currently published.",
})

func init() {
	metrics.Registry.MustRegister(issuerCertExpiry, issuerSigningKeys)
}

// IssuerTrustReconciler keeps what ichor-ui's own OIDC issuer needs from the controller: the signing keys and their
// rotation, the internal CA and serving certificate, the public
// trust ConfigMap, IchorUser.status, and the clean-up of credentials entries whose user is gone.
// It writes only those Secrets and that ConfigMap and status; it never changes any spec.
type IssuerTrustReconciler struct {
	client.Client

	// APIReader is uncached; the clean-up decides from a live list of users.
	APIReader client.Reader

	// Namespace is where every object lives (ichor-system).
	Namespace string

	// UIServiceName is the Service whose ClusterIP the serving certificate and discoveryURL name.
	UIServiceName string

	// RefreshLifetime is the longest-lived token ichor-ui signs; a key is kept that long past its
	// retirement. Zero means DefaultRefreshLifetime.
	RefreshLifetime time.Duration

	// ExpiryWarning is the window for ConditionIssuerCertificatesExpiring. Zero means
	// DefaultCertificateExpiryWarning.
	ExpiryWarning time.Duration

	// Now is the clock; nil means time.Now.
	Now func() time.Time

	// OrphanGrace overrides issuerOrphanGrace (tests).
	OrphanGrace time.Duration

	orphanSince map[string]time.Time
}

// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=ichorusers,verbs=get;list;watch
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=ichorusers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",namespace=ichor-system,resources=configmaps,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="",namespace=ichor-system,resources=services,verbs=get;list;watch
// +kubebuilder:rbac:groups="",namespace=ichor-system,resources=secrets,resourceNames=ichor-user-credentials,verbs=get;update;patch

func (r *IssuerTrustReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}

	return time.Now()
}

func (r *IssuerTrustReconciler) plan() rotationPlan {
	life := r.RefreshLifetime
	if life <= 0 {
		life = DefaultRefreshLifetime
	}

	return rotationPlan{
		RotateEvery: issuerRotateEvery, ActivationDelay: issuerActivationDelay,
		TokenLife: life, RetireGrace: issuerRetireGrace,
	}
}

func (r *IssuerTrustReconciler) serviceName() string {
	if r.UIServiceName == "" {
		return DefaultUIServiceName
	}

	return r.UIServiceName
}

// Reconcile converges every object. The request key is ignored: all events map to one pass.
func (r *IssuerTrustReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	now := r.now()

	nextKeys, err := r.reconcileKeys(ctx, now)
	if err != nil {
		return ctrl.Result{}, err
	}

	next := nextKeys

	tlsNext, err := r.reconcileTLS(ctx, now)
	if err != nil {
		return ctrl.Result{}, err
	}

	if !tlsNext.IsZero() && (next.IsZero() || tlsNext.Before(next)) {
		next = tlsNext
	}

	if err := r.reconcileUsers(ctx, now); err != nil {
		return ctrl.Result{}, err
	}

	wait := issuerRecheck
	if !next.IsZero() {
		wait = min(wait, max(next.Sub(now), time.Second))
	}

	return ctrl.Result{RequeueAfter: wait}, nil
}

func (r *IssuerTrustReconciler) key(name string) types.NamespacedName {
	return types.NamespacedName{Namespace: r.Namespace, Name: name}
}

// reconcileKeys creates or rotates the signing keys and returns when it next has work.
func (r *IssuerTrustReconciler) reconcileKeys(ctx context.Context, now time.Time) (time.Time, error) {
	var sec corev1.Secret

	err := r.Get(ctx, r.key(oidctrust.SigningSecret), &sec)
	exists := err == nil

	if err != nil && !apierrors.IsNotFound(err) {
		return time.Time{}, fmt.Errorf("reading signing keys: %w", err)
	}

	var current oidctrust.Keys

	if exists {
		// An unreadable document is never overwritten: that would silently invalidate every key
		// ichor-ui and the apiserver know. The error carries no key material.
		if current, err = oidctrust.ParseKeys(sec.Data[oidctrust.KeysKey]); err != nil {
			return time.Time{}, fmt.Errorf("signing keys Secret %s is unreadable, not replacing it: %w",
				oidctrust.SigningSecret, err)
		}
	}

	updated, changed, next, err := rotateKeys(current, now, r.plan())
	if err != nil {
		return time.Time{}, err
	}

	issuerSigningKeys.Set(float64(len(updated.Published(now))))

	if exists && !changed {
		return next, nil
	}

	raw, err := updated.Marshal()
	if err != nil {
		return time.Time{}, fmt.Errorf("encoding signing keys: %w", err)
	}

	if err := r.apply(ctx, &sec, oidctrust.SigningSecret, corev1.SecretTypeOpaque,
		map[string][]byte{oidctrust.KeysKey: raw}); err != nil {
		return time.Time{}, err
	}

	ctrl.LoggerFrom(ctx).Info("Issuer signing keys updated", "keys", len(updated.Keys))

	return next, nil
}

// apply creates or updates a Secret with exactly data. The Secret's type is fixed on creation.
func (r *IssuerTrustReconciler) apply(
	ctx context.Context, sec *corev1.Secret, name string, typ corev1.SecretType, data map[string][]byte,
) error {
	if sec.UID == "" {
		sec.Namespace, sec.Name, sec.Type = r.Namespace, name, typ
		sec.Labels = map[string]string{managedByLabel: managedByValue}
		sec.Data = data

		if err := r.Create(ctx, sec); err != nil {
			return fmt.Errorf("creating Secret %s: %w", name, err)
		}

		return nil
	}

	sec.Data = data
	if sec.Labels == nil {
		sec.Labels = map[string]string{}
	}

	sec.Labels[managedByLabel] = managedByValue

	if err := r.Update(ctx, sec); err != nil {
		return fmt.Errorf("updating Secret %s: %w", name, err)
	}

	return nil
}

// reconcileTLS keeps the CA, the serving certificate and the trust ConfigMap. Without a ClusterIP
// (the Service is missing or headless) it issues nothing and retries shortly.
func (r *IssuerTrustReconciler) reconcileTLS(ctx context.Context, now time.Time) (time.Time, error) {
	var svc corev1.Service

	if err := r.Get(ctx, r.key(r.serviceName()), &svc); err != nil {
		if apierrors.IsNotFound(err) {
			ctrl.LoggerFrom(ctx).Info("ichor-ui Service not found; internal TLS waits", "service", r.serviceName())

			return now.Add(30 * time.Second), nil
		}

		return time.Time{}, fmt.Errorf("reading Service %s: %w", r.serviceName(), err)
	}

	ip := net.ParseIP(svc.Spec.ClusterIP)
	if ip == nil {
		ctrl.LoggerFrom(ctx).Info("ichor-ui Service has no ClusterIP; internal TLS waits", "service", r.serviceName())

		return now.Add(30 * time.Second), nil
	}

	spec := leafSpec{ip: ip, dns: []string{fmt.Sprintf("%s.%s.svc", r.serviceName(), r.Namespace)}}

	ca, caX509, caKey, err := r.ensureCA(ctx, now)
	if err != nil {
		return time.Time{}, err
	}

	leaf, leafEnd, err := r.ensureLeaf(ctx, ca, caX509, caKey, spec, now)
	if err != nil {
		return time.Time{}, err
	}

	if err := r.ensureTrustMap(ctx, ip, ca.certPEM); err != nil {
		return time.Time{}, err
	}

	issuerCertExpiry.WithLabelValues("ca").Set(float64(caX509.NotAfter.Unix()))
	issuerCertExpiry.WithLabelValues("tls").Set(float64(leafEnd.Unix()))

	if err := r.setExpiring(ctx, now, caX509.NotAfter, leafEnd); err != nil {
		return time.Time{}, err
	}

	// Wake for the leaf's renewal point.
	return leafRenewAt(leaf), nil
}

func leafRenewAt(leaf keyPair) time.Time {
	c, err := decodeCert(leaf.certPEM)
	if err != nil {
		return time.Time{}
	}

	return c.NotBefore.Add(c.NotAfter.Sub(c.NotBefore) * 2 / 3)
}

// ensureCA returns a usable CA, making one when none exists or it is near its end.
func (r *IssuerTrustReconciler) ensureCA(
	ctx context.Context, now time.Time,
) (keyPair, *x509.Certificate, *rsa.PrivateKey, error) {
	var sec corev1.Secret

	err := r.Get(ctx, r.key(oidctrust.CASecret), &sec)
	if err != nil && !apierrors.IsNotFound(err) {
		return keyPair{}, nil, nil, fmt.Errorf("reading CA: %w", err)
	}

	have := keyPair{sec.Data[oidctrust.CertKey], sec.Data[oidctrust.PrivateKeyKey]}
	if cert, key, ok := caUsable(have, now); ok {
		return have, cert, key, nil
	}

	fresh, err := newCA(now)
	if err != nil {
		return keyPair{}, nil, nil, err
	}

	if err := r.apply(ctx, &sec, oidctrust.CASecret, corev1.SecretTypeTLS, map[string][]byte{
		oidctrust.CertKey: fresh.certPEM, oidctrust.PrivateKeyKey: fresh.keyPEM,
	}); err != nil {
		return keyPair{}, nil, nil, err
	}

	ctrl.LoggerFrom(ctx).Info("Issuer internal CA created")

	cert, key, ok := caUsable(fresh, now)
	if !ok {
		return keyPair{}, nil, nil, errors.New("a freshly made CA does not parse")
	}

	return fresh, cert, key, nil
}

// ensureLeaf returns a leaf that matches spec and is signed by ca, reissuing otherwise. The second
// result is the leaf's NotAfter.
func (r *IssuerTrustReconciler) ensureLeaf(
	ctx context.Context, ca keyPair, caCert *x509.Certificate, caKey *rsa.PrivateKey, spec leafSpec, now time.Time,
) (keyPair, time.Time, error) {
	var sec corev1.Secret

	err := r.Get(ctx, r.key(oidctrust.TLSSecret), &sec)
	if err != nil && !apierrors.IsNotFound(err) {
		return keyPair{}, time.Time{}, fmt.Errorf("reading serving certificate: %w", err)
	}

	have := keyPair{sec.Data[oidctrust.CertKey], sec.Data[oidctrust.PrivateKeyKey]}
	if leafUsable(have, caCert, spec, now) && bytes.Equal(sec.Data[oidctrust.CAKey], ca.certPEM) {
		c, _ := decodeCert(have.certPEM)

		return have, c.NotAfter, nil
	}

	fresh, err := newLeaf(caCert, caKey, spec, now)
	if err != nil {
		return keyPair{}, time.Time{}, err
	}

	if err := r.apply(ctx, &sec, oidctrust.TLSSecret, corev1.SecretTypeTLS, map[string][]byte{
		oidctrust.CertKey: fresh.certPEM, oidctrust.PrivateKeyKey: fresh.keyPEM, oidctrust.CAKey: ca.certPEM,
	}); err != nil {
		return keyPair{}, time.Time{}, err
	}

	ctrl.LoggerFrom(ctx).Info("Issuer serving certificate issued", "ip", spec.ip.String())

	c, _ := decodeCert(fresh.certPEM)

	return fresh, c.NotAfter, nil
}

// ensureTrustMap publishes the public parts. A leaf renewal leaves it untouched.
func (r *IssuerTrustReconciler) ensureTrustMap(ctx context.Context, ip net.IP, caPEM []byte) error {
	want := map[string]string{
		oidctrust.DiscoveryURLKey: fmt.Sprintf("https://%s/.well-known/openid-configuration",
			net.JoinHostPort(ip.String(), fmt.Sprint(oidctrust.InternalPort))),
		oidctrust.CAKey: string(caPEM),
	}

	var cm corev1.ConfigMap

	err := r.Get(ctx, r.key(oidctrust.TrustConfigMap), &cm)

	switch {
	case apierrors.IsNotFound(err):
		cm = corev1.ConfigMap{
			Namespace: r.Namespace, Name: oidctrust.TrustConfigMap,
			Labels: map[string]string{managedByLabel: managedByValue}, Data: want,
		}

		if err := r.Create(ctx, &cm); err != nil {
			return fmt.Errorf("creating ConfigMap %s: %w", oidctrust.TrustConfigMap, err)
		}

		return nil
	case err != nil:
		return fmt.Errorf("reading ConfigMap %s: %w", oidctrust.TrustConfigMap, err)
	}

	if len(cm.BinaryData) == 0 && len(cm.Data) == len(want) &&
		cm.Data[oidctrust.DiscoveryURLKey] == want[oidctrust.DiscoveryURLKey] &&
		cm.Data[oidctrust.CAKey] == want[oidctrust.CAKey] {
		return nil
	}

	cm.Data, cm.BinaryData = want, nil

	if err := r.Update(ctx, &cm); err != nil {
		return fmt.Errorf("updating ConfigMap %s: %w", oidctrust.TrustConfigMap, err)
	}

	return nil
}

// setExpiring sets ConditionIssuerCertificatesExpiring on every TalosCluster in the namespace.
func (r *IssuerTrustReconciler) setExpiring(ctx context.Context, now, caEnd, leafEnd time.Time) error {
	window := r.ExpiryWarning
	if window <= 0 {
		window = DefaultCertificateExpiryWarning
	}

	var soon []string

	for _, c := range []struct {
		name string
		end  time.Time
	}{{"CA", caEnd}, {"serving certificate", leafEnd}} {
		if c.end.Before(now.Add(window)) {
			soon = append(soon, fmt.Sprintf("the issuer %s expires on %s", c.name, c.end.UTC().Format(time.DateOnly)))
		}
	}

	want := metav1.Condition{
		Type: ConditionIssuerCertificatesExpiring, Status: metav1.ConditionFalse, Reason: "NoneExpiring",
		Message: fmt.Sprintf("neither issuer certificate expires within %d days", int(window.Hours()/24)),
	}

	if len(soon) > 0 {
		want.Status, want.Reason = metav1.ConditionTrue, "ExpiringSoon"
		want.Message = strings.Join(soon, "; ") + ". The controller renews them on its own; this stays True only if it cannot."
	}

	var clusters ichorv1alpha1.TalosClusterList
	if err := r.List(ctx, &clusters, client.InNamespace(r.Namespace)); err != nil {
		return fmt.Errorf("listing TalosClusters: %w", err)
	}

	for i := range clusters.Items {
		c := &clusters.Items[i]

		before := apimeta.FindStatusCondition(c.Status.Conditions, want.Type)
		if before != nil && before.Status == want.Status && before.Reason == want.Reason && before.Message == want.Message {
			continue
		}

		base := c.DeepCopy()
		want.ObservedGeneration = c.Generation
		apimeta.SetStatusCondition(&c.Status.Conditions, want)

		if err := r.Status().Patch(ctx, c, client.MergeFrom(base)); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("setting %s on %s: %w", want.Type, c.Name, err)
		}
	}

	return nil
}

// credentialEntry is the part of a credentials value the controller reads. It never copies the hash.
type credentialEntry struct {
	Version int    `json:"v"`
	Hash    string `json:"hash"`
}

func passwordSet(raw []byte) bool {
	var e credentialEntry

	return json.Unmarshal(raw, &e) == nil && e.Version == 1 && strings.HasPrefix(e.Hash, credentialsShape)
}

// reconcileUsers writes IchorUser.status and removes credentials entries of deleted users.
func (r *IssuerTrustReconciler) reconcileUsers(ctx context.Context, now time.Time) error {
	var creds corev1.Secret

	err := r.Get(ctx, r.key(oidctrust.CredentialsSecret), &creds)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("reading credentials: %w", err)
	}

	var users ichorv1alpha1.IchorUserList
	if err := r.List(ctx, &users, client.InNamespace(r.Namespace)); err != nil {
		return fmt.Errorf("listing IchorUsers: %w", err)
	}

	for i := range users.Items {
		if err := r.setUserStatus(ctx, &users.Items[i], creds.Data[users.Items[i].Name]); err != nil {
			return err
		}
	}

	return r.collectOrphans(ctx, now, &creds)
}

func (r *IssuerTrustReconciler) setUserStatus(ctx context.Context, u *ichorv1alpha1.IchorUser, entry []byte) error {
	pw := metav1.Condition{
		Type: ichorv1alpha1.IchorUserConditionPasswordSet, Status: metav1.ConditionFalse, Reason: "NoPassword",
		Message: "no password is set; an admin has to set one before this user can sign in",
	}

	if passwordSet(entry) {
		pw.Status, pw.Reason, pw.Message = metav1.ConditionTrue, "PasswordSet", "a password is set"
	}

	ready := metav1.Condition{
		Type: ichorv1alpha1.IchorUserConditionReady, Status: metav1.ConditionTrue, Reason: "Ready",
		Message: "the user can sign in",
	}

	switch {
	case u.Spec.Disabled:
		ready.Status, ready.Reason, ready.Message = metav1.ConditionFalse, "Disabled", "spec.disabled is true"
	case pw.Status != metav1.ConditionTrue:
		ready.Status, ready.Reason, ready.Message = metav1.ConditionFalse, "NoPassword", pw.Message
	}

	base := u.DeepCopy()
	changed := false

	for _, c := range []metav1.Condition{pw, ready} {
		c.ObservedGeneration = u.Generation

		before := apimeta.FindStatusCondition(u.Status.Conditions, c.Type)
		if before != nil && before.Status == c.Status && before.Reason == c.Reason &&
			before.Message == c.Message && before.ObservedGeneration == c.ObservedGeneration {
			continue
		}

		apimeta.SetStatusCondition(&u.Status.Conditions, c)

		changed = true
	}

	if !changed {
		return nil
	}

	if err := r.Status().Patch(ctx, u, client.MergeFrom(base)); client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("setting status of IchorUser %s: %w", u.Name, err)
	}

	return nil
}

// collectOrphans removes credentials entries that no IchorUser names, once they have been
// orphaned for the grace period. The decision uses a live list, not the cache.
func (r *IssuerTrustReconciler) collectOrphans(ctx context.Context, now time.Time, creds *corev1.Secret) error {
	if r.orphanSince == nil {
		r.orphanSince = map[string]time.Time{}
	}

	if creds.UID == "" || len(creds.Data) == 0 {
		clear(r.orphanSince)

		return nil
	}

	var live ichorv1alpha1.IchorUserList

	reader := r.APIReader
	if reader == nil {
		reader = r.Client
	}

	if err := reader.List(ctx, &live, client.InNamespace(r.Namespace)); err != nil {
		return fmt.Errorf("listing IchorUsers for clean-up: %w", err)
	}

	known := map[string]bool{}
	for _, u := range live.Items {
		known[u.Name] = true
	}

	grace := r.OrphanGrace
	if grace <= 0 {
		grace = issuerOrphanGrace
	}

	var drop []string

	for name := range creds.Data {
		if known[name] {
			delete(r.orphanSince, name)

			continue
		}

		first, seen := r.orphanSince[name]
		if !seen {
			r.orphanSince[name] = now

			continue
		}

		if now.Sub(first) >= grace {
			drop = append(drop, name)
		}
	}

	for name := range r.orphanSince {
		if _, ok := creds.Data[name]; !ok {
			delete(r.orphanSince, name)
		}
	}

	if len(drop) == 0 {
		return nil
	}

	slices.Sort(drop)

	// A JSON merge patch of only the dropped keys, so a concurrent write of another user's entry
	// by ichor-ui is never overwritten.
	patch := map[string]any{"data": map[string]any{}}
	for _, name := range drop {
		patch["data"].(map[string]any)[name] = nil
	}

	raw, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("encoding credentials patch: %w", err)
	}

	if err := r.Patch(ctx, creds, client.RawPatch(types.MergePatchType, raw)); err != nil {
		return fmt.Errorf("removing credentials of deleted users: %w", err)
	}

	for _, name := range drop {
		delete(r.orphanSince, name)
	}

	ctrl.LoggerFrom(ctx).Info("Removed credentials entries of deleted users", "users", drop)

	return nil
}

// SetupWithManager wires every event that can change the outcome to one reconcile.
func (r *IssuerTrustReconciler) SetupWithManager(mgr ctrl.Manager) error {
	one := handler.EnqueueRequestsFromMapFunc(func(context.Context, client.Object) []reconcile.Request {
		return []reconcile.Request{{NamespacedName: r.key("issuer-trust")}}
	})

	named := predicate.NewPredicateFuncs(func(o client.Object) bool {
		if o.GetNamespace() != r.Namespace {
			return false
		}

		return slices.Contains([]string{
			oidctrust.SigningSecret, oidctrust.CASecret, oidctrust.TLSSecret, oidctrust.TrustConfigMap,
			oidctrust.CredentialsSecret, r.serviceName(),
		}, o.GetName())
	})

	return ctrl.NewControllerManagedBy(mgr).
		Named("issuertrust").
		Watches(&corev1.Secret{}, one, builder.WithPredicates(named)).
		Watches(&corev1.ConfigMap{}, one, builder.WithPredicates(named)).
		Watches(&corev1.Service{}, one, builder.WithPredicates(named)).
		Watches(&ichorv1alpha1.IchorUser{}, one).
		Complete(r)
}
