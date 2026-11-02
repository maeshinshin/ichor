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

package uiapi

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/credentials"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
)

// The real controller is in internal/controller, which imports this package, so the suite answers
// TalosCredentialRequests itself, with the same signing code (internal/credentials) and the bundle Secret a
// test created. It stands in for the controller; it checks nothing the controller's own tests do not.
func startFakeController(ctx context.Context, c client.Client) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(20 * time.Millisecond):
			}

			var list ichorv1alpha1.TalosCredentialRequestList
			if c.List(ctx, &list) != nil {
				continue
			}

			for i := range list.Items {
				cr := &list.Items[i]
				if cr.Status.Phase != "" {
					continue
				}

				cr.Status = fakeAnswer(ctx, c, cr)
				_ = c.Status().Update(ctx, cr)
			}
		}
	}()
}

func fakeAnswer(ctx context.Context, c client.Client, cr *ichorv1alpha1.TalosCredentialRequest) ichorv1alpha1.TalosCredentialRequestStatus {
	fail := func(msg string) ichorv1alpha1.TalosCredentialRequestStatus {
		return ichorv1alpha1.TalosCredentialRequestStatus{Phase: ichorv1alpha1.CredentialPhaseFailed, Message: msg}
	}

	var cluster ichorv1alpha1.TalosCluster
	if err := c.Get(ctx, types.NamespacedName{Namespace: cr.Namespace, Name: cr.Spec.ClusterRef.Name}, &cluster); err != nil {
		return fail("no cluster")
	}

	var secrets corev1.SecretList
	if err := c.List(ctx, &secrets); err != nil {
		return fail("no secrets")
	}

	for i := range secrets.Items {
		sec := &secrets.Items[i]
		if sec.Name != cluster.Spec.SecretsRef.Name || sec.Data[secretsbundle.SecretKey] == nil {
			continue
		}

		bundle, err := secretsbundle.Decode(sec.Data[secretsbundle.SecretKey])
		if err != nil {
			return fail("bundle")
		}

		if cr.Spec.Purpose == ichorv1alpha1.CredentialPurposePublic {
			return ichorv1alpha1.TalosCredentialRequestStatus{Phase: ichorv1alpha1.CredentialPhaseIssued, Public: &ichorv1alpha1.TalosCredentialPublic{
				ClusterID: bundle.Cluster.ID, TalosCA: string(bundle.Certs.OS.Crt), KubernetesCA: string(bundle.Certs.K8s.Crt),
				EtcdCA: string(bundle.Certs.Etcd.Crt), KubernetesAggregatorCA: string(bundle.Certs.K8sAggregator.Crt),
			}}
		}

		var rot *secretsbundle.Rotation

		if raw := sec.Data[secretsbundle.RotationKey]; raw != nil && !credentials.IsDialPurpose(cr.Spec.Purpose) {
			if rot, err = secretsbundle.ParseRotation(raw); err != nil {
				return fail("rotation")
			}
		}

		src, err := credentials.NewSources(bundle, rot)
		if err != nil {
			return fail(err.Error())
		}

		var ttl time.Duration
		if cr.Spec.TTL != nil {
			ttl = cr.Spec.TTL.Duration
		}

		if ttl, err = credentials.PurposeLifetime(cr.Spec.Purpose, ttl); err != nil {
			return fail(err.Error())
		}

		var (
			crt, trust []byte
			notAfter   time.Time
		)

		if cr.Spec.Purpose == ichorv1alpha1.CredentialPurposeCredentialsKubernetes {
			crt, notAfter, err = src.IssueKubernetes([]byte(cr.Spec.CSR), ttl)
			trust = src.KubernetesTrust
		} else {
			roles, rerr := credentials.TalosRoles(cr.Spec.Purpose, cr.Spec.TalosRole)
			if rerr != nil {
				return fail(rerr.Error())
			}

			crt, notAfter, err = src.IssueTalos([]byte(cr.Spec.CSR), roles, ttl)
			trust = src.TalosTrust
		}

		if err != nil {
			return fail(err.Error())
		}

		na := metav1.NewTime(notAfter)

		return ichorv1alpha1.TalosCredentialRequestStatus{
			Phase: ichorv1alpha1.CredentialPhaseIssued, Certificate: string(crt), Trust: string(trust), NotAfter: &na,
		}
	}

	return fail("no bundle Secret")
}

// testTalosIdentity is a Talos client identity for a fake node, signed by the shared test bundle's OS CA.
func testTalosIdentity(t *testing.T) *TalosIdentity {
	t.Helper()

	src, err := credentials.NewSources(newTestBundle(), nil)
	if err != nil {
		t.Fatal(err)
	}

	csr, key, err := credentials.NewCSR(false)
	if err != nil {
		t.Fatal(err)
	}

	roles, _ := credentials.TalosRoles(ichorv1alpha1.CredentialPurposeNodeReader, "")

	crt, notAfter, err := src.IssueTalos(csr, roles, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	return &TalosIdentity{CA: src.TalosTrust, Crt: crt, Key: key, NotAfter: notAfter}
}
