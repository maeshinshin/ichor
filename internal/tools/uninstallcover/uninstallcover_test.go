// Package uninstallcover checks that the uninstall steps remove every Ichor object that a webhook guards on DELETE.
// The namespace controller deletes the webhook Service before the custom resources, so an object left in the
// namespace whose DELETE the webhook covers can never be deleted and the namespace stays Terminating.
//
// The kinds come from the chart's webhook configuration; the scope of each from its CRD.
package uninstallcover

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

const (
	repoRoot   = "../../.."
	webhookCfg = repoRoot + "/dist/chart/templates/webhook/validating-webhook-configuration.yaml"
	upgradeDoc = repoRoot + "/docs/guides/install-with-helm/upgrade.md"
	crdDir     = repoRoot + "/config/crd/bases/"
	// allKinds is the command that lists every namespaced Ichor kind, so no kind has to be named.
	allKinds = "kubectl api-resources --api-group=ichor.maesh.dev -o name --namespaced"
)

var hook = regexp.MustCompile(`"ops" \(list ([^)]*)\) "res" \(list ([^)]*)\)`)

func read(t *testing.T, p string) string {
	t.Helper()

	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}

	return string(b)
}

func TestUninstallCoversEveryKindWithADeleteWebhook(t *testing.T) {
	t.Parallel()

	doc := read(t, upgradeDoc)
	seen := 0

	for _, m := range hook.FindAllStringSubmatch(read(t, webhookCfg), -1) {
		if !strings.Contains(m[1], `"DELETE"`) {
			continue
		}

		for r := range strings.FieldsSeq(strings.ReplaceAll(m[2], `"`, "")) {
			seen++

			crd := read(t, crdDir+"ichor.maesh.dev_"+r+".yaml")
			if !strings.Contains(crd, "scope: Namespaced") {
				// Cluster-scoped: the namespace delete never meets it, so it outlives the namespace and the
				// webhook with it. The steps must delete it by name while the webhook still runs.
				if one := strings.TrimSuffix(r, "s"); !strings.Contains(doc, "delete "+one+" ") {
					t.Errorf("%s is cluster-scoped with a DELETE webhook and outlives the namespace, but the uninstall steps in %s never run \"kubectl delete %s NAME\"",
						r, upgradeDoc, one)
				}

				continue
			}

			if !strings.Contains(doc, allKinds) && !strings.Contains(doc, r) {
				t.Errorf("%s has a DELETE webhook and lives in the namespace, but the uninstall steps in %s neither name it nor run %q",
					r, upgradeDoc, allKinds)
			}
		}
	}

	if seen == 0 {
		t.Fatal("no DELETE webhook found in " + webhookCfg + ": the pattern no longer reads it")
	}
}
