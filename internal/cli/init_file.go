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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/pflag"
	"sigs.k8s.io/yaml"
)

// `ichor init -f FILE` reads the init settings from a structured file instead of a long flag line. The file is only
// another way to set the same flags: every value goes through the
// flag it names, so parsing, defaults and validation are the flags' own, and a flag given on the
// command line wins over the file.

const (
	flagEndpointType    = "endpoint-type"
	flagEndpointAddress = "endpoint-address"
	flagNodeEndpoint    = "node-endpoint"
	flagInstallDisk     = "install-disk"
	flagDNS             = "dns"

	initConfigAPIVersion = "ichor.maesh.dev/v1alpha1"
	initConfigKind       = "InitConfig"
)

// initConfigTemplate is what `ichor init --print-config-template` prints. It is a valid InitConfig
// as it stands; the test parses it strictly. Lists are in block style.
var initConfigTemplate = `apiVersion: ichor.maesh.dev/v1alpha1
kind: InitConfig
cluster:
  name: demo                       # --cluster (required)
  namespace: ichor-system          # -n/--namespace
  preset: single                   # --preset: single, compact-ha, standard or large
  endpoint:
    type: node                     # --endpoint-type: ` + endpointTypeNames(", ", " or ") + `
    address: 192.168.1.50          # --endpoint-address (required)
  network:
    podSubnets:                    # --pod-subnets
      - 10.248.0.0/16
    serviceSubnets:                # --service-subnets
      - 10.249.0.0/16
    cni:
      tunnel: geneve               # --cni-tunnel: vxlan or geneve
  registryMirrors:                 # --registry-mirror HOST=URL
    ghcr.io: http://192.168.1.10:5000
  certSANs:                        # --cert-san: extra names both certificates carry
    - ichor.lan
firstNode:
  endpoint: 192.168.1.50:50000     # --node-endpoint (required)
  installDisk: /dev/sda            # --install-disk (required)
  # nic: 52:54:00:12:34:56         # --nic: with a vip endpoint, the primary link by name (enp1s0) or permanent MAC (asked for when the node has several)
  # addresses:                     # --address: with a vip endpoint, a static address with prefix length instead of DHCP
  #   - 192.168.1.50/24
  # gateway: 192.168.1.1           # --gateway
  # dns:                           # --dns
  #   - 1.1.1.1
  # vlan: 100                      # --vlan
  disks:
    ephemeralSelector: 'disk.wwid == "naa.0000"'   # --ephemeral-disk-selector
    storage:
      selector: 'disk.wwid == "naa.0001"'          # --storage-disk-selector
      volumeName: storage                          # --storage-volume-name
    userVolume:
      selector: 'disk.wwid == "naa.0002"'          # --user-volume-disk-selector
      volumeName: data                             # --user-volume-name
discovery:
  addresses:                       # --discover-address
    - 192.168.1.51
  cidrs:                           # --discover-cidr
    - 192.168.1.64/29
ui:                                # leave the whole block out for no sign-in (no --ui-url)
  url: https://192.168.1.60        # --ui-url
  adminUser: admin                 # --admin-user
  tls:                             # optional; leave out for Ichor's own CA
    certFile: ./ui.crt             # --ui-tls-cert (relative to this file)
    keyFile: ./ui.key              # --ui-tls-key (a path, never the key itself)
    # secretName: ichor-ui-cert    # --ui-tls-secret, instead of certFile and keyFile
  kubeAPI:
    serverURL: https://192.168.1.60:6443   # --kube-api-server-url
    tlsServerName: 192.168.1.50            # --kube-api-tls-server-name
waitTimeout: 30m                   # --wait-timeout
`

type initConfigFile struct {
	APIVersion  string            `json:"apiVersion"`
	Kind        string            `json:"kind"`
	Cluster     initConfigCluster `json:"cluster"`
	FirstNode   initConfigNode    `json:"firstNode"`
	Discovery   initConfigDisco   `json:"discovery"`
	UI          initConfigUI      `json:"ui"`
	WaitTimeout string            `json:"waitTimeout"`
}

type initConfigCluster struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Preset    string `json:"preset"`
	Endpoint  struct {
		Type    string `json:"type"`
		Address string `json:"address"`
	} `json:"endpoint"`
	Network struct {
		PodSubnets     []string `json:"podSubnets"`
		ServiceSubnets []string `json:"serviceSubnets"`
		CNI            struct {
			Tunnel string `json:"tunnel"`
		} `json:"cni"`
	} `json:"network"`
	RegistryMirrors map[string]string `json:"registryMirrors"`
	CertSANs        []string          `json:"certSANs"`
}

type initConfigVolume struct {
	Selector   string `json:"selector"`
	VolumeName string `json:"volumeName"`
}

type initConfigNode struct {
	Endpoint    string   `json:"endpoint"`
	InstallDisk string   `json:"installDisk"`
	NIC         string   `json:"nic"`
	Addresses   []string `json:"addresses"`
	Gateway     string   `json:"gateway"`
	DNS         []string `json:"dns"`
	VLAN        int32    `json:"vlan"`
	Disks       struct {
		EphemeralSelector string           `json:"ephemeralSelector"`
		Storage           initConfigVolume `json:"storage"`
		UserVolume        initConfigVolume `json:"userVolume"`
	} `json:"disks"`
}

type initConfigDisco struct {
	Addresses []string `json:"addresses"`
	CIDRs     []string `json:"cidrs"`
}

type initConfigUI struct {
	URL       string `json:"url"`
	AdminUser string `json:"adminUser"`
	TLS       struct {
		CertFile   string `json:"certFile"`
		KeyFile    string `json:"keyFile"`
		SecretName string `json:"secretName"`
	} `json:"tls"`
	KubeAPI struct {
		ServerURL     string `json:"serverURL"`
		TLSServerName string `json:"tlsServerName"`
	} `json:"kubeAPI"`
}

// initFileValue is one file value: the flag it sets, the file key it came from, and its values
// (several for the repeatable flags). Empty values are not listed.
type initFileValue struct {
	flag, key string
	values    []string
}

// initFileKeys maps each required flag to the file key that can stand in for it.
var initFileKeys = map[string]string{
	flagCluster:         "cluster.name",
	flagEndpointAddress: "cluster.endpoint.address",
	flagNodeEndpoint:    "firstNode.endpoint",
	flagInstallDisk:     "firstNode.installDisk",
}

// parseInitConfig decodes data strictly: an unknown key, a duplicate key, a wrong type, apiVersion
// or kind is an error.
func parseInitConfig(data []byte) (initConfigFile, error) {
	var c initConfigFile

	if err := yaml.UnmarshalStrict(data, &c, yaml.DisallowUnknownFields); err != nil {
		return c, err
	}

	if c.APIVersion != initConfigAPIVersion || c.Kind != initConfigKind {
		return c, fmt.Errorf("apiVersion/kind must be %s/%s, got %q/%q", initConfigAPIVersion, initConfigKind, c.APIVersion, c.Kind)
	}

	return c, nil
}

// values lists the file's settings as flag values. Relative paths are resolved against dir, the
// directory of the file.
func (c initConfigFile) values(dir string) []initFileValue {
	rel := func(p string) string {
		if p == "" || filepath.IsAbs(p) {
			return p
		}

		return filepath.Join(dir, p)
	}

	one := func(s string) []string {
		if s == "" {
			return nil
		}

		return []string{s}
	}

	var vlan []string
	if c.FirstNode.VLAN != 0 {
		vlan = []string{fmt.Sprint(c.FirstNode.VLAN)}
	}

	mirrors := make([]string, 0, len(c.Cluster.RegistryMirrors))
	for host, u := range c.Cluster.RegistryMirrors {
		mirrors = append(mirrors, host+"="+u)
	}

	slices.Sort(mirrors)

	all := []initFileValue{
		{flagCluster, "cluster.name", one(c.Cluster.Name)},
		{"namespace", "cluster.namespace", one(c.Cluster.Namespace)},
		{"preset", "cluster.preset", one(c.Cluster.Preset)},
		{"endpoint-type", "cluster.endpoint.type", one(c.Cluster.Endpoint.Type)},
		{flagEndpointAddress, "cluster.endpoint.address", one(c.Cluster.Endpoint.Address)},
		{"pod-subnets", "cluster.network.podSubnets", c.Cluster.Network.PodSubnets},
		{"service-subnets", "cluster.network.serviceSubnets", c.Cluster.Network.ServiceSubnets},
		{"cni-tunnel", "cluster.network.cni.tunnel", one(c.Cluster.Network.CNI.Tunnel)},
		{"registry-mirror", "cluster.registryMirrors", mirrors},
		{flagNodeEndpoint, "firstNode.endpoint", one(c.FirstNode.Endpoint)},
		{flagInstallDisk, "firstNode.installDisk", one(c.FirstNode.InstallDisk)},
		{flagNIC, "firstNode.nic", one(c.FirstNode.NIC)},
		{"address", "firstNode.addresses", c.FirstNode.Addresses},
		{"gateway", "firstNode.gateway", one(c.FirstNode.Gateway)},
		{flagDNS, "firstNode.dns", c.FirstNode.DNS},
		{"vlan", "firstNode.vlan", vlan},
		{"ephemeral-disk-selector", "firstNode.disks.ephemeralSelector", one(c.FirstNode.Disks.EphemeralSelector)},
		{"storage-disk-selector", "firstNode.disks.storage.selector", one(c.FirstNode.Disks.Storage.Selector)},
		{"storage-volume-name", "firstNode.disks.storage.volumeName", one(c.FirstNode.Disks.Storage.VolumeName)},
		{"user-volume-disk-selector", "firstNode.disks.userVolume.selector", one(c.FirstNode.Disks.UserVolume.Selector)},
		{"user-volume-name", "firstNode.disks.userVolume.volumeName", one(c.FirstNode.Disks.UserVolume.VolumeName)},
		{"cert-san", "cluster.certSANs", c.Cluster.CertSANs},
		{"discover-address", "discovery.addresses", c.Discovery.Addresses},
		{"discover-cidr", "discovery.cidrs", c.Discovery.CIDRs},
		{"ui-url", "ui.url", one(c.UI.URL)},
		{"admin-user", "ui.adminUser", one(c.UI.AdminUser)},
		{"ui-tls-cert", "ui.tls.certFile", one(rel(c.UI.TLS.CertFile))},
		{"ui-tls-key", "ui.tls.keyFile", one(rel(c.UI.TLS.KeyFile))},
		{"ui-tls-secret", "ui.tls.secretName", one(c.UI.TLS.SecretName)},
		{"kube-api-server-url", "ui.kubeAPI.serverURL", one(c.UI.KubeAPI.ServerURL)},
		{"kube-api-tls-server-name", "ui.kubeAPI.tlsServerName", one(c.UI.KubeAPI.TLSServerName)},
		{"wait-timeout", "waitTimeout", one(c.WaitTimeout)},
	}

	var out []initFileValue

	for _, v := range all {
		if len(v.values) > 0 {
			out = append(out, v)
		}
	}

	return out
}

// applyInitFile sets the flags from the file at path, skipping any flag the command line set.
func applyInitFile(flags *pflag.FlagSet, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("init: reading --file: %w", err)
	}

	cfg, err := parseInitConfig(data)
	if err != nil {
		return fmt.Errorf("init: %s: %w", path, err)
	}

	for _, v := range cfg.values(filepath.Dir(path)) {
		if flags.Changed(v.flag) {
			continue
		}

		vals := v.values
		// A slice flag takes its values comma-joined; the array flags take one Set per value.
		if flags.Lookup(v.flag).Value.Type() == "stringSlice" {
			vals = []string{strings.Join(vals, ",")}
		}

		for _, s := range vals {
			if err := flags.Set(v.flag, s); err != nil {
				return fmt.Errorf("init: %s: key %s: %w", path, v.key, err)
			}
		}
	}

	return nil
}

// checkInitRequired refuses a run in which a required setting is given neither by flag nor by the
// file. On a terminal --install-disk is not required here: init asks for it once the node can list
// its disks (chooseInstallDisk). A given --install-disk is checked now, not after the node is dialed.
func checkInitRequired(flags *pflag.FlagSet) error {
	var missing []string

	names := []string{flagCluster, flagEndpointAddress, flagNodeEndpoint}
	if !bundleTerminal() {
		names = append(names, flagInstallDisk)
	}

	for _, name := range names {
		if !flags.Changed(name) {
			missing = append(missing, fmt.Sprintf("%q (file key %s)", name, initFileKeys[name]))
		}
	}

	if len(missing) > 0 {
		return errors.New("required flag(s) " + strings.Join(missing, ", ") + " not set")
	}

	// An empty value is left to the later check, whose message it keeps.
	if v, _ := flags.GetString(flagInstallDisk); v != "" {
		if _, err := parseDiskSelector("--install-disk", v); err != nil {
			return fmt.Errorf("init: %w", err)
		}
	}

	return nil
}
