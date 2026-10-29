package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/render"
)

const (
	ver1143 = "v1.14.3"
	ver113  = "v1.13"
)

const factsLegacyAll = `version: v1alpha1
machine:
  type: worker
  install:
    disk: /dev/vda
    image: ghcr.io/siderolabs/installer:v1.12.4
  features:
    kubePrism:
      enabled: true
      port: 7445
cluster:
  network:
    cni:
      name: none
  proxy:
    disabled: true
  inlineManifests:
    - name: cilium
      contents: "kind: DaemonSet"
  extraManifests:
    - https://example.invalid/a.yaml
`

const factsLegacyDefault = `version: v1alpha1
machine:
  type: controlplane
cluster:
  network:
    dnsDomain: cluster.local
`

const factsLegacyCustom = `version: v1alpha1
machine:
  type: worker
cluster:
  network:
    cni:
      name: custom
      urls:
        - https://example.invalid/cni.yaml
`

const factsLegacyOther = `version: v1alpha1
machine:
  type: worker
cluster:
  network:
    cni:
      name: calico
`

const factsMultidocNone = `apiVersion: v1alpha1
kind: KubeNetworkConfig
---
apiVersion: v1alpha1
kind: KubeProxyConfig
enabled: false
---
apiVersion: v1alpha1
kind: KubePrismConfig
port: 7446
---
apiVersion: v1alpha1
kind: KubeInlineManifestConfig
name: my-app
manifest: "kind: ConfigMap"
`

const factsMultidocFlannel = `apiVersion: v1alpha1
kind: KubeNetworkConfig
---
apiVersion: v1alpha1
kind: KubeFlannelCNIConfig
`

func TestReadTalosFacts(t *testing.T) {
	testReadTalosFactsLegacy(t)
	testReadTalosFactsMultidoc(t)
}

func testReadTalosFactsLegacy(t *testing.T) {
	t.Helper()

	t.Run("legacy form with every fact", func(t *testing.T) {
		f, err := ReadTalosFacts([]byte(factsLegacyAll))
		if err != nil {
			t.Fatal(err)
		}

		if f.Role != "worker" {
			t.Errorf("role = %q", f.Role)
		}

		if f.CNI.Kind != CNINone || f.CNI.Name != string(detectedNone) {
			t.Errorf("cni = %+v, want none", f.CNI)
		}

		if f.KubeProxyEnabled {
			t.Error("kube-proxy should be disabled")
		}

		if !f.KubePrism.Enabled || f.KubePrism.Port != 7445 {
			t.Errorf("kubeprism = %+v", f.KubePrism)
		}

		if len(f.InlineManifests) != 1 || f.InlineManifests[0].Name != "cilium" || !strings.Contains(f.InlineManifests[0].Contents, "DaemonSet") {
			t.Errorf("inline manifests = %+v", f.InlineManifests)
		}

		if len(f.ExternalManifestURLs) != 1 || f.ExternalManifestURLs[0] != "https://example.invalid/a.yaml" {
			t.Errorf("external = %v", f.ExternalManifestURLs)
		}

		if f.Install.Disk != "/dev/vda" || f.Install.Image != "ghcr.io/siderolabs/installer:v1.12.4" {
			t.Errorf("install = %+v", f.Install)
		}
	})

	t.Run("legacy defaults are flannel with kube-proxy and no KubePrism", func(t *testing.T) {
		f, err := ReadTalosFacts([]byte(factsLegacyDefault))
		if err != nil {
			t.Fatal(err)
		}

		if f.Role != "controlplane" || f.CNI.Kind != CNIFlannel || !f.KubeProxyEnabled || f.KubePrism.Enabled {
			t.Errorf("facts = %+v", f)
		}
	})

	t.Run("custom CNI keeps its URLs", func(t *testing.T) {
		f, err := ReadTalosFacts([]byte(factsLegacyCustom))
		if err != nil {
			t.Fatal(err)
		}

		if f.CNI.Kind != CNICustom || len(f.CNI.URLs) != 1 {
			t.Errorf("cni = %+v", f.CNI)
		}
	})

	t.Run("another CNI name is other", func(t *testing.T) {
		f, err := ReadTalosFacts([]byte(factsLegacyOther))
		if err != nil {
			t.Fatal(err)
		}

		if f.CNI.Kind != CNIOther || f.CNI.Name != string(detectedCalico) {
			t.Errorf("cni = %+v", f.CNI)
		}
	})

}

func testReadTalosFactsMultidoc(t *testing.T) {
	t.Helper()

	t.Run("multi-document form without a flannel document is none", func(t *testing.T) {
		f, err := ReadTalosFacts([]byte(factsMultidocNone))
		if err != nil {
			t.Fatal(err)
		}

		if f.CNI.Kind != CNINone || f.KubeProxyEnabled || f.KubePrism.Port != 7446 || !f.KubePrism.Enabled {
			t.Errorf("facts = %+v", f)
		}

		if len(f.InlineManifests) != 1 || f.InlineManifests[0].Name != "my-app" {
			t.Errorf("inline manifests = %+v", f.InlineManifests)
		}
	})

	t.Run("multi-document form with a flannel document is flannel", func(t *testing.T) {
		f, err := ReadTalosFacts([]byte(factsMultidocFlannel))
		if err != nil {
			t.Fatal(err)
		}

		if f.CNI.Kind != CNIFlannel {
			t.Errorf("cni = %+v", f.CNI)
		}
	})
}

func TestCheckTalosVersions(t *testing.T) {
	ok := []nodeVersion{{"a", "v1.12.0"}, {"b", ver1143}, {"c", "v1.12.0-beta.1"}}
	if err := checkTalosVersions(ok); err != nil {
		t.Fatalf("v1.12 and later must pass: %v", err)
	}

	err := checkTalosVersions([]nodeVersion{{opCP, "v1.11.5"}})
	if !errors.Is(err, ErrTalosTooOld) || !strings.Contains(err.Error(), "Talos 1.11.5 is older than the oldest version Ichor adopts (v1.12)") ||
		!strings.Contains(err.Error(), "Upgrade Talos first") {
		t.Fatalf("single old node: %v", err)
	}

	err = checkTalosVersions([]nodeVersion{{opCP, "v1.12.1"}, {"w2", "v1.10.0"}, {"w1", "v1.11.9"}})
	if !errors.Is(err, ErrTalosTooOld) {
		t.Fatalf("mixed: %v", err)
	}

	msg := err.Error()
	if !strings.Contains(msg, "w1 runs Talos 1.11.9") || !strings.Contains(msg, "w2 runs Talos 1.10.0") || strings.Contains(msg, opCP) {
		t.Fatalf("mixed message must name exactly the old nodes: %v", msg)
	}

	if err := checkTalosVersions([]nodeVersion{{"x", "bogus"}}); err == nil || errors.Is(err, ErrTalosTooOld) {
		t.Fatalf("an unreadable version is an error, not a pass: %v", err)
	}
}

func TestContractCandidates(t *testing.T) {
	cases := map[string][]string{
		ver1143:   {"v1.14", ver113, adoptMinTalos},
		"v1.12.0": {adoptMinTalos},
		"v1.11.9": nil,
		"junk":    nil,
	}

	for in, want := range cases {
		got := contractCandidates(in)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("contractCandidates(%q) = %v, want %v", in, got, want)
		}
	}
}

type versionedReader struct {
	fakeTalosReader
	version string
	err     error
}

func (v *versionedReader) TalosVersion(context.Context) (string, error) { return v.version, v.err }

func TestGateTalosVersionsReadsEveryApprovedNode(t *testing.T) {
	mk := func(name string) *ichorv1alpha1.TalosMachine {
		m := newTestMachine("c")
		m.Name = name

		return m
	}

	versions := map[string]string{"a": "v1.14.0", "b": "v1.11.2"}
	dial := func(_ context.Context, endpoint string) (talosConfigReader, error) {
		return &versionedReader{version: versions[endpoint]}, nil
	}

	a, b := mk("a"), mk("b")
	a.Spec.Endpoint = "a"
	b.Spec.Endpoint = "b"

	err := gateTalosVersions(context.Background(), []*ichorv1alpha1.TalosMachine{a, b}, dial)
	if !errors.Is(err, ErrTalosTooOld) || !strings.Contains(err.Error(), "b runs Talos 1.11.2") {
		t.Fatalf("mixed cluster must be refused naming b: %v", err)
	}

	if err := gateTalosVersions(context.Background(), []*ichorv1alpha1.TalosMachine{a}, dial); err != nil {
		t.Fatalf("v1.14 passes: %v", err)
	}

	// A reader that cannot report a version is not gated.
	plain := func(context.Context, string) (talosConfigReader, error) { return &fakeTalosReader{}, nil }
	if err := gateTalosVersions(context.Background(), []*ichorv1alpha1.TalosMachine{a, b}, plain); err != nil {
		t.Fatalf("unversioned reader: %v", err)
	}
}

// runAdopt refuses an old node before it reads a configuration or writes anything.
func TestRunAdoptRefusesOldTalosBeforeReadingConfig(t *testing.T) {
	cluster := newTestCluster()
	machine := newTestMachine(cluster.Name)

	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cluster, machine).Build()

	reader := &versionedReader{version: "v1.11.0"}
	reader.currentErr = errors.New("must not be read")

	var out strings.Builder

	err := runAdopt(context.Background(), cluster.Name, testNamespace, adoptDeps{
		K8s: k8s, Bundle: newTestBundle(t), DialTalos: dialerFor(reader, nil),
		Stdout: &out, Stderr: &out, Stdin: strings.NewReader(""), AssumeYes: true, Output: outputYAML,
	})
	if !errors.Is(err, ErrTalosTooOld) {
		t.Fatalf("runAdopt = %v, want ErrTalosTooOld", err)
	}
}

// A cluster whose nodes were generated at an older contract and upgraded in place renders cleanly
// only at that contract: the probe must find it instead of assuming the running version.
func TestProbeVersionContractFindsTheLegacyContract(t *testing.T) {
	cluster := newTestCluster()
	machine := newTestMachine(cluster.Name)
	bundle := newTestBundle(t)

	var sys *render.SystemInputs

	legacy := cluster.DeepCopy()
	legacy.Spec.VersionContract = adoptMinTalos

	live, _, err := render.Render(legacy, machine, bundle, nil, sys)
	if err != nil {
		t.Fatalf("rendering the v1.12 fixture: %v", err)
	}

	best, probes, err := probeVersionContract(cluster, machine, bundle, nil, live, sys, ver1143)
	if err != nil {
		t.Fatal(err)
	}

	if len(probes) != 3 {
		t.Fatalf("probes = %d, want 3 (v1.14, v1.13, v1.12)", len(probes))
	}

	// v1.13 and v1.12 both render the legacy single-document form, so they tie at zero and the
	// higher one wins; v1.14 (multi-document) must have been rejected.
	if best != ver113 || len(probes[0].UnrepresentablePaths) == 0 || len(probes[2].UnrepresentablePaths) != 0 {
		t.Fatalf("best = %q, want v1.13 (tie with v1.12, v1.14 worse); probes = %+v", best, probes)
	}

	if cluster.Spec.VersionContract != testVersionContract {
		t.Fatal("probing must not mutate the cluster")
	}

	// A configuration rendered at the running contract stays at the running contract on a tie.
	current, _, err := render.Render(cluster, machine, bundle, nil, sys)
	if err != nil {
		t.Fatal(err)
	}

	best, _, err = probeVersionContract(cluster, machine, bundle, nil, current, sys, ver1143)
	if err != nil || best != testVersionContract {
		t.Fatalf("best = %q, err = %v, want %s", best, err, testVersionContract)
	}

	if _, _, err = probeVersionContract(cluster, machine, bundle, nil, current, sys, "v1.11.0"); err == nil {
		t.Fatal("running below the floor must fail")
	}
}
