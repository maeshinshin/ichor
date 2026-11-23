package cli

import (
	"bytes"
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const helmControllerNamespace = "tenant"

// An Ichor controller from a Helm install in another namespace reads authSecretRef Secrets from
// there; adopt resolves the same ones.
func TestAdoptReadsAuthSecretsFromTheControllerNamespace(t *testing.T) {
	cluster := newTestCluster()
	cluster.Namespace = helmControllerNamespace
	cluster.Spec.Registries = []ichorv1alpha1.TalosClusterRegistry{{
		Host: "registry.example", AuthSecretRef: &corev1.LocalObjectReference{Name: "reg-auth"},
	}}

	secret := &corev1.Secret{
		Namespace: helmControllerNamespace, Name: "reg-auth", Data: map[string][]byte{"password": []byte("pw")},
	}
	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cluster, secret).Build()

	st, err := loadAdoption(context.Background(), cluster.Name, helmControllerNamespace, adoptDeps{K8s: k8s})
	if err != nil {
		t.Fatalf("loadAdoption: %v", err)
	}

	if got := st.sys.RegistryAuth["registry.example"].Password; got != "pw" {
		t.Fatalf("registry password = %q, want %q read from the Secret in %s", got, "pw", helmControllerNamespace)
	}
}

// The readiness wait looks for the controller Deployment in the namespace it is given.
func TestWaitForWebhookServerFindsTheControllerInAnotherNamespace(t *testing.T) {
	d := readyControllerDeployment(1)
	d.Namespace = helmControllerNamespace
	s := webhookSlice(nil)
	s.Namespace = helmControllerNamespace
	c := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(d, s).Build()

	if err := waitForWebhookServer(context.Background(), c, helmControllerNamespace, time.Second, time.Millisecond, &bytes.Buffer{}); err != nil {
		t.Fatalf("waitForWebhookServer: %v", err)
	}
}
