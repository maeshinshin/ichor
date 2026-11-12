---
title: "TalosConfigPatch"
description: "TalosConfigPatch is the Schema for the talosconfigpatches API"
---

# TalosConfigPatch

This page is generated from the CRD schema in `config/crd/bases` (`make docs-reference`). Do not edit it.

TalosConfigPatch is the Schema for the talosconfigpatches API

| | |
|---|---|
| Group | `ichor.maesh.dev` |
| Kind | `TalosConfigPatch` (list kind `TalosConfigPatchList`) |
| Resource | `talosconfigpatches` |
| Short names | `tcp` |
| Scope | Namespaced |

## v1alpha1

Version v1alpha1: has a `status` subresource.

### spec

| Field | Type | Required | Description |
|---|---|---|---|
| `spec` | `object` | yes | spec defines the desired state of TalosConfigPatch |
| `spec.clusterRef` | `object` | yes | clusterRef names the TalosCluster this patch applies to. |
| `spec.clusterRef.name` | `string` | no | Name of the referent. This field is effectively required, but due to backwards compatibility is allowed to be empty. Instances of this type with an empty value here are almost certainly wrong. More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#names Default: `""`. |
| `spec.documents` | `[]object` | no | documents holds the Talos config documents this patch applies, restricted to an allow-list of kinds. |
| `spec.documents[].config` | `object` | no | config is a Talos v1alpha1 Config document; populated when kind is "Config". |
| `spec.documents[].config.cluster` | `object` | no | Talos: ClusterConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.acceptedCAs` | `[]object` | no |  |
| `spec.documents[].config.cluster.acceptedCAs[].crt` | `string (byte)` | yes |  |
| `spec.documents[].config.cluster.adminKubeconfig` | `object` | no | Talos: AdminKubeconfigConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.adminKubeconfig.certLifetime` | `string` | no |  |
| `spec.documents[].config.cluster.allowSchedulingOnControlPlanes` | `boolean` | no |  |
| `spec.documents[].config.cluster.allowSchedulingOnMasters` | `boolean` | no |  |
| `spec.documents[].config.cluster.apiServer` | `object` | no | Talos: APIServerConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.apiServer.admissionControl` | `[]object` | no |  |
| `spec.documents[].config.cluster.apiServer.admissionControl[].name` | `string` | yes |  |
| `spec.documents[].config.cluster.apiServer.auditPolicy` | `object` | no | Policy defines the configuration of audit logging, and the rules for how different request categories are logged. |
| `spec.documents[].config.cluster.apiServer.auditPolicy.apiVersion` | `string` | no | APIVersion defines the versioned schema of this representation of an object. Servers should convert recognized schemas to the latest internal value, and may reject unrecognized values. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |
| `spec.documents[].config.cluster.apiServer.auditPolicy.kind` | `string` | no | Kind is a string value representing the REST resource this object represents. Servers may infer this from the endpoint the client submits requests to. Cannot be updated. In CamelCase. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |
| `spec.documents[].config.cluster.apiServer.auditPolicy.metadata` | `object` | no | ObjectMeta is included for interoperability with API infrastructure. |
| `spec.documents[].config.cluster.apiServer.auditPolicy.omitManagedFields` | `boolean` | no | OmitManagedFields indicates whether to omit the managed fields of the request and response bodies from being written to the API audit log. This is used as a global default - a value of 'true' will omit the managed fileds, otherwise the managed fields will be included in the API audit log. Note that this can also be specified per rule in which case the value specified in a rule will override the global default. |
| `spec.documents[].config.cluster.apiServer.auditPolicy.omitStages` | `[]string` | no | OmitStages is a list of stages for which no events are created. Note that this can also be specified per rule in which case the union of both are omitted. |
| `spec.documents[].config.cluster.apiServer.auditPolicy.rules` | `[]object` | yes | Rules specify the audit Level a request should be recorded at. A request may match multiple rules, in which case the FIRST matching rule is used. The default audit level is None, but can be overridden by a catch-all rule at the end of the list. PolicyRules are strictly ordered. |
| `spec.documents[].config.cluster.apiServer.auditPolicy.rules[].level` | `string` | yes | The Level that requests matching this rule are recorded at. |
| `spec.documents[].config.cluster.apiServer.auditPolicy.rules[].namespaces` | `[]string` | no | Namespaces that this rule matches. The empty string "" matches non-namespaced resources. An empty list implies every namespace. |
| `spec.documents[].config.cluster.apiServer.auditPolicy.rules[].nonResourceURLs` | `[]string` | no | NonResourceURLs is a set of URL paths that should be audited. `*`s are allowed, but only as the full, final step in the path. Examples: - `/metrics` - Log requests for apiserver metrics - `/healthz*` - Log all health checks |
| `spec.documents[].config.cluster.apiServer.auditPolicy.rules[].omitManagedFields` | `boolean` | no | OmitManagedFields indicates whether to omit the managed fields of the request and response bodies from being written to the API audit log. - a value of 'true' will drop the managed fields from the API audit log - a value of 'false' indicates that the managed fileds should be included in the API audit log Note that the value, if specified, in this rule will override the global default If a value is not specified then the global default specified in Policy.OmitManagedFields will stand. |
| `spec.documents[].config.cluster.apiServer.auditPolicy.rules[].omitStages` | `[]string` | no | OmitStages is a list of stages for which no events are created. Note that this can also be specified policy wide in which case the union of both are omitted. An empty list means no restrictions will apply. |
| `spec.documents[].config.cluster.apiServer.auditPolicy.rules[].resources` | `[]object` | no | Resources that this rule matches. An empty list implies all kinds in all API groups. |
| `spec.documents[].config.cluster.apiServer.auditPolicy.rules[].resources[].group` | `string` | no | Group is the name of the API group that contains the resources. The empty string represents the core API group. `*` matches all groups |
| `spec.documents[].config.cluster.apiServer.auditPolicy.rules[].resources[].resourceNames` | `[]string` | no | ResourceNames is a list of resource instance names that the policy matches. Using this field requires Resources to be specified. An empty list implies that every instance of the resource is matched. |
| `spec.documents[].config.cluster.apiServer.auditPolicy.rules[].resources[].resources` | `[]string` | no | Resources is a list of resources this rule applies to. For example: - `pods` matches pods. - `pods/log` matches the log subresource of pods. - `*` matches all resources and their subresources. - `pods/*` matches all subresources of pods. - `*/scale` matches all scale subresources. If wildcard is present, the validation rule will ensure resources do not overlap with each other. An empty list implies all resources and subresources in this API groups apply. |
| `spec.documents[].config.cluster.apiServer.auditPolicy.rules[].userGroups` | `[]string` | no | The user groups this rule applies to. A user is considered matching if it is a member of any of the UserGroups. An empty list implies every user group. |
| `spec.documents[].config.cluster.apiServer.auditPolicy.rules[].users` | `[]string` | no | The users (by authenticated user name) this rule applies to. An empty list implies every user. |
| `spec.documents[].config.cluster.apiServer.auditPolicy.rules[].verbs` | `[]string` | no | The verbs that match this rule. An empty list implies every verb. |
| `spec.documents[].config.cluster.apiServer.authorizationConfig` | `[]object` | no |  |
| `spec.documents[].config.cluster.apiServer.authorizationConfig[].name` | `string` | yes |  |
| `spec.documents[].config.cluster.apiServer.authorizationConfig[].type` | `string` | yes |  |
| `spec.documents[].config.cluster.apiServer.authorizationConfig[].webhook` | `object` | no |  |
| `spec.documents[].config.cluster.apiServer.authorizationConfig[].webhook.authorizedTTL` | `string` | yes | The duration to cache 'authorized' responses from the webhook authorizer. Same as setting `--authorization-webhook-cache-authorized-ttl` flag Default: 5m0s |
| `spec.documents[].config.cluster.apiServer.authorizationConfig[].webhook.cacheAuthorizedRequests` | `boolean` | no | CacheAuthorizedRequests specifies whether authorized requests should be cached. If set to true, the TTL for cached decisions can be configured via the AuthorizedTTL field. Default: true |
| `spec.documents[].config.cluster.apiServer.authorizationConfig[].webhook.cacheUnauthorizedRequests` | `boolean` | no | CacheUnauthorizedRequests specifies whether unauthorized requests should be cached. If set to true, the TTL for cached decisions can be configured via the UnauthorizedTTL field. Default: true |
| `spec.documents[].config.cluster.apiServer.authorizationConfig[].webhook.connectionInfo` | `object` | yes | ConnectionInfo defines how we talk to the webhook |
| `spec.documents[].config.cluster.apiServer.authorizationConfig[].webhook.connectionInfo.kubeConfigFile` | `string` | yes | Path to KubeConfigFile for connection info Required, if connectionInfo.Type is KubeConfig |
| `spec.documents[].config.cluster.apiServer.authorizationConfig[].webhook.connectionInfo.type` | `string` | yes | Controls how the webhook should communicate with the server. Valid values: - KubeConfigFile: use the file specified in kubeConfigFile to locate the server. - InClusterConfig: use the in-cluster configuration to call the SubjectAccessReview API hosted by kube-apiserver. This mode is not allowed for kube-apiserver. |
| `spec.documents[].config.cluster.apiServer.authorizationConfig[].webhook.failurePolicy` | `string` | yes | Controls the authorization decision when a webhook request fails to complete or returns a malformed response or errors evaluating matchConditions. Valid values: - NoOpinion: continue to subsequent authorizers to see if one of them allows the request - Deny: reject the request without consulting subsequent authorizers Required, with no default. |
| `spec.documents[].config.cluster.apiServer.authorizationConfig[].webhook.matchConditionSubjectAccessReviewVersion` | `string` | yes | MatchConditionSubjectAccessReviewVersion specifies the SubjectAccessReview version the CEL expressions are evaluated against Valid values: v1 Required, no default value |
| `spec.documents[].config.cluster.apiServer.authorizationConfig[].webhook.matchConditions` | `[]object` | yes | matchConditions is a list of conditions that must be met for a request to be sent to this webhook. An empty list of matchConditions matches all requests. There are a maximum of 64 match conditions allowed. The exact matching logic is (in order): 1. If at least one matchCondition evaluates to FALSE, then the webhook is skipped. 2. If ALL matchConditions evaluate to TRUE, then the webhook is called. 3. If at least one matchCondition evaluates to an error (but none are FALSE): - If failurePolicy=Deny, then the webhook rejects the request - If failurePolicy=NoOpinion, then the error is ignored and the webhook is skipped |
| `spec.documents[].config.cluster.apiServer.authorizationConfig[].webhook.matchConditions[].expression` | `string` | yes | expression represents the expression which will be evaluated by CEL. Must evaluate to bool. CEL expressions have access to the contents of the SubjectAccessReview in v1 version. If version specified by subjectAccessReviewVersion in the request variable is v1beta1, the contents would be converted to the v1 version before evaluating the CEL expression. - 'resourceAttributes' describes information for a resource access request and is unset for non-resource requests. e.g. has(request.resourceAttributes) && request.resourceAttributes.namespace == 'default' - 'nonResourceAttributes' describes information for a non-resource access request and is unset for resource requests. e.g. has(request.nonResourceAttributes) && request.nonResourceAttributes.path == '/healthz'. - 'user' is the user to test for. e.g. request.user == 'alice' - 'groups' is the groups to test for. e.g. ('group1' in request.groups) - 'extra' corresponds to the user.Info.GetExtra() method from the authenticator. - 'uid' is the information about the requesting user. e.g. request.uid == '1' Documentation on CEL: https://kubernetes.io/docs/reference/using-api/cel/ |
| `spec.documents[].config.cluster.apiServer.authorizationConfig[].webhook.subjectAccessReviewVersion` | `string` | yes | The API version of the authorization.k8s.io SubjectAccessReview to send to and expect from the webhook. Same as setting `--authorization-webhook-version` flag Valid values: v1beta1, v1 Required, no default value |
| `spec.documents[].config.cluster.apiServer.authorizationConfig[].webhook.timeout` | `string` | yes | Timeout for the webhook request Maximum allowed value is 30s. Required, no default value. |
| `spec.documents[].config.cluster.apiServer.authorizationConfig[].webhook.unauthorizedTTL` | `string` | yes | The duration to cache 'unauthorized' responses from the webhook authorizer. Same as setting `--authorization-webhook-cache-unauthorized-ttl` flag Default: 30s |
| `spec.documents[].config.cluster.apiServer.certSANs` | `[]string` | no |  |
| `spec.documents[].config.cluster.apiServer.disablePodSecurityPolicy` | `boolean` | no |  |
| `spec.documents[].config.cluster.apiServer.env` | `map[string]string` | no |  |
| `spec.documents[].config.cluster.apiServer.extraArgs` | `map[string][]string` | no |  |
| `spec.documents[].config.cluster.apiServer.extraVolumes` | `[]object` | no |  |
| `spec.documents[].config.cluster.apiServer.extraVolumes[].hostPath` | `string` | yes |  |
| `spec.documents[].config.cluster.apiServer.extraVolumes[].mountPath` | `string` | yes |  |
| `spec.documents[].config.cluster.apiServer.extraVolumes[].readonly` | `boolean` | no |  |
| `spec.documents[].config.cluster.apiServer.image` | `string` | no |  |
| `spec.documents[].config.cluster.apiServer.resources` | `object` | no | Talos: ResourcesConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.apiServer.resources.limits` | `map[string]integer or string` | no | ResourceList is a set of (resource name, quantity) pairs. |
| `spec.documents[].config.cluster.apiServer.resources.requests` | `map[string]integer or string` | no | ResourceList is a set of (resource name, quantity) pairs. |
| `spec.documents[].config.cluster.clusterName` | `string` | no |  |
| `spec.documents[].config.cluster.controlPlane` | `object` | no | Talos: ControlPlaneConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.controlPlane.endpoint` | `string` | yes |  |
| `spec.documents[].config.cluster.controlPlane.localAPIServerPort` | `integer` | no |  |
| `spec.documents[].config.cluster.controllerManager` | `object` | no | Talos: ControllerManagerConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.controllerManager.env` | `map[string]string` | no |  |
| `spec.documents[].config.cluster.controllerManager.extraArgs` | `map[string][]string` | no |  |
| `spec.documents[].config.cluster.controllerManager.extraVolumes` | `[]object` | no |  |
| `spec.documents[].config.cluster.controllerManager.extraVolumes[].hostPath` | `string` | yes |  |
| `spec.documents[].config.cluster.controllerManager.extraVolumes[].mountPath` | `string` | yes |  |
| `spec.documents[].config.cluster.controllerManager.extraVolumes[].readonly` | `boolean` | no |  |
| `spec.documents[].config.cluster.controllerManager.image` | `string` | no |  |
| `spec.documents[].config.cluster.controllerManager.resources` | `object` | no | Talos: ResourcesConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.controllerManager.resources.limits` | `map[string]integer or string` | no | ResourceList is a set of (resource name, quantity) pairs. |
| `spec.documents[].config.cluster.controllerManager.resources.requests` | `map[string]integer or string` | no | ResourceList is a set of (resource name, quantity) pairs. |
| `spec.documents[].config.cluster.coreDNS` | `object` | no | Talos: CoreDNS — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.coreDNS.disabled` | `boolean` | no |  |
| `spec.documents[].config.cluster.coreDNS.image` | `string` | no |  |
| `spec.documents[].config.cluster.discovery` | `object` | no | Talos: ClusterDiscoveryConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.discovery.enabled` | `boolean` | no |  |
| `spec.documents[].config.cluster.discovery.registries` | `object` | yes | Talos: DiscoveryRegistriesConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.discovery.registries.kubernetes` | `object` | yes | Talos: RegistryKubernetesConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.discovery.registries.kubernetes.disabled` | `boolean` | no |  |
| `spec.documents[].config.cluster.discovery.registries.service` | `object` | yes | Talos: RegistryServiceConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.discovery.registries.service.disabled` | `boolean` | no |  |
| `spec.documents[].config.cluster.discovery.registries.service.endpoint` | `string` | no |  |
| `spec.documents[].config.cluster.etcd` | `object` | no | Talos: EtcdConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.etcd.advertisedSubnets` | `[]string` | no |  |
| `spec.documents[].config.cluster.etcd.extraArgs` | `map[string][]string` | no |  |
| `spec.documents[].config.cluster.etcd.image` | `string` | no |  |
| `spec.documents[].config.cluster.etcd.listenSubnets` | `[]string` | no |  |
| `spec.documents[].config.cluster.etcd.subnet` | `string` | no |  |
| `spec.documents[].config.cluster.externalCloudProvider` | `object` | no | Talos: ExternalCloudProviderConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.externalCloudProvider.enabled` | `boolean` | no |  |
| `spec.documents[].config.cluster.externalCloudProvider.manifests` | `[]string` | no |  |
| `spec.documents[].config.cluster.extraManifests` | `[]string` | no |  |
| `spec.documents[].config.cluster.id` | `string` | no |  |
| `spec.documents[].config.cluster.inlineManifests` | `[]object` | no |  |
| `spec.documents[].config.cluster.inlineManifests[].contents` | `string` | yes |  |
| `spec.documents[].config.cluster.inlineManifests[].name` | `string` | yes |  |
| `spec.documents[].config.cluster.network` | `object` | no | Talos: ClusterNetworkConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.network.cni` | `object` | no | Talos: CNIConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.network.cni.flannel` | `object` | no | Talos: FlannelCNIConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.network.cni.flannel.extraArgs` | `[]string` | no |  |
| `spec.documents[].config.cluster.network.cni.flannel.kubeNetworkPoliciesEnabled` | `boolean` | no |  |
| `spec.documents[].config.cluster.network.cni.name` | `string` | no |  |
| `spec.documents[].config.cluster.network.cni.urls` | `[]string` | no |  |
| `spec.documents[].config.cluster.network.dnsDomain` | `string` | yes |  |
| `spec.documents[].config.cluster.network.podSubnets` | `[]string` | yes |  |
| `spec.documents[].config.cluster.network.serviceSubnets` | `[]string` | yes |  |
| `spec.documents[].config.cluster.proxy` | `object` | no | Talos: ProxyConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.proxy.disabled` | `boolean` | no |  |
| `spec.documents[].config.cluster.proxy.extraArgs` | `map[string][]string` | no |  |
| `spec.documents[].config.cluster.proxy.image` | `string` | no |  |
| `spec.documents[].config.cluster.proxy.mode` | `string` | no |  |
| `spec.documents[].config.cluster.scheduler` | `object` | no | Talos: SchedulerConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.scheduler.env` | `map[string]string` | no |  |
| `spec.documents[].config.cluster.scheduler.extraArgs` | `map[string][]string` | no |  |
| `spec.documents[].config.cluster.scheduler.extraVolumes` | `[]object` | no |  |
| `spec.documents[].config.cluster.scheduler.extraVolumes[].hostPath` | `string` | yes |  |
| `spec.documents[].config.cluster.scheduler.extraVolumes[].mountPath` | `string` | yes |  |
| `spec.documents[].config.cluster.scheduler.extraVolumes[].readonly` | `boolean` | no |  |
| `spec.documents[].config.cluster.scheduler.image` | `string` | no |  |
| `spec.documents[].config.cluster.scheduler.resources` | `object` | no | Talos: ResourcesConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.scheduler.resources.limits` | `map[string]integer or string` | no | ResourceList is a set of (resource name, quantity) pairs. |
| `spec.documents[].config.cluster.scheduler.resources.requests` | `map[string]integer or string` | no | ResourceList is a set of (resource name, quantity) pairs. |
| `spec.documents[].config.debug` | `boolean` | no |  |
| `spec.documents[].config.machine` | `object` | no | Talos: MachineConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.acceptedCAs` | `[]object` | no |  |
| `spec.documents[].config.machine.acceptedCAs[].crt` | `string (byte)` | yes |  |
| `spec.documents[].config.machine.certSANs` | `[]string` | no |  |
| `spec.documents[].config.machine.controlPlane` | `object` | no | Talos: MachineControlPlaneConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.controlPlane.controllerManager` | `object` | no | Talos: MachineControllerManagerConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.controlPlane.controllerManager.disabled` | `boolean` | no |  |
| `spec.documents[].config.machine.controlPlane.scheduler` | `object` | no | Talos: MachineSchedulerConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.controlPlane.scheduler.disabled` | `boolean` | no |  |
| `spec.documents[].config.machine.disks` | `[]object` | no |  |
| `spec.documents[].config.machine.disks[].device` | `string` | no |  |
| `spec.documents[].config.machine.disks[].partitions` | `[]object` | no |  |
| `spec.documents[].config.machine.disks[].partitions[].mountpoint` | `string` | no |  |
| `spec.documents[].config.machine.disks[].partitions[].size` | `string` | no |  |
| `spec.documents[].config.machine.env` | `map[string]string` | no |  |
| `spec.documents[].config.machine.features` | `object` | no | Talos: FeaturesConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.features.apidCheckExtKeyUsage` | `boolean` | no |  |
| `spec.documents[].config.machine.features.diskQuotaSupport` | `boolean` | no |  |
| `spec.documents[].config.machine.features.hostDNS` | `object` | no | Talos: HostDNSConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.features.hostDNS.enabled` | `boolean` | no |  |
| `spec.documents[].config.machine.features.hostDNS.forwardKubeDNSToHost` | `boolean` | no |  |
| `spec.documents[].config.machine.features.hostDNS.resolveMemberNames` | `boolean` | no |  |
| `spec.documents[].config.machine.features.imageCache` | `object` | no | Talos: ImageCacheConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.features.imageCache.localEnabled` | `boolean` | no |  |
| `spec.documents[].config.machine.features.kubePrism` | `object` | no | Talos: KubePrism — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.features.kubePrism.enabled` | `boolean` | no |  |
| `spec.documents[].config.machine.features.kubePrism.port` | `integer` | no |  |
| `spec.documents[].config.machine.features.kubernetesTalosAPIAccess` | `object` | no | Talos: KubernetesTalosAPIAccessConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.features.kubernetesTalosAPIAccess.allowedKubernetesNamespaces` | `[]string` | no |  |
| `spec.documents[].config.machine.features.kubernetesTalosAPIAccess.allowedRoles` | `[]string` | no |  |
| `spec.documents[].config.machine.features.kubernetesTalosAPIAccess.enabled` | `boolean` | no |  |
| `spec.documents[].config.machine.features.nodeAddressSortAlgorithm` | `string` | no |  |
| `spec.documents[].config.machine.features.rbac` | `boolean` | no |  |
| `spec.documents[].config.machine.features.stableHostname` | `boolean` | no |  |
| `spec.documents[].config.machine.files` | `[]object` | no |  |
| `spec.documents[].config.machine.files[].content` | `string` | yes |  |
| `spec.documents[].config.machine.files[].op` | `string` | yes |  |
| `spec.documents[].config.machine.files[].path` | `string` | yes |  |
| `spec.documents[].config.machine.files[].permissions` | `string` | yes |  |
| `spec.documents[].config.machine.install` | `object` | no | Talos: InstallConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.install.bootloader` | `boolean` | no |  |
| `spec.documents[].config.machine.install.disk` | `string` | no |  |
| `spec.documents[].config.machine.install.diskSelector` | `object` | no | Talos: InstallDiskSelector — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.install.diskSelector.busPath` | `string` | no |  |
| `spec.documents[].config.machine.install.diskSelector.modalias` | `string` | no |  |
| `spec.documents[].config.machine.install.diskSelector.model` | `string` | no |  |
| `spec.documents[].config.machine.install.diskSelector.name` | `string` | no |  |
| `spec.documents[].config.machine.install.diskSelector.serial` | `string` | no |  |
| `spec.documents[].config.machine.install.diskSelector.size` | `string` | no |  |
| `spec.documents[].config.machine.install.diskSelector.type` | `string` | no |  |
| `spec.documents[].config.machine.install.diskSelector.uuid` | `string` | no |  |
| `spec.documents[].config.machine.install.diskSelector.wwid` | `string` | no |  |
| `spec.documents[].config.machine.install.extensions` | `[]object` | no |  |
| `spec.documents[].config.machine.install.extensions[].image` | `string` | yes |  |
| `spec.documents[].config.machine.install.extraKernelArgs` | `[]string` | no |  |
| `spec.documents[].config.machine.install.grubUseUKICmdline` | `boolean` | no |  |
| `spec.documents[].config.machine.install.image` | `string` | no |  |
| `spec.documents[].config.machine.install.legacyBIOSSupport` | `boolean` | no |  |
| `spec.documents[].config.machine.install.wipe` | `boolean` | yes |  |
| `spec.documents[].config.machine.kernel` | `object` | no | Talos: KernelConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.kernel.modules` | `[]object` | no |  |
| `spec.documents[].config.machine.kernel.modules[].name` | `string` | yes |  |
| `spec.documents[].config.machine.kernel.modules[].parameters` | `[]string` | no |  |
| `spec.documents[].config.machine.kubelet` | `object` | no | Talos: KubeletConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.kubelet.clusterDNS` | `[]string` | no |  |
| `spec.documents[].config.machine.kubelet.credentialProviderConfig` | `object` | no | CredentialProviderConfig is the configuration containing information about each exec credential provider. Kubelet reads this configuration from disk and enables each provider as specified by the CredentialProvider type. |
| `spec.documents[].config.machine.kubelet.credentialProviderConfig.apiVersion` | `string` | no | APIVersion defines the versioned schema of this representation of an object. Servers should convert recognized schemas to the latest internal value, and may reject unrecognized values. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |
| `spec.documents[].config.machine.kubelet.credentialProviderConfig.kind` | `string` | no | Kind is a string value representing the REST resource this object represents. Servers may infer this from the endpoint the client submits requests to. Cannot be updated. In CamelCase. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |
| `spec.documents[].config.machine.kubelet.credentialProviderConfig.providers` | `[]object` | yes | providers is a list of credential provider plugins that will be enabled by the kubelet. Multiple providers may match against a single image, in which case credentials from all providers will be returned to the kubelet. If multiple providers are called for a single image, the results are combined. If providers return overlapping auth keys, the value from the provider earlier in this list is attempted first. |
| `spec.documents[].config.machine.kubelet.credentialProviderConfig.providers[].apiVersion` | `string` | yes | Required input version of the exec CredentialProviderRequest. The returned CredentialProviderResponse MUST use the same encoding version as the input. Current supported values are: - credentialprovider.kubelet.k8s.io/v1 |
| `spec.documents[].config.machine.kubelet.credentialProviderConfig.providers[].args` | `[]string` | no | Arguments to pass to the command when executing it. |
| `spec.documents[].config.machine.kubelet.credentialProviderConfig.providers[].defaultCacheDuration` | `string` | yes | defaultCacheDuration is the default duration the plugin will cache credentials in-memory if a cache duration is not provided in the plugin response. This field is required. |
| `spec.documents[].config.machine.kubelet.credentialProviderConfig.providers[].env` | `[]object` | no | Env defines additional environment variables to expose to the process. These are unioned with the host's environment, as well as variables client-go uses to pass argument to the plugin. |
| `spec.documents[].config.machine.kubelet.credentialProviderConfig.providers[].env[].name` | `string` | yes |  |
| `spec.documents[].config.machine.kubelet.credentialProviderConfig.providers[].env[].value` | `string` | yes |  |
| `spec.documents[].config.machine.kubelet.credentialProviderConfig.providers[].matchImages` | `[]string` | yes | matchImages is a required list of strings used to match against images in order to determine if this provider should be invoked. If one of the strings matches the requested image from the kubelet, the plugin will be invoked and given a chance to provide credentials. Images are expected to contain the registry domain and URL path. Each entry in matchImages is a pattern which can optionally contain a port and a path. Globs can be used in the domain, but not in the port or the path. Globs are supported as subdomains like '*.k8s.io' or 'k8s.*.io', and top-level-domains such as 'k8s.*'. Matching partial subdomains like 'app*.k8s.io' is also supported. Each glob can only match a single subdomain segment, so *.io does not match *.k8s.io. A match exists between an image and a matchImage when all of the below are true: - Both contain the same number of domain parts and each part matches. - The URL path of an imageMatch must be a prefix of the target image URL path. - If the imageMatch contains a port, then the port must match in the image as well. Example values of matchImages: - 123456789.dkr.ecr.us-east-1.amazonaws.com - *.azurecr.io - gcr.io - *.*.registry.io - registry.io:8080/path |
| `spec.documents[].config.machine.kubelet.credentialProviderConfig.providers[].name` | `string` | yes | name is the required name of the credential provider. It must match the name of the provider executable as seen by the kubelet. The executable must be in the kubelet's bin directory (set by the --image-credential-provider-bin-dir flag). Required to be unique across all providers. |
| `spec.documents[].config.machine.kubelet.credentialProviderConfig.providers[].tokenAttributes` | `object` | no | tokenAttributes is the configuration for the service account token that will be passed to the plugin. The credential provider opts in to using service account tokens for image pull by setting this field. When this field is set, kubelet will generate a service account token bound to the pod for which the image is being pulled and pass to the plugin as part of CredentialProviderRequest along with other attributes required by the plugin. The service account metadata and token attributes will be used as a dimension to cache the credentials in kubelet. The cache key is generated by combining the service account metadata (namespace, name, UID, and annotations key+value for the keys defined in serviceAccountTokenAttribute.requiredServiceAccountAnnotationKeys and serviceAccountTokenAttribute.optionalServiceAccountAnnotationKeys). The pod metadata (namespace, name, UID) that are in the service account token are not used as a dimension to cache the credentials in kubelet. This means workloads that are using the same service account could end up using the same credentials for image pull. For plugins that don't want this behavior, or plugins that operate in pass-through mode; i.e., they return the service account token as-is, they can set the credentialProviderResponse.cacheDuration to 0. This will disable the caching of credentials in kubelet and the plugin will be invoked for every image pull. This does result in token generation overhead for every image pull, but it is the only way to ensure that the credentials are not shared across pods (even if they are using the same service account). |
| `spec.documents[].config.machine.kubelet.credentialProviderConfig.providers[].tokenAttributes.cacheType` | `string` | yes | cacheType indicates the type of cache key use for caching the credentials returned by the plugin when the service account token is used. The most conservative option is to set this to "Token", which means the kubelet will cache returned credentials on a per-token basis. This should be set if the returned credential's lifetime is limited to the service account token's lifetime. If the plugin's credential retrieval logic depends only on the service account and not on pod-specific claims, then the plugin can set this to "ServiceAccount". In this case, the kubelet will cache returned credentials on a per-serviceaccount basis. Use this when the returned credential is valid for all pods using the same service account. |
| `spec.documents[].config.machine.kubelet.credentialProviderConfig.providers[].tokenAttributes.optionalServiceAccountAnnotationKeys` | `[]string` | no | optionalServiceAccountAnnotationKeys is the list of annotation keys that the plugin is interested in and that are optional to be present in the service account. The keys defined in this list will be extracted from the corresponding service account and passed to the plugin as part of the CredentialProviderRequest. The plugin is responsible for validating the existence of annotations and their values. This field is optional and may be empty. Plugins may use this field to extract additional information required to fetch credentials. Keys in this list must be unique. |
| `spec.documents[].config.machine.kubelet.credentialProviderConfig.providers[].tokenAttributes.requireServiceAccount` | `boolean` | yes | requireServiceAccount indicates whether the plugin requires the pod to have a service account. If set to true, kubelet will only invoke the plugin if the pod has a service account. If set to false, kubelet will invoke the plugin even if the pod does not have a service account and will not include a token in the CredentialProviderRequest in that scenario. This is useful for plugins that are used to pull images for pods without service accounts (e.g., static pods). |
| `spec.documents[].config.machine.kubelet.credentialProviderConfig.providers[].tokenAttributes.requiredServiceAccountAnnotationKeys` | `[]string` | no | requiredServiceAccountAnnotationKeys is the list of annotation keys that the plugin is interested in and that are required to be present in the service account. The keys defined in this list will be extracted from the corresponding service account and passed to the plugin as part of the CredentialProviderRequest. If any of the keys defined in this list are not present in the service account, kubelet will not invoke the plugin and will return an error. This field is optional and may be empty. Plugins may use this field to extract additional information required to fetch credentials or allow workloads to opt in to using service account tokens for image pull. If non-empty, requireServiceAccount must be set to true. Keys in this list must be unique. This list needs to be mutually exclusive with optionalServiceAccountAnnotationKeys. |
| `spec.documents[].config.machine.kubelet.credentialProviderConfig.providers[].tokenAttributes.serviceAccountTokenAudience` | `string` | yes | serviceAccountTokenAudience is the intended audience for the projected service account token. |
| `spec.documents[].config.machine.kubelet.defaultRuntimeSeccompProfileEnabled` | `boolean` | no |  |
| `spec.documents[].config.machine.kubelet.disableManifestsDirectory` | `boolean` | no |  |
| `spec.documents[].config.machine.kubelet.extraArgs` | `map[string][]string` | no |  |
| `spec.documents[].config.machine.kubelet.extraMounts` | `[]object` | no |  |
| `spec.documents[].config.machine.kubelet.extraMounts[].destination` | `string` | yes |  |
| `spec.documents[].config.machine.kubelet.extraMounts[].gidMappings` | `[]object` | no |  |
| `spec.documents[].config.machine.kubelet.extraMounts[].gidMappings[].containerID` | `integer` | yes |  |
| `spec.documents[].config.machine.kubelet.extraMounts[].gidMappings[].hostID` | `integer` | yes |  |
| `spec.documents[].config.machine.kubelet.extraMounts[].gidMappings[].size` | `integer` | yes |  |
| `spec.documents[].config.machine.kubelet.extraMounts[].options` | `[]string` | no |  |
| `spec.documents[].config.machine.kubelet.extraMounts[].source` | `string` | no |  |
| `spec.documents[].config.machine.kubelet.extraMounts[].type` | `string` | no |  |
| `spec.documents[].config.machine.kubelet.extraMounts[].uidMappings` | `[]object` | no |  |
| `spec.documents[].config.machine.kubelet.extraMounts[].uidMappings[].containerID` | `integer` | yes |  |
| `spec.documents[].config.machine.kubelet.extraMounts[].uidMappings[].hostID` | `integer` | yes |  |
| `spec.documents[].config.machine.kubelet.extraMounts[].uidMappings[].size` | `integer` | yes |  |
| `spec.documents[].config.machine.kubelet.image` | `string` | no |  |
| `spec.documents[].config.machine.kubelet.nodeIP` | `object` | no | Talos: KubeletNodeIPConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.kubelet.nodeIP.validSubnets` | `[]string` | no |  |
| `spec.documents[].config.machine.kubelet.registerWithFQDN` | `boolean` | no |  |
| `spec.documents[].config.machine.kubelet.skipNodeRegistration` | `boolean` | no |  |
| `spec.documents[].config.machine.logging` | `object` | no | Talos: LoggingConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.logging.destinations` | `[]object` | yes |  |
| `spec.documents[].config.machine.logging.destinations[].endpoint` | `string` | yes |  |
| `spec.documents[].config.machine.logging.destinations[].extraTags` | `map[string]string` | no |  |
| `spec.documents[].config.machine.logging.destinations[].format` | `string` | yes |  |
| `spec.documents[].config.machine.network` | `object` | no | Talos: NetworkConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.disableSearchDomain` | `boolean` | no |  |
| `spec.documents[].config.machine.network.extraHostEntries` | `[]object` | no |  |
| `spec.documents[].config.machine.network.extraHostEntries[].aliases` | `[]string` | yes |  |
| `spec.documents[].config.machine.network.extraHostEntries[].ip` | `string` | yes |  |
| `spec.documents[].config.machine.network.hostname` | `string` | no |  |
| `spec.documents[].config.machine.network.interfaces` | `[]object` | no |  |
| `spec.documents[].config.machine.network.interfaces[].addresses` | `[]string` | no |  |
| `spec.documents[].config.machine.network.interfaces[].bond` | `object` | no | Talos: Bond — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bond.adActorSysPrio` | `integer` | no |  |
| `spec.documents[].config.machine.network.interfaces[].bond.adActorSystem` | `string` | no |  |
| `spec.documents[].config.machine.network.interfaces[].bond.adSelect` | `string` | no |  |
| `spec.documents[].config.machine.network.interfaces[].bond.adUserPortKey` | `integer` | no |  |
| `spec.documents[].config.machine.network.interfaces[].bond.allSlavesActive` | `integer` | no |  |
| `spec.documents[].config.machine.network.interfaces[].bond.arpAllTargets` | `string` | no |  |
| `spec.documents[].config.machine.network.interfaces[].bond.arpIPTarget` | `[]string` | no |  |
| `spec.documents[].config.machine.network.interfaces[].bond.arpInterval` | `integer` | no |  |
| `spec.documents[].config.machine.network.interfaces[].bond.arpValidate` | `string` | no |  |
| `spec.documents[].config.machine.network.interfaces[].bond.deviceSelectors` | `[]object` | no |  |
| `spec.documents[].config.machine.network.interfaces[].bond.deviceSelectors[].busPath` | `string` | no |  |
| `spec.documents[].config.machine.network.interfaces[].bond.deviceSelectors[].driver` | `string` | no |  |
| `spec.documents[].config.machine.network.interfaces[].bond.deviceSelectors[].hardwareAddr` | `string` | no |  |
| `spec.documents[].config.machine.network.interfaces[].bond.deviceSelectors[].pciID` | `string` | no |  |
| `spec.documents[].config.machine.network.interfaces[].bond.deviceSelectors[].permanentAddr` | `string` | no |  |
| `spec.documents[].config.machine.network.interfaces[].bond.deviceSelectors[].physical` | `boolean` | no |  |
| `spec.documents[].config.machine.network.interfaces[].bond.downdelay` | `integer` | no |  |
| `spec.documents[].config.machine.network.interfaces[].bond.failOverMac` | `string` | no |  |
| `spec.documents[].config.machine.network.interfaces[].bond.interfaces` | `[]string` | yes |  |
| `spec.documents[].config.machine.network.interfaces[].bond.lacpRate` | `string` | no |  |
| `spec.documents[].config.machine.network.interfaces[].bond.lpInterval` | `integer` | no |  |
| `spec.documents[].config.machine.network.interfaces[].bond.miimon` | `integer` | no |  |
| `spec.documents[].config.machine.network.interfaces[].bond.minLinks` | `integer` | no |  |
| `spec.documents[].config.machine.network.interfaces[].bond.mode` | `string` | yes |  |
| `spec.documents[].config.machine.network.interfaces[].bond.numPeerNotif` | `integer` | no |  |
| `spec.documents[].config.machine.network.interfaces[].bond.packetsPerSlave` | `integer` | no |  |
| `spec.documents[].config.machine.network.interfaces[].bond.peerNotifyDelay` | `integer` | no |  |
| `spec.documents[].config.machine.network.interfaces[].bond.primary` | `string` | no |  |
| `spec.documents[].config.machine.network.interfaces[].bond.primaryReselect` | `string` | no |  |
| `spec.documents[].config.machine.network.interfaces[].bond.resendIgmp` | `integer` | no |  |
| `spec.documents[].config.machine.network.interfaces[].bond.tlbDynamicLb` | `integer` | no |  |
| `spec.documents[].config.machine.network.interfaces[].bond.updelay` | `integer` | no |  |
| `spec.documents[].config.machine.network.interfaces[].bond.useCarrier` | `boolean` | no |  |
| `spec.documents[].config.machine.network.interfaces[].bond.xmitHashPolicy` | `string` | no |  |
| `spec.documents[].config.machine.network.interfaces[].bridge` | `object` | no | Talos: Bridge — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bridge.interfaces` | `[]string` | yes |  |
| `spec.documents[].config.machine.network.interfaces[].bridge.stp` | `object` | no | Talos: STP — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bridge.stp.enabled` | `boolean` | no |  |
| `spec.documents[].config.machine.network.interfaces[].bridge.vlan` | `object` | no | Talos: BridgeVLAN — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bridge.vlan.vlanFiltering` | `boolean` | no |  |
| `spec.documents[].config.machine.network.interfaces[].bridgePort` | `object` | no | Talos: BridgePort — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bridgePort.master` | `string` | no |  |
| `spec.documents[].config.machine.network.interfaces[].cidr` | `string` | no |  |
| `spec.documents[].config.machine.network.interfaces[].deviceSelector` | `object` | no | Talos: NetworkDeviceSelector — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].deviceSelector.busPath` | `string` | no |  |
| `spec.documents[].config.machine.network.interfaces[].deviceSelector.driver` | `string` | no |  |
| `spec.documents[].config.machine.network.interfaces[].deviceSelector.hardwareAddr` | `string` | no |  |
| `spec.documents[].config.machine.network.interfaces[].deviceSelector.pciID` | `string` | no |  |
| `spec.documents[].config.machine.network.interfaces[].deviceSelector.permanentAddr` | `string` | no |  |
| `spec.documents[].config.machine.network.interfaces[].deviceSelector.physical` | `boolean` | no |  |
| `spec.documents[].config.machine.network.interfaces[].dhcp` | `boolean` | no |  |
| `spec.documents[].config.machine.network.interfaces[].dhcpOptions` | `object` | no | Talos: DHCPOptions — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].dhcpOptions.duidv6` | `string` | no |  |
| `spec.documents[].config.machine.network.interfaces[].dhcpOptions.ipv4` | `boolean` | no |  |
| `spec.documents[].config.machine.network.interfaces[].dhcpOptions.ipv6` | `boolean` | no |  |
| `spec.documents[].config.machine.network.interfaces[].dhcpOptions.routeMetric` | `integer` | yes |  |
| `spec.documents[].config.machine.network.interfaces[].dummy` | `boolean` | no |  |
| `spec.documents[].config.machine.network.interfaces[].ignore` | `boolean` | no |  |
| `spec.documents[].config.machine.network.interfaces[].interface` | `string` | no |  |
| `spec.documents[].config.machine.network.interfaces[].mtu` | `integer` | no |  |
| `spec.documents[].config.machine.network.interfaces[].routes` | `[]object` | no |  |
| `spec.documents[].config.machine.network.interfaces[].routes[].gateway` | `string` | yes |  |
| `spec.documents[].config.machine.network.interfaces[].routes[].metric` | `integer` | no |  |
| `spec.documents[].config.machine.network.interfaces[].routes[].mtu` | `integer` | no |  |
| `spec.documents[].config.machine.network.interfaces[].routes[].network` | `string` | yes |  |
| `spec.documents[].config.machine.network.interfaces[].routes[].source` | `string` | no |  |
| `spec.documents[].config.machine.network.interfaces[].vip` | `object` | no | Talos: DeviceVIPConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].vip.equinixMetal` | `object` | no | Talos: VIPEquinixMetalConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].vip.hcloud` | `object` | no | Talos: VIPHCloudConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].vip.ip` | `string` | no |  |
| `spec.documents[].config.machine.network.interfaces[].vlans` | `[]object` | no |  |
| `spec.documents[].config.machine.network.interfaces[].vlans[].addresses` | `[]string` | no |  |
| `spec.documents[].config.machine.network.interfaces[].vlans[].cidr` | `string` | no |  |
| `spec.documents[].config.machine.network.interfaces[].vlans[].dhcp` | `boolean` | no |  |
| `spec.documents[].config.machine.network.interfaces[].vlans[].dhcpOptions` | `object` | no | Talos: DHCPOptions — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].vlans[].dhcpOptions.duidv6` | `string` | no |  |
| `spec.documents[].config.machine.network.interfaces[].vlans[].dhcpOptions.ipv4` | `boolean` | no |  |
| `spec.documents[].config.machine.network.interfaces[].vlans[].dhcpOptions.ipv6` | `boolean` | no |  |
| `spec.documents[].config.machine.network.interfaces[].vlans[].dhcpOptions.routeMetric` | `integer` | yes |  |
| `spec.documents[].config.machine.network.interfaces[].vlans[].mtu` | `integer` | no |  |
| `spec.documents[].config.machine.network.interfaces[].vlans[].routes` | `[]object` | yes |  |
| `spec.documents[].config.machine.network.interfaces[].vlans[].routes[].gateway` | `string` | yes |  |
| `spec.documents[].config.machine.network.interfaces[].vlans[].routes[].metric` | `integer` | no |  |
| `spec.documents[].config.machine.network.interfaces[].vlans[].routes[].mtu` | `integer` | no |  |
| `spec.documents[].config.machine.network.interfaces[].vlans[].routes[].network` | `string` | yes |  |
| `spec.documents[].config.machine.network.interfaces[].vlans[].routes[].source` | `string` | no |  |
| `spec.documents[].config.machine.network.interfaces[].vlans[].vip` | `object` | no | Talos: DeviceVIPConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].vlans[].vip.equinixMetal` | `object` | no | Talos: VIPEquinixMetalConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].vlans[].vip.hcloud` | `object` | no | Talos: VIPHCloudConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].vlans[].vip.ip` | `string` | no |  |
| `spec.documents[].config.machine.network.interfaces[].vlans[].vlanId` | `integer` | yes |  |
| `spec.documents[].config.machine.network.interfaces[].wireguard` | `object` | no | Talos: DeviceWireguardConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].wireguard.firewallMark` | `integer` | no |  |
| `spec.documents[].config.machine.network.interfaces[].wireguard.listenPort` | `integer` | no |  |
| `spec.documents[].config.machine.network.interfaces[].wireguard.peers` | `[]object` | no |  |
| `spec.documents[].config.machine.network.interfaces[].wireguard.peers[].allowedIPs` | `[]string` | no |  |
| `spec.documents[].config.machine.network.interfaces[].wireguard.peers[].endpoint` | `string` | no |  |
| `spec.documents[].config.machine.network.interfaces[].wireguard.peers[].persistentKeepaliveInterval` | `string` | no |  |
| `spec.documents[].config.machine.network.interfaces[].wireguard.peers[].publicKey` | `string` | no |  |
| `spec.documents[].config.machine.network.kubespan` | `object` | no | Talos: NetworkKubeSpan — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.kubespan.advertiseKubernetesNetworks` | `boolean` | no |  |
| `spec.documents[].config.machine.network.kubespan.allowDownPeerBypass` | `boolean` | no |  |
| `spec.documents[].config.machine.network.kubespan.enabled` | `boolean` | no |  |
| `spec.documents[].config.machine.network.kubespan.filters` | `object` | no | Talos: KubeSpanFilters — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.kubespan.filters.endpoints` | `[]string` | no |  |
| `spec.documents[].config.machine.network.kubespan.filters.excludeAdvertisedNetworks` | `[]string` | no |  |
| `spec.documents[].config.machine.network.kubespan.harvestExtraEndpoints` | `boolean` | no |  |
| `spec.documents[].config.machine.network.kubespan.mtu` | `integer` | no |  |
| `spec.documents[].config.machine.network.nameservers` | `[]string` | no |  |
| `spec.documents[].config.machine.network.searchDomains` | `[]string` | no |  |
| `spec.documents[].config.machine.nodeAnnotations` | `map[string]string` | no |  |
| `spec.documents[].config.machine.nodeLabels` | `map[string]string` | no |  |
| `spec.documents[].config.machine.nodeTaints` | `map[string]string` | no |  |
| `spec.documents[].config.machine.registries` | `object` | no | Talos: RegistriesConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.registries.config` | `map[string]object` | no |  |
| `spec.documents[].config.machine.registries.config.<key>.auth` | `object` | no | Talos: RegistryAuthConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.registries.config.<key>.auth.username` | `string` | no |  |
| `spec.documents[].config.machine.registries.config.<key>.tls` | `object` | no | Talos: RegistryTLSConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.registries.config.<key>.tls.ca` | `string (byte)` | no |  |
| `spec.documents[].config.machine.registries.config.<key>.tls.insecureSkipVerify` | `boolean` | no |  |
| `spec.documents[].config.machine.registries.mirrors` | `map[string]object` | no |  |
| `spec.documents[].config.machine.registries.mirrors.<key>.endpoints` | `[]string` | yes |  |
| `spec.documents[].config.machine.registries.mirrors.<key>.overridePath` | `boolean` | no |  |
| `spec.documents[].config.machine.registries.mirrors.<key>.skipFallback` | `boolean` | no |  |
| `spec.documents[].config.machine.seccompProfiles` | `[]object` | no |  |
| `spec.documents[].config.machine.seccompProfiles[].name` | `string` | yes |  |
| `spec.documents[].config.machine.seccompProfiles[].value` | `object` | yes | SeccompProfile is MachineSeccompProfile.MachineSeccompProfileValue's mirror type (internal/tools/mirrorgen/main.go's fieldOverrides): byte-for-byte the same fields and JSON tags as specs.LinuxSeccomp (github.com/opencontainers/ runtime-spec), under a local name so DeepCopyInto/DeepCopy can be hand-written below. The generic conversion helpers in substitution.go and the generated CRD schema are unaffected by the rename -- both work off the JSON shape, which is identical. Why hand-written: specs.LinuxSeccomp has pointer and slice fields but, unlike the other four field overrides (all Kubernetes API types), no DeepCopy of its own; controller-gen's object:generate step assumes one exists for any struct-shaped field type it doesn't otherwise recognize, and errors at compile time rather than generation time when it doesn't. The methods can't live on specs.LinuxSeccomp directly -- Go forbids methods on types from another package -- and can't live one level up, on MachineSeccompProfile itself, either: MachineConfig holds MachineSeccompProfiles []*MachineSeccompProfile, and controller-gen's generated code for a slice of pointers to a type with a *known* DeepCopy method assumes a value-typed element and double-dereferences, which doesn't compile. Putting the hand-written methods here, one level down, avoids both problems: SeccompProfile is reached only through a plain pointer field, never through a pointer slice. deepCopyLinuxSeccomp and deepCopyLinuxSyscall below copy their known pointer/slice fields by name rather than reflecting over the upstream type, so they go stale -- silently, by sharing instead of copying -- if specs-go ever adds one. seccomp_test.go's TestLinuxSeccompShape guards against that by pinning the exact field set each one assumes. |
| `spec.documents[].config.machine.seccompProfiles[].value.architectures` | `[]string` | no |  |
| `spec.documents[].config.machine.seccompProfiles[].value.defaultAction` | `string` | yes | LinuxSeccompAction taken upon Seccomp rule match |
| `spec.documents[].config.machine.seccompProfiles[].value.defaultErrnoRet` | `integer` | no |  |
| `spec.documents[].config.machine.seccompProfiles[].value.flags` | `[]string` | no |  |
| `spec.documents[].config.machine.seccompProfiles[].value.listenerMetadata` | `string` | no |  |
| `spec.documents[].config.machine.seccompProfiles[].value.listenerPath` | `string` | no |  |
| `spec.documents[].config.machine.seccompProfiles[].value.syscalls` | `[]object` | no |  |
| `spec.documents[].config.machine.seccompProfiles[].value.syscalls[].action` | `string` | yes | LinuxSeccompAction taken upon Seccomp rule match |
| `spec.documents[].config.machine.seccompProfiles[].value.syscalls[].args` | `[]object` | no |  |
| `spec.documents[].config.machine.seccompProfiles[].value.syscalls[].args[].index` | `integer` | yes |  |
| `spec.documents[].config.machine.seccompProfiles[].value.syscalls[].args[].op` | `string` | yes | LinuxSeccompOperator used to match syscall arguments in Seccomp |
| `spec.documents[].config.machine.seccompProfiles[].value.syscalls[].args[].value` | `integer` | yes |  |
| `spec.documents[].config.machine.seccompProfiles[].value.syscalls[].args[].valueTwo` | `integer` | no |  |
| `spec.documents[].config.machine.seccompProfiles[].value.syscalls[].errnoRet` | `integer` | no |  |
| `spec.documents[].config.machine.seccompProfiles[].value.syscalls[].names` | `[]string` | yes |  |
| `spec.documents[].config.machine.sysctls` | `map[string]string` | no |  |
| `spec.documents[].config.machine.sysfs` | `map[string]string` | no |  |
| `spec.documents[].config.machine.systemDiskEncryption` | `object` | no | Talos: SystemDiskEncryptionConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.systemDiskEncryption.ephemeral` | `object` | no | Talos: EncryptionConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.systemDiskEncryption.ephemeral.blockSize` | `integer` | no |  |
| `spec.documents[].config.machine.systemDiskEncryption.ephemeral.cipher` | `string` | no |  |
| `spec.documents[].config.machine.systemDiskEncryption.ephemeral.keySize` | `integer` | no |  |
| `spec.documents[].config.machine.systemDiskEncryption.ephemeral.keys` | `[]object` | yes |  |
| `spec.documents[].config.machine.systemDiskEncryption.ephemeral.keys[].kms` | `object` | no | Talos: EncryptionKeyKMS — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.systemDiskEncryption.ephemeral.keys[].kms.endpoint` | `string` | yes |  |
| `spec.documents[].config.machine.systemDiskEncryption.ephemeral.keys[].nodeID` | `object` | no | Talos: EncryptionKeyNodeID — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.systemDiskEncryption.ephemeral.keys[].slot` | `integer` | yes |  |
| `spec.documents[].config.machine.systemDiskEncryption.ephemeral.keys[].tpm` | `object` | no | Talos: EncryptionKeyTPM — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.systemDiskEncryption.ephemeral.keys[].tpm.checkSecurebootStatusOnEnroll` | `boolean` | no |  |
| `spec.documents[].config.machine.systemDiskEncryption.ephemeral.options` | `[]string` | no |  |
| `spec.documents[].config.machine.systemDiskEncryption.ephemeral.provider` | `string` | yes |  |
| `spec.documents[].config.machine.systemDiskEncryption.state` | `object` | no | Talos: EncryptionConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.systemDiskEncryption.state.blockSize` | `integer` | no |  |
| `spec.documents[].config.machine.systemDiskEncryption.state.cipher` | `string` | no |  |
| `spec.documents[].config.machine.systemDiskEncryption.state.keySize` | `integer` | no |  |
| `spec.documents[].config.machine.systemDiskEncryption.state.keys` | `[]object` | yes |  |
| `spec.documents[].config.machine.systemDiskEncryption.state.keys[].kms` | `object` | no | Talos: EncryptionKeyKMS — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.systemDiskEncryption.state.keys[].kms.endpoint` | `string` | yes |  |
| `spec.documents[].config.machine.systemDiskEncryption.state.keys[].nodeID` | `object` | no | Talos: EncryptionKeyNodeID — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.systemDiskEncryption.state.keys[].slot` | `integer` | yes |  |
| `spec.documents[].config.machine.systemDiskEncryption.state.keys[].tpm` | `object` | no | Talos: EncryptionKeyTPM — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.systemDiskEncryption.state.keys[].tpm.checkSecurebootStatusOnEnroll` | `boolean` | no |  |
| `spec.documents[].config.machine.systemDiskEncryption.state.options` | `[]string` | no |  |
| `spec.documents[].config.machine.systemDiskEncryption.state.provider` | `string` | yes |  |
| `spec.documents[].config.machine.time` | `object` | no | Talos: TimeConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.time.bootTimeout` | `string` | no |  |
| `spec.documents[].config.machine.time.disabled` | `boolean` | no |  |
| `spec.documents[].config.machine.time.servers` | `[]string` | no |  |
| `spec.documents[].config.machine.type` | `string` | no |  |
| `spec.documents[].config.machine.udev` | `object` | no | Talos: UdevConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.udev.rules` | `[]string` | no |  |
| `spec.documents[].config.persist` | `boolean` | no |  |
| `spec.documents[].config.version` | `string` | yes |  |
| `spec.documents[].kernelModuleConfig` | `object` | no | kernelModuleConfig is a Talos KernelModuleConfig document; populated when kind is "KernelModuleConfig". |
| `spec.documents[].kernelModuleConfig.name` | `string` | yes |  |
| `spec.documents[].kernelModuleConfig.parameters` | `[]string` | no |  |
| `spec.documents[].kind` | `string` | yes | kind selects which of the fields below is populated. Allowed values: `SysctlConfig`, `KernelModuleConfig`, `Config`, `VolumeConfig`, `UserVolumeConfig`, `RawVolumeConfig`, `ResolverConfig`. |
| `spec.documents[].rawVolumeConfig` | `object` | no | rawVolumeConfig is a Talos RawVolumeConfig document (an unformatted partition for a CSI provisioner); populated when kind is "RawVolumeConfig". |
| `spec.documents[].rawVolumeConfig.encryption` | `object` | no | Talos: EncryptionSpec |
| `spec.documents[].rawVolumeConfig.encryption.allowDiscards` | `boolean` | no |  |
| `spec.documents[].rawVolumeConfig.encryption.blockSize` | `integer` | no |  |
| `spec.documents[].rawVolumeConfig.encryption.cipher` | `string` | no |  |
| `spec.documents[].rawVolumeConfig.encryption.keySize` | `integer` | no |  |
| `spec.documents[].rawVolumeConfig.encryption.keys` | `[]object` | yes |  |
| `spec.documents[].rawVolumeConfig.encryption.keys[].kms` | `object` | no | Talos: EncryptionKeyKMS |
| `spec.documents[].rawVolumeConfig.encryption.keys[].kms.endpoint` | `string` | yes |  |
| `spec.documents[].rawVolumeConfig.encryption.keys[].lockToState` | `boolean` | no |  |
| `spec.documents[].rawVolumeConfig.encryption.keys[].nodeID` | `object` | no | Talos: EncryptionKeyNodeID |
| `spec.documents[].rawVolumeConfig.encryption.keys[].slot` | `integer` | yes |  |
| `spec.documents[].rawVolumeConfig.encryption.keys[].tpm` | `object` | no | Talos: EncryptionKeyTPM |
| `spec.documents[].rawVolumeConfig.encryption.keys[].tpm.checkSecurebootStatusOnEnroll` | `boolean` | no |  |
| `spec.documents[].rawVolumeConfig.encryption.keys[].tpm.options` | `object` | no | Talos: EncryptionKeyTPMOptions |
| `spec.documents[].rawVolumeConfig.encryption.keys[].tpm.options.pcrs` | `[]integer` | no |  |
| `spec.documents[].rawVolumeConfig.encryption.options` | `[]string` | no |  |
| `spec.documents[].rawVolumeConfig.encryption.provider` | `string` | yes |  |
| `spec.documents[].rawVolumeConfig.name` | `string` | yes |  |
| `spec.documents[].rawVolumeConfig.provisioning` | `object` | no | Talos: ProvisioningSpec |
| `spec.documents[].rawVolumeConfig.provisioning.diskSelector` | `object` | no | Talos: DiskSelector |
| `spec.documents[].rawVolumeConfig.provisioning.diskSelector.match` | `string` | no |  |
| `spec.documents[].rawVolumeConfig.provisioning.grow` | `boolean` | no |  |
| `spec.documents[].rawVolumeConfig.provisioning.maxSize` | `string` | no |  |
| `spec.documents[].rawVolumeConfig.provisioning.minSize` | `string` | no |  |
| `spec.documents[].resolverConfig` | `object` | no | resolverConfig is a Talos ResolverConfig document (DNS nameservers and search domains); populated when kind is "ResolverConfig". |
| `spec.documents[].resolverConfig.hostDNS` | `object` | no | Talos: HostDNSConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/network/resolverconfig |
| `spec.documents[].resolverConfig.hostDNS.enabled` | `boolean` | no |  |
| `spec.documents[].resolverConfig.hostDNS.forwardKubeDNSToHost` | `boolean` | no |  |
| `spec.documents[].resolverConfig.hostDNS.resolveMemberNames` | `boolean` | no |  |
| `spec.documents[].resolverConfig.nameservers` | `[]object` | no |  |
| `spec.documents[].resolverConfig.nameservers[].address` | `string` | yes |  |
| `spec.documents[].resolverConfig.nameservers[].protocol` | `string` | no |  |
| `spec.documents[].resolverConfig.nameservers[].tlsServerName` | `string` | no |  |
| `spec.documents[].resolverConfig.searchDomains` | `object` | no | Talos: SearchDomainsConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/network/resolverconfig |
| `spec.documents[].resolverConfig.searchDomains.disableDefault` | `boolean` | no |  |
| `spec.documents[].resolverConfig.searchDomains.domains` | `[]string` | no |  |
| `spec.documents[].sysctlConfig` | `object` | no | sysctlConfig is a Talos SysctlConfig document; populated when kind is "SysctlConfig". |
| `spec.documents[].sysctlConfig.params` | `map[string]string` | yes |  |
| `spec.documents[].userVolumeConfig` | `object` | no | userVolumeConfig is a Talos UserVolumeConfig document; populated when kind is "UserVolumeConfig". |
| `spec.documents[].userVolumeConfig.encryption` | `object` | no | Talos: EncryptionSpec |
| `spec.documents[].userVolumeConfig.encryption.allowDiscards` | `boolean` | no |  |
| `spec.documents[].userVolumeConfig.encryption.blockSize` | `integer` | no |  |
| `spec.documents[].userVolumeConfig.encryption.cipher` | `string` | no |  |
| `spec.documents[].userVolumeConfig.encryption.keySize` | `integer` | no |  |
| `spec.documents[].userVolumeConfig.encryption.keys` | `[]object` | yes |  |
| `spec.documents[].userVolumeConfig.encryption.keys[].kms` | `object` | no | Talos: EncryptionKeyKMS |
| `spec.documents[].userVolumeConfig.encryption.keys[].kms.endpoint` | `string` | yes |  |
| `spec.documents[].userVolumeConfig.encryption.keys[].lockToState` | `boolean` | no |  |
| `spec.documents[].userVolumeConfig.encryption.keys[].nodeID` | `object` | no | Talos: EncryptionKeyNodeID |
| `spec.documents[].userVolumeConfig.encryption.keys[].slot` | `integer` | yes |  |
| `spec.documents[].userVolumeConfig.encryption.keys[].tpm` | `object` | no | Talos: EncryptionKeyTPM |
| `spec.documents[].userVolumeConfig.encryption.keys[].tpm.checkSecurebootStatusOnEnroll` | `boolean` | no |  |
| `spec.documents[].userVolumeConfig.encryption.keys[].tpm.options` | `object` | no | Talos: EncryptionKeyTPMOptions |
| `spec.documents[].userVolumeConfig.encryption.keys[].tpm.options.pcrs` | `[]integer` | no |  |
| `spec.documents[].userVolumeConfig.encryption.options` | `[]string` | no |  |
| `spec.documents[].userVolumeConfig.encryption.provider` | `string` | yes |  |
| `spec.documents[].userVolumeConfig.filesystem` | `object` | no | Talos: FilesystemSpec — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/uservolumeconfig |
| `spec.documents[].userVolumeConfig.filesystem.projectQuotaSupport` | `boolean` | no |  |
| `spec.documents[].userVolumeConfig.filesystem.type` | `string` | no |  |
| `spec.documents[].userVolumeConfig.filesystem.xfs` | `object` | no | Talos: XFSSpec |
| `spec.documents[].userVolumeConfig.filesystem.xfs.minAllocationGroupSize` | `string` | no |  |
| `spec.documents[].userVolumeConfig.mount` | `object` | no | Talos: UserMountSpec — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/uservolumeconfig |
| `spec.documents[].userVolumeConfig.mount.disableAccessTime` | `boolean` | no |  |
| `spec.documents[].userVolumeConfig.mount.secure` | `boolean` | no |  |
| `spec.documents[].userVolumeConfig.name` | `string` | yes |  |
| `spec.documents[].userVolumeConfig.provisioning` | `object` | no | Talos: ProvisioningSpec |
| `spec.documents[].userVolumeConfig.provisioning.diskSelector` | `object` | no | Talos: DiskSelector |
| `spec.documents[].userVolumeConfig.provisioning.diskSelector.match` | `string` | no |  |
| `spec.documents[].userVolumeConfig.provisioning.grow` | `boolean` | no |  |
| `spec.documents[].userVolumeConfig.provisioning.maxSize` | `string` | no |  |
| `spec.documents[].userVolumeConfig.provisioning.minSize` | `string` | no |  |
| `spec.documents[].userVolumeConfig.scrub` | `object` | no | Talos: ScrubConfig |
| `spec.documents[].userVolumeConfig.scrub.enabled` | `boolean` | no |  |
| `spec.documents[].userVolumeConfig.scrub.interval` | `string` | no |  |
| `spec.documents[].userVolumeConfig.trim` | `object` | no | Talos: TrimConfig |
| `spec.documents[].userVolumeConfig.trim.enabled` | `boolean` | no |  |
| `spec.documents[].userVolumeConfig.trim.interval` | `string` | no |  |
| `spec.documents[].userVolumeConfig.volumeType` | `string` | no |  |
| `spec.documents[].volumeConfig` | `object` | no | volumeConfig is a Talos VolumeConfig document (a system volume, e.g. STATE or EPHEMERAL, placed on its own disk); populated when kind is "VolumeConfig". |
| `spec.documents[].volumeConfig.encryption` | `object` | no | Talos: EncryptionSpec |
| `spec.documents[].volumeConfig.encryption.allowDiscards` | `boolean` | no |  |
| `spec.documents[].volumeConfig.encryption.blockSize` | `integer` | no |  |
| `spec.documents[].volumeConfig.encryption.cipher` | `string` | no |  |
| `spec.documents[].volumeConfig.encryption.keySize` | `integer` | no |  |
| `spec.documents[].volumeConfig.encryption.keys` | `[]object` | yes |  |
| `spec.documents[].volumeConfig.encryption.keys[].kms` | `object` | no | Talos: EncryptionKeyKMS |
| `spec.documents[].volumeConfig.encryption.keys[].kms.endpoint` | `string` | yes |  |
| `spec.documents[].volumeConfig.encryption.keys[].lockToState` | `boolean` | no |  |
| `spec.documents[].volumeConfig.encryption.keys[].nodeID` | `object` | no | Talos: EncryptionKeyNodeID |
| `spec.documents[].volumeConfig.encryption.keys[].slot` | `integer` | yes |  |
| `spec.documents[].volumeConfig.encryption.keys[].tpm` | `object` | no | Talos: EncryptionKeyTPM |
| `spec.documents[].volumeConfig.encryption.keys[].tpm.checkSecurebootStatusOnEnroll` | `boolean` | no |  |
| `spec.documents[].volumeConfig.encryption.keys[].tpm.options` | `object` | no | Talos: EncryptionKeyTPMOptions |
| `spec.documents[].volumeConfig.encryption.keys[].tpm.options.pcrs` | `[]integer` | no |  |
| `spec.documents[].volumeConfig.encryption.options` | `[]string` | no |  |
| `spec.documents[].volumeConfig.encryption.provider` | `string` | yes |  |
| `spec.documents[].volumeConfig.filesystem` | `object` | no | Talos: SystemVolumeFilesystemSpec — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/volumeconfig |
| `spec.documents[].volumeConfig.filesystem.xfs` | `object` | no | Talos: XFSSpec |
| `spec.documents[].volumeConfig.filesystem.xfs.minAllocationGroupSize` | `string` | no |  |
| `spec.documents[].volumeConfig.mount` | `object` | no | Talos: MountSpec — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/volumeconfig |
| `spec.documents[].volumeConfig.mount.disableAccessTime` | `boolean` | no |  |
| `spec.documents[].volumeConfig.mount.secure` | `boolean` | no |  |
| `spec.documents[].volumeConfig.name` | `string` | yes |  |
| `spec.documents[].volumeConfig.provisioning` | `object` | no | Talos: ProvisioningSpec |
| `spec.documents[].volumeConfig.provisioning.diskSelector` | `object` | no | Talos: DiskSelector |
| `spec.documents[].volumeConfig.provisioning.diskSelector.match` | `string` | no |  |
| `spec.documents[].volumeConfig.provisioning.grow` | `boolean` | no |  |
| `spec.documents[].volumeConfig.provisioning.maxSize` | `string` | no |  |
| `spec.documents[].volumeConfig.provisioning.minSize` | `string` | no |  |
| `spec.documents[].volumeConfig.scrub` | `object` | no | Talos: ScrubConfig |
| `spec.documents[].volumeConfig.scrub.enabled` | `boolean` | no |  |
| `spec.documents[].volumeConfig.scrub.interval` | `string` | no |  |
| `spec.documents[].volumeConfig.trim` | `object` | no | Talos: TrimConfig |
| `spec.documents[].volumeConfig.trim.enabled` | `boolean` | no |  |
| `spec.documents[].volumeConfig.trim.interval` | `string` | no |  |
| `spec.layer` | `string` | yes | layer is where this patch applies in the bottom-up stack; "system" is absent because Ichor generates that layer itself. Allowed values: `cluster`, `role`, `selector`, `machine`. |
| `spec.priority` | `integer` | no | priority orders patches within the same layer. Default: `0`. |
| `spec.target` | `object` | no | target narrows which machines this patch applies to; required for every layer but "cluster", and its kind follows the layer: role -> role, selector -> labelSelector, machine -> machineRef. |
| `spec.target.labelSelector` | `object` | no | labelSelector targets machines whose Kubernetes Node labels match. |
| `spec.target.labelSelector.matchExpressions` | `[]object` | no | matchExpressions is a list of label selector requirements. The requirements are ANDed. |
| `spec.target.labelSelector.matchExpressions[].key` | `string` | yes | key is the label key that the selector applies to. |
| `spec.target.labelSelector.matchExpressions[].operator` | `string` | yes | operator represents a key's relationship to a set of values. Valid operators are In, NotIn, Exists and DoesNotExist. |
| `spec.target.labelSelector.matchExpressions[].values` | `[]string` | no | values is an array of string values. If the operator is In or NotIn, the values array must be non-empty. If the operator is Exists or DoesNotExist, the values array must be empty. This array is replaced during a strategic merge patch. |
| `spec.target.labelSelector.matchLabels` | `map[string]string` | no | matchLabels is a map of {key,value} pairs. A single {key,value} in the matchLabels map is equivalent to an element of matchExpressions, whose key field is "key", the operator is "In", and the values array contains only "value". The requirements are ANDed. |
| `spec.target.machineRef` | `object` | no | machineRef targets a single named TalosMachine. |
| `spec.target.machineRef.name` | `string` | no | Name of the referent. This field is effectively required, but due to backwards compatibility is allowed to be empty. Instances of this type with an empty value here are almost certainly wrong. More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#names Default: `""`. |
| `spec.target.role` | `string` | no | role targets every machine with this role. Allowed values: `controlplane`, `worker`. |

### status

| Field | Type | Required | Description |
|---|---|---|---|
| `status` | `object` | no | status defines the observed state of TalosConfigPatch |
| `status.conditions` | `[]object` | no | conditions represent the current state of the TalosConfigPatch resource, kstatus-compatible. |
| `status.conditions[].lastTransitionTime` | `string (date-time)` | yes | lastTransitionTime is the last time the condition transitioned from one status to another. This should be when the underlying condition changed. If that is not known, then using the time when the API field changed is acceptable. |
| `status.conditions[].message` | `string` | yes | message is a human readable message indicating details about the transition. This may be an empty string. |
| `status.conditions[].observedGeneration` | `integer` | no | observedGeneration represents the .metadata.generation that the condition was set based upon. For instance, if .metadata.generation is currently 12, but the .status.conditions[x].observedGeneration is 9, the condition is out of date with respect to the current state of the instance. |
| `status.conditions[].reason` | `string` | yes | reason contains a programmatic identifier indicating the reason for the condition's last transition. Producers of specific condition types may define expected values and meanings for this field, and whether the values are considered a guaranteed API. The value should be a CamelCase string. This field may not be empty. |
| `status.conditions[].status` | `string` | yes | status of the condition, one of True, False, Unknown. Allowed values: `True`, `False`, `Unknown`. |
| `status.conditions[].type` | `string` | yes | type of condition in CamelCase or in foo.example.com/CamelCase. |
| `status.observedGeneration` | `integer` | no | observedGeneration is the most recent generation the controller has reconciled. |

