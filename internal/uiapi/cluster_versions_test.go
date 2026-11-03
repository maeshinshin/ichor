package uiapi

import (
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/release"
)

func TestComponentVersionsCompareWithTheReleaseSet(t *testing.T) {
	c := &ichorv1alpha1.TalosCluster{Spec: ichorv1alpha1.TalosClusterSpec{
		TalosVersion: "v1.13.2", VersionContract: "v1.13", KubernetesVersion: release.Pinned.Kubernetes,
	}}

	got := map[string]string{}
	for _, v := range componentVersionsToPB(c) {
		got[v.GetName()] = v.GetState()
	}

	want := map[string]string{
		componentTalos:    string(release.Behind),
		componentContract: string(release.Behind),
		componentK8s:      string(release.UpToDate),
		componentCilium:   string(release.Unknown),
	}
	for name, state := range want {
		if got[name] != state {
			t.Errorf("%s state = %q, want %q", name, got[name], state)
		}
	}
}
