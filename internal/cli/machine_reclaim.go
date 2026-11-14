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
	"cmp"
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
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/reclaim"
)

// reclaimFlags are `ichor reclaim node`'s own flags. --talosconfig and --yes
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

var reclaimNodeCmd = &cobra.Command{
	Use:   "node ADDRESS (--talosconfig FILE | --cluster NAME)",
	Short: "Reset a configured Talos node of another cluster back to maintenance mode",
	Long: `Reclaim a node that already runs a configured Talos, so that Ichor can join it.

The node is inspected with YOUR talosconfig (read from --talosconfig, used
only for this command, never stored and never printed). Ichor refuses a node
that is already in an Ichor cluster, and a control plane whose removal would
break its cluster's etcd quorum (override: --force plus the cluster name typed
a second time). The reset is graceful (cordon, drain, leave etcd) when the
foreign cluster is reachable; for a dead cluster pass --force-ungraceful.

The reset wipes only the STATE and EPHEMERAL partitions and reboots.
User disks are not touched; they are listed, and encrypted ones are called out
because their keys may be gone.

Without --talosconfig, give --cluster NAME: the node is then an orphan of that Ichor
cluster (its TalosMachine was deleted, which wipes on remove, so it still
carries the cluster's configuration) and the controller wipes it with the credentials Ichor
already holds for that cluster, deletes its Kubernetes Node and, afterwards, this command
registers it as a pending node. A node that still has an approved TalosMachine is
refused: use "ichor delete node". Needs the "reclaim" verb on machinereclaims.

Consent is the node's UUID, typed at the prompt or given as --confirm. --yes
does not satisfy it.

Afterwards the node comes back in maintenance mode with the same UUID. With
--cluster NAME its address is added to that cluster's spec.discovery.addresses
so that Ichor registers it as a pending node. A pending TalosMachine of the same
UUID that an earlier reclaim left is kept: on its next pass (within about five
minutes) discovery re-probes the node and records the new maintenance certificate
and disks on that same node (Event MaintenanceIdentityRefreshed); approve it after that.`,
	Example: `  ichor reclaim node 192.168.10.21 --talosconfig old-cluster/talosconfig
  ichor reclaim node 192.168.10.21 --cluster prod`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if talosconfigPath == "" {
			return runOwnReclaim(cmd, args[0])
		}

		raw, err := os.ReadFile(talosconfigPath)
		if err != nil {
			// The path, not the content.
			return fmt.Errorf("reclaim node: reading --talosconfig: %w", err)
		}

		k8s, err := newKubeClient(kubeconfigPath)
		if err != nil {
			return fmt.Errorf("reclaim node: building the Kubernetes client: %w", err)
		}

		f := machineReclaimFlags

		deps := reclaimDeps{
			Credential: reclaim.NewCredential(raw),
			Dial:       reclaim.Dial,
			Known: func(ctx context.Context) (reclaim.Known, error) {
				return reclaim.LoadKnown(ctx, k8s, apiOpts.IchorNamespace)
			},
			Probe: reclaim.MaintenanceUUID,
			Register: func(ctx context.Context, address string) error {
				return registerDiscoveryAddress(ctx, k8s, apiOpts.Namespace, f.Cluster, address)
			},
			In: cmd.InOrStdin(), Out: cmd.OutOrStdout(), Poll: 5 * time.Second,
		}

		return silenceIfShown(cmd, runMachineReclaim(cmd.Context(), deps, args[0], f))
	},
}

// runOwnReclaim is the command without --talosconfig: the machine is an orphan of the cluster named by
// --cluster and the controller wipes it with that cluster's own credentials.
func runOwnReclaim(cmd *cobra.Command, address string) error {
	cfg, err := loadKubeconfig(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("reclaim node: %w", err)
	}

	k8s, err := newKubeClientForConfig(cfg)
	if err != nil {
		return fmt.Errorf("reclaim node: building the Kubernetes client: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("reclaim node: %w", err)
	}

	f := machineReclaimFlags

	return silenceIfShown(cmd, runMachineReclaimOwn(cmd.Context(), ownReclaimDeps{
		K8s: k8s, Namespace: apiOpts.IchorNamespace,
		Who: func(ctx context.Context) (string, error) {
			return selfSubjectName(ctx, clientset.AuthenticationV1().SelfSubjectReviews())
		},
		Register: func(ctx context.Context, a string) error {
			return registerDiscoveryAddress(ctx, k8s, apiOpts.Namespace, f.Cluster, a)
		},
		In: cmd.InOrStdin(), Out: cmd.OutOrStdout(), Poll: 2 * time.Second,
	}, address, f))
}

// reclaimDeps are the effects runMachineReclaim has; tests replace them.
type reclaimDeps struct {
	Credential reclaim.Credential
	Dial       func(ctx context.Context, cred reclaim.Credential, address, contextName string) (reclaim.Node, error)
	Known      func(ctx context.Context) (reclaim.Known, error)
	Probe      func(ctx context.Context, address string) (string, error)
	Register   func(ctx context.Context, address string) error
	In         io.Reader
	Out        io.Writer
	Poll       time.Duration
}

func runMachineReclaim(ctx context.Context, d reclaimDeps, address string, f reclaimFlags) error {
	known, err := d.Known(ctx)
	if err != nil {
		return fmt.Errorf("reclaim node: cannot tell which nodes Ichor manages, so cannot rule out reclaiming one: %w", err)
	}

	node, err := d.Dial(ctx, d.Credential, address, f.Context)
	if err != nil {
		return fmt.Errorf("reclaim node: %w", err)
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
			return "", fmt.Errorf("reclaim node: inspecting %s: %w", address, ierr)
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
		return fmt.Errorf("reclaim node: %w", err)
	}

	r := bufio.NewReader(d.In)
	c := reclaim.Confirmation{UUID: f.Confirm, ClusterName: f.ConfirmCluster}

	if c.UUID == "" {
		say(d.Out, "\nThis wipes STATE and EPHEMERAL on %s and removes it from cluster %q.\nType the node's UUID to continue: ", in.Hostname, in.ClusterName)

		if c.UUID, err = readTypedLine(r); err != nil {
			return fmt.Errorf("reclaim node: reading the UUID (pass --confirm UUID when not interactive): %w", err)
		}
	}

	if plan.NeedsForce && c.ClusterName == "" {
		say(d.Out, "--force overrides a safety check. Type the cluster name %q to continue: ", in.ClusterName)

		if c.ClusterName, err = readTypedLine(r); err != nil {
			return fmt.Errorf("reclaim node: reading the cluster name (pass --confirm-cluster NAME when not interactive): %w", err)
		}
	}
	// Execute inspects once more, so a node swapped after the report is caught.
	err = steps.Run("Reset", func(*step) (string, error) {
		if _, err := reclaim.Execute(ctx, node, known, opt, c); err != nil {
			return "", fmt.Errorf("reclaim node: %w", err)
		}

		return "accepted, " + map[bool]string{true: "graceful", false: "not graceful"}[plan.Graceful], nil
	})
	if err != nil {
		return err
	}

	return finishReclaim(ctx, d, steps, address, in.UUID, f)
}

// finishReclaim waits for the machine to answer in maintenance mode with the
// same UUID, then registers it. A changed DHCP address is reported, not guessed.
func finishReclaim(ctx context.Context, d reclaimDeps, steps *progress, address, uuid string, f reclaimFlags) error {
	if f.Wait <= 0 {
		steps.Info("not verified: --wait 0 did not look at the node, so the reset is accepted but not confirmed")
		steps.Done()

		return nil
	}

	host := reclaim.HostOnly(address)

	err := steps.Run("Maintenance mode", func(s *step) (string, error) {
		s.Update("waiting for " + host + " to come back")

		wctx, cancel := context.WithTimeout(ctx, f.Wait)
		defer cancel()

		if err := reclaim.WaitMaintenance(wctx, host, uuid, d.Poll, d.Probe); err != nil {
			return "", fmt.Errorf("reclaim node: the reset was sent but the node did not come back at %s: %w\n"+
				"The node may have taken a new address from DHCP after the reboot. "+
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
			return "", fmt.Errorf("reclaim node: %w", err)
		}

		return "added to cluster " + f.Cluster + "'s discovery addresses; it will appear as a pending node", nil
	})
	if err != nil {
		return err
	}

	steps.Done()
	steps.Next("ichor get nodes" + nsFlag(apiOpts.Namespace))

	return nil
}

// say writes a formatted line to w; a failing terminal is not worth failing a reset report.
func say(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

func orNone(s string) string { return cmp.Or(s, "(none)") }

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
	addAPIFlags(reclaimNodeCmd)
	reclaimCmd.AddCommand(reclaimNodeCmd)

	fl := reclaimNodeCmd.Flags()
	fl.StringVar(&machineReclaimFlags.Context, "context", "", "talosconfig context to use (default: the file's own)")
	fl.StringVar(&machineReclaimFlags.Confirm, "confirm", "", "the node's UUID, for non-interactive use (--yes does not replace it)")
	fl.StringVar(&machineReclaimFlags.ConfirmCluster, "confirm-cluster", "", "the foreign cluster's name, the second confirmation --force needs")
	fl.BoolVar(&machineReclaimFlags.Force, "force", false, "allow a control plane whose removal breaks etcd quorum, or is the last one")
	fl.BoolVar(&machineReclaimFlags.ForceUngraceful, "force-ungraceful", false, "reset without cordon, drain and etcd leave, for a dead cluster")
	fl.StringVar(&machineReclaimFlags.Cluster, "cluster", "", "TalosCluster (in --namespace) whose discovery addresses get the node's address afterwards")
	fl.DurationVar(&machineReclaimFlags.Wait, "wait", 10*time.Minute, "how long to wait for maintenance mode (0 to not wait)")
}
