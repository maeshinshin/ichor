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
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/util/validation"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// endpointTypes is the one definition of TalosCluster.spec.endpoint.type's values (the CRD enum), with
// what --endpoint-address holds for each. The flag help, buildInitPlan's check, the `init -f`
// template and the prompt all read it. The first is the --endpoint-type default.
var endpointTypes = []struct{ Name, Address string }{
	{"vip", "an IP address"},
	{"loadBalancer", "a hostname"},
	{"dns", "a hostname"},
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

// ask prompts until check accepts the answer, printing check's error between tries, and returns what
// check returned. End of input with nothing typed is an error, so a closed stdin cannot loop.
func ask(r io.Reader, w io.Writer, prompt string, check func(answer string) error) (string, error) {
	br := bufio.NewReader(r)

	for {
		_, _ = fmt.Fprint(w, prompt)

		line, err := br.ReadString('\n')
		if err != nil && (!errors.Is(err, io.EOF) || line == "") {
			return "", fmt.Errorf("reading the answer: %w", err)
		}

		answer := strings.TrimSpace(line)

		if cerr := check(answer); cerr != nil {
			_, _ = fmt.Fprintln(w, cerr)

			continue
		}

		return answer, nil
	}
}

// readChoice lists options numbered from 1 and returns the index of the one chosen; def is the index
// an empty answer takes, or -1 for no default.
func readChoice(r io.Reader, w io.Writer, label string, options []string, def int) (int, error) {
	for i, o := range options {
		_, _ = fmt.Fprintf(w, "  %d) %s\n", i+1, o)
	}

	prompt := label + ": "
	if def >= 0 {
		prompt = fmt.Sprintf("%s [%d]: ", label, def+1)
	}

	chosen := -1

	_, err := ask(r, w, prompt, func(a string) error {
		if a == "" && def >= 0 {
			chosen = def

			return nil
		}

		n, err := strconv.Atoi(a)
		if err != nil || n < 1 || n > len(options) {
			return fmt.Errorf("enter a number from 1 to %d", len(options))
		}

		chosen = n - 1

		return nil
	})

	return chosen, err
}

// promptInitMissing asks for the settings init needs that neither a flag nor the file gave, and sets
// them on flags as if they had been typed there. --install-disk is not asked here: only the node can
// list its disks (see chooseInstallDisk).
func promptInitMissing(flags *pflag.FlagSet, r io.Reader, w io.Writer) error {
	r = bufio.NewReader(r) // one reader for every question, so a buffered line is not lost between them

	set := func(name, prompt string, check func(string) error) error {
		v, err := ask(r, w, prompt, check)
		if err != nil {
			return err
		}

		return flags.Set(name, v)
	}

	if !flags.Changed(flagCluster) {
		err := set(flagCluster, "Cluster name: ", func(a string) error {
			if errs := validation.IsDNS1123Subdomain(a); len(errs) > 0 {
				return fmt.Errorf("%q is not a valid name: %s", a, strings.Join(errs, "; "))
			}

			return nil
		})
		if err != nil {
			return err
		}
	}

	if !flags.Changed(flagEndpointAddress) {
		// The type decides what the address is, so it is asked first, but only here: left unasked it is the default.
		if !flags.Changed(flagEndpointType) {
			options := make([]string, len(endpointTypes))
			for i, t := range endpointTypes {
				options[i] = t.Name + " (" + t.Address + ")"
			}

			i, err := readChoice(r, w, "Cluster endpoint type", options, 0)
			if err != nil {
				return err
			}

			if err := flags.Set(flagEndpointType, endpointTypes[i].Name); err != nil {
				return err
			}
		}

		kind := "an address" // a type from -f or --endpoint-type that the plan check will refuse
		typ, _ := flags.GetString(flagEndpointType)

		for _, t := range endpointTypes {
			if t.Name == typ {
				kind = t.Address
			}
		}

		if err := set(flagEndpointAddress, "Cluster endpoint address ("+kind+"): ", answerRequired); err != nil {
			return err
		}
	}

	if !flags.Changed(flagNodeEndpoint) {
		return set(flagNodeEndpoint, "First node's Talos API address (host:port): ", answerRequired)
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
// taken and named, none is an error, and several are a menu. When the node's disks could not be
// read, it asks for the value as text.
func chooseInstallDisk(r io.Reader, w io.Writer, disks []ichorv1alpha1.TalosMachineDiscoveredDisk, readErr error) (string, error) {
	if readErr != nil {
		_, _ = fmt.Fprintf(w, "Could not list the node's disks: %v\n", readErr)

		return ask(r, w, "Install disk ("+installDiskUsage+"): ", func(a string) error {
			_, err := parseDiskSelector("--install-disk", a)

			return err
		})
	}

	if len(disks) == 0 {
		return "", errors.New("no installable disk was found on the node; name one with --install-disk")
	}

	pick := 0

	if len(disks) > 1 {
		options := make([]string, len(disks))
		for i, d := range disks {
			options[i] = fmt.Sprintf("%s  %s  %s  %s", d.DevPath, d.Model, humanBytes(d.SizeBytes), d.Transport)
		}

		var err error

		pick, err = readChoice(r, w, "Install disk (Talos is installed here and the disk is wiped)", options, -1)
		if err != nil {
			return "", err
		}
	}

	v := diskFlagValue(disks[pick])
	if _, err := parseDiskSelector("--install-disk", v); err != nil {
		return "", err
	}

	if len(disks) == 1 {
		_, _ = fmt.Fprintf(w, "Install disk: %s (the node's only disk), as --install-disk %s\n", disks[0].DevPath, v)
	}

	return v, nil
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
