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
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// testServerV04 is the server version most skew cases are measured from.
const testServerV04 = "v0.4.0"

func serverAt(t *testing.T, installed string) client.Reader {
	t.Helper()

	scheme, err := cliScheme()
	if err != nil {
		t.Fatal(err)
	}

	b := fake.NewClientBuilder().WithScheme(scheme)
	if installed != "" {
		b = b.WithObjects(&ichorv1alpha1.IchorInstallation{
			Name:   ichorv1alpha1.IchorInstallationName,
			Status: ichorv1alpha1.IchorInstallationStatus{InstalledVersion: installed},
		})
	}

	return b.Build()
}

func TestCheckServerSkew(t *testing.T) {
	for _, tc := range []struct {
		name, cli, server string
		wantErr           bool
	}{
		{name: "same version", cli: "v0.4.2", server: "v0.4.2"},
		{name: "patch apart", cli: "v0.4.9", server: testServerV04},
		{name: "one minor ahead", cli: "v0.5.0", server: testServerV04},
		{name: "one minor behind", cli: "v0.3.0", server: testServerV04},
		{name: "two minors ahead", cli: "v0.6.0", server: testServerV04, wantErr: true},
		{name: "two minors behind", cli: "v0.2.0", server: testServerV04, wantErr: true},
		{name: "a dev CLI cannot be judged", cli: "dev", server: testServerV04},
		{name: "a server that reports nothing cannot be judged", cli: "v0.6.0", server: ""},
		{name: "different majors cannot be compared", cli: "v2.0.0", server: testServerV04},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkServerSkew(context.Background(), serverAt(t, tc.server), tc.cli)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tc.wantErr)
			}

			if err != nil && !strings.Contains(err.Error(), "writes are refused") {
				t.Errorf("error %q does not say what is refused", err)
			}
		})
	}
}

// Every leaf command of the tree is classified here, so a new command fails the test until someone
// decides whether it writes. exempt commands write but are deliberately not gated (ADR 0059 section 6).
const (
	clsWrite = "write"
	clsRead  = "read"
	clsExmpt = "exempt"
	clsCond  = "write-if-acknowledge"
)

var commandClasses = map[string]string{
	cmdAdopt: clsExmpt, cmdInit: clsExmpt, "restore cluster": clsExmpt, // run before or without a server to compare with
	"bootstrap cni": clsExmpt, "cni helm apply": clsExmpt, "installer run": clsExmpt, // Jobs of the server's own version
	"create credentials": clsCond,
	"apply cni-values":   clsWrite, "approve node": clsWrite, "approve operation": clsWrite, "backup etcd": clsWrite,
	"create etcd-backup": clsWrite, "create talosconfig": clsWrite, "create user": clsWrite, "defrag etcd": clsWrite,
	"delete node": clsWrite, "delete operation": clsWrite, "delete patch": clsWrite, "delete user": clsWrite,
	"discovery add": clsWrite, "discovery remove": clsWrite, "edit cni-values": clsWrite, "edit user": clsWrite,
	"export bundle": clsWrite, "forfeit-leadership etcd": clsWrite, "migrate patches": clsWrite, "pause operation": clsWrite,
	"reboot node": clsWrite, "reclaim node": clsWrite, "reject node": clsWrite, "restart service": clsWrite,
	"restore etcd": clsWrite, "resume operation": clsWrite, "rollout config": clsWrite, "rotate ca": clsWrite,
	"rotate keys": clsWrite, "set containerd-device-ownership": clsWrite, "set install-disk": clsWrite,
	"set kube-proxy": clsWrite, "set settings": clsWrite, "shutdown cluster": clsWrite, "shutdown node": clsWrite,
	"upgrade cni": clsWrite, "upgrade ichor": clsWrite, "upgrade kubernetes": clsWrite, "upgrade talos": clsWrite,
	"user revoke-sessions": clsWrite, "user set-password": clsWrite,
	"auth login": clsRead, "auth logout": clsRead, "auth token": clsRead, "auth whoami": clsRead,
	"create kubeconfig": clsRead, "describe cluster": clsRead, "describe config": clsRead, "describe node": clsRead,
	"describe operation": clsRead, "describe patch": clsRead, "describe user": clsRead, "diff cni-values": clsRead,
	"discovery list": clsRead, "get cluster": clsRead, "get clusters": clsRead, "get cni-values": clsRead,
	"get nodes": clsRead, "get operations": clsRead, "get patches": clsRead, "get settings": clsRead,
	"get users": clsRead, "installer check": clsRead, "logs node": clsRead, "pcap": clsRead, "preview": clsRead,
	"secrets encrypt": clsRead, "snapshot etcd": clsRead, "support-bundle": clsRead, "ui": clsRead,
	"upgrade cli": clsRead, "version": clsRead,
}

func TestEveryWriteCommandIsGatedAndReadsAreNot(t *testing.T) {
	seen := map[string]bool{}

	var walk func(c *cobra.Command)

	walk = func(c *cobra.Command) {
		for _, s := range c.Commands() {
			if len(s.Commands()) > 0 {
				walk(s)

				continue
			}

			name := strings.TrimPrefix(s.CommandPath(), rootCmd.Name()+" ")
			if strings.HasPrefix(name, "help") || strings.HasPrefix(name, "completion ") { // cobra's own, added once a command has run
				continue
			}

			seen[name] = true

			cls, ok := commandClasses[name]
			if !ok {
				t.Errorf("%s: not classified in commandClasses; decide whether it writes and mark it with markWrites if so", name)

				continue
			}

			if marked := s.Annotations[writesAnnotation] == "true"; marked != (cls == clsWrite) {
				t.Errorf("%s: marked = %v, but it is classified %s", name, marked, cls)
			}
		}
	}
	walk(rootCmd)

	for name := range commandClasses {
		if !seen[name] {
			t.Errorf("%s is classified but no such command exists", name)
		}
	}
}

func TestCreateCredentialsIsGatedOnlyWithAcknowledge(t *testing.T) {
	t.Cleanup(func() { clusterCredentialsOpts.Acknowledge = false })

	for _, ack := range []bool{false, true} {
		clusterCredentialsOpts.Acknowledge = ack
		if got := commandWrites(createCredentialsCmd); got != ack {
			t.Errorf("--acknowledge=%v: gated = %v", ack, got)
		}
	}
}

// TestRootVersionMatchesVersionClient keeps `ichor --version` and `ichor version --client` on one source.
func TestRootVersionMatchesVersionClient(t *testing.T) {
	if got, want := rootCmd.Version, clientVersion(); got != want {
		t.Errorf("rootCmd.Version = %q, clientVersion() = %q", got, want)
	}
}
