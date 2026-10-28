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
	"slices"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// `ichor init --ui-url` also takes the chart's ichorUI.tls.secretName and
// ichorUI.users.kubeAPIServerURL / kubeAPITLSServerName, applied in place to the
// ichor-ui Deployment the way the chart renders them.

const (
	// uiTLSStoredSecretName is the Secret --ui-tls-cert/--ui-tls-key are stored in.
	uiTLSStoredSecretName = "ichor-ui-tls"
	uiTLSVolume           = "tls"
	uiTLSMountPath        = "/etc/ichor/tls"
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

	if opts.UIURL == "" {
		return errors.New("init: --ui-tls-secret, --ui-tls-cert, --ui-tls-key, --kube-api-server-url and --kube-api-tls-server-name need --ui-url")
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
		err = retryInit(ctx, progress, func() error { return k.Update(ctx, sec) })
	}

	if err != nil {
		return fmt.Errorf("init: storing the ichor-ui TLS Secret: %w", err)
	}

	return nil
}

// uiExtrasOnDeployment applies the TLS and kubeconfig flags to the ichor-ui
// Deployment in memory, as the chart renders them.
func uiExtrasOnDeployment(dep *appsv1.Deployment, opts initOptions) error {
	i := slices.IndexFunc(dep.Spec.Template.Spec.Containers, func(c corev1.Container) bool { return c.Name == uiContainer })
	if i < 0 {
		return fmt.Errorf("init: the ichor-ui Deployment %s has no container %q", dep.Name, uiContainer)
	}

	c := &dep.Spec.Template.Spec.Containers[i]

	c.Args = slices.DeleteFunc(c.Args, func(a string) bool {
		return strings.HasPrefix(a, "--kube-api-server-url=") || strings.HasPrefix(a, "--kube-api-tls-server-name=")
	})

	if opts.KubeAPIServerURL != "" {
		c.Args = append(c.Args, "--kube-api-server-url="+opts.KubeAPIServerURL)
	}

	if opts.KubeAPITLSServerName != "" {
		c.Args = append(c.Args, "--kube-api-tls-server-name="+opts.KubeAPITLSServerName)
	}

	if !opts.uiTLSOn() {
		return nil
	}

	port, err := uiListenPort(c.Args)
	if err != nil {
		return fmt.Errorf("init: ichor-ui Deployment %s: %w", dep.Name, err)
	}

	c.Args = slices.DeleteFunc(c.Args, func(a string) bool {
		return strings.HasPrefix(a, "--tls-cert-file=") || strings.HasPrefix(a, "--tls-key-file=")
	})
	c.Args = append(c.Args, "--tls-cert-file="+uiTLSMountPath+"/tls.crt", "--tls-key-file="+uiTLSMountPath+"/tls.key")

	// A kubelet TCP probe makes a TLS server log a handshake error every few seconds, so probe over HTTPS.
	probe := func(period int32) *corev1.Probe {
		return &corev1.Probe{
			HTTPGet: &corev1.HTTPGetAction{
				Path: "/auth/config", Port: intstr.FromInt32(port), Scheme: corev1.URISchemeHTTPS,
			},
			InitialDelaySeconds: 5, PeriodSeconds: period,
		}
	}
	c.LivenessProbe, c.ReadinessProbe = probe(20), probe(10)

	vols := slices.DeleteFunc(dep.Spec.Template.Spec.Volumes, func(v corev1.Volume) bool { return v.Name == uiTLSVolume })
	dep.Spec.Template.Spec.Volumes = append(vols, corev1.Volume{
		Name: uiTLSVolume, Secret: &corev1.SecretVolumeSource{SecretName: opts.uiTLSSecretName()},
	})

	c.VolumeMounts = slices.DeleteFunc(c.VolumeMounts, func(m corev1.VolumeMount) bool { return m.Name == uiTLSVolume })
	c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{Name: uiTLSVolume, MountPath: uiTLSMountPath, ReadOnly: true})

	return nil
}

// uiListenPort reads the port of ichor-ui's --addr.
func uiListenPort(args []string) (int32, error) {
	for _, a := range args {
		v, ok := strings.CutPrefix(a, "--addr=")
		if !ok {
			continue
		}

		_, p, err := net.SplitHostPort(v)
		if err != nil {
			return 0, fmt.Errorf("cannot read the port from %q: %w", a, err)
		}

		n, err := strconv.ParseInt(p, 10, 32)
		if err != nil {
			return 0, fmt.Errorf("cannot read the port from %q: %w", a, err)
		}

		return int32(n), nil
	}

	return 0, errors.New("no --addr argument to take the listening port from")
}
