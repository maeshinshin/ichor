package main

// row says where one Talos document kind lands in Ichor's schema. The rows are the only hand-written part of
// the schema's shape: which fields a kind has, and their types, are read from the mirror, so a field Talos adds
// to a kind appears where its row puts the kind, with no edit here.
type row struct {
	kind string

	// at is the Ichor object the kind's fields join, by the names they have in the document. A field that is
	// already there with the same shape is the same setting and is written once.
	at string

	// keyed is the Ichor path of a collection of this kind, a map from the document's name to the rest of the
	// document. keyedList is the same for a kind whose order matters: a list that keeps the name in each entry.
	keyed, keyedList string

	// move sends a field of the document to a setting of another name or place, joined by the same rule.
	move map[string]string

	// skip names fields of the document that Ichor does not carry because another setting spells them.
	skip map[string]string

	// doc is Ichor's own description of the section or collection.
	doc string
}

// dropped is a setting of the v1alpha1 document that Ichor does not carry because a document spells it: the
// same setting is not written twice, and the document is the spelling Talos adds new fields to.
type dropped struct {
	path, why string
}

const (
	byDoc      = "spelled by a document of its own"
	byInverted = "spelled by a document, with the opposite sense"
)

var droppedFromConfig = []dropped{
	{"version", "Ichor writes the version of the document it converts to"},
	{"machine.kernel.modules", byDoc},
	{"machine.time.disabled", byInverted},
	{"machine.time.servers", byDoc},
	{"machine.registries.mirrors", byDoc},
	{"machine.features.imageCache", byDoc},
	{"machine.network.nameservers", byDoc},
	{"machine.network.searchDomains", byDoc},
	{"machine.network.disableSearchDomain", byDoc},
	{"machine.network.extraHostEntries", byDoc},
	{"machine.controlPlane.controllerManager", byInverted},
	{"cluster.inlineManifests", byDoc},
	{"cluster.extraManifests", byDoc},
	{"cluster.apiServer.authorizationConfig", byDoc},
	{"cluster.coreDNS.disabled", byInverted},
}

// sections describes the objects Ichor adds around the documents.
var sections = map[string]string{
	"machine.network":           "",
	"machine.volumes":           "Volumes: the system volumes, user volumes and the storage layers built on disks.",
	"machine.logging":           "",
	"machine.features.hostDNS":  "",
	"machine.network.kubespan":  "",
	"machine.registries":        "",
	"machine.udev":              "",
	"cluster.apiServer":         "",
	"machine.oom":               "The out-of-memory controller.",
	"machine.zswap":             "Compressed swap in memory.",
	"machine.watchdog":          "The hardware watchdog timer.",
	"machine.imageVerification": "Image signature verification rules.",
	"machine.logging.eventSink": "Where the machine sends its events.",
	"machine.volumes.scrub":     "When filesystems are scrubbed.",
	"machine.volumes.trim":      "When filesystems are trimmed.",
}

func coll(kind, path, doc string) row { return row{kind: kind, keyed: path, doc: doc} }

var rows = []row{
	// Settings that were a document of their own in Talos land beside the setting of the same meaning.
	{kind: "SysctlConfig", move: map[string]string{"params": "machine.sysctls"}},
	{kind: "SysfsConfig", move: map[string]string{"params": "machine.sysfs"}},
	{kind: "EnvironmentConfig", move: map[string]string{"variables": "machine.env"}},
	{kind: "UdevRulesConfig", move: map[string]string{"rules": "machine.udev.rules"}},
	coll("KernelModuleConfig", "machine.kernel.modules", "Kernel modules to load, by module name."),
	{kind: "TimeSyncConfig", at: "machine.time"},
	coll("EtcFileConfig", "machine.etcFiles", "Files written under /etc, by file name."),
	coll("TrustedRootsConfig", "machine.trustedRoots", "Extra trusted CA certificate sets, by name."),
	coll("RegistryMirrorConfig", "machine.registries.mirrors", "Registry mirrors, by registry host."),
	{kind: "ImageCacheConfig", at: "machine.features.imageCache"},
	{kind: "ImageVerificationConfig", at: "machine.imageVerification"},
	{kind: "KubePrismConfig", at: "machine.features.kubePrism"},
	{kind: "KubeTalosAPIAccessConfig", at: "machine.features.kubernetesTalosAPIAccess"},
	{kind: "ResolverConfig", at: "machine.network", move: map[string]string{"hostDNS": "machine.features.hostDNS"}},
	coll("StaticHostConfig", "machine.network.extraHostEntries", "Entries of the hosts file, by IP address."),
	coll("KmsgLogConfig", "machine.logging.kmsg", "Kernel log destinations, by name."),
	{kind: "EventSinkConfig", at: "machine.logging.eventSink"},
	{kind: "KubeSpanConfig", at: "machine.network.kubespan"},
	{kind: "KubeSpanEndpointsConfig", at: "machine.network.kubespan"},

	// Links and routing: each kind is a collection of its own, because each names a different thing.
	coll("BondConfig", "machine.network.bonds", "Bond links, by link name."),
	coll("BridgeConfig", "machine.network.bridges", "Bridge links, by link name."),
	coll("DummyLinkConfig", "machine.network.dummyLinks", "Dummy links, by link name."),
	coll("VethConfig", "machine.network.veths", "Veth pairs, by link name."),
	coll("VRFConfig", "machine.network.vrfs", "VRF links, by link name."),
	coll("EthernetConfig", "machine.network.ethernet", "Ethernet tuning of physical links, by link name."),
	coll("DHCPv6Config", "machine.network.dhcpv6", "DHCPv6 settings of links, by link name."),
	coll("Layer2VIPConfig", "machine.network.vips", "Layer 2 shared IP addresses, by address."),
	coll("RoutingRuleConfig", "machine.network.routingRules", "Policy routing rules, by name."),
	coll("BlackholeRouteConfig", "machine.network.blackholeRoutes", "Blackhole routes, by destination."),
	coll("BGPInstanceConfig", "machine.network.bgp", "BGP instances, by name."),
	coll("TCPProbeConfig", "machine.network.tcpProbes", "TCP network probes, by name."),
	coll("HTTPProbeConfig", "machine.network.httpProbes", "HTTP network probes, by name."),

	// Storage.
	coll("VolumeConfig", "machine.volumes.system", "System volumes such as STATE and EPHEMERAL, by volume name."),
	coll("UserVolumeConfig", "machine.volumes.user", "User volumes, by name."),
	coll("RawVolumeConfig", "machine.volumes.raw", "Raw volumes for a CSI provisioner, by name."),
	coll("ExistingVolumeConfig", "machine.volumes.existing", "Existing volumes to mount, by name."),
	coll("ExternalVolumeConfig", "machine.volumes.external", "Network and virtiofs volumes to mount, by name."),
	coll("SwapVolumeConfig", "machine.volumes.swap", "Swap volumes, by name."),
	coll("LVMVolumeGroupConfig", "machine.volumes.lvmGroups", "LVM volume groups, by name."),
	coll("LVMLogicalVolumeConfig", "machine.volumes.lvmVolumes", "LVM logical volumes, by name."),
	coll("RAIDArrayConfig", "machine.volumes.raid", "Software RAID arrays, by name."),
	{kind: "FilesystemScrubConfig", at: "machine.volumes.scrub"},
	{kind: "FilesystemTrimConfig", at: "machine.volumes.trim"},

	// The machine's own services.
	{kind: "OOMConfig", at: "machine.oom"},
	{kind: "ZswapConfig", at: "machine.zswap"},
	{kind: "WatchdogTimerConfig", at: "machine.watchdog"},
	coll("PCIDriverRebindConfig", "machine.pciDriverRebind", "PCI driver rebinds, by name."),
	coll("ExtensionServiceConfig", "machine.extensionServices", "Configuration of extension services, by service name."),
	coll("ContainerConfig", "machine.containers", "Containers Talos runs, by name."),
	coll("KubeStaticPodConfig", "machine.staticPods", "Static pods, by name."),

	// Kubernetes.
	{kind: "KubeletConfig", at: "machine.kubelet"},
	{kind: "KubeCredentialProviderConfig", move: map[string]string{"configuration": "machine.kubelet.credentialProviderConfig"}},
	{kind: "KubeNodeConfig", move: map[string]string{
		"skipNodeRegistration": "machine.kubelet.skipNodeRegistration",
		"registerWithFQDN":     "machine.kubelet.registerWithFQDN",
		"nodeIP":               "machine.kubelet.nodeIP",
		"labels":               "machine.nodeLabels",
		"annotations":          "machine.nodeAnnotations",
		"taints":               "machine.nodeTaints",
	}},
	{kind: "KubeClusterConfig", move: map[string]string{"clusterName": "cluster.clusterName", "endpoint": "cluster.controlPlane.endpoint"}},
	{kind: "KubeNetworkConfig", at: "cluster.network"},
	{kind: "KubeAPIServerConfig", at: "cluster.apiServer", move: map[string]string{"apiPort": "cluster.controlPlane.localAPIServerPort"}},
	{kind: "KubeAuditPolicyConfig", move: map[string]string{"configuration": "cluster.apiServer.auditPolicy"}},
	{kind: "KubeAuthorizerConfig", keyedList: "cluster.apiServer.authorizationConfig", doc: "The API server's authorizers, in the order they are consulted."},
	{kind: "KubeControllerManagerConfig", at: "cluster.controllerManager"},
	{kind: "KubeSchedulerConfig", at: "cluster.scheduler"},
	{kind: "KubeProxyConfig", at: "cluster.proxy"},
	{kind: "KubeCoreDNSConfig", at: "cluster.coreDNS"},
	coll("KubeInlineManifestConfig", "cluster.inlineManifests", "Manifests applied from the configuration itself, by name."),
	coll("KubeExternalManifestConfig", "cluster.extraManifests", "Manifests fetched from a URL, by name."),
}
