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
	"fmt"

	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

// bondEnum is one bond enumeration the BondConfig mirror holds as an integer, and the largest value its nethelpers
// table has. Each table numbers its constants from zero with iota, so the largest value is its last constant:
// BondMode (nethelpers/bondmode.go) BondModeALB, 6; BondXmitHashPolicy (bondxmithashpolicy.go)
// BondXmitPolicyEncap34, 4; LACPRate (lacprate.go) LACPRateFast, 1; ARPValidate (arpvalidate.go)
// ARPValidateFilterBackup, 6; ARPAllTargets (arpalltargets.go) ARPAllTargetsAll, 1; FailOverMAC (failovermac.go)
// FailOverMACFollow, 2; ADSelect (adselect.go) ADSelectCount, 2; PrimaryReselect (primaryreselect.go)
// PrimaryReselectFailure, 2; ADLACPActive (adlacpactive.go) ADLACPActiveOn, 1.
type bondEnum struct {
	field string
	max   uint64
	value func(*typedconfig.BondConfigV1Alpha1) (uint64, bool)
}

func optU8(p *uint8) (uint64, bool) {
	if p == nil {
		return 0, false
	}

	return uint64(*p), true
}

func optU32(p *uint32) (uint64, bool) {
	if p == nil {
		return 0, false
	}

	return uint64(*p), true
}

var bondEnums = []bondEnum{
	{"bondMode", 6, func(b *typedconfig.BondConfigV1Alpha1) (uint64, bool) { return optU8(b.BondMode) }},
	{"xmitHashPolicy", 4, func(b *typedconfig.BondConfigV1Alpha1) (uint64, bool) { return optU8(b.BondXmitHashPolicy) }},
	{"lacpRate", 1, func(b *typedconfig.BondConfigV1Alpha1) (uint64, bool) { return optU8(b.BondLACPRate) }},
	{"arpValidate", 6, func(b *typedconfig.BondConfigV1Alpha1) (uint64, bool) { return optU32(b.BondARPValidate) }},
	{"arpAllTargets", 1, func(b *typedconfig.BondConfigV1Alpha1) (uint64, bool) { return optU32(b.BondARPAllTargets) }},
	{"failOverMac", 2, func(b *typedconfig.BondConfigV1Alpha1) (uint64, bool) { return optU8(b.BondFailOverMAC) }},
	{"adSelect", 2, func(b *typedconfig.BondConfigV1Alpha1) (uint64, bool) { return optU8(b.BondADSelect) }},
	{"primaryReselect", 2, func(b *typedconfig.BondConfigV1Alpha1) (uint64, bool) { return optU8(b.BondPrimaryReselect) }},
	{"adLACPActive", 1, func(b *typedconfig.BondConfigV1Alpha1) (uint64, bool) { return optU8(b.BondADLACPActive) }},
}

// checkBondEnums refuses a BondConfig whose enumeration holds a value no nethelpers constant has. Without it the
// integer is written to the node as it is, and the node runs a mode no table names.
func checkBondEnums(b *typedconfig.BondConfigV1Alpha1) error {
	for _, e := range bondEnums {
		if v, ok := e.value(b); ok && v > e.max {
			return fmt.Errorf("bond %s: %s is %d, and the setting has no value above %d", b.MetaName, e.field, v, e.max)
		}
	}

	return nil
}
