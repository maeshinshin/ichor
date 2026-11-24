package installer

import (
	"context"
	"errors"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// signInManifest is a target manifest of the two Deployments the installer customizes and one CRD.
func signInManifest(props ...string) []byte {
	dep := func(name, container string) string {
		return "---\napiVersion: apps/v1\nkind: Deployment\nmetadata: {name: " + name + ", namespace: ns}\nspec:\n  selector:\n    matchLabels: {app: " + name + "}\n" +
			"  template:\n    metadata:\n      labels: {app: " + name + "}\n    spec:\n      containers:\n      - name: " + container + "\n        image: x\n        args: [--addr=:8443]\n"
	}

	return []byte(checkCRDManifest(props...) + dep("ichor-ichor-ui", UIContainer) + dep("ichor-controller-manager", ControllerContainer))
}

// signInClient holds the live Deployments, with a sign-in (Ichor users) or without, and a CRD with liveProps.
func signInClient(t *testing.T, signedIn bool, liveProps ...string) client.Client {
	t.Helper()

	args := []string{"--addr=:8443"}
	if signedIn {
		args = append(args, "--users-enabled=true", "--external-url=https://ui.example")
	}

	ui := &appsv1.Deployment{}
	ui.Name, ui.Namespace = "ichor-ichor-ui", "ns"
	ui.Spec.Template.Spec.Containers = []corev1.Container{{Name: UIContainer, Args: args}}

	ctrl := &appsv1.Deployment{}
	ctrl.Name, ctrl.Namespace = "ichor-controller-manager", "ns"
	ctrl.Spec.Template.Spec.Containers = []corev1.Container{{Name: ControllerContainer}}

	return checkFixtureClient(t, liveProps, ui, ctrl)
}

// An ichor-ui with no sign-in refuses Check, unless the caller says it turns sign-in on; Run never takes that word.
// With sign-in pending, Check reaches the verdict Run reaches once sign-in is on.
func TestCheckWithASignInPendingMatchesRunAfterSignIn(t *testing.T) {
	ctx := context.Background()
	opts := func(pending bool, props ...string) Options {
		return Options{Namespace: "ns", RunID: "run", Manifest: signInManifest(props...), SignInPending: pending}
	}

	if err := Check(ctx, signInClient(t, false, kSize), opts(false, kSize)); !errors.Is(err, errNoSignInToUpgrade) {
		t.Fatalf("Check without the flag on a sign-in-less ichor-ui: want errNoSignInToUpgrade, got %v", err)
	}

	if err := Check(ctx, signInClient(t, false, kSize), opts(true, kSize)); err != nil {
		t.Fatalf("Check with sign-in pending refused: %v", err)
	}

	if err := Run(ctx, signInClient(t, false, kSize), opts(true, kSize)); !errors.Is(err, errNoSignInToUpgrade) {
		t.Fatalf("Run must stay strict, got %v", err)
	}

	// Everything else is judged the same: a CRD that would lose a property refuses both, with the same words.
	checkErr := Check(ctx, signInClient(t, false, "color", kSize), opts(true, kSize))
	runErr := Run(ctx, signInClient(t, true, "color", kSize), opts(false, kSize))

	if checkErr == nil || runErr == nil || checkErr.Error() != runErr.Error() || !strings.Contains(runErr.Error(), "color would be removed") {
		t.Errorf("the verdicts differ:\n Check(pending): %v\n Run(signed in): %v", checkErr, runErr)
	}
}
