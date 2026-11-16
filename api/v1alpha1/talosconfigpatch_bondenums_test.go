package v1alpha1

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/maeshinshin/ichor/pkg/ichorconfig"
)

// Literals the bond tests share, named so goconst counts them once.
const (
	bondModeField    = "bondMode"
	adLACPActiveName = "adLACPActive"
	missedMaxJSON    = `"missedMax":2`
)

// bondEnumCases sets one bond enumeration past the largest value its nethelpers table has, with the bond mode
// valid and nothing else, so the range check is the only thing the case asserts.
var bondEnumCases = []struct {
	field, value string
}{
	{bondModeField, "7"},
	{"xmitHashPolicy", "5"},
	{"lacpRate", "2"},
	{"arpValidate", "7"},
	{"arpAllTargets", "2"},
	{"failOverMac", "3"},
	{"adSelect", "3"},
	{"primaryReselect", "3"},
	{adLACPActiveName, "2"},
}

// TestBondEnumsStayInRange refuses a bond enumeration whose value names no nethelpers constant.
func TestBondEnumsStayInRange(t *testing.T) {
	t.Parallel()

	for _, tc := range bondEnumCases {
		t.Run(tc.field, func(t *testing.T) {
			t.Parallel()

			mode := `"bondMode":1,`
			if tc.field == bondModeField {
				mode = ""
			}

			spec := `{"machine":{"network":{"bonds":{"bond0":{"links":["eth0","eth1"],` + mode + `"` + tc.field + `":` + tc.value + `}}}}}`

			var c ichorconfig.Config
			if err := json.Unmarshal([]byte(spec), &c); err != nil {
				t.Fatal(err)
			}

			if _, err := ConvertConfig(&c, true); err == nil || !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("%s=%s: want a refusal naming %s, got %v", tc.field, tc.value, tc.field, err)
			}
		})
	}
}

// TestBondEnumsAcceptTheLargestValue keeps the largest value of each enumeration valid: a range check one too
// strict would refuse it.
func TestBondEnumsAcceptTheLargestValue(t *testing.T) {
	t.Parallel()

	for _, tc := range bondEnumCases {
		t.Run(tc.field, func(t *testing.T) {
			t.Parallel()

			max := map[string]string{bondModeField: "6", "xmitHashPolicy": "4", "lacpRate": "1", "arpValidate": "6",
				"arpAllTargets": "1", "failOverMac": "2", "adSelect": "2", "primaryReselect": "2", adLACPActiveName: "1"}[tc.field]

			mode := `"bondMode":1,`
			if tc.field == bondModeField {
				mode = ""
			}

			spec := `{"machine":{"network":{"bonds":{"bond0":{"links":["eth0","eth1"],` + mode + `"` + tc.field + `":` + max + `}}}}}`

			var c ichorconfig.Config
			if err := json.Unmarshal([]byte(spec), &c); err != nil {
				t.Fatal(err)
			}

			if _, err := ConvertConfig(&c, true); err != nil {
				t.Fatalf("%s=%s: refused: %v", tc.field, max, err)
			}
		})
	}
}
