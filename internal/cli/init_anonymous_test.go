package cli

import (
	"bytes"
	"context"
	"slices"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const uiManifest = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: ichor-ichor-ui
  namespace: ichor-system
spec:
  selector:
    matchLabels: {app: ui}
  template:
    metadata:
      labels: {app: ui}
    spec:
      containers:
      - name: ichor-ui
        image: x
        args: ["--addr=:8443"]
`

func TestInitStartsIchorUIWithAnExplicitAllowUnauthenticated(t *testing.T) {
	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()

	if err := applyIchorManifest(context.Background(), k8sClient, []byte(uiManifest), &bytes.Buffer{}, false, true); err != nil {
		t.Fatal(err)
	}

	var dep appsv1.Deployment
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: "ichor-system", Name: "ichor-ichor-ui"}, &dep); err != nil {
		t.Fatal(err)
	}

	if args := dep.Spec.Template.Spec.Containers[0].Args; !slices.Contains(args, "--allow-unauthenticated=true") {
		t.Fatalf("a fresh init must keep ichor-ui open explicitly, args = %v", args)
	}

	var out bytes.Buffer

	initEpilogue(newProgress(&out), initOptions{AllowUnauthenticated: true}, "", true)

	if !bytes.Contains(out.Bytes(), []byte("--allow-unauthenticated")) {
		t.Fatalf("init prints no warning with --allow-unauthenticated: %q", out.String())
	}
}
