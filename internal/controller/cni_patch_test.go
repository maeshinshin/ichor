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
	"fmt"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
)

// patchVersion is a Cilium patch above the embedded chart (the chart is 1.20.4 today; the test builds
// the next patch from it so a chart bump keeps the test true).
func patchVersion(t *testing.T) string {
	t.Helper()

	var minor, patch int

	if _, err := fmt.Sscanf(cilium.ChartVersion, "%d.%d.%d", new(int), &minor, &patch); err != nil {
		t.Fatalf("the chart version %q: %v", cilium.ChartVersion, err)
	}

	return fmt.Sprintf("v1.%d.%d", minor, patch+1)
}

func patchAdvancer(images *fakeImages) *cniKubeProxyAdvancer {
	return &cniKubeProxyAdvancer{Images: images}
}

func patchCluster() *ichorv1alpha1.TalosCluster {
	c := &ichorv1alpha1.TalosCluster{ObjectMeta: metav1.ObjectMeta{Name: "c", Namespace: "ns"}}
	c.Spec.KubernetesVersion = "v1.36.6"

	return c
}

func TestPatchOverrideVerifiesEveryImageOfThePatch(t *testing.T) {
	version := patchVersion(t)
	o, err := patchAdvancer(&fakeImages{}).patchOverride(context.Background(), patchCluster(), version)
	if err != nil {
		t.Fatalf("patchOverride: %v", err)
	}

	if o.Version != version || len(o.Images) != 3 {
		t.Fatalf("override = %+v", o)
	}

	for key, ref := range o.Images {
		if !strings.HasPrefix(ref, "quay.io/cilium/") || !strings.Contains(ref, ":"+version+"@sha256:") {
			t.Errorf("the %s image %q is not the patch by digest", key, ref)
		}
	}
}

func TestPatchOverrideRefusesAPatchWithAnUnsignedImage(t *testing.T) {
	version := patchVersion(t)
	images := &fakeImages{unsigned: map[string]bool{"quay.io/cilium/hubble-relay " + version: true}}

	_, err := patchAdvancer(images).patchOverride(context.Background(), patchCluster(), version)
	if err == nil || !strings.Contains(err.Error(), "not signed by its release workflow") {
		t.Fatalf("an unsigned hubble-relay image: err = %v", err)
	}
}

func TestPatchOverrideRefusesWhatCannotRun(t *testing.T) {
	version := patchVersion(t)

	if _, err := (&cniKubeProxyAdvancer{}).patchOverride(context.Background(), patchCluster(), version); err == nil {
		t.Error("a controller with no verifier ran a patch")
	}

	if _, err := patchAdvancer(&fakeImages{}).patchOverride(context.Background(), patchCluster(), "v"+cilium.ChartVersion); err == nil {
		t.Error("a patch equal to the embedded chart was accepted")
	}

	c := patchCluster()
	c.Spec.KubernetesVersion = "v1.31.9"

	if _, err := patchAdvancer(&fakeImages{}).patchOverride(context.Background(), c, version); err == nil {
		t.Error("a patch that does not run the cluster's Kubernetes was accepted")
	}
}

func TestCiliumJobCarriesTheActivePatch(t *testing.T) {
	version := patchVersion(t)
	c := patchCluster()
	c.Status.CNI = &ichorv1alpha1.TalosClusterCNIStatus{ImageOverride: &ichorv1alpha1.TalosCiliumImageOverride{
		Version: version,
		Images: map[string]string{
			cilium.ImageAgent:       "quay.io/cilium/cilium:" + version + "@sha256:" + strings.Repeat("1", 64),
			cilium.ImageOperator:    "quay.io/cilium/operator-generic:" + version + "@sha256:" + strings.Repeat("2", 64),
			cilium.ImageHubbleRelay: "quay.io/cilium/hubble-relay:" + version + "@sha256:" + strings.Repeat("3", 64),
		},
	}}

	job := newCNIHelmJob("ichor-system", "job", c, "enabled", &ichorv1alpha1.IchorInstallation{}, "hash", false)
	args := strings.Join(job.Spec.Template.Spec.Containers[0].Args, " ")

	if !strings.Contains(args, "--image-version="+version) {
		t.Fatalf("the Job's args %q do not name the patch", args)
	}

	for _, key := range []string{cilium.ImageAgent, cilium.ImageOperator, cilium.ImageHubbleRelay} {
		if !strings.Contains(args, "--image-override="+key+"=") {
			t.Errorf("the Job's args %q do not carry the %s image", args, key)
		}
	}

	plain := newCNIHelmJob("ichor-system", "job", patchCluster(), "enabled", &ichorv1alpha1.IchorInstallation{}, "hash", false)
	if strings.Contains(strings.Join(plain.Spec.Template.Spec.Containers[0].Args, " "), "--image-version") {
		t.Error("a Job with no patch names one")
	}
}
