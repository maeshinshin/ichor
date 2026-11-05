// Package releasecheck finds the newest final Ichor release (ADR 0114). A release is a tag of the
// chart repository, which the release workflow pushes last (ADR 0108). The package holds the version
// rules and the lister; the controller decides when to ask and where to write the answer.
package releasecheck

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strings"

	"golang.org/x/mod/semver"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/retry"
)

// DefaultRepository is the chart repository the controller lists (the chart's OCI address, no scheme).
const DefaultRepository = "ghcr.io/maeshinshin/charts/ichor"

// The repositories whose tags are the Talos and Kubernetes releases an upgrade can list (ADR 0115).
// Talos 1.14 stopped publishing ghcr.io/siderolabs/installer (its installers come from Image Factory),
// so the Talos list is the installer-base image, which carries a tag per release (v1.14.3 is there,
// the installer image's is a 404) and is on the registry host the check already allows. Image Factory
// is not a ghcr.io host, and the controller does not pull installers, so it needs no egress for this.
const (
	DefaultTalosRepository      = "ghcr.io/siderolabs/installer-base"
	DefaultKubernetesRepository = "ghcr.io/siderolabs/kubelet"
)

// Lister returns the tags of a repository. RegistryLister is the production one.
type Lister func(repository string) ([]string, error)

// RegistryLister lists the tags of a repository over the OCI distribution API, anonymously (the chart and
// image repositories are public), page by page. It returns every tag as it is. Helm's own Tags would drop
// the "v1.14.3" style tags of the Talos, Kubernetes and Cilium images, because it keeps only semantic
// versions without a leading v, which left every image-based upgrade list empty (ADR 0115, amendment
// 2026-11-04). plainHTTP is for a local registry only.
func RegistryLister(plainHTTP bool) Lister {
	return func(repository string) ([]string, error) {
		repo, err := remote.NewRepository(strings.TrimPrefix(repository, "oci://"))
		if err != nil {
			return nil, err
		}

		repo.PlainHTTP = plainHTTP
		repo.Client = &auth.Client{Client: retry.DefaultClient, Cache: auth.NewCache()}

		var tags []string

		err = repo.Tags(context.Background(), "", func(page []string) error {
			tags = append(tags, page...)

			return nil
		})

		return tags, err
	}
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
