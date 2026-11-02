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
	"fmt"
	"time"

	"github.com/siderolabs/crypto/x509"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/credentials"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
)

// credentialRequestLifetime is how long a TalosCredentialRequest lives: the requester has had its
// answer long before, and the object is only an audit trail the API server's own log already keeps.
const credentialRequestLifetime = 2 * time.Minute

// TalosCredentialRequestReconciler answers TalosCredentialRequests (ADR 0108): it is the only place
// outside the CLI that reads the secrets bundle for a credential, so that ichor-ui needs no CA key. It
// signs the public key of a request the admission webhook has already judged, and deletes the request
// once it is old.
type TalosCredentialRequestReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// ControllerNamespace is where the bundle Secrets live (ADR 0011).
	ControllerNamespace string
}

// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=taloscredentialrequests,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=taloscredentialrequests/status,verbs=get;update;patch

// Reconcile signs a pending request, or deletes an old one.
func (r *TalosCredentialRequestReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var cr ichorv1alpha1.TalosCredentialRequest
	if err := r.Get(ctx, req.NamespacedName, &cr); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if left := credentialRequestLifetime - time.Since(cr.CreationTimestamp.Time); left <= 0 {
		return ctrl.Result{}, client.IgnoreNotFound(r.Delete(ctx, &cr))
	} else if cr.Status.Phase != "" {
		return ctrl.Result{RequeueAfter: left}, nil
	}

	status := r.issue(ctx, &cr)
	cr.Status = status

	if err := r.Status().Update(ctx, &cr); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, err
	}

	logf.FromContext(ctx).Info("credential request answered", "purpose", cr.Spec.Purpose, "phase", status.Phase,
		"cluster", cr.Spec.ClusterRef.Name)

	return ctrl.Result{RequeueAfter: credentialRequestLifetime}, nil
}

// certOf is the certificate half of a CA, empty when the bundle has none.
func certOf(p *x509.PEMEncodedCertificateAndKey) string {
	if p == nil {
		return ""
	}

	return string(p.Crt)
}

func failed(format string, args ...any) ichorv1alpha1.TalosCredentialRequestStatus {
	return ichorv1alpha1.TalosCredentialRequestStatus{
		Phase: ichorv1alpha1.CredentialPhaseFailed, Message: fmt.Sprintf(format, args...),
	}
}

// issue builds the answer. Its messages never carry key material: every error here is structural.
func (r *TalosCredentialRequestReconciler) issue(ctx context.Context, cr *ichorv1alpha1.TalosCredentialRequest) ichorv1alpha1.TalosCredentialRequestStatus {
	spec := &cr.Spec

	var cluster ichorv1alpha1.TalosCluster
	if err := r.Get(ctx, types.NamespacedName{Namespace: cr.Namespace, Name: spec.ClusterRef.Name}, &cluster); err != nil {
		if apierrors.IsNotFound(err) {
			return failed("cluster %q not found", spec.ClusterRef.Name)
		}

		return failed("cluster %q could not be read", spec.ClusterRef.Name)
	}

	var ttl time.Duration
	if spec.TTL != nil {
		ttl = spec.TTL.Duration
	}

	ttl, err := credentials.PurposeLifetime(spec.Purpose, ttl)
	if err != nil {
		return failed("%v", err)
	}

	bundle, err := loadBundle(ctx, r.Client, r.ControllerNamespace, &cluster)
	if err != nil {
		return failed("the cluster's secrets bundle could not be read")
	}

	if spec.Purpose == ichorv1alpha1.CredentialPurposePublic {
		return ichorv1alpha1.TalosCredentialRequestStatus{
			Phase: ichorv1alpha1.CredentialPhaseIssued,
			Public: &ichorv1alpha1.TalosCredentialPublic{
				ClusterID: bundle.Cluster.ID, TalosCA: certOf(bundle.Certs.OS), KubernetesCA: certOf(bundle.Certs.K8s),
				EtcdCA: certOf(bundle.Certs.Etcd), KubernetesAggregatorCA: certOf(bundle.Certs.K8sAggregator),
			},
		}
	}

	// A call's certificate is signed by the CA the nodes trust now; an operator credential by the new CA
	// during a rotation, trusting both (ADR 0091).
	var rot *secretsbundle.Rotation

	if !credentials.IsDialPurpose(spec.Purpose) {
		if rot, err = secretsbundle.LoadRotation(ctx, r.Client, r.ControllerNamespace, cluster.Spec.SecretsRef.Name); err != nil {
			return failed("the cluster's rotation state could not be read")
		}
	}

	src, err := credentials.NewSources(bundle, rot)
	if err != nil {
		return failed("%v", err)
	}

	var (
		crt, trust []byte
		notAfter   time.Time
	)

	switch spec.Purpose {
	case ichorv1alpha1.CredentialPurposeCredentialsKubernetes:
		crt, notAfter, err = src.IssueKubernetes([]byte(spec.CSR), ttl)
		trust = src.KubernetesTrust
	default:
		roles, rerr := credentials.TalosRoles(spec.Purpose, spec.TalosRole)
		if rerr != nil {
			return failed("%v", rerr)
		}

		crt, notAfter, err = src.IssueTalos([]byte(spec.CSR), roles, ttl)
		trust = src.TalosTrust
	}

	if err != nil {
		return failed("%v", err)
	}

	na := metav1.NewTime(notAfter)

	return ichorv1alpha1.TalosCredentialRequestStatus{
		Phase: ichorv1alpha1.CredentialPhaseIssued, Certificate: string(crt), Trust: string(trust), NotAfter: &na,
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *TalosCredentialRequestReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ichorv1alpha1.TalosCredentialRequest{}).
		Named("taloscredentialrequest").
		Complete(r)
}
