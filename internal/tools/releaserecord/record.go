package main

import (
	"context"
	"fmt"
	"time"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage"
	"helm.sh/helm/v3/pkg/storage/driver"
	helmtime "helm.sh/helm/v3/pkg/time"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// limit is the size of a Secret that Helm's release record must stay under (1 MiB).
const limit = 1 << 20

// Procedure of ADR 0158 decision 10 and ADR 0165: Helm's own release encoder (JSON, gzip at best compression,
// base64) over the release a client-only dry-run install of the chart produces, against Kubernetes v1.36.0,
// status deployed with the description "Install complete", and the first and last deployed times pinned to
// 2026-01-01T00:00:00Z, so the figure is the same on every run.
//
// The chart refuses to render without a sign-in, so the values must set ichorUI.users.enabled=true and
// ichorUI.users.externalURL. With the release name, namespace and URL below, the figure is the one the ADRs
// record; other values give another figure. The record excludes the Secret's own metadata (name, labels, type).
const (
	releaseName = "ichor"
	namespace   = "ichor-system"
	kubeVersion = "v1.36.0"
	externalURL = "https://ichor.example.com"
)

// measure returns the size in bytes of the data.release value Helm would write for chartDir.
func measure(chartDir string) (int, error) {
	s, err := record(chartDir)
	return len(s), err
}

// record returns the data.release value itself: base64 of gzipped JSON.
func record(chartDir string) (string, error) {
	ch, err := loader.Load(chartDir)
	if err != nil {
		return "", err
	}
	kv, err := chartutil.ParseKubeVersion(kubeVersion)
	if err != nil {
		return "", err
	}
	cfg := &action.Configuration{Releases: storage.Init(driver.NewMemory())}
	in := action.NewInstall(cfg)
	in.ClientOnly, in.DryRun = true, true
	in.ReleaseName, in.Namespace, in.KubeVersion = releaseName, namespace, kv
	rel, err := in.Run(ch, map[string]any{"ichorUI": map[string]any{"users": map[string]any{"enabled": true, "externalURL": externalURL}}})
	if err != nil {
		return "", err
	}
	ts := helmtime.Time{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	rel.Info.FirstDeployed, rel.Info.LastDeployed = ts, ts
	rel.SetStatus(release.StatusDeployed, "Install complete")

	// The encoder is unexported; the Secrets driver calls it, and a fake client keeps what it wrote.
	secrets := fake.NewSimpleClientset().CoreV1().Secrets(namespace)
	key := fmt.Sprintf("sh.helm.release.v1.%s.v1", releaseName)
	if err := driver.NewSecrets(secrets).Create(key, rel); err != nil {
		return "", err
	}
	s, err := secrets.Get(context.Background(), key, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	return string(s.Data["release"]), nil
}

// verdict is the line to print and whether the size is within the limit.
func verdict(size int) (string, bool) {
	if size > limit {
		return fmt.Sprintf("Helm release record is %d bytes, over the limit of %d by %d bytes: helm install fails once it is stored", size, limit, size-limit), false
	}
	return fmt.Sprintf("Helm release record is %d bytes of the limit of %d, %d bytes of headroom", size, limit, limit-size), true
}
