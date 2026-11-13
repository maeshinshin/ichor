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
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// paths splits a list with one command path per line.
func paths(lines string) []string { return strings.Split(strings.TrimSpace(lines), "\n") }

// walk calls fn for every command of the tree, the root first.
func walk(c *cobra.Command, fn func(*cobra.Command)) {
	fn(c)

	for _, s := range c.Commands() {
		walk(s, fn)
	}
}

// ownCommand is true for the commands a person runs: cobra's own help and completion trees are not, and
// neither is anything under a hidden command (what the installer and Cilium Jobs run).
func ownCommand(c *cobra.Command) bool {
	for p := c; p != nil; p = p.Parent() {
		if p.Name() == "help" || strings.HasPrefix(p.CommandPath(), "ichor completion") || p.Hidden {
			return false
		}
	}

	return true
}

func TestEveryCommandAPersonRunsHasAnExample(t *testing.T) {
	walk(rootCmd, func(c *cobra.Command) {
		if c == rootCmd || !c.IsAvailableCommand() || !ownCommand(c) {
			return
		}

		if !strings.Contains(c.Example, c.CommandPath()) {
			t.Errorf("%s: the Example %q does not show the command", c.CommandPath(), c.Example)
		}
	})
}

func TestEveryRootCommandIsInAHelpGroup(t *testing.T) {
	ids := map[string]bool{}
	for _, g := range helpGroups {
		ids[g.ID] = true
	}

	rootCmd.InitDefaultHelpCmd()
	rootCmd.InitDefaultCompletionCmd()

	for _, c := range rootCmd.Commands() {
		if !c.IsAvailableCommand() {
			continue
		}

		if !ids[c.GroupID] {
			t.Errorf("ichor %s is in help group %q, which does not exist", c.Name(), c.GroupID)
		}
	}

	if !rootCmd.AllChildCommandsHaveGroup() {
		t.Error("a command has no help group, so --help lists it under Additional Commands")
	}
}

func TestEveryHelpGroupHoldsACommand(t *testing.T) {
	held := map[string]int{}
	for _, c := range rootCmd.Commands() {
		held[c.GroupID]++
	}

	for _, g := range helpGroups {
		if held[g.ID] == 0 {
			t.Errorf("help group %q is empty", g.Title)
		}
	}
}

// The command surface is verb first. Each row is a path that must resolve to a command named by its last word.
func TestVerbFirstCommandPathsResolve(t *testing.T) {
	for _, path := range paths(`
get clusters
get cluster
get nodes
get patches
get operations
get users
get settings
get cni-values
describe cluster
describe node
describe patch
describe operation
describe user
describe config
approve node
reject node
reclaim node
delete node
delete patch
delete operation
delete user
logs node
reboot node
shutdown node
shutdown cluster
restart service
pcap
support-bundle
apply
apply cni-values
preview
diff cni-values
edit cni-values
edit user
create user
rollout config
upgrade talos
upgrade kubernetes
upgrade ichor
upgrade cni
bootstrap cni
set kube-proxy
set settings
snapshot etcd
backup etcd
defrag etcd
forfeit-leadership etcd
restore etcd
restore cluster
rotate ca
rotate keys
create credentials
create talosconfig
create kubeconfig
export bundle
pause operation
resume operation
discovery add
discovery list
discovery remove
auth login
auth logout
auth whoami
auth token
user set-password
user revoke-sessions
secrets encrypt
adopt
init
version
ui
`) {
		words := strings.Fields(path)

		c, rest, err := rootCmd.Find(words)
		if err != nil || c == rootCmd || len(rest) != 0 || c.Name() != words[len(words)-1] {
			t.Errorf("ichor %s does not resolve: %v (found %q, left over %v)", path, err, c.CommandPath(), rest)
		}
	}
}

// The noun-first groups of the earlier surface are gone, with no alias left behind.
func TestTheNounFirstGroupsAreGone(t *testing.T) {
	for _, path := range paths(`
cluster
machine
node
patch
config
operation
op
settings
etcd
cluster list
machine list
node remove
patch create
config apply
cni upgrade
cni values
etcd snapshot
auth status
auth kubeconfig
cluster discovery
user list
user create
`) {
		words := strings.Fields(path)

		c, rest, err := rootCmd.Find(words)
		if err == nil && len(rest) == 0 && c != rootCmd && c.Name() == words[len(words)-1] && c.IsAvailableCommand() {
			t.Errorf("ichor %s still resolves", path)
		}
	}

	walk(rootCmd, func(c *cobra.Command) {
		if len(c.Aliases) != 0 {
			t.Errorf("ichor %s has aliases %v", c.CommandPath(), c.Aliases)
		}
	})
}

// These names are written into things that outlive a build: kubeconfigs (auth token), and the installer Job
// and the Cilium Job the controller of the running version starts from the image of the target version
// (their arguments are also pinned by the controller-jobs admission policy). Renaming one breaks them.
func TestNamesStoredOutsideTheBinaryDoNotChange(t *testing.T) {
	for _, tc := range []struct {
		path   string
		hidden bool
	}{
		{"auth token", false},
		{"installer run", true},
		{"cni helm apply", true},
	} {
		words := strings.Fields(tc.path)

		c, rest, err := rootCmd.Find(words)
		if err != nil || len(rest) != 0 || c.Name() != words[len(words)-1] {
			t.Fatalf("ichor %s is gone: %v", tc.path, err)
		}

		hidden := false
		for p := c; p != nil; p = p.Parent() {
			hidden = hidden || p.Hidden
		}

		if hidden != tc.hidden {
			t.Errorf("ichor %s: hidden = %v, want %v", tc.path, hidden, tc.hidden)
		}
	}
}

// describe node: the record always, the live detail when ichor-ui can give it.

type liveNode struct {
	localNode
	detail *pb.NodeDetail
	err    error
}

func (n liveNode) GetNode(context.Context, *connect.Request[pb.GetNodeRequest]) (*connect.Response[pb.GetNodeResponse], error) {
	if n.err != nil {
		return nil, n.err
	}

	return connect.NewResponse(&pb.GetNodeResponse{Node: n.detail}), nil
}

func describeNodeOutput(t *testing.T, c *apiClients, format string) (string, string) {
	t.Helper()

	setHumanTestFlags(t, format)

	var out, errw bytes.Buffer
	if err := runDescribeNode(context.Background(), c, &out, &errw, testMachineName); err != nil {
		t.Fatal(err)
	}

	return out.String(), errw.String()
}

func TestDescribeNodeShowsTheRecordAndTheLiveDetailWhenIchorUIIsReachable(t *testing.T) {
	c, _ := nodeActionClients(t, true)
	c.Local = false
	c.Node = liveNode{localNode: localNode{}, detail: &pb.NodeDetail{
		Machine: objectRef(testNamespace, testMachineName), Hardware: &pb.Hardware{Manufacturer: "Dell", Model: "Standard PC", CpuCores: 4},
		Kubernetes: &pb.KubernetesNodeStatus{NodeName: "n1", Ready: true, KubeletVersion: humanK8s},
	}}

	out, errw := describeNodeOutput(t, c, outputHuman)

	for _, want := range []string{"Identity\n", "Hardware\n  Manufacturer Dell", "Kubernetes\n  Node         n1"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}

	if strings.Contains(out, "not shown") || errw != "" {
		t.Errorf("nothing is missing, so nothing is said:\n%s\nstderr: %s", out, errw)
	}

	out, _ = describeNodeOutput(t, c, outputJSON)
	if !strings.Contains(out, `"machine"`) || !strings.Contains(out, `"node"`) || !strings.Contains(out, `"manufacturer": "Dell"`) {
		t.Errorf("-o json holds the record under machine and the live part under node:\n%s", out)
	}
}

func TestDescribeNodeShowsTheRecordAloneWithoutIchorUI(t *testing.T) {
	c, _ := nodeActionClients(t, true) // the in-process fallback: Local is true

	out, _ := describeNodeOutput(t, c, outputHuman)

	if !strings.Contains(out, "Identity\n") || strings.Contains(out, "Hardware") {
		t.Errorf("the record is shown, and no hardware:\n%s", out)
	}

	if !strings.Contains(out, "Live detail (hardware, usage, Kubernetes node) is not shown: it needs ichor-ui") {
		t.Errorf("the missing live part is explained:\n%s", out)
	}

	out, errw := describeNodeOutput(t, c, outputYAML)
	if !strings.Contains(out, "machine:") || strings.Contains(out, "node:\n") || !strings.Contains(errw, "no live detail") {
		t.Errorf("-o yaml has the record only, and stderr says why:\n%s\nstderr: %s", out, errw)
	}
}

func TestDescribeNodeKeepsTheRecordWhenTheLiveReadFails(t *testing.T) {
	c, _ := nodeActionClients(t, true)
	c.Local = false
	c.Node = liveNode{err: connect.NewError(connect.CodeFailedPrecondition, errors.New("node n1 is not approved"))}

	out, _ := describeNodeOutput(t, c, outputHuman)

	if !strings.Contains(out, "Identity\n") || !strings.Contains(out, "is not shown: failed_precondition: node n1 is not approved") {
		t.Errorf("the record is shown and the reason is named:\n%s", out)
	}
}

// A node that does not exist is an error, not an empty record.
func TestDescribeNodeFailsForAMissingNode(t *testing.T) {
	c, _ := nodeActionClients(t, true)
	setHumanTestFlags(t, outputHuman)

	err := runDescribeNode(context.Background(), c, &bytes.Buffer{}, &bytes.Buffer{}, "no-such-node")
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("err = %v", err)
	}
}

// run executes the root command with args and returns the error and what it wrote.
func run(t *testing.T, args ...string) (string, error) {
	t.Helper()

	var out, errw bytes.Buffer

	rootCmd.SetArgs(args)
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errw)

	t.Cleanup(func() { rootCmd.SetArgs(nil); rootCmd.SetOut(nil); rootCmd.SetErr(nil) })

	_, err := rootCmd.ExecuteC()

	return out.String() + errw.String(), err
}

// A word that names no command under a verb or a noun is an error with a non-zero exit, not a help page.
func TestAnUnknownWordUnderAVerbIsAnError(t *testing.T) {
	var parents []*cobra.Command

	walk(rootCmd, func(c *cobra.Command) {
		if c != rootCmd && ownCommand(c) && c.IsAvailableCommand() && c.HasAvailableSubCommands() {
			parents = append(parents, c)
		}
	})

	if len(parents) < 20 {
		t.Fatalf("found only %d commands that group others", len(parents))
	}

	for _, p := range parents {
		args := append(strings.Fields(strings.TrimPrefix(p.CommandPath(), "ichor ")), "no-such-thing")

		if _, err := run(t, args...); err == nil || !strings.Contains(err.Error(), "no-such-thing") {
			t.Errorf("ichor %s: err = %v", strings.Join(args, " "), err)
		}
	}
}

func TestAnUnknownWordSuggestsTheClosestCommand(t *testing.T) {
	_, err := run(t, "get", "nodse")
	if err == nil || !strings.Contains(err.Error(), "Did you mean this?\n\tnodes") {
		t.Fatalf("err = %v", err)
	}

	// With no word, a verb prints its help and succeeds.
	if out, err := run(t, "get"); err != nil || !strings.Contains(out, "Available Commands:") {
		t.Fatalf("ichor get: %v\n%s", err, out)
	}
}

// A command that only groups others offers `[command]`, not `[flags]`: `ichor get -o json` returns nothing.
func TestAGroupingCommandsUsageOffersOnlyCommand(t *testing.T) {
	usage := func(c *cobra.Command) string { return c.UsageString() }

	if u := usage(getCmd); !strings.Contains(u, "ichor get [command]") || strings.Contains(u, "[flags]") {
		t.Errorf("ichor get usage:\n%s", u)
	}

	// apply is a command and a group at once: both lines.
	if u := usage(applyCmd); !strings.Contains(u, "ichor apply -f FILE [flags]") || !strings.Contains(u, "ichor apply [command]") {
		t.Errorf("ichor apply usage:\n%s", u)
	}

	walk(rootCmd, func(c *cobra.Command) {
		if c == rootCmd || !ownCommand(c) || !c.IsAvailableCommand() || !c.HasAvailableSubCommands() || c == applyCmd {
			return
		}

		if !IsGroupingCommand(c) {
			t.Errorf("ichor %s only groups others but is not marked as grouping", c.CommandPath())
		}
	})
}
