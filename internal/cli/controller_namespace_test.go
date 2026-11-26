package cli

import (
	"bytes"
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
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

// --add-machine skips the namespace check, so it finds the controller's namespace itself: the
// bundle Secret is read there, not in ichor-system.
func TestResolveClusterBundleReadsTheSecretFromTheControllerNamespace(t *testing.T) {
	cluster := newTestCluster()
	bundle := newTestBundle(t)

	secret, err := buildBundleSecret(cluster, bundle)
	if err != nil {
		t.Fatal(err)
	}

	secret.Namespace = helmControllerNamespace
	controller := &appsv1.Deployment{
		Name: controllerDeploymentName, Namespace: helmControllerNamespace,
		Labels: map[string]string{controllerLabel: controllerLabelValue},
	}
	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cluster, secret, controller).Build()

	if _, err := resolveClusterBundle(context.Background(), k8s, testNamespace, cluster.Name); err != nil {
		t.Fatal(err)
	}
}

// The bundle Secret every writer builds (init, adopt) carries the label the delete-protection policy matches.
func TestBundleSecretIsProtected(t *testing.T) {
	secret, err := buildBundleSecret(newTestCluster(), newTestBundle(t))
	if err != nil {
		t.Fatal(err)
	}

	if got := secret.Labels[deleteguard.ProtectedLabel]; got != deleteguard.ProtectedValue {
		t.Errorf("label %s = %q, want %q", deleteguard.ProtectedLabel, got, deleteguard.ProtectedValue)
	}
}
