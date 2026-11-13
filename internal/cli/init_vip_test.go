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
	"io"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/controller"
)

const (
	testLinkName   = "enp0s2"
	testLinkMAC    = "52:54:00:aa:bb:cc"
	testSecondLink = "enp0s3"
	testSecondMAC  = "52:54:00:dd:ee:ff"
	testAdoptAddr  = "10.0.0.11"
	testNodeAddr   = "10.0.0.10"
)

// initWithLinks runs init for a first node that reports links (and a read error) and returns what init applied
// to the node, the TalosMachine it created, the CRs' patches and the error.
func initWithLinks(
	t *testing.T, mutate func(*initOptions), links []ichorv1alpha1.TalosMachineDiscoveredLink, readErr error,
) (applied string, machine ichorv1alpha1.TalosMachine, patches []ichorv1alpha1.TalosConfigPatch, stderr string, err error) {
	t.Helper()

	withTempHome(t)

	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()
	deps, maint, _ := newFakeInitDeps(t, k8sClient)
	errw := &bytes.Buffer{}
	deps.Stderr = errw
	deps.ReadNode = func(context.Context, string, bool) (nodeReading, error) {
		return nodeReading{
			Identity: controller.HardwareIdentity{UUID: testInitMachineUUID, Serial: "serial-1"},
			Links:    links, InventoryErr: readErr,
		}, nil
	}

	opts := validInitOptions()
	if mutate != nil {
		mutate(&opts)
	}

	err = runInit(context.Background(), opts, deps)
	_ = k8sClient.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: testInitMachineUUID}, &machine)

	var list ichorv1alpha1.TalosConfigPatchList

	_ = k8sClient.List(context.Background(), &list)

	return string(maint.applied), machine, list.Items, errw.String(), err
}

// A vip init renders the VIP and the net0 alias that names its link together in the configuration it applies
// to the first node, and records the same link as the machine's spec.network, so the controller's later
// renders of that machine carry both.
func TestRunInit_VIPFirstNodeGetsTheVIPAndItsLink(t *testing.T) {
	for _, preset := range []string{"single", "standard", "compact-ha"} {
		t.Run(preset, func(t *testing.T) {
			applied, machine, patches, stderr, err := initWithLinks(t, func(o *initOptions) { o.Preset = preset },
				[]ichorv1alpha1.TalosMachineDiscoveredLink{testInitLink}, nil)
			if err != nil {
				t.Fatalf("runInit: %v", err)
			}

			for _, want := range []string{
				"kind: Layer2VIPConfig", "name: " + testEndpointAddr, "link: net0",
				"kind: LinkAliasConfig", `mac(link.permanent_addr) == "` + testLinkMAC + `"`, "kind: DHCPv4Config",
			} {
				if !strings.Contains(applied, want) {
					t.Errorf("%q missing from the applied configuration", want)
				}
			}

			if n := machine.Spec.Network; n == nil || n.MAC != testInitLink.PermanentMAC || n.Addressing != "dhcp" {
				t.Errorf("machine spec.network = %+v, want dhcp on %s", n, testInitLink.PermanentMAC)
			}

			// The VIP comes from the renderer, never from a patch the operator would see and own.
			for _, p := range patches {
				for _, d := range p.Spec.Documents {
					if d.Kind == "Layer2VIPConfig" {
						t.Errorf("patch %s carries a Layer2VIPConfig", p.Name)
					}
				}
			}

			if !strings.Contains(stderr, "net0 → "+testLinkName+" ("+testLinkMAC+")") {
				t.Errorf("the output does not connect the alias to the interface:\n%s", stderr)
			}
		})
	}
}

// Any other endpoint type leaves the node as it was: no spec.network, no VIP, and the links are not asked for.
func TestRunInit_OtherEndpointTypesGetNoVIP(t *testing.T) {
	for _, typ := range []string{endpointNode, "loadBalancer", "dns"} {
		t.Run(typ, func(t *testing.T) {
			applied, machine, _, _, err := initWithLinks(t, func(o *initOptions) { o.EndpointType = typ }, nil, errors.New("not read"))
			if err != nil {
				t.Fatalf("runInit: %v", err)
			}

			if machine.Spec.Network != nil || strings.Contains(applied, "Layer2VIPConfig") || strings.Contains(applied, "net0") {
				t.Errorf("network = %+v, applied has the VIP = %v", machine.Spec.Network, strings.Contains(applied, "Layer2VIPConfig"))
			}
		})
	}
}

// With several links init never guesses: it refuses, listing them with the flag that settles it, before
// anything is applied to the node; --nic settles it, and a MAC the node does not have is refused.
func TestRunInit_VIPLinkChoice(t *testing.T) {
	two := []ichorv1alpha1.TalosMachineDiscoveredLink{
		testInitLink, {Name: testSecondLink, PermanentMAC: testSecondMAC, LinkUp: false},
	}

	t.Run("several links and no --nic: refused with the list", func(t *testing.T) {
		applied, _, _, _, err := initWithLinks(t, nil, two, nil)
		if err == nil {
			t.Fatal("expected a refusal")
		}

		for _, want := range []string{"--nic", testLinkName, testInitLink.PermanentMAC, testSecondLink, testSecondMAC, "down"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%q missing from %q", want, err)
			}
		}

		if applied != "" {
			t.Error("the node was configured despite the refusal")
		}
	})

	t.Run("--nic picks one", func(t *testing.T) {
		applied, machine, _, _, err := initWithLinks(t, func(o *initOptions) { o.NIC = strings.ToUpper(testSecondMAC) }, two, nil)
		if err != nil {
			t.Fatalf("runInit: %v", err)
		}

		if machine.Spec.Network == nil || machine.Spec.Network.MAC != testSecondMAC ||
			!strings.Contains(applied, `mac(link.permanent_addr) == "`+testSecondMAC+`"`) {
			t.Errorf("network = %+v", machine.Spec.Network)
		}
	})

	t.Run("--nic the node does not have: refused", func(t *testing.T) {
		_, _, _, _, err := initWithLinks(t, func(o *initOptions) { o.NIC = "52:54:00:00:00:99" }, two, nil)
		if err == nil || !strings.Contains(err.Error(), testInitLink.PermanentMAC) {
			t.Fatalf("err = %v, want a refusal that lists the node's links", err)
		}
	})

	t.Run("no link at all: refused, naming the flag", func(t *testing.T) {
		_, _, _, _, err := initWithLinks(t, nil, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "--nic") {
			t.Fatalf("err = %v, want a refusal naming --nic", err)
		}
	})

	t.Run("links unreadable and no --nic: refused", func(t *testing.T) {
		_, _, _, _, err := initWithLinks(t, nil, nil, errors.New("boom"))
		if err == nil || !strings.Contains(err.Error(), "--nic") || !strings.Contains(err.Error(), "boom") {
			t.Fatalf("err = %v", err)
		}
	})
}

// The picker rows hold what is needed to choose without looking anything up, and a down link is listed.
func TestNICChoices(t *testing.T) {
	rows := nicChoices([]ichorv1alpha1.TalosMachineDiscoveredLink{
		{Name: testLinkName, PermanentMAC: testLinkMAC, LinkUp: true, SpeedMbit: 10000, Driver: "ixgbe"},
		{Name: "eno1", PermanentMAC: testSecondMAC},
	})

	if len(rows) != 2 {
		t.Fatalf("%d rows, want both links", len(rows))
	}

	for _, want := range []string{testLinkName, testLinkMAC, "up", "10000 Mbit/s", "ixgbe"} {
		if !strings.Contains(rows[0].label, want) {
			t.Errorf("%q missing from %q", want, rows[0].label)
		}
	}

	if !strings.Contains(rows[1].label, "down") || rows[1].value != testSecondMAC {
		t.Errorf("the down link = %+v", rows[1])
	}
}

func TestBuildInitPlan_VIPInput(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*initPlanInput)
		want   string
	}{
		"a vip must be an IP address":       {func(in *initPlanInput) { in.EndpointAddress = "vip.example.com" }, "not an IP address"},
		"--nic needs a vip":                 {func(in *initPlanInput) { in.EndpointType, in.NIC = endpointNode, testSecondMAC }, "only used with --endpoint-type vip"},
		"--nic must be a MAC":               {func(in *initPlanInput) { in.NIC = "eno1" }, "not a MAC address"},
		"a valid vip and --nic are allowed": {func(in *initPlanInput) { in.NIC = testSecondMAC }, ""},
	} {
		t.Run(name, func(t *testing.T) {
			in := validInitPlanInput()
			tc.mutate(&in)

			_, err := buildInitPlan(in, io.Discard)
			if (tc.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("err = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

// A vip equal to the first node's own address would be a second address on the link the node is reached by.
func TestRunInit_VIPEqualToTheNodeAddressIsRefused(t *testing.T) {
	_, _, _, _, err := initWithLinks(t, func(o *initOptions) {
		o.EndpointAddress, o.NodeEndpoint = testNodeAddr, testNodeAddr+":50000"
	}, []ichorv1alpha1.TalosMachineDiscoveredLink{testInitLink}, nil)
	if err == nil || !strings.Contains(err.Error(), "first node's own address") {
		t.Fatalf("err = %v", err)
	}

	// For any other type the address may be the node's own (a single node's endpoint).
	if _, _, _, _, err := initWithLinks(t, func(o *initOptions) {
		o.EndpointType, o.EndpointAddress, o.NodeEndpoint = endpointNode, testNodeAddr, testNodeAddr+":50000"
	}, nil, nil); err != nil {
		t.Fatalf("type node: %v", err)
	}
}

// -f carries the link under firstNode.nic, the same value as --nic.
func TestInitFile_NIC(t *testing.T) {
	c, err := parseInitConfig([]byte("apiVersion: ichor.maesh.dev/v1alpha1\nkind: InitConfig\nfirstNode:\n  nic: " + testSecondMAC + "\n"))
	if err != nil {
		t.Fatal(err)
	}

	for _, v := range c.values("") {
		if v.flag == flagNIC && v.values[0] == testSecondMAC {
			return
		}
	}

	t.Fatal("firstNode.nic does not set --nic")
}

// adopt calls an address that is no node's own a load balancer: vip means Ichor configures it, and adopt did not.
func TestAdoptCallsAnUnmatchedEndpointALoadBalancer(t *testing.T) {
	node := func(endpoint, host string) *adoptNode {
		m := &ichorv1alpha1.TalosMachine{Spec: ichorv1alpha1.TalosMachineSpec{Endpoint: endpoint, Role: roleControlPlane}}

		return &adoptNode{M: m, Facts: &TalosFacts{EndpointHost: host}}
	}

	for name, tc := range map[string]struct{ endpoint, host, want string }{
		"an address in front of the nodes": {testAdoptAddr, testEndpointAddr, "loadBalancer"},
		"a node's own address":             {testAdoptAddr, testAdoptAddr, endpointNode},
		"a name":                           {testAdoptAddr, "k8s.example.com", "dns"},
	} {
		t.Run(name, func(t *testing.T) {
			c, err := clusterFromNodes("c", "ns", "v1.14.3", []*adoptNode{node(tc.endpoint, tc.host)})
			if err != nil {
				t.Fatal(err)
			}

			if got := c.Spec.Endpoint.Type; got != tc.want {
				t.Fatalf("endpoint type = %q, want %q", got, tc.want)
			}
		})
	}
}

// On a terminal, several links are a list like the disks: nothing is selected to begin with, so Enter alone
// picks nothing, and the link moved onto is taken and named with its alias.
func TestChooseNIC_ListOnATerminal(t *testing.T) {
	two := []ichorv1alpha1.TalosMachineDiscoveredLink{
		testInitLink, {Name: testSecondLink, PermanentMAC: testSecondMAC},
	}

	setTerminal(t, true)

	t.Run("a link moved onto is taken and named", func(t *testing.T) {
		var out bytes.Buffer

		// Two rows down from the empty first row is the second link.
		got, err := chooseNIC(typed(t, "\x1b[B\x1b[B\r"), &out, "", two, nil)
		if err != nil || got != testSecondMAC {
			t.Fatalf("got %q, %v", got, err)
		}

		if !strings.Contains(out.String(), "net0 → "+testSecondLink+" ("+testSecondMAC+")") {
			t.Errorf("the choice was not shown with its alias:\n%s", out.String())
		}
	})

	t.Run("Enter on the untouched list picks nothing", func(t *testing.T) {
		if got, err := chooseNIC(typed(t, "\r\x1b"), &bytes.Buffer{}, "", two, nil); err == nil || got != "" {
			t.Fatalf("got %q, %v, want a refusal", got, err)
		}
	})
}
