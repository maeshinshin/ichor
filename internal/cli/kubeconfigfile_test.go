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
	"os"
	"runtime"
	"testing"
)

// TestSaveKubeconfigFile_FixesLooseExistingDirectory is the saveBundleFile guarantee for the kubeconfig
// written next to the bundle.
func TestSaveKubeconfigFile_FixesLooseExistingDirectory(t *testing.T) {
	if runtime.GOOS == osWindows {
		t.Skip("POSIX permission bits don't apply on windows")
	}

	t.Setenv("HOME", t.TempDir())

	dir, err := bundleDir("my-cluster")
	if err != nil {
		t.Fatalf("bundleDir: %v", err)
	}

	if err := os.MkdirAll(dir, 0o777); err != nil {
		t.Fatal(err)
	}

	if _, err := saveKubeconfigFile("my-cluster", []byte("kc")); err != nil {
		t.Fatalf("saveKubeconfigFile: %v", err)
	}

	fi, err := os.Stat(dir)
	if err != nil || fi.Mode().Perm() != bundleDirPerm {
		t.Fatalf("dir mode = %v, %v; want %o", fi, err, bundleDirPerm)
	}
}
