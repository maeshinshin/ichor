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
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
	"github.com/maeshinshin/ichor/pkg/reclaim"
)

// reclaimFlags are `ichor machine reclaim`'s own flags. --talosconfig and --yes
// are root flags; --yes is deliberately not consulted here.
type reclaimFlags struct {
	Context         string
	Confirm         string
	ConfirmCluster  string
	Force           bool
	ForceUngraceful bool
	Cluster         string
	Wait            time.Duration
}

var machineReclaimFlags reclaimFlags

var machineReclaimCmd = &cobra.Command{
	Use:   "reclaim ADDRESS --talosconfig FILE",
	Short: "Reset a configured Talos machine of another cluster back to maintenance mode",
	Long: `Reclaim a machine that already runs a configured Talos, so that Ichor can join it.

The machine is inspected with YOUR talosconfig (read from --talosconfig, used
only for this command, never stored and never printed). Ichor refuses a machine
that is already in an Ichor cluster, and a control plane whose removal would
break its cluster's etcd quorum (override: --force plus the cluster name typed
a second time). The reset is graceful (cordon, drain, leave etcd) when the
foreign cluster is reachable; for a dead cluster pass --force-ungraceful.

The reset wipes only the STATE and EPHEMERAL partitions and reboots (ADR 0042).
User disks are not touched; they are listed, and encrypted ones are called out
because their keys may be gone.

Consent is the machine's UUID, typed at the prompt or given as --confirm. --yes
does not satisfy it.

Afterwards the machine comes back in maintenance mode with the same UUID. With
--cluster NAME its address is added to that cluster's spec.discovery.addresses
so that Ichor registers it as a pending machine. A pending TalosMachine of the same
UUID that an earlier reclaim left is deleted after the reset (it pinned the old
maintenance certificate); discovery creates it again with the new one.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if talosconfigPath == "" {
			return errors.New("machine reclaim: --talosconfig FILE is required (the operator's own credential for that machine)")
		}

		raw, err := os.ReadFile(talosconfigPath)
		if err != nil {
			// The path, not the content.
			return fmt.Errorf("machine reclaim: reading --talosconfig: %w", err)
		}

		k8s, err := newKubeClient(kubeconfigPath)
		if err != nil {
			return fmt.Errorf("machine reclaim: building the Kubernetes client: %w", err)
		}

		f := machineReclaimFlags

		deps := reclaimDeps{
			Credential: reclaim.NewCredential(raw),
			Dial:       reclaim.Dial,
			Known: func(ctx context.Context) (reclaim.Known, error) {
				return reclaim.LoadKnown(ctx, k8s, apiOpts.IchorNamespace)
			},
			Probe: reclaim.MaintenanceUUID,
			ClearStale: func(ctx context.Context, uuid string) ([]string, error) {
				return clearStalePending(ctx, k8s, apiOpts.Namespace, uuid)
			},
			Register: func(ctx context.Context, address string) error {
				return registerDiscoveryAddress(ctx, k8s, apiOpts.Namespace, f.Cluster, address)
			},
			In: cmd.InOrStdin(), Out: cmd.OutOrStdout(), Poll: 5 * time.Second,
		}

		return silenceIfShown(cmd, runMachineReclaim(cmd.Context(), deps, args[0], f))
	},
}

// reclaimDeps are the effects runMachineReclaim has; tests replace them.
type reclaimDeps struct {
	Credential reclaim.Credential
	Dial       func(ctx context.Context, cred reclaim.Credential, address, contextName string) (reclaim.Node, error)
	Known      func(ctx context.Context) (reclaim.Known, error)
	Probe      func(ctx context.Context, address string) (string, error)
	Register   func(ctx context.Context, address string) error
	// ClearStale deletes the pending TalosMachines of the reset node's UUID and returns their names.
	// Nil means none is cleared.
	ClearStale func(ctx context.Context, uuid string) ([]string, error)
	In         io.Reader
	Out        io.Writer
	Poll       time.Duration
}

func runMachineReclaim(ctx context.Context, d reclaimDeps, address string, f reclaimFlags) error {
	known, err := d.Known(ctx)
	if err != nil {
		return fmt.Errorf("machine reclaim: cannot tell which machines Ichor manages, so cannot rule out reclaiming one: %w", err)
	}

	node, err := d.Dial(ctx, d.Credential, address, f.Context)
	if err != nil {
		return fmt.Errorf("machine reclaim: %w", err)
	}
	defer node.Close() //nolint:errcheck

	steps := newProgress(d.Out)

	var in reclaim.Inspection

	// Every call to the node is bounded (reclaim.NodeCallTimeout), so a node whose etcd does not
	// run cannot hang this step; the line says so before the first call is made.
	err = steps.Run("Inspect", func(s *step) (string, error) {
		s.Update(fmt.Sprintf("reading %s (each call gives up after %s)", address, reclaim.NodeCallTimeout))

		var ierr error

		if in, ierr = node.Inspect(ctx); ierr != nil {
			return "", fmt.Errorf("machine reclaim: inspecting %s: %w", address, ierr)
		}

		return "read " + address, nil
	})
	if err != nil {
		return err
	}

	opt := reclaim.Options{Force: f.Force, ForceUngraceful: f.ForceUngraceful}
	plan := reclaim.Assess(in, known, opt)

	renderInspection(newHuman(d.Out), in, plan)

	if err := plan.Refused(); err != nil {
		return fmt.Errorf("machine reclaim: %w", err)
	}

	r := bufio.NewReader(d.In)
	c := reclaim.Confirmation{UUID: f.Confirm, ClusterName: f.ConfirmCluster}

	if c.UUID == "" {
		say(d.Out, "\nThis wipes STATE and EPHEMERAL on %s and removes it from cluster %q.\nType the machine's UUID to continue: ", in.Hostname, in.ClusterName)

		if c.UUID, err = readTypedLine(r); err != nil {
			return fmt.Errorf("machine reclaim: reading the UUID (pass --confirm UUID when not interactive): %w", err)
		}
	}

	if plan.NeedsForce && c.ClusterName == "" {
		say(d.Out, "--force overrides a safety check. Type the cluster name %q to continue: ", in.ClusterName)

		if c.ClusterName, err = readTypedLine(r); err != nil {
			return fmt.Errorf("machine reclaim: reading the cluster name (pass --confirm-cluster NAME when not interactive): %w", err)
		}
	}
	// Execute inspects once more, so a node swapped after the report is caught.
	err = steps.Run("Reset", func(*step) (string, error) {
		if _, err := reclaim.Execute(ctx, node, known, opt, c); err != nil {
			return "", fmt.Errorf("machine reclaim: %w", err)
		}

		return "accepted, " + map[bool]string{true: "graceful", false: "not graceful"}[plan.Graceful], nil
	})
	if err != nil {
		return err
	}

	clearStaleMachines(ctx, d, steps, in.UUID)

	return finishReclaim(ctx, d, steps, address, in.UUID, f)
}

// clearStaleMachines removes the pending TalosMachine of the node that was just reset. It pinned the
// maintenance certificate the node presented before the reset; the reset gives it a new one, so the
// machine would stick at MaintenanceIdentityChanged after approval. Deleting it is safe: a pending
// machine holds nothing the operator typed, and the cluster's discovery creates it again, pinned to
// the new certificate. The reset is done by now, so a failure here is a warning with the commands.
func clearStaleMachines(ctx context.Context, d reclaimDeps, steps *progress, uuid string) {
	if d.ClearStale == nil {
		return
	}

	names, err := d.ClearStale(ctx, uuid)

	switch {
	case err != nil:
		steps.Warn(fmt.Sprintf("could not remove the stale pending TalosMachine of %s: %v. Its pinned maintenance certificate is "+
			"out of date, so remove it before approving: "+
			"kubectl -n %[3]s annotate talosmachines.ichor.maesh.dev %[1]s ichor.maesh.dev/allow-delete=true && "+
			"kubectl -n %[3]s delete talosmachines.ichor.maesh.dev %[1]s", uuid, err, apiOpts.Namespace))
	case len(names) > 0:
		steps.Info(fmt.Sprintf("removed the stale pending TalosMachine %s: it pinned the node's old maintenance certificate, "+
			"and discovery creates it again with the new one", strings.Join(names, ", ")))
	}
}

// clearStalePending deletes the pending TalosMachines in namespace whose identity UUID is uuid
// (compared case-insensitively) and returns their names. An approved machine is never touched (the
// reclaim refused it before the reset), nor a rejected one, which is an operator's decision. The
// allow-delete annotation, which guards against a prune, is set on the machine first.
func clearStalePending(ctx context.Context, c client.Client, namespace, uuid string) ([]string, error) {
	var list ichorv1alpha1.TalosMachineList
	if err := c.List(ctx, &list, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("listing TalosMachines in %s: %w", namespace, err)
	}

	var names []string

	for i := range list.Items {
		m := &list.Items[i]

		if !strings.EqualFold(m.Spec.Identity.UUID, uuid) || m.Spec.IsApproved() ||
			m.Spec.ApprovalState == ichorv1alpha1.MachineApprovalStateRejected {
			continue
		}

		if m.Annotations[deleteguard.AllowDeleteAnnotation] != deleteguard.AllowDeleteValue {
			base := m.DeepCopy()
			if m.Annotations == nil {
				m.Annotations = map[string]string{}
			}

			m.Annotations[deleteguard.AllowDeleteAnnotation] = deleteguard.AllowDeleteValue

			if err := c.Patch(ctx, m, client.MergeFrom(base)); err != nil {
				return names, fmt.Errorf("allowing the deletion of TalosMachine %s: %w", m.Name, err)
			}
		}

		if err := c.Delete(ctx, m); client.IgnoreNotFound(err) != nil {
			return names, fmt.Errorf("deleting TalosMachine %s: %w", m.Name, err)
		}

		names = append(names, m.Name)
	}

	return names, nil
}

// finishReclaim waits for the machine to answer in maintenance mode with the
// same UUID, then registers it. A changed DHCP address is reported, not guessed.
func finishReclaim(ctx context.Context, d reclaimDeps, steps *progress, address, uuid string, f reclaimFlags) error {
	if f.Wait <= 0 {
		steps.Info("not waiting (--wait 0)")
		steps.Done()

		return nil
	}

	host := reclaim.HostOnly(address)

	err := steps.Run("Maintenance mode", func(s *step) (string, error) {
		s.Update("waiting for " + host + " to come back")

		wctx, cancel := context.WithTimeout(ctx, f.Wait)
		defer cancel()

		if err := reclaim.WaitMaintenance(wctx, host, uuid, d.Poll, d.Probe); err != nil {
			return "", fmt.Errorf("machine reclaim: the reset was sent but the machine did not come back at %s: %w\n"+
				"The machine may have taken a new address from DHCP after the reboot. "+
				"Find it (its UUID is %s) and add it to a cluster's spec.discovery.addresses or cidrs", host, err, uuid)
		}

		return host + " answers with UUID " + uuid, nil
	})
	if err != nil {
		return err
	}

	if f.Cluster == "" {
		steps.Info("To register it, add its address to a cluster's spec.discovery.addresses (or run again with --cluster NAME)")
		steps.Done()

		return nil
	}

	err = steps.Run("Register", func(*step) (string, error) {
		if err := d.Register(ctx, host); err != nil {
			return "", fmt.Errorf("machine reclaim: %w", err)
		}

		return "added to cluster " + f.Cluster + "'s discovery addresses; it will appear as a pending machine", nil
	})
	if err != nil {
		return err
	}

	steps.Done()
	steps.Next("ichor machine list" + nsFlag(apiOpts.Namespace))

	return nil
}

// say writes a formatted line to w; a failing terminal is not worth failing a reset report.
func say(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}

	return s
}

func readTypedLine(r *bufio.Reader) (string, error) {
	s, err := r.ReadString('\n')
	if err != nil && (!errors.Is(err, io.EOF) || s == "") {
		return "", err
	}

	return strings.TrimSpace(s), nil
}

// registerDiscoveryAddress appends address to the cluster's spec.discovery.addresses
// (the CRD allows only IPv4 there). The CLI changes the world by writing CRs.
func registerDiscoveryAddress(ctx context.Context, c client.Client, namespace, name, address string) error {
	a, err := netip.ParseAddr(address)
	if err != nil || !a.Is4() {
		return fmt.Errorf("%q is not an IPv4 address; add it to the cluster's discovery by hand", address)
	}

	var cluster ichorv1alpha1.TalosCluster
	if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &cluster); err != nil {
		return fmt.Errorf("getting TalosCluster %s/%s: %w", namespace, name, err)
	}

	if cluster.Spec.Discovery == nil {
		cluster.Spec.Discovery = &ichorv1alpha1.TalosClusterDiscovery{}
	}

	if slices.Contains(cluster.Spec.Discovery.Addresses, a.String()) {
		return nil
	}

	cluster.Spec.Discovery.Addresses = append(cluster.Spec.Discovery.Addresses, a.String())

	return c.Update(ctx, &cluster)
}

func init() {
	machineCmd.AddCommand(machineReclaimCmd)

	fl := machineReclaimCmd.Flags()
	fl.StringVar(&machineReclaimFlags.Context, "context", "", "talosconfig context to use (default: the file's own)")
	fl.StringVar(&machineReclaimFlags.Confirm, "confirm", "", "the machine's UUID, for non-interactive use (--yes does not replace it)")
	fl.StringVar(&machineReclaimFlags.ConfirmCluster, "confirm-cluster", "", "the foreign cluster's name, the second confirmation --force needs")
	fl.BoolVar(&machineReclaimFlags.Force, "force", false, "allow a control plane whose removal breaks etcd quorum, or is the last one")
	fl.BoolVar(&machineReclaimFlags.ForceUngraceful, "force-ungraceful", false, "reset without cordon, drain and etcd leave, for a dead cluster")
	fl.StringVar(&machineReclaimFlags.Cluster, "cluster", "", "TalosCluster (in --namespace) whose discovery addresses get the machine's address afterwards")
	fl.DurationVar(&machineReclaimFlags.Wait, "wait", 10*time.Minute, "how long to wait for maintenance mode (0 to not wait)")
}
