// Package releasecheck finds the newest final Ichor release. A release is a tag of the
// chart repository, which the release workflow pushes last. The package holds the version
// rules and the lister; the controller decides when to ask and where to write the answer.
package releasecheck

import (
	"fmt"
	"net"
	"slices"
	"strings"

	"golang.org/x/mod/semver"
	"helm.sh/helm/v3/pkg/registry"
)

// DefaultRepository is the chart repository the controller lists (the chart's OCI address, no scheme).
const DefaultRepository = "ghcr.io/maeshinshin/charts/ichor"

// The repositories whose tags are the Talos and Kubernetes releases an upgrade can list.
// Their tags are the releases themselves: the installer image and the kubelet image.
const (
	DefaultTalosRepository      = "ghcr.io/siderolabs/installer"
	DefaultKubernetesRepository = "ghcr.io/siderolabs/kubelet"
)

// Lister returns the tags of a repository. Helm's registry client is the production one.
type Lister func(repository string) ([]string, error)

// HelmLister lists tags with Helm's registry client, anonymously (the chart repository is public).
// plainHTTP is for a local registry only.
func HelmLister(plainHTTP bool) (Lister, error) {
	var opts []registry.ClientOption
	if plainHTTP {
		opts = append(opts, registry.ClientOptPlainHTTP())
	}

	c, err := registry.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("creating the registry client: %w", err)
	}

	return c.Tags, nil
}

// Host is the registry host of a repository reference, without the port: the name a network
// policy matches.
func Host(repository string) (string, error) {
	host, _, ok := strings.Cut(strings.TrimPrefix(repository, "oci://"), "/")
	if !ok || host == "" {
		return "", fmt.Errorf("%q has no registry host", repository)
	}

	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}

	return host, nil
}

// canonical returns the tag as a semver with a leading v, or "" when it is not one.
func canonical(tag string) string {
	v := tag
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}

	if !semver.IsValid(v) || semver.Canonical(v) != v || semver.Build(v) != "" {
		return ""
	}

	return v
}

// Finals returns the final releases among the tags, newest first, each with a leading v and each once.
// Prereleases, build metadata, shorthands like "1.2" and every other tag are ignored.
func Finals(tags []string) []string {
	var out []string

	for _, t := range tags {
		v := canonical(t)
		if v == "" || semver.Prerelease(v) != "" {
			continue
		}

		out = append(out, v)
	}

	slices.SortFunc(out, func(a, b string) int { return semver.Compare(b, a) })

	return slices.Compact(out)
}

// Latest is the highest final release among the tags, with a leading v ("" when there is none).
func Latest(tags []string) string {
	if f := Finals(tags); len(f) > 0 {
		return f[0]
	}

	return ""
}

// Newer reports whether release is a newer version than running. A running version that is not
// semver (a dev build) is never older than anything, so it is never prompted.
func Newer(running, release string) bool {
	if !semver.IsValid(running) || release == "" {
		return false
	}

	return semver.Compare(release, running) > 0
}
