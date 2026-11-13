---
title: "TalosCluster"
description: "TalosCluster is the Schema for the talosclusters API"
---

# TalosCluster

This page is generated from the CRD schema in `config/crd/bases` (`make docs-reference`). Do not edit it.

TalosCluster is the Schema for the talosclusters API

| | |
|---|---|
| Group | `ichor.maesh.dev` |
| Kind | `TalosCluster` (list kind `TalosClusterList`) |
| Resource | `talosclusters` |
| Short names | `tcl` |
| Scope | Namespaced |

## v1alpha1

Version v1alpha1: has a `status` subresource.

### spec

| Field | Type | Required | Description |
|---|---|---|---|
| `spec` | `object` | yes | spec defines the desired state of TalosCluster |
| `spec.authentication` | `object` | no | authentication declares who the kube-apiserver trusts to sign users in with a JWT: Ichor's own issuer and up to three external OIDC providers. It is rendered into Talos's KubeAuthenticationConfig document, so it needs versionContract v1.14 or later, and changing it needs the update verb on talosclusters/authentication. Unset (the default) renders the Talos default: no JWT authenticator, anonymous access to the health endpoints only. |
| `spec.authentication.external` | `[]object` | no | external lists the external OIDC providers the kube-apiserver also trusts. Keep it equal to ichorUI.oidc.*: that is what the UI signs in against, this is what the kube-apiserver accepts. |
| `spec.authentication.external[].audiences` | `[]string` | yes | audiences are the accepted aud values (the OIDC client IDs). |
| `spec.authentication.external[].certificateAuthority` | `string` | no | certificateAuthority is a PEM bundle (public certificates only) that verifies the provider's TLS certificate. Empty uses the control plane's system roots. |
| `spec.authentication.external[].groupsClaim` | `string` | no | groupsClaim is the token claim that lists the user's groups. Default: `"groups"`. |
| `spec.authentication.external[].groupsPrefix` | `string` | no | groupsPrefix is put in front of every group, with the same restrictions as usernamePrefix. Default: `"oidc:"`. |
| `spec.authentication.external[].issuerURL` | `string` | yes | issuerURL is the provider's issuer, an https URL without query or fragment. |
| `spec.authentication.external[].usernameClaim` | `string` | no | usernameClaim is the token claim that names the user. Default: `"email"`. |
| `spec.authentication.external[].usernamePrefix` | `string` | no | usernamePrefix is put in front of every username. It cannot be empty or start with "ichor:", so an external user can never be mistaken for an Ichor user. Default: `"oidc:"`. |
| `spec.authentication.ichor` | `object` | no | ichor turns on Ichor's own OIDC issuer (ichor-ui) as a kube-apiserver authenticator. Users appear as ichor:\<name>, groups as ichor:\<role>. |
| `spec.authentication.ichor.enabled` | `boolean` | no | enabled renders Ichor's issuer into the kube-apiserver. It needs the trust ConfigMap ichor-oidc-trust (the controller publishes it); without it the render fails and says so. Default: `false`. |
| `spec.authentication.ichor.url` | `string` | yes | url is the external https URL of ichor-ui. It is the issuer (the iss claim of every token). |
| `spec.certSANs` | `[]string` | no | certSANs are extra subject alternative names (hostnames, IPs) for the Talos API certificate of every node and for the kube-apiserver serving certificate: machine.certSANs and KubeAPIServerConfig.certExtraSANs, which Talos's generator writes together from one list. The cluster endpoint host is not added for you here; list it if the nodes should serve it. Unset adds none. |
| `spec.cni` | `object` | no | cni configures which CNI preset this cluster bootstraps and how kube-proxy is split between Talos and the CNI (docs/design/architecture.md, "CNI"). |
| `spec.cni.cilium` | `object` | no | cilium holds the user layer of Cilium's Helm values. |
| `spec.cni.cilium.values` | `object (free-form)` | no | values is the user layer of Cilium's Helm values. Effective values are Ichor's defaults, then these deep-merged on top, then the Ichor-owned keys (ipam.mode, securityContext.capabilities.ciliumAgent, securityContext.capabilities.cleanCiliumState, cgroup.autoMount.enabled, cgroup.hostRoot, k8sServiceHost, k8sServicePort, kubeProxyReplacement, tunnelProtocol); a value for an owned key is a validation error. The merged values must stay under 64 KiB. |
| `spec.cni.custom` **(deprecated)** | `object` | no | custom is deprecated, unused; kept so the CRD never drops a field. It declared the chart/manifest source and Talos-side settings for a user-supplied CNI. |
| `spec.cni.custom.documents` **(deprecated)** | `[]object` | no | documents are the Talos-side config documents this CNI needs, applied as a cluster-layer patch -- e.g. disabling kube-proxy when kubeProxy is "replaced". |
| `spec.cni.custom.documents[].bgpInstanceConfig` **(deprecated)** | `object` | no | bgpInstanceConfig is a Talos BGPInstanceConfig document; populated when kind is "BGPInstanceConfig". |
| `spec.cni.custom.documents[].bgpInstanceConfig.advertise` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].bgpInstanceConfig.importRoutes` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].bgpInstanceConfig.importRoutes[].bgpInstance` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].bgpInstanceConfig.importRoutes[].prefixes` **(deprecated)** | `[]string` | yes |  |
| `spec.cni.custom.documents[].bgpInstanceConfig.installRoutes` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].bgpInstanceConfig.localASN` **(deprecated)** | `integer` | yes |  |
| `spec.cni.custom.documents[].bgpInstanceConfig.maxPaths` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].bgpInstanceConfig.multipath` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].bgpInstanceConfig.name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].bgpInstanceConfig.neighbors` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].bgpInstanceConfig.neighbors[].address` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].bgpInstanceConfig.neighbors[].bfd` **(deprecated)** | `object` | no | Talos: BGPBFDConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/network/bgpinstanceconfig |
| `spec.cni.custom.documents[].bgpInstanceConfig.neighbors[].bfd.detectMultiplier` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].bgpInstanceConfig.neighbors[].bfd.receiveInterval` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].bgpInstanceConfig.neighbors[].bfd.transmitInterval` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].bgpInstanceConfig.neighbors[].holdTime` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].bgpInstanceConfig.neighbors[].link` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].bgpInstanceConfig.neighbors[].localASN` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].bgpInstanceConfig.neighbors[].passive` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].bgpInstanceConfig.neighbors[].peerASN` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].bgpInstanceConfig.routeSource` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].bgpInstanceConfig.routerID` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].bgpInstanceConfig.vrf` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].blackholeRouteConfig` **(deprecated)** | `object` | no | blackholeRouteConfig is a Talos BlackholeRouteConfig document; populated when kind is "BlackholeRouteConfig". |
| `spec.cni.custom.documents[].blackholeRouteConfig.metric` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].blackholeRouteConfig.name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].bondConfig` **(deprecated)** | `object` | no | bondConfig is a Talos BondConfig document; populated when kind is "BondConfig". |
| `spec.cni.custom.documents[].bondConfig.adActorSysPrio` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].bondConfig.adLACPActive` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].bondConfig.adSelect` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].bondConfig.adUserPortKey` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].bondConfig.addresses` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].bondConfig.addresses[].address` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].bondConfig.addresses[].routePriority` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].bondConfig.allSlavesActive` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].bondConfig.arpAllTargets` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].bondConfig.arpInterval` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].bondConfig.arpIpTargets` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].bondConfig.arpValidate` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].bondConfig.bondMode` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].bondConfig.downdelay` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].bondConfig.failOverMac` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].bondConfig.hardwareAddr` **(deprecated)** | `string (byte)` | no |  |
| `spec.cni.custom.documents[].bondConfig.lacpRate` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].bondConfig.links` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].bondConfig.lpInterval` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].bondConfig.miimon` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].bondConfig.minLinks` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].bondConfig.missedMax` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].bondConfig.mtu` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].bondConfig.multicast` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].bondConfig.name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].bondConfig.nsIp6Targets` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].bondConfig.numPeerNotif` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].bondConfig.packetsPerSlave` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].bondConfig.peerNotifDelay` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].bondConfig.primaryReselect` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].bondConfig.resendIGMP` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].bondConfig.routes` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].bondConfig.routes[].destination` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].bondConfig.routes[].gateway` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].bondConfig.routes[].metric` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].bondConfig.routes[].mtu` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].bondConfig.routes[].source` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].bondConfig.routes[].table` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].bondConfig.tlbLogicalLb` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].bondConfig.up` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].bondConfig.updelay` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].bondConfig.useCarrier` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].bondConfig.xmitHashPolicy` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].bridgeConfig` **(deprecated)** | `object` | no | bridgeConfig is a Talos BridgeConfig document; populated when kind is "BridgeConfig". |
| `spec.cni.custom.documents[].bridgeConfig.addresses` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].bridgeConfig.addresses[].address` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].bridgeConfig.addresses[].routePriority` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].bridgeConfig.hardwareAddr` **(deprecated)** | `string (byte)` | no |  |
| `spec.cni.custom.documents[].bridgeConfig.links` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].bridgeConfig.mtu` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].bridgeConfig.multicast` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].bridgeConfig.name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].bridgeConfig.routes` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].bridgeConfig.routes[].destination` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].bridgeConfig.routes[].gateway` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].bridgeConfig.routes[].metric` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].bridgeConfig.routes[].mtu` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].bridgeConfig.routes[].source` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].bridgeConfig.routes[].table` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].bridgeConfig.stp` **(deprecated)** | `object` | no | Talos: BridgeSTPConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/network/bridgeconfig |
| `spec.cni.custom.documents[].bridgeConfig.stp.enabled` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].bridgeConfig.up` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].bridgeConfig.vlan` **(deprecated)** | `object` | no | Talos: BridgeVLANConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/network/bridgeconfig |
| `spec.cni.custom.documents[].bridgeConfig.vlan.filtering` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config` **(deprecated)** | `object` | no | config is a Talos v1alpha1 Config document; populated when kind is "Config". |
| `spec.cni.custom.documents[].config.cluster` **(deprecated)** | `object` | no | Talos: ClusterConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.cluster.acceptedCAs` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].config.cluster.acceptedCAs[].crt` **(deprecated)** | `string (byte)` | yes |  |
| `spec.cni.custom.documents[].config.cluster.adminKubeconfig` **(deprecated)** | `object` | no | Talos: AdminKubeconfigConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.cluster.adminKubeconfig.certLifetime` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.cluster.allowSchedulingOnControlPlanes` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.cluster.allowSchedulingOnMasters` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.cluster.apiServer` **(deprecated)** | `object` | no | Talos: APIServerConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.cluster.apiServer.admissionControl` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].config.cluster.apiServer.admissionControl[].name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].config.cluster.apiServer.auditPolicy` **(deprecated)** | `object` | no | Policy defines the configuration of audit logging, and the rules for how different request categories are logged. |
| `spec.cni.custom.documents[].config.cluster.apiServer.auditPolicy.apiVersion` **(deprecated)** | `string` | no | APIVersion defines the versioned schema of this representation of an object. Servers should convert recognized schemas to the latest internal value, and may reject unrecognized values. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |
| `spec.cni.custom.documents[].config.cluster.apiServer.auditPolicy.kind` **(deprecated)** | `string` | no | Kind is a string value representing the REST resource this object represents. Servers may infer this from the endpoint the client submits requests to. Cannot be updated. In CamelCase. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |
| `spec.cni.custom.documents[].config.cluster.apiServer.auditPolicy.metadata` **(deprecated)** | `object` | no | ObjectMeta is included for interoperability with API infrastructure. |
| `spec.cni.custom.documents[].config.cluster.apiServer.auditPolicy.omitManagedFields` **(deprecated)** | `boolean` | no | OmitManagedFields indicates whether to omit the managed fields of the request and response bodies from being written to the API audit log. This is used as a global default - a value of 'true' will omit the managed fileds, otherwise the managed fields will be included in the API audit log. Note that this can also be specified per rule in which case the value specified in a rule will override the global default. |
| `spec.cni.custom.documents[].config.cluster.apiServer.auditPolicy.omitStages` **(deprecated)** | `[]string` | no | OmitStages is a list of stages for which no events are created. Note that this can also be specified per rule in which case the union of both are omitted. |
| `spec.cni.custom.documents[].config.cluster.apiServer.auditPolicy.rules` **(deprecated)** | `[]object` | yes | Rules specify the audit Level a request should be recorded at. A request may match multiple rules, in which case the FIRST matching rule is used. The default audit level is None, but can be overridden by a catch-all rule at the end of the list. PolicyRules are strictly ordered. |
| `spec.cni.custom.documents[].config.cluster.apiServer.auditPolicy.rules[].level` **(deprecated)** | `string` | yes | The Level that requests matching this rule are recorded at. |
| `spec.cni.custom.documents[].config.cluster.apiServer.auditPolicy.rules[].namespaces` **(deprecated)** | `[]string` | no | Namespaces that this rule matches. The empty string "" matches non-namespaced resources. An empty list implies every namespace. |
| `spec.cni.custom.documents[].config.cluster.apiServer.auditPolicy.rules[].nonResourceURLs` **(deprecated)** | `[]string` | no | NonResourceURLs is a set of URL paths that should be audited. `*`s are allowed, but only as the full, final step in the path. Examples: - `/metrics` - Log requests for apiserver metrics - `/healthz*` - Log all health checks |
| `spec.cni.custom.documents[].config.cluster.apiServer.auditPolicy.rules[].omitManagedFields` **(deprecated)** | `boolean` | no | OmitManagedFields indicates whether to omit the managed fields of the request and response bodies from being written to the API audit log. - a value of 'true' will drop the managed fields from the API audit log - a value of 'false' indicates that the managed fileds should be included in the API audit log Note that the value, if specified, in this rule will override the global default If a value is not specified then the global default specified in Policy.OmitManagedFields will stand. |
| `spec.cni.custom.documents[].config.cluster.apiServer.auditPolicy.rules[].omitStages` **(deprecated)** | `[]string` | no | OmitStages is a list of stages for which no events are created. Note that this can also be specified policy wide in which case the union of both are omitted. An empty list means no restrictions will apply. |
| `spec.cni.custom.documents[].config.cluster.apiServer.auditPolicy.rules[].resources` **(deprecated)** | `[]object` | no | Resources that this rule matches. An empty list implies all kinds in all API groups. |
| `spec.cni.custom.documents[].config.cluster.apiServer.auditPolicy.rules[].resources[].group` **(deprecated)** | `string` | no | Group is the name of the API group that contains the resources. The empty string represents the core API group. `*` matches all groups |
| `spec.cni.custom.documents[].config.cluster.apiServer.auditPolicy.rules[].resources[].resourceNames` **(deprecated)** | `[]string` | no | ResourceNames is a list of resource instance names that the policy matches. Using this field requires Resources to be specified. An empty list implies that every instance of the resource is matched. |
| `spec.cni.custom.documents[].config.cluster.apiServer.auditPolicy.rules[].resources[].resources` **(deprecated)** | `[]string` | no | Resources is a list of resources this rule applies to. For example: - `pods` matches pods. - `pods/log` matches the log subresource of pods. - `*` matches all resources and their subresources. - `pods/*` matches all subresources of pods. - `*/scale` matches all scale subresources. If wildcard is present, the validation rule will ensure resources do not overlap with each other. An empty list implies all resources and subresources in this API groups apply. |
| `spec.cni.custom.documents[].config.cluster.apiServer.auditPolicy.rules[].userGroups` **(deprecated)** | `[]string` | no | The user groups this rule applies to. A user is considered matching if it is a member of any of the UserGroups. An empty list implies every user group. |
| `spec.cni.custom.documents[].config.cluster.apiServer.auditPolicy.rules[].users` **(deprecated)** | `[]string` | no | The users (by authenticated user name) this rule applies to. An empty list implies every user. |
| `spec.cni.custom.documents[].config.cluster.apiServer.auditPolicy.rules[].verbs` **(deprecated)** | `[]string` | no | The verbs that match this rule. An empty list implies every verb. |
| `spec.cni.custom.documents[].config.cluster.apiServer.authorizationConfig` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].config.cluster.apiServer.authorizationConfig[].name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].config.cluster.apiServer.authorizationConfig[].type` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].config.cluster.apiServer.authorizationConfig[].webhook` **(deprecated)** | `object` | no |  |
| `spec.cni.custom.documents[].config.cluster.apiServer.authorizationConfig[].webhook.authorizedTTL` **(deprecated)** | `string` | yes | The duration to cache 'authorized' responses from the webhook authorizer. Same as setting `--authorization-webhook-cache-authorized-ttl` flag Default: 5m0s |
| `spec.cni.custom.documents[].config.cluster.apiServer.authorizationConfig[].webhook.cacheAuthorizedRequests` **(deprecated)** | `boolean` | no | CacheAuthorizedRequests specifies whether authorized requests should be cached. If set to true, the TTL for cached decisions can be configured via the AuthorizedTTL field. Default: true |
| `spec.cni.custom.documents[].config.cluster.apiServer.authorizationConfig[].webhook.cacheUnauthorizedRequests` **(deprecated)** | `boolean` | no | CacheUnauthorizedRequests specifies whether unauthorized requests should be cached. If set to true, the TTL for cached decisions can be configured via the UnauthorizedTTL field. Default: true |
| `spec.cni.custom.documents[].config.cluster.apiServer.authorizationConfig[].webhook.connectionInfo` **(deprecated)** | `object` | yes | ConnectionInfo defines how we talk to the webhook |
| `spec.cni.custom.documents[].config.cluster.apiServer.authorizationConfig[].webhook.connectionInfo.kubeConfigFile` **(deprecated)** | `string` | yes | Path to KubeConfigFile for connection info Required, if connectionInfo.Type is KubeConfig |
| `spec.cni.custom.documents[].config.cluster.apiServer.authorizationConfig[].webhook.connectionInfo.type` **(deprecated)** | `string` | yes | Controls how the webhook should communicate with the server. Valid values: - KubeConfigFile: use the file specified in kubeConfigFile to locate the server. - InClusterConfig: use the in-cluster configuration to call the SubjectAccessReview API hosted by kube-apiserver. This mode is not allowed for kube-apiserver. |
| `spec.cni.custom.documents[].config.cluster.apiServer.authorizationConfig[].webhook.failurePolicy` **(deprecated)** | `string` | yes | Controls the authorization decision when a webhook request fails to complete or returns a malformed response or errors evaluating matchConditions. Valid values: - NoOpinion: continue to subsequent authorizers to see if one of them allows the request - Deny: reject the request without consulting subsequent authorizers Required, with no default. |
| `spec.cni.custom.documents[].config.cluster.apiServer.authorizationConfig[].webhook.matchConditionSubjectAccessReviewVersion` **(deprecated)** | `string` | yes | MatchConditionSubjectAccessReviewVersion specifies the SubjectAccessReview version the CEL expressions are evaluated against Valid values: v1 Required, no default value |
| `spec.cni.custom.documents[].config.cluster.apiServer.authorizationConfig[].webhook.matchConditions` **(deprecated)** | `[]object` | yes | matchConditions is a list of conditions that must be met for a request to be sent to this webhook. An empty list of matchConditions matches all requests. There are a maximum of 64 match conditions allowed. The exact matching logic is (in order): 1. If at least one matchCondition evaluates to FALSE, then the webhook is skipped. 2. If ALL matchConditions evaluate to TRUE, then the webhook is called. 3. If at least one matchCondition evaluates to an error (but none are FALSE): - If failurePolicy=Deny, then the webhook rejects the request - If failurePolicy=NoOpinion, then the error is ignored and the webhook is skipped |
| `spec.cni.custom.documents[].config.cluster.apiServer.authorizationConfig[].webhook.matchConditions[].expression` **(deprecated)** | `string` | yes | expression represents the expression which will be evaluated by CEL. Must evaluate to bool. CEL expressions have access to the contents of the SubjectAccessReview in v1 version. If version specified by subjectAccessReviewVersion in the request variable is v1beta1, the contents would be converted to the v1 version before evaluating the CEL expression. - 'resourceAttributes' describes information for a resource access request and is unset for non-resource requests. e.g. has(request.resourceAttributes) && request.resourceAttributes.namespace == 'default' - 'nonResourceAttributes' describes information for a non-resource access request and is unset for resource requests. e.g. has(request.nonResourceAttributes) && request.nonResourceAttributes.path == '/healthz'. - 'user' is the user to test for. e.g. request.user == 'alice' - 'groups' is the groups to test for. e.g. ('group1' in request.groups) - 'extra' corresponds to the user.Info.GetExtra() method from the authenticator. - 'uid' is the information about the requesting user. e.g. request.uid == '1' Documentation on CEL: https://kubernetes.io/docs/reference/using-api/cel/ |
| `spec.cni.custom.documents[].config.cluster.apiServer.authorizationConfig[].webhook.subjectAccessReviewVersion` **(deprecated)** | `string` | yes | The API version of the authorization.k8s.io SubjectAccessReview to send to and expect from the webhook. Same as setting `--authorization-webhook-version` flag Valid values: v1beta1, v1 Required, no default value |
| `spec.cni.custom.documents[].config.cluster.apiServer.authorizationConfig[].webhook.timeout` **(deprecated)** | `string` | yes | Timeout for the webhook request Maximum allowed value is 30s. Required, no default value. |
| `spec.cni.custom.documents[].config.cluster.apiServer.authorizationConfig[].webhook.unauthorizedTTL` **(deprecated)** | `string` | yes | The duration to cache 'unauthorized' responses from the webhook authorizer. Same as setting `--authorization-webhook-cache-unauthorized-ttl` flag Default: 30s |
| `spec.cni.custom.documents[].config.cluster.apiServer.certSANs` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].config.cluster.apiServer.disablePodSecurityPolicy` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.cluster.apiServer.env` **(deprecated)** | `map[string]string` | no |  |
| `spec.cni.custom.documents[].config.cluster.apiServer.extraArgs` **(deprecated)** | `map[string][]string` | no |  |
| `spec.cni.custom.documents[].config.cluster.apiServer.extraVolumes` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].config.cluster.apiServer.extraVolumes[].hostPath` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].config.cluster.apiServer.extraVolumes[].mountPath` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].config.cluster.apiServer.extraVolumes[].readonly` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.cluster.apiServer.image` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.cluster.apiServer.resources` **(deprecated)** | `object` | no | Talos: ResourcesConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.cluster.apiServer.resources.limits` **(deprecated)** | `map[string]integer or string` | no | ResourceList is a set of (resource name, quantity) pairs. |
| `spec.cni.custom.documents[].config.cluster.apiServer.resources.requests` **(deprecated)** | `map[string]integer or string` | no | ResourceList is a set of (resource name, quantity) pairs. |
| `spec.cni.custom.documents[].config.cluster.clusterName` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.cluster.controlPlane` **(deprecated)** | `object` | no | Talos: ControlPlaneConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.cluster.controlPlane.endpoint` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].config.cluster.controlPlane.localAPIServerPort` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].config.cluster.controllerManager` **(deprecated)** | `object` | no | Talos: ControllerManagerConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.cluster.controllerManager.env` **(deprecated)** | `map[string]string` | no |  |
| `spec.cni.custom.documents[].config.cluster.controllerManager.extraArgs` **(deprecated)** | `map[string][]string` | no |  |
| `spec.cni.custom.documents[].config.cluster.controllerManager.extraVolumes` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].config.cluster.controllerManager.extraVolumes[].hostPath` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].config.cluster.controllerManager.extraVolumes[].mountPath` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].config.cluster.controllerManager.extraVolumes[].readonly` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.cluster.controllerManager.image` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.cluster.controllerManager.resources` **(deprecated)** | `object` | no | Talos: ResourcesConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.cluster.controllerManager.resources.limits` **(deprecated)** | `map[string]integer or string` | no | ResourceList is a set of (resource name, quantity) pairs. |
| `spec.cni.custom.documents[].config.cluster.controllerManager.resources.requests` **(deprecated)** | `map[string]integer or string` | no | ResourceList is a set of (resource name, quantity) pairs. |
| `spec.cni.custom.documents[].config.cluster.coreDNS` **(deprecated)** | `object` | no | Talos: CoreDNS — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.cluster.coreDNS.disabled` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.cluster.coreDNS.image` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.cluster.discovery` **(deprecated)** | `object` | no | Talos: ClusterDiscoveryConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.cluster.discovery.enabled` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.cluster.discovery.registries` **(deprecated)** | `object` | yes | Talos: DiscoveryRegistriesConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.cluster.discovery.registries.kubernetes` **(deprecated)** | `object` | yes | Talos: RegistryKubernetesConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.cluster.discovery.registries.kubernetes.disabled` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.cluster.discovery.registries.service` **(deprecated)** | `object` | yes | Talos: RegistryServiceConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.cluster.discovery.registries.service.disabled` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.cluster.discovery.registries.service.endpoint` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.cluster.etcd` **(deprecated)** | `object` | no | Talos: EtcdConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.cluster.etcd.advertisedSubnets` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].config.cluster.etcd.extraArgs` **(deprecated)** | `map[string][]string` | no |  |
| `spec.cni.custom.documents[].config.cluster.etcd.image` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.cluster.etcd.listenSubnets` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].config.cluster.etcd.subnet` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.cluster.externalCloudProvider` **(deprecated)** | `object` | no | Talos: ExternalCloudProviderConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.cluster.externalCloudProvider.enabled` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.cluster.externalCloudProvider.manifests` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].config.cluster.extraManifests` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].config.cluster.id` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.cluster.inlineManifests` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].config.cluster.inlineManifests[].contents` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].config.cluster.inlineManifests[].name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].config.cluster.network` **(deprecated)** | `object` | no | Talos: ClusterNetworkConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.cluster.network.cni` **(deprecated)** | `object` | no | Talos: CNIConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.cluster.network.cni.flannel` **(deprecated)** | `object` | no | Talos: FlannelCNIConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.cluster.network.cni.flannel.extraArgs` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].config.cluster.network.cni.flannel.kubeNetworkPoliciesEnabled` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.cluster.network.cni.name` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.cluster.network.cni.urls` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].config.cluster.network.dnsDomain` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].config.cluster.network.podSubnets` **(deprecated)** | `[]string` | yes |  |
| `spec.cni.custom.documents[].config.cluster.network.serviceSubnets` **(deprecated)** | `[]string` | yes |  |
| `spec.cni.custom.documents[].config.cluster.proxy` **(deprecated)** | `object` | no | Talos: ProxyConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.cluster.proxy.disabled` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.cluster.proxy.extraArgs` **(deprecated)** | `map[string][]string` | no |  |
| `spec.cni.custom.documents[].config.cluster.proxy.image` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.cluster.proxy.mode` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.cluster.scheduler` **(deprecated)** | `object` | no | Talos: SchedulerConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.cluster.scheduler.env` **(deprecated)** | `map[string]string` | no |  |
| `spec.cni.custom.documents[].config.cluster.scheduler.extraArgs` **(deprecated)** | `map[string][]string` | no |  |
| `spec.cni.custom.documents[].config.cluster.scheduler.extraVolumes` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].config.cluster.scheduler.extraVolumes[].hostPath` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].config.cluster.scheduler.extraVolumes[].mountPath` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].config.cluster.scheduler.extraVolumes[].readonly` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.cluster.scheduler.image` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.cluster.scheduler.resources` **(deprecated)** | `object` | no | Talos: ResourcesConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.cluster.scheduler.resources.limits` **(deprecated)** | `map[string]integer or string` | no | ResourceList is a set of (resource name, quantity) pairs. |
| `spec.cni.custom.documents[].config.cluster.scheduler.resources.requests` **(deprecated)** | `map[string]integer or string` | no | ResourceList is a set of (resource name, quantity) pairs. |
| `spec.cni.custom.documents[].config.debug` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine` **(deprecated)** | `object` | no | Talos: MachineConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.acceptedCAs` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].config.machine.acceptedCAs[].crt` **(deprecated)** | `string (byte)` | yes |  |
| `spec.cni.custom.documents[].config.machine.certSANs` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].config.machine.controlPlane` **(deprecated)** | `object` | no | Talos: MachineControlPlaneConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.controlPlane.controllerManager` **(deprecated)** | `object` | no | Talos: MachineControllerManagerConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.controlPlane.controllerManager.disabled` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.controlPlane.scheduler` **(deprecated)** | `object` | no | Talos: MachineSchedulerConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.controlPlane.scheduler.disabled` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.disks` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].config.machine.disks[].device` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.disks[].partitions` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].config.machine.disks[].partitions[].mountpoint` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.disks[].partitions[].size` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.env` **(deprecated)** | `map[string]string` | no |  |
| `spec.cni.custom.documents[].config.machine.features` **(deprecated)** | `object` | no | Talos: FeaturesConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.features.apidCheckExtKeyUsage` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.features.diskQuotaSupport` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.features.hostDNS` **(deprecated)** | `object` | no | Talos: HostDNSConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.features.hostDNS.enabled` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.features.hostDNS.forwardKubeDNSToHost` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.features.hostDNS.resolveMemberNames` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.features.imageCache` **(deprecated)** | `object` | no | Talos: ImageCacheConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.features.imageCache.localEnabled` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.features.kubePrism` **(deprecated)** | `object` | no | Talos: KubePrism — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.features.kubePrism.enabled` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.features.kubePrism.port` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].config.machine.features.kubernetesTalosAPIAccess` **(deprecated)** | `object` | no | Talos: KubernetesTalosAPIAccessConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.features.kubernetesTalosAPIAccess.allowedKubernetesNamespaces` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].config.machine.features.kubernetesTalosAPIAccess.allowedRoles` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].config.machine.features.kubernetesTalosAPIAccess.enabled` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.features.nodeAddressSortAlgorithm` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.features.rbac` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.features.stableHostname` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.files` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].config.machine.files[].content` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].config.machine.files[].op` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].config.machine.files[].path` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].config.machine.files[].permissions` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].config.machine.install` **(deprecated)** | `object` | no | Talos: InstallConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.install.bootloader` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.install.disk` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.install.diskSelector` **(deprecated)** | `object` | no | Talos: InstallDiskSelector — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.install.diskSelector.busPath` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.install.diskSelector.modalias` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.install.diskSelector.model` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.install.diskSelector.name` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.install.diskSelector.serial` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.install.diskSelector.size` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.install.diskSelector.type` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.install.diskSelector.uuid` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.install.diskSelector.wwid` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.install.extensions` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].config.machine.install.extensions[].image` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].config.machine.install.extraKernelArgs` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].config.machine.install.grubUseUKICmdline` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.install.image` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.install.legacyBIOSSupport` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.install.wipe` **(deprecated)** | `boolean` | yes |  |
| `spec.cni.custom.documents[].config.machine.kernel` **(deprecated)** | `object` | no | Talos: KernelConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.kernel.modules` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].config.machine.kernel.modules[].name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].config.machine.kernel.modules[].parameters` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].config.machine.kubelet` **(deprecated)** | `object` | no | Talos: KubeletConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.kubelet.clusterDNS` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].config.machine.kubelet.credentialProviderConfig` **(deprecated)** | `object` | no | CredentialProviderConfig is the configuration containing information about each exec credential provider. Kubelet reads this configuration from disk and enables each provider as specified by the CredentialProvider type. |
| `spec.cni.custom.documents[].config.machine.kubelet.credentialProviderConfig.apiVersion` **(deprecated)** | `string` | no | APIVersion defines the versioned schema of this representation of an object. Servers should convert recognized schemas to the latest internal value, and may reject unrecognized values. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |
| `spec.cni.custom.documents[].config.machine.kubelet.credentialProviderConfig.kind` **(deprecated)** | `string` | no | Kind is a string value representing the REST resource this object represents. Servers may infer this from the endpoint the client submits requests to. Cannot be updated. In CamelCase. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |
| `spec.cni.custom.documents[].config.machine.kubelet.credentialProviderConfig.providers` **(deprecated)** | `[]object` | yes | providers is a list of credential provider plugins that will be enabled by the kubelet. Multiple providers may match against a single image, in which case credentials from all providers will be returned to the kubelet. If multiple providers are called for a single image, the results are combined. If providers return overlapping auth keys, the value from the provider earlier in this list is attempted first. |
| `spec.cni.custom.documents[].config.machine.kubelet.credentialProviderConfig.providers[].apiVersion` **(deprecated)** | `string` | yes | Required input version of the exec CredentialProviderRequest. The returned CredentialProviderResponse MUST use the same encoding version as the input. Current supported values are: - credentialprovider.kubelet.k8s.io/v1 |
| `spec.cni.custom.documents[].config.machine.kubelet.credentialProviderConfig.providers[].args` **(deprecated)** | `[]string` | no | Arguments to pass to the command when executing it. |
| `spec.cni.custom.documents[].config.machine.kubelet.credentialProviderConfig.providers[].defaultCacheDuration` **(deprecated)** | `string` | yes | defaultCacheDuration is the default duration the plugin will cache credentials in-memory if a cache duration is not provided in the plugin response. This field is required. |
| `spec.cni.custom.documents[].config.machine.kubelet.credentialProviderConfig.providers[].env` **(deprecated)** | `[]object` | no | Env defines additional environment variables to expose to the process. These are unioned with the host's environment, as well as variables client-go uses to pass argument to the plugin. |
| `spec.cni.custom.documents[].config.machine.kubelet.credentialProviderConfig.providers[].env[].name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].config.machine.kubelet.credentialProviderConfig.providers[].env[].value` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].config.machine.kubelet.credentialProviderConfig.providers[].matchImages` **(deprecated)** | `[]string` | yes | matchImages is a required list of strings used to match against images in order to determine if this provider should be invoked. If one of the strings matches the requested image from the kubelet, the plugin will be invoked and given a chance to provide credentials. Images are expected to contain the registry domain and URL path. Each entry in matchImages is a pattern which can optionally contain a port and a path. Globs can be used in the domain, but not in the port or the path. Globs are supported as subdomains like '*.k8s.io' or 'k8s.*.io', and top-level-domains such as 'k8s.*'. Matching partial subdomains like 'app*.k8s.io' is also supported. Each glob can only match a single subdomain segment, so *.io does not match *.k8s.io. A match exists between an image and a matchImage when all of the below are true: - Both contain the same number of domain parts and each part matches. - The URL path of an imageMatch must be a prefix of the target image URL path. - If the imageMatch contains a port, then the port must match in the image as well. Example values of matchImages: - 123456789.dkr.ecr.us-east-1.amazonaws.com - *.azurecr.io - gcr.io - *.*.registry.io - registry.io:8080/path |
| `spec.cni.custom.documents[].config.machine.kubelet.credentialProviderConfig.providers[].name` **(deprecated)** | `string` | yes | name is the required name of the credential provider. It must match the name of the provider executable as seen by the kubelet. The executable must be in the kubelet's bin directory (set by the --image-credential-provider-bin-dir flag). Required to be unique across all providers. |
| `spec.cni.custom.documents[].config.machine.kubelet.credentialProviderConfig.providers[].tokenAttributes` **(deprecated)** | `object` | no | tokenAttributes is the configuration for the service account token that will be passed to the plugin. The credential provider opts in to using service account tokens for image pull by setting this field. When this field is set, kubelet will generate a service account token bound to the pod for which the image is being pulled and pass to the plugin as part of CredentialProviderRequest along with other attributes required by the plugin. The service account metadata and token attributes will be used as a dimension to cache the credentials in kubelet. The cache key is generated by combining the service account metadata (namespace, name, UID, and annotations key+value for the keys defined in serviceAccountTokenAttribute.requiredServiceAccountAnnotationKeys and serviceAccountTokenAttribute.optionalServiceAccountAnnotationKeys). The pod metadata (namespace, name, UID) that are in the service account token are not used as a dimension to cache the credentials in kubelet. This means workloads that are using the same service account could end up using the same credentials for image pull. For plugins that don't want this behavior, or plugins that operate in pass-through mode; i.e., they return the service account token as-is, they can set the credentialProviderResponse.cacheDuration to 0. This will disable the caching of credentials in kubelet and the plugin will be invoked for every image pull. This does result in token generation overhead for every image pull, but it is the only way to ensure that the credentials are not shared across pods (even if they are using the same service account). |
| `spec.cni.custom.documents[].config.machine.kubelet.credentialProviderConfig.providers[].tokenAttributes.cacheType` **(deprecated)** | `string` | yes | cacheType indicates the type of cache key use for caching the credentials returned by the plugin when the service account token is used. The most conservative option is to set this to "Token", which means the kubelet will cache returned credentials on a per-token basis. This should be set if the returned credential's lifetime is limited to the service account token's lifetime. If the plugin's credential retrieval logic depends only on the service account and not on pod-specific claims, then the plugin can set this to "ServiceAccount". In this case, the kubelet will cache returned credentials on a per-serviceaccount basis. Use this when the returned credential is valid for all pods using the same service account. |
| `spec.cni.custom.documents[].config.machine.kubelet.credentialProviderConfig.providers[].tokenAttributes.optionalServiceAccountAnnotationKeys` **(deprecated)** | `[]string` | no | optionalServiceAccountAnnotationKeys is the list of annotation keys that the plugin is interested in and that are optional to be present in the service account. The keys defined in this list will be extracted from the corresponding service account and passed to the plugin as part of the CredentialProviderRequest. The plugin is responsible for validating the existence of annotations and their values. This field is optional and may be empty. Plugins may use this field to extract additional information required to fetch credentials. Keys in this list must be unique. |
| `spec.cni.custom.documents[].config.machine.kubelet.credentialProviderConfig.providers[].tokenAttributes.requireServiceAccount` **(deprecated)** | `boolean` | yes | requireServiceAccount indicates whether the plugin requires the pod to have a service account. If set to true, kubelet will only invoke the plugin if the pod has a service account. If set to false, kubelet will invoke the plugin even if the pod does not have a service account and will not include a token in the CredentialProviderRequest in that scenario. This is useful for plugins that are used to pull images for pods without service accounts (e.g., static pods). |
| `spec.cni.custom.documents[].config.machine.kubelet.credentialProviderConfig.providers[].tokenAttributes.requiredServiceAccountAnnotationKeys` **(deprecated)** | `[]string` | no | requiredServiceAccountAnnotationKeys is the list of annotation keys that the plugin is interested in and that are required to be present in the service account. The keys defined in this list will be extracted from the corresponding service account and passed to the plugin as part of the CredentialProviderRequest. If any of the keys defined in this list are not present in the service account, kubelet will not invoke the plugin and will return an error. This field is optional and may be empty. Plugins may use this field to extract additional information required to fetch credentials or allow workloads to opt in to using service account tokens for image pull. If non-empty, requireServiceAccount must be set to true. Keys in this list must be unique. This list needs to be mutually exclusive with optionalServiceAccountAnnotationKeys. |
| `spec.cni.custom.documents[].config.machine.kubelet.credentialProviderConfig.providers[].tokenAttributes.serviceAccountTokenAudience` **(deprecated)** | `string` | yes | serviceAccountTokenAudience is the intended audience for the projected service account token. |
| `spec.cni.custom.documents[].config.machine.kubelet.defaultRuntimeSeccompProfileEnabled` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.kubelet.disableManifestsDirectory` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.kubelet.extraArgs` **(deprecated)** | `map[string][]string` | no |  |
| `spec.cni.custom.documents[].config.machine.kubelet.extraMounts` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].config.machine.kubelet.extraMounts[].destination` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].config.machine.kubelet.extraMounts[].gidMappings` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].config.machine.kubelet.extraMounts[].gidMappings[].containerID` **(deprecated)** | `integer` | yes |  |
| `spec.cni.custom.documents[].config.machine.kubelet.extraMounts[].gidMappings[].hostID` **(deprecated)** | `integer` | yes |  |
| `spec.cni.custom.documents[].config.machine.kubelet.extraMounts[].gidMappings[].size` **(deprecated)** | `integer` | yes |  |
| `spec.cni.custom.documents[].config.machine.kubelet.extraMounts[].options` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].config.machine.kubelet.extraMounts[].source` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.kubelet.extraMounts[].type` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.kubelet.extraMounts[].uidMappings` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].config.machine.kubelet.extraMounts[].uidMappings[].containerID` **(deprecated)** | `integer` | yes |  |
| `spec.cni.custom.documents[].config.machine.kubelet.extraMounts[].uidMappings[].hostID` **(deprecated)** | `integer` | yes |  |
| `spec.cni.custom.documents[].config.machine.kubelet.extraMounts[].uidMappings[].size` **(deprecated)** | `integer` | yes |  |
| `spec.cni.custom.documents[].config.machine.kubelet.image` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.kubelet.nodeIP` **(deprecated)** | `object` | no | Talos: KubeletNodeIPConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.kubelet.nodeIP.validSubnets` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].config.machine.kubelet.registerWithFQDN` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.kubelet.skipNodeRegistration` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.logging` **(deprecated)** | `object` | no | Talos: LoggingConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.logging.destinations` **(deprecated)** | `[]object` | yes |  |
| `spec.cni.custom.documents[].config.machine.logging.destinations[].endpoint` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].config.machine.logging.destinations[].extraTags` **(deprecated)** | `map[string]string` | no |  |
| `spec.cni.custom.documents[].config.machine.logging.destinations[].format` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].config.machine.network` **(deprecated)** | `object` | no | Talos: NetworkConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.network.disableSearchDomain` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.network.extraHostEntries` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].config.machine.network.extraHostEntries[].aliases` **(deprecated)** | `[]string` | yes |  |
| `spec.cni.custom.documents[].config.machine.network.extraHostEntries[].ip` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].config.machine.network.hostname` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].addresses` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bond` **(deprecated)** | `object` | no | Talos: Bond — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bond.adActorSysPrio` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bond.adActorSystem` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bond.adSelect` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bond.adUserPortKey` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bond.allSlavesActive` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bond.arpAllTargets` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bond.arpIPTarget` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bond.arpInterval` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bond.arpValidate` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bond.deviceSelectors` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bond.deviceSelectors[].busPath` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bond.deviceSelectors[].driver` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bond.deviceSelectors[].hardwareAddr` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bond.deviceSelectors[].pciID` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bond.deviceSelectors[].permanentAddr` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bond.deviceSelectors[].physical` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bond.downdelay` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bond.failOverMac` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bond.interfaces` **(deprecated)** | `[]string` | yes |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bond.lacpRate` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bond.lpInterval` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bond.miimon` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bond.minLinks` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bond.mode` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bond.numPeerNotif` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bond.packetsPerSlave` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bond.peerNotifyDelay` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bond.primary` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bond.primaryReselect` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bond.resendIgmp` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bond.tlbDynamicLb` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bond.updelay` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bond.useCarrier` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bond.xmitHashPolicy` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bridge` **(deprecated)** | `object` | no | Talos: Bridge — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bridge.interfaces` **(deprecated)** | `[]string` | yes |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bridge.stp` **(deprecated)** | `object` | no | Talos: STP — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bridge.stp.enabled` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bridge.vlan` **(deprecated)** | `object` | no | Talos: BridgeVLAN — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bridge.vlan.vlanFiltering` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bridgePort` **(deprecated)** | `object` | no | Talos: BridgePort — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].bridgePort.master` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].cidr` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].deviceSelector` **(deprecated)** | `object` | no | Talos: NetworkDeviceSelector — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].deviceSelector.busPath` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].deviceSelector.driver` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].deviceSelector.hardwareAddr` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].deviceSelector.pciID` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].deviceSelector.permanentAddr` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].deviceSelector.physical` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].dhcp` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].dhcpOptions` **(deprecated)** | `object` | no | Talos: DHCPOptions — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].dhcpOptions.duidv6` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].dhcpOptions.ipv4` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].dhcpOptions.ipv6` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].dhcpOptions.routeMetric` **(deprecated)** | `integer` | yes |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].dummy` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].ignore` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].interface` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].mtu` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].routes` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].routes[].gateway` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].routes[].metric` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].routes[].mtu` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].routes[].network` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].routes[].source` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].vip` **(deprecated)** | `object` | no | Talos: DeviceVIPConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].vip.equinixMetal` **(deprecated)** | `object` | no | Talos: VIPEquinixMetalConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].vip.hcloud` **(deprecated)** | `object` | no | Talos: VIPHCloudConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].vip.ip` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].vlans` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].vlans[].addresses` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].vlans[].cidr` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].vlans[].dhcp` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].vlans[].dhcpOptions` **(deprecated)** | `object` | no | Talos: DHCPOptions — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].vlans[].dhcpOptions.duidv6` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].vlans[].dhcpOptions.ipv4` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].vlans[].dhcpOptions.ipv6` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].vlans[].dhcpOptions.routeMetric` **(deprecated)** | `integer` | yes |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].vlans[].mtu` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].vlans[].routes` **(deprecated)** | `[]object` | yes |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].vlans[].routes[].gateway` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].vlans[].routes[].metric` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].vlans[].routes[].mtu` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].vlans[].routes[].network` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].vlans[].routes[].source` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].vlans[].vip` **(deprecated)** | `object` | no | Talos: DeviceVIPConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].vlans[].vip.equinixMetal` **(deprecated)** | `object` | no | Talos: VIPEquinixMetalConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].vlans[].vip.hcloud` **(deprecated)** | `object` | no | Talos: VIPHCloudConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].vlans[].vip.ip` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].vlans[].vlanId` **(deprecated)** | `integer` | yes |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].wireguard` **(deprecated)** | `object` | no | Talos: DeviceWireguardConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].wireguard.firewallMark` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].wireguard.listenPort` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].wireguard.peers` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].wireguard.peers[].allowedIPs` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].wireguard.peers[].endpoint` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].wireguard.peers[].persistentKeepaliveInterval` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.network.interfaces[].wireguard.peers[].publicKey` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.network.kubespan` **(deprecated)** | `object` | no | Talos: NetworkKubeSpan — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.network.kubespan.advertiseKubernetesNetworks` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.network.kubespan.allowDownPeerBypass` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.network.kubespan.enabled` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.network.kubespan.filters` **(deprecated)** | `object` | no | Talos: KubeSpanFilters — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.network.kubespan.filters.endpoints` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].config.machine.network.kubespan.filters.excludeAdvertisedNetworks` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].config.machine.network.kubespan.harvestExtraEndpoints` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.network.kubespan.mtu` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].config.machine.network.nameservers` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].config.machine.network.searchDomains` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].config.machine.nodeAnnotations` **(deprecated)** | `map[string]string` | no |  |
| `spec.cni.custom.documents[].config.machine.nodeLabels` **(deprecated)** | `map[string]string` | no |  |
| `spec.cni.custom.documents[].config.machine.nodeTaints` **(deprecated)** | `map[string]string` | no |  |
| `spec.cni.custom.documents[].config.machine.registries` **(deprecated)** | `object` | no | Talos: RegistriesConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.registries.config` **(deprecated)** | `map[string]object` | no |  |
| `spec.cni.custom.documents[].config.machine.registries.config.<key>.auth` **(deprecated)** | `object` | no | Talos: RegistryAuthConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.registries.config.<key>.auth.username` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.registries.config.<key>.tls` **(deprecated)** | `object` | no | Talos: RegistryTLSConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.registries.config.<key>.tls.ca` **(deprecated)** | `string (byte)` | no |  |
| `spec.cni.custom.documents[].config.machine.registries.config.<key>.tls.insecureSkipVerify` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.registries.mirrors` **(deprecated)** | `map[string]object` | no |  |
| `spec.cni.custom.documents[].config.machine.registries.mirrors.<key>.endpoints` **(deprecated)** | `[]string` | yes |  |
| `spec.cni.custom.documents[].config.machine.registries.mirrors.<key>.overridePath` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.registries.mirrors.<key>.skipFallback` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.seccompProfiles` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].config.machine.seccompProfiles[].name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].config.machine.seccompProfiles[].value` **(deprecated)** | `object` | yes | SeccompProfile is MachineSeccompProfile.MachineSeccompProfileValue's mirror type (internal/tools/mirrorgen/main.go's fieldOverrides): byte-for-byte the same fields and JSON tags as specs.LinuxSeccomp (github.com/opencontainers/ runtime-spec), under a local name so DeepCopyInto/DeepCopy can be hand-written below. The generic conversion helpers in substitution.go and the generated CRD schema are unaffected by the rename -- both work off the JSON shape, which is identical. Why hand-written: specs.LinuxSeccomp has pointer and slice fields but, unlike the other four field overrides (all Kubernetes API types), no DeepCopy of its own; controller-gen's object:generate step assumes one exists for any struct-shaped field type it doesn't otherwise recognize, and errors at compile time rather than generation time when it doesn't. The methods can't live on specs.LinuxSeccomp directly -- Go forbids methods on types from another package -- and can't live one level up, on MachineSeccompProfile itself, either: MachineConfig holds MachineSeccompProfiles []*MachineSeccompProfile, and controller-gen's generated code for a slice of pointers to a type with a *known* DeepCopy method assumes a value-typed element and double-dereferences, which doesn't compile. Putting the hand-written methods here, one level down, avoids both problems: SeccompProfile is reached only through a plain pointer field, never through a pointer slice. deepCopyLinuxSeccomp and deepCopyLinuxSyscall below copy their known pointer/slice fields by name rather than reflecting over the upstream type, so they go stale -- silently, by sharing instead of copying -- if specs-go ever adds one. seccomp_test.go's TestLinuxSeccompShape guards against that by pinning the exact field set each one assumes. |
| `spec.cni.custom.documents[].config.machine.seccompProfiles[].value.architectures` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].config.machine.seccompProfiles[].value.defaultAction` **(deprecated)** | `string` | yes | LinuxSeccompAction taken upon Seccomp rule match |
| `spec.cni.custom.documents[].config.machine.seccompProfiles[].value.defaultErrnoRet` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].config.machine.seccompProfiles[].value.flags` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].config.machine.seccompProfiles[].value.listenerMetadata` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.seccompProfiles[].value.listenerPath` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.seccompProfiles[].value.syscalls` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].config.machine.seccompProfiles[].value.syscalls[].action` **(deprecated)** | `string` | yes | LinuxSeccompAction taken upon Seccomp rule match |
| `spec.cni.custom.documents[].config.machine.seccompProfiles[].value.syscalls[].args` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].config.machine.seccompProfiles[].value.syscalls[].args[].index` **(deprecated)** | `integer` | yes |  |
| `spec.cni.custom.documents[].config.machine.seccompProfiles[].value.syscalls[].args[].op` **(deprecated)** | `string` | yes | LinuxSeccompOperator used to match syscall arguments in Seccomp |
| `spec.cni.custom.documents[].config.machine.seccompProfiles[].value.syscalls[].args[].value` **(deprecated)** | `integer` | yes |  |
| `spec.cni.custom.documents[].config.machine.seccompProfiles[].value.syscalls[].args[].valueTwo` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].config.machine.seccompProfiles[].value.syscalls[].errnoRet` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].config.machine.seccompProfiles[].value.syscalls[].names` **(deprecated)** | `[]string` | yes |  |
| `spec.cni.custom.documents[].config.machine.sysctls` **(deprecated)** | `map[string]string` | no |  |
| `spec.cni.custom.documents[].config.machine.sysfs` **(deprecated)** | `map[string]string` | no |  |
| `spec.cni.custom.documents[].config.machine.systemDiskEncryption` **(deprecated)** | `object` | no | Talos: SystemDiskEncryptionConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.systemDiskEncryption.ephemeral` **(deprecated)** | `object` | no | Talos: EncryptionConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.systemDiskEncryption.ephemeral.blockSize` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].config.machine.systemDiskEncryption.ephemeral.cipher` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.systemDiskEncryption.ephemeral.keySize` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].config.machine.systemDiskEncryption.ephemeral.keys` **(deprecated)** | `[]object` | yes |  |
| `spec.cni.custom.documents[].config.machine.systemDiskEncryption.ephemeral.keys[].kms` **(deprecated)** | `object` | no | Talos: EncryptionKeyKMS — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.systemDiskEncryption.ephemeral.keys[].kms.endpoint` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].config.machine.systemDiskEncryption.ephemeral.keys[].nodeID` **(deprecated)** | `object` | no | Talos: EncryptionKeyNodeID — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.systemDiskEncryption.ephemeral.keys[].slot` **(deprecated)** | `integer` | yes |  |
| `spec.cni.custom.documents[].config.machine.systemDiskEncryption.ephemeral.keys[].tpm` **(deprecated)** | `object` | no | Talos: EncryptionKeyTPM — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.systemDiskEncryption.ephemeral.keys[].tpm.checkSecurebootStatusOnEnroll` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.systemDiskEncryption.ephemeral.options` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].config.machine.systemDiskEncryption.ephemeral.provider` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].config.machine.systemDiskEncryption.state` **(deprecated)** | `object` | no | Talos: EncryptionConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.systemDiskEncryption.state.blockSize` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].config.machine.systemDiskEncryption.state.cipher` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.systemDiskEncryption.state.keySize` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].config.machine.systemDiskEncryption.state.keys` **(deprecated)** | `[]object` | yes |  |
| `spec.cni.custom.documents[].config.machine.systemDiskEncryption.state.keys[].kms` **(deprecated)** | `object` | no | Talos: EncryptionKeyKMS — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.systemDiskEncryption.state.keys[].kms.endpoint` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].config.machine.systemDiskEncryption.state.keys[].nodeID` **(deprecated)** | `object` | no | Talos: EncryptionKeyNodeID — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.systemDiskEncryption.state.keys[].slot` **(deprecated)** | `integer` | yes |  |
| `spec.cni.custom.documents[].config.machine.systemDiskEncryption.state.keys[].tpm` **(deprecated)** | `object` | no | Talos: EncryptionKeyTPM — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.systemDiskEncryption.state.keys[].tpm.checkSecurebootStatusOnEnroll` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.systemDiskEncryption.state.options` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].config.machine.systemDiskEncryption.state.provider` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].config.machine.time` **(deprecated)** | `object` | no | Talos: TimeConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.time.bootTimeout` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.time.disabled` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.machine.time.servers` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].config.machine.type` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].config.machine.udev` **(deprecated)** | `object` | no | Talos: UdevConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.cni.custom.documents[].config.machine.udev.rules` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].config.persist` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].config.version` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].containerConfig` **(deprecated)** | `object` | no | containerConfig is a Talos ContainerConfig document; populated when kind is "ContainerConfig". |
| `spec.cni.custom.documents[].containerConfig.args` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].containerConfig.dependsOn` **(deprecated)** | `object` | no | Talos: ContainerDependsOn — https://docs.siderolabs.com/talos/v1.14/reference/configuration/container/containerconfig |
| `spec.cni.custom.documents[].containerConfig.dependsOn.containers` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].containerConfig.dependsOn.networks` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].containerConfig.dependsOn.paths` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].containerConfig.dependsOn.time` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].containerConfig.entrypoint` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].containerConfig.environment` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].containerConfig.image` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].containerConfig.mounts` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].containerConfig.mounts[].hostPath` **(deprecated)** | `object` | no | Talos: HostPathMount — https://docs.siderolabs.com/talos/v1.14/reference/configuration/container/containerconfig |
| `spec.cni.custom.documents[].containerConfig.mounts[].hostPath.destination` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].containerConfig.mounts[].hostPath.options` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].containerConfig.mounts[].hostPath.source` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].containerConfig.mounts[].tmpfs` **(deprecated)** | `object` | no | Talos: TmpfsMount — https://docs.siderolabs.com/talos/v1.14/reference/configuration/container/containerconfig |
| `spec.cni.custom.documents[].containerConfig.mounts[].tmpfs.destination` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].containerConfig.mounts[].tmpfs.options` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].containerConfig.mounts[].tmpfs.size` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].containerConfig.mounts[].userVolume` **(deprecated)** | `object` | no | Talos: UserVolumeMount — https://docs.siderolabs.com/talos/v1.14/reference/configuration/container/containerconfig |
| `spec.cni.custom.documents[].containerConfig.mounts[].userVolume.destination` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].containerConfig.mounts[].userVolume.name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].containerConfig.mounts[].userVolume.options` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].containerConfig.name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].containerConfig.network` **(deprecated)** | `object` | no | Talos: ContainerNetwork — https://docs.siderolabs.com/talos/v1.14/reference/configuration/container/containerconfig |
| `spec.cni.custom.documents[].containerConfig.network.mode` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].containerConfig.resources` **(deprecated)** | `object` | no | Talos: ContainerResources — https://docs.siderolabs.com/talos/v1.14/reference/configuration/container/containerconfig |
| `spec.cni.custom.documents[].containerConfig.resources.limits` **(deprecated)** | `object` | no | Talos: ContainerResourceLimits — https://docs.siderolabs.com/talos/v1.14/reference/configuration/container/containerconfig |
| `spec.cni.custom.documents[].containerConfig.resources.limits.cpu` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].containerConfig.resources.limits.memory` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].containerConfig.runAs` **(deprecated)** | `object` | no | Talos: ContainerRunAs — https://docs.siderolabs.com/talos/v1.14/reference/configuration/container/containerconfig |
| `spec.cni.custom.documents[].containerConfig.runAs.gid` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].containerConfig.runAs.uid` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].containerConfig.security` **(deprecated)** | `object` | no | Talos: ContainerSecurity — https://docs.siderolabs.com/talos/v1.14/reference/configuration/container/containerconfig |
| `spec.cni.custom.documents[].containerConfig.security.capabilities` **(deprecated)** | `object` | no | Talos: ContainerCapabilities — https://docs.siderolabs.com/talos/v1.14/reference/configuration/container/containerconfig |
| `spec.cni.custom.documents[].containerConfig.security.capabilities.add` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].containerConfig.security.capabilities.drop` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].containerConfig.security.machinedAccess` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].containerConfig.security.profile` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].containerConfig.workingDir` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].dhcpv6Config` **(deprecated)** | `object` | no | dhcpv6Config is a Talos DHCPv6Config document; populated when kind is "DHCPv6Config". |
| `spec.cni.custom.documents[].dhcpv6Config.clientIdentifier` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].dhcpv6Config.duidRaw` **(deprecated)** | `string (byte)` | no |  |
| `spec.cni.custom.documents[].dhcpv6Config.ignoreHostname` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].dhcpv6Config.name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].dhcpv6Config.routeMetric` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].dummyLinkConfig` **(deprecated)** | `object` | no | dummyLinkConfig is a Talos DummyLinkConfig document; populated when kind is "DummyLinkConfig". |
| `spec.cni.custom.documents[].dummyLinkConfig.addresses` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].dummyLinkConfig.addresses[].address` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].dummyLinkConfig.addresses[].routePriority` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].dummyLinkConfig.hardwareAddr` **(deprecated)** | `string (byte)` | no |  |
| `spec.cni.custom.documents[].dummyLinkConfig.mtu` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].dummyLinkConfig.multicast` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].dummyLinkConfig.name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].dummyLinkConfig.routes` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].dummyLinkConfig.routes[].destination` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].dummyLinkConfig.routes[].gateway` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].dummyLinkConfig.routes[].metric` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].dummyLinkConfig.routes[].mtu` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].dummyLinkConfig.routes[].source` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].dummyLinkConfig.routes[].table` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].dummyLinkConfig.up` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].environmentConfig` **(deprecated)** | `object` | no | environmentConfig is a Talos EnvironmentConfig document; populated when kind is "EnvironmentConfig". |
| `spec.cni.custom.documents[].environmentConfig.variables` **(deprecated)** | `map[string]string` | yes |  |
| `spec.cni.custom.documents[].etcFileConfig` **(deprecated)** | `object` | no | etcFileConfig is a Talos EtcFileConfig document; populated when kind is "EtcFileConfig". |
| `spec.cni.custom.documents[].etcFileConfig.contents` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].etcFileConfig.mode` **(deprecated)** | `integer` | yes |  |
| `spec.cni.custom.documents[].etcFileConfig.name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].ethernetConfig` **(deprecated)** | `object` | no | ethernetConfig is a Talos EthernetConfig document; populated when kind is "EthernetConfig". |
| `spec.cni.custom.documents[].ethernetConfig.channels` **(deprecated)** | `object` | no | Talos: EthernetChannelsConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/network/ethernetconfig |
| `spec.cni.custom.documents[].ethernetConfig.channels.combined` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].ethernetConfig.channels.other` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].ethernetConfig.channels.rx` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].ethernetConfig.channels.tx` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].ethernetConfig.features` **(deprecated)** | `map[string]boolean` | no |  |
| `spec.cni.custom.documents[].ethernetConfig.name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].ethernetConfig.rings` **(deprecated)** | `object` | no | Talos: EthernetRingsConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/network/ethernetconfig |
| `spec.cni.custom.documents[].ethernetConfig.rings.cqe-size` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].ethernetConfig.rings.rx` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].ethernetConfig.rings.rx-buf-len` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].ethernetConfig.rings.rx-jumbo` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].ethernetConfig.rings.rx-mini` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].ethernetConfig.rings.rx-push` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].ethernetConfig.rings.tcp-data-split` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].ethernetConfig.rings.tx` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].ethernetConfig.rings.tx-push` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].ethernetConfig.rings.tx-push-buf-len` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].ethernetConfig.wakeOnLan` **(deprecated)** | `[]integer` | no |  |
| `spec.cni.custom.documents[].eventSinkConfig` **(deprecated)** | `object` | no | eventSinkConfig is a Talos EventSinkConfig document; populated when kind is "EventSinkConfig". |
| `spec.cni.custom.documents[].eventSinkConfig.endpoint` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].existingVolumeConfig` **(deprecated)** | `object` | no | existingVolumeConfig is a Talos ExistingVolumeConfig document; populated when kind is "ExistingVolumeConfig". |
| `spec.cni.custom.documents[].existingVolumeConfig.discovery` **(deprecated)** | `object` | no | Talos: VolumeDiscoverySpec — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/existingvolumeconfig |
| `spec.cni.custom.documents[].existingVolumeConfig.discovery.volumeSelector` **(deprecated)** | `object` | no | Talos: VolumeSelector — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/existingvolumeconfig |
| `spec.cni.custom.documents[].existingVolumeConfig.discovery.volumeSelector.match` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].existingVolumeConfig.mount` **(deprecated)** | `object` | no | Talos: ExistingMountSpec — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/existingvolumeconfig |
| `spec.cni.custom.documents[].existingVolumeConfig.mount.disableAccessTime` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].existingVolumeConfig.mount.readOnly` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].existingVolumeConfig.mount.secure` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].existingVolumeConfig.name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].existingVolumeConfig.scrub` **(deprecated)** | `object` | no | Talos: ScrubConfig |
| `spec.cni.custom.documents[].existingVolumeConfig.scrub.enabled` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].existingVolumeConfig.scrub.interval` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].existingVolumeConfig.trim` **(deprecated)** | `object` | no | Talos: TrimConfig |
| `spec.cni.custom.documents[].existingVolumeConfig.trim.enabled` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].existingVolumeConfig.trim.interval` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].extensionServiceConfig` **(deprecated)** | `object` | no | extensionServiceConfig is a Talos ExtensionServiceConfig document; populated when kind is "ExtensionServiceConfig". |
| `spec.cni.custom.documents[].extensionServiceConfig.configFiles` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].extensionServiceConfig.configFiles[].content` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].extensionServiceConfig.configFiles[].mountPath` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].extensionServiceConfig.environment` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].extensionServiceConfig.name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].externalVolumeConfig` **(deprecated)** | `object` | no | externalVolumeConfig is a Talos ExternalVolumeConfig document; populated when kind is "ExternalVolumeConfig". |
| `spec.cni.custom.documents[].externalVolumeConfig.filesystemType` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].externalVolumeConfig.mount` **(deprecated)** | `object` | no | Talos: ExternalMountSpec — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/externalvolumeconfig |
| `spec.cni.custom.documents[].externalVolumeConfig.mount.disableAccessTime` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].externalVolumeConfig.mount.readOnly` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].externalVolumeConfig.mount.secure` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].externalVolumeConfig.mount.virtiofs` **(deprecated)** | `object` | no | Talos: VirtiofsMountSpec — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/externalvolumeconfig |
| `spec.cni.custom.documents[].externalVolumeConfig.mount.virtiofs.tag` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].externalVolumeConfig.name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].filesystemScrubConfig` **(deprecated)** | `object` | no | filesystemScrubConfig is a Talos FilesystemScrubConfig document; populated when kind is "FilesystemScrubConfig". |
| `spec.cni.custom.documents[].filesystemScrubConfig.interval` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].filesystemTrimConfig` **(deprecated)** | `object` | no | filesystemTrimConfig is a Talos FilesystemTrimConfig document; populated when kind is "FilesystemTrimConfig". |
| `spec.cni.custom.documents[].filesystemTrimConfig.interval` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].httpProbeConfig` **(deprecated)** | `object` | no | httpProbeConfig is a Talos HTTPProbeConfig document; populated when kind is "HTTPProbeConfig". |
| `spec.cni.custom.documents[].httpProbeConfig.failureThreshold` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].httpProbeConfig.interval` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].httpProbeConfig.name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].httpProbeConfig.timeout` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].httpProbeConfig.url` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].imageCacheConfig` **(deprecated)** | `object` | no | imageCacheConfig is a Talos ImageCacheConfig document; populated when kind is "ImageCacheConfig". |
| `spec.cni.custom.documents[].imageCacheConfig.local` **(deprecated)** | `object` | yes | Talos: LocalImageCacheConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/cri/imagecacheconfig |
| `spec.cni.custom.documents[].imageCacheConfig.local.enabled` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].imageVerificationConfig` **(deprecated)** | `object` | no | imageVerificationConfig is a Talos ImageVerificationConfig document; populated when kind is "ImageVerificationConfig". |
| `spec.cni.custom.documents[].imageVerificationConfig.rules` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].imageVerificationConfig.rules[].deny` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].imageVerificationConfig.rules[].image` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].imageVerificationConfig.rules[].keyless` **(deprecated)** | `object` | no | Talos: ImageKeylessVerifierV1Alpha1 — https://docs.siderolabs.com/talos/v1.14/reference/configuration/security/imageverificationconfig |
| `spec.cni.custom.documents[].imageVerificationConfig.rules[].keyless.issuer` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].imageVerificationConfig.rules[].keyless.subject` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].imageVerificationConfig.rules[].keyless.subjectRegex` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].imageVerificationConfig.rules[].publicKey` **(deprecated)** | `object` | no | Talos: ImagePublicKeyVerifierV1Alpha1 — https://docs.siderolabs.com/talos/v1.14/reference/configuration/security/imageverificationconfig |
| `spec.cni.custom.documents[].imageVerificationConfig.rules[].publicKey.certificate` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].imageVerificationConfig.rules[].skip` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].kernelModuleConfig` **(deprecated)** | `object` | no | kernelModuleConfig is a Talos KernelModuleConfig document; populated when kind is "KernelModuleConfig". |
| `spec.cni.custom.documents[].kernelModuleConfig.name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].kernelModuleConfig.parameters` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].kind` **(deprecated)** | `string` | yes | kind selects which of the fields below is populated. Allowed values: `SysctlConfig`, `KernelModuleConfig`, `Config`, `VolumeConfig`, `UserVolumeConfig`, `RawVolumeConfig`, `ResolverConfig`, `BGPInstanceConfig`, `BlackholeRouteConfig`, `BondConfig`, `BridgeConfig`, `ContainerConfig`, `DHCPv6Config`, `DummyLinkConfig`, `EnvironmentConfig`, `EtcFileConfig`, `EthernetConfig`, `EventSinkConfig`, `ExistingVolumeConfig`, `ExtensionServiceConfig`, `ExternalVolumeConfig`, `FilesystemScrubConfig`, `FilesystemTrimConfig`, `HTTPProbeConfig`, `ImageCacheConfig`, `ImageVerificationConfig`, `KmsgLogConfig`, `KubeAPIServerConfig`, `KubeClusterConfig`, `KubeControllerManagerConfig`, `KubeCoreDNSConfig`, `KubeInlineManifestConfig`, `KubeNetworkConfig`, `KubeNodeConfig`, `KubePrismConfig`, `KubeSpanConfig`, `KubeSpanEndpointsConfig`, `KubeTalosAPIAccessConfig`, `LVMLogicalVolumeConfig`, `LVMVolumeGroupConfig`, `Layer2VIPConfig`, `OOMConfig`, `PCIDriverRebindConfig`, `RAIDArrayConfig`, `RegistryMirrorConfig`, `RoutingRuleConfig`, `StaticHostConfig`, `SwapVolumeConfig`, `SysfsConfig`, `TCPProbeConfig`, `TimeSyncConfig`, `TrustedRootsConfig`, `UdevRulesConfig`, `VRFConfig`, `VethConfig`, `WatchdogTimerConfig`, `ZswapConfig`, `KubeAuditPolicyConfig`, `KubeAuthorizerConfig`, `KubeCredentialProviderConfig`, `KubeExternalManifestConfig`, `KubeletConfig`, `KubeProxyConfig`, `KubeSchedulerConfig`. |
| `spec.cni.custom.documents[].kmsgLogConfig` **(deprecated)** | `object` | no | kmsgLogConfig is a Talos KmsgLogConfig document; populated when kind is "KmsgLogConfig". |
| `spec.cni.custom.documents[].kmsgLogConfig.name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].kmsgLogConfig.url` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].kubeAPIServerConfig` **(deprecated)** | `object` | no | kubeAPIServerConfig is a Talos KubeAPIServerConfig document; populated when kind is "KubeAPIServerConfig". |
| `spec.cni.custom.documents[].kubeAPIServerConfig.apiPort` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].kubeAPIServerConfig.certExtraSANs` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].kubeAPIServerConfig.env` **(deprecated)** | `map[string]string` | no |  |
| `spec.cni.custom.documents[].kubeAPIServerConfig.extraArgs` **(deprecated)** | `map[string][]string` | no |  |
| `spec.cni.custom.documents[].kubeAPIServerConfig.image` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].kubeAPIServerConfig.resources` **(deprecated)** | `object` | no | Talos: ResourcesConfig |
| `spec.cni.custom.documents[].kubeAPIServerConfig.resources.limits` **(deprecated)** | `map[string]integer or string` | no | ResourceList is a set of (resource name, quantity) pairs. |
| `spec.cni.custom.documents[].kubeAPIServerConfig.resources.requests` **(deprecated)** | `map[string]integer or string` | no | ResourceList is a set of (resource name, quantity) pairs. |
| `spec.cni.custom.documents[].kubeAPIServerConfig.startupProbes` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].kubeAuditPolicyConfig` **(deprecated)** | `object` | no | kubeAuditPolicyConfig is a Talos KubeAuditPolicyConfig document; populated when kind is "KubeAuditPolicyConfig". |
| `spec.cni.custom.documents[].kubeAuditPolicyConfig.configuration` **(deprecated)** | `object` | yes | Policy defines the configuration of audit logging, and the rules for how different request categories are logged. |
| `spec.cni.custom.documents[].kubeAuditPolicyConfig.configuration.apiVersion` **(deprecated)** | `string` | no | APIVersion defines the versioned schema of this representation of an object. Servers should convert recognized schemas to the latest internal value, and may reject unrecognized values. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |
| `spec.cni.custom.documents[].kubeAuditPolicyConfig.configuration.kind` **(deprecated)** | `string` | no | Kind is a string value representing the REST resource this object represents. Servers may infer this from the endpoint the client submits requests to. Cannot be updated. In CamelCase. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |
| `spec.cni.custom.documents[].kubeAuditPolicyConfig.configuration.metadata` **(deprecated)** | `object` | no | ObjectMeta is included for interoperability with API infrastructure. |
| `spec.cni.custom.documents[].kubeAuditPolicyConfig.configuration.omitManagedFields` **(deprecated)** | `boolean` | no | OmitManagedFields indicates whether to omit the managed fields of the request and response bodies from being written to the API audit log. This is used as a global default - a value of 'true' will omit the managed fileds, otherwise the managed fields will be included in the API audit log. Note that this can also be specified per rule in which case the value specified in a rule will override the global default. |
| `spec.cni.custom.documents[].kubeAuditPolicyConfig.configuration.omitStages` **(deprecated)** | `[]string` | no | OmitStages is a list of stages for which no events are created. Note that this can also be specified per rule in which case the union of both are omitted. |
| `spec.cni.custom.documents[].kubeAuditPolicyConfig.configuration.rules` **(deprecated)** | `[]object` | yes | Rules specify the audit Level a request should be recorded at. A request may match multiple rules, in which case the FIRST matching rule is used. The default audit level is None, but can be overridden by a catch-all rule at the end of the list. PolicyRules are strictly ordered. |
| `spec.cni.custom.documents[].kubeAuditPolicyConfig.configuration.rules[].level` **(deprecated)** | `string` | yes | The Level that requests matching this rule are recorded at. |
| `spec.cni.custom.documents[].kubeAuditPolicyConfig.configuration.rules[].namespaces` **(deprecated)** | `[]string` | no | Namespaces that this rule matches. The empty string "" matches non-namespaced resources. An empty list implies every namespace. |
| `spec.cni.custom.documents[].kubeAuditPolicyConfig.configuration.rules[].nonResourceURLs` **(deprecated)** | `[]string` | no | NonResourceURLs is a set of URL paths that should be audited. `*`s are allowed, but only as the full, final step in the path. Examples: - `/metrics` - Log requests for apiserver metrics - `/healthz*` - Log all health checks |
| `spec.cni.custom.documents[].kubeAuditPolicyConfig.configuration.rules[].omitManagedFields` **(deprecated)** | `boolean` | no | OmitManagedFields indicates whether to omit the managed fields of the request and response bodies from being written to the API audit log. - a value of 'true' will drop the managed fields from the API audit log - a value of 'false' indicates that the managed fileds should be included in the API audit log Note that the value, if specified, in this rule will override the global default If a value is not specified then the global default specified in Policy.OmitManagedFields will stand. |
| `spec.cni.custom.documents[].kubeAuditPolicyConfig.configuration.rules[].omitStages` **(deprecated)** | `[]string` | no | OmitStages is a list of stages for which no events are created. Note that this can also be specified policy wide in which case the union of both are omitted. An empty list means no restrictions will apply. |
| `spec.cni.custom.documents[].kubeAuditPolicyConfig.configuration.rules[].resources` **(deprecated)** | `[]object` | no | Resources that this rule matches. An empty list implies all kinds in all API groups. |
| `spec.cni.custom.documents[].kubeAuditPolicyConfig.configuration.rules[].resources[].group` **(deprecated)** | `string` | no | Group is the name of the API group that contains the resources. The empty string represents the core API group. `*` matches all groups |
| `spec.cni.custom.documents[].kubeAuditPolicyConfig.configuration.rules[].resources[].resourceNames` **(deprecated)** | `[]string` | no | ResourceNames is a list of resource instance names that the policy matches. Using this field requires Resources to be specified. An empty list implies that every instance of the resource is matched. |
| `spec.cni.custom.documents[].kubeAuditPolicyConfig.configuration.rules[].resources[].resources` **(deprecated)** | `[]string` | no | Resources is a list of resources this rule applies to. For example: - `pods` matches pods. - `pods/log` matches the log subresource of pods. - `*` matches all resources and their subresources. - `pods/*` matches all subresources of pods. - `*/scale` matches all scale subresources. If wildcard is present, the validation rule will ensure resources do not overlap with each other. An empty list implies all resources and subresources in this API groups apply. |
| `spec.cni.custom.documents[].kubeAuditPolicyConfig.configuration.rules[].userGroups` **(deprecated)** | `[]string` | no | The user groups this rule applies to. A user is considered matching if it is a member of any of the UserGroups. An empty list implies every user group. |
| `spec.cni.custom.documents[].kubeAuditPolicyConfig.configuration.rules[].users` **(deprecated)** | `[]string` | no | The users (by authenticated user name) this rule applies to. An empty list implies every user. |
| `spec.cni.custom.documents[].kubeAuditPolicyConfig.configuration.rules[].verbs` **(deprecated)** | `[]string` | no | The verbs that match this rule. An empty list implies every verb. |
| `spec.cni.custom.documents[].kubeAuthorizerConfig` **(deprecated)** | `object` | no | kubeAuthorizerConfig is a Talos KubeAuthorizerConfig document; populated when kind is "KubeAuthorizerConfig". |
| `spec.cni.custom.documents[].kubeAuthorizerConfig.name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].kubeAuthorizerConfig.type` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].kubeAuthorizerConfig.webhook` **(deprecated)** | `object` | no |  |
| `spec.cni.custom.documents[].kubeAuthorizerConfig.webhook.authorizedTTL` **(deprecated)** | `string` | yes | The duration to cache 'authorized' responses from the webhook authorizer. Same as setting `--authorization-webhook-cache-authorized-ttl` flag Default: 5m0s |
| `spec.cni.custom.documents[].kubeAuthorizerConfig.webhook.cacheAuthorizedRequests` **(deprecated)** | `boolean` | no | CacheAuthorizedRequests specifies whether authorized requests should be cached. If set to true, the TTL for cached decisions can be configured via the AuthorizedTTL field. Default: true |
| `spec.cni.custom.documents[].kubeAuthorizerConfig.webhook.cacheUnauthorizedRequests` **(deprecated)** | `boolean` | no | CacheUnauthorizedRequests specifies whether unauthorized requests should be cached. If set to true, the TTL for cached decisions can be configured via the UnauthorizedTTL field. Default: true |
| `spec.cni.custom.documents[].kubeAuthorizerConfig.webhook.connectionInfo` **(deprecated)** | `object` | yes | ConnectionInfo defines how we talk to the webhook |
| `spec.cni.custom.documents[].kubeAuthorizerConfig.webhook.connectionInfo.kubeConfigFile` **(deprecated)** | `string` | yes | Path to KubeConfigFile for connection info Required, if connectionInfo.Type is KubeConfig |
| `spec.cni.custom.documents[].kubeAuthorizerConfig.webhook.connectionInfo.type` **(deprecated)** | `string` | yes | Controls how the webhook should communicate with the server. Valid values: - KubeConfigFile: use the file specified in kubeConfigFile to locate the server. - InClusterConfig: use the in-cluster configuration to call the SubjectAccessReview API hosted by kube-apiserver. This mode is not allowed for kube-apiserver. |
| `spec.cni.custom.documents[].kubeAuthorizerConfig.webhook.failurePolicy` **(deprecated)** | `string` | yes | Controls the authorization decision when a webhook request fails to complete or returns a malformed response or errors evaluating matchConditions. Valid values: - NoOpinion: continue to subsequent authorizers to see if one of them allows the request - Deny: reject the request without consulting subsequent authorizers Required, with no default. |
| `spec.cni.custom.documents[].kubeAuthorizerConfig.webhook.matchConditionSubjectAccessReviewVersion` **(deprecated)** | `string` | yes | MatchConditionSubjectAccessReviewVersion specifies the SubjectAccessReview version the CEL expressions are evaluated against Valid values: v1 Required, no default value |
| `spec.cni.custom.documents[].kubeAuthorizerConfig.webhook.matchConditions` **(deprecated)** | `[]object` | yes | matchConditions is a list of conditions that must be met for a request to be sent to this webhook. An empty list of matchConditions matches all requests. There are a maximum of 64 match conditions allowed. The exact matching logic is (in order): 1. If at least one matchCondition evaluates to FALSE, then the webhook is skipped. 2. If ALL matchConditions evaluate to TRUE, then the webhook is called. 3. If at least one matchCondition evaluates to an error (but none are FALSE): - If failurePolicy=Deny, then the webhook rejects the request - If failurePolicy=NoOpinion, then the error is ignored and the webhook is skipped |
| `spec.cni.custom.documents[].kubeAuthorizerConfig.webhook.matchConditions[].expression` **(deprecated)** | `string` | yes | expression represents the expression which will be evaluated by CEL. Must evaluate to bool. CEL expressions have access to the contents of the SubjectAccessReview in v1 version. If version specified by subjectAccessReviewVersion in the request variable is v1beta1, the contents would be converted to the v1 version before evaluating the CEL expression. - 'resourceAttributes' describes information for a resource access request and is unset for non-resource requests. e.g. has(request.resourceAttributes) && request.resourceAttributes.namespace == 'default' - 'nonResourceAttributes' describes information for a non-resource access request and is unset for resource requests. e.g. has(request.nonResourceAttributes) && request.nonResourceAttributes.path == '/healthz'. - 'user' is the user to test for. e.g. request.user == 'alice' - 'groups' is the groups to test for. e.g. ('group1' in request.groups) - 'extra' corresponds to the user.Info.GetExtra() method from the authenticator. - 'uid' is the information about the requesting user. e.g. request.uid == '1' Documentation on CEL: https://kubernetes.io/docs/reference/using-api/cel/ |
| `spec.cni.custom.documents[].kubeAuthorizerConfig.webhook.subjectAccessReviewVersion` **(deprecated)** | `string` | yes | The API version of the authorization.k8s.io SubjectAccessReview to send to and expect from the webhook. Same as setting `--authorization-webhook-version` flag Valid values: v1beta1, v1 Required, no default value |
| `spec.cni.custom.documents[].kubeAuthorizerConfig.webhook.timeout` **(deprecated)** | `string` | yes | Timeout for the webhook request Maximum allowed value is 30s. Required, no default value. |
| `spec.cni.custom.documents[].kubeAuthorizerConfig.webhook.unauthorizedTTL` **(deprecated)** | `string` | yes | The duration to cache 'unauthorized' responses from the webhook authorizer. Same as setting `--authorization-webhook-cache-unauthorized-ttl` flag Default: 30s |
| `spec.cni.custom.documents[].kubeClusterConfig` **(deprecated)** | `object` | no | kubeClusterConfig is a Talos KubeClusterConfig document; populated when kind is "KubeClusterConfig". |
| `spec.cni.custom.documents[].kubeClusterConfig.clusterName` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].kubeClusterConfig.endpoint` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].kubeControllerManagerConfig` **(deprecated)** | `object` | no | kubeControllerManagerConfig is a Talos KubeControllerManagerConfig document; populated when kind is "KubeControllerManagerConfig". |
| `spec.cni.custom.documents[].kubeControllerManagerConfig.enabled` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].kubeControllerManagerConfig.env` **(deprecated)** | `map[string]string` | no |  |
| `spec.cni.custom.documents[].kubeControllerManagerConfig.extraArgs` **(deprecated)** | `map[string][]string` | no |  |
| `spec.cni.custom.documents[].kubeControllerManagerConfig.image` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].kubeControllerManagerConfig.resources` **(deprecated)** | `object` | no | Talos: ResourcesConfig |
| `spec.cni.custom.documents[].kubeControllerManagerConfig.resources.limits` **(deprecated)** | `map[string]integer or string` | no | ResourceList is a set of (resource name, quantity) pairs. |
| `spec.cni.custom.documents[].kubeControllerManagerConfig.resources.requests` **(deprecated)** | `map[string]integer or string` | no | ResourceList is a set of (resource name, quantity) pairs. |
| `spec.cni.custom.documents[].kubeCoreDNSConfig` **(deprecated)** | `object` | no | kubeCoreDNSConfig is a Talos KubeCoreDNSConfig document; populated when kind is "KubeCoreDNSConfig". |
| `spec.cni.custom.documents[].kubeCoreDNSConfig.enabled` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].kubeCoreDNSConfig.image` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].kubeCredentialProviderConfig` **(deprecated)** | `object` | no | kubeCredentialProviderConfig is a Talos KubeCredentialProviderConfig document; populated when kind is "KubeCredentialProviderConfig". |
| `spec.cni.custom.documents[].kubeCredentialProviderConfig.configuration` **(deprecated)** | `object` | yes | CredentialProviderConfig is the configuration containing information about each exec credential provider. Kubelet reads this configuration from disk and enables each provider as specified by the CredentialProvider type. |
| `spec.cni.custom.documents[].kubeCredentialProviderConfig.configuration.apiVersion` **(deprecated)** | `string` | no | APIVersion defines the versioned schema of this representation of an object. Servers should convert recognized schemas to the latest internal value, and may reject unrecognized values. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |
| `spec.cni.custom.documents[].kubeCredentialProviderConfig.configuration.kind` **(deprecated)** | `string` | no | Kind is a string value representing the REST resource this object represents. Servers may infer this from the endpoint the client submits requests to. Cannot be updated. In CamelCase. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |
| `spec.cni.custom.documents[].kubeCredentialProviderConfig.configuration.providers` **(deprecated)** | `[]object` | yes | providers is a list of credential provider plugins that will be enabled by the kubelet. Multiple providers may match against a single image, in which case credentials from all providers will be returned to the kubelet. If multiple providers are called for a single image, the results are combined. If providers return overlapping auth keys, the value from the provider earlier in this list is attempted first. |
| `spec.cni.custom.documents[].kubeCredentialProviderConfig.configuration.providers[].apiVersion` **(deprecated)** | `string` | yes | Required input version of the exec CredentialProviderRequest. The returned CredentialProviderResponse MUST use the same encoding version as the input. Current supported values are: - credentialprovider.kubelet.k8s.io/v1 |
| `spec.cni.custom.documents[].kubeCredentialProviderConfig.configuration.providers[].args` **(deprecated)** | `[]string` | no | Arguments to pass to the command when executing it. |
| `spec.cni.custom.documents[].kubeCredentialProviderConfig.configuration.providers[].defaultCacheDuration` **(deprecated)** | `string` | yes | defaultCacheDuration is the default duration the plugin will cache credentials in-memory if a cache duration is not provided in the plugin response. This field is required. |
| `spec.cni.custom.documents[].kubeCredentialProviderConfig.configuration.providers[].env` **(deprecated)** | `[]object` | no | Env defines additional environment variables to expose to the process. These are unioned with the host's environment, as well as variables client-go uses to pass argument to the plugin. |
| `spec.cni.custom.documents[].kubeCredentialProviderConfig.configuration.providers[].env[].name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].kubeCredentialProviderConfig.configuration.providers[].env[].value` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].kubeCredentialProviderConfig.configuration.providers[].matchImages` **(deprecated)** | `[]string` | yes | matchImages is a required list of strings used to match against images in order to determine if this provider should be invoked. If one of the strings matches the requested image from the kubelet, the plugin will be invoked and given a chance to provide credentials. Images are expected to contain the registry domain and URL path. Each entry in matchImages is a pattern which can optionally contain a port and a path. Globs can be used in the domain, but not in the port or the path. Globs are supported as subdomains like '*.k8s.io' or 'k8s.*.io', and top-level-domains such as 'k8s.*'. Matching partial subdomains like 'app*.k8s.io' is also supported. Each glob can only match a single subdomain segment, so *.io does not match *.k8s.io. A match exists between an image and a matchImage when all of the below are true: - Both contain the same number of domain parts and each part matches. - The URL path of an imageMatch must be a prefix of the target image URL path. - If the imageMatch contains a port, then the port must match in the image as well. Example values of matchImages: - 123456789.dkr.ecr.us-east-1.amazonaws.com - *.azurecr.io - gcr.io - *.*.registry.io - registry.io:8080/path |
| `spec.cni.custom.documents[].kubeCredentialProviderConfig.configuration.providers[].name` **(deprecated)** | `string` | yes | name is the required name of the credential provider. It must match the name of the provider executable as seen by the kubelet. The executable must be in the kubelet's bin directory (set by the --image-credential-provider-bin-dir flag). Required to be unique across all providers. |
| `spec.cni.custom.documents[].kubeCredentialProviderConfig.configuration.providers[].tokenAttributes` **(deprecated)** | `object` | no | tokenAttributes is the configuration for the service account token that will be passed to the plugin. The credential provider opts in to using service account tokens for image pull by setting this field. When this field is set, kubelet will generate a service account token bound to the pod for which the image is being pulled and pass to the plugin as part of CredentialProviderRequest along with other attributes required by the plugin. The service account metadata and token attributes will be used as a dimension to cache the credentials in kubelet. The cache key is generated by combining the service account metadata (namespace, name, UID, and annotations key+value for the keys defined in serviceAccountTokenAttribute.requiredServiceAccountAnnotationKeys and serviceAccountTokenAttribute.optionalServiceAccountAnnotationKeys). The pod metadata (namespace, name, UID) that are in the service account token are not used as a dimension to cache the credentials in kubelet. This means workloads that are using the same service account could end up using the same credentials for image pull. For plugins that don't want this behavior, or plugins that operate in pass-through mode; i.e., they return the service account token as-is, they can set the credentialProviderResponse.cacheDuration to 0. This will disable the caching of credentials in kubelet and the plugin will be invoked for every image pull. This does result in token generation overhead for every image pull, but it is the only way to ensure that the credentials are not shared across pods (even if they are using the same service account). |
| `spec.cni.custom.documents[].kubeCredentialProviderConfig.configuration.providers[].tokenAttributes.cacheType` **(deprecated)** | `string` | yes | cacheType indicates the type of cache key use for caching the credentials returned by the plugin when the service account token is used. The most conservative option is to set this to "Token", which means the kubelet will cache returned credentials on a per-token basis. This should be set if the returned credential's lifetime is limited to the service account token's lifetime. If the plugin's credential retrieval logic depends only on the service account and not on pod-specific claims, then the plugin can set this to "ServiceAccount". In this case, the kubelet will cache returned credentials on a per-serviceaccount basis. Use this when the returned credential is valid for all pods using the same service account. |
| `spec.cni.custom.documents[].kubeCredentialProviderConfig.configuration.providers[].tokenAttributes.optionalServiceAccountAnnotationKeys` **(deprecated)** | `[]string` | no | optionalServiceAccountAnnotationKeys is the list of annotation keys that the plugin is interested in and that are optional to be present in the service account. The keys defined in this list will be extracted from the corresponding service account and passed to the plugin as part of the CredentialProviderRequest. The plugin is responsible for validating the existence of annotations and their values. This field is optional and may be empty. Plugins may use this field to extract additional information required to fetch credentials. Keys in this list must be unique. |
| `spec.cni.custom.documents[].kubeCredentialProviderConfig.configuration.providers[].tokenAttributes.requireServiceAccount` **(deprecated)** | `boolean` | yes | requireServiceAccount indicates whether the plugin requires the pod to have a service account. If set to true, kubelet will only invoke the plugin if the pod has a service account. If set to false, kubelet will invoke the plugin even if the pod does not have a service account and will not include a token in the CredentialProviderRequest in that scenario. This is useful for plugins that are used to pull images for pods without service accounts (e.g., static pods). |
| `spec.cni.custom.documents[].kubeCredentialProviderConfig.configuration.providers[].tokenAttributes.requiredServiceAccountAnnotationKeys` **(deprecated)** | `[]string` | no | requiredServiceAccountAnnotationKeys is the list of annotation keys that the plugin is interested in and that are required to be present in the service account. The keys defined in this list will be extracted from the corresponding service account and passed to the plugin as part of the CredentialProviderRequest. If any of the keys defined in this list are not present in the service account, kubelet will not invoke the plugin and will return an error. This field is optional and may be empty. Plugins may use this field to extract additional information required to fetch credentials or allow workloads to opt in to using service account tokens for image pull. If non-empty, requireServiceAccount must be set to true. Keys in this list must be unique. This list needs to be mutually exclusive with optionalServiceAccountAnnotationKeys. |
| `spec.cni.custom.documents[].kubeCredentialProviderConfig.configuration.providers[].tokenAttributes.serviceAccountTokenAudience` **(deprecated)** | `string` | yes | serviceAccountTokenAudience is the intended audience for the projected service account token. |
| `spec.cni.custom.documents[].kubeExternalManifestConfig` **(deprecated)** | `object` | no | kubeExternalManifestConfig is a Talos KubeExternalManifestConfig document; populated when kind is "KubeExternalManifestConfig". |
| `spec.cni.custom.documents[].kubeExternalManifestConfig.name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].kubeExternalManifestConfig.url` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].kubeInlineManifestConfig` **(deprecated)** | `object` | no | kubeInlineManifestConfig is a Talos KubeInlineManifestConfig document; populated when kind is "KubeInlineManifestConfig". |
| `spec.cni.custom.documents[].kubeInlineManifestConfig.manifest` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].kubeInlineManifestConfig.name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].kubeNetworkConfig` **(deprecated)** | `object` | no | kubeNetworkConfig is a Talos KubeNetworkConfig document; populated when kind is "KubeNetworkConfig". |
| `spec.cni.custom.documents[].kubeNetworkConfig.dnsDomain` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].kubeNetworkConfig.nodeCIDRMaskSizeIPv4` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].kubeNetworkConfig.nodeCIDRMaskSizeIPv6` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].kubeNetworkConfig.podSubnets` **(deprecated)** | `[]string` | yes |  |
| `spec.cni.custom.documents[].kubeNetworkConfig.serviceSubnets` **(deprecated)** | `[]string` | yes |  |
| `spec.cni.custom.documents[].kubeNodeConfig` **(deprecated)** | `object` | no | kubeNodeConfig is a Talos KubeNodeConfig document; populated when kind is "KubeNodeConfig". |
| `spec.cni.custom.documents[].kubeNodeConfig.annotations` **(deprecated)** | `map[string]string` | no |  |
| `spec.cni.custom.documents[].kubeNodeConfig.labels` **(deprecated)** | `map[string]string` | no |  |
| `spec.cni.custom.documents[].kubeNodeConfig.nodeIP` **(deprecated)** | `object` | yes | Talos: NodeIPConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/k8s/kubenodeconfig |
| `spec.cni.custom.documents[].kubeNodeConfig.nodeIP.validSubnets` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].kubeNodeConfig.registerWithFQDN` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].kubeNodeConfig.skipNodeRegistration` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].kubeNodeConfig.taints` **(deprecated)** | `map[string]string` | no |  |
| `spec.cni.custom.documents[].kubePrismConfig` **(deprecated)** | `object` | no | kubePrismConfig is a Talos KubePrismConfig document; populated when kind is "KubePrismConfig". |
| `spec.cni.custom.documents[].kubePrismConfig.port` **(deprecated)** | `integer` | yes |  |
| `spec.cni.custom.documents[].kubePrismConfig.tlsServerName` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].kubeProxyConfig` **(deprecated)** | `object` | no | kubeProxyConfig is a Talos KubeProxyConfig document; populated when kind is "KubeProxyConfig". |
| `spec.cni.custom.documents[].kubeProxyConfig.config` **(deprecated)** | `object` | yes | KubeProxyConfigurationSubset mirrors k8s.io/kube-proxy/config/v1alpha1.KubeProxyConfiguration. Ichor does not carry bindAddress, clientConnection, configSyncPeriod, enableProfiling, healthzBindAddress, hostnameOverride, logging, mode, portRange, showHiddenMetricsForVersion, windowsRunAsService, winkernel. |
| `spec.cni.custom.documents[].kubeProxyConfig.config.bindAddressHardFail` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].kubeProxyConfig.config.clusterCIDR` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].kubeProxyConfig.config.conntrack` **(deprecated)** | `object` | no | KubeProxyConntrackConfiguration contains conntrack settings for the Kubernetes proxy server. |
| `spec.cni.custom.documents[].kubeProxyConfig.config.conntrack.maxPerCore` **(deprecated)** | `integer` | yes | maxPerCore is the maximum number of NAT connections to track per CPU core (0 to leave the limit as-is and ignore min). |
| `spec.cni.custom.documents[].kubeProxyConfig.config.conntrack.min` **(deprecated)** | `integer` | yes | min is the minimum value of connect-tracking records to allocate, regardless of maxPerCore (set maxPerCore=0 to leave the limit as-is). |
| `spec.cni.custom.documents[].kubeProxyConfig.config.conntrack.tcpBeLiberal` **(deprecated)** | `boolean` | yes | tcpBeLiberal, if true, kube-proxy will configure conntrack to run in liberal mode for TCP connections and packets with out-of-window sequence numbers won't be marked INVALID. |
| `spec.cni.custom.documents[].kubeProxyConfig.config.conntrack.tcpCloseWaitTimeout` **(deprecated)** | `string` | yes | tcpCloseWaitTimeout is how long an idle conntrack entry in CLOSE_WAIT state will remain in the conntrack table. (e.g. '60s'). Must be greater than 0 to set. |
| `spec.cni.custom.documents[].kubeProxyConfig.config.conntrack.tcpEstablishedTimeout` **(deprecated)** | `string` | yes | tcpEstablishedTimeout is how long an idle TCP connection will be kept open (e.g. '2s'). Must be greater than 0 to set. |
| `spec.cni.custom.documents[].kubeProxyConfig.config.conntrack.udpStreamTimeout` **(deprecated)** | `string` | yes | udpStreamTimeout is how long an idle UDP conntrack entry in ASSURED state will remain in the conntrack table (e.g. '300s'). Must be greater than 0 to set. |
| `spec.cni.custom.documents[].kubeProxyConfig.config.conntrack.udpTimeout` **(deprecated)** | `string` | yes | udpTimeout is how long an idle UDP conntrack entry in UNREPLIED state will remain in the conntrack table (e.g. '30s'). Must be greater than 0 to set. |
| `spec.cni.custom.documents[].kubeProxyConfig.config.detectLocal` **(deprecated)** | `object` | no | DetectLocalConfiguration contains optional settings related to DetectLocalMode option |
| `spec.cni.custom.documents[].kubeProxyConfig.config.detectLocal.bridgeInterface` **(deprecated)** | `string` | yes | bridgeInterface is a bridge interface name. When DetectLocalMode is set to LocalModeBridgeInterface, kube-proxy will consider traffic to be local if it originates from this bridge. |
| `spec.cni.custom.documents[].kubeProxyConfig.config.detectLocal.interfaceNamePrefix` **(deprecated)** | `string` | yes | interfaceNamePrefix is an interface name prefix. When DetectLocalMode is set to LocalModeInterfaceNamePrefix, kube-proxy will consider traffic to be local if it originates from any interface whose name begins with this prefix. |
| `spec.cni.custom.documents[].kubeProxyConfig.config.detectLocalMode` **(deprecated)** | `string` | no | LocalMode represents modes to detect local traffic from the node |
| `spec.cni.custom.documents[].kubeProxyConfig.config.featureGates` **(deprecated)** | `map[string]boolean` | no |  |
| `spec.cni.custom.documents[].kubeProxyConfig.config.iptables` **(deprecated)** | `object` | no | KubeProxyIPTablesConfiguration contains iptables-related configuration details for the Kubernetes proxy server. |
| `spec.cni.custom.documents[].kubeProxyConfig.config.iptables.localhostNodePorts` **(deprecated)** | `boolean` | yes | localhostNodePorts, if false, tells kube-proxy to disable the legacy behavior of allowing NodePort services to be accessed via localhost. (Applies only to iptables mode and IPv4; localhost NodePorts are never allowed with other proxy modes or with IPv6.) |
| `spec.cni.custom.documents[].kubeProxyConfig.config.iptables.masqueradeAll` **(deprecated)** | `boolean` | yes | masqueradeAll tells kube-proxy to SNAT all traffic sent to Service cluster IPs, when using the iptables or ipvs proxy mode. This may be required with some CNI plugins. |
| `spec.cni.custom.documents[].kubeProxyConfig.config.iptables.masqueradeBit` **(deprecated)** | `integer` | yes | masqueradeBit is the bit of the iptables fwmark space to use for SNAT if using the iptables or ipvs proxy mode. Values must be within the range [0, 31]. |
| `spec.cni.custom.documents[].kubeProxyConfig.config.iptables.minSyncPeriod` **(deprecated)** | `string` | yes | minSyncPeriod is the minimum period between iptables rule resyncs (e.g. '5s', '1m', '2h22m'). A value of 0 means every Service or EndpointSlice change will result in an immediate iptables resync. |
| `spec.cni.custom.documents[].kubeProxyConfig.config.iptables.syncPeriod` **(deprecated)** | `string` | yes | syncPeriod is an interval (e.g. '5s', '1m', '2h22m') indicating how frequently various re-synchronizing and cleanup operations are performed. Must be greater than 0. |
| `spec.cni.custom.documents[].kubeProxyConfig.config.ipvs` **(deprecated)** | `object` | no | KubeProxyIPVSConfiguration contains ipvs-related configuration details for the Kubernetes proxy server. |
| `spec.cni.custom.documents[].kubeProxyConfig.config.ipvs.excludeCIDRs` **(deprecated)** | `[]string` | yes | excludeCIDRs is a list of CIDRs which the ipvs proxier should not touch when cleaning up ipvs services. |
| `spec.cni.custom.documents[].kubeProxyConfig.config.ipvs.minSyncPeriod` **(deprecated)** | `string` | yes | minSyncPeriod is the minimum period between IPVS rule resyncs (e.g. '5s', '1m', '2h22m'). A value of 0 means every Service or EndpointSlice change will result in an immediate IPVS resync. |
| `spec.cni.custom.documents[].kubeProxyConfig.config.ipvs.scheduler` **(deprecated)** | `string` | yes | scheduler is the IPVS scheduler to use |
| `spec.cni.custom.documents[].kubeProxyConfig.config.ipvs.strictARP` **(deprecated)** | `boolean` | yes | strictARP configures arp_ignore and arp_announce to avoid answering ARP queries from kube-ipvs0 interface |
| `spec.cni.custom.documents[].kubeProxyConfig.config.ipvs.syncPeriod` **(deprecated)** | `string` | yes | syncPeriod is an interval (e.g. '5s', '1m', '2h22m') indicating how frequently various re-synchronizing and cleanup operations are performed. Must be greater than 0. |
| `spec.cni.custom.documents[].kubeProxyConfig.config.ipvs.tcpFinTimeout` **(deprecated)** | `string` | yes | tcpFinTimeout is the timeout value used for IPVS TCP sessions after receiving a FIN. The default value is 0, which preserves the current timeout value on the system. |
| `spec.cni.custom.documents[].kubeProxyConfig.config.ipvs.tcpTimeout` **(deprecated)** | `string` | yes | tcpTimeout is the timeout value used for idle IPVS TCP sessions. The default value is 0, which preserves the current timeout value on the system. |
| `spec.cni.custom.documents[].kubeProxyConfig.config.ipvs.udpTimeout` **(deprecated)** | `string` | yes | udpTimeout is the timeout value used for IPVS UDP packets. The default value is 0, which preserves the current timeout value on the system. |
| `spec.cni.custom.documents[].kubeProxyConfig.config.metricsBindAddress` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].kubeProxyConfig.config.nftables` **(deprecated)** | `object` | no | KubeProxyNFTablesConfiguration contains nftables-related configuration details for the Kubernetes proxy server. |
| `spec.cni.custom.documents[].kubeProxyConfig.config.nftables.masqueradeAll` **(deprecated)** | `boolean` | yes | masqueradeAll tells kube-proxy to SNAT all traffic sent to Service cluster IPs, when using the nftables mode. This may be required with some CNI plugins. |
| `spec.cni.custom.documents[].kubeProxyConfig.config.nftables.masqueradeBit` **(deprecated)** | `integer` | yes | masqueradeBit is the bit of the iptables fwmark space to use for SNAT if using the nftables proxy mode. Values must be within the range [0, 31]. |
| `spec.cni.custom.documents[].kubeProxyConfig.config.nftables.minSyncPeriod` **(deprecated)** | `string` | yes | minSyncPeriod is the minimum period between iptables rule resyncs (e.g. '5s', '1m', '2h22m'). A value of 0 means every Service or EndpointSlice change will result in an immediate iptables resync. |
| `spec.cni.custom.documents[].kubeProxyConfig.config.nftables.syncPeriod` **(deprecated)** | `string` | yes | syncPeriod is an interval (e.g. '5s', '1m', '2h22m') indicating how frequently various re-synchronizing and cleanup operations are performed. Must be greater than 0. |
| `spec.cni.custom.documents[].kubeProxyConfig.config.nodePortAddresses` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].kubeProxyConfig.config.oomScoreAdj` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].kubeProxyConfig.extraArgs` **(deprecated)** | `map[string][]string` | no |  |
| `spec.cni.custom.documents[].kubeProxyConfig.mode` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].kubeProxyConfig.resources` **(deprecated)** | `object` | no | Talos: ResourcesConfig |
| `spec.cni.custom.documents[].kubeProxyConfig.resources.limits` **(deprecated)** | `map[string]integer or string` | no | ResourceList is a set of (resource name, quantity) pairs. |
| `spec.cni.custom.documents[].kubeProxyConfig.resources.requests` **(deprecated)** | `map[string]integer or string` | no | ResourceList is a set of (resource name, quantity) pairs. |
| `spec.cni.custom.documents[].kubeSchedulerConfig` **(deprecated)** | `object` | no | kubeSchedulerConfig is a Talos KubeSchedulerConfig document; populated when kind is "KubeSchedulerConfig". |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config` **(deprecated)** | `object` | yes | KubeSchedulerConfigurationSubset mirrors k8s.io/kube-scheduler/config/v1.KubeSchedulerConfiguration. Ichor does not carry DebuggingConfiguration, clientConnection, delayCacheUntilActive, extenders, leaderElection, parallelism, podInitialBackoffSeconds, podMaxBackoffSeconds. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.percentageOfNodesToScore` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].percentageOfNodesToScore` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins` **(deprecated)** | `object` | no | Plugins include multiple extension points. When specified, the list of plugins for a particular extension point are the only ones enabled. If an extension point is omitted from the config, then the default set of plugins is used for that extension point. Enabled plugins are called in the order specified here, after default plugins. If they need to be invoked before default plugins, default plugins must be disabled and re-enabled here in desired order. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.bind` **(deprecated)** | `object` | no | Bind is a list of plugins that should be invoked at "Bind" extension point of the scheduling framework. The scheduler call these plugins in order. Scheduler skips the rest of these plugins as soon as one returns success. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.bind.disabled` **(deprecated)** | `[]object` | no | Disabled specifies default plugins that should be disabled. When all default plugins need to be disabled, an array containing only one "*" should be provided. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.bind.disabled[].name` **(deprecated)** | `string` | yes | Name defines the name of plugin |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.bind.disabled[].weight` **(deprecated)** | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.bind.enabled` **(deprecated)** | `[]object` | no | Enabled specifies plugins that should be enabled in addition to default plugins. If the default plugin is also configured in the scheduler config file, the weight of plugin will be overridden accordingly. These are called after default plugins and in the same order specified here. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.bind.enabled[].name` **(deprecated)** | `string` | yes | Name defines the name of plugin |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.bind.enabled[].weight` **(deprecated)** | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.filter` **(deprecated)** | `object` | no | Filter is a list of plugins that should be invoked when filtering out nodes that cannot run the Pod. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.filter.disabled` **(deprecated)** | `[]object` | no | Disabled specifies default plugins that should be disabled. When all default plugins need to be disabled, an array containing only one "*" should be provided. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.filter.disabled[].name` **(deprecated)** | `string` | yes | Name defines the name of plugin |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.filter.disabled[].weight` **(deprecated)** | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.filter.enabled` **(deprecated)** | `[]object` | no | Enabled specifies plugins that should be enabled in addition to default plugins. If the default plugin is also configured in the scheduler config file, the weight of plugin will be overridden accordingly. These are called after default plugins and in the same order specified here. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.filter.enabled[].name` **(deprecated)** | `string` | yes | Name defines the name of plugin |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.filter.enabled[].weight` **(deprecated)** | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.multiPoint` **(deprecated)** | `object` | no | MultiPoint is a simplified config section to enable plugins for all valid extension points. Plugins enabled through MultiPoint will automatically register for every individual extension point the plugin has implemented. Disabling a plugin through MultiPoint disables that behavior. The same is true for disabling "*" through MultiPoint (no default plugins will be automatically registered). Plugins can still be disabled through their individual extension points. In terms of precedence, plugin config follows this basic hierarchy 1. Specific extension points 2. Explicitly configured MultiPoint plugins 3. The set of default plugins, as MultiPoint plugins This implies that a higher precedence plugin will run first and overwrite any settings within MultiPoint. Explicitly user-configured plugins also take a higher precedence over default plugins. Within this hierarchy, an Enabled setting takes precedence over Disabled. For example, if a plugin is set in both `multiPoint.Enabled` and `multiPoint.Disabled`, the plugin will be enabled. Similarly, including `multiPoint.Disabled = '*'` and `multiPoint.Enabled = pluginA` will still register that specific plugin through MultiPoint. This follows the same behavior as all other extension point configurations. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.multiPoint.disabled` **(deprecated)** | `[]object` | no | Disabled specifies default plugins that should be disabled. When all default plugins need to be disabled, an array containing only one "*" should be provided. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.multiPoint.disabled[].name` **(deprecated)** | `string` | yes | Name defines the name of plugin |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.multiPoint.disabled[].weight` **(deprecated)** | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.multiPoint.enabled` **(deprecated)** | `[]object` | no | Enabled specifies plugins that should be enabled in addition to default plugins. If the default plugin is also configured in the scheduler config file, the weight of plugin will be overridden accordingly. These are called after default plugins and in the same order specified here. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.multiPoint.enabled[].name` **(deprecated)** | `string` | yes | Name defines the name of plugin |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.multiPoint.enabled[].weight` **(deprecated)** | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.permit` **(deprecated)** | `object` | no | Permit is a list of plugins that control binding of a Pod. These plugins can prevent or delay binding of a Pod. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.permit.disabled` **(deprecated)** | `[]object` | no | Disabled specifies default plugins that should be disabled. When all default plugins need to be disabled, an array containing only one "*" should be provided. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.permit.disabled[].name` **(deprecated)** | `string` | yes | Name defines the name of plugin |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.permit.disabled[].weight` **(deprecated)** | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.permit.enabled` **(deprecated)** | `[]object` | no | Enabled specifies plugins that should be enabled in addition to default plugins. If the default plugin is also configured in the scheduler config file, the weight of plugin will be overridden accordingly. These are called after default plugins and in the same order specified here. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.permit.enabled[].name` **(deprecated)** | `string` | yes | Name defines the name of plugin |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.permit.enabled[].weight` **(deprecated)** | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.placementGenerate` **(deprecated)** | `object` | no | PlacementGenerate is a list of plugins that should be invoked during pod group scheduling cycle when determining placements for a pod group. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.placementGenerate.disabled` **(deprecated)** | `[]object` | no | Disabled specifies default plugins that should be disabled. When all default plugins need to be disabled, an array containing only one "*" should be provided. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.placementGenerate.disabled[].name` **(deprecated)** | `string` | yes | Name defines the name of plugin |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.placementGenerate.disabled[].weight` **(deprecated)** | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.placementGenerate.enabled` **(deprecated)** | `[]object` | no | Enabled specifies plugins that should be enabled in addition to default plugins. If the default plugin is also configured in the scheduler config file, the weight of plugin will be overridden accordingly. These are called after default plugins and in the same order specified here. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.placementGenerate.enabled[].name` **(deprecated)** | `string` | yes | Name defines the name of plugin |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.placementGenerate.enabled[].weight` **(deprecated)** | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.placementScore` **(deprecated)** | `object` | no | PlacementScore is a list of plugins that should be invoked during workload scheduling cycle when ranking pod group assignments. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.placementScore.disabled` **(deprecated)** | `[]object` | no | Disabled specifies default plugins that should be disabled. When all default plugins need to be disabled, an array containing only one "*" should be provided. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.placementScore.disabled[].name` **(deprecated)** | `string` | yes | Name defines the name of plugin |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.placementScore.disabled[].weight` **(deprecated)** | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.placementScore.enabled` **(deprecated)** | `[]object` | no | Enabled specifies plugins that should be enabled in addition to default plugins. If the default plugin is also configured in the scheduler config file, the weight of plugin will be overridden accordingly. These are called after default plugins and in the same order specified here. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.placementScore.enabled[].name` **(deprecated)** | `string` | yes | Name defines the name of plugin |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.placementScore.enabled[].weight` **(deprecated)** | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.postBind` **(deprecated)** | `object` | no | PostBind is a list of plugins that should be invoked after a pod is successfully bound. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.postBind.disabled` **(deprecated)** | `[]object` | no | Disabled specifies default plugins that should be disabled. When all default plugins need to be disabled, an array containing only one "*" should be provided. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.postBind.disabled[].name` **(deprecated)** | `string` | yes | Name defines the name of plugin |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.postBind.disabled[].weight` **(deprecated)** | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.postBind.enabled` **(deprecated)** | `[]object` | no | Enabled specifies plugins that should be enabled in addition to default plugins. If the default plugin is also configured in the scheduler config file, the weight of plugin will be overridden accordingly. These are called after default plugins and in the same order specified here. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.postBind.enabled[].name` **(deprecated)** | `string` | yes | Name defines the name of plugin |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.postBind.enabled[].weight` **(deprecated)** | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.postFilter` **(deprecated)** | `object` | no | PostFilter is a list of plugins that are invoked after filtering phase, but only when no feasible nodes were found for the pod. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.postFilter.disabled` **(deprecated)** | `[]object` | no | Disabled specifies default plugins that should be disabled. When all default plugins need to be disabled, an array containing only one "*" should be provided. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.postFilter.disabled[].name` **(deprecated)** | `string` | yes | Name defines the name of plugin |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.postFilter.disabled[].weight` **(deprecated)** | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.postFilter.enabled` **(deprecated)** | `[]object` | no | Enabled specifies plugins that should be enabled in addition to default plugins. If the default plugin is also configured in the scheduler config file, the weight of plugin will be overridden accordingly. These are called after default plugins and in the same order specified here. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.postFilter.enabled[].name` **(deprecated)** | `string` | yes | Name defines the name of plugin |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.postFilter.enabled[].weight` **(deprecated)** | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.preBind` **(deprecated)** | `object` | no | PreBind is a list of plugins that should be invoked before a pod is bound. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.preBind.disabled` **(deprecated)** | `[]object` | no | Disabled specifies default plugins that should be disabled. When all default plugins need to be disabled, an array containing only one "*" should be provided. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.preBind.disabled[].name` **(deprecated)** | `string` | yes | Name defines the name of plugin |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.preBind.disabled[].weight` **(deprecated)** | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.preBind.enabled` **(deprecated)** | `[]object` | no | Enabled specifies plugins that should be enabled in addition to default plugins. If the default plugin is also configured in the scheduler config file, the weight of plugin will be overridden accordingly. These are called after default plugins and in the same order specified here. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.preBind.enabled[].name` **(deprecated)** | `string` | yes | Name defines the name of plugin |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.preBind.enabled[].weight` **(deprecated)** | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.preEnqueue` **(deprecated)** | `object` | no | PreEnqueue is a list of plugins that should be invoked before adding pods to the scheduling queue. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.preEnqueue.disabled` **(deprecated)** | `[]object` | no | Disabled specifies default plugins that should be disabled. When all default plugins need to be disabled, an array containing only one "*" should be provided. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.preEnqueue.disabled[].name` **(deprecated)** | `string` | yes | Name defines the name of plugin |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.preEnqueue.disabled[].weight` **(deprecated)** | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.preEnqueue.enabled` **(deprecated)** | `[]object` | no | Enabled specifies plugins that should be enabled in addition to default plugins. If the default plugin is also configured in the scheduler config file, the weight of plugin will be overridden accordingly. These are called after default plugins and in the same order specified here. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.preEnqueue.enabled[].name` **(deprecated)** | `string` | yes | Name defines the name of plugin |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.preEnqueue.enabled[].weight` **(deprecated)** | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.preFilter` **(deprecated)** | `object` | no | PreFilter is a list of plugins that should be invoked at "PreFilter" extension point of the scheduling framework. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.preFilter.disabled` **(deprecated)** | `[]object` | no | Disabled specifies default plugins that should be disabled. When all default plugins need to be disabled, an array containing only one "*" should be provided. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.preFilter.disabled[].name` **(deprecated)** | `string` | yes | Name defines the name of plugin |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.preFilter.disabled[].weight` **(deprecated)** | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.preFilter.enabled` **(deprecated)** | `[]object` | no | Enabled specifies plugins that should be enabled in addition to default plugins. If the default plugin is also configured in the scheduler config file, the weight of plugin will be overridden accordingly. These are called after default plugins and in the same order specified here. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.preFilter.enabled[].name` **(deprecated)** | `string` | yes | Name defines the name of plugin |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.preFilter.enabled[].weight` **(deprecated)** | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.preScore` **(deprecated)** | `object` | no | PreScore is a list of plugins that are invoked before scoring. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.preScore.disabled` **(deprecated)** | `[]object` | no | Disabled specifies default plugins that should be disabled. When all default plugins need to be disabled, an array containing only one "*" should be provided. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.preScore.disabled[].name` **(deprecated)** | `string` | yes | Name defines the name of plugin |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.preScore.disabled[].weight` **(deprecated)** | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.preScore.enabled` **(deprecated)** | `[]object` | no | Enabled specifies plugins that should be enabled in addition to default plugins. If the default plugin is also configured in the scheduler config file, the weight of plugin will be overridden accordingly. These are called after default plugins and in the same order specified here. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.preScore.enabled[].name` **(deprecated)** | `string` | yes | Name defines the name of plugin |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.preScore.enabled[].weight` **(deprecated)** | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.queueSort` **(deprecated)** | `object` | no | QueueSort is a list of plugins that should be invoked when sorting pods in the scheduling queue. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.queueSort.disabled` **(deprecated)** | `[]object` | no | Disabled specifies default plugins that should be disabled. When all default plugins need to be disabled, an array containing only one "*" should be provided. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.queueSort.disabled[].name` **(deprecated)** | `string` | yes | Name defines the name of plugin |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.queueSort.disabled[].weight` **(deprecated)** | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.queueSort.enabled` **(deprecated)** | `[]object` | no | Enabled specifies plugins that should be enabled in addition to default plugins. If the default plugin is also configured in the scheduler config file, the weight of plugin will be overridden accordingly. These are called after default plugins and in the same order specified here. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.queueSort.enabled[].name` **(deprecated)** | `string` | yes | Name defines the name of plugin |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.queueSort.enabled[].weight` **(deprecated)** | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.reserve` **(deprecated)** | `object` | no | Reserve is a list of plugins invoked when reserving/unreserving resources after a node is assigned to run the pod. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.reserve.disabled` **(deprecated)** | `[]object` | no | Disabled specifies default plugins that should be disabled. When all default plugins need to be disabled, an array containing only one "*" should be provided. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.reserve.disabled[].name` **(deprecated)** | `string` | yes | Name defines the name of plugin |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.reserve.disabled[].weight` **(deprecated)** | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.reserve.enabled` **(deprecated)** | `[]object` | no | Enabled specifies plugins that should be enabled in addition to default plugins. If the default plugin is also configured in the scheduler config file, the weight of plugin will be overridden accordingly. These are called after default plugins and in the same order specified here. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.reserve.enabled[].name` **(deprecated)** | `string` | yes | Name defines the name of plugin |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.reserve.enabled[].weight` **(deprecated)** | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.score` **(deprecated)** | `object` | no | Score is a list of plugins that should be invoked when ranking nodes that have passed the filtering phase. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.score.disabled` **(deprecated)** | `[]object` | no | Disabled specifies default plugins that should be disabled. When all default plugins need to be disabled, an array containing only one "*" should be provided. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.score.disabled[].name` **(deprecated)** | `string` | yes | Name defines the name of plugin |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.score.disabled[].weight` **(deprecated)** | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.score.enabled` **(deprecated)** | `[]object` | no | Enabled specifies plugins that should be enabled in addition to default plugins. If the default plugin is also configured in the scheduler config file, the weight of plugin will be overridden accordingly. These are called after default plugins and in the same order specified here. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.score.enabled[].name` **(deprecated)** | `string` | yes | Name defines the name of plugin |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].plugins.score.enabled[].weight` **(deprecated)** | `integer` | no | Weight defines the weight of plugin, only used for Score and PlacementScore plugins. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.config.profiles[].schedulerName` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].kubeSchedulerConfig.env` **(deprecated)** | `map[string]string` | no |  |
| `spec.cni.custom.documents[].kubeSchedulerConfig.extraArgs` **(deprecated)** | `map[string][]string` | no |  |
| `spec.cni.custom.documents[].kubeSchedulerConfig.resources` **(deprecated)** | `object` | no | Talos: ResourcesConfig |
| `spec.cni.custom.documents[].kubeSchedulerConfig.resources.limits` **(deprecated)** | `map[string]integer or string` | no | ResourceList is a set of (resource name, quantity) pairs. |
| `spec.cni.custom.documents[].kubeSchedulerConfig.resources.requests` **(deprecated)** | `map[string]integer or string` | no | ResourceList is a set of (resource name, quantity) pairs. |
| `spec.cni.custom.documents[].kubeSpanConfig` **(deprecated)** | `object` | no | kubeSpanConfig is a Talos KubeSpanConfig document; populated when kind is "KubeSpanConfig". |
| `spec.cni.custom.documents[].kubeSpanConfig.advertiseKubernetesNetworks` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].kubeSpanConfig.allowDownPeerBypass` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].kubeSpanConfig.enabled` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].kubeSpanConfig.filters` **(deprecated)** | `object` | no | Talos: KubeSpanFiltersConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/network/kubespanconfig |
| `spec.cni.custom.documents[].kubeSpanConfig.filters.endpoints` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].kubeSpanConfig.filters.excludeAdvertisedNetworks` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].kubeSpanConfig.harvestExtraEndpoints` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].kubeSpanConfig.mtu` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].kubeSpanEndpointsConfig` **(deprecated)** | `object` | no | kubeSpanEndpointsConfig is a Talos KubeSpanEndpointsConfig document; populated when kind is "KubeSpanEndpointsConfig". |
| `spec.cni.custom.documents[].kubeSpanEndpointsConfig.extraAnnouncedEndpoints` **(deprecated)** | `[]string` | yes |  |
| `spec.cni.custom.documents[].kubeTalosAPIAccessConfig` **(deprecated)** | `object` | no | kubeTalosAPIAccessConfig is a Talos KubeTalosAPIAccessConfig document; populated when kind is "KubeTalosAPIAccessConfig". |
| `spec.cni.custom.documents[].kubeTalosAPIAccessConfig.allowedKubernetesNamespaces` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].kubeTalosAPIAccessConfig.allowedRoles` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].kubeletConfig` **(deprecated)** | `object` | no | kubeletConfig is a Talos KubeletConfig document; populated when kind is "KubeletConfig". |
| `spec.cni.custom.documents[].kubeletConfig.clusterDNS` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.config` **(deprecated)** | `object` | yes | KubeletConfigurationSubset mirrors k8s.io/kubelet/config/v1beta1.KubeletConfiguration. Ichor does not carry address, authentication, authorization, cgroupDriver, cgroupRoot, cgroupsPerQOS, clusterDNS, configMapAndSecretChangeDetectionStrategy, containerRuntimeEndpoint, contentType, cpuCFSQuota, cpuCFSQuotaPeriod, crashLoopBackOff, enableContentionProfiling, enableControllerAttachDetach, enableDebugFlagsHandler, enableDebuggingHandlers, enableProfilingHandler, enableServer, enableSystemLogHandler, enableSystemLogQuery, enforceNodeAllocatable, eventBurst, eventRecordQPS, failCgroupV1, fileCheckFrequency, hairpinMode, healthzBindAddress, healthzPort, httpCheckFrequency, imagePullCredentialsVerificationPolicy, imageServiceEndpoint, iptablesDropBit, iptablesMasqueradeBit, kernelMemcgNotification, kubeAPIBurst, kubeAPIQPS, kubeReservedCgroup, kubeletCgroups, localStorageCapacityIsolation, logging, makeIPTablesUtilChains, maxOpenFiles, memoryReservationPolicy, memoryThrottlingFactor, nodeLeaseDurationSeconds, nodeStatusMaxImages, nodeStatusReportFrequency, nodeStatusUpdateFrequency, oomScoreAdj, podCIDR, podLogsDir, podsPerCore, port, preloadedImagesVerificationAllowlist, protectKernelDefaults, providerID, qosReserved, readOnlyPort, registerNode, registryBurst, registryPullQPS, reservedSystemCPUs, resolvConf, rotateCertificates, runOnce, runtimeRequestTimeout, seccompDefault, showHiddenMetricsForVersion, singleProcessOOMKill, staticPodPath, staticPodURL, staticPodURLHeader, streamingConnectionIdleTimeout, syncFrequency, systemCgroups, systemReservedCgroup, tlsCertFile, tlsCipherSuites, tlsCurvePreferences, tlsMinVersion, tlsPrivateKeyFile, volumePluginDir, volumeStatsAggPeriod. |
| `spec.cni.custom.documents[].kubeletConfig.config.allowedUnsafeSysctls` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.config.clusterDomain` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.config.containerLogMaxFiles` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.config.containerLogMaxSize` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.config.containerLogMaxWorkers` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.config.containerLogMonitorInterval` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.config.cpuManagerPolicy` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.config.cpuManagerPolicyOptions` **(deprecated)** | `map[string]string` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.config.cpuManagerReconcilePeriod` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.config.evictionHard` **(deprecated)** | `map[string]string` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.config.evictionMaxPodGracePeriod` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.config.evictionMinimumReclaim` **(deprecated)** | `map[string]string` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.config.evictionPressureTransitionPeriod` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.config.evictionSoft` **(deprecated)** | `map[string]string` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.config.evictionSoftGracePeriod` **(deprecated)** | `map[string]string` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.config.failSwapOn` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.config.featureGates` **(deprecated)** | `map[string]boolean` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.config.imageGCHighThresholdPercent` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.config.imageGCLowThresholdPercent` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.config.imageMaximumGCAge` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.config.imageMinimumGCAge` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.config.kubeReserved` **(deprecated)** | `map[string]string` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.config.maxParallelImagePulls` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.config.maxPods` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.config.memoryManagerPolicy` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.config.memorySwap` **(deprecated)** | `object` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.config.memorySwap.swapBehavior` **(deprecated)** | `string` | no | swapBehavior configures swap memory available to container workloads. May be one of "", "NoSwap": workloads can not use swap, default option. "LimitedSwap": workload swap usage is limited. The swap limit is proportionate to the container's memory request. |
| `spec.cni.custom.documents[].kubeletConfig.config.mergeDefaultEvictionSettings` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.config.podPidsLimit` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.config.registerWithTaints` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.config.registerWithTaints[].effect` **(deprecated)** | `string` | yes | Required. The effect of the taint on pods that do not tolerate the taint. Valid effects are NoSchedule, PreferNoSchedule and NoExecute. |
| `spec.cni.custom.documents[].kubeletConfig.config.registerWithTaints[].key` **(deprecated)** | `string` | yes | Required. The taint key to be applied to a node. |
| `spec.cni.custom.documents[].kubeletConfig.config.registerWithTaints[].timeAdded` **(deprecated)** | `string (date-time)` | no | TimeAdded represents the time at which the taint was added. |
| `spec.cni.custom.documents[].kubeletConfig.config.registerWithTaints[].value` **(deprecated)** | `string` | no | The taint value corresponding to the taint key. |
| `spec.cni.custom.documents[].kubeletConfig.config.reservedMemory` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.config.reservedMemory[].limits` **(deprecated)** | `map[string]integer or string` | yes | ResourceList is a set of (resource name, quantity) pairs. |
| `spec.cni.custom.documents[].kubeletConfig.config.reservedMemory[].numaNode` **(deprecated)** | `integer` | yes |  |
| `spec.cni.custom.documents[].kubeletConfig.config.serializeImagePulls` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.config.serverTLSBootstrap` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.config.shutdownGracePeriod` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.config.shutdownGracePeriodByPodPriority` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.config.shutdownGracePeriodByPodPriority[].priority` **(deprecated)** | `integer` | yes | priority is the priority value associated with the shutdown grace period |
| `spec.cni.custom.documents[].kubeletConfig.config.shutdownGracePeriodByPodPriority[].shutdownGracePeriodSeconds` **(deprecated)** | `integer` | yes | shutdownGracePeriodSeconds is the shutdown grace period in seconds |
| `spec.cni.custom.documents[].kubeletConfig.config.shutdownGracePeriodCriticalPods` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.config.systemReserved` **(deprecated)** | `map[string]string` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.config.topologyManagerPolicy` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.config.topologyManagerPolicyOptions` **(deprecated)** | `map[string]string` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.config.topologyManagerScope` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.config.tracing` **(deprecated)** | `object` | no | TracingConfiguration provides versioned configuration for OpenTelemetry tracing clients. |
| `spec.cni.custom.documents[].kubeletConfig.config.tracing.endpoint` **(deprecated)** | `string` | no | Endpoint of the collector this component will report traces to. The connection is insecure, and does not currently support TLS. Recommended is unset, and endpoint is the otlp grpc default, localhost:4317. |
| `spec.cni.custom.documents[].kubeletConfig.config.tracing.samplingRatePerMillion` **(deprecated)** | `integer` | no | SamplingRatePerMillion is the number of samples to collect per million spans. Recommended is unset. If unset, sampler respects its parent span's sampling rate, but otherwise never samples. |
| `spec.cni.custom.documents[].kubeletConfig.config.userNamespaces` **(deprecated)** | `object` | no | UserNamespaces contains User Namespace configurations. |
| `spec.cni.custom.documents[].kubeletConfig.config.userNamespaces.idsPerPod` **(deprecated)** | `integer` | no | IDsPerPod is the mapping length of UIDs and GIDs. The length must be a multiple of 65536, and must be less than 1\<\<32. On non-linux such as windows, only null / absent is allowed. Changing the value may require recreating all containers on the node. Default: 65536 |
| `spec.cni.custom.documents[].kubeletConfig.defaultRuntimeSeccompProfileEnabled` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].kubeletConfig.extraArgs` **(deprecated)** | `map[string][]string` | no |  |
| `spec.cni.custom.documents[].layer2VIPConfig` **(deprecated)** | `object` | no | layer2VIPConfig is a Talos Layer2VIPConfig document; populated when kind is "Layer2VIPConfig". |
| `spec.cni.custom.documents[].layer2VIPConfig.link` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].layer2VIPConfig.name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].lvmLogicalVolumeConfig` **(deprecated)** | `object` | no | lvmLogicalVolumeConfig is a Talos LVMLogicalVolumeConfig document; populated when kind is "LVMLogicalVolumeConfig". |
| `spec.cni.custom.documents[].lvmLogicalVolumeConfig.mirrors` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].lvmLogicalVolumeConfig.name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].lvmLogicalVolumeConfig.provisioning` **(deprecated)** | `object` | yes | Talos: LVMLogicalVolumeProvisioningSpec — https://docs.siderolabs.com/talos/v1.14/reference/configuration/storage/lvmlogicalvolumeconfig |
| `spec.cni.custom.documents[].lvmLogicalVolumeConfig.provisioning.maxSize` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].lvmLogicalVolumeConfig.provisioning.minSize` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].lvmLogicalVolumeConfig.provisioning.volumeGroup` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].lvmLogicalVolumeConfig.stripes` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].lvmLogicalVolumeConfig.type` **(deprecated)** | `integer` | yes |  |
| `spec.cni.custom.documents[].lvmVolumeGroupConfig` **(deprecated)** | `object` | no | lvmVolumeGroupConfig is a Talos LVMVolumeGroupConfig document; populated when kind is "LVMVolumeGroupConfig". |
| `spec.cni.custom.documents[].lvmVolumeGroupConfig.name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].lvmVolumeGroupConfig.provisioning` **(deprecated)** | `object` | yes | Talos: ProvisioningSpec — https://docs.siderolabs.com/talos/v1.14/reference/configuration/storage/lvmvolumegroupconfig |
| `spec.cni.custom.documents[].lvmVolumeGroupConfig.provisioning.volumeSelector` **(deprecated)** | `object` | no | Talos: LVMVolumeSelectorSpec — https://docs.siderolabs.com/talos/v1.14/reference/configuration/storage/lvmvolumegroupconfig |
| `spec.cni.custom.documents[].lvmVolumeGroupConfig.provisioning.volumeSelector.match` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].oomConfig` **(deprecated)** | `object` | no | oomConfig is a Talos OOMConfig document; populated when kind is "OOMConfig". |
| `spec.cni.custom.documents[].oomConfig.cgroupRankingExpression` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].oomConfig.sampleInterval` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].oomConfig.strictCgroupClassOrdering` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].oomConfig.triggerExpression` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].pciDriverRebindConfig` **(deprecated)** | `object` | no | pciDriverRebindConfig is a Talos PCIDriverRebindConfig document; populated when kind is "PCIDriverRebindConfig". |
| `spec.cni.custom.documents[].pciDriverRebindConfig.name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].pciDriverRebindConfig.targetDriver` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].raidArrayConfig` **(deprecated)** | `object` | no | raidArrayConfig is a Talos RAIDArrayConfig document; populated when kind is "RAIDArrayConfig". |
| `spec.cni.custom.documents[].raidArrayConfig.level` **(deprecated)** | `integer` | yes |  |
| `spec.cni.custom.documents[].raidArrayConfig.metadata` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].raidArrayConfig.name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].raidArrayConfig.provisioning` **(deprecated)** | `object` | yes | Talos: RAIDProvisioningSpec — https://docs.siderolabs.com/talos/v1.14/reference/configuration/storage/raidarrayconfig |
| `spec.cni.custom.documents[].raidArrayConfig.provisioning.volumeSelector` **(deprecated)** | `object` | yes | Talos: RAIDVolumeSelector — https://docs.siderolabs.com/talos/v1.14/reference/configuration/storage/raidarrayconfig |
| `spec.cni.custom.documents[].raidArrayConfig.provisioning.volumeSelector.match` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].rawVolumeConfig` **(deprecated)** | `object` | no | rawVolumeConfig is a Talos RawVolumeConfig document (an unformatted partition for a CSI provisioner); populated when kind is "RawVolumeConfig". |
| `spec.cni.custom.documents[].rawVolumeConfig.encryption` **(deprecated)** | `object` | no | Talos: EncryptionSpec |
| `spec.cni.custom.documents[].rawVolumeConfig.encryption.allowDiscards` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].rawVolumeConfig.encryption.blockSize` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].rawVolumeConfig.encryption.cipher` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].rawVolumeConfig.encryption.keySize` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].rawVolumeConfig.encryption.keys` **(deprecated)** | `[]object` | yes |  |
| `spec.cni.custom.documents[].rawVolumeConfig.encryption.keys[].kms` **(deprecated)** | `object` | no | Talos: EncryptionKeyKMS |
| `spec.cni.custom.documents[].rawVolumeConfig.encryption.keys[].kms.endpoint` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].rawVolumeConfig.encryption.keys[].lockToState` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].rawVolumeConfig.encryption.keys[].nodeID` **(deprecated)** | `object` | no | Talos: EncryptionKeyNodeID |
| `spec.cni.custom.documents[].rawVolumeConfig.encryption.keys[].slot` **(deprecated)** | `integer` | yes |  |
| `spec.cni.custom.documents[].rawVolumeConfig.encryption.keys[].tpm` **(deprecated)** | `object` | no | Talos: EncryptionKeyTPM |
| `spec.cni.custom.documents[].rawVolumeConfig.encryption.keys[].tpm.checkSecurebootStatusOnEnroll` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].rawVolumeConfig.encryption.keys[].tpm.options` **(deprecated)** | `object` | no | Talos: EncryptionKeyTPMOptions |
| `spec.cni.custom.documents[].rawVolumeConfig.encryption.keys[].tpm.options.pcrs` **(deprecated)** | `[]integer` | no |  |
| `spec.cni.custom.documents[].rawVolumeConfig.encryption.options` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].rawVolumeConfig.encryption.provider` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].rawVolumeConfig.name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].rawVolumeConfig.provisioning` **(deprecated)** | `object` | no | Talos: ProvisioningSpec |
| `spec.cni.custom.documents[].rawVolumeConfig.provisioning.diskSelector` **(deprecated)** | `object` | no | Talos: DiskSelector |
| `spec.cni.custom.documents[].rawVolumeConfig.provisioning.diskSelector.match` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].rawVolumeConfig.provisioning.grow` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].rawVolumeConfig.provisioning.maxSize` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].rawVolumeConfig.provisioning.minSize` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].registryMirrorConfig` **(deprecated)** | `object` | no | registryMirrorConfig is a Talos RegistryMirrorConfig document; populated when kind is "RegistryMirrorConfig". |
| `spec.cni.custom.documents[].registryMirrorConfig.endpoints` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].registryMirrorConfig.endpoints[].overridePath` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].registryMirrorConfig.endpoints[].url` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].registryMirrorConfig.name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].registryMirrorConfig.skipFallback` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].resolverConfig` **(deprecated)** | `object` | no | resolverConfig is a Talos ResolverConfig document (DNS nameservers and search domains); populated when kind is "ResolverConfig". |
| `spec.cni.custom.documents[].resolverConfig.hostDNS` **(deprecated)** | `object` | no | Talos: HostDNSConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/network/resolverconfig |
| `spec.cni.custom.documents[].resolverConfig.hostDNS.enabled` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].resolverConfig.hostDNS.forwardKubeDNSToHost` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].resolverConfig.hostDNS.resolveMemberNames` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].resolverConfig.nameservers` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].resolverConfig.nameservers[].address` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].resolverConfig.nameservers[].protocol` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].resolverConfig.nameservers[].tlsServerName` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].resolverConfig.searchDomains` **(deprecated)** | `object` | no | Talos: SearchDomainsConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/network/resolverconfig |
| `spec.cni.custom.documents[].resolverConfig.searchDomains.disableDefault` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].resolverConfig.searchDomains.domains` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].routingRuleConfig` **(deprecated)** | `object` | no | routingRuleConfig is a Talos RoutingRuleConfig document; populated when kind is "RoutingRuleConfig". |
| `spec.cni.custom.documents[].routingRuleConfig.action` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].routingRuleConfig.dst` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].routingRuleConfig.fwMark` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].routingRuleConfig.fwMask` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].routingRuleConfig.iifName` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].routingRuleConfig.name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].routingRuleConfig.oifName` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].routingRuleConfig.src` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].routingRuleConfig.table` **(deprecated)** | `integer` | yes |  |
| `spec.cni.custom.documents[].staticHostConfig` **(deprecated)** | `object` | no | staticHostConfig is a Talos StaticHostConfig document; populated when kind is "StaticHostConfig". |
| `spec.cni.custom.documents[].staticHostConfig.hostnames` **(deprecated)** | `[]string` | yes |  |
| `spec.cni.custom.documents[].staticHostConfig.name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].swapVolumeConfig` **(deprecated)** | `object` | no | swapVolumeConfig is a Talos SwapVolumeConfig document; populated when kind is "SwapVolumeConfig". |
| `spec.cni.custom.documents[].swapVolumeConfig.encryption` **(deprecated)** | `object` | no | Talos: EncryptionSpec |
| `spec.cni.custom.documents[].swapVolumeConfig.encryption.allowDiscards` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].swapVolumeConfig.encryption.blockSize` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].swapVolumeConfig.encryption.cipher` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].swapVolumeConfig.encryption.keySize` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].swapVolumeConfig.encryption.keys` **(deprecated)** | `[]object` | yes |  |
| `spec.cni.custom.documents[].swapVolumeConfig.encryption.keys[].kms` **(deprecated)** | `object` | no | Talos: EncryptionKeyKMS |
| `spec.cni.custom.documents[].swapVolumeConfig.encryption.keys[].kms.endpoint` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].swapVolumeConfig.encryption.keys[].lockToState` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].swapVolumeConfig.encryption.keys[].nodeID` **(deprecated)** | `object` | no | Talos: EncryptionKeyNodeID |
| `spec.cni.custom.documents[].swapVolumeConfig.encryption.keys[].slot` **(deprecated)** | `integer` | yes |  |
| `spec.cni.custom.documents[].swapVolumeConfig.encryption.keys[].tpm` **(deprecated)** | `object` | no | Talos: EncryptionKeyTPM |
| `spec.cni.custom.documents[].swapVolumeConfig.encryption.keys[].tpm.checkSecurebootStatusOnEnroll` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].swapVolumeConfig.encryption.keys[].tpm.options` **(deprecated)** | `object` | no | Talos: EncryptionKeyTPMOptions |
| `spec.cni.custom.documents[].swapVolumeConfig.encryption.keys[].tpm.options.pcrs` **(deprecated)** | `[]integer` | no |  |
| `spec.cni.custom.documents[].swapVolumeConfig.encryption.options` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].swapVolumeConfig.encryption.provider` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].swapVolumeConfig.name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].swapVolumeConfig.provisioning` **(deprecated)** | `object` | no | Talos: ProvisioningSpec |
| `spec.cni.custom.documents[].swapVolumeConfig.provisioning.diskSelector` **(deprecated)** | `object` | no | Talos: DiskSelector |
| `spec.cni.custom.documents[].swapVolumeConfig.provisioning.diskSelector.match` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].swapVolumeConfig.provisioning.grow` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].swapVolumeConfig.provisioning.maxSize` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].swapVolumeConfig.provisioning.minSize` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].sysctlConfig` **(deprecated)** | `object` | no | sysctlConfig is a Talos SysctlConfig document; populated when kind is "SysctlConfig". |
| `spec.cni.custom.documents[].sysctlConfig.params` **(deprecated)** | `map[string]string` | yes |  |
| `spec.cni.custom.documents[].sysfsConfig` **(deprecated)** | `object` | no | sysfsConfig is a Talos SysfsConfig document; populated when kind is "SysfsConfig". |
| `spec.cni.custom.documents[].sysfsConfig.params` **(deprecated)** | `map[string]string` | yes |  |
| `spec.cni.custom.documents[].tcpProbeConfig` **(deprecated)** | `object` | no | tcpProbeConfig is a Talos TCPProbeConfig document; populated when kind is "TCPProbeConfig". |
| `spec.cni.custom.documents[].tcpProbeConfig.endpoint` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].tcpProbeConfig.failureThreshold` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].tcpProbeConfig.interval` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].tcpProbeConfig.name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].tcpProbeConfig.timeout` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].timeSyncConfig` **(deprecated)** | `object` | no | timeSyncConfig is a Talos TimeSyncConfig document; populated when kind is "TimeSyncConfig". |
| `spec.cni.custom.documents[].timeSyncConfig.bootTimeout` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].timeSyncConfig.enabled` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].timeSyncConfig.ntp` **(deprecated)** | `object` | no | Talos: NTPConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/network/timesyncconfig |
| `spec.cni.custom.documents[].timeSyncConfig.ntp.servers` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].timeSyncConfig.ntp.useNTS` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].timeSyncConfig.ptp` **(deprecated)** | `object` | no | Talos: PTPConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/network/timesyncconfig |
| `spec.cni.custom.documents[].timeSyncConfig.ptp.devices` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].trustedRootsConfig` **(deprecated)** | `object` | no | trustedRootsConfig is a Talos TrustedRootsConfig document; populated when kind is "TrustedRootsConfig". |
| `spec.cni.custom.documents[].trustedRootsConfig.certificates` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].trustedRootsConfig.name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].udevRulesConfig` **(deprecated)** | `object` | no | udevRulesConfig is a Talos UdevRulesConfig document; populated when kind is "UdevRulesConfig". |
| `spec.cni.custom.documents[].udevRulesConfig.rules` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].userVolumeConfig` **(deprecated)** | `object` | no | userVolumeConfig is a Talos UserVolumeConfig document; populated when kind is "UserVolumeConfig". |
| `spec.cni.custom.documents[].userVolumeConfig.encryption` **(deprecated)** | `object` | no | Talos: EncryptionSpec |
| `spec.cni.custom.documents[].userVolumeConfig.encryption.allowDiscards` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].userVolumeConfig.encryption.blockSize` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].userVolumeConfig.encryption.cipher` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].userVolumeConfig.encryption.keySize` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].userVolumeConfig.encryption.keys` **(deprecated)** | `[]object` | yes |  |
| `spec.cni.custom.documents[].userVolumeConfig.encryption.keys[].kms` **(deprecated)** | `object` | no | Talos: EncryptionKeyKMS |
| `spec.cni.custom.documents[].userVolumeConfig.encryption.keys[].kms.endpoint` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].userVolumeConfig.encryption.keys[].lockToState` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].userVolumeConfig.encryption.keys[].nodeID` **(deprecated)** | `object` | no | Talos: EncryptionKeyNodeID |
| `spec.cni.custom.documents[].userVolumeConfig.encryption.keys[].slot` **(deprecated)** | `integer` | yes |  |
| `spec.cni.custom.documents[].userVolumeConfig.encryption.keys[].tpm` **(deprecated)** | `object` | no | Talos: EncryptionKeyTPM |
| `spec.cni.custom.documents[].userVolumeConfig.encryption.keys[].tpm.checkSecurebootStatusOnEnroll` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].userVolumeConfig.encryption.keys[].tpm.options` **(deprecated)** | `object` | no | Talos: EncryptionKeyTPMOptions |
| `spec.cni.custom.documents[].userVolumeConfig.encryption.keys[].tpm.options.pcrs` **(deprecated)** | `[]integer` | no |  |
| `spec.cni.custom.documents[].userVolumeConfig.encryption.options` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].userVolumeConfig.encryption.provider` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].userVolumeConfig.filesystem` **(deprecated)** | `object` | no | Talos: FilesystemSpec — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/uservolumeconfig |
| `spec.cni.custom.documents[].userVolumeConfig.filesystem.projectQuotaSupport` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].userVolumeConfig.filesystem.type` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].userVolumeConfig.filesystem.xfs` **(deprecated)** | `object` | no | Talos: XFSSpec |
| `spec.cni.custom.documents[].userVolumeConfig.filesystem.xfs.minAllocationGroupSize` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].userVolumeConfig.mount` **(deprecated)** | `object` | no | Talos: UserMountSpec — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/uservolumeconfig |
| `spec.cni.custom.documents[].userVolumeConfig.mount.disableAccessTime` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].userVolumeConfig.mount.secure` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].userVolumeConfig.name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].userVolumeConfig.provisioning` **(deprecated)** | `object` | no | Talos: ProvisioningSpec |
| `spec.cni.custom.documents[].userVolumeConfig.provisioning.diskSelector` **(deprecated)** | `object` | no | Talos: DiskSelector |
| `spec.cni.custom.documents[].userVolumeConfig.provisioning.diskSelector.match` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].userVolumeConfig.provisioning.grow` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].userVolumeConfig.provisioning.maxSize` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].userVolumeConfig.provisioning.minSize` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].userVolumeConfig.scrub` **(deprecated)** | `object` | no | Talos: ScrubConfig |
| `spec.cni.custom.documents[].userVolumeConfig.scrub.enabled` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].userVolumeConfig.scrub.interval` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].userVolumeConfig.trim` **(deprecated)** | `object` | no | Talos: TrimConfig |
| `spec.cni.custom.documents[].userVolumeConfig.trim.enabled` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].userVolumeConfig.trim.interval` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].userVolumeConfig.volumeType` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].vethConfig` **(deprecated)** | `object` | no | vethConfig is a Talos VethConfig document; populated when kind is "VethConfig". |
| `spec.cni.custom.documents[].vethConfig.addresses` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].vethConfig.addresses[].address` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].vethConfig.addresses[].routePriority` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].vethConfig.mtu` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].vethConfig.multicast` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].vethConfig.name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].vethConfig.peer` **(deprecated)** | `object` | yes | Talos: VethPeerConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/network/vethconfig |
| `spec.cni.custom.documents[].vethConfig.peer.addresses` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].vethConfig.peer.addresses[].address` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].vethConfig.peer.addresses[].routePriority` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].vethConfig.peer.mtu` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].vethConfig.peer.multicast` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].vethConfig.peer.name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].vethConfig.peer.routes` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].vethConfig.peer.routes[].destination` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].vethConfig.peer.routes[].gateway` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].vethConfig.peer.routes[].metric` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].vethConfig.peer.routes[].mtu` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].vethConfig.peer.routes[].source` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].vethConfig.peer.routes[].table` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].vethConfig.peer.up` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].vethConfig.routes` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].vethConfig.routes[].destination` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].vethConfig.routes[].gateway` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].vethConfig.routes[].metric` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].vethConfig.routes[].mtu` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].vethConfig.routes[].source` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].vethConfig.routes[].table` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].vethConfig.up` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].volumeConfig` **(deprecated)** | `object` | no | volumeConfig is a Talos VolumeConfig document (a system volume, e.g. STATE or EPHEMERAL, placed on its own disk); populated when kind is "VolumeConfig". |
| `spec.cni.custom.documents[].volumeConfig.encryption` **(deprecated)** | `object` | no | Talos: EncryptionSpec |
| `spec.cni.custom.documents[].volumeConfig.encryption.allowDiscards` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].volumeConfig.encryption.blockSize` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].volumeConfig.encryption.cipher` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].volumeConfig.encryption.keySize` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].volumeConfig.encryption.keys` **(deprecated)** | `[]object` | yes |  |
| `spec.cni.custom.documents[].volumeConfig.encryption.keys[].kms` **(deprecated)** | `object` | no | Talos: EncryptionKeyKMS |
| `spec.cni.custom.documents[].volumeConfig.encryption.keys[].kms.endpoint` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].volumeConfig.encryption.keys[].lockToState` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].volumeConfig.encryption.keys[].nodeID` **(deprecated)** | `object` | no | Talos: EncryptionKeyNodeID |
| `spec.cni.custom.documents[].volumeConfig.encryption.keys[].slot` **(deprecated)** | `integer` | yes |  |
| `spec.cni.custom.documents[].volumeConfig.encryption.keys[].tpm` **(deprecated)** | `object` | no | Talos: EncryptionKeyTPM |
| `spec.cni.custom.documents[].volumeConfig.encryption.keys[].tpm.checkSecurebootStatusOnEnroll` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].volumeConfig.encryption.keys[].tpm.options` **(deprecated)** | `object` | no | Talos: EncryptionKeyTPMOptions |
| `spec.cni.custom.documents[].volumeConfig.encryption.keys[].tpm.options.pcrs` **(deprecated)** | `[]integer` | no |  |
| `spec.cni.custom.documents[].volumeConfig.encryption.options` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].volumeConfig.encryption.provider` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].volumeConfig.filesystem` **(deprecated)** | `object` | no | Talos: SystemVolumeFilesystemSpec — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/volumeconfig |
| `spec.cni.custom.documents[].volumeConfig.filesystem.xfs` **(deprecated)** | `object` | no | Talos: XFSSpec |
| `spec.cni.custom.documents[].volumeConfig.filesystem.xfs.minAllocationGroupSize` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].volumeConfig.mount` **(deprecated)** | `object` | no | Talos: MountSpec — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/volumeconfig |
| `spec.cni.custom.documents[].volumeConfig.mount.disableAccessTime` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].volumeConfig.mount.secure` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].volumeConfig.name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].volumeConfig.provisioning` **(deprecated)** | `object` | no | Talos: ProvisioningSpec |
| `spec.cni.custom.documents[].volumeConfig.provisioning.diskSelector` **(deprecated)** | `object` | no | Talos: DiskSelector |
| `spec.cni.custom.documents[].volumeConfig.provisioning.diskSelector.match` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].volumeConfig.provisioning.grow` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].volumeConfig.provisioning.maxSize` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].volumeConfig.provisioning.minSize` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].volumeConfig.scrub` **(deprecated)** | `object` | no | Talos: ScrubConfig |
| `spec.cni.custom.documents[].volumeConfig.scrub.enabled` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].volumeConfig.scrub.interval` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].volumeConfig.trim` **(deprecated)** | `object` | no | Talos: TrimConfig |
| `spec.cni.custom.documents[].volumeConfig.trim.enabled` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].volumeConfig.trim.interval` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].vrfConfig` **(deprecated)** | `object` | no | vrfConfig is a Talos VRFConfig document; populated when kind is "VRFConfig". |
| `spec.cni.custom.documents[].vrfConfig.addresses` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].vrfConfig.addresses[].address` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].vrfConfig.addresses[].routePriority` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].vrfConfig.hardwareAddr` **(deprecated)** | `string (byte)` | no |  |
| `spec.cni.custom.documents[].vrfConfig.links` **(deprecated)** | `[]string` | no |  |
| `spec.cni.custom.documents[].vrfConfig.mtu` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].vrfConfig.multicast` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].vrfConfig.name` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].vrfConfig.routes` **(deprecated)** | `[]object` | no |  |
| `spec.cni.custom.documents[].vrfConfig.routes[].destination` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].vrfConfig.routes[].gateway` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].vrfConfig.routes[].metric` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].vrfConfig.routes[].mtu` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].vrfConfig.routes[].source` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].vrfConfig.routes[].table` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].vrfConfig.table` **(deprecated)** | `integer` | yes |  |
| `spec.cni.custom.documents[].vrfConfig.up` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.documents[].watchdogTimerConfig` **(deprecated)** | `object` | no | watchdogTimerConfig is a Talos WatchdogTimerConfig document; populated when kind is "WatchdogTimerConfig". |
| `spec.cni.custom.documents[].watchdogTimerConfig.device` **(deprecated)** | `string` | yes |  |
| `spec.cni.custom.documents[].watchdogTimerConfig.timeout` **(deprecated)** | `string` | no |  |
| `spec.cni.custom.documents[].zswapConfig` **(deprecated)** | `object` | no | zswapConfig is a Talos ZswapConfig document; populated when kind is "ZswapConfig". |
| `spec.cni.custom.documents[].zswapConfig.maxPoolPercent` **(deprecated)** | `integer` | no |  |
| `spec.cni.custom.documents[].zswapConfig.shrinkerEnabled` **(deprecated)** | `boolean` | no |  |
| `spec.cni.custom.source` **(deprecated)** | `object` | yes | source points Argo CD at the chart or manifests for this CNI. |
| `spec.cni.custom.source.chart` **(deprecated)** | `string` | no | chart is a Helm chart name within repoURL; set instead of path. |
| `spec.cni.custom.source.path` **(deprecated)** | `string` | no | path is a directory within repoURL holding plain manifests; set instead of chart. |
| `spec.cni.custom.source.repoURL` **(deprecated)** | `string` | yes | repoURL is the Git or Helm repository Argo CD syncs from. |
| `spec.cni.custom.source.targetRevision` **(deprecated)** | `string` | yes | targetRevision is the Git ref or chart version Argo CD syncs. |
| `spec.cni.kubeProxy` | `string` | no | kubeProxy is enabled (Talos's own default) or replaced by the CNI. Replacing it splits the setting across two owners -- Talos (cluster.proxy.disabled, written by Ichor) and the CNI (written by Argo CD) -- so a consistency check reads the CNI's live configuration and reports a mismatch as a health failure. Allowed values: `enabled`, `replaced`. Default: `"enabled"`. |
| `spec.cni.preset` **(deprecated)** | `string` | no | preset selects the CNI this cluster bootstraps. Cilium is the only CNI Ichor installs; unmanaged leaves the CNI of an adopted cluster exactly as it is, and the webhook accepts it on create only for an object carrying the adoption annotation. The values flannel and custom are deprecated, unused and kept so the CRD never drops a value. Allowed values: `flannel`, `cilium`, `custom`, `unmanaged`. Default: `"cilium"`. |
| `spec.cni.tunnel` | `string` | no | tunnel is Cilium's tunnel protocol. It only applies to the cilium preset; empty means Cilium's default (vxlan). Immutable once set. Allowed values: `vxlan`, `geneve`. |
| `spec.cni.unmanaged` | `object` | no | unmanaged configures the unmanaged preset. Only valid when preset is unmanaged; when omitted there, talosCNI is default. |
| `spec.cni.unmanaged.talosCNI` | `string` | no | talosCNI is default (the machine config has no CNI selection, so Talos runs its own flannel or whatever cluster.network.cni selects by default; render emits nothing about the CNI) or none (render emits cluster.network.cni.name: none). Allowed values: `default`, `none`. Default: `"default"`. |
| `spec.controlPlane` | `object` | yes | controlPlane configures the control-plane topology. |
| `spec.controlPlane.count` | `integer` | yes | count is the number of control-plane nodes; 2 and 4 are rejected because they don't improve etcd availability over 1 or 3. Allowed values: `1`, `3`, `5`. |
| `spec.controlPlane.omitDefaultNodeLabels` | `boolean` | no | omitDefaultNodeLabels renders the control planes without the labels Talos's generator adds to them (node-role.kubernetes.io/control-plane and node.kubernetes.io/exclude-from-external-load-balancers). `ichor adopt` sets it when the control planes it takes over do not carry them, so that adopting does not change the nodes: the exclude label removes a node from Service type=LoadBalancer membership. A cluster Ichor creates leaves it unset and gets Talos's defaults. |
| `spec.controlPlane.schedulable` | `string` | yes | schedulable controls whether control planes accept workloads; required with no default because nothing on the object can resolve an auto value before any machine exists. Allowed values: `true`, `false`. |
| `spec.controlPlaneMetrics` | `object` | no | controlPlaneMetrics exposes the control-plane metrics Talos binds locally and restricts scraping to a CIDR. Unset (the default) leaves the Talos defaults alone. |
| `spec.controlPlaneMetrics.allowedCIDRs` | `[]string` | no | allowedCIDRs are the only sources that may reach the metrics ports (TCP 10257, 10259 and 2381) on control-plane nodes. Every other port keeps Talos's default firewall action (accept). A prefix length of 0 is rejected: that would be no restriction at all. |
| `spec.controlPlaneMetrics.enabled` | `boolean` | no | enabled binds the metrics endpoints on all addresses and adds ingress firewall rules that drop connections to those ports from any source outside allowedCIDRs. Default: `false`. |
| `spec.discovery` | `object` | no | discovery lists maintenance-mode candidates for the controller to probe directly. Empty (the default) disables discovery: the controller opens no connection at all. |
| `spec.discovery.addresses` | `[]string` | no | addresses are specific IPv4 candidates to probe, each on Talos's fixed maintenance API port. |
| `spec.discovery.cidrs` | `[]string` | no | cidrs are IPv4 address ranges to probe, one maintenance-mode connection attempt per address in range. Each must be /24 or smaller (at most 256 addresses). |
| `spec.driftPolicy` | `string` | no | driftPolicy controls what the reconciler does when a node's live configuration no longer matches what Ichor last rendered for it, and nothing on Ichor's side (this TalosCluster, a TalosConfigPatch, or the TalosMachine) has changed since Ichor last confirmed the two matched: warnOnly leaves the node as it is and reports the drift through a condition; autoCorrect re-applies, but only when every changed field is reboot-free (pkg/fieldmeta) -- a reboot-requiring or install-time-only drift is always just reported, regardless of this setting. Defaults to warnOnly: unlike controlPlane.schedulable, the two values here are not wrong half the time each -- silently changing a node is the one that can surprise, so it is never the default. The CRD carries no default: the defaulting webhook fills warnOnly, or the installation's spec.defaults.driftPolicy, at create; an empty value is warnOnly. Allowed values: `warnOnly`, `autoCorrect`. |
| `spec.endpoint` | `object` | yes | endpoint is the cluster's control-plane endpoint. |
| `spec.endpoint.address` | `string` | yes | address is the endpoint's address: an IP for vip/node, or an IP or a hostname for loadBalancer. |
| `spec.endpoint.type` | `string` | yes | type is the kind of address the endpoint resolves through. vip is an address Ichor itself puts on every control plane that has a spec.network, as a shared IP on its primary link; an address something else holds, such as a hardware load balancer or a VIP the cluster already runs, is loadBalancer. Allowed values: `vip`, `loadBalancer`, `node`. |
| `spec.kubernetesVersion` | `string` | yes | kubernetesVersion is the Kubernetes release this cluster runs. |
| `spec.network` | `object` | no | network sets the pod and service subnets. Unset keeps Talos's defaults. It cannot change once set. |
| `spec.network.podSubnets` | `[]string` | no | podSubnets are the pod CIDRs, at most one per IP family. |
| `spec.network.serviceSubnets` | `[]string` | no | serviceSubnets are the service CIDRs, at most one per IP family. |
| `spec.registries` | `[]object` | no | registries declares this cluster's private registry mirrors and where their pull credentials live, pushed to every node through the system layer (docs/design/architecture.md "Monitoring and registry mirrors"). Empty (the default) configures no mirrors at all -- mirrors stay entirely optional. listType=map on host (rather than a CEL uniqueness rule, same as status.conditions below) is what the structural schema itself rejects a duplicate host under -- a CEL self.exists_one rule over this list was tried first and rejected by envtest's apiserver: with MaxItems=32 its estimated cost exceeds the per-field budget by 32x (quadratic in list length), and still contributed enough to blow the whole-schema budget by 3.3x alongside this CRD's existing CEL rules. |
| `spec.registries[].authSecretRef` | `object` | no | authSecretRef names a Secret, read in the controller's own namespace (the same reasoning as secretsRef below: RBAC cannot scope a Secret read to "wherever this TalosCluster happens to live" below cluster-wide), holding this registry's password-shaped credentials. Unset means an anonymous mirror -- a public registry needing no credentials at all, not an error. Set but unresolvable (Secret missing, or present but carrying none of its three recognized keys) is: pkg/render.ResolveRegistryAuth fails the render rather than silently producing a mirror with no auth, since that would look identical to a working one until the first pull fails on the node. A corev1.LocalObjectReference, not a SecretKeySelector: machinery's RegistryAuthConfig carries three mutually-exclusive credential shapes (password, auth, identityToken, matching docker config.json's own auth forms) that one registry's credentials pick at most one of, so one reference to a Secret with those three well-known keys covers it -- same shape secretsRef already uses for bundleSecretKey, rather than three separate single-key selectors for a value that is, in practice, one secret. |
| `spec.registries[].authSecretRef.name` | `string` | no | Name of the referent. This field is effectively required, but due to backwards compatibility is allowed to be empty. Instances of this type with an empty value here are almost certainly wrong. More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#names Default: `""`. |
| `spec.registries[].endpoints` | `[]string` | yes | endpoints are the mirror endpoint URLs for host (RegistryMirrorConfig.endpoints) -- not secret, so they stay a plain typed field here rather than behind a reference. Checked here, not left to machinery: pkg/machinery/config/types/v1alpha1's own ValidateAtRuntime has no validator at all for RegistryMirrorConfig/RegistryConfig, so an endpoint shaped wrong would otherwise reach a node's machine config unexamined, exactly the gap spec.discovery's own CEL above already closes for its addresses. |
| `spec.registries[].host` | `string` | yes | host is the registry host (and port, if not the default) this entry's mirror and credentials apply to, matching RegistriesConfig's own map key: the first segment of an image reference, "docker.io" being the default one, or "*" for a fallback mirror. TLS config (RegistryConfig.tls) uses the same key but never the "*" fallback; this field carries both mirror and auth, so "*" is only meaningful when authSecretRef is unset. |
| `spec.registries[].overridePath` | `boolean` | no | overridePath makes the nodes use every endpoint's path as given, without adding the /v2/ suffix (RegistryMirrorConfig.endpoints[].overridePath). It applies to all of this entry's endpoints; Talos allows it per endpoint, and a mix cannot be recorded here. |
| `spec.registries[].skipFallback` | `boolean` | no | skipFallback stops nodes falling back to host itself when none of the endpoints has the image (RegistryMirrorConfig.skipFallback). Default false, Talos's own default. |
| `spec.registries[].username` | `string` | no | username is this registry's non-secret auth username (RegistryAuthConfig.username). Optional: some private registries authenticate on password/token alone. |
| `spec.schematic` | `object` | no | schematic is the Image Factory schematic used for this cluster's installer image. |
| `spec.schematic.id` | `string` | yes | id is the Image Factory schematic ID. |
| `spec.secretsRef` | `object` | yes | secretsRef names the Secret holding the Talos, Kubernetes and etcd CA bundle. The Secret lives in the controller's own namespace (not necessarily this TalosCluster's), because Kubernetes RBAC cannot restrict Secret reads to "wherever the referencing TalosCluster happens to live" at anything narrower than cluster-wide. |
| `spec.secretsRef.name` | `string` | no | Name of the referent. This field is effectively required, but due to backwards compatibility is allowed to be empty. Instances of this type with an empty value here are almost certainly wrong. More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#names Default: `""`. |
| `spec.security` | `object` | no | security holds opt-in hardening of this cluster: an ingress firewall, Cilium WireGuard encryption, Pod Security levels, an audit policy preset and disk encryption. Unset (the default) changes nothing. |
| `spec.security.audit` | `object` | no | audit selects a kube-apiserver audit policy preset. Restarts the kube-apiserver on every control plane, no node reboot. Needs versionContract v1.14 or later. |
| `spec.security.audit.policy` | `string` | yes | policy is the preset. metadata records who did what, without bodies, for every request (Talos's own default). rbacChanges is metadata plus the full request and response of changes to roles and bindings. writes is metadata plus the request body of every change. Secrets, configmaps, service account tokens and token reviews are never logged above metadata in any preset. Allowed values: `metadata`, `rbacChanges`, `writes`. |
| `spec.security.diskEncryption` | `object` | no | diskEncryption encrypts the STATE and EPHEMERAL volumes with LUKS2. Talos encrypts a volume only when it creates it, so on a cluster that already has machines it needs the wipe-and-rejoin path (see docs/guides/hardening.md) and the confirm annotation. |
| `spec.security.diskEncryption.kmsEndpoint` | `string` | no | kmsEndpoint is the KMS server, as a URL with a scheme (https://kms.example.com:4443). A scheme other than https is refused. |
| `spec.security.diskEncryption.requireSecureBoot` | `boolean` | no | requireSecureBoot makes enrolling a TPM key fail unless SecureBoot is enabled in the firmware. Without SecureBoot a TPM key gives no boot-chain protection. |
| `spec.security.diskEncryption.type` | `string` | yes | type is where the key comes from. tpm seals it in the machine's TPM 2.0 (bound to PCR 7); nodeID derives it from the node UUID, which protects only against a removed disk; kms has a KMS server seal and unseal it. Allowed values: `tpm`, `nodeID`, `kms`. |
| `spec.security.firewall` | `object` | no | firewall turns on Talos's ingress firewall in block mode with the rules a Talos cluster needs, plus the sources you list. No reboot. |
| `spec.security.firewall.allow` | `[]object` | no | allow opens further host ports, for services running with host networking (an ingress controller on 80 and 443, a node exporter). Everything not listed is blocked. |
| `spec.security.firewall.allow[].cidrs` | `[]string` | yes | cidrs are the sources allowed to reach the ports. |
| `spec.security.firewall.allow[].name` | `string` | yes | name identifies the rule; it becomes ichor-fw-allow-\<name> in the machine config. |
| `spec.security.firewall.allow[].ports` | `[]string` | yes | ports are single ports ("443") or inclusive ranges ("8000-8100"). |
| `spec.security.firewall.allow[].protocol` | `string` | yes | protocol is tcp or udp. Allowed values: `tcp`, `udp`. |
| `spec.security.firewall.apiServerCIDRs` | `[]string` | no | apiServerCIDRs are further sources allowed to reach the Kubernetes API (6443), for users and load balancers. |
| `spec.security.firewall.controlPlaneCIDRs` | `[]string` | no | controlPlaneCIDRs are the addresses of the control planes only; etcd and the control planes' own reach to apid and the kubelet are limited to them. Unset means nodeCIDRs. |
| `spec.security.firewall.controllerCIDRs` | `[]string` | no | controllerCIDRs are the addresses Ichor's controller connects from. They are always allowed to reach apid and the Kubernetes API. The admission webhook refuses a value that does not contain the address the controller reaches the cluster endpoint from. |
| `spec.security.firewall.enabled` | `boolean` | no | enabled turns the firewall on. Default: `false`. |
| `spec.security.firewall.kubeSpan` | `boolean` | no | kubeSpan allows KubeSpan's WireGuard port (UDP 51820) from anywhere; set it when the cluster uses KubeSpan. |
| `spec.security.firewall.nodeCIDRs` | `[]string` | no | nodeCIDRs are the addresses of all the cluster's machines, control planes and workers. |
| `spec.security.firewall.talosAPICIDRs` | `[]string` | no | talosAPICIDRs are further sources allowed to reach apid (50000), for operators using talosctl. |
| `spec.security.kernelLockdown` | `string` | no | kernelLockdown is the kernel lockdown level the nodes are expected to run with. Ichor checks it and reports a node below it; it does not render it, because the kernel command line is embedded in the UKI (build it into the boot asset with an Image Factory schematic whose extraKernelArgs has lockdown=confidentiality, and set spec.schematic). SecureBoot images run at integrity by themselves in Talos v1.14. Allowed values: `integrity`, `confidentiality`. |
| `spec.security.kubeletServingCertificates` | `object` | no | kubeletServingCertificates makes every kubelet serve a certificate signed by the Kubernetes CA instead of a self-signed one. Ichor's controller approves the kubelets' serving-certificate requests itself, for the machines it manages and nothing else. A kubelet restart on every node, no node reboot. |
| `spec.security.kubeletServingCertificates.enabled` | `boolean` | no | enabled turns serverTLSBootstrap on and the controller's approval of the requests on. Default: `false`. |
| `spec.security.kubeletServingCertificates.verifyFromAPIServer` | `boolean` | no | verifyFromAPIServer also makes the kube-apiserver check the kubelets' certificates against the Kubernetes CA (--kubelet-certificate-authority). Ichor renders it only once every machine has been issued a serving certificate (condition KubeletServingCertificates), and keeps it once rendered. It restarts the kube-apiserver on every control plane. Until then the apiserver does not verify the kubelets, as before. |
| `spec.security.podSecurity` | `object` | no | podSecurity sets the Pod Security admission levels of the kube-apiserver. Restarts the kube-apiserver on every control plane, no node reboot. Needs versionContract v1.14 or later. |
| `spec.security.podSecurity.audit` | `string` | no | audit is the level above which violations are recorded in the audit log. Allowed values: `privileged`, `baseline`, `restricted`. |
| `spec.security.podSecurity.enforce` | `string` | no | enforce is the level a pod must meet to be admitted. Allowed values: `privileged`, `baseline`, `restricted`. |
| `spec.security.podSecurity.exemptNamespaces` | `[]string` | no | exemptNamespaces are added to the exempt namespaces (kube-system stays exempt). |
| `spec.security.podSecurity.warn` | `string` | no | warn is the level above which violations produce a warning to the client. Allowed values: `privileged`, `baseline`, `restricted`. |
| `spec.security.secureBoot` | `object` | no | secureBoot asks for UEFI SecureBoot with Talos's UKI images. It picks the installer image variant and reports each node's actual state; it never changes an installed node, because Talos cannot convert a GRUB install and the firmware keys are not Talos's. |
| `spec.security.secureBoot.mode` | `string` | yes | mode is Off (the default: the stock installer, nothing reported), Preferred (Ichor uses the SecureBoot installer where it chooses one and reports nodes without SecureBoot) or Required (as Preferred, a TPM disk key must check SecureBoot, enabling it needs the confirm annotation while any approved machine lacks SecureBoot, and a machine in maintenance mode without SecureBoot is not given its first configuration). A node that already runs SecureBoot is upgraded with the SecureBoot installer whatever the mode. Allowed values: `Off`, `Preferred`, `Required`. |
| `spec.security.wireGuard` | `object` | no | wireGuard turns on Cilium's transparent WireGuard encryption of pod traffic between nodes. A rolling restart of the Cilium agents, no node reboot. |
| `spec.security.wireGuard.enabled` | `boolean` | no | enabled sets encryption.enabled=true and encryption.type=wireguard in the managed Cilium values. It wins over any encryption.* the user values set. When the firewall is on, UDP 51871 is opened between nodes. Default: `false`. |
| `spec.security.wireGuard.nodeEncryption` | `boolean` | no | nodeEncryption also encrypts node-to-node traffic that is not pod traffic (encryption.nodeEncryption). |
| `spec.talosVersion` | `string` | yes | talosVersion is the Talos release this cluster renders configuration for. |
| `spec.versionContract` | `string` | yes | versionContract is required rather than defaulted, so that a machinery bump cannot silently change what gets applied (principle 4). |

### status

| Field | Type | Required | Description |
|---|---|---|---|
| `status` | `object` | no | status defines the observed state of TalosCluster |
| `status.certificates` | `[]object` | no | certificates lists when each cluster certificate authority in the secrets bundle expires, so an approaching expiry is visible before it locks anyone out. Read from the bundle, never from a node. |
| `status.certificates[].name` | `string` | yes | name is which authority: talos, kubernetes, kubernetes-aggregator or etcd. |
| `status.certificates[].notAfter` | `string (date-time)` | yes | notAfter is the certificate's last instant of validity. |
| `status.cni` | `object` | no | cni is the observed CNI state that is not a spec ingredient. |
| `status.cni.appliedValuesHash` | `string` | no | appliedValuesHash is the hash of the effective Cilium values the last successful Helm Job applied. The controller starts a new Job when the hash of spec.cni.cilium.values (merged with Ichor's defaults) differs from it. Written by the controller only. |
| `status.cni.availableChartVersion` | `string` | no | availableChartVersion is the Cilium chart version the running Ichor ships when it differs from the installed release; empty when up to date. It backs the CiliumUpgradeAvailable condition. |
| `status.cni.detected` | `string` | no | detected is the CNI the cluster is observed to run, read-only display and the network-policy gate. Written by the controller only. Allowed values: `cilium`, `flannel`, `calico`, `other`, `none`. |
| `status.cni.detectedVersion` | `string` | no | detectedVersion is the image tag of the detected CNI's DaemonSet where one exposes it. |
| `status.cni.imageOverride` | `object` | no | imageOverride is the Cilium patch an earlier Ichor recorded as the images the release runs over the embedded chart. Ichor sets no new one (amendment 2026-11-04 (3)). A recorded patch newer than the embedded chart keeps being rendered, so an Ichor upgrade never steps Cilium back, and the CiliumUpgradeAvailable condition says so; the controller clears it once the embedded chart reaches the patch. |
| `status.cni.imageOverride.images` | `map[string]string` | yes | images maps each image key to the reference the Job runs: "agent", "operator" and "hubble-relay" are repository:tag@sha256:digest, verified against the Cilium release signature. |
| `status.cni.imageOverride.version` | `string` | yes | version is the patch, with a leading v (v1.20.5). |
| `status.cni.jobFailures` | `integer` | no | jobFailures is how many Cilium Helm Jobs failed in a row for the values whose hash is jobFailuresHash, counted when the controller removes a failed Job to try again. Each failure doubles the wait before the next attempt (a minute, up to 30 minutes), so a Job that keeps failing does not hold the cluster operation lock in a near-continuous loop. Other values (another hash) start again at once, and a successful Job clears it. Written by the controller only. |
| `status.cni.jobFailuresHash` | `string` | no | jobFailuresHash is the effective-values hash jobFailures counts for. |
| `status.cni.kubeProxy` | `string` | no | kubeProxy is the mode the Talos side (cluster.proxy.disabled) is rendered for. Unset until a kube-proxy switch first runs, which means "render whatever the patches say"; once set, pkg/render appends it as the last patch, so it wins over any patch. Only the TalosOperation of kind cni writes it, and only after the new side is verified. Allowed values: `enabled`, `replaced`. |
| `status.cni.takenOverInlineManifest` | `string` | no | takenOverInlineManifest is the name of the cluster.inlineManifests entry of the adopted machines that held the Cilium DaemonSet Ichor took over, which pkg/render stops rendering once CNIHandover is True. The controller copies it from the TalosClusterCNIInlineManifestAnnotation when the hand-over completes; empty when the adopted Cilium was not in an inline manifest. |
| `status.conditions` | `[]object` | no | conditions represent the current state of the TalosCluster resource, kstatus-compatible. |
| `status.conditions[].lastTransitionTime` | `string (date-time)` | yes | lastTransitionTime is the last time the condition transitioned from one status to another. This should be when the underlying condition changed. If that is not known, then using the time when the API field changed is acceptable. |
| `status.conditions[].message` | `string` | yes | message is a human readable message indicating details about the transition. This may be an empty string. |
| `status.conditions[].observedGeneration` | `integer` | no | observedGeneration represents the .metadata.generation that the condition was set based upon. For instance, if .metadata.generation is currently 12, but the .status.conditions[x].observedGeneration is 9, the condition is out of date with respect to the current state of the instance. |
| `status.conditions[].reason` | `string` | yes | reason contains a programmatic identifier indicating the reason for the condition's last transition. Producers of specific condition types may define expected values and meanings for this field, and whether the values are considered a guaranteed API. The value should be a CamelCase string. This field may not be empty. |
| `status.conditions[].status` | `string` | yes | status of the condition, one of True, False, Unknown. Allowed values: `True`, `False`, `Unknown`. |
| `status.conditions[].type` | `string` | yes | type of condition in CamelCase or in foo.example.com/CamelCase. |
| `status.observedGeneration` | `integer` | no | observedGeneration is the most recent generation the controller has reconciled. |

