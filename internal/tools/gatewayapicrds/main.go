// Command gatewayapicrds extracts the Gateway API CRDs that Cilium's gatewayAPI needs from an upstream
// standard-channel install manifest, into the file the binary embeds (pkg/cni/gatewayapi).
// The upstream's copyright notice (the comment document it opens with) is written verbatim at the top of
// the output, as Apache-2.0 section 4(c) requires of a derivative work.
// Run via "make gateway-api-crds", which verifies the upstream file's SHA-256 first.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"sigs.k8s.io/yaml"
)

const crdKind = "CustomResourceDefinition"

func main() {
	in := flag.String("in", "", "the upstream standard-install.yaml")
	out := flag.String("out", "", "the file to write")
	names := flag.String("names", "", "space-separated CRD plurals to keep, e.g. \"gateways httproutes\"")
	version := flag.String("version", "", "the Gateway API release the file is taken from, written in its header")
	flag.Parse()

	if err := extract(*in, *out, *names, *version); err != nil {
		fmt.Fprintln(os.Stderr, "gatewayapicrds:", err)
		os.Exit(1)
	}
}

func extract(in, out, names, version string) error {
	data, err := os.ReadFile(in)
	if err != nil {
		return err
	}

	want := map[string]bool{}
	for n := range strings.FieldsSeq(names) {
		want[n+".gateway.networking.k8s.io"] = true
	}

	if len(want) == 0 {
		return fmt.Errorf("-names is empty")
	}

	var kept []string
	var notice string

	for doc := range strings.SplitSeq(string(data), "\n---\n") {
		var obj struct {
			Kind     string `json:"kind"`
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
		}

		if err := yaml.Unmarshal([]byte(doc), &obj); err != nil {
			return fmt.Errorf("reading a document of %s: %w", in, err)
		}

		if obj.Kind == "" && strings.Contains(doc, "Copyright ") {
			notice = strings.TrimSpace(doc)
		}

		if obj.Kind == crdKind && want[obj.Metadata.Name] {
			kept = append(kept, strings.TrimSpace(doc))
			delete(want, obj.Metadata.Name)
		}
	}

	if len(want) > 0 {
		return fmt.Errorf("%s has no CRD for %v", in, mapKeys(want))
	}

	if notice == "" {
		return fmt.Errorf("%s carries no copyright notice, and the CRDs must not lose it", in)
	}

	header := fmt.Sprintf("# Gateway API %s standard-channel CRDs that Cilium's gatewayAPI needs, extracted from\n"+
		"# the upstream standard-install.yaml by `make gateway-api-crds`. Do not edit by hand.\n", version)

	return os.WriteFile(out, []byte(notice+"\n\n"+header+strings.Join(kept, "\n---\n")+"\n"), 0o644)
}

func mapKeys(m map[string]bool) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}

	return ks
}
