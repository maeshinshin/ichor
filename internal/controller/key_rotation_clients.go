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

	"github.com/go-jose/go-jose/v4"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// What a key rotation does inside the managed cluster it does with the admin kubeconfig a Talos
// node issues, the way NodesReady and the root-CA check reach it: the controller's own ServiceAccount gets no
// right on Secrets in the cluster it manages, and the credential exists only for the call.

// ReencryptResult is what one complete pass over the cluster's Secrets found.
type ReencryptResult struct {
	// Scanned is the number of Secrets updated.
	Scanned int64
	// Rewritten is how many of them the API server stored again: their resourceVersion changed, which it does
	// only for a Secret that another key than the current one had encrypted.
	Rewritten int64
	// Conflicts is how many changed while they were being updated, so this pass did not look at them.
	Conflicts int64
}

// Clean reports whether the pass proves no Secret is left under the old key: it wrote nothing and left nothing out.
func (r ReencryptResult) Clean() bool { return r.Rewritten == 0 && r.Conflicts == 0 }

// ReencryptSecretsFunc runs one complete pass over every Secret in the cluster that talos belongs to, updating
// each with its own content so that the API server stores it under the current encryption key (the
// `kubectl get secrets -A -o json | kubectl replace -f -` of the Kubernetes procedure). A seam so tests never
// reach a cluster; cmd/main.go wires NewReencryptSecrets.
type ReencryptSecretsFunc func(ctx context.Context, talos TalosClient) (ReencryptResult, error)

// LegacyTokensFunc lists the Secrets of type kubernetes.io/service-account-token ("namespace/name") whose
// token the service-account public key oldPublicKey (PEM) verifies: they stop working when that key is dropped.
// A seam; cmd/main.go wires NewLegacyTokens.
type LegacyTokensFunc func(ctx context.Context, talos TalosClient, oldPublicKey []byte) ([]string, error)

// reencryptPage is how many Secrets one list call returns.
const reencryptPage = 500

// adminClientQPS is the rate of a re-encryption pass. client-go's default (5 requests a second) would take
// hours for a cluster with thousands of Secrets.
const (
	adminClientQPS   = 100
	adminClientBurst = 200
)

// adminClientset builds a clientset from an admin kubeconfig the Talos API issues.
func adminClientset(ctx context.Context, talos TalosClient) (kubernetes.Interface, error) {
	cfg, err := restConfig(ctx, talos)
	if err != nil {
		return nil, err
	}

	cfg.QPS, cfg.Burst = adminClientQPS, adminClientBurst

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("building a clientset from the admin kubeconfig: %w", err)
	}

	return cs, nil
}

// NewReencryptSecrets returns the ReencryptSecretsFunc cmd/main.go wires in.
func NewReencryptSecrets() ReencryptSecretsFunc {
	return func(ctx context.Context, talos TalosClient) (ReencryptResult, error) {
		cs, err := adminClientset(ctx, talos)
		if err != nil {
			return ReencryptResult{}, err
		}

		return reencryptSweep(ctx, cs)
	}
}

// reencryptSweep updates every Secret with the content it was listed with. The API server skips the write
// of an object whose stored form is current and rewrites one whose stored form another key produced
// (etcd3 store GuaranteedUpdate: `!origState.stale && bytes.Equal(data, origState.data)`), so a changed
// resourceVersion in the response is exactly a Secret that was moved to the current key.
//
// ponytail: one sweep is sequential and bounded only by ctx; a cluster with hundreds of thousands of Secrets
// restarts its sweep every reconcile and may never finish. A continue token kept in status would fix that.
func reencryptSweep(ctx context.Context, cs kubernetes.Interface) (ReencryptResult, error) {
	var res ReencryptResult

	opts := metav1.ListOptions{Limit: reencryptPage}

	for {
		page, err := cs.CoreV1().Secrets(metav1.NamespaceAll).List(ctx, opts)
		if err != nil {
			return res, fmt.Errorf("listing Secrets: %w", err)
		}

		for i := range page.Items {
			secret := &page.Items[i]

			updated, err := cs.CoreV1().Secrets(secret.Namespace).Update(ctx, secret, metav1.UpdateOptions{})

			switch {
			case apierrors.IsNotFound(err):
				continue // deleted since it was listed: nothing left to move
			case apierrors.IsConflict(err):
				res.Scanned++
				res.Conflicts++

				continue
			case err != nil:
				return res, fmt.Errorf("updating Secret %s/%s: %w", secret.Namespace, secret.Name, err)
			}

			res.Scanned++

			if updated.ResourceVersion != secret.ResourceVersion {
				res.Rewritten++
			}
		}

		if page.Continue == "" {
			return res, nil
		}

		opts.Continue = page.Continue
	}
}

// NewLegacyTokens returns the LegacyTokensFunc cmd/main.go wires in.
func NewLegacyTokens() LegacyTokensFunc {
	return func(ctx context.Context, talos TalosClient, oldPublicKey []byte) ([]string, error) {
		cs, err := adminClientset(ctx, talos)
		if err != nil {
			return nil, err
		}

		return legacyTokensSignedBy(ctx, cs, oldPublicKey)
	}
}

// serviceAccountSigningAlgs are the algorithms kube-apiserver can sign a service-account token with.
var serviceAccountSigningAlgs = []jose.SignatureAlgorithm{
	jose.RS256, jose.RS384, jose.RS512, jose.ES256, jose.ES384, jose.ES512,
}

// legacyTokensSignedBy returns "namespace/name" of every token Secret whose token the public key (PEM)
// verifies. The tokens are read to check a signature and are never stored, logged or returned.
func legacyTokensSignedBy(ctx context.Context, cs kubernetes.Interface, publicKeyPEM []byte) ([]string, error) {
	block, _ := pem.Decode(publicKeyPEM)
	if block == nil {
		return nil, errors.New("the service-account public key is not PEM")
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, errors.New("the service-account public key cannot be parsed")
	}

	var out []string

	opts := metav1.ListOptions{Limit: reencryptPage, FieldSelector: "type=" + string(corev1.SecretTypeServiceAccountToken)}

	for {
		page, err := cs.CoreV1().Secrets(metav1.NamespaceAll).List(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("listing service-account token Secrets: %w", err)
		}

		for i := range page.Items {
			secret := &page.Items[i]

			// The field selector is the server's job; the type is checked again so a server that ignores it
			// cannot make an ordinary Secret look like a token.
			if secret.Type != corev1.SecretTypeServiceAccountToken {
				continue
			}

			if verifiesWith(secret.Data[corev1.ServiceAccountTokenKey], pub) {
				out = append(out, secret.Namespace+"/"+secret.Name)
			}
		}

		if page.Continue == "" {
			return out, nil
		}

		opts.Continue = page.Continue
	}
}

// verifiesWith reports whether token is a JWT signed by the key whose public half is pub.
func verifiesWith(token []byte, pub any) bool {
	if len(token) == 0 {
		return false
	}

	sig, err := jose.ParseSignedCompact(string(token), serviceAccountSigningAlgs)
	if err != nil {
		return false
	}

	_, err = sig.Verify(pub)

	return err == nil
}
