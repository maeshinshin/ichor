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

package main

// kindClass says what Ichor does with one Talos config document kind.
type kindClass string

const (
	// accept: Ichor represents the kind in TalosConfigPatch.
	accept kindClass = "accept"
	// secret: the document carries credentials, so Ichor refuses it.
	secret kindClass = "secret"
	// ichorOwned: Ichor renders the document itself from a spec field, so a
	// patch must not set it.
	ichorOwned kindClass = "ichor-owned"
	// unstructured: the document is raw YAML or text with no usable schema.
	unstructured kindClass = "unstructured"
)

type kindRow struct {
	class kindClass
	why   string
}

// kindClasses holds one row for every document kind Talos registers, keyed by
// the string it registers (see registeredKinds in kinds_test.go). A kind
// Talos adds with no row here fails TestEveryRegisteredKindIsClassified.
var kindClasses = map[string]kindRow{
	"BGPInstanceConfig":            {accept, "BGP instance settings; no credentials"},
	"BlackholeRouteConfig":         {accept, "a blackhole route; no credentials"},
	"BondConfig":                   {accept, "a bond link; no credentials"},
	"BridgeConfig":                 {accept, "a bridge link; no credentials"},
	"CRIBaseRuntimeSpecConfig":     {unstructured, "the whole payload is one free-form OCI runtime spec override"},
	"CRICustomizationConfig":       {unstructured, "the whole payload is one raw containerd config file in a string"},
	"ContainerConfig":              {accept, "a Talos-run container; environment values are the author's own"},
	"DHCPv4Config":                 {ichorOwned, "Ichor renders the DHCP setting of a link from the machine's network spec"},
	"DHCPv6Config":                 {accept, "DHCPv6 settings of a link; no credentials"},
	"DiscoveryIdentityConfig":      {secret, "carries the cluster's discovery identity (cluster ID and secret); implements SecretDocument"},
	"DiscoveryServiceConfig":       {ichorOwned, "Ichor decides the cluster's discovery service itself"},
	"DummyLinkConfig":              {accept, "a dummy link; no credentials"},
	"EnvironmentConfig":            {accept, "machine environment variables; the author's own values"},
	"EtcFileConfig":                {accept, "a file under /etc; the author's own content"},
	"EthernetConfig":               {accept, "ethernet link tuning; no credentials"},
	"EventSinkConfig":              {accept, "an event sink endpoint; no credentials"},
	"ExistingVolumeConfig":         {accept, "mounts an existing volume; no encryption block"},
	"ExtensionServiceConfig":       {accept, "configuration files and environment of an extension service; the author's own values"},
	"ExternalVolumeConfig":         {accept, "an external (network or virtiofs) volume mount; no credentials"},
	"FilesystemScrubConfig":        {accept, "filesystem scrub schedule; no credentials"},
	"FilesystemTrimConfig":         {accept, "filesystem trim schedule; no credentials"},
	"HCloudVIPConfig":              {secret, "carries the Hetzner Cloud API token; implements SecretDocument"},
	"HTTPProbeConfig":              {accept, "an HTTP network probe; no credentials"},
	"HostnameConfig":               {ichorOwned, "Ichor renders it from spec.hostname, which the controller relies on for the node and the etcd member"},
	"ImageCacheConfig":             {accept, "image cache settings; no credentials"},
	"ImageVerificationConfig":      {accept, "image signature verification rules (public keys); no credentials"},
	"KernelModuleConfig":           {accept, "kernel modules to load; no credentials"},
	"KmsgLogConfig":                {accept, "a kernel log destination; no credentials"},
	"KubeAPIServerCAConfig":        {secret, "carries the API server CA private key; implements SecretDocument"},
	"KubeAPIServerConfig":          {accept, "kube-apiserver settings; Ichor renders a baseline that a patch is merged over"},
	"KubeAdmissionControlConfig":   {unstructured, "the payload is one free-form configuration per admission plugin, so no single schema exists; Ichor writes the PodSecurity plugin itself"},
	"KubeAggregatorCAConfig":       {secret, "carries the aggregator CA private key; implements SecretDocument"},
	"KubeAuditPolicyConfig":        {accept, "audit policy; Ichor renders a baseline that a patch is merged over"},
	"KubeAuthenticationConfig":     {ichorOwned, "Ichor renders the API server's authentication configuration itself (ADR 0094); its configuration field replaces rather than merges, so a patch would drop Ichor's own issuers"},
	"KubeAuthorizerConfig":         {accept, "API server authorizer chain; no credentials"},
	"KubeClusterConfig":            {accept, "cluster name and control-plane endpoint; no credentials"},
	"KubeControllerManagerConfig":  {accept, "kube-controller-manager settings; Ichor renders a baseline that a patch is merged over"},
	"KubeCoreDNSConfig":            {accept, "CoreDNS deployment settings; no credentials"},
	"KubeCredentialProviderConfig": {accept, "kubelet image credential provider wiring; no credentials in the document"},
	"KubeEtcdEncryptionConfig":     {secret, "carries the secretbox key that encrypts Secrets at rest; implements SecretDocument"},
	"KubeExternalManifestConfig":   {accept, "a manifest fetched from a URL; its headers map is dropped like the v1alpha1 document's extraManifestHeaders (ADR 0152), so only an unauthenticated URL works"},
	"KubeFlannelCNIConfig":         {ichorOwned, "Cilium is Ichor's only CNI; a Flannel document would install a second one"},
	"KubeInlineManifestConfig":     {accept, "an inline manifest; the author's own content"},
	"KubeNetworkConfig":            {accept, "pod and service subnets and DNS domain; no credentials"},
	"KubeNodeConfig":               {accept, "node labels, taints and annotations; Ichor renders a baseline that a patch is merged over"},
	"KubePrismConfig":              {accept, "KubePrism load balancer settings; no credentials"},
	"KubeProxyConfig":              {accept, "kube-proxy settings; Ichor renders a baseline that a patch is merged over"},
	"KubeSchedulerConfig":          {accept, "kube-scheduler settings; Ichor renders a baseline that a patch is merged over"},
	"KubeServiceAccountConfig":     {secret, "carries the service account signing key; implements SecretDocument"},
	"KubeSpanConfig":               {accept, "KubeSpan settings and filters; no credentials"},
	"KubeSpanEndpointsConfig":      {accept, "extra KubeSpan endpoints; no credentials"},
	"KubeStaticPodConfig":          {accept, "a static pod; the author's own pod spec, carried whole as the core v1 PodSpec. Its CRD is larger than the client-side apply limit, so it is installed with server-side apply"},
	"KubeTalosAPIAccessConfig":     {accept, "Talos API access from Kubernetes workloads; no credentials"},
	"KubeletConfig":                {accept, "kubelet settings; Ichor renders a baseline that a patch is merged over"},
	"LVMLogicalVolumeConfig":       {accept, "an LVM logical volume; no credentials"},
	"LVMVolumeGroupConfig":         {accept, "an LVM volume group; no credentials"},
	"Layer2VIPConfig":              {accept, "a layer 2 shared IP; no credentials"},
	"LinkAliasConfig":              {ichorOwned, "Ichor renders the stable link names of a machine from its network spec"},
	"LinkConfig":                   {ichorOwned, "Ichor renders each link's addresses and routes from the machine's network spec"},
	"NetworkDefaultActionConfig":   {ichorOwned, "Ichor renders the firewall default action together with its rules"},
	"NetworkRuleConfig":            {ichorOwned, "Ichor renders the firewall rules from the cluster's security spec"},
	"OOMConfig":                    {accept, "OOM controller settings; no credentials"},
	"PCIDriverRebindConfig":        {accept, "PCI driver rebind rules; no credentials"},
	"RAIDArrayConfig":              {accept, "a software RAID array; no credentials"},
	"RawVolumeConfig":              {accept, "a raw volume for a CSI provisioner; its static encryption key is dropped like SwapVolumeConfig's"},
	"RegistryAuthConfig":           {secret, "carries registry username, password and tokens; implements SecretDocument"},
	"RegistryMirrorConfig":         {accept, "registry mirror endpoints; Ichor renders a baseline that a patch is merged over"},
	"RegistryTLSConfig":            {secret, "carries a registry client certificate and key; implements SecretDocument"},
	"ResolverConfig":               {accept, "nameservers and search domains; no credentials"},
	"RoutingRuleConfig":            {accept, "a policy routing rule; no credentials"},
	"SecurityProfileConfig":        {ichorOwned, "Ichor decides the node's workload isolation profile itself"},
	"SideroLinkConfig":             {secret, "carries the SideroLink API URL with its join token; implements SecretDocument"},
	"StaticHostConfig":             {accept, "an /etc/hosts entry; no credentials"},
	"SwapVolumeConfig":             {accept, "a swap volume; its KeyStatic field is dropped because a static disk-encryption passphrase is a credential"},
	"SysctlConfig":                 {accept, "kernel sysctls; no credentials"},
	"SysfsConfig":                  {accept, "sysfs values; no credentials"},
	"TCPProbeConfig":               {accept, "a TCP network probe; no credentials"},
	"TimeSyncConfig":               {accept, "NTP servers and time sync settings; no credentials"},
	"TrustedRootsConfig":           {accept, "extra trusted CA certificates (public); no credentials"},
	"UdevRulesConfig":              {accept, "udev rules; the author's own content"},
	"UnattendedInstallConfig":      {ichorOwned, "Ichor drives the install itself (image, disk selector, wipe) from the machine spec"},
	"UserVolumeConfig":             {accept, "a user volume; its static encryption key is dropped like SwapVolumeConfig's"},
	"VLANConfig":                   {ichorOwned, "Ichor renders VLAN links from the machine's network spec"},
	"VRFConfig":                    {accept, "a VRF link; no credentials"},
	"VethConfig":                   {accept, "a veth pair; no credentials"},
	"VolumeConfig":                 {accept, "a system volume (EPHEMERAL, STATE); its static encryption key is dropped like SwapVolumeConfig's"},
	"WatchdogTimerConfig":          {accept, "watchdog timer settings; no credentials"},
	"WireguardConfig":              {secret, "carries the WireGuard private key and preshared keys; implements SecretDocument"},
	"ZswapConfig":                  {accept, "zswap settings; no credentials"},
	"v1alpha1":                     {accept, "the legacy machine and cluster document, which Ichor names Config; its secret fields are dropped by mirrorgen"},
}
