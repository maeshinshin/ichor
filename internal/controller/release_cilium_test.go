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
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/mod/semver"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/imagesig"
	"github.com/maeshinshin/ichor/internal/releasecheck"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
)

// fakeImages resolves every tag to a digest derived from its name and verifies a signature unless the
// image and tag are marked unsigned. It counts the verifications so a test can see the cache.
type fakeImages struct {
	unsigned map[string]bool // "repository tag"
	verifies int
}

func (f *fakeImages) Digest(_ context.Context, repository, tag string) (string, error) {
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(repository+":"+tag))), nil
}

func (f *fakeImages) Verify(_ context.Context, img imagesig.Image, id imagesig.Identity) error {
	f.verifies++

	for key := range f.unsigned {
		repo, tag, _ := strings.Cut(key, " ")
		if img.Repository == repo && id == imagesig.CiliumIdentity(tag) {
			return errors.New("no cosign signature")
		}
	}

	return nil
}

// chartPatches are the newer patches of the embedded Cilium minor (the two after the chart), and the tags
// that are never offered: the chart's own patch and a prerelease above it.
func chartPatches(t *testing.T) (newer []string, others []string) {
	t.Helper()

	base := "v" + cilium.ChartVersion
	minor := semver.MajorMinor(base)

	patch, err := strconv.Atoi(strings.TrimPrefix(base, minor+"."))
	if err != nil {
		t.Fatalf("the chart version %s has no patch number: %v", cilium.ChartVersion, err)
	}

	m := strings.TrimPrefix(minor, "v")
	for _, n := range []int{patch + 1, patch + 2} {
		newer = append(newer, fmt.Sprintf("%s.%d", m, n))
	}

	others = []string{
		fmt.Sprintf("%s.%d", m, patch),
		fmt.Sprintf("%s.%d-rc.1", m, patch+3),
	}

	return newer, others
}

func TestVerifiedPatchesOffersOnlySignedNewerPatchesOfTheChartMinor(t *testing.T) {
	newer, others := chartPatches(t)
	unsigned := newer[1] // the second newer patch has an unsigned operator image

	fake := &fakeImages{unsigned: map[string]bool{"quay.io/cilium/operator-generic v" + unsigned: true}}
	r := &ReleaseCheck{Images: fake}

	tags := append([]string{"1.21.0", "1.19.9"}, append(others, newer...)...)

	got := r.verifiedPatches(context.Background(), tags)

	want := []string{"v" + newer[0]}
	if !slices.Equal(got, want) {
		t.Fatalf("verifiedPatches = %v, want %v", got, want)
	}
}

func TestVerifiedPatchesRemembersVerifiedImages(t *testing.T) {
	newer, _ := chartPatches(t)
	fake := &fakeImages{}
	r := &ReleaseCheck{Images: fake}

	if got := r.verifiedPatches(context.Background(), newer); len(got) != len(newer) {
		t.Fatalf("verifiedPatches = %v, want all of %v", got, newer)
	}

	first := fake.verifies

	if got := r.verifiedPatches(context.Background(), newer); len(got) != len(newer) {
		t.Fatalf("second check: verifiedPatches = %v", got)
	}

	if fake.verifies != first {
		t.Fatalf("a second check verified %d images again; the verified images are not remembered", fake.verifies-first)
	}
}

func TestVerifiedPatchesCapsTheCandidates(t *testing.T) {
	base := "v" + cilium.ChartVersion
	minor := semver.MajorMinor(base)
	patch := 0
	_, _ = fmt.Sscanf(strings.TrimPrefix(base, minor+"."), "%d", &patch)

	var tags []string
	for n := patch + 1; n <= patch+maxCiliumCandidates+3; n++ {
		tags = append(tags, fmt.Sprintf("%s.%d", strings.TrimPrefix(minor, "v"), n))
	}

	r := &ReleaseCheck{Images: &fakeImages{}}

	if got := r.verifiedPatches(context.Background(), tags); len(got) != maxCiliumCandidates {
		t.Fatalf("verifiedPatches offered %d patches, want the %d newest", len(got), maxCiliumCandidates)
	}
}

func TestReleaseCheckEgressIncludesTheCiliumRegistry(t *testing.T) {
	r := &ReleaseCheck{Repository: releasecheck.DefaultRepository, Images: &fakeImages{}}

	hosts, err := r.registryHosts()
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Contains(hosts, "quay.io") {
		t.Fatalf("the egress hosts %v do not include quay.io, where the Cilium images are", hosts)
	}
}

func TestUpgradeTargetsOfKeepsTheLastGoodCiliumListOnFailure(t *testing.T) {
	prev := &ichorv1alpha1.IchorUpgradeTargets{Cilium: []string{"v1.20.5"}}

	got := upgradeTargetsOf(prev, metav1.Now(), listing{}, listing{}, listing{err: errors.New("quay unreachable")})
	if !slices.Equal(got.Cilium, []string{"v1.20.5"}) {
		t.Fatalf("a failed Cilium listing kept %v, want the last good list", got.Cilium)
	}

	if got.Error == "" {
		t.Fatal("the failed Cilium listing does not say why")
	}
}
