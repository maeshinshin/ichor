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
| `spec.documents[].bgpInstanceConfig` | `object` | no | bgpInstanceConfig is a Talos BGPInstanceConfig document; populated when kind is "BGPInstanceConfig". |
| `spec.documents[].bgpInstanceConfig.advertise` | `[]string` | no |  |
| `spec.documents[].bgpInstanceConfig.importRoutes` | `[]object` | no |  |
| `spec.documents[].bgpInstanceConfig.importRoutes[].bgpInstance` | `string` | yes |  |
| `spec.documents[].bgpInstanceConfig.importRoutes[].prefixes` | `[]string` | yes |  |
| `spec.documents[].bgpInstanceConfig.installRoutes` | `boolean` | no |  |
| `spec.documents[].bgpInstanceConfig.localASN` | `integer` | yes |  |
| `spec.documents[].bgpInstanceConfig.maxPaths` | `integer` | no |  |
| `spec.documents[].bgpInstanceConfig.multipath` | `boolean` | no |  |
| `spec.documents[].bgpInstanceConfig.name` | `string` | yes |  |
| `spec.documents[].bgpInstanceConfig.neighbors` | `[]object` | no |  |
| `spec.documents[].bgpInstanceConfig.neighbors[].address` | `string` | no |  |
| `spec.documents[].bgpInstanceConfig.neighbors[].bfd` | `object` | no | Talos: BGPBFDConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/network/bgpinstanceconfig |
| `spec.documents[].bgpInstanceConfig.neighbors[].bfd.detectMultiplier` | `integer` | no |  |
| `spec.documents[].bgpInstanceConfig.neighbors[].bfd.receiveInterval` | `string` | no |  |
| `spec.documents[].bgpInstanceConfig.neighbors[].bfd.transmitInterval` | `string` | no |  |
| `spec.documents[].bgpInstanceConfig.neighbors[].holdTime` | `string` | no |  |
| `spec.documents[].bgpInstanceConfig.neighbors[].link` | `string` | no |  |
| `spec.documents[].bgpInstanceConfig.neighbors[].localASN` | `integer` | no |  |
| `spec.documents[].bgpInstanceConfig.neighbors[].passive` | `boolean` | no |  |
| `spec.documents[].bgpInstanceConfig.neighbors[].peerASN` | `integer` | no |  |
| `spec.documents[].bgpInstanceConfig.routeSource` | `string` | no |  |
| `spec.documents[].bgpInstanceConfig.routerID` | `string` | no |  |
| `spec.documents[].bgpInstanceConfig.vrf` | `string` | no |  |
| `spec.documents[].blackholeRouteConfig` | `object` | no | blackholeRouteConfig is a Talos BlackholeRouteConfig document; populated when kind is "BlackholeRouteConfig". |
| `spec.documents[].blackholeRouteConfig.metric` | `integer` | no |  |
| `spec.documents[].blackholeRouteConfig.name` | `string` | yes |  |
| `spec.documents[].bondConfig` | `object` | no | bondConfig is a Talos BondConfig document; populated when kind is "BondConfig". |
| `spec.documents[].bondConfig.adActorSysPrio` | `integer` | no |  |
| `spec.documents[].bondConfig.adLACPActive` | `integer` | no |  |
| `spec.documents[].bondConfig.adSelect` | `integer` | no |  |
| `spec.documents[].bondConfig.adUserPortKey` | `integer` | no |  |
| `spec.documents[].bondConfig.addresses` | `[]object` | no |  |
| `spec.documents[].bondConfig.addresses[].address` | `string` | yes |  |
| `spec.documents[].bondConfig.addresses[].routePriority` | `integer` | no |  |
| `spec.documents[].bondConfig.allSlavesActive` | `integer` | no |  |
| `spec.documents[].bondConfig.arpAllTargets` | `integer` | no |  |
| `spec.documents[].bondConfig.arpInterval` | `integer` | no |  |
| `spec.documents[].bondConfig.arpIpTargets` | `[]string` | no |  |
| `spec.documents[].bondConfig.arpValidate` | `integer` | no |  |
| `spec.documents[].bondConfig.bondMode` | `integer` | no |  |
| `spec.documents[].bondConfig.downdelay` | `integer` | no |  |
| `spec.documents[].bondConfig.failOverMac` | `integer` | no |  |
| `spec.documents[].bondConfig.hardwareAddr` | `string (byte)` | no |  |
| `spec.documents[].bondConfig.lacpRate` | `integer` | no |  |
| `spec.documents[].bondConfig.links` | `[]string` | no |  |
| `spec.documents[].bondConfig.lpInterval` | `integer` | no |  |
| `spec.documents[].bondConfig.miimon` | `integer` | no |  |
| `spec.documents[].bondConfig.minLinks` | `integer` | no |  |
| `spec.documents[].bondConfig.missedMax` | `integer` | no |  |
| `spec.documents[].bondConfig.mtu` | `integer` | no |  |
| `spec.documents[].bondConfig.multicast` | `boolean` | no |  |
| `spec.documents[].bondConfig.name` | `string` | yes |  |
| `spec.documents[].bondConfig.nsIp6Targets` | `[]string` | no |  |
| `spec.documents[].bondConfig.numPeerNotif` | `integer` | no |  |
| `spec.documents[].bondConfig.packetsPerSlave` | `integer` | no |  |
| `spec.documents[].bondConfig.peerNotifDelay` | `integer` | no |  |
| `spec.documents[].bondConfig.primaryReselect` | `integer` | no |  |
| `spec.documents[].bondConfig.resendIGMP` | `integer` | no |  |
| `spec.documents[].bondConfig.routes` | `[]object` | no |  |
| `spec.documents[].bondConfig.routes[].destination` | `string` | no |  |
| `spec.documents[].bondConfig.routes[].gateway` | `string` | no |  |
| `spec.documents[].bondConfig.routes[].metric` | `integer` | no |  |
| `spec.documents[].bondConfig.routes[].mtu` | `integer` | no |  |
| `spec.documents[].bondConfig.routes[].source` | `string` | no |  |
| `spec.documents[].bondConfig.routes[].table` | `integer` | no |  |
| `spec.documents[].bondConfig.tlbLogicalLb` | `integer` | no |  |
| `spec.documents[].bondConfig.up` | `boolean` | no |  |
| `spec.documents[].bondConfig.updelay` | `integer` | no |  |
| `spec.documents[].bondConfig.useCarrier` | `boolean` | no |  |
| `spec.documents[].bondConfig.xmitHashPolicy` | `integer` | no |  |
| `spec.documents[].bridgeConfig` | `object` | no | bridgeConfig is a Talos BridgeConfig document; populated when kind is "BridgeConfig". |
| `spec.documents[].bridgeConfig.addresses` | `[]object` | no |  |
| `spec.documents[].bridgeConfig.addresses[].address` | `string` | yes |  |
| `spec.documents[].bridgeConfig.addresses[].routePriority` | `integer` | no |  |
| `spec.documents[].bridgeConfig.hardwareAddr` | `string (byte)` | no |  |
| `spec.documents[].bridgeConfig.links` | `[]string` | no |  |
| `spec.documents[].bridgeConfig.mtu` | `integer` | no |  |
| `spec.documents[].bridgeConfig.multicast` | `boolean` | no |  |
| `spec.documents[].bridgeConfig.name` | `string` | yes |  |
| `spec.documents[].bridgeConfig.routes` | `[]object` | no |  |
| `spec.documents[].bridgeConfig.routes[].destination` | `string` | no |  |
| `spec.documents[].bridgeConfig.routes[].gateway` | `string` | no |  |
| `spec.documents[].bridgeConfig.routes[].metric` | `integer` | no |  |
| `spec.documents[].bridgeConfig.routes[].mtu` | `integer` | no |  |
| `spec.documents[].bridgeConfig.routes[].source` | `string` | no |  |
| `spec.documents[].bridgeConfig.routes[].table` | `integer` | no |  |
| `spec.documents[].bridgeConfig.stp` | `object` | no | Talos: BridgeSTPConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/network/bridgeconfig |
| `spec.documents[].bridgeConfig.stp.enabled` | `boolean` | no |  |
| `spec.documents[].bridgeConfig.up` | `boolean` | no |  |
| `spec.documents[].bridgeConfig.vlan` | `object` | no | Talos: BridgeVLANConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/network/bridgeconfig |
| `spec.documents[].bridgeConfig.vlan.filtering` | `boolean` | no |  |
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
| `spec.documents[].containerConfig` | `object` | no | containerConfig is a Talos ContainerConfig document; populated when kind is "ContainerConfig". |
| `spec.documents[].containerConfig.args` | `[]string` | no |  |
| `spec.documents[].containerConfig.dependsOn` | `object` | no | Talos: ContainerDependsOn — https://docs.siderolabs.com/talos/v1.14/reference/configuration/container/containerconfig |
| `spec.documents[].containerConfig.dependsOn.containers` | `[]string` | no |  |
| `spec.documents[].containerConfig.dependsOn.networks` | `[]string` | no |  |
| `spec.documents[].containerConfig.dependsOn.paths` | `[]string` | no |  |
| `spec.documents[].containerConfig.dependsOn.time` | `boolean` | no |  |
| `spec.documents[].containerConfig.entrypoint` | `[]string` | no |  |
| `spec.documents[].containerConfig.environment` | `[]string` | no |  |
| `spec.documents[].containerConfig.image` | `string` | yes |  |
| `spec.documents[].containerConfig.mounts` | `[]object` | no |  |
| `spec.documents[].containerConfig.mounts[].hostPath` | `object` | no | Talos: HostPathMount — https://docs.siderolabs.com/talos/v1.14/reference/configuration/container/containerconfig |
| `spec.documents[].containerConfig.mounts[].hostPath.destination` | `string` | yes |  |
| `spec.documents[].containerConfig.mounts[].hostPath.options` | `[]string` | no |  |
| `spec.documents[].containerConfig.mounts[].hostPath.source` | `string` | yes |  |
| `spec.documents[].containerConfig.mounts[].tmpfs` | `object` | no | Talos: TmpfsMount — https://docs.siderolabs.com/talos/v1.14/reference/configuration/container/containerconfig |
| `spec.documents[].containerConfig.mounts[].tmpfs.destination` | `string` | yes |  |
| `spec.documents[].containerConfig.mounts[].tmpfs.options` | `[]string` | no |  |
| `spec.documents[].containerConfig.mounts[].tmpfs.size` | `string` | no |  |
| `spec.documents[].containerConfig.mounts[].userVolume` | `object` | no | Talos: UserVolumeMount — https://docs.siderolabs.com/talos/v1.14/reference/configuration/container/containerconfig |
| `spec.documents[].containerConfig.mounts[].userVolume.destination` | `string` | yes |  |
| `spec.documents[].containerConfig.mounts[].userVolume.name` | `string` | yes |  |
| `spec.documents[].containerConfig.mounts[].userVolume.options` | `[]string` | no |  |
| `spec.documents[].containerConfig.name` | `string` | yes |  |
| `spec.documents[].containerConfig.network` | `object` | no | Talos: ContainerNetwork — https://docs.siderolabs.com/talos/v1.14/reference/configuration/container/containerconfig |
| `spec.documents[].containerConfig.network.mode` | `string` | no |  |
| `spec.documents[].containerConfig.resources` | `object` | no | Talos: ContainerResources — https://docs.siderolabs.com/talos/v1.14/reference/configuration/container/containerconfig |
| `spec.documents[].containerConfig.resources.limits` | `object` | no | Talos: ContainerResourceLimits — https://docs.siderolabs.com/talos/v1.14/reference/configuration/container/containerconfig |
| `spec.documents[].containerConfig.resources.limits.cpu` | `string` | no |  |
| `spec.documents[].containerConfig.resources.limits.memory` | `string` | no |  |
| `spec.documents[].containerConfig.runAs` | `object` | no | Talos: ContainerRunAs — https://docs.siderolabs.com/talos/v1.14/reference/configuration/container/containerconfig |
| `spec.documents[].containerConfig.runAs.gid` | `integer` | no |  |
| `spec.documents[].containerConfig.runAs.uid` | `integer` | no |  |
| `spec.documents[].containerConfig.security` | `object` | no | Talos: ContainerSecurity — https://docs.siderolabs.com/talos/v1.14/reference/configuration/container/containerconfig |
| `spec.documents[].containerConfig.security.capabilities` | `object` | no | Talos: ContainerCapabilities — https://docs.siderolabs.com/talos/v1.14/reference/configuration/container/containerconfig |
| `spec.documents[].containerConfig.security.capabilities.add` | `[]string` | no |  |
| `spec.documents[].containerConfig.security.capabilities.drop` | `[]string` | no |  |
| `spec.documents[].containerConfig.security.machinedAccess` | `boolean` | no |  |
| `spec.documents[].containerConfig.security.profile` | `string` | no |  |
| `spec.documents[].containerConfig.workingDir` | `string` | no |  |
| `spec.documents[].dhcpv6Config` | `object` | no | dhcpv6Config is a Talos DHCPv6Config document; populated when kind is "DHCPv6Config". |
| `spec.documents[].dhcpv6Config.clientIdentifier` | `integer` | no |  |
| `spec.documents[].dhcpv6Config.duidRaw` | `string (byte)` | no |  |
| `spec.documents[].dhcpv6Config.ignoreHostname` | `boolean` | no |  |
| `spec.documents[].dhcpv6Config.name` | `string` | yes |  |
| `spec.documents[].dhcpv6Config.routeMetric` | `integer` | no |  |
| `spec.documents[].dummyLinkConfig` | `object` | no | dummyLinkConfig is a Talos DummyLinkConfig document; populated when kind is "DummyLinkConfig". |
| `spec.documents[].dummyLinkConfig.addresses` | `[]object` | no |  |
| `spec.documents[].dummyLinkConfig.addresses[].address` | `string` | yes |  |
| `spec.documents[].dummyLinkConfig.addresses[].routePriority` | `integer` | no |  |
| `spec.documents[].dummyLinkConfig.hardwareAddr` | `string (byte)` | no |  |
| `spec.documents[].dummyLinkConfig.mtu` | `integer` | no |  |
| `spec.documents[].dummyLinkConfig.multicast` | `boolean` | no |  |
| `spec.documents[].dummyLinkConfig.name` | `string` | yes |  |
| `spec.documents[].dummyLinkConfig.routes` | `[]object` | no |  |
| `spec.documents[].dummyLinkConfig.routes[].destination` | `string` | no |  |
| `spec.documents[].dummyLinkConfig.routes[].gateway` | `string` | no |  |
| `spec.documents[].dummyLinkConfig.routes[].metric` | `integer` | no |  |
| `spec.documents[].dummyLinkConfig.routes[].mtu` | `integer` | no |  |
| `spec.documents[].dummyLinkConfig.routes[].source` | `string` | no |  |
| `spec.documents[].dummyLinkConfig.routes[].table` | `integer` | no |  |
| `spec.documents[].dummyLinkConfig.up` | `boolean` | no |  |
| `spec.documents[].environmentConfig` | `object` | no | environmentConfig is a Talos EnvironmentConfig document; populated when kind is "EnvironmentConfig". |
| `spec.documents[].environmentConfig.variables` | `map[string]string` | yes |  |
| `spec.documents[].etcFileConfig` | `object` | no | etcFileConfig is a Talos EtcFileConfig document; populated when kind is "EtcFileConfig". |
| `spec.documents[].etcFileConfig.contents` | `string` | yes |  |
| `spec.documents[].etcFileConfig.mode` | `integer` | yes |  |
| `spec.documents[].etcFileConfig.name` | `string` | yes |  |
| `spec.documents[].ethernetConfig` | `object` | no | ethernetConfig is a Talos EthernetConfig document; populated when kind is "EthernetConfig". |
| `spec.documents[].ethernetConfig.channels` | `object` | no | Talos: EthernetChannelsConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/network/ethernetconfig |
| `spec.documents[].ethernetConfig.channels.combined` | `integer` | no |  |
| `spec.documents[].ethernetConfig.channels.other` | `integer` | no |  |
| `spec.documents[].ethernetConfig.channels.rx` | `integer` | no |  |
| `spec.documents[].ethernetConfig.channels.tx` | `integer` | no |  |
| `spec.documents[].ethernetConfig.features` | `map[string]boolean` | no |  |
| `spec.documents[].ethernetConfig.name` | `string` | yes |  |
| `spec.documents[].ethernetConfig.rings` | `object` | no | Talos: EthernetRingsConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/network/ethernetconfig |
| `spec.documents[].ethernetConfig.rings.cqe-size` | `integer` | no |  |
| `spec.documents[].ethernetConfig.rings.rx` | `integer` | no |  |
| `spec.documents[].ethernetConfig.rings.rx-buf-len` | `integer` | no |  |
| `spec.documents[].ethernetConfig.rings.rx-jumbo` | `integer` | no |  |
| `spec.documents[].ethernetConfig.rings.rx-mini` | `integer` | no |  |
| `spec.documents[].ethernetConfig.rings.rx-push` | `boolean` | no |  |
| `spec.documents[].ethernetConfig.rings.tcp-data-split` | `boolean` | no |  |
| `spec.documents[].ethernetConfig.rings.tx` | `integer` | no |  |
| `spec.documents[].ethernetConfig.rings.tx-push` | `boolean` | no |  |
| `spec.documents[].ethernetConfig.rings.tx-push-buf-len` | `integer` | no |  |
| `spec.documents[].ethernetConfig.wakeOnLan` | `[]integer` | no |  |
| `spec.documents[].eventSinkConfig` | `object` | no | eventSinkConfig is a Talos EventSinkConfig document; populated when kind is "EventSinkConfig". |
| `spec.documents[].eventSinkConfig.endpoint` | `string` | yes |  |
| `spec.documents[].existingVolumeConfig` | `object` | no | existingVolumeConfig is a Talos ExistingVolumeConfig document; populated when kind is "ExistingVolumeConfig". |
| `spec.documents[].existingVolumeConfig.discovery` | `object` | no | Talos: VolumeDiscoverySpec — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/existingvolumeconfig |
| `spec.documents[].existingVolumeConfig.discovery.volumeSelector` | `object` | no | Talos: VolumeSelector — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/existingvolumeconfig |
| `spec.documents[].existingVolumeConfig.discovery.volumeSelector.match` | `string` | no |  |
| `spec.documents[].existingVolumeConfig.mount` | `object` | no | Talos: ExistingMountSpec — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/existingvolumeconfig |
| `spec.documents[].existingVolumeConfig.mount.disableAccessTime` | `boolean` | no |  |
| `spec.documents[].existingVolumeConfig.mount.readOnly` | `boolean` | no |  |
| `spec.documents[].existingVolumeConfig.mount.secure` | `boolean` | no |  |
| `spec.documents[].existingVolumeConfig.name` | `string` | yes |  |
| `spec.documents[].existingVolumeConfig.scrub` | `object` | no | Talos: ScrubConfig |
| `spec.documents[].existingVolumeConfig.scrub.enabled` | `boolean` | no |  |
| `spec.documents[].existingVolumeConfig.scrub.interval` | `string` | no |  |
| `spec.documents[].existingVolumeConfig.trim` | `object` | no | Talos: TrimConfig |
| `spec.documents[].existingVolumeConfig.trim.enabled` | `boolean` | no |  |
| `spec.documents[].existingVolumeConfig.trim.interval` | `string` | no |  |
| `spec.documents[].extensionServiceConfig` | `object` | no | extensionServiceConfig is a Talos ExtensionServiceConfig document; populated when kind is "ExtensionServiceConfig". |
| `spec.documents[].extensionServiceConfig.configFiles` | `[]object` | no |  |
| `spec.documents[].extensionServiceConfig.configFiles[].content` | `string` | yes |  |
| `spec.documents[].extensionServiceConfig.configFiles[].mountPath` | `string` | yes |  |
| `spec.documents[].extensionServiceConfig.environment` | `[]string` | no |  |
| `spec.documents[].extensionServiceConfig.name` | `string` | yes |  |
| `spec.documents[].externalVolumeConfig` | `object` | no | externalVolumeConfig is a Talos ExternalVolumeConfig document; populated when kind is "ExternalVolumeConfig". |
| `spec.documents[].externalVolumeConfig.filesystemType` | `string` | yes |  |
| `spec.documents[].externalVolumeConfig.mount` | `object` | no | Talos: ExternalMountSpec — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/externalvolumeconfig |
| `spec.documents[].externalVolumeConfig.mount.disableAccessTime` | `boolean` | no |  |
| `spec.documents[].externalVolumeConfig.mount.readOnly` | `boolean` | no |  |
| `spec.documents[].externalVolumeConfig.mount.secure` | `boolean` | no |  |
| `spec.documents[].externalVolumeConfig.mount.virtiofs` | `object` | no | Talos: VirtiofsMountSpec — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/externalvolumeconfig |
| `spec.documents[].externalVolumeConfig.mount.virtiofs.tag` | `string` | yes |  |
| `spec.documents[].externalVolumeConfig.name` | `string` | yes |  |
| `spec.documents[].filesystemScrubConfig` | `object` | no | filesystemScrubConfig is a Talos FilesystemScrubConfig document; populated when kind is "FilesystemScrubConfig". |
| `spec.documents[].filesystemScrubConfig.interval` | `string` | no |  |
| `spec.documents[].filesystemTrimConfig` | `object` | no | filesystemTrimConfig is a Talos FilesystemTrimConfig document; populated when kind is "FilesystemTrimConfig". |
| `spec.documents[].filesystemTrimConfig.interval` | `string` | no |  |
| `spec.documents[].httpProbeConfig` | `object` | no | httpProbeConfig is a Talos HTTPProbeConfig document; populated when kind is "HTTPProbeConfig". |
| `spec.documents[].httpProbeConfig.failureThreshold` | `integer` | no |  |
| `spec.documents[].httpProbeConfig.interval` | `string` | no |  |
| `spec.documents[].httpProbeConfig.name` | `string` | yes |  |
| `spec.documents[].httpProbeConfig.timeout` | `string` | no |  |
| `spec.documents[].httpProbeConfig.url` | `string` | yes |  |
| `spec.documents[].imageCacheConfig` | `object` | no | imageCacheConfig is a Talos ImageCacheConfig document; populated when kind is "ImageCacheConfig". |
| `spec.documents[].imageCacheConfig.local` | `object` | yes | Talos: LocalImageCacheConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/cri/imagecacheconfig |
| `spec.documents[].imageCacheConfig.local.enabled` | `boolean` | no |  |
| `spec.documents[].imageVerificationConfig` | `object` | no | imageVerificationConfig is a Talos ImageVerificationConfig document; populated when kind is "ImageVerificationConfig". |
| `spec.documents[].imageVerificationConfig.rules` | `[]object` | no |  |
| `spec.documents[].imageVerificationConfig.rules[].deny` | `boolean` | no |  |
| `spec.documents[].imageVerificationConfig.rules[].image` | `string` | no |  |
| `spec.documents[].imageVerificationConfig.rules[].keyless` | `object` | no | Talos: ImageKeylessVerifierV1Alpha1 — https://docs.siderolabs.com/talos/v1.14/reference/configuration/security/imageverificationconfig |
| `spec.documents[].imageVerificationConfig.rules[].keyless.issuer` | `string` | no |  |
| `spec.documents[].imageVerificationConfig.rules[].keyless.subject` | `string` | no |  |
| `spec.documents[].imageVerificationConfig.rules[].keyless.subjectRegex` | `string` | no |  |
| `spec.documents[].imageVerificationConfig.rules[].publicKey` | `object` | no | Talos: ImagePublicKeyVerifierV1Alpha1 — https://docs.siderolabs.com/talos/v1.14/reference/configuration/security/imageverificationconfig |
| `spec.documents[].imageVerificationConfig.rules[].publicKey.certificate` | `string` | no |  |
| `spec.documents[].imageVerificationConfig.rules[].skip` | `boolean` | no |  |
| `spec.documents[].kernelModuleConfig` | `object` | no | kernelModuleConfig is a Talos KernelModuleConfig document; populated when kind is "KernelModuleConfig". |
| `spec.documents[].kernelModuleConfig.name` | `string` | yes |  |
| `spec.documents[].kernelModuleConfig.parameters` | `[]string` | no |  |
| `spec.documents[].kind` | `string` | yes | kind selects which of the fields below is populated. Allowed values: `SysctlConfig`, `KernelModuleConfig`, `Config`, `VolumeConfig`, `UserVolumeConfig`, `RawVolumeConfig`, `ResolverConfig`, `BGPInstanceConfig`, `BlackholeRouteConfig`, `BondConfig`, `BridgeConfig`, `ContainerConfig`, `DHCPv6Config`, `DummyLinkConfig`, `EnvironmentConfig`, `EtcFileConfig`, `EthernetConfig`, `EventSinkConfig`, `ExistingVolumeConfig`, `ExtensionServiceConfig`, `ExternalVolumeConfig`, `FilesystemScrubConfig`, `FilesystemTrimConfig`, `HTTPProbeConfig`, `ImageCacheConfig`, `ImageVerificationConfig`, `KmsgLogConfig`, `KubeAPIServerConfig`, `KubeClusterConfig`, `KubeControllerManagerConfig`, `KubeCoreDNSConfig`, `KubeInlineManifestConfig`, `KubeNetworkConfig`, `KubeNodeConfig`, `KubePrismConfig`, `KubeSpanConfig`, `KubeSpanEndpointsConfig`, `KubeTalosAPIAccessConfig`, `LVMLogicalVolumeConfig`, `LVMVolumeGroupConfig`, `Layer2VIPConfig`, `OOMConfig`, `PCIDriverRebindConfig`, `RAIDArrayConfig`, `RegistryMirrorConfig`, `RoutingRuleConfig`, `StaticHostConfig`, `SwapVolumeConfig`, `SysfsConfig`, `TCPProbeConfig`, `TimeSyncConfig`, `TrustedRootsConfig`, `UdevRulesConfig`, `VRFConfig`, `VethConfig`, `WatchdogTimerConfig`, `ZswapConfig`, `KubeAuditPolicyConfig`, `KubeAuthorizerConfig`, `KubeCredentialProviderConfig`, `KubeExternalManifestConfig`, `KubeletConfig`, `KubeProxyConfig`, `KubeSchedulerConfig`. |
| `spec.documents[].kmsgLogConfig` | `object` | no | kmsgLogConfig is a Talos KmsgLogConfig document; populated when kind is "KmsgLogConfig". |
| `spec.documents[].kmsgLogConfig.name` | `string` | yes |  |
| `spec.documents[].kmsgLogConfig.url` | `string` | yes |  |
| `spec.documents[].kubeAPIServerConfig` | `object` | no | kubeAPIServerConfig is a Talos KubeAPIServerConfig document; populated when kind is "KubeAPIServerConfig". |
| `spec.documents[].kubeAPIServerConfig.apiPort` | `integer` | no |  |
| `spec.documents[].kubeAPIServerConfig.certExtraSANs` | `[]string` | no |  |
| `spec.documents[].kubeAPIServerConfig.env` | `map[string]string` | no |  |
| `spec.documents[].kubeAPIServerConfig.extraArgs` | `map[string][]string` | no |  |
| `spec.documents[].kubeAPIServerConfig.image` | `string` | yes |  |
| `spec.documents[].kubeAPIServerConfig.resources` | `object` | no | Talos: ResourcesConfig |
| `spec.documents[].kubeAPIServerConfig.resources.limits` | `map[string]integer or string` | no | ResourceList is a set of (resource name, quantity) pairs. |
| `spec.documents[].kubeAPIServerConfig.resources.requests` | `map[string]integer or string` | no | ResourceList is a set of (resource name, quantity) pairs. |
| `spec.documents[].kubeAPIServerConfig.startupProbes` | `boolean` | no |  |
| `spec.documents[].kubeAuditPolicyConfig` | `object` | no | kubeAuditPolicyConfig is a Talos KubeAuditPolicyConfig document; populated when kind is "KubeAuditPolicyConfig". |
| `spec.documents[].kubeAuditPolicyConfig.configuration` | `object` | yes | Policy defines the configuration of audit logging, and the rules for how different request categories are logged. |
| `spec.documents[].kubeAuditPolicyConfig.configuration.apiVersion` | `string` | no | APIVersion defines the versioned schema of this representation of an object. Servers should convert recognized schemas to the latest internal value, and may reject unrecognized values. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |
| `spec.documents[].kubeAuditPolicyConfig.configuration.kind` | `string` | no | Kind is a string value representing the REST resource this object represents. Servers may infer this from the endpoint the client submits requests to. Cannot be updated. In CamelCase. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |
| `spec.documents[].kubeAuditPolicyConfig.configuration.metadata` | `object` | no | ObjectMeta is included for interoperability with API infrastructure. |
| `spec.documents[].kubeAuditPolicyConfig.configuration.omitManagedFields` | `boolean` | no | OmitManagedFields indicates whether to omit the managed fields of the request and response bodies from being written to the API audit log. This is used as a global default - a value of 'true' will omit the managed fileds, otherwise the managed fields will be included in the API audit log. Note that this can also be specified per rule in which case the value specified in a rule will override the global default. |
| `spec.documents[].kubeAuditPolicyConfig.configuration.omitStages` | `[]string` | no | OmitStages is a list of stages for which no events are created. Note that this can also be specified per rule in which case the union of both are omitted. |
| `spec.documents[].kubeAuditPolicyConfig.configuration.rules` | `[]object` | yes | Rules specify the audit Level a request should be recorded at. A request may match multiple rules, in which case the FIRST matching rule is used. The default audit level is None, but can be overridden by a catch-all rule at the end of the list. PolicyRules are strictly ordered. |
| `spec.documents[].kubeAuditPolicyConfig.configuration.rules[].level` | `string` | yes | The Level that requests matching this rule are recorded at. |
| `spec.documents[].kubeAuditPolicyConfig.configuration.rules[].namespaces` | `[]string` | no | Namespaces that this rule matches. The empty string "" matches non-namespaced resources. An empty list implies every namespace. |
| `spec.documents[].kubeAuditPolicyConfig.configuration.rules[].nonResourceURLs` | `[]string` | no | NonResourceURLs is a set of URL paths that should be audited. `*`s are allowed, but only as the full, final step in the path. Examples: - `/metrics` - Log requests for apiserver metrics - `/healthz*` - Log all health checks |
| `spec.documents[].kubeAuditPolicyConfig.configuration.rules[].omitManagedFields` | `boolean` | no | OmitManagedFields indicates whether to omit the managed fields of the request and response bodies from being written to the API audit log. - a value of 'true' will drop the managed fields from the API audit log - a value of 'false' indicates that the managed fileds should be included in the API audit log Note that the value, if specified, in this rule will override the global default If a value is not specified then the global default specified in Policy.OmitManagedFields will stand. |
| `spec.documents[].kubeAuditPolicyConfig.configuration.rules[].omitStages` | `[]string` | no | OmitStages is a list of stages for which no events are created. Note that this can also be specified policy wide in which case the union of both are omitted. An empty list means no restrictions will apply. |
| `spec.documents[].kubeAuditPolicyConfig.configuration.rules[].resources` | `[]object` | no | Resources that this rule matches. An empty list implies all kinds in all API groups. |
| `spec.documents[].kubeAuditPolicyConfig.configuration.rules[].resources[].group` | `string` | no | Group is the name of the API group that contains the resources. The empty string represents the core API group. `*` matches all groups |
| `spec.documents[].kubeAuditPolicyConfig.configuration.rules[].resources[].resourceNames` | `[]string` | no | ResourceNames is a list of resource instance names that the policy matches. Using this field requires Resources to be specified. An empty list implies that every instance of the resource is matched. |
| `spec.documents[].kubeAuditPolicyConfig.configuration.rules[].resources[].resources` | `[]string` | no | Resources is a list of resources this rule applies to. For example: - `pods` matches pods. - `pods/log` matches the log subresource of pods. - `*` matches all resources and their subresources. - `pods/*` matches all subresources of pods. - `*/scale` matches all scale subresources. If wildcard is present, the validation rule will ensure resources do not overlap with each other. An empty list implies all resources and subresources in this API groups apply. |
| `spec.documents[].kubeAuditPolicyConfig.configuration.rules[].userGroups` | `[]string` | no | The user groups this rule applies to. A user is considered matching if it is a member of any of the UserGroups. An empty list implies every user group. |
| `spec.documents[].kubeAuditPolicyConfig.configuration.rules[].users` | `[]string` | no | The users (by authenticated user name) this rule applies to. An empty list implies every user. |
| `spec.documents[].kubeAuditPolicyConfig.configuration.rules[].verbs` | `[]string` | no | The verbs that match this rule. An empty list implies every verb. |
| `spec.documents[].kubeAuthorizerConfig` | `object` | no | kubeAuthorizerConfig is a Talos KubeAuthorizerConfig document; populated when kind is "KubeAuthorizerConfig". |
| `spec.documents[].kubeAuthorizerConfig.name` | `string` | yes |  |
| `spec.documents[].kubeAuthorizerConfig.type` | `string` | yes |  |
| `spec.documents[].kubeAuthorizerConfig.webhook` | `object` | no |  |
| `spec.documents[].kubeAuthorizerConfig.webhook.authorizedTTL` | `string` | yes | The duration to cache 'authorized' responses from the webhook authorizer. Same as setting `--authorization-webhook-cache-authorized-ttl` flag Default: 5m0s |
| `spec.documents[].kubeAuthorizerConfig.webhook.cacheAuthorizedRequests` | `boolean` | no | CacheAuthorizedRequests specifies whether authorized requests should be cached. If set to true, the TTL for cached decisions can be configured via the AuthorizedTTL field. Default: true |
| `spec.documents[].kubeAuthorizerConfig.webhook.cacheUnauthorizedRequests` | `boolean` | no | CacheUnauthorizedRequests specifies whether unauthorized requests should be cached. If set to true, the TTL for cached decisions can be configured via the UnauthorizedTTL field. Default: true |
| `spec.documents[].kubeAuthorizerConfig.webhook.connectionInfo` | `object` | yes | ConnectionInfo defines how we talk to the webhook |
| `spec.documents[].kubeAuthorizerConfig.webhook.connectionInfo.kubeConfigFile` | `string` | yes | Path to KubeConfigFile for connection info Required, if connectionInfo.Type is KubeConfig |
| `spec.documents[].kubeAuthorizerConfig.webhook.connectionInfo.type` | `string` | yes | Controls how the webhook should communicate with the server. Valid values: - KubeConfigFile: use the file specified in kubeConfigFile to locate the server. - InClusterConfig: use the in-cluster configuration to call the SubjectAccessReview API hosted by kube-apiserver. This mode is not allowed for kube-apiserver. |
| `spec.documents[].kubeAuthorizerConfig.webhook.failurePolicy` | `string` | yes | Controls the authorization decision when a webhook request fails to complete or returns a malformed response or errors evaluating matchConditions. Valid values: - NoOpinion: continue to subsequent authorizers to see if one of them allows the request - Deny: reject the request without consulting subsequent authorizers Required, with no default. |
| `spec.documents[].kubeAuthorizerConfig.webhook.matchConditionSubjectAccessReviewVersion` | `string` | yes | MatchConditionSubjectAccessReviewVersion specifies the SubjectAccessReview version the CEL expressions are evaluated against Valid values: v1 Required, no default value |
| `spec.documents[].kubeAuthorizerConfig.webhook.matchConditions` | `[]object` | yes | matchConditions is a list of conditions that must be met for a request to be sent to this webhook. An empty list of matchConditions matches all requests. There are a maximum of 64 match conditions allowed. The exact matching logic is (in order): 1. If at least one matchCondition evaluates to FALSE, then the webhook is skipped. 2. If ALL matchConditions evaluate to TRUE, then the webhook is called. 3. If at least one matchCondition evaluates to an error (but none are FALSE): - If failurePolicy=Deny, then the webhook rejects the request - If failurePolicy=NoOpinion, then the error is ignored and the webhook is skipped |
| `spec.documents[].kubeAuthorizerConfig.webhook.matchConditions[].expression` | `string` | yes | expression represents the expression which will be evaluated by CEL. Must evaluate to bool. CEL expressions have access to the contents of the SubjectAccessReview in v1 version. If version specified by subjectAccessReviewVersion in the request variable is v1beta1, the contents would be converted to the v1 version before evaluating the CEL expression. - 'resourceAttributes' describes information for a resource access request and is unset for non-resource requests. e.g. has(request.resourceAttributes) && request.resourceAttributes.namespace == 'default' - 'nonResourceAttributes' describes information for a non-resource access request and is unset for resource requests. e.g. has(request.nonResourceAttributes) && request.nonResourceAttributes.path == '/healthz'. - 'user' is the user to test for. e.g. request.user == 'alice' - 'groups' is the groups to test for. e.g. ('group1' in request.groups) - 'extra' corresponds to the user.Info.GetExtra() method from the authenticator. - 'uid' is the information about the requesting user. e.g. request.uid == '1' Documentation on CEL: https://kubernetes.io/docs/reference/using-api/cel/ |
| `spec.documents[].kubeAuthorizerConfig.webhook.subjectAccessReviewVersion` | `string` | yes | The API version of the authorization.k8s.io SubjectAccessReview to send to and expect from the webhook. Same as setting `--authorization-webhook-version` flag Valid values: v1beta1, v1 Required, no default value |
| `spec.documents[].kubeAuthorizerConfig.webhook.timeout` | `string` | yes | Timeout for the webhook request Maximum allowed value is 30s. Required, no default value. |
| `spec.documents[].kubeAuthorizerConfig.webhook.unauthorizedTTL` | `string` | yes | The duration to cache 'unauthorized' responses from the webhook authorizer. Same as setting `--authorization-webhook-cache-unauthorized-ttl` flag Default: 30s |
| `spec.documents[].kubeClusterConfig` | `object` | no | kubeClusterConfig is a Talos KubeClusterConfig document; populated when kind is "KubeClusterConfig". |
| `spec.documents[].kubeClusterConfig.clusterName` | `string` | yes |  |
| `spec.documents[].kubeClusterConfig.endpoint` | `string` | yes |  |
| `spec.documents[].kubeControllerManagerConfig` | `object` | no | kubeControllerManagerConfig is a Talos KubeControllerManagerConfig document; populated when kind is "KubeControllerManagerConfig". |
| `spec.documents[].kubeControllerManagerConfig.enabled` | `boolean` | no |  |
| `spec.documents[].kubeControllerManagerConfig.env` | `map[string]string` | no |  |
| `spec.documents[].kubeControllerManagerConfig.extraArgs` | `map[string][]string` | no |  |
| `spec.documents[].kubeControllerManagerConfig.image` | `string` | yes |  |
| `spec.documents[].kubeControllerManagerConfig.resources` | `object` | no | Talos: ResourcesConfig |
| `spec.documents[].kubeControllerManagerConfig.resources.limits` | `map[string]integer or string` | no | ResourceList is a set of (resource name, quantity) pairs. |
| `spec.documents[].kubeControllerManagerConfig.resources.requests` | `map[string]integer or string` | no | ResourceList is a set of (resource name, quantity) pairs. |
| `spec.documents[].kubeCoreDNSConfig` | `object` | no | kubeCoreDNSConfig is a Talos KubeCoreDNSConfig document; populated when kind is "KubeCoreDNSConfig". |
| `spec.documents[].kubeCoreDNSConfig.enabled` | `boolean` | no |  |
| `spec.documents[].kubeCoreDNSConfig.image` | `string` | no |  |
| `spec.documents[].kubeCredentialProviderConfig` | `object` | no | kubeCredentialProviderConfig is a Talos KubeCredentialProviderConfig document; populated when kind is "KubeCredentialProviderConfig". |
| `spec.documents[].kubeCredentialProviderConfig.configuration` | `object` | yes | CredentialProviderConfig is the configuration containing information about each exec credential provider. Kubelet reads this configuration from disk and enables each provider as specified by the CredentialProvider type. |
| `spec.documents[].kubeCredentialProviderConfig.configuration.apiVersion` | `string` | no | APIVersion defines the versioned schema of this representation of an object. Servers should convert recognized schemas to the latest internal value, and may reject unrecognized values. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |
| `spec.documents[].kubeCredentialProviderConfig.configuration.kind` | `string` | no | Kind is a string value representing the REST resource this object represents. Servers may infer this from the endpoint the client submits requests to. Cannot be updated. In CamelCase. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |
| `spec.documents[].kubeCredentialProviderConfig.configuration.providers` | `[]object` | yes | providers is a list of credential provider plugins that will be enabled by the kubelet. Multiple providers may match against a single image, in which case credentials from all providers will be returned to the kubelet. If multiple providers are called for a single image, the results are combined. If providers return overlapping auth keys, the value from the provider earlier in this list is attempted first. |
| `spec.documents[].kubeCredentialProviderConfig.configuration.providers[].apiVersion` | `string` | yes | Required input version of the exec CredentialProviderRequest. The returned CredentialProviderResponse MUST use the same encoding version as the input. Current supported values are: - credentialprovider.kubelet.k8s.io/v1 |
| `spec.documents[].kubeCredentialProviderConfig.configuration.providers[].args` | `[]string` | no | Arguments to pass to the command when executing it. |
| `spec.documents[].kubeCredentialProviderConfig.configuration.providers[].defaultCacheDuration` | `string` | yes | defaultCacheDuration is the default duration the plugin will cache credentials in-memory if a cache duration is not provided in the plugin response. This field is required. |
| `spec.documents[].kubeCredentialProviderConfig.configuration.providers[].env` | `[]object` | no | Env defines additional environment variables to expose to the process. These are unioned with the host's environment, as well as variables client-go uses to pass argument to the plugin. |
| `spec.documents[].kubeCredentialProviderConfig.configuration.providers[].env[].name` | `string` | yes |  |
| `spec.documents[].kubeCredentialProviderConfig.configuration.providers[].env[].value` | `string` | yes |  |
| `spec.documents[].kubeCredentialProviderConfig.configuration.providers[].matchImages` | `[]string` | yes | matchImages is a required list of strings used to match against images in order to determine if this provider should be invoked. If one of the strings matches the requested image from the kubelet, the plugin will be invoked and given a chance to provide credentials. Images are expected to contain the registry domain and URL path. Each entry in matchImages is a pattern which can optionally contain a port and a path. Globs can be used in the domain, but not in the port or the path. Globs are supported as subdomains like '*.k8s.io' or 'k8s.*.io', and top-level-domains such as 'k8s.*'. Matching partial subdomains like 'app*.k8s.io' is also supported. Each glob can only match a single subdomain segment, so *.io does not match *.k8s.io. A match exists between an image and a matchImage when all of the below are true: - Both contain the same number of domain parts and each part matches. - The URL path of an imageMatch must be a prefix of the target image URL path. - If the imageMatch contains a port, then the port must match in the image as well. Example values of matchImages: - 123456789.dkr.ecr.us-east-1.amazonaws.com - *.azurecr.io - gcr.io - *.*.registry.io - registry.io:8080/path |
| `spec.documents[].kubeCredentialProviderConfig.configuration.providers[].name` | `string` | yes | name is the required name of the credential provider. It must match the name of the provider executable as seen by the kubelet. The executable must be in the kubelet's bin directory (set by the --image-credential-provider-bin-dir flag). Required to be unique across all providers. |
| `spec.documents[].kubeCredentialProviderConfig.configuration.providers[].tokenAttributes` | `object` | no | tokenAttributes is the configuration for the service account token that will be passed to the plugin. The credential provider opts in to using service account tokens for image pull by setting this field. When this field is set, kubelet will generate a service account token bound to the pod for which the image is being pulled and pass to the plugin as part of CredentialProviderRequest along with other attributes required by the plugin. The service account metadata and token attributes will be used as a dimension to cache the credentials in kubelet. The cache key is generated by combining the service account metadata (namespace, name, UID, and annotations key+value for the keys defined in serviceAccountTokenAttribute.requiredServiceAccountAnnotationKeys and serviceAccountTokenAttribute.optionalServiceAccountAnnotationKeys). The pod metadata (namespace, name, UID) that are in the service account token are not used as a dimension to cache the credentials in kubelet. This means workloads that are using the same service account could end up using the same credentials for image pull. For plugins that don't want this behavior, or plugins that operate in pass-through mode; i.e., they return the service account token as-is, they can set the credentialProviderResponse.cacheDuration to 0. This will disable the caching of credentials in kubelet and the plugin will be invoked for every image pull. This does result in token generation overhead for every image pull, but it is the only way to ensure that the credentials are not shared across pods (even if they are using the same service account). |
| `spec.documents[].kubeCredentialProviderConfig.configuration.providers[].tokenAttributes.cacheType` | `string` | yes | cacheType indicates the type of cache key use for caching the credentials returned by the plugin when the service account token is used. The most conservative option is to set this to "Token", which means the kubelet will cache returned credentials on a per-token basis. This should be set if the returned credential's lifetime is limited to the service account token's lifetime. If the plugin's credential retrieval logic depends only on the service account and not on pod-specific claims, then the plugin can set this to "ServiceAccount". In this case, the kubelet will cache returned credentials on a per-serviceaccount basis. Use this when the returned credential is valid for all pods using the same service account. |
| `spec.documents[].kubeCredentialProviderConfig.configuration.providers[].tokenAttributes.optionalServiceAccountAnnotationKeys` | `[]string` | no | optionalServiceAccountAnnotationKeys is the list of annotation keys that the plugin is interested in and that are optional to be present in the service account. The keys defined in this list will be extracted from the corresponding service account and passed to the plugin as part of the CredentialProviderRequest. The plugin is responsible for validating the existence of annotations and their values. This field is optional and may be empty. Plugins may use this field to extract additional information required to fetch credentials. Keys in this list must be unique. |
| `spec.documents[].kubeCredentialProviderConfig.configuration.providers[].tokenAttributes.requireServiceAccount` | `boolean` | yes | requireServiceAccount indicates whether the plugin requires the pod to have a service account. If set to true, kubelet will only invoke the plugin if the pod has a service account. If set to false, kubelet will invoke the plugin even if the pod does not have a service account and will not include a token in the CredentialProviderRequest in that scenario. This is useful for plugins that are used to pull images for pods without service accounts (e.g., static pods). |
| `spec.documents[].kubeCredentialProviderConfig.configuration.providers[].tokenAttributes.requiredServiceAccountAnnotationKeys` | `[]string` | no | requiredServiceAccountAnnotationKeys is the list of annotation keys that the plugin is interested in and that are required to be present in the service account. The keys defined in this list will be extracted from the corresponding service account and passed to the plugin as part of the CredentialProviderRequest. If any of the keys defined in this list are not present in the service account, kubelet will not invoke the plugin and will return an error. This field is optional and may be empty. Plugins may use this field to extract additional information required to fetch credentials or allow workloads to opt in to using service account tokens for image pull. If non-empty, requireServiceAccount must be set to true. Keys in this list must be unique. This list needs to be mutually exclusive with optionalServiceAccountAnnotationKeys. |
| `spec.documents[].kubeCredentialProviderConfig.configuration.providers[].tokenAttributes.serviceAccountTokenAudience` | `string` | yes | serviceAccountTokenAudience is the intended audience for the projected service account token. |
| `spec.documents[].kubeExternalManifestConfig` | `object` | no | kubeExternalManifestConfig is a Talos KubeExternalManifestConfig document; populated when kind is "KubeExternalManifestConfig". |
| `spec.documents[].kubeExternalManifestConfig.name` | `string` | yes |  |
| `spec.documents[].kubeExternalManifestConfig.url` | `string` | yes |  |
| `spec.documents[].kubeInlineManifestConfig` | `object` | no | kubeInlineManifestConfig is a Talos KubeInlineManifestConfig document; populated when kind is "KubeInlineManifestConfig". |
| `spec.documents[].kubeInlineManifestConfig.manifest` | `string` | yes |  |
| `spec.documents[].kubeInlineManifestConfig.name` | `string` | yes |  |
| `spec.documents[].kubeNetworkConfig` | `object` | no | kubeNetworkConfig is a Talos KubeNetworkConfig document; populated when kind is "KubeNetworkConfig". |
| `spec.documents[].kubeNetworkConfig.dnsDomain` | `string` | yes |  |
| `spec.documents[].kubeNetworkConfig.nodeCIDRMaskSizeIPv4` | `integer` | no |  |
| `spec.documents[].kubeNetworkConfig.nodeCIDRMaskSizeIPv6` | `integer` | no |  |
| `spec.documents[].kubeNetworkConfig.podSubnets` | `[]string` | yes |  |
| `spec.documents[].kubeNetworkConfig.serviceSubnets` | `[]string` | yes |  |
| `spec.documents[].kubeNodeConfig` | `object` | no | kubeNodeConfig is a Talos KubeNodeConfig document; populated when kind is "KubeNodeConfig". |
| `spec.documents[].kubeNodeConfig.annotations` | `map[string]string` | no |  |
| `spec.documents[].kubeNodeConfig.labels` | `map[string]string` | no |  |
| `spec.documents[].kubeNodeConfig.nodeIP` | `object` | yes | Talos: NodeIPConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/k8s/kubenodeconfig |
| `spec.documents[].kubeNodeConfig.nodeIP.validSubnets` | `[]string` | no |  |
| `spec.documents[].kubeNodeConfig.registerWithFQDN` | `boolean` | no |  |
| `spec.documents[].kubeNodeConfig.skipNodeRegistration` | `boolean` | no |  |
| `spec.documents[].kubeNodeConfig.taints` | `map[string]string` | no |  |
| `spec.documents[].kubePrismConfig` | `object` | no | kubePrismConfig is a Talos KubePrismConfig document; populated when kind is "KubePrismConfig". |
| `spec.documents[].kubePrismConfig.port` | `integer` | yes |  |
| `spec.documents[].kubePrismConfig.tlsServerName` | `string` | no |  |
| `spec.documents[].kubeProxyConfig` | `object` | no | kubeProxyConfig is a Talos KubeProxyConfig document; populated when kind is "KubeProxyConfig". |
| `spec.documents[].kubeProxyConfig.config` | `object` | yes | KubeProxyConfigurationSubset mirrors k8s.io/kube-proxy/config/v1alpha1.KubeProxyConfiguration. Ichor does not carry bindAddress, clientConnection, configSyncPeriod, enableProfiling, healthzBindAddress, hostnameOverride, logging, mode, portRange, showHiddenMetricsForVersion, windowsRunAsService, winkernel. |
| `spec.documents[].kubeProxyConfig.config.bindAddressHardFail` | `boolean` | no |  |
| `spec.documents[].kubeProxyConfig.config.clusterCIDR` | `string` | no |  |
| `spec.documents[].kubeProxyConfig.config.conntrack` | `object` | no | KubeProxyConntrackConfiguration contains conntrack settings for the Kubernetes proxy server. |
| `spec.documents[].kubeProxyConfig.config.conntrack.maxPerCore` | `integer` | yes | maxPerCore is the maximum number of NAT connections to track per CPU core (0 to leave the limit as-is and ignore min). |
| `spec.documents[].kubeProxyConfig.config.conntrack.min` | `integer` | yes | min is the minimum value of connect-tracking records to allocate, regardless of maxPerCore (set maxPerCore=0 to leave the limit as-is). |
| `spec.documents[].kubeProxyConfig.config.conntrack.tcpBeLiberal` | `boolean` | yes | tcpBeLiberal, if true, kube-proxy will configure conntrack to run in liberal mode for TCP connections and packets with out-of-window sequence numbers won't be marked INVALID. |
| `spec.documents[].kubeProxyConfig.config.conntrack.tcpCloseWaitTimeout` | `string` | yes | tcpCloseWaitTimeout is how long an idle conntrack entry in CLOSE_WAIT state will remain in the conntrack table. (e.g. '60s'). Must be greater than 0 to set. |
| `spec.documents[].kubeProxyConfig.config.conntrack.tcpEstablishedTimeout` | `string` | yes | tcpEstablishedTimeout is how long an idle TCP connection will be kept open (e.g. '2s'). Must be greater than 0 to set. |
| `spec.documents[].kubeProxyConfig.config.conntrack.udpStreamTimeout` | `string` | yes | udpStreamTimeout is how long an idle UDP conntrack entry in ASSURED state will remain in the conntrack table (e.g. '300s'). Must be greater than 0 to set. |
| `spec.documents[].kubeProxyConfig.config.conntrack.udpTimeout` | `string` | yes | udpTimeout is how long an idle UDP conntrack entry in UNREPLIED state will remain in the conntrack table (e.g. '30s'). Must be greater than 0 to set. |
| `spec.documents[].kubeProxyConfig.config.detectLocal` | `object` | no | DetectLocalConfiguration contains optional settings related to DetectLocalMode option |
| `spec.documents[].kubeProxyConfig.config.detectLocal.bridgeInterface` | `string` | yes | bridgeInterface is a bridge interface name. When DetectLocalMode is set to LocalModeBridgeInterface, kube-proxy will consider traffic to be local if it originates from this bridge. |
| `spec.documents[].kubeProxyConfig.config.detectLocal.interfaceNamePrefix` | `string` | yes | interfaceNamePrefix is an interface name prefix. When DetectLocalMode is set to LocalModeInterfaceNamePrefix, kube-proxy will consider traffic to be local if it originates from any interface whose name begins with this prefix. |
| `spec.documents[].kubeProxyConfig.config.detectLocalMode` | `string` | no | LocalMode represents modes to detect local traffic from the node |
| `spec.documents[].kubeProxyConfig.config.featureGates` | `map[string]boolean` | no |  |
| `spec.documents[].kubeProxyConfig.config.iptables` | `object` | no | KubeProxyIPTablesConfiguration contains iptables-related configuration details for the Kubernetes proxy server. |
| `spec.documents[].kubeProxyConfig.config.iptables.localhostNodePorts` | `boolean` | yes | localhostNodePorts, if false, tells kube-proxy to disable the legacy behavior of allowing NodePort services to be accessed via localhost. (Applies only to iptables mode and IPv4; localhost NodePorts are never allowed with other proxy modes or with IPv6.) |
| `spec.documents[].kubeProxyConfig.config.iptables.masqueradeAll` | `boolean` | yes | masqueradeAll tells kube-proxy to SNAT all traffic sent to Service cluster IPs, when using the iptables or ipvs proxy mode. This may be required with some CNI plugins. |
| `spec.documents[].kubeProxyConfig.config.iptables.masqueradeBit` | `integer` | yes | masqueradeBit is the bit of the iptables fwmark space to use for SNAT if using the iptables or ipvs proxy mode. Values must be within the range [0, 31]. |
| `spec.documents[].kubeProxyConfig.config.iptables.minSyncPeriod` | `string` | yes | minSyncPeriod is the minimum period between iptables rule resyncs (e.g. '5s', '1m', '2h22m'). A value of 0 means every Service or EndpointSlice change will result in an immediate iptables resync. |
| `spec.documents[].kubeProxyConfig.config.iptables.syncPeriod` | `string` | yes | syncPeriod is an interval (e.g. '5s', '1m', '2h22m') indicating how frequently various re-synchronizing and cleanup operations are performed. Must be greater than 0. |
| `spec.documents[].kubeProxyConfig.config.ipvs` | `object` | no | KubeProxyIPVSConfiguration contains ipvs-related configuration details for the Kubernetes proxy server. |
| `spec.documents[].kubeProxyConfig.config.ipvs.excludeCIDRs` | `[]string` | yes | excludeCIDRs is a list of CIDRs which the ipvs proxier should not touch when cleaning up ipvs services. |
| `spec.documents[].kubeProxyConfig.config.ipvs.minSyncPeriod` | `string` | yes | minSyncPeriod is the minimum period between IPVS rule resyncs (e.g. '5s', '1m', '2h22m'). A value of 0 means every Service or EndpointSlice change will result in an immediate IPVS resync. |
| `spec.documents[].kubeProxyConfig.config.ipvs.scheduler` | `string` | yes | scheduler is the IPVS scheduler to use |
| `spec.documents[].kubeProxyConfig.config.ipvs.strictARP` | `boolean` | yes | strictARP configures arp_ignore and arp_announce to avoid answering ARP queries from kube-ipvs0 interface |
| `spec.documents[].kubeProxyConfig.config.ipvs.syncPeriod` | `string` | yes | syncPeriod is an interval (e.g. '5s', '1m', '2h22m') indicating how frequently various re-synchronizing and cleanup operations are performed. Must be greater than 0. |
| `spec.documents[].kubeProxyConfig.config.ipvs.tcpFinTimeout` | `string` | yes | tcpFinTimeout is the timeout value used for IPVS TCP sessions after receiving a FIN. The default value is 0, which preserves the current timeout value on the system. |
| `spec.documents[].kubeProxyConfig.config.ipvs.tcpTimeout` | `string` | yes | tcpTimeout is the timeout value used for idle IPVS TCP sessions. The default value is 0, which preserves the current timeout value on the system. |
| `spec.documents[].kubeProxyConfig.config.ipvs.udpTimeout` | `string` | yes | udpTimeout is the timeout value used for IPVS UDP packets. The default value is 0, which preserves the current timeout value on the system. |
| `spec.documents[].kubeProxyConfig.config.metricsBindAddress` | `string` | no |  |
| `spec.documents[].kubeProxyConfig.config.nftables` | `object` | no | KubeProxyNFTablesConfiguration contains nftables-related configuration details for the Kubernetes proxy server. |
| `spec.documents[].kubeProxyConfig.config.nftables.masqueradeAll` | `boolean` | yes | masqueradeAll tells kube-proxy to SNAT all traffic sent to Service cluster IPs, when using the nftables mode. This may be required with some CNI plugins. |
| `spec.documents[].kubeProxyConfig.config.nftables.masqueradeBit` | `integer` | yes | masqueradeBit is the bit of the iptables fwmark space to use for SNAT if using the nftables proxy mode. Values must be within the range [0, 31]. |
| `spec.documents[].kubeProxyConfig.config.nftables.minSyncPeriod` | `string` | yes | minSyncPeriod is the minimum period between iptables rule resyncs (e.g. '5s', '1m', '2h22m'). A value of 0 means every Service or EndpointSlice change will result in an immediate iptables resync. |
| `spec.documents[].kubeProxyConfig.config.nftables.syncPeriod` | `string` | yes | syncPeriod is an interval (e.g. '5s', '1m', '2h22m') indicating how frequently various re-synchronizing and cleanup operations are performed. Must be greater than 0. |
| `spec.documents[].kubeProxyConfig.config.nodePortAddresses` | `[]string` | no |  |
| `spec.documents[].kubeProxyConfig.config.oomScoreAdj` | `integer` | no |  |
| `spec.documents[].kubeProxyConfig.extraArgs` | `map[string][]string` | no |  |
| `spec.documents[].kubeProxyConfig.mode` | `string` | no |  |
| `spec.documents[].kubeProxyConfig.resources` | `object` | no | Talos: ResourcesConfig |
| `spec.documents[].kubeProxyConfig.resources.limits` | `map[string]integer or string` | no | ResourceList is a set of (resource name, quantity) pairs. |
| `spec.documents[].kubeProxyConfig.resources.requests` | `map[string]integer or string` | no | ResourceList is a set of (resource name, quantity) pairs. |
| `spec.documents[].kubeSchedulerConfig` | `object` | no | kubeSchedulerConfig is a Talos KubeSchedulerConfig document; populated when kind is "KubeSchedulerConfig". |
| `spec.documents[].kubeSchedulerConfig.config` | `object` | yes | KubeSchedulerConfigurationSubset mirrors k8s.io/kube-scheduler/config/v1.KubeSchedulerConfiguration. Ichor does not carry DebuggingConfiguration, clientConnection, delayCacheUntilActive, extenders, leaderElection, parallelism, podInitialBackoffSeconds, podMaxBackoffSeconds. |
| `spec.documents[].kubeSchedulerConfig.config.percentageOfNodesToScore` | `integer` | no |  |
| `spec.documents[].kubeSchedulerConfig.config.profiles` | `[]object` | no |  |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].percentageOfNodesToScore` | `integer` | no |  |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins` | `object` | no | Plugins include multiple extension points. When specified, the list of plugins for a particular extension point are the only ones enabled. If an extension point is omitted from the config, then the default set of plugins is used for that extension point. Enabled plugins are called in the order specified here, after default plugins. If they need to be invoked before default plugins, default plugins must be disabled and re-enabled here in desired order. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.bind` | `object` | no | Bind is a list of plugins that should be invoked at "Bind" extension point of the scheduling framework. The scheduler call these plugins in order. Scheduler skips the rest of these plugins as soon as one returns success. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.bind.disabled` | `[]object` | no | Disabled specifies default plugins that should be disabled. When all default plugins need to be disabled, an array containing only one "*" should be provided. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.bind.disabled[].name` | `string` | yes | Name defines the name of plugin |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.bind.disabled[].weight` | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.bind.enabled` | `[]object` | no | Enabled specifies plugins that should be enabled in addition to default plugins. If the default plugin is also configured in the scheduler config file, the weight of plugin will be overridden accordingly. These are called after default plugins and in the same order specified here. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.bind.enabled[].name` | `string` | yes | Name defines the name of plugin |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.bind.enabled[].weight` | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.filter` | `object` | no | Filter is a list of plugins that should be invoked when filtering out nodes that cannot run the Pod. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.filter.disabled` | `[]object` | no | Disabled specifies default plugins that should be disabled. When all default plugins need to be disabled, an array containing only one "*" should be provided. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.filter.disabled[].name` | `string` | yes | Name defines the name of plugin |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.filter.disabled[].weight` | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.filter.enabled` | `[]object` | no | Enabled specifies plugins that should be enabled in addition to default plugins. If the default plugin is also configured in the scheduler config file, the weight of plugin will be overridden accordingly. These are called after default plugins and in the same order specified here. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.filter.enabled[].name` | `string` | yes | Name defines the name of plugin |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.filter.enabled[].weight` | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.multiPoint` | `object` | no | MultiPoint is a simplified config section to enable plugins for all valid extension points. Plugins enabled through MultiPoint will automatically register for every individual extension point the plugin has implemented. Disabling a plugin through MultiPoint disables that behavior. The same is true for disabling "*" through MultiPoint (no default plugins will be automatically registered). Plugins can still be disabled through their individual extension points. In terms of precedence, plugin config follows this basic hierarchy 1. Specific extension points 2. Explicitly configured MultiPoint plugins 3. The set of default plugins, as MultiPoint plugins This implies that a higher precedence plugin will run first and overwrite any settings within MultiPoint. Explicitly user-configured plugins also take a higher precedence over default plugins. Within this hierarchy, an Enabled setting takes precedence over Disabled. For example, if a plugin is set in both `multiPoint.Enabled` and `multiPoint.Disabled`, the plugin will be enabled. Similarly, including `multiPoint.Disabled = '*'` and `multiPoint.Enabled = pluginA` will still register that specific plugin through MultiPoint. This follows the same behavior as all other extension point configurations. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.multiPoint.disabled` | `[]object` | no | Disabled specifies default plugins that should be disabled. When all default plugins need to be disabled, an array containing only one "*" should be provided. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.multiPoint.disabled[].name` | `string` | yes | Name defines the name of plugin |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.multiPoint.disabled[].weight` | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.multiPoint.enabled` | `[]object` | no | Enabled specifies plugins that should be enabled in addition to default plugins. If the default plugin is also configured in the scheduler config file, the weight of plugin will be overridden accordingly. These are called after default plugins and in the same order specified here. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.multiPoint.enabled[].name` | `string` | yes | Name defines the name of plugin |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.multiPoint.enabled[].weight` | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.permit` | `object` | no | Permit is a list of plugins that control binding of a Pod. These plugins can prevent or delay binding of a Pod. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.permit.disabled` | `[]object` | no | Disabled specifies default plugins that should be disabled. When all default plugins need to be disabled, an array containing only one "*" should be provided. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.permit.disabled[].name` | `string` | yes | Name defines the name of plugin |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.permit.disabled[].weight` | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.permit.enabled` | `[]object` | no | Enabled specifies plugins that should be enabled in addition to default plugins. If the default plugin is also configured in the scheduler config file, the weight of plugin will be overridden accordingly. These are called after default plugins and in the same order specified here. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.permit.enabled[].name` | `string` | yes | Name defines the name of plugin |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.permit.enabled[].weight` | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.placementGenerate` | `object` | no | PlacementGenerate is a list of plugins that should be invoked during pod group scheduling cycle when determining placements for a pod group. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.placementGenerate.disabled` | `[]object` | no | Disabled specifies default plugins that should be disabled. When all default plugins need to be disabled, an array containing only one "*" should be provided. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.placementGenerate.disabled[].name` | `string` | yes | Name defines the name of plugin |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.placementGenerate.disabled[].weight` | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.placementGenerate.enabled` | `[]object` | no | Enabled specifies plugins that should be enabled in addition to default plugins. If the default plugin is also configured in the scheduler config file, the weight of plugin will be overridden accordingly. These are called after default plugins and in the same order specified here. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.placementGenerate.enabled[].name` | `string` | yes | Name defines the name of plugin |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.placementGenerate.enabled[].weight` | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.placementScore` | `object` | no | PlacementScore is a list of plugins that should be invoked during workload scheduling cycle when ranking pod group assignments. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.placementScore.disabled` | `[]object` | no | Disabled specifies default plugins that should be disabled. When all default plugins need to be disabled, an array containing only one "*" should be provided. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.placementScore.disabled[].name` | `string` | yes | Name defines the name of plugin |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.placementScore.disabled[].weight` | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.placementScore.enabled` | `[]object` | no | Enabled specifies plugins that should be enabled in addition to default plugins. If the default plugin is also configured in the scheduler config file, the weight of plugin will be overridden accordingly. These are called after default plugins and in the same order specified here. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.placementScore.enabled[].name` | `string` | yes | Name defines the name of plugin |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.placementScore.enabled[].weight` | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.postBind` | `object` | no | PostBind is a list of plugins that should be invoked after a pod is successfully bound. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.postBind.disabled` | `[]object` | no | Disabled specifies default plugins that should be disabled. When all default plugins need to be disabled, an array containing only one "*" should be provided. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.postBind.disabled[].name` | `string` | yes | Name defines the name of plugin |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.postBind.disabled[].weight` | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.postBind.enabled` | `[]object` | no | Enabled specifies plugins that should be enabled in addition to default plugins. If the default plugin is also configured in the scheduler config file, the weight of plugin will be overridden accordingly. These are called after default plugins and in the same order specified here. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.postBind.enabled[].name` | `string` | yes | Name defines the name of plugin |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.postBind.enabled[].weight` | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.postFilter` | `object` | no | PostFilter is a list of plugins that are invoked after filtering phase, but only when no feasible nodes were found for the pod. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.postFilter.disabled` | `[]object` | no | Disabled specifies default plugins that should be disabled. When all default plugins need to be disabled, an array containing only one "*" should be provided. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.postFilter.disabled[].name` | `string` | yes | Name defines the name of plugin |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.postFilter.disabled[].weight` | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.postFilter.enabled` | `[]object` | no | Enabled specifies plugins that should be enabled in addition to default plugins. If the default plugin is also configured in the scheduler config file, the weight of plugin will be overridden accordingly. These are called after default plugins and in the same order specified here. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.postFilter.enabled[].name` | `string` | yes | Name defines the name of plugin |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.postFilter.enabled[].weight` | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.preBind` | `object` | no | PreBind is a list of plugins that should be invoked before a pod is bound. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.preBind.disabled` | `[]object` | no | Disabled specifies default plugins that should be disabled. When all default plugins need to be disabled, an array containing only one "*" should be provided. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.preBind.disabled[].name` | `string` | yes | Name defines the name of plugin |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.preBind.disabled[].weight` | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.preBind.enabled` | `[]object` | no | Enabled specifies plugins that should be enabled in addition to default plugins. If the default plugin is also configured in the scheduler config file, the weight of plugin will be overridden accordingly. These are called after default plugins and in the same order specified here. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.preBind.enabled[].name` | `string` | yes | Name defines the name of plugin |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.preBind.enabled[].weight` | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.preEnqueue` | `object` | no | PreEnqueue is a list of plugins that should be invoked before adding pods to the scheduling queue. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.preEnqueue.disabled` | `[]object` | no | Disabled specifies default plugins that should be disabled. When all default plugins need to be disabled, an array containing only one "*" should be provided. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.preEnqueue.disabled[].name` | `string` | yes | Name defines the name of plugin |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.preEnqueue.disabled[].weight` | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.preEnqueue.enabled` | `[]object` | no | Enabled specifies plugins that should be enabled in addition to default plugins. If the default plugin is also configured in the scheduler config file, the weight of plugin will be overridden accordingly. These are called after default plugins and in the same order specified here. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.preEnqueue.enabled[].name` | `string` | yes | Name defines the name of plugin |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.preEnqueue.enabled[].weight` | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.preFilter` | `object` | no | PreFilter is a list of plugins that should be invoked at "PreFilter" extension point of the scheduling framework. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.preFilter.disabled` | `[]object` | no | Disabled specifies default plugins that should be disabled. When all default plugins need to be disabled, an array containing only one "*" should be provided. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.preFilter.disabled[].name` | `string` | yes | Name defines the name of plugin |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.preFilter.disabled[].weight` | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.preFilter.enabled` | `[]object` | no | Enabled specifies plugins that should be enabled in addition to default plugins. If the default plugin is also configured in the scheduler config file, the weight of plugin will be overridden accordingly. These are called after default plugins and in the same order specified here. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.preFilter.enabled[].name` | `string` | yes | Name defines the name of plugin |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.preFilter.enabled[].weight` | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.preScore` | `object` | no | PreScore is a list of plugins that are invoked before scoring. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.preScore.disabled` | `[]object` | no | Disabled specifies default plugins that should be disabled. When all default plugins need to be disabled, an array containing only one "*" should be provided. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.preScore.disabled[].name` | `string` | yes | Name defines the name of plugin |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.preScore.disabled[].weight` | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.preScore.enabled` | `[]object` | no | Enabled specifies plugins that should be enabled in addition to default plugins. If the default plugin is also configured in the scheduler config file, the weight of plugin will be overridden accordingly. These are called after default plugins and in the same order specified here. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.preScore.enabled[].name` | `string` | yes | Name defines the name of plugin |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.preScore.enabled[].weight` | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.queueSort` | `object` | no | QueueSort is a list of plugins that should be invoked when sorting pods in the scheduling queue. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.queueSort.disabled` | `[]object` | no | Disabled specifies default plugins that should be disabled. When all default plugins need to be disabled, an array containing only one "*" should be provided. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.queueSort.disabled[].name` | `string` | yes | Name defines the name of plugin |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.queueSort.disabled[].weight` | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.queueSort.enabled` | `[]object` | no | Enabled specifies plugins that should be enabled in addition to default plugins. If the default plugin is also configured in the scheduler config file, the weight of plugin will be overridden accordingly. These are called after default plugins and in the same order specified here. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.queueSort.enabled[].name` | `string` | yes | Name defines the name of plugin |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.queueSort.enabled[].weight` | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.reserve` | `object` | no | Reserve is a list of plugins invoked when reserving/unreserving resources after a node is assigned to run the pod. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.reserve.disabled` | `[]object` | no | Disabled specifies default plugins that should be disabled. When all default plugins need to be disabled, an array containing only one "*" should be provided. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.reserve.disabled[].name` | `string` | yes | Name defines the name of plugin |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.reserve.disabled[].weight` | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.reserve.enabled` | `[]object` | no | Enabled specifies plugins that should be enabled in addition to default plugins. If the default plugin is also configured in the scheduler config file, the weight of plugin will be overridden accordingly. These are called after default plugins and in the same order specified here. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.reserve.enabled[].name` | `string` | yes | Name defines the name of plugin |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.reserve.enabled[].weight` | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.score` | `object` | no | Score is a list of plugins that should be invoked when ranking nodes that have passed the filtering phase. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.score.disabled` | `[]object` | no | Disabled specifies default plugins that should be disabled. When all default plugins need to be disabled, an array containing only one "*" should be provided. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.score.disabled[].name` | `string` | yes | Name defines the name of plugin |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.score.disabled[].weight` | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.score.enabled` | `[]object` | no | Enabled specifies plugins that should be enabled in addition to default plugins. If the default plugin is also configured in the scheduler config file, the weight of plugin will be overridden accordingly. These are called after default plugins and in the same order specified here. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.score.enabled[].name` | `string` | yes | Name defines the name of plugin |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].plugins.score.enabled[].weight` | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.documents[].kubeSchedulerConfig.config.profiles[].schedulerName` | `string` | no |  |
| `spec.documents[].kubeSchedulerConfig.env` | `map[string]string` | no |  |
| `spec.documents[].kubeSchedulerConfig.extraArgs` | `map[string][]string` | no |  |
| `spec.documents[].kubeSchedulerConfig.resources` | `object` | no | Talos: ResourcesConfig |
| `spec.documents[].kubeSchedulerConfig.resources.limits` | `map[string]integer or string` | no | ResourceList is a set of (resource name, quantity) pairs. |
| `spec.documents[].kubeSchedulerConfig.resources.requests` | `map[string]integer or string` | no | ResourceList is a set of (resource name, quantity) pairs. |
| `spec.documents[].kubeSpanConfig` | `object` | no | kubeSpanConfig is a Talos KubeSpanConfig document; populated when kind is "KubeSpanConfig". |
| `spec.documents[].kubeSpanConfig.advertiseKubernetesNetworks` | `boolean` | no |  |
| `spec.documents[].kubeSpanConfig.allowDownPeerBypass` | `boolean` | no |  |
| `spec.documents[].kubeSpanConfig.enabled` | `boolean` | no |  |
| `spec.documents[].kubeSpanConfig.filters` | `object` | no | Talos: KubeSpanFiltersConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/network/kubespanconfig |
| `spec.documents[].kubeSpanConfig.filters.endpoints` | `[]string` | no |  |
| `spec.documents[].kubeSpanConfig.filters.excludeAdvertisedNetworks` | `[]string` | no |  |
| `spec.documents[].kubeSpanConfig.harvestExtraEndpoints` | `boolean` | no |  |
| `spec.documents[].kubeSpanConfig.mtu` | `integer` | no |  |
| `spec.documents[].kubeSpanEndpointsConfig` | `object` | no | kubeSpanEndpointsConfig is a Talos KubeSpanEndpointsConfig document; populated when kind is "KubeSpanEndpointsConfig". |
| `spec.documents[].kubeSpanEndpointsConfig.extraAnnouncedEndpoints` | `[]string` | yes |  |
| `spec.documents[].kubeTalosAPIAccessConfig` | `object` | no | kubeTalosAPIAccessConfig is a Talos KubeTalosAPIAccessConfig document; populated when kind is "KubeTalosAPIAccessConfig". |
| `spec.documents[].kubeTalosAPIAccessConfig.allowedKubernetesNamespaces` | `[]string` | no |  |
| `spec.documents[].kubeTalosAPIAccessConfig.allowedRoles` | `[]string` | no |  |
| `spec.documents[].kubeletConfig` | `object` | no | kubeletConfig is a Talos KubeletConfig document; populated when kind is "KubeletConfig". |
| `spec.documents[].kubeletConfig.clusterDNS` | `[]string` | no |  |
| `spec.documents[].kubeletConfig.config` | `object` | yes | KubeletConfigurationSubset mirrors k8s.io/kubelet/config/v1beta1.KubeletConfiguration. Ichor does not carry address, authentication, authorization, cgroupDriver, cgroupRoot, cgroupsPerQOS, clusterDNS, configMapAndSecretChangeDetectionStrategy, containerRuntimeEndpoint, contentType, cpuCFSQuota, cpuCFSQuotaPeriod, crashLoopBackOff, enableContentionProfiling, enableControllerAttachDetach, enableDebugFlagsHandler, enableDebuggingHandlers, enableProfilingHandler, enableServer, enableSystemLogHandler, enableSystemLogQuery, enforceNodeAllocatable, eventBurst, eventRecordQPS, failCgroupV1, fileCheckFrequency, hairpinMode, healthzBindAddress, healthzPort, httpCheckFrequency, imagePullCredentialsVerificationPolicy, imageServiceEndpoint, iptablesDropBit, iptablesMasqueradeBit, kernelMemcgNotification, kubeAPIBurst, kubeAPIQPS, kubeReservedCgroup, kubeletCgroups, localStorageCapacityIsolation, logging, makeIPTablesUtilChains, maxOpenFiles, memoryReservationPolicy, memoryThrottlingFactor, nodeLeaseDurationSeconds, nodeStatusMaxImages, nodeStatusReportFrequency, nodeStatusUpdateFrequency, oomScoreAdj, podCIDR, podLogsDir, podsPerCore, port, preloadedImagesVerificationAllowlist, protectKernelDefaults, providerID, qosReserved, readOnlyPort, registerNode, registryBurst, registryPullQPS, reservedSystemCPUs, resolvConf, rotateCertificates, runOnce, runtimeRequestTimeout, seccompDefault, showHiddenMetricsForVersion, singleProcessOOMKill, staticPodPath, staticPodURL, staticPodURLHeader, streamingConnectionIdleTimeout, syncFrequency, systemCgroups, systemReservedCgroup, tlsCertFile, tlsCipherSuites, tlsCurvePreferences, tlsMinVersion, tlsPrivateKeyFile, volumePluginDir, volumeStatsAggPeriod. |
| `spec.documents[].kubeletConfig.config.allowedUnsafeSysctls` | `[]string` | no |  |
| `spec.documents[].kubeletConfig.config.clusterDomain` | `string` | no |  |
| `spec.documents[].kubeletConfig.config.containerLogMaxFiles` | `integer` | no |  |
| `spec.documents[].kubeletConfig.config.containerLogMaxSize` | `string` | no |  |
| `spec.documents[].kubeletConfig.config.containerLogMaxWorkers` | `integer` | no |  |
| `spec.documents[].kubeletConfig.config.containerLogMonitorInterval` | `string` | no |  |
| `spec.documents[].kubeletConfig.config.cpuManagerPolicy` | `string` | no |  |
| `spec.documents[].kubeletConfig.config.cpuManagerPolicyOptions` | `map[string]string` | no |  |
| `spec.documents[].kubeletConfig.config.cpuManagerReconcilePeriod` | `string` | no |  |
| `spec.documents[].kubeletConfig.config.evictionHard` | `map[string]string` | no |  |
| `spec.documents[].kubeletConfig.config.evictionMaxPodGracePeriod` | `integer` | no |  |
| `spec.documents[].kubeletConfig.config.evictionMinimumReclaim` | `map[string]string` | no |  |
| `spec.documents[].kubeletConfig.config.evictionPressureTransitionPeriod` | `string` | no |  |
| `spec.documents[].kubeletConfig.config.evictionSoft` | `map[string]string` | no |  |
| `spec.documents[].kubeletConfig.config.evictionSoftGracePeriod` | `map[string]string` | no |  |
| `spec.documents[].kubeletConfig.config.failSwapOn` | `boolean` | no |  |
| `spec.documents[].kubeletConfig.config.featureGates` | `map[string]boolean` | no |  |
| `spec.documents[].kubeletConfig.config.imageGCHighThresholdPercent` | `integer` | no |  |
| `spec.documents[].kubeletConfig.config.imageGCLowThresholdPercent` | `integer` | no |  |
| `spec.documents[].kubeletConfig.config.imageMaximumGCAge` | `string` | no |  |
| `spec.documents[].kubeletConfig.config.imageMinimumGCAge` | `string` | no |  |
| `spec.documents[].kubeletConfig.config.kubeReserved` | `map[string]string` | no |  |
| `spec.documents[].kubeletConfig.config.maxParallelImagePulls` | `integer` | no |  |
| `spec.documents[].kubeletConfig.config.maxPods` | `integer` | no |  |
| `spec.documents[].kubeletConfig.config.memoryManagerPolicy` | `string` | no |  |
| `spec.documents[].kubeletConfig.config.memorySwap` | `object` | no |  |
| `spec.documents[].kubeletConfig.config.memorySwap.swapBehavior` | `string` | no | swapBehavior configures swap memory available to container workloads. May be one of "", "NoSwap": workloads can not use swap, default option. "LimitedSwap": workload swap usage is limited. The swap limit is proportionate to the container's memory request. |
| `spec.documents[].kubeletConfig.config.mergeDefaultEvictionSettings` | `boolean` | no |  |
| `spec.documents[].kubeletConfig.config.podPidsLimit` | `integer` | no |  |
| `spec.documents[].kubeletConfig.config.registerWithTaints` | `[]object` | no |  |
| `spec.documents[].kubeletConfig.config.registerWithTaints[].effect` | `string` | yes | Required. The effect of the taint on pods that do not tolerate the taint. Valid effects are NoSchedule, PreferNoSchedule and NoExecute. |
| `spec.documents[].kubeletConfig.config.registerWithTaints[].key` | `string` | yes | Required. The taint key to be applied to a node. |
| `spec.documents[].kubeletConfig.config.registerWithTaints[].timeAdded` | `string (date-time)` | no | TimeAdded represents the time at which the taint was added. |
| `spec.documents[].kubeletConfig.config.registerWithTaints[].value` | `string` | no | The taint value corresponding to the taint key. |
| `spec.documents[].kubeletConfig.config.reservedMemory` | `[]object` | no |  |
| `spec.documents[].kubeletConfig.config.reservedMemory[].limits` | `map[string]integer or string` | yes | ResourceList is a set of (resource name, quantity) pairs. |
| `spec.documents[].kubeletConfig.config.reservedMemory[].numaNode` | `integer` | yes |  |
| `spec.documents[].kubeletConfig.config.serializeImagePulls` | `boolean` | no |  |
| `spec.documents[].kubeletConfig.config.serverTLSBootstrap` | `boolean` | no |  |
| `spec.documents[].kubeletConfig.config.shutdownGracePeriod` | `string` | no |  |
| `spec.documents[].kubeletConfig.config.shutdownGracePeriodByPodPriority` | `[]object` | no |  |
| `spec.documents[].kubeletConfig.config.shutdownGracePeriodByPodPriority[].priority` | `integer` | yes | priority is the priority value associated with the shutdown grace period |
| `spec.documents[].kubeletConfig.config.shutdownGracePeriodByPodPriority[].shutdownGracePeriodSeconds` | `integer` | yes | shutdownGracePeriodSeconds is the shutdown grace period in seconds |
| `spec.documents[].kubeletConfig.config.shutdownGracePeriodCriticalPods` | `string` | no |  |
| `spec.documents[].kubeletConfig.config.systemReserved` | `map[string]string` | no |  |
| `spec.documents[].kubeletConfig.config.topologyManagerPolicy` | `string` | no |  |
| `spec.documents[].kubeletConfig.config.topologyManagerPolicyOptions` | `map[string]string` | no |  |
| `spec.documents[].kubeletConfig.config.topologyManagerScope` | `string` | no |  |
| `spec.documents[].kubeletConfig.config.tracing` | `object` | no | TracingConfiguration provides versioned configuration for OpenTelemetry tracing clients. |
| `spec.documents[].kubeletConfig.config.tracing.endpoint` | `string` | no | Endpoint of the collector this component will report traces to. The connection is insecure, and does not currently support TLS. Recommended is unset, and endpoint is the otlp grpc default, localhost:4317. |
| `spec.documents[].kubeletConfig.config.tracing.samplingRatePerMillion` | `integer` | no | SamplingRatePerMillion is the number of samples to collect per million spans. Recommended is unset. If unset, sampler respects its parent span's sampling rate, but otherwise never samples. |
| `spec.documents[].kubeletConfig.config.userNamespaces` | `object` | no | UserNamespaces contains User Namespace configurations. |
| `spec.documents[].kubeletConfig.config.userNamespaces.idsPerPod` | `integer` | no | IDsPerPod is the mapping length of UIDs and GIDs. The length must be a multiple of 65536, and must be less than 1\<\<32. On non-linux such as windows, only null / absent is allowed. Changing the value may require recreating all containers on the node. Default: 65536 |
| `spec.documents[].kubeletConfig.defaultRuntimeSeccompProfileEnabled` | `boolean` | no |  |
| `spec.documents[].kubeletConfig.extraArgs` | `map[string][]string` | no |  |
| `spec.documents[].layer2VIPConfig` | `object` | no | layer2VIPConfig is a Talos Layer2VIPConfig document; populated when kind is "Layer2VIPConfig". |
| `spec.documents[].layer2VIPConfig.link` | `string` | yes |  |
| `spec.documents[].layer2VIPConfig.name` | `string` | yes |  |
| `spec.documents[].lvmLogicalVolumeConfig` | `object` | no | lvmLogicalVolumeConfig is a Talos LVMLogicalVolumeConfig document; populated when kind is "LVMLogicalVolumeConfig". |
| `spec.documents[].lvmLogicalVolumeConfig.mirrors` | `integer` | no |  |
| `spec.documents[].lvmLogicalVolumeConfig.name` | `string` | yes |  |
| `spec.documents[].lvmLogicalVolumeConfig.provisioning` | `object` | yes | Talos: LVMLogicalVolumeProvisioningSpec — https://docs.siderolabs.com/talos/v1.14/reference/configuration/storage/lvmlogicalvolumeconfig |
| `spec.documents[].lvmLogicalVolumeConfig.provisioning.maxSize` | `string` | no |  |
| `spec.documents[].lvmLogicalVolumeConfig.provisioning.minSize` | `string` | no |  |
| `spec.documents[].lvmLogicalVolumeConfig.provisioning.volumeGroup` | `string` | yes |  |
| `spec.documents[].lvmLogicalVolumeConfig.stripes` | `integer` | no |  |
| `spec.documents[].lvmLogicalVolumeConfig.type` | `integer` | yes |  |
| `spec.documents[].lvmVolumeGroupConfig` | `object` | no | lvmVolumeGroupConfig is a Talos LVMVolumeGroupConfig document; populated when kind is "LVMVolumeGroupConfig". |
| `spec.documents[].lvmVolumeGroupConfig.name` | `string` | yes |  |
| `spec.documents[].lvmVolumeGroupConfig.provisioning` | `object` | yes | Talos: ProvisioningSpec — https://docs.siderolabs.com/talos/v1.14/reference/configuration/storage/lvmvolumegroupconfig |
| `spec.documents[].lvmVolumeGroupConfig.provisioning.volumeSelector` | `object` | no | Talos: LVMVolumeSelectorSpec — https://docs.siderolabs.com/talos/v1.14/reference/configuration/storage/lvmvolumegroupconfig |
| `spec.documents[].lvmVolumeGroupConfig.provisioning.volumeSelector.match` | `string` | no |  |
| `spec.documents[].oomConfig` | `object` | no | oomConfig is a Talos OOMConfig document; populated when kind is "OOMConfig". |
| `spec.documents[].oomConfig.cgroupRankingExpression` | `string` | no |  |
| `spec.documents[].oomConfig.sampleInterval` | `string` | no |  |
| `spec.documents[].oomConfig.strictCgroupClassOrdering` | `boolean` | no |  |
| `spec.documents[].oomConfig.triggerExpression` | `string` | no |  |
| `spec.documents[].pciDriverRebindConfig` | `object` | no | pciDriverRebindConfig is a Talos PCIDriverRebindConfig document; populated when kind is "PCIDriverRebindConfig". |
| `spec.documents[].pciDriverRebindConfig.name` | `string` | yes |  |
| `spec.documents[].pciDriverRebindConfig.targetDriver` | `string` | yes |  |
| `spec.documents[].raidArrayConfig` | `object` | no | raidArrayConfig is a Talos RAIDArrayConfig document; populated when kind is "RAIDArrayConfig". |
| `spec.documents[].raidArrayConfig.level` | `integer` | yes |  |
| `spec.documents[].raidArrayConfig.metadata` | `integer` | no |  |
| `spec.documents[].raidArrayConfig.name` | `string` | yes |  |
| `spec.documents[].raidArrayConfig.provisioning` | `object` | yes | Talos: RAIDProvisioningSpec — https://docs.siderolabs.com/talos/v1.14/reference/configuration/storage/raidarrayconfig |
| `spec.documents[].raidArrayConfig.provisioning.volumeSelector` | `object` | yes | Talos: RAIDVolumeSelector — https://docs.siderolabs.com/talos/v1.14/reference/configuration/storage/raidarrayconfig |
| `spec.documents[].raidArrayConfig.provisioning.volumeSelector.match` | `string` | no |  |
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
| `spec.documents[].registryMirrorConfig` | `object` | no | registryMirrorConfig is a Talos RegistryMirrorConfig document; populated when kind is "RegistryMirrorConfig". |
| `spec.documents[].registryMirrorConfig.endpoints` | `[]object` | no |  |
| `spec.documents[].registryMirrorConfig.endpoints[].overridePath` | `boolean` | no |  |
| `spec.documents[].registryMirrorConfig.endpoints[].url` | `string` | yes |  |
| `spec.documents[].registryMirrorConfig.name` | `string` | yes |  |
| `spec.documents[].registryMirrorConfig.skipFallback` | `boolean` | no |  |
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
| `spec.documents[].routingRuleConfig` | `object` | no | routingRuleConfig is a Talos RoutingRuleConfig document; populated when kind is "RoutingRuleConfig". |
| `spec.documents[].routingRuleConfig.action` | `integer` | no |  |
| `spec.documents[].routingRuleConfig.dst` | `string` | no |  |
| `spec.documents[].routingRuleConfig.fwMark` | `integer` | no |  |
| `spec.documents[].routingRuleConfig.fwMask` | `integer` | no |  |
| `spec.documents[].routingRuleConfig.iifName` | `string` | no |  |
| `spec.documents[].routingRuleConfig.name` | `string` | yes |  |
| `spec.documents[].routingRuleConfig.oifName` | `string` | no |  |
| `spec.documents[].routingRuleConfig.src` | `string` | no |  |
| `spec.documents[].routingRuleConfig.table` | `integer` | yes |  |
| `spec.documents[].staticHostConfig` | `object` | no | staticHostConfig is a Talos StaticHostConfig document; populated when kind is "StaticHostConfig". |
| `spec.documents[].staticHostConfig.hostnames` | `[]string` | yes |  |
| `spec.documents[].staticHostConfig.name` | `string` | yes |  |
| `spec.documents[].swapVolumeConfig` | `object` | no | swapVolumeConfig is a Talos SwapVolumeConfig document; populated when kind is "SwapVolumeConfig". |
| `spec.documents[].swapVolumeConfig.encryption` | `object` | no | Talos: EncryptionSpec |
| `spec.documents[].swapVolumeConfig.encryption.allowDiscards` | `boolean` | no |  |
| `spec.documents[].swapVolumeConfig.encryption.blockSize` | `integer` | no |  |
| `spec.documents[].swapVolumeConfig.encryption.cipher` | `string` | no |  |
| `spec.documents[].swapVolumeConfig.encryption.keySize` | `integer` | no |  |
| `spec.documents[].swapVolumeConfig.encryption.keys` | `[]object` | yes |  |
| `spec.documents[].swapVolumeConfig.encryption.keys[].kms` | `object` | no | Talos: EncryptionKeyKMS |
| `spec.documents[].swapVolumeConfig.encryption.keys[].kms.endpoint` | `string` | yes |  |
| `spec.documents[].swapVolumeConfig.encryption.keys[].lockToState` | `boolean` | no |  |
| `spec.documents[].swapVolumeConfig.encryption.keys[].nodeID` | `object` | no | Talos: EncryptionKeyNodeID |
| `spec.documents[].swapVolumeConfig.encryption.keys[].slot` | `integer` | yes |  |
| `spec.documents[].swapVolumeConfig.encryption.keys[].tpm` | `object` | no | Talos: EncryptionKeyTPM |
| `spec.documents[].swapVolumeConfig.encryption.keys[].tpm.checkSecurebootStatusOnEnroll` | `boolean` | no |  |
| `spec.documents[].swapVolumeConfig.encryption.keys[].tpm.options` | `object` | no | Talos: EncryptionKeyTPMOptions |
| `spec.documents[].swapVolumeConfig.encryption.keys[].tpm.options.pcrs` | `[]integer` | no |  |
| `spec.documents[].swapVolumeConfig.encryption.options` | `[]string` | no |  |
| `spec.documents[].swapVolumeConfig.encryption.provider` | `string` | yes |  |
| `spec.documents[].swapVolumeConfig.name` | `string` | yes |  |
| `spec.documents[].swapVolumeConfig.provisioning` | `object` | no | Talos: ProvisioningSpec |
| `spec.documents[].swapVolumeConfig.provisioning.diskSelector` | `object` | no | Talos: DiskSelector |
| `spec.documents[].swapVolumeConfig.provisioning.diskSelector.match` | `string` | no |  |
| `spec.documents[].swapVolumeConfig.provisioning.grow` | `boolean` | no |  |
| `spec.documents[].swapVolumeConfig.provisioning.maxSize` | `string` | no |  |
| `spec.documents[].swapVolumeConfig.provisioning.minSize` | `string` | no |  |
| `spec.documents[].sysctlConfig` | `object` | no | sysctlConfig is a Talos SysctlConfig document; populated when kind is "SysctlConfig". |
| `spec.documents[].sysctlConfig.params` | `map[string]string` | yes |  |
| `spec.documents[].sysfsConfig` | `object` | no | sysfsConfig is a Talos SysfsConfig document; populated when kind is "SysfsConfig". |
| `spec.documents[].sysfsConfig.params` | `map[string]string` | yes |  |
| `spec.documents[].tcpProbeConfig` | `object` | no | tcpProbeConfig is a Talos TCPProbeConfig document; populated when kind is "TCPProbeConfig". |
| `spec.documents[].tcpProbeConfig.endpoint` | `string` | yes |  |
| `spec.documents[].tcpProbeConfig.failureThreshold` | `integer` | no |  |
| `spec.documents[].tcpProbeConfig.interval` | `string` | no |  |
| `spec.documents[].tcpProbeConfig.name` | `string` | yes |  |
| `spec.documents[].tcpProbeConfig.timeout` | `string` | no |  |
| `spec.documents[].timeSyncConfig` | `object` | no | timeSyncConfig is a Talos TimeSyncConfig document; populated when kind is "TimeSyncConfig". |
| `spec.documents[].timeSyncConfig.bootTimeout` | `string` | no |  |
| `spec.documents[].timeSyncConfig.enabled` | `boolean` | no |  |
| `spec.documents[].timeSyncConfig.ntp` | `object` | no | Talos: NTPConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/network/timesyncconfig |
| `spec.documents[].timeSyncConfig.ntp.servers` | `[]string` | no |  |
| `spec.documents[].timeSyncConfig.ntp.useNTS` | `boolean` | no |  |
| `spec.documents[].timeSyncConfig.ptp` | `object` | no | Talos: PTPConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/network/timesyncconfig |
| `spec.documents[].timeSyncConfig.ptp.devices` | `[]string` | no |  |
| `spec.documents[].trustedRootsConfig` | `object` | no | trustedRootsConfig is a Talos TrustedRootsConfig document; populated when kind is "TrustedRootsConfig". |
| `spec.documents[].trustedRootsConfig.certificates` | `string` | yes |  |
| `spec.documents[].trustedRootsConfig.name` | `string` | yes |  |
| `spec.documents[].udevRulesConfig` | `object` | no | udevRulesConfig is a Talos UdevRulesConfig document; populated when kind is "UdevRulesConfig". |
| `spec.documents[].udevRulesConfig.rules` | `[]string` | no |  |
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
| `spec.documents[].vethConfig` | `object` | no | vethConfig is a Talos VethConfig document; populated when kind is "VethConfig". |
| `spec.documents[].vethConfig.addresses` | `[]object` | no |  |
| `spec.documents[].vethConfig.addresses[].address` | `string` | yes |  |
| `spec.documents[].vethConfig.addresses[].routePriority` | `integer` | no |  |
| `spec.documents[].vethConfig.mtu` | `integer` | no |  |
| `spec.documents[].vethConfig.multicast` | `boolean` | no |  |
| `spec.documents[].vethConfig.name` | `string` | yes |  |
| `spec.documents[].vethConfig.peer` | `object` | yes | Talos: VethPeerConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/network/vethconfig |
| `spec.documents[].vethConfig.peer.addresses` | `[]object` | no |  |
| `spec.documents[].vethConfig.peer.addresses[].address` | `string` | yes |  |
| `spec.documents[].vethConfig.peer.addresses[].routePriority` | `integer` | no |  |
| `spec.documents[].vethConfig.peer.mtu` | `integer` | no |  |
| `spec.documents[].vethConfig.peer.multicast` | `boolean` | no |  |
| `spec.documents[].vethConfig.peer.name` | `string` | yes |  |
| `spec.documents[].vethConfig.peer.routes` | `[]object` | no |  |
| `spec.documents[].vethConfig.peer.routes[].destination` | `string` | no |  |
| `spec.documents[].vethConfig.peer.routes[].gateway` | `string` | no |  |
| `spec.documents[].vethConfig.peer.routes[].metric` | `integer` | no |  |
| `spec.documents[].vethConfig.peer.routes[].mtu` | `integer` | no |  |
| `spec.documents[].vethConfig.peer.routes[].source` | `string` | no |  |
| `spec.documents[].vethConfig.peer.routes[].table` | `integer` | no |  |
| `spec.documents[].vethConfig.peer.up` | `boolean` | no |  |
| `spec.documents[].vethConfig.routes` | `[]object` | no |  |
| `spec.documents[].vethConfig.routes[].destination` | `string` | no |  |
| `spec.documents[].vethConfig.routes[].gateway` | `string` | no |  |
| `spec.documents[].vethConfig.routes[].metric` | `integer` | no |  |
| `spec.documents[].vethConfig.routes[].mtu` | `integer` | no |  |
| `spec.documents[].vethConfig.routes[].source` | `string` | no |  |
| `spec.documents[].vethConfig.routes[].table` | `integer` | no |  |
| `spec.documents[].vethConfig.up` | `boolean` | no |  |
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
| `spec.documents[].vrfConfig` | `object` | no | vrfConfig is a Talos VRFConfig document; populated when kind is "VRFConfig". |
| `spec.documents[].vrfConfig.addresses` | `[]object` | no |  |
| `spec.documents[].vrfConfig.addresses[].address` | `string` | yes |  |
| `spec.documents[].vrfConfig.addresses[].routePriority` | `integer` | no |  |
| `spec.documents[].vrfConfig.hardwareAddr` | `string (byte)` | no |  |
| `spec.documents[].vrfConfig.links` | `[]string` | no |  |
| `spec.documents[].vrfConfig.mtu` | `integer` | no |  |
| `spec.documents[].vrfConfig.multicast` | `boolean` | no |  |
| `spec.documents[].vrfConfig.name` | `string` | yes |  |
| `spec.documents[].vrfConfig.routes` | `[]object` | no |  |
| `spec.documents[].vrfConfig.routes[].destination` | `string` | no |  |
| `spec.documents[].vrfConfig.routes[].gateway` | `string` | no |  |
| `spec.documents[].vrfConfig.routes[].metric` | `integer` | no |  |
| `spec.documents[].vrfConfig.routes[].mtu` | `integer` | no |  |
| `spec.documents[].vrfConfig.routes[].source` | `string` | no |  |
| `spec.documents[].vrfConfig.routes[].table` | `integer` | no |  |
| `spec.documents[].vrfConfig.table` | `integer` | yes |  |
| `spec.documents[].vrfConfig.up` | `boolean` | no |  |
| `spec.documents[].watchdogTimerConfig` | `object` | no | watchdogTimerConfig is a Talos WatchdogTimerConfig document; populated when kind is "WatchdogTimerConfig". |
| `spec.documents[].watchdogTimerConfig.device` | `string` | yes |  |
| `spec.documents[].watchdogTimerConfig.timeout` | `string` | no |  |
| `spec.documents[].zswapConfig` | `object` | no | zswapConfig is a Talos ZswapConfig document; populated when kind is "ZswapConfig". |
| `spec.documents[].zswapConfig.maxPoolPercent` | `integer` | no |  |
| `spec.documents[].zswapConfig.shrinkerEnabled` | `boolean` | no |  |
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

