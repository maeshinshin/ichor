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
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

// The hidden --cni flag has a default and cilium is the only CNI, so it must not be a
// required flag: the getting-started guide's command has no --cni.
func TestInitCmd_GettingStartedFlagsPassTheRequiredCheck(t *testing.T) {
	t.Cleanup(func() {
		initCmd.Flags().VisitAll(func(f *pflag.Flag) { f.Changed = false })
	})

	err := initCmd.ParseFlags(strings.Fields("--cluster demo --node-endpoint 192.168.1.50:50000 " +
		"--endpoint-type node --endpoint-address 192.168.1.50 --install-disk /dev/sda " +
		"--preset single --registry-mirror ghcr.io=http://192.168.1.10:5000"))
	if err != nil {
		t.Fatal(err)
	}

	if err := initCmd.ValidateRequiredFlags(); err != nil {
		t.Errorf("the getting-started flags are refused: %v", err)
	}
}
