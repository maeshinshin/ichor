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
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"
)

const (
	testOtherCluster = "other"
	testOtherAddr    = "10.9.9.9"
	testVDA          = "/dev/vda"
	initKeyName      = "name"
)

const initFileFull = `apiVersion: ichor.maesh.dev/v1alpha1
kind: InitConfig
cluster:
  name: ichor-ui-demo5
  namespace: ichor-system
  preset: single
  endpoint:
    type: node
    address: 10.244.1.56
  network:
    podSubnets:
      - 10.248.0.0/16
    serviceSubnets:
      - 10.249.0.0/16
    cni:
      tunnel: geneve
  registryMirrors:
    ghcr.io: http://10.107.28.190:5000
  certSANs:
    - ichor.lan
firstNode:
  endpoint: 10.244.1.56:50000
  installDisk: /dev/vda
  disks:
    ephemeralSelector: 'disk.wwid == "e"'
    storage:
      selector: 'disk.wwid == "s"'
      volumeName: storage
    userVolume:
      selector: 'disk.wwid == "u"'
      volumeName: data
discovery:
  addresses:
    - 10.244.1.225
  cidrs:
    - 10.244.1.224/29
ui:
  url: https://192.168.0.69
  adminUser: admin
  tls:
    certFile: ./ui.crt
    keyFile: ./ui.key
  kubeAPI:
    serverURL: https://192.168.0.69:6443
    tlsServerName: 10.244.1.56
waitTimeout: 30m
`

const initFileMinimal = `apiVersion: ichor.maesh.dev/v1alpha1
kind: InitConfig
cluster:
  name: demo
  endpoint:
    address: 10.0.0.1
firstNode:
  endpoint: 10.0.0.1:50000
  installDisk: /dev/sda
`

// parsedInit binds a fresh flag set, parses args, applies the file (when given), runs the required
// check and returns what runInit would be given.
func parsedInit(t *testing.T, file string, args ...string) (initOptions, time.Duration, error) {
	t.Helper()
	setTerminal(t, false)

	var (
		o    initOptions
		wait time.Duration
	)

	flags := pflag.NewFlagSet("init", pflag.ContinueOnError)
	bindInitFlags(flags, &o, &wait)

	if err := flags.Parse(args); err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	if file != "" {
		if err := applyInitFile(flags, file); err != nil {
			return o, wait, err
		}
	}

	return o, wait, checkInitRequired(flags)
}

func writeInitFile(t *testing.T, dir, content string) string {
	t.Helper()

	p := filepath.Join(dir, "init.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	return p
}

func TestInitFile_FullExampleEqualsTheFlagLine(t *testing.T) {
	dir := t.TempDir()
	file := writeInitFile(t, dir, initFileFull)

	fromFile, fileWait, err := parsedInit(t, file)
	if err != nil {
		t.Fatalf("file: %v", err)
	}

	fromFlags, flagWait, err := parsedInit(t, "",
		"--cluster", "ichor-ui-demo5", "-n", "ichor-system", "--preset", "single",
		"--endpoint-type", "node", "--endpoint-address", "10.244.1.56",
		"--pod-subnets", "10.248.0.0/16", "--service-subnets", "10.249.0.0/16",
		"--cni-tunnel", "geneve", "--registry-mirror", "ghcr.io=http://10.107.28.190:5000",
		"--node-endpoint", "10.244.1.56:50000", "--install-disk", testVDA,
		"--ephemeral-disk-selector", `disk.wwid == "e"`,
		"--storage-disk-selector", `disk.wwid == "s"`, "--storage-volume-name", "storage",
		"--user-volume-disk-selector", `disk.wwid == "u"`, "--user-volume-name", "data",
		"--cert-san", "ichor.lan",
		"--discover-address", "10.244.1.225", "--discover-cidr", "10.244.1.224/29",
		"--ui-url", "https://192.168.0.69", "--admin-user", "admin",
		"--ui-tls-cert", filepath.Join(dir, "ui.crt"), "--ui-tls-key", filepath.Join(dir, "ui.key"),
		"--kube-api-server-url", "https://192.168.0.69:6443", "--kube-api-tls-server-name", "10.244.1.56",
		"--wait-timeout", "30m")
	if err != nil {
		t.Fatalf("flags: %v", err)
	}

	if !reflect.DeepEqual(fromFile, fromFlags) {
		t.Errorf("file and flags differ:\nfile:  %+v\nflags: %+v", fromFile, fromFlags)
	}

	if fileWait != flagWait || fileWait != 30*time.Minute {
		t.Errorf("wait: file %v, flags %v", fileWait, flagWait)
	}
}

func TestInitFile_AFlagOverridesTheFile(t *testing.T) {
	file := writeInitFile(t, t.TempDir(), initFileFull)

	o, _, err := parsedInit(t, file, "--cluster", testOtherCluster, "--discover-address", testOtherAddr, "--preset", "large")
	if err != nil {
		t.Fatal(err)
	}

	if o.Cluster != testOtherCluster || o.Preset != "large" {
		t.Errorf("cluster %q preset %q, want the flags' values", o.Cluster, o.Preset)
	}

	if !reflect.DeepEqual(o.DiscoverAddresses, []string{testOtherAddr}) {
		t.Errorf("discover addresses %v, want only the flag's", o.DiscoverAddresses)
	}

	if o.InstallDisk != testVDA {
		t.Errorf("install disk %q, want the file's", o.InstallDisk)
	}
}

func TestInitFile_ParseErrors(t *testing.T) {
	for name, tc := range map[string]struct{ content, want string }{
		"unknown key": {
			strings.Replace(initFileMinimal, "firstNode:", "firstNode:\n  instalDisk: x", 1), "instalDisk",
		},
		"wrong kind":       {strings.Replace(initFileMinimal, "InitConfig", "Other", 1), `"Other"`},
		"wrong apiVersion": {strings.Replace(initFileMinimal, "v1alpha1", "v2", 1), `"ichor.maesh.dev/v2"`},
		"wrong type":       {strings.Replace(initFileMinimal, "name: demo", "name: [a]", 1), initKeyName},
		"duplicate key":    {initFileMinimal + "cluster:\n  name: x\n", flagCluster},
		"bad duration":     {initFileMinimal + "waitTimeout: soon\n", "waitTimeout"},
	} {
		t.Run(name, func(t *testing.T) {
			file := writeInitFile(t, t.TempDir(), tc.content)

			_, _, err := parsedInit(t, file)
			if err == nil {
				t.Fatal("want an error")
			}

			if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), file) {
				t.Errorf("error %q should name %q and the file %q", err, tc.want, file)
			}
		})
	}
}

func TestInitFile_RelativePathsResolveAgainstTheFileDirectory(t *testing.T) {
	dir := t.TempDir()
	file := writeInitFile(t, dir, initFileFull)

	t.Chdir(t.TempDir())

	o, _, err := parsedInit(t, file)
	if err != nil {
		t.Fatal(err)
	}

	if o.UITLSCert != filepath.Join(dir, "ui.crt") || o.UITLSKey != filepath.Join(dir, "ui.key") {
		t.Errorf("cert %q key %q, want them under %q", o.UITLSCert, o.UITLSKey, dir)
	}
}

func TestInitFile_MissingRequiredNamesTheFlagAndTheKey(t *testing.T) {
	file := writeInitFile(t, t.TempDir(), "apiVersion: ichor.maesh.dev/v1alpha1\nkind: InitConfig\n")

	_, _, err := parsedInit(t, file)
	if err == nil {
		t.Fatal("want an error")
	}

	for _, want := range []string{`"cluster"`, "cluster.name", `"endpoint-address"`, `"node-endpoint"`, "firstNode.endpoint", `"install-disk"`, "firstNode.installDisk"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}

	// Flags fill the gap.
	if _, _, err := parsedInit(t, file, "--cluster", "c", "--endpoint-address", "a", "--node-endpoint", "n:1", "--install-disk", "/dev/sda"); err != nil {
		t.Errorf("flags alone should do: %v", err)
	}

	// No file, no flags: same message.
	if _, _, err := parsedInit(t, ""); err == nil || !strings.Contains(err.Error(), "required flag(s)") {
		t.Errorf("no file: %v", err)
	}
}

func TestInitFile_NoUIBlockMeansNoUsers(t *testing.T) {
	file := writeInitFile(t, t.TempDir(), initFileMinimal)

	o, _, err := parsedInit(t, file)
	if err != nil {
		t.Fatal(err)
	}

	if o.UIURL != "" || o.AdminUser != "" || o.uiTLSOn() {
		t.Errorf("ui settings leaked in: %+v", o)
	}

	if o.Namespace != ichorControllerNamespace || o.Preset != defaultPreset {
		t.Errorf("flag defaults lost: namespace %q preset %q", o.Namespace, o.Preset)
	}
}

func TestInitFile_SecretNameWithCertFileFailsTheExistingValidation(t *testing.T) {
	file := writeInitFile(t, t.TempDir(),
		initFileMinimal+"ui:\n  url: https://10.0.0.2\n  tls:\n    secretName: s\n    certFile: c.crt\n    keyFile: c.key\n")

	o, _, err := parsedInit(t, file)
	if err != nil {
		t.Fatal(err)
	}

	err = validateInitUIExtras(o)
	if err == nil || !strings.Contains(err.Error(), "are alternatives, not both") {
		t.Errorf("validateInitUIExtras: %v", err)
	}
}

func TestInitConfigTemplate_ParsesAndUsesBlockLists(t *testing.T) {
	c, err := parseInitConfig([]byte(initConfigTemplate))
	if err != nil {
		t.Fatalf("template does not parse: %v", err)
	}

	if len(c.Discovery.Addresses) == 0 || len(c.Cluster.Network.PodSubnets) == 0 {
		t.Error("template lists did not decode")
	}

	if strings.Contains(initConfigTemplate, "[") {
		t.Error("template has an inline list; lists are shown in block style")
	}

	if _, _, err := parsedInit(t, writeInitFile(t, t.TempDir(), initConfigTemplate)); err != nil {
		t.Errorf("template applied: %v", err)
	}
}

func TestInitFile_FixturesUseBlockLists(t *testing.T) {
	for _, s := range []string{initFileFull, initFileMinimal} {
		if strings.Contains(s, ": [") {
			t.Error("fixture has an inline list")
		}
	}
}
