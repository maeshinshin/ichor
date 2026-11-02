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
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

func discoveryTestCluster(t *testing.T) (client.Client, *apiClients) {
	t.Helper()
	setAPITestFlags(t, false)

	cl := newTestCluster()
	cl.Spec.Discovery = &ichorv1alpha1.TalosClusterDiscovery{Addresses: []string{"10.0.0.5"}, CIDRs: []string{"10.1.0.0/24"}}
	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cl).Build()

	return k8s, localClients(apiOpts, apiDeps{K8s: k8s})
}

func storedCLIDiscovery(t *testing.T, k8s client.Client) *ichorv1alpha1.TalosClusterDiscovery {
	t.Helper()

	var cl ichorv1alpha1.TalosCluster
	if err := k8s.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: opTestCluster}, &cl); err != nil {
		t.Fatal(err)
	}

	return cl.Spec.Discovery
}

func discoveryCmdIO(in string) (*cobra.Command, *bytes.Buffer) {
	cmd := &cobra.Command{}
	out := &bytes.Buffer{}
	cmd.SetIn(strings.NewReader(in))
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})

	return cmd, out
}

func TestDiscoveryAddPicksTheCallByWhatWasTyped(t *testing.T) {
	k8s, c := discoveryTestCluster(t)
	cmd, _ := discoveryCmdIO("")

	for _, target := range []string{"10.0.0.6", "192.168.0.9/24"} {
		if err := runDiscoveryEdit(context.Background(), c, cmd, target, false); err != nil {
			t.Fatalf("%s: %v", target, err)
		}
	}

	d := storedCLIDiscovery(t, k8s)
	if !slices.Equal(d.Addresses, []string{"10.0.0.5", "10.0.0.6"}) || !slices.Equal(d.CIDRs, []string{"10.1.0.0/24", "192.168.0.0/24"}) {
		t.Fatalf("got %+v", d)
	}

	if err := runDiscoveryEdit(context.Background(), c, cmd, "10.0.0.0/16", false); err == nil || !strings.Contains(err.Error(), "/24") {
		t.Fatalf("a range larger than /24 must be refused with the reason, got %v", err)
	}
}

func TestDiscoveryRemoveAsksBeforeRemovingARange(t *testing.T) {
	k8s, c := discoveryTestCluster(t)

	cmd, _ := discoveryCmdIO("n\n")
	if err := runDiscoveryEdit(context.Background(), c, cmd, "10.1.0.0/24", true); err == nil || !strings.Contains(err.Error(), "aborted") {
		t.Fatalf("want aborted, got %v", err)
	}

	if len(storedCLIDiscovery(t, k8s).CIDRs) != 1 {
		t.Fatal("the range was removed without consent")
	}

	cmd, _ = discoveryCmdIO("y\n")
	if err := runDiscoveryEdit(context.Background(), c, cmd, "10.1.0.0/24", true); err != nil {
		t.Fatal(err)
	}

	// An address needs no question.
	cmd, _ = discoveryCmdIO("")
	if err := runDiscoveryEdit(context.Background(), c, cmd, "10.0.0.5", true); err != nil {
		t.Fatal(err)
	}

	if d := storedCLIDiscovery(t, k8s); len(d.Addresses)+len(d.CIDRs) != 0 {
		t.Fatalf("got %+v", d)
	}
}

func TestDiscoveryList(t *testing.T) {
	_, c := discoveryTestCluster(t)

	var out bytes.Buffer
	if err := runDiscoveryList(context.Background(), c, &out); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"address  10.0.0.5", "range    10.1.0.0/24"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("list lacks %q:\n%s", want, out.String())
		}
	}
}
