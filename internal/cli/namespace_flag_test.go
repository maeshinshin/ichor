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
	"maps"
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

// namespaceFlagError parses args on cmd, runs the namespace check the root's pre-run runs, and restores every flag.
func namespaceFlagError(t *testing.T, cmd *cobra.Command, args ...string) error {
	t.Helper()

	reset := func() { cmd.Flags().VisitAll(func(f *pflag.Flag) { _ = f.Value.Set(f.DefValue); f.Changed = false }) }
	reset()
	t.Cleanup(reset)

	if err := cmd.ParseFlags(args); err != nil {
		t.Fatalf("%s %v: %v", cmd.CommandPath(), args, err)
	}

	return checkNamespaceFlag(cmd)
}

// -n names the controller's namespace or it is refused. That namespace is the one --ichor-namespace names (a Helm
// install may use another than the default), so the flag is judged against it, not against a constant.
func TestNamespaceFlagIsJudgedAgainstTheEffectiveControllerNamespace(t *testing.T) {
	flags := namespaceFlags(rootCmd)

	for _, path := range slices.Sorted(maps.Keys(flags)) {
		// init and adopt refuse when they run (buildInitPlan, checkControllerNamespace); the installer's Job passes
		// the namespace the controller really runs in.
		if path == rootCmd.Name()+" "+cmdInit || path == rootCmd.Name()+" adopt" || strings.HasPrefix(path, rootCmd.Name()+" installer") {
			continue
		}

		cmd, _, err := rootCmd.Find(strings.Fields(path)[1:])
		if err != nil {
			t.Fatal(err)
		}

		if flags[path].Deprecated == "" {
			t.Errorf("%s: --namespace is not marked deprecated", path)
		}

		if err := namespaceFlagError(t, cmd, "--ichor-namespace", "other", "-n", "other"); err != nil {
			t.Errorf("%s: -n other with --ichor-namespace other: %v", path, err)
		}

		err = namespaceFlagError(t, cmd, "--ichor-namespace", "other", "-n", ichorControllerNamespace)
		if err == nil || !strings.Contains(err.Error(), `"other"`) {
			t.Errorf("%s: -n %s with --ichor-namespace other = %v, want a refusal naming other", path, ichorControllerNamespace, err)
		}

		if err := namespaceFlagError(t, cmd, "-n", ichorControllerNamespace); err != nil {
			t.Errorf("%s: -n %s alone: %v", path, ichorControllerNamespace, err)
		}
	}
}
