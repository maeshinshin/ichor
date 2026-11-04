// Command ciliumgolden rewrites pkg/cni/cilium/testdata/bootstrap-*.golden.yaml from the embedded
// Cilium chart, for the empty user values TestBootstrapManifest_EmptyUserValuesAreByteIdentical
// pins. Run via "make cilium-goldens" after the pinned chart changes (hack/bump-cilium-patch.sh does).
package main

import (
	"fmt"
	"os"

	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ciliumgolden:", err)
		os.Exit(1)
	}
}

func run() error {
	for _, tunnel := range []struct{ arg, file string }{{"", "default"}, {"geneve", "geneve"}} {
		for _, mode := range []cni.KubeProxyMode{cni.KubeProxyEnabled, cni.KubeProxyReplaced} {
			p, err := cilium.New().WithTunnel(tunnel.arg)
			if err != nil {
				return err
			}

			manifest, err := p.BootstrapManifest(mode)
			if err != nil {
				return err
			}

			name := "pkg/cni/cilium/testdata/bootstrap-" + string(mode) + "-tunnel-" + tunnel.file + ".golden.yaml"
			if err := os.WriteFile(name, manifest, 0o644); err != nil { //nolint:gosec // a checked-in test fixture, world-readable like the others
				return err
			}
		}
	}

	return nil
}
