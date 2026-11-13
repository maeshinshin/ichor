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
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/credentials"
)

// readerSuffix is appended to the current talosconfig's path to name the file `ichor create talosconfig`
// writes by default; the current talosconfig is never overwritten.
const readerSuffix = ".ichor-reader"

var clusterTalosconfigOpts struct {
	Out       string
	Endpoints []string
	TTL       time.Duration
}

// readerTalosconfigRequest is one run of writeReaderTalosconfig.
type readerTalosconfigRequest struct {
	Cluster   string
	Namespace string

	// TalosconfigIn is the current talosconfig ("" resolves it like talosctl); it names the default output and
	// supplies endpoints when nothing else does.
	TalosconfigIn string
	Out           string
	Endpoints     []string

	// TTL is the lifetime asked for; zero leaves it to the controller's default.
	TTL time.Duration

	// Wait bounds the wait for the controller's answer, Poll is the pause between reads.
	Wait, Poll time.Duration
}

// createTalosconfigCmd implements `ichor create talosconfig`.
var createTalosconfigCmd = &cobra.Command{
	Use:   "talosconfig NAME",
	Short: "Write a short-lived os:reader talosconfig for talosctl",
	Long: `create talosconfig writes a talosconfig (mode 0600, content never printed) whose client certificate carries
the Talos role os:reader: talosctl can read a node (get members, logs, dmesg, processes) and cannot change
it (reboot, upgrade and reset are refused by the node).

It lives one hour by default; --ttl asks for fewer or up to ` + credentials.MaxTalosconfigLifetime.String() + `. The key is made on
this machine and never leaves it: the controller signs its public half. You need the Kubernetes RBAC
verb "download" on talosclusters/talosconfig-reader (ichor-operator and ichor-admin hold it; ichor-viewer
does not), and no access to the secrets bundle. Each request is recorded as an Event on the cluster that
names you.

The Talos endpoints are the cluster's control-plane machines (or --talos-endpoints, or your current
talosconfig's). The file is written next to your current talosconfig with the suffix ` + readerSuffix + ` (never over
it), unless --out says otherwise. Use it with: TALOSCONFIG=<file> talosctl get members`,
	Example: `  ichor create talosconfig prod
  ichor create talosconfig prod --ttl 15m --out reader.talosconfig`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		o := clusterTalosconfigOpts

		k8s, err := newKubeClient(kubeconfigPath)
		if err != nil {
			return err
		}

		out, notAfter, err := writeReaderTalosconfig(cmd.Context(), k8s, readerTalosconfigRequest{
			Cluster: args[0], Namespace: apiOpts.Namespace, TalosconfigIn: talosconfigPath,
			Out: o.Out, Endpoints: o.Endpoints, TTL: o.TTL, Wait: credentialAnswerWait, Poll: credentialAnswerPoll,
		})
		if err != nil {
			return err
		}

		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Wrote an os:reader talosconfig (mode 0600, content not printed), valid until %s:\n  %s\n"+
			"Use it with: TALOSCONFIG=%s talosctl get members\n", notAfter.Local().Format(time.RFC3339), out, out)

		return nil
	},
}

const (
	credentialAnswerWait = 20 * time.Second
	credentialAnswerPoll = 200 * time.Millisecond
)

// writeReaderTalosconfig asks the controller, as the caller, for an os:reader certificate for a key made here
// and writes the talosconfig. It returns the path and when the certificate expires. Nothing secret is returned
// or put in an error.
func writeReaderTalosconfig(ctx context.Context, k8s client.Client, req readerTalosconfigRequest) (string, time.Time, error) {
	if req.TTL < 0 || req.TTL > credentials.MaxTalosconfigLifetime {
		return "", time.Time{}, fmt.Errorf("--ttl must be between 0 and %s", credentials.MaxTalosconfigLifetime)
	}

	talosIn := currentTalosconfigFile(req.TalosconfigIn)
	out := cmp.Or(req.Out, talosIn+readerSuffix)

	if overwritesCurrentFile(out, talosIn) {
		return "", time.Time{}, errors.New("refusing to overwrite the current talosconfig; choose another output path")
	}

	var cluster ichorv1alpha1.TalosCluster
	if err := k8s.Get(ctx, types.NamespacedName{Namespace: req.Namespace, Name: req.Cluster}, &cluster); err != nil {
		return "", time.Time{}, fmt.Errorf("getting TalosCluster %s/%s: %w", req.Namespace, req.Cluster, err)
	}

	endpoints := req.Endpoints

	if len(endpoints) == 0 {
		var machines ichorv1alpha1.TalosMachineList
		if err := k8s.List(ctx, &machines, client.InNamespace(req.Namespace)); err != nil {
			return "", time.Time{}, fmt.Errorf("listing the cluster's machines: %w", err)
		}

		endpoints = credentials.Endpoints(machines.Items, req.Cluster)
	}

	if len(endpoints) == 0 {
		endpoints = endpointsOfTalosconfig(talosIn)
	}

	if len(endpoints) == 0 {
		return "", time.Time{}, errors.New("no Talos API endpoint known for the talosconfig: pass --talos-endpoints")
	}

	csr, key, err := credentials.NewCSR(false)
	if err != nil {
		return "", time.Time{}, err
	}

	answer, err := requestTalosconfigReader(ctx, k8s, &cluster, string(csr), req)
	if err != nil {
		return "", time.Time{}, err
	}

	data, err := credentials.BuildTalosconfig(req.Cluster, endpoints, []byte(answer.Trust), []byte(answer.Certificate), key)
	if err != nil {
		return "", time.Time{}, err
	}

	if err := os.MkdirAll(filepath.Dir(out), bundleDirPerm); err != nil {
		return "", time.Time{}, fmt.Errorf("creating %s: %w", filepath.Dir(out), err)
	}

	if err := writeFileAtomic(out, data); err != nil {
		return "", time.Time{}, fmt.Errorf("writing %s: %w", out, err)
	}

	var notAfter time.Time
	if answer.NotAfter != nil {
		notAfter = answer.NotAfter.Time
	}

	return out, notAfter, nil
}

// requestTalosconfigReader creates the TalosCredentialRequest as the caller (the webhook checks the caller's
// permission and records the Event) and waits for the controller's answer.
func requestTalosconfigReader(ctx context.Context, k8s client.Client, cluster *ichorv1alpha1.TalosCluster, csr string,
	req readerTalosconfigRequest) (*ichorv1alpha1.TalosCredentialRequestStatus, error) {
	cr := &ichorv1alpha1.TalosCredentialRequest{
		GenerateName: "cred-", Namespace: cluster.Namespace,
		Spec: ichorv1alpha1.TalosCredentialRequestSpec{
			ClusterRef: corev1.LocalObjectReference{Name: cluster.Name}, Purpose: ichorv1alpha1.CredentialPurposeTalosconfigReader, CSR: csr,
		},
	}
	if req.TTL > 0 {
		cr.Spec.TTL = &metav1.Duration{Duration: req.TTL}
	}

	if err := k8s.Create(ctx, cr); err != nil {
		return nil, fmt.Errorf("asking for an os:reader talosconfig of cluster %q: %w", cluster.Name, err)
	}

	var answer *ichorv1alpha1.TalosCredentialRequestStatus

	err := wait.PollUntilContextTimeout(ctx, req.Poll, req.Wait, true, func(ctx context.Context) (bool, error) {
		var got ichorv1alpha1.TalosCredentialRequest
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
			return false, err
		}

		if got.Status.Phase == "" {
			return false, nil
		}

		answer = &got.Status

		return true, nil
	})
	if err != nil {
		return nil, fmt.Errorf("waiting for the controller to answer (is it running?): %w", err)
	}

	if answer.Phase != ichorv1alpha1.CredentialPhaseIssued {
		return nil, fmt.Errorf("the controller refused the request: %s", strings.TrimSpace(answer.Message))
	}

	return answer, nil
}

func init() {
	addAPIFlags(createTalosconfigCmd)
	createCmd.AddCommand(createTalosconfigCmd)

	f := createTalosconfigCmd.Flags()
	f.StringVar(&clusterTalosconfigOpts.Out, "out", "",
		"where to write the talosconfig (default: your current talosconfig's path plus "+readerSuffix+")")
	f.StringSliceVar(&clusterTalosconfigOpts.Endpoints, "talos-endpoints", nil,
		"the Talos API endpoints for the talosconfig (default: the cluster's control-plane machines, else your current talosconfig's)")
	f.DurationVar(&clusterTalosconfigOpts.TTL, "ttl", 0,
		"how long the talosconfig is valid; the default is "+credentials.TalosconfigLifetime.String()+" and the maximum "+credentials.MaxTalosconfigLifetime.String())
}
