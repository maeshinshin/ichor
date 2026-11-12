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

// The omit rows of the three subset overrides in main.go: every field of the upstream configuration type that
// Ichor does not carry, with the reason. A field of the upstream type that has no row here is carried.

const (
	// reasonProtected: Talos itself rejects the field in a kubelet configuration, so a CRD field for it could
	// only ever fail at render time, and a CRD field cannot be withdrawn once an installed build has had it.
	reasonProtected = "Talos refuses it in a kubelet configuration (kubelet.ProtectedConfigurationFields)"
	// reasonInexpressible: the type has no CRD schema (a float, an unknown quantity type, a struct field with
	// no JSON tag, a list with no list type).
	reasonInexpressible = "its type cannot be written as a CRD schema"
	// reasonUnused: no Talos or upstream documentation has an operator set it on a Talos node. It can be
	// carried later by deleting its row; a field cannot be taken back once carried.
	reasonUnused = "not among the fields a Talos operator is documented to set"
	// reasonRawExtension: the value is an arbitrary object, which a typed document must not carry.
	reasonRawExtension = "its value is free-form (a runtime.RawExtension), and a typed document does not carry free-form YAML"
)

// kubeletOmits are the fields of kubelet's KubeletConfiguration that KubeletConfig's config does not carry.
var kubeletOmits = map[string]string{
	"KubeletConfiguration.enableServer":                              reasonUnused,
	"KubeletConfiguration.staticPodPath":                             reasonProtected,
	"KubeletConfiguration.podLogsDir":                                reasonUnused,
	"KubeletConfiguration.syncFrequency":                             reasonUnused,
	"KubeletConfiguration.fileCheckFrequency":                        reasonUnused,
	"KubeletConfiguration.httpCheckFrequency":                        reasonUnused,
	"KubeletConfiguration.staticPodURL":                              reasonUnused,
	"KubeletConfiguration.staticPodURLHeader":                        reasonUnused,
	"KubeletConfiguration.address":                                   reasonUnused,
	"KubeletConfiguration.port":                                      reasonProtected,
	"KubeletConfiguration.readOnlyPort":                              reasonUnused,
	"KubeletConfiguration.tlsCertFile":                               reasonUnused,
	"KubeletConfiguration.tlsPrivateKeyFile":                         reasonUnused,
	"KubeletConfiguration.tlsCipherSuites":                           reasonUnused,
	"KubeletConfiguration.tlsCurvePreferences":                       reasonUnused,
	"KubeletConfiguration.tlsMinVersion":                             reasonUnused,
	"KubeletConfiguration.rotateCertificates":                        reasonProtected,
	"KubeletConfiguration.authentication":                            reasonProtected,
	"KubeletConfiguration.authorization":                             reasonProtected,
	"KubeletConfiguration.registryPullQPS":                           reasonUnused,
	"KubeletConfiguration.registryBurst":                             reasonUnused,
	"KubeletConfiguration.imagePullCredentialsVerificationPolicy":    reasonUnused,
	"KubeletConfiguration.preloadedImagesVerificationAllowlist":      reasonUnused,
	"KubeletConfiguration.eventRecordQPS":                            reasonUnused,
	"KubeletConfiguration.eventBurst":                                reasonUnused,
	"KubeletConfiguration.enableDebuggingHandlers":                   reasonUnused,
	"KubeletConfiguration.enableContentionProfiling":                 reasonUnused,
	"KubeletConfiguration.healthzPort":                               reasonUnused,
	"KubeletConfiguration.healthzBindAddress":                        reasonUnused,
	"KubeletConfiguration.oomScoreAdj":                               reasonUnused,
	"KubeletConfiguration.clusterDNS":                                reasonUnused,
	"KubeletConfiguration.streamingConnectionIdleTimeout":            reasonUnused,
	"KubeletConfiguration.nodeStatusUpdateFrequency":                 reasonUnused,
	"KubeletConfiguration.nodeStatusReportFrequency":                 reasonUnused,
	"KubeletConfiguration.nodeLeaseDurationSeconds":                  reasonUnused,
	"KubeletConfiguration.volumeStatsAggPeriod":                      reasonUnused,
	"KubeletConfiguration.kubeletCgroups":                            reasonProtected,
	"KubeletConfiguration.systemCgroups":                             reasonProtected,
	"KubeletConfiguration.cgroupRoot":                                reasonProtected,
	"KubeletConfiguration.cgroupsPerQOS":                             reasonUnused,
	"KubeletConfiguration.cgroupDriver":                              reasonUnused,
	"KubeletConfiguration.singleProcessOOMKill":                      reasonUnused,
	"KubeletConfiguration.qosReserved":                               reasonUnused,
	"KubeletConfiguration.runtimeRequestTimeout":                     reasonUnused,
	"KubeletConfiguration.hairpinMode":                               reasonUnused,
	"KubeletConfiguration.podCIDR":                                   reasonUnused,
	"KubeletConfiguration.resolvConf":                                reasonProtected,
	"KubeletConfiguration.runOnce":                                   reasonUnused,
	"KubeletConfiguration.cpuCFSQuota":                               reasonUnused,
	"KubeletConfiguration.cpuCFSQuotaPeriod":                         reasonUnused,
	"KubeletConfiguration.nodeStatusMaxImages":                       reasonUnused,
	"KubeletConfiguration.maxOpenFiles":                              reasonUnused,
	"KubeletConfiguration.contentType":                               reasonUnused,
	"KubeletConfiguration.kubeAPIQPS":                                reasonUnused,
	"KubeletConfiguration.kubeAPIBurst":                              reasonUnused,
	"KubeletConfiguration.podsPerCore":                               reasonUnused,
	"KubeletConfiguration.enableControllerAttachDetach":              reasonUnused,
	"KubeletConfiguration.protectKernelDefaults":                     reasonProtected,
	"KubeletConfiguration.makeIPTablesUtilChains":                    reasonUnused,
	"KubeletConfiguration.iptablesMasqueradeBit":                     reasonUnused,
	"KubeletConfiguration.iptablesDropBit":                           reasonUnused,
	"KubeletConfiguration.configMapAndSecretChangeDetectionStrategy": reasonUnused,
	"KubeletConfiguration.reservedSystemCPUs":                        reasonUnused,
	"KubeletConfiguration.showHiddenMetricsForVersion":               reasonUnused,
	"KubeletConfiguration.systemReservedCgroup":                      reasonUnused,
	"KubeletConfiguration.kubeReservedCgroup":                        reasonUnused,
	"KubeletConfiguration.enforceNodeAllocatable":                    reasonUnused,
	"KubeletConfiguration.volumePluginDir":                           reasonUnused,
	"KubeletConfiguration.providerID":                                reasonUnused,
	"KubeletConfiguration.kernelMemcgNotification":                   reasonUnused,
	"KubeletConfiguration.logging":                                   reasonInexpressible,
	"KubeletConfiguration.enableSystemLogHandler":                    reasonUnused,
	"KubeletConfiguration.enableSystemLogQuery":                      reasonUnused,
	"KubeletConfiguration.crashLoopBackOff":                          reasonUnused,
	"KubeletConfiguration.enableProfilingHandler":                    reasonUnused,
	"KubeletConfiguration.enableDebugFlagsHandler":                   reasonUnused,
	"KubeletConfiguration.seccompDefault":                            reasonProtected,
	"KubeletConfiguration.memoryThrottlingFactor":                    reasonInexpressible,
	"KubeletConfiguration.memoryReservationPolicy":                   reasonUnused,
	"KubeletConfiguration.registerNode":                              reasonUnused,
	"KubeletConfiguration.localStorageCapacityIsolation":             reasonUnused,
	"KubeletConfiguration.containerRuntimeEndpoint":                  reasonUnused,
	"KubeletConfiguration.imageServiceEndpoint":                      reasonUnused,
	"KubeletConfiguration.failCgroupV1":                              reasonUnused,
}

// kubeProxyOmits are the fields of KubeProxyConfiguration that KubeProxyConfig's config does not carry.
var kubeProxyOmits = map[string]string{
	"KubeProxyConfiguration.clientConnection":            reasonInexpressible,
	"KubeProxyConfiguration.logging":                     reasonInexpressible,
	"KubeProxyConfiguration.hostnameOverride":            reasonUnused,
	"KubeProxyConfiguration.bindAddress":                 reasonUnused,
	"KubeProxyConfiguration.healthzBindAddress":          reasonUnused,
	"KubeProxyConfiguration.enableProfiling":             reasonUnused,
	"KubeProxyConfiguration.showHiddenMetricsForVersion": reasonUnused,
	"KubeProxyConfiguration.mode":                        reasonUnused,
	"KubeProxyConfiguration.winkernel":                   reasonUnused,
	"KubeProxyConfiguration.configSyncPeriod":            reasonUnused,
	"KubeProxyConfiguration.portRange":                   reasonUnused,
	"KubeProxyConfiguration.windowsRunAsService":         reasonUnused,
}

// kubeSchedulerOmits are the fields of KubeSchedulerConfiguration, and of its profiles, that
// KubeSchedulerConfig's config does not carry. Without pluginConfig the arguments of a scheduler plugin, such
// as a scoring strategy, cannot be set.
var kubeSchedulerOmits = map[string]string{
	"KubeSchedulerConfiguration.parallelism":              reasonUnused,
	"KubeSchedulerConfiguration.leaderElection":           reasonInexpressible,
	"KubeSchedulerConfiguration.clientConnection":         reasonInexpressible,
	"KubeSchedulerConfiguration.podInitialBackoffSeconds": reasonUnused,
	"KubeSchedulerConfiguration.podMaxBackoffSeconds":     reasonUnused,
	"KubeSchedulerConfiguration.extenders":                reasonInexpressible,
	"KubeSchedulerConfiguration.delayCacheUntilActive":    reasonUnused,
	"KubeSchedulerProfile.pluginConfig":                   reasonRawExtension,
	// Embedded inline, so it has no JSON name of its own: the row is the embedded type's name.
	"KubeSchedulerConfiguration.DebuggingConfiguration": reasonUnused,
}
