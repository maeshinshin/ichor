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

package upgrade

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/mod/semver"
)

// deprecatedAPIsMetric is the kube-apiserver metric that counts requests to deprecated API
// versions, labelled with the release that removes each one
// (kubernetes.io/docs/reference/using-api/deprecation-guide: "kubectl get --raw /metrics").
const deprecatedAPIsMetric = "apiserver_requested_deprecated_apis"

var metricLabel = regexp.MustCompile(`(\w+)="([^"]*)"`)

// KubernetesMinorOf returns "X.Y" for a "vX.Y.Z" Kubernetes version.
func KubernetesMinorOf(version string) (string, error) {
	if !semver.IsValid(version) {
		return "", fmt.Errorf("invalid Kubernetes version %q", version)
	}

	return strings.TrimPrefix(semver.MajorMinor(version), "v"), nil
}

// RemovedAPIsInUse reads a kube-apiserver /metrics body and lists the API versions that have
// been requested since that apiserver started and that targetMinor ("X.Y") or an earlier release
// removes, as "group/version resource (removed in X.Y)", sorted and de-duplicated.
//
// It deliberately carries no table of removed APIs: the apiserver itself reports, per request, the
// release that removes the API version, so nothing here needs bumping for a new Kubernetes minor
// (the table go-kubernetes keeps for talosctl does, ADR 0022). The cost is that it only sees APIs
// that something has requested since the apiserver last restarted; a client that has not talked
// to a deprecated API recently is invisible, which is why the result is a warning, not a gate.
func RemovedAPIsInUse(metrics, targetMinor string) ([]string, error) {
	targetMajor, target, err := splitMinor(targetMinor)
	if err != nil {
		return nil, fmt.Errorf("invalid target minor %q: %w", targetMinor, err)
	}

	found := map[string]struct{}{}

	for line := range strings.SplitSeq(metrics, "\n") {
		if !strings.HasPrefix(line, deprecatedAPIsMetric+"{") {
			continue
		}

		labels := map[string]string{}
		for _, m := range metricLabel.FindAllStringSubmatch(line, -1) {
			labels[m[1]] = m[2]
		}

		removedMajor, removed, err := splitMinor(labels["removed_release"])
		if err != nil || removedMajor != targetMajor || removed > target {
			continue
		}

		name := labels["version"] + " " + labels["resource"]
		if labels["group"] != "" {
			name = labels["group"] + "/" + name
		}

		found[fmt.Sprintf("%s (removed in %s)", name, labels["removed_release"])] = struct{}{}
	}

	return slices.Sorted(maps.Keys(found)), nil
}

func splitMinor(v string) (major, minor int, err error) {
	maj, min, ok := strings.Cut(v, ".")
	if !ok {
		return 0, 0, fmt.Errorf("%q is not X.Y", v)
	}

	if major, err = strconv.Atoi(maj); err != nil {
		return 0, 0, err
	}

	if minor, err = strconv.Atoi(min); err != nil {
		return 0, 0, err
	}

	return major, minor, nil
}
