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
	"os"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/controller"
)

const (
	testHWLink        = "enp1s0"
	testTwinLink      = "twin"
	testLinkName      = "enp0s2"
	testLinkMAC       = "52:54:00:aa:bb:cc"
	testSecondLink    = "enp0s3"
	testSecondMAC     = "52:54:00:dd:ee:ff"
	testAdoptAddr     = "10.0.0.11"
	testNodeAddr      = "10.0.0.10"
	testStaticCIDR    = "192.168.0.201/16"
	testNotIP         = "not an IP address"
	testHWGateway     = "192.168.0.254"
	testLinkAliasKind = "kind: LinkAliasConfig"
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
				testLinkAliasKind, `mac(link.permanent_addr) == "` + testLinkMAC + `"`, "kind: DHCPv4Config",
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

			if !strings.Contains(stderr, "Primary link: "+testLinkName+" <"+testLinkMAC+">\n") {
				t.Errorf("the output does not name the link taken:\n%s", stderr)
			}
		})
	}
}

// A static address for the first node is taken for any endpoint type. A node or loadBalancer gets no VIP, only the
// addressing on its link.
func TestRunInit_StaticAddressForAnyEndpointType(t *testing.T) {
	one := []ichorv1alpha1.TalosMachineDiscoveredLink{testInitLink}

	for _, typ := range []string{endpointNode, endpointLoadBalancer} {
		t.Run(typ, func(t *testing.T) {
			applied, machine, _, _, err := initWithLinks(t, func(o *initOptions) {
				o.EndpointType, o.Addresses, o.Gateway = typ, []string{testStaticCIDR}, testHWGateway
			}, one, nil)
			if err != nil {
				t.Fatalf("runInit: %v", err)
			}

			for _, want := range []string{"kind: LinkConfig", "address: " + testStaticCIDR, "gateway: " + testHWGateway, testLinkAliasKind} {
				if !strings.Contains(applied, want) {
					t.Errorf("%q missing from the applied configuration", want)
				}
			}

			if strings.Contains(applied, "Layer2VIPConfig") || strings.Contains(applied, "kind: DHCPv4Config") {
				t.Error("a non-vip node got a VIP or DHCP on its link")
			}

			if n := machine.Spec.Network; n == nil || n.Addressing != ichorv1alpha1.MachineAddressingStatic || n.MAC != testLinkMAC {
				t.Errorf("machine spec.network = %+v, want static on %s", n, testLinkMAC)
			}
		})
	}
}

// Any other endpoint type leaves the node as it was: no spec.network, no VIP, and the links are not asked for.
func TestRunInit_OtherEndpointTypesGetNoVIP(t *testing.T) {
	for _, typ := range []string{endpointNode, endpointLoadBalancer} {
		t.Run(typ, func(t *testing.T) {
			applied, machine, _, _, err := initWithLinks(t, func(o *initOptions) {
				o.EndpointType = typ
			}, nil, errors.New("not read"))
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

		for _, want := range []string{
			"--nic",
			"\n  " + testLinkName + " <" + testLinkMAC + ">  up\n  " + testSecondLink + " <" + testSecondMAC + ">  down",
		} {
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

	// The format is pinned whole: the link as name <MAC> first, as --nic takes either, then state, speed and driver.
	for i, want := range []string{
		"enp0s2 <52:54:00:aa:bb:cc>  up    10000 Mbit/s  ixgbe",
		"eno1 <52:54:00:dd:ee:ff>    down",
	} {
		if rows[i].label != want {
			t.Errorf("row %d = %q, want %q", i, rows[i].label, want)
		}
	}

	if rows[1].value != testSecondMAC {
		t.Errorf("the down link chooses %q", rows[1].value)
	}
}

func TestBuildInitPlan_VIPInput(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*initPlanInput)
		want   string
	}{
		"a vip must be an IP address": {func(in *initPlanInput) { in.EndpointAddress = "vip.example.com" },
			`"vip.example.com" is a hostname, but endpoint type vip needs an IP address; type loadBalancer would take a hostname`},
		"loadBalancer takes a hostname":       {func(in *initPlanInput) { in.EndpointType, in.EndpointAddress = endpointLoadBalancer, "lb.example.com" }, ""},
		"loadBalancer takes an IP":            {func(in *initPlanInput) { in.EndpointType = endpointLoadBalancer }, ""},
		"--nic is taken for a node endpoint":  {func(in *initPlanInput) { in.EndpointType, in.NIC = endpointNode, testSecondMAC }, ""},
		"--nic written as a MAC needs colons": {func(in *initPlanInput) { in.NIC = "52-54-00-dd-ee-ff" }, "not a MAC address"},
		"--nic may be a link name":            {func(in *initPlanInput) { in.NIC = testHWLink }, ""},
		"--address is taken for a node endpoint": {func(in *initPlanInput) {
			in.EndpointType, in.Addresses = endpointNode, []string{testStaticCIDR}
		}, ""},
		"--gateway is taken for a loadBalancer endpoint": {func(in *initPlanInput) {
			in.EndpointType, in.Gateway = endpointLoadBalancer, testHWGateway
		}, ""},
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
		"a name":                           {testAdoptAddr, "k8s.example.com", endpointLoadBalancer},
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
// picks nothing, and the link moved onto is taken and named.
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

		// Both lines are pinned exactly: the picker's row and the confirmation of the link taken.
		for _, want := range []string{
			testSecondLink + " <" + testSecondMAC + ">",
			"Primary link: " + testSecondLink + " <" + testSecondMAC + ">\n",
		} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("%q missing from the output:\n%s", want, out.String())
			}
		}
	})

	t.Run("Enter on the untouched list picks nothing", func(t *testing.T) {
		if got, err := chooseNIC(typed(t, "\r\x1b"), &bytes.Buffer{}, "", two, nil); err == nil || got != "" {
			t.Fatalf("got %q, %v, want a refusal", got, err)
		}
	})
}

// --address puts the first node's static address on its primary link, as `approve node --address` does: a
// LinkConfig with the address (a /16 as given) and the route, no DHCPv4Config, and the VIP and alias as before.
func TestRunInit_VIPStaticAddress(t *testing.T) {
	one := []ichorv1alpha1.TalosMachineDiscoveredLink{testInitLink}

	applied, machine, _, _, err := initWithLinks(t, func(o *initOptions) {
		o.Addresses, o.Gateway, o.DNS = []string{testStaticCIDR}, testHWGateway, []string{"1.1.1.1"}
	}, one, nil)
	if err != nil {
		t.Fatalf("runInit: %v", err)
	}

	for _, want := range []string{
		"kind: LinkConfig", "address: " + testStaticCIDR, "gateway: " + testHWGateway, "address: 1.1.1.1",
		"kind: Layer2VIPConfig", "link: net0", testLinkAliasKind,
	} {
		if !strings.Contains(applied, want) {
			t.Errorf("%q missing from the applied configuration", want)
		}
	}

	if strings.Contains(applied, "kind: DHCPv4Config") {
		t.Error("a static node also got a DHCPv4Config")
	}

	if n := machine.Spec.Network; n == nil || n.Addressing != ichorv1alpha1.MachineAddressingStatic || n.Gateway != testHWGateway || n.Addresses[0] != testStaticCIDR {
		t.Errorf("machine spec.network = %+v", n)
	}

	// With no --address the node is still DHCP, as before.
	if applied, _, _, _, err = initWithLinks(t, nil, one, nil); err != nil || !strings.Contains(applied, "kind: DHCPv4Config") {
		t.Fatalf("no --address: err %v, DHCPv4Config present = %v", err, strings.Contains(applied, "kind: DHCPv4Config"))
	}
}

// What `approve node` refuses, init refuses too, before anything is applied to the node.
func TestRunInit_VIPStaticAddressRefusals(t *testing.T) {
	one := []ichorv1alpha1.TalosMachineDiscoveredLink{testInitLink}

	for name, tc := range map[string]struct {
		mutate func(*initOptions)
		want   string
	}{
		"--gateway without --address":       {func(o *initOptions) { o.Gateway = testHWGateway }, "--gateway needs a static --address"},
		"--address without a prefix length": {func(o *initOptions) { o.Addresses = []string{"192.168.0.201"} }, "needs a prefix length"},
		"--gateway that is no IP":           {func(o *initOptions) { o.Addresses, o.Gateway = []string{testStaticCIDR}, "gw" }, testNotIP},
		"--dns that is no IP":               {func(o *initOptions) { o.Addresses, o.DNS = []string{testStaticCIDR}, []string{"dns-name"} }, testNotIP},
	} {
		t.Run(name, func(t *testing.T) {
			applied, _, _, _, err := initWithLinks(t, tc.mutate, one, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one containing %q", err, tc.want)
			}

			if applied != "" {
				t.Error("the node was configured despite the refusal")
			}
		})
	}
}

// -f carries the static settings under firstNode, the same values as the flags.
func TestInitFile_StaticKeys(t *testing.T) {
	c, err := parseInitConfig([]byte("apiVersion: ichor.maesh.dev/v1alpha1\nkind: InitConfig\nfirstNode:\n  addresses: [" + testStaticCIDR +
		"]\n  gateway: " + testHWGateway + "\n  dns: [1.1.1.1]\n  vlan: 7\n"))
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]string{}
	for _, v := range c.values("") {
		got[v.flag] = strings.Join(v.values, ",")
	}

	for flag, want := range map[string]string{"address": testStaticCIDR, "gateway": testHWGateway, flagDNS: "1.1.1.1", "vlan": "7"} {
		if got[flag] != want {
			t.Errorf("--%s from the file = %q, want %q", flag, got[flag], want)
		}
	}
}

// A static address needs a link to go on: with several links and no --nic init refuses and lists them, before
// anything is applied, exactly as it does for DHCP; with a sole link that link is the one the address goes on.
func TestRunInit_VIPStaticAddressNeedsAnUnambiguousLink(t *testing.T) {
	two := []ichorv1alpha1.TalosMachineDiscoveredLink{testInitLink, {Name: testSecondLink, PermanentMAC: testSecondMAC}}
	static := func(o *initOptions) { o.Addresses = []string{testStaticCIDR} }

	applied, _, _, _, err := initWithLinks(t, static, two, nil)
	if err == nil || !strings.Contains(err.Error(), "--nic") || applied != "" {
		t.Fatalf("err = %v, applied = %q, want a refusal naming --nic and nothing applied", err, applied)
	}

	if _, machine, _, _, err := initWithLinks(t, static, two[:1], nil); err != nil || machine.Spec.Network.MAC != testLinkMAC {
		t.Fatalf("sole link: err = %v", err)
	}
}

// --nic may be a link name: it is looked up among the links the node reports, and only the MAC goes on, into
// spec.network and into the selector of the rendered LinkAliasConfig, never the name.
func TestRunInit_VIPLinkGivenByName(t *testing.T) {
	two := []ichorv1alpha1.TalosMachineDiscoveredLink{
		testInitLink, {Name: testSecondLink, PermanentMAC: testSecondMAC},
	}

	t.Run("a name resolves to its MAC and only the MAC is rendered", func(t *testing.T) {
		applied, machine, _, stderr, err := initWithLinks(t, func(o *initOptions) { o.NIC = testSecondLink }, two, nil)
		if err != nil {
			t.Fatalf("runInit: %v", err)
		}

		if machine.Spec.Network == nil || machine.Spec.Network.MAC != testSecondMAC {
			t.Errorf("spec.network = %+v, want the MAC of %s", machine.Spec.Network, testSecondLink)
		}

		if !strings.Contains(applied, `mac(link.permanent_addr) == "`+testSecondMAC+`"`) || strings.Contains(applied, testSecondLink) {
			t.Errorf("the rendered configuration does not select by MAC alone:\n%s", applied)
		}

		if !strings.Contains(stderr, "Primary link: "+testSecondLink+" <"+testSecondMAC+">\n") {
			t.Errorf("the output does not name the link taken:\n%s", stderr)
		}
	})

	t.Run("an unknown name is refused, listing every link by name and MAC", func(t *testing.T) {
		applied, _, _, _, err := initWithLinks(t, func(o *initOptions) { o.NIC = "eth9" }, two, nil)
		if err == nil {
			t.Fatal("expected a refusal")
		}

		for _, want := range []string{
			"eth9", "\n  " + testLinkName + " <" + testLinkMAC + ">\n  " + testSecondLink + " <" + testSecondMAC + ">",
		} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%q missing from %q", want, err)
			}
		}

		if applied != "" {
			t.Error("the node was configured despite the refusal")
		}
	})

	t.Run("a name two links share is refused", func(t *testing.T) {
		twins := []ichorv1alpha1.TalosMachineDiscoveredLink{
			{Name: testTwinLink, PermanentMAC: testLinkMAC}, {Name: testTwinLink, PermanentMAC: testSecondMAC},
		}

		_, _, _, _, err := initWithLinks(t, func(o *initOptions) { o.NIC = testTwinLink }, twins, nil)
		if err == nil || !strings.Contains(err.Error(), "names 2 links") || !strings.Contains(err.Error(), testSecondMAC) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("a name cannot be looked up when the links could not be read", func(t *testing.T) {
		_, _, _, _, err := initWithLinks(t, func(o *initOptions) { o.NIC = testSecondLink }, nil, errors.New("boom"))
		if err == nil || !strings.Contains(err.Error(), "boom") || !strings.Contains(err.Error(), "MAC") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("a MAC still works unchanged", func(t *testing.T) {
		_, machine, _, _, err := initWithLinks(t, func(o *initOptions) { o.NIC = testSecondMAC }, two, nil)
		if err != nil || machine.Spec.Network.MAC != testSecondMAC {
			t.Fatalf("err = %v, network = %+v", err, machine.Spec.Network)
		}
	})
}

// -f takes a link name under firstNode.nic as well as a MAC.
func TestInitFile_NICByName(t *testing.T) {
	c, err := parseInitConfig([]byte("apiVersion: ichor.maesh.dev/v1alpha1\nkind: InitConfig\nfirstNode:\n  nic: enp1s0\n"))
	if err != nil {
		t.Fatal(err)
	}

	for _, v := range c.values("") {
		if v.flag == flagNIC && v.values[0] == testHWLink {
			return
		}
	}

	t.Fatal("firstNode.nic: enp1s0 does not set --nic")
}

// A hostname with --endpoint-type vip is refused before anything is created: no bundle is written, nothing is
// applied to the node.
func TestRunInit_EndpointAddressAgainstTypeIsRefusedBeforeAnythingIsCreated(t *testing.T) {
	home := withTempHome(t)
	deps, maint, _ := newFakeInitDeps(t, fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build())

	opts := validInitOptions()
	opts.EndpointAddress = "cluster.example.com"

	err := runInit(context.Background(), opts, deps)
	if err == nil || !strings.Contains(err.Error(), "needs an IP address") || !strings.Contains(err.Error(), "type loadBalancer would take a hostname") {
		t.Fatalf("err = %v", err)
	}

	if _, statErr := os.Stat(home + "/.ichor/clusters/" + testInitClusterName + "/secrets.yaml"); statErr == nil {
		t.Error("a secrets bundle was written before the refusal")
	}

	if maint.callCount != 0 {
		t.Errorf("the node was sent a configuration: %d calls", maint.callCount)
	}
}
