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

	"github.com/spf13/cobra"
)

func TestAuthFlowRefusesAPlainHTTPIssuer(t *testing.T) {
	old := authOpts
	t.Cleanup(func() { authOpts = old })

	authOpts.issuer = "http://ichor.example.com"

	if _, err := newAuthFlowFor(&cobra.Command{}, uiAudience, false); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("newAuthFlowFor error = %v, want an https refusal", err)
	}
}
