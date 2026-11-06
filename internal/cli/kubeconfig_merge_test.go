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
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

const mergedDemo = "admin@demo"

// adminKubeconfig is what the cluster hands out: cluster, admin@cluster user and context.
func adminKubeconfig(t *testing.T, cluster, server string) []byte {
	t.Helper()

	cfg := clientcmdapi.NewConfig()
	cfg.Clusters[cluster] = &clientcmdapi.Cluster{Server: server, CertificateAuthorityData: []byte("ca-" + cluster)}
	cfg.AuthInfos["admin@"+cluster] = &clientcmdapi.AuthInfo{ClientCertificateData: []byte("crt"), ClientKeyData: []byte("key")}
	cfg.Contexts["admin@"+cluster] = &clientcmdapi.Context{Cluster: cluster, AuthInfo: "admin@" + cluster}
	cfg.CurrentContext = "admin@" + cluster

	out, err := clientcmd.Write(*cfg)
	if err != nil {
		t.Fatal(err)
	}

	return out
}

func mustLoad(t *testing.T, path string) *clientcmdapi.Config {
	t.Helper()

	cfg, err := clientcmd.LoadFromFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return cfg
}

func TestMergeKubeconfig_IntoMissingFileCreatesDirAndSets0600(t *testing.T) {
	target := filepath.Join(t.TempDir(), "newdir", "config")

	name, err := mergeKubeconfig(target, []string{target}, adminKubeconfig(t, "demo", credServer), "demo")
	if err != nil || name != mergedDemo {
		t.Fatalf("mergeKubeconfig = %q, %v", name, err)
	}

	cfg := mustLoad(t, target)
	if cfg.CurrentContext != mergedDemo || cfg.Clusters["demo"].Server != credServer || cfg.AuthInfos[mergedDemo] == nil {
		t.Fatalf("merged config wrong: %+v", cfg)
	}

	if runtime.GOOS != osWindows {
		if fi, err := os.Stat(target); err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %v, %v; want 0600", fi, err)
		}
	}
}

func TestMergeKubeconfig_KeepsOtherContextsIntact(t *testing.T) {
	target := filepath.Join(t.TempDir(), "config")

	if _, err := mergeKubeconfig(target, []string{target}, adminKubeconfig(t, "other", "https://10.0.0.9:6443"), "other"); err != nil {
		t.Fatal(err)
	}

	if _, err := mergeKubeconfig(target, []string{target}, adminKubeconfig(t, "demo", credServer), "demo"); err != nil {
		t.Fatal(err)
	}

	cfg := mustLoad(t, target)
	if len(cfg.Contexts) != 2 || cfg.Clusters["other"].Server != "https://10.0.0.9:6443" || cfg.Contexts["admin@other"] == nil {
		t.Fatalf("other cluster not intact: %+v", cfg)
	}

	if cfg.CurrentContext != mergedDemo {
		t.Fatalf("current-context = %q", cfg.CurrentContext)
	}
}

func TestMergeKubeconfig_CollisionRenamesWithSuffix(t *testing.T) {
	target := filepath.Join(t.TempDir(), "config")

	for _, server := range []string{credServer, "https://10.0.0.2:6443", "https://10.0.0.3:6443"} {
		if _, err := mergeKubeconfig(target, []string{target}, adminKubeconfig(t, "demo", server), "demo"); err != nil {
			t.Fatal(err)
		}
	}

	cfg := mustLoad(t, target)
	for ctx, server := range map[string]string{
		mergedDemo: credServer, "admin@demo-1": "https://10.0.0.2:6443", "admin@demo-2": "https://10.0.0.3:6443",
	} {
		c := cfg.Contexts[ctx]
		if c == nil || cfg.Clusters[c.Cluster].Server != server || cfg.AuthInfos[c.AuthInfo] == nil {
			t.Fatalf("context %s wrong: %+v", ctx, cfg)
		}
	}

	if cfg.CurrentContext != "admin@demo-2" {
		t.Fatalf("current-context = %q", cfg.CurrentContext)
	}
}

func TestMergeKubeconfig_RepeatedMergeIsIdempotent(t *testing.T) {
	target := filepath.Join(t.TempDir(), "config")
	kc := adminKubeconfig(t, "demo", credServer)

	for range 2 {
		if name, err := mergeKubeconfig(target, []string{target}, kc, "demo"); err != nil || name != mergedDemo {
			t.Fatalf("mergeKubeconfig = %q, %v", name, err)
		}
	}

	if cfg := mustLoad(t, target); len(cfg.Contexts) != 1 {
		t.Fatalf("contexts = %d, want 1", len(cfg.Contexts))
	}
}

func TestKubeconfigMergeTarget_MultiPathEnv(t *testing.T) {
	dir := t.TempDir()
	missing, existing := filepath.Join(dir, "a"), filepath.Join(dir, "b")

	if err := os.WriteFile(existing, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("KUBECONFIG", missing+string(os.PathListSeparator)+existing)

	// client-go writes to the first existing file; none existing, to the first entry.
	if got, chain := kubeconfigMergeTarget(""); got != existing || len(chain) != 2 {
		t.Fatalf("target = %q, chain %v; want %q", got, chain, existing)
	}

	if got, _ := kubeconfigMergeTarget(filepath.Join(dir, "x")); got != filepath.Join(dir, "x") {
		t.Fatalf("explicit target = %q", got)
	}

	if err := os.Remove(existing); err != nil {
		t.Fatal(err)
	}

	if got, _ := kubeconfigMergeTarget(""); got != missing {
		t.Fatalf("target = %q, want %q", got, missing)
	}
}

func TestKubeconfigMergeTarget_DefaultsToHomeKubeConfig(t *testing.T) {
	t.Setenv("KUBECONFIG", "")
	t.Setenv("HOME", t.TempDir())

	if got, _ := kubeconfigMergeTarget(""); !strings.HasSuffix(got, filepath.Join(".kube", "config")) {
		t.Fatalf("target = %q", got)
	}
}

func TestMergeKubeconfig_MultiPathWritesOnlyTargetButAvoidsNamesInOtherFiles(t *testing.T) {
	dir := t.TempDir()
	first, second := filepath.Join(dir, "first"), filepath.Join(dir, "second")

	if _, err := mergeKubeconfig(first, []string{first}, adminKubeconfig(t, "demo", credServer), "demo"); err != nil {
		t.Fatal(err)
	}

	before, _ := os.ReadFile(first)

	name, err := mergeKubeconfig(second, []string{first, second}, adminKubeconfig(t, "demo", "https://10.0.0.2:6443"), "demo")
	if err != nil || name != "admin@demo-1" {
		t.Fatalf("mergeKubeconfig = %q, %v", name, err)
	}

	if after, _ := os.ReadFile(first); string(after) != string(before) {
		t.Fatal("the other file of $KUBECONFIG was modified")
	}
}

func TestMergeKubeconfig_ErrorsNeverContainCredentials(t *testing.T) {
	target := filepath.Join(t.TempDir(), "config")

	if err := os.WriteFile(target, []byte("not: [valid"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := mergeKubeconfig(target, []string{target}, adminKubeconfig(t, "demo", "https://x"), "demo")
	if err == nil || strings.Contains(err.Error(), "key") && strings.Contains(err.Error(), "crt") {
		t.Fatalf("err = %v", err)
	}
}
