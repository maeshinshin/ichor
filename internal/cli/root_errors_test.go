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
	"strings"
	"testing"
)

// A failing command prints its error once (cobra's own line and Execute's used to both print it).
func TestExecuteRootPrintsAnErrorOnce(t *testing.T) {
	var buf bytes.Buffer

	rootCmd.SetArgs([]string{"secrets", "--no-such-flag"})
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetErr(nil)
	})

	if _, err := executeRoot(&buf); err == nil {
		t.Fatal("want an error for an unknown flag")
	}

	if n := strings.Count(buf.String(), "unknown flag: --no-such-flag"); n != 1 {
		t.Fatalf("the error was printed %d times:\n%s", n, buf.String())
	}
}
