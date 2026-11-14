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
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// The command surface is verb first: `ichor get nodes`, `ichor reboot node NAME`. The verbs below
// are the parents; every command file adds its own object (nodes, cluster, patch, ...) to the verb it
// belongs to. auth, user, discovery and secrets have no natural verb and stay nouns, the way kubectl
// keeps auth, config and rollout.

// Help groups of the root command, in the order `ichor --help` lists them.
const (
	groupStart        = "start"
	groupInspect      = "inspect"
	groupChange       = "change"
	groupLifecycle    = "lifecycle"
	groupMaintain     = "maintain"
	groupTroubleshoot = "troubleshoot"
	groupAccess       = "access"
	groupOther        = "other"
)

var helpGroups = []*cobra.Group{
	{ID: groupStart, Title: "Get Started Commands:"},
	{ID: groupInspect, Title: "Inspect Commands:"},
	{ID: groupChange, Title: "Change Commands:"},
	{ID: groupLifecycle, Title: "Node and Cluster Lifecycle Commands:"},
	{ID: groupMaintain, Title: "Maintenance Commands:"},
	{ID: groupTroubleshoot, Title: "Troubleshooting Commands:"},
	{ID: groupAccess, Title: "Access Commands:"},
	{ID: groupOther, Title: "Other Commands:"},
}

// groupingKey marks a command whose only job is to group others: its usage line is "ichor get [command]",
// not "ichor get [flags]", since it takes no flags of its own to run with. The marker is an annotation the usage
// template reads (see groupingUsageTemplate), and the reference generator reads it for the synopsis.
const groupingKey = "ichor.maesh.dev/grouping"

// groupingAnnotations is the Annotations of a grouping command.
func groupingAnnotations() map[string]string { return map[string]string{groupingKey: groupingValue} }

const groupingValue = "true"

// IsGroupingCommand is true for a command that only groups others.
func IsGroupingCommand(c *cobra.Command) bool { return c.Annotations[groupingKey] == groupingValue }

// HelpGroups are the groups of the root command's help, in the order --help lists them.
func HelpGroups() []*cobra.Group { return helpGroups }

// groupingUsageTemplate is cobra's usage template without the "ichor get [flags]" line for a grouping command,
// which would read as if `ichor get -o json` returned something. The "ichor get [command]" line stays.
func groupingUsageTemplate(def string) string {
	return strings.Replace(def, "Usage:{{if .Runnable}}", "Usage:{{if and .Runnable (not (index .Annotations \""+groupingKey+"\"))}}", 1)
}

// groupRunE is the RunE of every command that only groups others: with no argument it prints its help, and
// with one it fails, so a typo (`ichor get noedes`) is an error with a non-zero exit, not a help page and a success.
func groupRunE(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}

	return groupArgs(cmd, args)
}

// groupArgs is the Args of a grouping command: any argument is unknown, since the command has no action of its own.
// Cobra runs Args before PersistentPreRunE, which for cni reaches the cluster, so a typo fails before that.
func groupArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}

	if cmd.SuggestionsMinimumDistance <= 0 { // cobra sets its default of 2 only when it finds the command itself
		cmd.SuggestionsMinimumDistance = 2
	}

	msg := fmt.Sprintf("unknown command %q for %q", args[0], cmd.CommandPath())
	if s := cmd.SuggestionsFor(args[0]); len(s) > 0 {
		msg += "\n\nDid you mean this?\n\t" + strings.Join(s, "\n\t")
	}

	return errors.New(msg)
}

var getCmd = &cobra.Command{
	Use:         "get",
	GroupID:     groupInspect,
	RunE:        groupRunE,
	Annotations: groupingAnnotations(),
	Short:       "List objects, or show a cluster in a few lines",
	Long: `get lists clusters, nodes, TalosConfigPatches, operations, users, Ichor's settings and
Cilium's Helm values. Add -o yaml or -o json for scripts. Use describe to see one object in full.`,
	Example: `  ichor get clusters
  ichor get nodes --cluster prod
  ichor get operations -o json`,
}

var describeCmd = &cobra.Command{
	Use:         "describe",
	GroupID:     groupInspect,
	RunE:        groupRunE,
	Annotations: groupingAnnotations(),
	Short:       "Show one object in full",
	Long: `describe shows one cluster, node, patch, operation, user or node configuration in full.
It reads the same resources the UI shows.`,
	Example: `  ichor describe cluster prod
  ichor describe node cp-1`,
}

var diffCmd = &cobra.Command{
	Use:         "diff",
	GroupID:     groupInspect,
	RunE:        groupRunE,
	Annotations: groupingAnnotations(),
	Short:       "Show how a change would alter what is in force",
	Long:        `diff shows how the effective Cilium values would change if a file became your layer, without writing anything.`,
	Example:     `  ichor diff cni-values -f cilium-values.yaml`,
}

var editCmd = &cobra.Command{
	Use:         "edit",
	GroupID:     groupChange,
	RunE:        groupRunE,
	Annotations: groupingAnnotations(),
	Short:       "Edit an object in $EDITOR, or change a user's fields",
	Example: `  ichor edit cni-values
  ichor edit user alice --role admin`,
}

var setCmd = &cobra.Command{
	Use:         "set",
	GroupID:     groupChange,
	RunE:        groupRunE,
	Annotations: groupingAnnotations(),
	Short:       "Change one setting: kube-proxy mode or Ichor's runtime settings",
	Example: `  ichor set kube-proxy replaced --cluster prod
  ichor set settings updateCheck.enabled=false`,
}

var createCmd = &cobra.Command{
	Use:         "create",
	GroupID:     groupChange,
	RunE:        groupRunE,
	Annotations: groupingAnnotations(),
	Short:       "Create a user, or write credentials for talosctl and kubectl",
	Long: `create makes an Ichor user, or writes the files a person needs to reach a cluster: the admin
credentials (a kubeconfig and a talosconfig), a short-lived read-only talosconfig, or your own kubeconfig.`,
	Example: `  ichor create user alice --role operator --generate
  ichor create talosconfig prod`,
}

var deleteCmd = &cobra.Command{
	Use:         "delete",
	GroupID:     groupChange,
	RunE:        groupRunE,
	Annotations: groupingAnnotations(),
	Short:       "Delete a node, patch, operation or user",
	Example: `  ichor delete patch ingress-sysctls
  ichor delete node worker-3 --confirm worker-3`,
}

var rolloutCmd = &cobra.Command{
	Use:         "rollout",
	GroupID:     groupChange,
	RunE:        groupRunE,
	Annotations: groupingAnnotations(),
	Short:       "Roll out pending changes that need a reboot or an API server restart",
	Example:     `  ichor rollout config --cluster prod`,
}

var approveCmd = &cobra.Command{
	Use:         "approve",
	GroupID:     groupLifecycle,
	RunE:        groupRunE,
	Annotations: groupingAnnotations(),
	Short:       "Approve a pending node so the controller starts configuring it",
	Example:     `  ichor approve node 3f1c2a --role worker`,
}

var rejectCmd = &cobra.Command{
	Use:         "reject",
	GroupID:     groupLifecycle,
	RunE:        groupRunE,
	Annotations: groupingAnnotations(),
	Short:       "Reject a pending node",
	Example:     `  ichor reject node 3f1c2a`,
}

var reclaimCmd = &cobra.Command{
	Use:         "reclaim",
	GroupID:     groupLifecycle,
	RunE:        groupRunE,
	Annotations: groupingAnnotations(),
	Short:       "Reset a configured Talos node of another cluster back to maintenance mode",
	Example:     `  ichor reclaim node 192.168.10.21 --talosconfig old-cluster/talosconfig --cluster prod`,
}

var rebootCmd = &cobra.Command{
	Use:         "reboot",
	GroupID:     groupLifecycle,
	RunE:        groupRunE,
	Annotations: groupingAnnotations(),
	Short:       "Reboot a node gracefully",
	Example:     `  ichor reboot node worker-3`,
}

var shutdownCmd = &cobra.Command{
	Use:         "shutdown",
	GroupID:     groupLifecycle,
	RunE:        groupRunE,
	Annotations: groupingAnnotations(),
	Short:       "Shut a node down, or every node of a cluster",
	Example: `  ichor shutdown node worker-3
  ichor shutdown cluster lab --follow`,
}

var restartCmd = &cobra.Command{
	Use:         "restart",
	GroupID:     groupLifecycle,
	RunE:        groupRunE,
	Annotations: groupingAnnotations(),
	Short:       "Restart a Talos service on a node",
	Example:     `  ichor restart service worker-3 kubelet`,
}

var bootstrapCmd = &cobra.Command{
	Use:         "bootstrap",
	GroupID:     groupMaintain,
	RunE:        groupRunE,
	Annotations: groupingAnnotations(),
	Short:       "Install Ichor's Cilium on a cluster with no CNI, before helm install ichor",
	Example:     `  ichor bootstrap cni --kube-proxy replaced`,
}

var rotateCmd = &cobra.Command{
	Use:         "rotate",
	GroupID:     groupMaintain,
	RunE:        groupRunE,
	Annotations: groupingAnnotations(),
	Short:       "Rotate a cluster's certificate authorities or its signing and encryption keys",
	Example: `  ichor rotate ca prod
  ichor rotate keys prod`,
}

var backupCmd = &cobra.Command{
	Use:         "backup",
	GroupID:     groupMaintain,
	RunE:        groupRunE,
	Annotations: groupingAnnotations(),
	Short:       "Take a backup now, outside the schedule",
	Example:     `  ichor backup etcd nightly`,
}

var snapshotCmd = &cobra.Command{
	Use:         "snapshot",
	GroupID:     groupMaintain,
	RunE:        groupRunE,
	Annotations: groupingAnnotations(),
	Short:       "Download a snapshot from a node",
	Example:     `  ichor snapshot etcd --node cp-1 --out etcd.snapshot`,
}

var defragCmd = &cobra.Command{
	Use:         "defrag",
	GroupID:     groupMaintain,
	RunE:        groupRunE,
	Annotations: groupingAnnotations(),
	Short:       "Defragment etcd on a control plane node",
	Example:     `  ichor defrag etcd --node cp-2`,
}

var forfeitLeadershipCmd = &cobra.Command{
	Use:         "forfeit-leadership",
	GroupID:     groupMaintain,
	RunE:        groupRunE,
	Annotations: groupingAnnotations(),
	Short:       "Hand etcd leadership over from a control plane node that leads",
	Example:     `  ichor forfeit-leadership etcd --node cp-1`,
}

var pauseCmd = &cobra.Command{
	Use:         "pause",
	GroupID:     groupMaintain,
	RunE:        groupRunE,
	Annotations: groupingAnnotations(),
	Short:       "Pause a running operation",
	Example:     `  ichor pause operation upgrade-talos-x7k2p`,
}

var resumeCmd = &cobra.Command{
	Use:         "resume",
	GroupID:     groupMaintain,
	RunE:        groupRunE,
	Annotations: groupingAnnotations(),
	Short:       "Resume a paused operation",
	Example:     `  ichor resume operation upgrade-talos-x7k2p`,
}

var exportCmd = &cobra.Command{
	Use:         "export",
	GroupID:     groupMaintain,
	RunE:        groupRunE,
	Annotations: groupingAnnotations(),
	Short:       "Save a cluster's secrets bundle locally",
	Example:     `  ichor export bundle prod`,
}

var logsCmd = &cobra.Command{
	Use:         "logs",
	GroupID:     groupTroubleshoot,
	RunE:        groupRunE,
	Annotations: groupingAnnotations(),
	Short:       "Print a node's kernel or service log",
	Example:     `  ichor logs node worker-3 --service kubelet --follow`,
}

func init() {
	rootCmd.SetUsageTemplate(groupingUsageTemplate(rootCmd.UsageTemplate()))
	rootCmd.AddGroup(helpGroups...)
	rootCmd.SetHelpCommandGroupID(groupOther)
	rootCmd.SetCompletionCommandGroupID(groupOther)

	rootCmd.AddCommand(getCmd, describeCmd, previewCmd, diffCmd, applyCmd, editCmd, setCmd, createCmd, deleteCmd, rolloutCmd,
		approveCmd, rejectCmd, reclaimCmd, rebootCmd, shutdownCmd, restartCmd,
		bootstrapCmd, rotateCmd, backupCmd, snapshotCmd, defragCmd, forfeitLeadershipCmd, pauseCmd, resumeCmd, exportCmd, logsCmd)
}
