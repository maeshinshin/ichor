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

package v1alpha1

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

// bondDocumentOnly are the settings only the v1alpha1 entry may carry beside a BondConfig of the same name: the two
// a BondConfig cannot spell. adActorSystem is in neither list, because Talos refuses any value of it.
var bondDocumentOnly = []string{"primary", "deviceSelectors"}

// CheckBondSpellings refuses a bond that one patch writes in both spellings: a BondConfig, and a v1alpha1 interfaces
// entry of the same name carrying any setting but primary and deviceSelectors. Talos loads the two without a
// conflict, and gives each setting the BondConfig carries its value, while a setting the BondConfig leaves unset is
// reset to zero, so the v1alpha1 value is lost. A name in both spellings is allowed when the entry carries only the
// two settings a BondConfig cannot carry.
//
// The check covers bonds only. The bridge, dummy link, Layer 2 VIP and DHCPv6 pairs are not analysed and are not
// checked. It runs on what a patch converts to, at admission, so an object stored before it existed still renders.
func CheckBondSpellings(docs []TalosConfigPatchDocument) error {
	bonds := map[string]bool{}

	for i := range docs {
		if docs[i].BondConfig != nil {
			bonds[docs[i].BondConfig.MetaName] = true
		}
	}

	for i := range docs {
		c := docs[i].Config
		if c == nil || c.MachineConfig == nil || c.MachineConfig.MachineNetwork == nil {
			continue
		}

		for _, dev := range c.MachineConfig.MachineNetwork.NetworkInterfaces {
			if dev == nil || dev.DeviceBond == nil || !bonds[dev.DeviceInterface] {
				continue
			}

			carried, err := bondCarriedBesidesBondConfig(dev.DeviceBond)
			if err != nil {
				return err
			}

			if len(carried) > 0 {
				return fmt.Errorf("bond %s is written in a BondConfig and in machine.network.interfaces, and its "+
					"v1alpha1 entry carries %s: write the bond once. The BondConfig owns every setting of the bond, "+
					"the eight only it spells (hardwareAddr, nsIp6Targets, adLACPActive, missedMax, up, multicast, "+
					"addresses[].routePriority, routes[].table) and each setting both spell alike. The v1alpha1 entry "+
					"owns primary and deviceSelectors, the two a BondConfig cannot carry. Talos gives each setting the "+
					"BondConfig carries its value and resets a setting it leaves unset to zero, so the v1alpha1 value "+
					"of the same bond is lost", dev.DeviceInterface, strings.Join(carried, ", "))
			}
		}
	}

	return nil
}

// bondCarriedBesidesBondConfig names the settings of a v1alpha1 bond that are set and are not one of the two a
// BondConfig cannot carry.
func bondCarriedBesidesBondConfig(b *typedconfig.Bond) ([]string, error) {
	raw, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}

	var set map[string]any
	if err := json.Unmarshal(raw, &set); err != nil {
		return nil, err
	}

	var carried []string

	for k, v := range set {
		if slices.Contains(bondDocumentOnly, k) || isEmptyJSON(v) {
			continue
		}

		carried = append(carried, k)
	}

	slices.Sort(carried)

	return carried, nil
}

// isEmptyJSON reports whether a decoded JSON value is one the encoder would have omitted: null, an empty string or
// list, or a zero number.
func isEmptyJSON(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	case []any:
		return len(t) == 0
	case float64:
		return t == 0
	case bool:
		return !t
	}

	return false
}
