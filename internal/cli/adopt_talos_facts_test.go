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

const factsV1alpha1All = `version: v1alpha1
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

const factsV1alpha1Default = `version: v1alpha1
machine:
  type: controlplane
cluster:
  network:
    dnsDomain: cluster.local
`

const factsV1alpha1Custom = `version: v1alpha1
machine:
  type: worker
cluster:
  network:
    cni:
      name: custom
      urls:
        - https://example.invalid/cni.yaml
`

const factsV1alpha1Other = `version: v1alpha1
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
	testReadTalosFactsV1alpha1(t)
	testReadTalosFactsMultidoc(t)
}

func testReadTalosFactsV1alpha1(t *testing.T) {
	t.Helper()

	t.Run("v1alpha1 form with every fact", func(t *testing.T) {
		f, err := ReadTalosFacts([]byte(factsV1alpha1All))
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

		if len(f.InlineManifests) != 1 || f.InlineManifests[0].Name != string(detectedCilium) || !strings.Contains(f.InlineManifests[0].Contents, "DaemonSet") {
			t.Errorf("inline manifests = %+v", f.InlineManifests)
		}

		if f.Install.Disk != "/dev/vda" || f.Install.Image != "ghcr.io/siderolabs/installer:v1.12.4" {
			t.Errorf("install = %+v", f.Install)
		}
	})

	t.Run("v1alpha1 defaults are flannel with kube-proxy and no KubePrism", func(t *testing.T) {
		f, err := ReadTalosFacts([]byte(factsV1alpha1Default))
		if err != nil {
			t.Fatal(err)
		}

		if f.Role != roleControlPlane || f.CNI.Kind != CNIFlannel || !f.KubeProxyEnabled || f.KubePrism.Enabled {
			t.Errorf("facts = %+v", f)
		}
	})

	t.Run("custom CNI keeps its URLs", func(t *testing.T) {
		f, err := ReadTalosFacts([]byte(factsV1alpha1Custom))
		if err != nil {
			t.Fatal(err)
		}

		if f.CNI.Kind != CNICustom || len(f.CNI.URLs) != 1 {
			t.Errorf("cni = %+v", f.CNI)
		}
	})

	t.Run("another CNI name is other", func(t *testing.T) {
		f, err := ReadTalosFacts([]byte(factsV1alpha1Other))
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
	ok := []nodeVersion{{"a", "v1.14.0"}, {"b", ver1143}, {"c", "v1.14.0-beta.1"}}
	if err := checkTalosVersions(ok, adoptMinTalos); err != nil {
		t.Fatalf("v1.14 and later must pass: %v", err)
	}

	// The reason is in the refusal: sign-in cannot be turned off, and v1.14 carries its issuer.
	err := checkTalosVersions([]nodeVersion{{opCP, ver113 + ".5"}}, adoptMinTalos)
	if !errors.Is(err, ErrTalosTooOld) || !strings.Contains(err.Error(), "Talos 1.13.5 is older than the oldest version Ichor adopts (v1.14)") ||
		!strings.Contains(err.Error(), "sign-in is always on") || !strings.Contains(err.Error(), "issuer") ||
		!strings.Contains(err.Error(), "Upgrade Talos first") {
		t.Fatalf("single old node: %v", err)
	}

	err = checkTalosVersions([]nodeVersion{{opCP, "v1.14.1"}, {"w2", "v1.10.0"}, {"w1", "v1.13.9"}}, adoptMinTalos)
	if !errors.Is(err, ErrTalosTooOld) {
		t.Fatalf("mixed: %v", err)
	}

	msg := err.Error()
	if !strings.Contains(msg, "w1 runs Talos 1.13.9") || !strings.Contains(msg, "w2 runs Talos 1.10.0") || strings.Contains(msg, opCP) {
		t.Fatalf("mixed message must name exactly the old nodes: %v", msg)
	}

	if err := checkTalosVersions([]nodeVersion{{"x", "bogus"}}, adoptMinTalos); err == nil || errors.Is(err, ErrTalosTooOld) {
		t.Fatalf("an unreadable version is an error, not a pass: %v", err)
	}
}

// TestTheTwoNodeFloors: adopting a cluster takes Talos v1.14 and later, because sign-in is always on and only
// v1.14 carries its issuer; a node added to a cluster that was adopted before (--add-machine) takes v1.12 and
// later, since that cluster's sign-in and issuer exist already. The same v1.13 node is refused by one and
// accepted by the other.
func TestTheTwoNodeFloors(t *testing.T) {
	v113 := []nodeVersion{{"n", ver113 + ".2"}}
	v111 := []nodeVersion{{"n", "v1.11.5"}}

	if err := checkTalosVersions(v113, adoptMinTalos); !errors.Is(err, ErrTalosTooOld) || !strings.Contains(err.Error(), "sign-in is always on") {
		t.Errorf("adopt, a v1.13 node: %v, want ErrTalosTooOld with the sign-in reason", err)
	}

	if err := checkTalosVersions(v113, addMachineMinTalos); err != nil {
		t.Errorf("--add-machine, a v1.13 node: %v, want accepted", err)
	}

	// The lower floor is still a floor, and says nothing about sign-in, which has no part in it.
	err := checkTalosVersions(v111, addMachineMinTalos)
	if !errors.Is(err, ErrTalosTooOld) || !strings.Contains(err.Error(), "(v1.12)") || strings.Contains(err.Error(), "sign-in") {
		t.Errorf("--add-machine, a v1.11 node: %v, want ErrTalosTooOld naming v1.12 and no sign-in reason", err)
	}
}

func TestContractCandidates(t *testing.T) {
	cases := map[string][]string{
		ver1143:      {"v1.14", ver113, adoptProbeFloor},
		opOldVersion: nil,
		"v1.12.0":    nil,
		"junk":       nil,
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

	err := gateTalosVersions(context.Background(), []*ichorv1alpha1.TalosMachine{a, b}, dial, adoptMinTalos)
	if !errors.Is(err, ErrTalosTooOld) || !strings.Contains(err.Error(), "b runs Talos 1.11.2") {
		t.Fatalf("mixed cluster must be refused naming b: %v", err)
	}

	if err := gateTalosVersions(context.Background(), []*ichorv1alpha1.TalosMachine{a}, dial, adoptMinTalos); err != nil {
		t.Fatalf("v1.14 passes: %v", err)
	}

	// A reader that cannot report a version is not gated.
	plain := func(context.Context, string) (talosConfigReader, error) { return &fakeTalosReader{}, nil }
	if err := gateTalosVersions(context.Background(), []*ichorv1alpha1.TalosMachine{a, b}, plain, adoptMinTalos); err != nil {
		t.Fatalf("unversioned reader: %v", err)
	}
}

// runAdopt refuses an old node before it reads a configuration or writes anything.
func TestRunAdoptRefusesOldTalosBeforeReadingConfig(t *testing.T) {
	cluster := newTestCluster()
	machine := newTestMachine(cluster.Name)

	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cluster, machine).Build()

	reader := &versionedReader{version: "v1.13.2"} // v1.13 was accepted before the floor moved to v1.14
	reader.currentErr = errors.New("must not be read")

	var out strings.Builder

	err := runAdopt(context.Background(), cluster.Name, testNamespace, adoptDeps{
		K8s: k8s, Bundle: newTestBundle(t), DialTalos: dialerFor(reader, nil),
		Stdout: &out, Stderr: &out, Stdin: strings.NewReader(""), AssumeYes: true, Output: outputYAML,
	})
	if !errors.Is(err, ErrTalosTooOld) || !strings.Contains(err.Error(), "sign-in is always on") || !strings.Contains(err.Error(), "issuer") {
		t.Fatalf("runAdopt = %v, want ErrTalosTooOld saying why", err)
	}
}

// A cluster whose nodes were generated at an older contract and upgraded in place renders cleanly
// only at that contract: the probe must find it instead of assuming the running version.
func TestProbeVersionContractFindsThePre114Contract(t *testing.T) {
	cluster := newTestCluster()
	machine := newTestMachine(cluster.Name)
	bundle := newTestBundle(t)

	var sys *render.SystemInputs

	pre114 := cluster.DeepCopy()
	pre114.Spec.VersionContract = adoptProbeFloor

	live, _, err := render.Render(pre114, machine, bundle, nil, sys)
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

	// v1.13 and v1.12 both render the v1alpha1 single-document form, so they tie at zero and the
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
