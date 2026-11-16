package v1alpha1

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/maeshinshin/ichor/pkg/ichorconfig"
)

// bondBothCases are one bond written in both spellings: a v1alpha1 interfaces entry and a BondConfig, same name.
var bondBothCases = []struct {
	name, legacy, document string
	wantRefused            string // a setting the refusal must name; empty when the pair is allowed
}{
	{
		name:        "same-setting-in-both",
		legacy:      `"miimon":100`,
		document:    `"miimon":200`,
		wantRefused: "miimon",
	},
	{
		name:        "unlike-setting-still-reset-by-the-document",
		legacy:      `"miimon":100`,
		document:    missedMaxJSON,
		wantRefused: "miimon",
	},
	{
		name:        "links-in-both",
		legacy:      `"interfaces":["eth0","eth1"],"mode":"active-backup"`,
		document:    missedMaxJSON,
		wantRefused: "interfaces",
	},
	{
		name:     "primary-beside-a-bondconfig-is-allowed",
		legacy:   `"primary":"eth0"`,
		document: missedMaxJSON,
	},
	{
		name:     "deviceSelectors-beside-a-bondconfig-is-allowed",
		legacy:   `"deviceSelectors":[{"driver":"virtio_net"}]`,
		document: missedMaxJSON,
	},
}

// TestBondInBothSpellingsIsRefused refuses a bond whose v1alpha1 entry carries a setting the BondConfig of the
// same name resets, and allows the two settings a BondConfig cannot carry.
func TestBondInBothSpellingsIsRefused(t *testing.T) {
	t.Parallel()

	for _, tc := range bondBothCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			spec := `{"machine":{"network":{"interfaces":[{"interface":"bond0","bond":{` + tc.legacy + `}}],` +
				`"bonds":{"bond0":{"links":["eth0","eth1"],"bondMode":1,` + tc.document + `}}}}}`

			var c ichorconfig.Config
			if err := json.Unmarshal([]byte(spec), &c); err != nil {
				t.Fatal(err)
			}

			docs, err := ConvertConfig(&c, true)
			if err != nil {
				t.Fatalf("ConvertConfig: %v", err)
			}

			err = CheckBondSpellings(docs)
			switch {
			case tc.wantRefused == "" && err != nil:
				t.Fatalf("want the pair allowed, got %v", err)
			case tc.wantRefused != "" && (err == nil || !strings.Contains(err.Error(), tc.wantRefused)):
				t.Fatalf("want a refusal naming %s, got %v", tc.wantRefused, err)
			}
		})
	}
}
