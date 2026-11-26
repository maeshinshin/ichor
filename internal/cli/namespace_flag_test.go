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

package cli

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// The controller's namespace is the only one init and adopt can use, so --namespace on them is a
// deprecated no-op that is hidden from --help; it is still accepted when it names that namespace.
func TestNamespaceFlagIsDeprecatedOnInitAndAdopt(t *testing.T) {
	for _, c := range []struct {
		name string
		got  func() (hidden bool, deprecated string)
		def  string
	}{
		{cmdInit, func() (bool, string) { f := initCmd.Flags().Lookup("namespace"); return f.Hidden, f.Deprecated }, ichorControllerNamespace},
		{"adopt", func() (bool, string) { f := adoptCmd.Flags().Lookup("namespace"); return f.Hidden, f.Deprecated }, ""},
	} {
		hidden, dep := c.got()
		if !hidden || dep == "" {
			t.Errorf("%s --namespace: hidden=%v deprecated=%q, want both set", c.name, hidden, dep)
		}

		if !strings.Contains(dep, ichorControllerNamespace) && !strings.Contains(dep, "controller") {
			t.Errorf("%s deprecation %q does not say the controller's namespace is used", c.name, dep)
		}
	}

	for _, cmd := range []string{cmdInit, "adopt"} {
		var usage string
		if cmd == cmdInit {
			usage = initCmd.LocalFlags().FlagUsages()
		} else {
			usage = adoptCmd.LocalFlags().FlagUsages()
		}

		if strings.Contains(usage, "--namespace") {
			t.Errorf("%s --help lists --namespace", cmd)
		}
	}
}

func TestInitWithoutNamespaceUsesTheControllersNamespace(t *testing.T) {
	if d := initCmd.Flags().Lookup("namespace").DefValue; d != ichorControllerNamespace {
		t.Fatalf("init --namespace default = %q, want %q", d, ichorControllerNamespace)
	}

	cluster, err := buildInitPlan(validInitPlanInput(), nil)
	if err != nil || cluster.Namespace != ichorControllerNamespace {
		t.Fatalf("buildInitPlan = %v, %v", cluster, err)
	}
}

// Without --namespace adopt uses the namespace the installed controller runs in, wherever Helm put it.
func TestAdoptNamespaceDefaultsToTheControllers(t *testing.T) {
	ctx := context.Background()
	empty := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()

	if got := adoptNamespaceOf(ctx, empty, ""); got != ichorControllerNamespace {
		t.Errorf("no Ichor: %q, want %q", got, ichorControllerNamespace)
	}

	tenant := &appsv1.Deployment{}
	tenant.Name, tenant.Namespace = controllerDeploymentName, helmControllerNamespace
	tenant.Labels = map[string]string{controllerLabel: controllerLabelValue}
	c := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(tenant).Build()

	if got := adoptNamespaceOf(ctx, c, ""); got != helmControllerNamespace {
		t.Errorf("controller in tenant: %q, want tenant", got)
	}

	if got := adoptNamespaceOf(ctx, c, "x"); got != "x" {
		t.Errorf("explicit --namespace: %q, want x (checkControllerNamespace judges it)", got)
	}
}

// namespaceFlags returns every `namespace` flag a command declares or inherits, by command path.
func namespaceFlags(root *cobra.Command) map[string]*pflag.Flag {
	out := map[string]*pflag.Flag{}

	var walk func(c *cobra.Command)

	walk = func(c *cobra.Command) {
		for _, fs := range []*pflag.FlagSet{c.LocalFlags(), c.InheritedFlags()} {
			if f := fs.Lookup("namespace"); f != nil {
				out[c.CommandPath()] = f
			}
		}

		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)

	return out
}

// Every Ichor object lives in the controller's namespace (ADR 0175), so no command shows --namespace.
func TestNoCommandShowsANamespaceFlag(t *testing.T) {
	flags := namespaceFlags(rootCmd)
	if len(flags) == 0 {
		t.Fatal("found no namespace flag at all; the walk is broken")
	}

	for path, f := range flags {
		if !f.Hidden {
			t.Errorf("%s: --namespace is visible in --help", path)
		}
	}
}

// -n is accepted only when it names the controller's namespace.
func TestNamespaceFlagRefusesAnotherNamespace(t *testing.T) {
	for path, f := range namespaceFlags(rootCmd) {
		// init and adopt refuse another namespace when they run (buildInitPlan, checkControllerNamespace); the
		// installer's Job passes the namespace the controller really runs in.
		if slices.Contains([]string{rootCmd.Name() + " " + cmdInit, rootCmd.Name() + " adopt"}, path) || strings.HasPrefix(path, "ichor installer") {
			continue
		}

		if f.Deprecated == "" {
			t.Errorf("%s: --namespace is not marked deprecated", path)
		}

		old := f.Value.String()

		err := f.Value.Set("elsewhere")
		if err == nil || !strings.Contains(err.Error(), ichorControllerNamespace) {
			t.Errorf("%s: -n elsewhere = %v, want a refusal naming %q", path, err, ichorControllerNamespace)
		}

		if err := f.Value.Set(ichorControllerNamespace); err != nil {
			t.Errorf("%s: -n %s refused: %v", path, ichorControllerNamespace, err)
		}

		_ = f.Value.Set(old)
	}
}
