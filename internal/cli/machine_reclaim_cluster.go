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
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/reclaim"
)

// ownReclaimDeps are the effects of `ichor machine reclaim ADDRESS --cluster NAME` without --talosconfig
// (ADR 0136); tests replace them.
type ownReclaimDeps struct {
	K8s client.Client
	// Who is the Kubernetes user name the API server sees for the CLI's writes; the webhook insists the
	// audit annotations carry it.
	Who func(ctx context.Context) (string, error)
	// Namespace is where Ichor runs: requests, and the TalosCluster the controller reads credentials of, live there.
	Namespace string
	Register  func(ctx context.Context, address string) error
	In        io.Reader
	Out       io.Writer
	Poll      time.Duration
}

// runMachineReclaimOwn wipes an orphan of a cluster Ichor manages with that cluster's own credentials. The
// controller does the work; the CLI writes the request, shows the inspection, writes the typed UUID and waits.
func runMachineReclaimOwn(ctx context.Context, d ownReclaimDeps, address string, f reclaimFlags) error {
	if f.Cluster == "" {
		return errors.New("machine reclaim: without --talosconfig the machine is wiped with the credentials Ichor holds for its own cluster; " +
			"name that cluster with --cluster NAME, or pass --talosconfig FILE for a machine of another cluster")
	}

	user, err := d.Who(ctx)
	if err != nil {
		return fmt.Errorf("machine reclaim: asking Kubernetes who you are: %w", err)
	}

	mr := &ichorv1alpha1.MachineReclaim{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "reclaim-", Namespace: d.Namespace,
			Annotations: map[string]string{ichorv1alpha1.ReclaimRequestedByAnnotation: user},
		},
		Spec: ichorv1alpha1.MachineReclaimSpec{
			Address: address, ClusterCredentials: true, ClusterRef: &corev1.LocalObjectReference{Name: f.Cluster},
			Force: f.Force, ForceUngraceful: f.ForceUngraceful,
		},
	}
	if err := d.K8s.Create(ctx, mr); err != nil {
		return fmt.Errorf("machine reclaim: creating the request (needs the %q verb on machinereclaims): %w", ichorv1alpha1.ReclaimVerb, err)
	}

	steps := newProgress(d.Out)

	err = steps.Run("Inspect", func(s *step) (string, error) {
		s.Update("the controller reads " + address)

		return "read " + address, d.await(ctx, mr, ichorv1alpha1.MachineReclaimAwaitingConfirmation)
	})
	if err != nil {
		return err
	}

	in, plan := inspectionFromStatus(address, mr.Status.Inspection)
	renderInspection(newHuman(d.Out), in, plan)

	if len(plan.Refusals) > 0 {
		_ = d.K8s.Delete(ctx, mr)

		return fmt.Errorf("machine reclaim: refusing to reclaim: %s", strings.Join(plan.Refusals, "; "))
	}

	r := bufio.NewReader(d.In)
	uuid := f.Confirm

	if uuid == "" {
		say(d.Out, "\nThis wipes STATE and EPHEMERAL on %s and deletes its Kubernetes Node.\nType the machine's UUID to continue: ", in.Hostname)

		if uuid, err = readTypedLine(r); err != nil {
			return fmt.Errorf("machine reclaim: reading the UUID (pass --confirm UUID when not interactive): %w", err)
		}
	}

	clusterName := f.ConfirmCluster
	if plan.NeedsForce && clusterName == "" {
		say(d.Out, "--force overrides a safety check. Type the cluster name %q to continue: ", in.ClusterName)

		if clusterName, err = readTypedLine(r); err != nil {
			return fmt.Errorf("machine reclaim: reading the cluster name (pass --confirm-cluster NAME when not interactive): %w", err)
		}
	}

	base := mr.DeepCopy()
	mr.Spec.ConfirmUUID, mr.Spec.ConfirmClusterName = uuid, clusterName
	metav1.SetMetaDataAnnotation(&mr.ObjectMeta, ichorv1alpha1.ReclaimConfirmedByAnnotation, user)

	if err := d.K8s.Patch(ctx, mr, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("machine reclaim: confirming (the typed UUID must be the machine's): %w", err)
	}

	if f.Wait <= 0 {
		steps.Info("not waiting (--wait 0); the request is " + mr.Name)
		steps.Done()

		return nil
	}

	wctx, cancel := context.WithTimeout(ctx, f.Wait)
	defer cancel()

	err = steps.Run("Reset", func(s *step) (string, error) {
		s.Update("wiping " + address + " and waiting for maintenance mode")

		if err := d.await(wctx, mr, ichorv1alpha1.MachineReclaimDone); err != nil {
			return "", err
		}

		return mr.Status.Result, nil
	})
	if err != nil {
		return err
	}

	if f.Cluster != "" && d.Register != nil {
		err = steps.Run("Register", func(*step) (string, error) {
			if err := d.Register(ctx, reclaim.HostOnly(address)); err != nil {
				return "", fmt.Errorf("machine reclaim: %w", err)
			}

			return "added to cluster " + f.Cluster + "'s discovery addresses; it will appear as a pending machine", nil
		})
		if err != nil {
			return err
		}
	}

	steps.Done()
	steps.Next("ichor machine list" + nsFlag(apiOpts.Namespace))

	return nil
}

// await polls the request until it reaches phase; Failed and Expired are errors that carry the controller's words.
func (d ownReclaimDeps) await(ctx context.Context, mr *ichorv1alpha1.MachineReclaim, phase ichorv1alpha1.MachineReclaimPhase) error {
	for {
		if err := d.K8s.Get(ctx, client.ObjectKeyFromObject(mr), mr); err != nil {
			return fmt.Errorf("machine reclaim: reading the request: %w", err)
		}

		switch mr.Status.Phase {
		case phase:
			return nil
		case ichorv1alpha1.MachineReclaimFailed, ichorv1alpha1.MachineReclaimExpired:
			return fmt.Errorf("machine reclaim: %s: %s", strings.ToLower(string(mr.Status.Phase)), mr.Status.Result)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("machine reclaim: gave up waiting for the controller (request %s is in phase %q): %w", mr.Name, mr.Status.Phase, ctx.Err())
		case <-time.After(d.Poll):
		}
	}
}

// inspectionFromStatus turns the controller's recorded inspection back into what renderInspection shows.
func inspectionFromStatus(address string, s *ichorv1alpha1.MachineReclaimInspection) (reclaim.Inspection, reclaim.Plan) {
	in := reclaim.Inspection{Address: address}
	if s == nil {
		return in, reclaim.Plan{}
	}

	in.UUID, in.Hostname, in.Version = s.UUID, s.Hostname, s.TalosVersion
	in.ControlPlane = s.Role == "controlplane"
	in.ClusterName, in.ClusterID, in.ClusterReachable = s.ClusterName, s.ClusterID, s.ClusterReachable

	for _, m := range s.EtcdMembers {
		in.EtcdMembers = append(in.EtcdMembers, reclaim.EtcdMember{ID: m.ID, Hostname: m.Hostname, Unhealthy: !m.Healthy})
	}

	in.EtcdKnown = len(in.EtcdMembers) > 0

	for _, v := range s.Volumes {
		in.Volumes = append(in.Volumes, reclaim.Volume{Name: v.Name, Location: v.Location, Size: v.SizeBytes, Encrypted: v.Encrypted})
	}

	return in, reclaim.Plan{Graceful: s.Graceful, NeedsForce: s.NeedsForce, Warnings: s.Warnings, Refusals: s.Refusals}
}
