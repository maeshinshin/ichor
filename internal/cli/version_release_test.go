package cli

import (
	"context"
	"strings"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// ichorWithRelease is an installation running installed whose controller found latest (ADR 0114).
func ichorWithRelease(t *testing.T, installed, latest string) client.Reader {
	t.Helper()

	scheme, err := cliScheme()
	if err != nil {
		t.Fatal(err)
	}

	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(&ichorv1alpha1.IchorInstallation{
		Name:   ichorv1alpha1.IchorInstallationName,
		Spec:   ichorv1alpha1.IchorInstallationSpec{Version: installed},
		Status: ichorv1alpha1.IchorInstallationStatus{InstalledVersion: installed, LatestRelease: &ichorv1alpha1.IchorLatestRelease{Version: latest}},
	}).Build()
}

func TestVersionMentionsANewerRelease(t *testing.T) {
	info, _ := collectVersion(context.Background(), ichorWithRelease(t, testServerV04, "v0.5.0"), testCLIVersion)
	if info.Server == nil || info.Server.LatestRelease != "v0.5.0" {
		t.Fatalf("server = %+v, want the newer release", info.Server)
	}

	if out := renderVersion(t, info, outputYAML); !strings.Contains(out, "latestRelease: v0.5.0") {
		t.Fatalf("output = %q", out)
	}
}

func TestVersionDoesNotMentionAnOlderOrRunningRelease(t *testing.T) {
	for _, latest := range []string{testServerV04, "v0.3.0"} {
		info, _ := collectVersion(context.Background(), ichorWithRelease(t, testServerV04, latest), testCLIVersion)
		if info.Server == nil || info.Server.LatestRelease != "" {
			t.Fatalf("latest %s: server = %+v, want no release mentioned", latest, info.Server)
		}
	}
}
