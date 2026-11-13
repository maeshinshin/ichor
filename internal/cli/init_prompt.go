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
	"io"
	"net"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/util/validation"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/uiapi"
)

// endpointTypes is the one definition of TalosCluster.spec.endpoint.type's values (the CRD enum), with
// what --endpoint-address holds for each. The flag help, buildInitPlan's check, the `init -f`
// template and the prompt all read it. The first is the --endpoint-type default.
var endpointTypes = []struct{ Name, Address string }{
	{"vip", "an IP address"},
	{"loadBalancer", "a hostname"},
	{"node", "an IP address"},
}

// endpointTypeNames lists the names joined by sep, with the last one joined by lastSep.
func endpointTypeNames(sep, lastSep string) string {
	names := make([]string, len(endpointTypes))
	for i, t := range endpointTypes {
		names[i] = t.Name
	}

	return strings.Join(names[:len(names)-1], sep) + lastSep + names[len(names)-1]
}

func validEndpointType(name string) bool {
	return slices.ContainsFunc(endpointTypes, func(t struct{ Name, Address string }) bool { return t.Name == name })
}

// endpointAddressKind says what --endpoint-address holds for an endpoint type.
func endpointAddressKind(typ string) string {
	for _, t := range endpointTypes {
		if t.Name == typ {
			return t.Address
		}
	}

	return "an address" // a type from -f or --endpoint-type that the plan check will refuse
}

// initQuestions lists what init must ask for: the settings that neither a flag nor the file gave.
// --install-disk is not among them: only the node can list its disks (see chooseInstallDisk).
func initQuestions(flags *pflag.FlagSet) []promptQuestion {
	var qs []promptQuestion

	if !flags.Changed(flagCluster) {
		qs = append(qs, promptQuestion{
			flag:  flagCluster,
			label: "Cluster name",
			check: func(a string) error {
				if errs := validation.IsDNS1123Subdomain(a); len(errs) > 0 {
					return fmt.Errorf("%q is not a valid name: %s", a, strings.Join(errs, "; "))
				}

				return nil
			},
		})
	}

	if !flags.Changed(flagEndpointAddress) {
		// The type decides what the address is, so it is asked first, but only here: left unasked it is the default.
		if !flags.Changed(flagEndpointType) {
			choices := make([]promptChoice, len(endpointTypes))
			for i, t := range endpointTypes {
				choices[i] = promptChoice{label: t.Name + " (" + t.Address + ")", value: t.Name}
			}

			qs = append(qs, promptQuestion{
				flag:    flagEndpointType,
				label:   "Endpoint type",
				choices: choices,
			})
		}

		qs = append(qs, promptQuestion{
			flag:  flagEndpointAddress,
			label: "Endpoint address",
			help: func(got map[string]string) string {
				return endpointAddressKind(got[flagEndpointType]) + " is expected here"
			},
			check: answerRequired,
		})
	}

	if !flags.Changed(flagNodeEndpoint) {
		qs = append(qs, promptQuestion{
			flag:  flagNodeEndpoint,
			label: "Node address",
			help:  func(map[string]string) string { return "host:port of the first node's Talos API" },
			check: answerRequired,
		})
	}

	return qs
}

// promptInitMissing asks the initQuestions and sets the answers on flags as if they had been typed there.
func promptInitMissing(flags *pflag.FlagSet, r io.Reader, w io.Writer) error {
	qs := initQuestions(flags)
	if len(qs) == 0 {
		return nil
	}

	typ, _ := flags.GetString(flagEndpointType)

	got, err := askPrompts(qs, map[string]string{flagEndpointType: typ}, r, w)
	if err != nil {
		return err
	}

	for _, q := range qs {
		if err := flags.Set(q.flag, got[q.flag]); err != nil {
			return err
		}
	}

	return nil
}

func answerRequired(a string) error {
	if a == "" {
		return errors.New("an answer is required")
	}

	return nil
}

// chooseInstallDisk returns the --install-disk value for the disk init installs Talos on. One disk is
// taken and named, none is an error, and several are a list to pick from, with nothing selected to
// begin with. When the node's disks could not be read, it asks for the value as text.
func chooseInstallDisk(r io.Reader, w io.Writer, disks []ichorv1alpha1.TalosMachineDiscoveredDisk, readErr error) (string, error) {
	const flag = flagInstallDisk

	if readErr != nil {
		_, _ = fmt.Fprintf(w, "Could not list the node's disks: %v\n", readErr)

		got, err := askPrompts([]promptQuestion{{
			flag:  flag,
			label: "Install disk",
			help:  func(map[string]string) string { return installDiskUsage },
			check: func(a string) error {
				_, err := parseDiskSelector("--install-disk", a)

				return err
			},
		}}, nil, r, w)

		return got[flag], err
	}

	if len(disks) == 0 {
		return "", errors.New("no installable disk was found on the node; name one with --install-disk")
	}

	v := diskFlagValue(disks[0])

	if len(disks) > 1 {
		got, err := askPrompts([]promptQuestion{{
			flag:      flag,
			label:     "Install disk",
			help:      func(map[string]string) string { return "Talos is installed here and the disk is wiped" },
			choices:   diskChoices(disks),
			noDefault: true,
		}}, nil, r, w)
		if err != nil {
			return "", err
		}

		v = got[flag]
	}

	if _, err := parseDiskSelector("--install-disk", v); err != nil {
		return "", err
	}

	if len(disks) == 1 {
		_, _ = fmt.Fprintf(w, "Install disk: %s (the node's only disk), as --install-disk %s\n", disks[0].DevPath, v)
	}

	return v, nil
}

// diskChoices lists disks as aligned rows of device path, model, size and transport, each choosing
// its --install-disk value. A column nobody has a value for is left out, and sizes share one unit.
func diskChoices(disks []ichorv1alpha1.TalosMachineDiscoveredDisk) []promptChoice {
	cells := make([][]string, len(disks))
	widths := make([]int, 4) //nolint:mnd

	for i, d := range disks {
		size := ""
		if d.SizeBytes > 0 {
			size = fmt.Sprintf("%.1f GiB", float64(d.SizeBytes)/(1<<30)) //nolint:mnd
		}

		cells[i] = []string{d.DevPath, d.Model, size, d.Transport}
		for c, v := range cells[i] {
			widths[c] = max(widths[c], utf8.RuneCountInString(v))
		}
	}

	choices := make([]promptChoice, len(disks))

	for i, d := range disks {
		var cols []string

		for c, v := range cells[i] {
			switch pad := strings.Repeat(" ", widths[c]-utf8.RuneCountInString(v)); {
			case widths[c] == 0:
			case c == 2: //nolint:mnd // the size, right-aligned
				cols = append(cols, pad+v)
			default:
				cols = append(cols, v+pad)
			}
		}

		choices[i] = promptChoice{label: strings.TrimRight(strings.Join(cols, "  "), " "), value: diskFlagValue(d)}
	}

	return choices
}

// diskFlagValue is the --install-disk value naming d by the most stable identifier it reports.
func diskFlagValue(d ichorv1alpha1.TalosMachineDiscoveredDisk) string {
	switch {
	case d.WWID != "":
		return "wwid:" + d.WWID
	case d.Serial != "":
		return "serial:" + d.Serial
	case d.ByIDPath != "":
		return d.ByIDPath
	}

	return d.DevPath
}

// chooseNIC returns the permanent MAC of the first node's primary link, the --nic value of a vip endpoint.
// It follows chooseInstallDisk: a given --nic is checked against the links the node reports, a sole link is
// taken and named, none is an error, and several are a list to pick from with nothing selected to begin with
// (or, with no terminal, a refusal that lists them). The link may be given by name or by MAC; only its MAC is
// returned. Whichever way it is settled, it says which link was taken, as name <MAC>.
func chooseNIC(r io.Reader, w io.Writer, given string, links []ichorv1alpha1.TalosMachineDiscoveredLink, readErr error) (string, error) {
	const flag = "--nic"

	if readErr != nil && given == "" {
		return "", fmt.Errorf("could not list the node's network links (%w); name the primary link (its name or permanent MAC) with %s", readErr, flag)
	}

	if _, err := net.ParseMAC(given); given != "" && err != nil { // a name: only its MAC is carried on
		if readErr != nil {
			return "", fmt.Errorf("could not list the node's network links (%w), so %s %q cannot be looked up; give its permanent MAC", readErr, flag, given)
		}

		mac, err := uiapi.ResolveLink(given, links)
		if err != nil {
			return "", fmt.Errorf("%s: %w", flag, err)
		}

		given = mac
	}

	if given != "" {
		i := slices.IndexFunc(links, func(l ichorv1alpha1.TalosMachineDiscoveredLink) bool {
			return strings.EqualFold(l.PermanentMAC, given)
		})
		if i < 0 && readErr == nil {
			return "", fmt.Errorf("%s %s is not a physical link of the node; it has:\n%s", flag, given, nicList(links))
		}

		if i < 0 { // the node's links could not be read, so the MAC is taken as given
			_, _ = fmt.Fprintf(w, "Primary link: %s (the node's links could not be read, so it is not checked)\n", given)

			return given, nil
		}

		return announceNIC(w, links[i]), nil
	}

	switch len(links) {
	case 0:
		return "", fmt.Errorf("no physical network link was found on the node; name the primary link (its name or permanent MAC) with %s", flag)
	case 1:
		return announceNIC(w, links[0]), nil
	}

	if !bundleTerminal() {
		return "", fmt.Errorf("the node has %d physical links and none can be taken for the VIP: choose one with %s\n%s",
			len(links), flag, nicList(links))
	}

	got, err := askPrompts([]promptQuestion{{
		flag:  flagNIC,
		label: "Primary link",
		help: func(map[string]string) string {
			return "the VIP is announced on this link, and the node is addressed on it (DHCP unless --address is given)"
		},
		choices:   nicChoices(links),
		noDefault: true,
	}}, nil, r, w)
	if err != nil {
		return "", err
	}

	i := slices.IndexFunc(links, func(l ichorv1alpha1.TalosMachineDiscoveredLink) bool { return l.PermanentMAC == got[flagNIC] })
	if i < 0 {
		return "", fmt.Errorf("no link was chosen; choose one with %s", flag)
	}

	return announceNIC(w, links[i]), nil
}

const (
	flagNIC       = "nic"
	linkStateDown = "down"
)

// announceNIC prints the link taken, as the name and MAC it is shown by everywhere (uiapi.LinkLabel), and returns its MAC.
func announceNIC(w io.Writer, l ichorv1alpha1.TalosMachineDiscoveredLink) string {
	_, _ = fmt.Fprintf(w, "Primary link: %s\n", uiapi.LinkLabel(l))

	return l.PermanentMAC
}

// nicChoices lists links as aligned rows that start with the link as uiapi.LinkLabel writes it, then its state, speed
// and driver, each row choosing its MAC. A link that is down is listed with its state, not left out.
func nicChoices(links []ichorv1alpha1.TalosMachineDiscoveredLink) []promptChoice {
	rows := make([][]string, len(links))
	widths := make([]int, 4) //nolint:mnd

	for i, l := range links {
		state, speed := linkStateDown, ""
		if l.LinkUp {
			state = "up"
		}

		if l.SpeedMbit > 0 {
			speed = fmt.Sprintf("%d Mbit/s", l.SpeedMbit)
		}

		rows[i] = []string{uiapi.LinkLabel(l), state, speed, l.Driver}
		for c, v := range rows[i] {
			widths[c] = max(widths[c], utf8.RuneCountInString(v))
		}
	}

	choices := make([]promptChoice, len(links))

	for i, l := range links {
		cols := make([]string, len(rows[i]))
		for c, v := range rows[i] {
			cols[c] = v + strings.Repeat(" ", widths[c]-utf8.RuneCountInString(v))
		}

		choices[i] = promptChoice{label: strings.TrimRight(strings.Join(cols, "  "), " "), value: l.PermanentMAC}
	}

	return choices
}

// nicList is nicChoices as lines for an error.
func nicList(links []ichorv1alpha1.TalosMachineDiscoveredLink) string {
	lines := make([]string, len(links))
	for i, c := range nicChoices(links) {
		lines[i] = "  " + c.label
	}

	return strings.Join(lines, "\n")
}
