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

package cli

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// `ichor init --ui-url` also takes the chart's ichorUI.tls.secretName and
// ichorUI.users.kubeAPIServerURL / kubeAPITLSServerName, applied in place to the
// ichor-ui Deployment the way the chart renders them.

const (
	// uiTLSStoredSecretName is the Secret --ui-tls-cert/--ui-tls-key are stored in.
	uiTLSStoredSecretName = "ichor-ui-tls"
)

func (o initOptions) uiTLSOn() bool { return o.UITLSSecret != "" || o.UITLSCert != "" }

// uiTLSSecretName is the Secret ichor-ui mounts.
func (o initOptions) uiTLSSecretName() string {
	if o.UITLSSecret != "" {
		return o.UITLSSecret
	}

	return uiTLSStoredSecretName
}

// validateInitUIExtras checks the TLS and kubeconfig flags without touching the cluster.
func validateInitUIExtras(opts initOptions) error {
	set := opts.uiTLSOn() || opts.UITLSKey != "" || opts.KubeAPIServerURL != "" || opts.KubeAPITLSServerName != ""
	if !set {
		return nil
	}

	if !opts.signInOn() {
		return errors.New("init: --ui-tls-secret, --ui-tls-cert, --ui-tls-key, --kube-api-server-url and --kube-api-tls-server-name configure sign-in, and --allow-unauthenticated turns it off")
	}

	var errs []error

	if opts.UITLSSecret != "" && (opts.UITLSCert != "" || opts.UITLSKey != "") {
		errs = append(errs, errors.New("init: --ui-tls-secret and --ui-tls-cert/--ui-tls-key are alternatives, not both"))
	}

	if (opts.UITLSCert == "") != (opts.UITLSKey == "") {
		errs = append(errs, errors.New("init: --ui-tls-cert and --ui-tls-key go together"))
	}

	if opts.UITLSSecret != "" {
		if msgs := validation.IsDNS1123Subdomain(opts.UITLSSecret); len(msgs) > 0 {
			errs = append(errs, fmt.Errorf("init: --ui-tls-secret %q is not a Secret name: %s", opts.UITLSSecret, strings.Join(msgs, "; ")))
		}
	}

	if opts.UITLSCert != "" && opts.UITLSKey != "" {
		if _, _, err := readUITLSFiles(opts); err != nil {
			errs = append(errs, err)
		}
	}

	if opts.KubeAPIServerURL != "" {
		if u, err := url.Parse(opts.KubeAPIServerURL); err != nil || u.Scheme != schemeHTTPS || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			errs = append(errs, fmt.Errorf("init: --kube-api-server-url %q must be an https URL without query, fragment or credentials", opts.KubeAPIServerURL))
		}
	}

	if n := opts.KubeAPITLSServerName; n != "" && len(validation.IsDNS1123Subdomain(n)) > 0 && net.ParseIP(n) == nil {
		errs = append(errs, fmt.Errorf("init: --kube-api-tls-server-name %q is not a host name", n))
	}

	return errors.Join(errs...)
}

// readUITLSFiles reads and checks the certificate and key pair. Errors never carry key bytes.
func readUITLSFiles(opts initOptions) (crt, key []byte, err error) {
	crt, err = os.ReadFile(opts.UITLSCert)
	if err != nil {
		return nil, nil, fmt.Errorf("init: reading --ui-tls-cert: %w", err)
	}

	key, err = os.ReadFile(opts.UITLSKey)
	if err != nil {
		return nil, nil, fmt.Errorf("init: reading --ui-tls-key: %w", err)
	}

	if _, err := tls.X509KeyPair(crt, key); err != nil {
		return nil, nil, fmt.Errorf("init: --ui-tls-cert and --ui-tls-key are not a matching PEM certificate and key: %w", err)
	}

	return crt, key, nil
}

// checkUITLSSecret verifies a named --ui-tls-secret is an existing kubernetes.io/tls Secret.
func checkUITLSSecret(ctx context.Context, k client.Client, ns string, opts initOptions) error {
	if opts.UITLSSecret == "" {
		return nil
	}

	var sec corev1.Secret
	if err := k.Get(ctx, types.NamespacedName{Namespace: ns, Name: opts.UITLSSecret}, &sec); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("init: --ui-tls-secret: no Secret %s in namespace %s (create a kubernetes.io/tls Secret first, or use --ui-tls-cert and --ui-tls-key)", opts.UITLSSecret, ns)
		}

		return fmt.Errorf("init: reading --ui-tls-secret %s: %w", opts.UITLSSecret, err)
	}

	if sec.Type != corev1.SecretTypeTLS {
		return fmt.Errorf("init: Secret %s/%s is of type %q, not %s", ns, sec.Name, sec.Type, corev1.SecretTypeTLS)
	}

	return nil
}

// storeUITLSSecret creates or updates the Secret for --ui-tls-cert/--ui-tls-key.
func storeUITLSSecret(ctx context.Context, k client.Client, ns string, opts initOptions, progress io.Writer) error {
	if opts.UITLSCert == "" {
		return nil
	}

	crt, key, err := readUITLSFiles(opts)
	if err != nil {
		return err
	}

	sec := &corev1.Secret{}
	sec.Namespace, sec.Name = ns, uiTLSStoredSecretName
	data := map[string][]byte{corev1.TLSCertKey: crt, corev1.TLSPrivateKeyKey: key}

	err = k.Get(ctx, client.ObjectKeyFromObject(sec), sec)
	switch {
	case apierrors.IsNotFound(err):
		sec.Labels = map[string]string{"app.kubernetes.io/name": appNameIchor, "app.kubernetes.io/component": "ui"}
		sec.Type, sec.Data = corev1.SecretTypeTLS, data
		err = createRetrying(ctx, k, sec, progress)
	case err == nil:
		if sec.Type != corev1.SecretTypeTLS {
			return fmt.Errorf("init: Secret %s/%s exists with type %q, not %s; remove it or use --ui-tls-secret", ns, sec.Name, sec.Type, corev1.SecretTypeTLS)
		}

		sec.Data = data
		err = updateRetrying(ctx, k, sec, func(s *corev1.Secret) error { s.Data = data; return nil }, progress)
	}

	if err != nil {
		return fmt.Errorf("init: storing the ichor-ui TLS Secret: %w", err)
	}

	return nil
}
