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
| `spec.documents[].config.cluster` | `object` | no | Talos: Config.cluster — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.acceptedCAs` | `[]object` | no | Talos: ClusterConfig.acceptedCAs — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.acceptedCAs[].crt` | `string (byte)` | yes | Talos: PEMEncodedCertificate.crt — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.adminKubeconfig` | `object` | no | Talos: ClusterConfig.adminKubeconfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.adminKubeconfig.certLifetime` | `string` | no | Talos: AdminKubeconfigConfig.certLifetime — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.allowSchedulingOnControlPlanes` | `boolean` | no | Talos: ClusterConfig.allowSchedulingOnControlPlanes — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.allowSchedulingOnMasters` | `boolean` | no | Talos: ClusterConfig.allowSchedulingOnMasters — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.apiServer` | `object` | no | Talos: ClusterConfig.apiServer — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.apiServer.admissionControl` | `[]object` | no | Talos: APIServerConfig.admissionControl — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.apiServer.admissionControl[].name` | `string` | yes | Talos: AdmissionPluginConfig.name — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.apiServer.auditPolicy` | `object` | no | Talos: APIServerConfig.auditPolicy — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
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
| `spec.documents[].config.cluster.apiServer.authorizationConfig` | `[]object` | no | Talos: APIServerConfig.authorizationConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.apiServer.authorizationConfig[].name` | `string` | yes | Talos: AuthorizationConfigAuthorizerConfig.name — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.apiServer.authorizationConfig[].type` | `string` | yes | Talos: AuthorizationConfigAuthorizerConfig.type — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.apiServer.authorizationConfig[].webhook` | `object` | no | Talos: AuthorizationConfigAuthorizerConfig.webhook — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
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
| `spec.documents[].config.cluster.apiServer.certSANs` | `[]string` | no | Talos: APIServerConfig.certSANs — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.apiServer.disablePodSecurityPolicy` | `boolean` | no | Talos: APIServerConfig.disablePodSecurityPolicy — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.apiServer.env` | `map[string]string` | no | Talos: APIServerConfig.env — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.apiServer.extraArgs` | `map[string][]string` | no | Talos: APIServerConfig.extraArgs — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.apiServer.extraVolumes` | `[]object` | no | Talos: APIServerConfig.extraVolumes — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.apiServer.extraVolumes[].hostPath` | `string` | yes | Talos: VolumeMountConfig.hostPath — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.apiServer.extraVolumes[].mountPath` | `string` | yes | Talos: VolumeMountConfig.mountPath — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.apiServer.extraVolumes[].readonly` | `boolean` | no | Talos: VolumeMountConfig.readonly — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.apiServer.image` | `string` | no | Talos: APIServerConfig.image — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.apiServer.resources` | `object` | no | Talos: APIServerConfig.resources — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.apiServer.resources.limits` | `map[string]integer or string` | no | Talos: ResourcesConfig.limits — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.apiServer.resources.requests` | `map[string]integer or string` | no | Talos: ResourcesConfig.requests — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.clusterName` | `string` | no | Talos: ClusterConfig.clusterName — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.controlPlane` | `object` | no | Talos: ClusterConfig.controlPlane — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.controlPlane.endpoint` | `string` | yes | Talos: ControlPlaneConfig.endpoint — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.controlPlane.localAPIServerPort` | `integer` | no | Talos: ControlPlaneConfig.localAPIServerPort — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.controllerManager` | `object` | no | Talos: ClusterConfig.controllerManager — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.controllerManager.env` | `map[string]string` | no | Talos: ControllerManagerConfig.env — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.controllerManager.extraArgs` | `map[string][]string` | no | Talos: ControllerManagerConfig.extraArgs — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.controllerManager.extraVolumes` | `[]object` | no | Talos: ControllerManagerConfig.extraVolumes — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.controllerManager.extraVolumes[].hostPath` | `string` | yes | Talos: VolumeMountConfig.hostPath — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.controllerManager.extraVolumes[].mountPath` | `string` | yes | Talos: VolumeMountConfig.mountPath — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.controllerManager.extraVolumes[].readonly` | `boolean` | no | Talos: VolumeMountConfig.readonly — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.controllerManager.image` | `string` | no | Talos: ControllerManagerConfig.image — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.controllerManager.resources` | `object` | no | Talos: ControllerManagerConfig.resources — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.controllerManager.resources.limits` | `map[string]integer or string` | no | Talos: ResourcesConfig.limits — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.controllerManager.resources.requests` | `map[string]integer or string` | no | Talos: ResourcesConfig.requests — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.coreDNS` | `object` | no | Talos: ClusterConfig.coreDNS — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.coreDNS.disabled` | `boolean` | no | Talos: CoreDNS.disabled — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.coreDNS.image` | `string` | no | Talos: CoreDNS.image — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.discovery` | `object` | no | Talos: ClusterConfig.discovery — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.discovery.enabled` | `boolean` | no | Talos: ClusterDiscoveryConfig.enabled — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.discovery.registries` | `object` | yes | Talos: ClusterDiscoveryConfig.registries — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.discovery.registries.kubernetes` | `object` | yes | Talos: DiscoveryRegistriesConfig.kubernetes — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.discovery.registries.kubernetes.disabled` | `boolean` | no | Talos: RegistryKubernetesConfig.disabled — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.discovery.registries.service` | `object` | yes | Talos: DiscoveryRegistriesConfig.service — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.discovery.registries.service.disabled` | `boolean` | no | Talos: RegistryServiceConfig.disabled — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.discovery.registries.service.endpoint` | `string` | no | Talos: RegistryServiceConfig.endpoint — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.etcd` | `object` | no | Talos: ClusterConfig.etcd — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.etcd.advertisedSubnets` | `[]string` | no | Talos: EtcdConfig.advertisedSubnets — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.etcd.extraArgs` | `map[string][]string` | no | Talos: EtcdConfig.extraArgs — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.etcd.image` | `string` | no | Talos: EtcdConfig.image — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.etcd.listenSubnets` | `[]string` | no | Talos: EtcdConfig.listenSubnets — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.etcd.subnet` | `string` | no | Talos: EtcdConfig.subnet — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.externalCloudProvider` | `object` | no | Talos: ClusterConfig.externalCloudProvider — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.externalCloudProvider.enabled` | `boolean` | no | Talos: ExternalCloudProviderConfig.enabled — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.externalCloudProvider.manifests` | `[]string` | no | Talos: ExternalCloudProviderConfig.manifests — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.extraManifestHeaders` | `map[string]string` | no | Talos: ClusterConfig.extraManifestHeaders — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.extraManifests` | `[]string` | no | Talos: ClusterConfig.extraManifests — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.id` | `string` | no | Talos: ClusterConfig.id — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.inlineManifests` | `[]object` | no | Talos: ClusterConfig.inlineManifests — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.inlineManifests[].contents` | `string` | yes | Talos: ClusterInlineManifest.contents — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.inlineManifests[].name` | `string` | yes | Talos: ClusterInlineManifest.name — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.network` | `object` | no | Talos: ClusterConfig.network — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.network.cni` | `object` | no | Talos: ClusterNetworkConfig.cni — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.network.cni.flannel` | `object` | no | Talos: CNIConfig.flannel — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.network.cni.flannel.extraArgs` | `[]string` | no | Talos: FlannelCNIConfig.extraArgs — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.network.cni.flannel.kubeNetworkPoliciesEnabled` | `boolean` | no | Talos: FlannelCNIConfig.kubeNetworkPoliciesEnabled — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.network.cni.name` | `string` | no | Talos: CNIConfig.name — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.network.cni.urls` | `[]string` | no | Talos: CNIConfig.urls — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.network.dnsDomain` | `string` | yes | Talos: ClusterNetworkConfig.dnsDomain — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.network.podSubnets` | `[]string` | yes | Talos: ClusterNetworkConfig.podSubnets — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.network.serviceSubnets` | `[]string` | yes | Talos: ClusterNetworkConfig.serviceSubnets — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.proxy` | `object` | no | Talos: ClusterConfig.proxy — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.proxy.disabled` | `boolean` | no | Talos: ProxyConfig.disabled — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.proxy.extraArgs` | `map[string][]string` | no | Talos: ProxyConfig.extraArgs — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.proxy.image` | `string` | no | Talos: ProxyConfig.image — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.proxy.mode` | `string` | no | Talos: ProxyConfig.mode — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.scheduler` | `object` | no | Talos: ClusterConfig.scheduler — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.scheduler.env` | `map[string]string` | no | Talos: SchedulerConfig.env — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.scheduler.extraArgs` | `map[string][]string` | no | Talos: SchedulerConfig.extraArgs — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.scheduler.extraVolumes` | `[]object` | no | Talos: SchedulerConfig.extraVolumes — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.scheduler.extraVolumes[].hostPath` | `string` | yes | Talos: VolumeMountConfig.hostPath — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.scheduler.extraVolumes[].mountPath` | `string` | yes | Talos: VolumeMountConfig.mountPath — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.scheduler.extraVolumes[].readonly` | `boolean` | no | Talos: VolumeMountConfig.readonly — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.scheduler.image` | `string` | no | Talos: SchedulerConfig.image — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.scheduler.resources` | `object` | no | Talos: SchedulerConfig.resources — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.scheduler.resources.limits` | `map[string]integer or string` | no | Talos: ResourcesConfig.limits — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.cluster.scheduler.resources.requests` | `map[string]integer or string` | no | Talos: ResourcesConfig.requests — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.debug` | `boolean` | no | Talos: Config.debug — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine` | `object` | no | Talos: Config.machine — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.acceptedCAs` | `[]object` | no | Talos: MachineConfig.acceptedCAs — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.acceptedCAs[].crt` | `string (byte)` | yes | Talos: PEMEncodedCertificate.crt — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.certSANs` | `[]string` | no | Talos: MachineConfig.certSANs — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.controlPlane` | `object` | no | Talos: MachineConfig.controlPlane — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.controlPlane.controllerManager` | `object` | no | Talos: MachineControlPlaneConfig.controllerManager — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.controlPlane.controllerManager.disabled` | `boolean` | no | Talos: MachineControllerManagerConfig.disabled — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.controlPlane.scheduler` | `object` | no | Talos: MachineControlPlaneConfig.scheduler — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.controlPlane.scheduler.disabled` | `boolean` | no | Talos: MachineSchedulerConfig.disabled — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.disks` | `[]object` | no | Talos: MachineConfig.disks — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.disks[].device` | `string` | no | Talos: MachineDisk.device — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.disks[].partitions` | `[]object` | no | Talos: MachineDisk.partitions — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.disks[].partitions[].mountpoint` | `string` | no | Talos: DiskPartition.mountpoint — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.disks[].partitions[].size` | `string` | no | Talos: DiskPartition.size — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.env` | `map[string]string` | no | Talos: MachineConfig.env — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.features` | `object` | no | Talos: MachineConfig.features — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.features.apidCheckExtKeyUsage` | `boolean` | no | Talos: FeaturesConfig.apidCheckExtKeyUsage — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.features.diskQuotaSupport` | `boolean` | no | Talos: FeaturesConfig.diskQuotaSupport — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.features.hostDNS` | `object` | no | Talos: FeaturesConfig.hostDNS — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.features.hostDNS.enabled` | `boolean` | no | Talos: HostDNSConfig.enabled — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.features.hostDNS.forwardKubeDNSToHost` | `boolean` | no | Talos: HostDNSConfig.forwardKubeDNSToHost — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.features.hostDNS.resolveMemberNames` | `boolean` | no | Talos: HostDNSConfig.resolveMemberNames — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.features.imageCache` | `object` | no | Talos: FeaturesConfig.imageCache — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.features.imageCache.localEnabled` | `boolean` | no | Talos: ImageCacheConfig.localEnabled — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.features.kubePrism` | `object` | no | Talos: FeaturesConfig.kubePrism — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.features.kubePrism.enabled` | `boolean` | no | Talos: KubePrism.enabled — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.features.kubePrism.port` | `integer` | no | Talos: KubePrism.port — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.features.kubernetesTalosAPIAccess` | `object` | no | Talos: FeaturesConfig.kubernetesTalosAPIAccess — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.features.kubernetesTalosAPIAccess.allowedKubernetesNamespaces` | `[]string` | no | Talos: KubernetesTalosAPIAccessConfig.allowedKubernetesNamespaces — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.features.kubernetesTalosAPIAccess.allowedRoles` | `[]string` | no | Talos: KubernetesTalosAPIAccessConfig.allowedRoles — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.features.kubernetesTalosAPIAccess.enabled` | `boolean` | no | Talos: KubernetesTalosAPIAccessConfig.enabled — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.features.nodeAddressSortAlgorithm` | `string` | no | Talos: FeaturesConfig.nodeAddressSortAlgorithm — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.features.rbac` | `boolean` | no | Talos: FeaturesConfig.rbac — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.features.stableHostname` | `boolean` | no | Talos: FeaturesConfig.stableHostname — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.files` | `[]object` | no | Talos: MachineConfig.files — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.files[].content` | `string` | yes | Talos: MachineFile.content — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.files[].op` | `string` | yes | Talos: MachineFile.op — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.files[].path` | `string` | yes | Talos: MachineFile.path — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.files[].permissions` | `string` | yes | Talos: MachineFile.permissions — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.install` | `object` | no | Talos: MachineConfig.install — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.install.bootloader` | `boolean` | no | Talos: InstallConfig.bootloader — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.install.disk` | `string` | no | Talos: InstallConfig.disk — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.install.diskSelector` | `object` | no | Talos: InstallConfig.diskSelector — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.install.diskSelector.busPath` | `string` | no | Talos: InstallDiskSelector.busPath — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.install.diskSelector.modalias` | `string` | no | Talos: InstallDiskSelector.modalias — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.install.diskSelector.model` | `string` | no | Talos: InstallDiskSelector.model — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.install.diskSelector.name` | `string` | no | Talos: InstallDiskSelector.name — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.install.diskSelector.serial` | `string` | no | Talos: InstallDiskSelector.serial — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.install.diskSelector.size` | `string` | no | Talos: InstallDiskSelector.size — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.install.diskSelector.type` | `string` | no | Talos: InstallDiskSelector.type — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.install.diskSelector.uuid` | `string` | no | Talos: InstallDiskSelector.uuid — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.install.diskSelector.wwid` | `string` | no | Talos: InstallDiskSelector.wwid — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.install.extensions` | `[]object` | no | Talos: InstallConfig.extensions — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.install.extensions[].image` | `string` | yes | Talos: InstallExtensionConfig.image — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.install.extraKernelArgs` | `[]string` | no | Talos: InstallConfig.extraKernelArgs — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.install.grubUseUKICmdline` | `boolean` | no | Talos: InstallConfig.grubUseUKICmdline — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.install.image` | `string` | no | Talos: InstallConfig.image — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.install.legacyBIOSSupport` | `boolean` | no | Talos: InstallConfig.legacyBIOSSupport — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.install.wipe` | `boolean` | yes | Talos: InstallConfig.wipe — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.kernel` | `object` | no | Talos: MachineConfig.kernel — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.kernel.modules` | `[]object` | no | Talos: KernelConfig.modules — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.kernel.modules[].name` | `string` | yes | Talos: KernelModuleConfig.name — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.kernel.modules[].parameters` | `[]string` | no | Talos: KernelModuleConfig.parameters — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.kubelet` | `object` | no | Talos: MachineConfig.kubelet — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.kubelet.clusterDNS` | `[]string` | no | Talos: KubeletConfig.clusterDNS — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.kubelet.credentialProviderConfig` | `object` | no | Talos: KubeletConfig.credentialProviderConfig — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
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
| `spec.documents[].config.machine.kubelet.defaultRuntimeSeccompProfileEnabled` | `boolean` | no | Talos: KubeletConfig.defaultRuntimeSeccompProfileEnabled — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.kubelet.disableManifestsDirectory` | `boolean` | no | Talos: KubeletConfig.disableManifestsDirectory — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.kubelet.extraArgs` | `map[string][]string` | no | Talos: KubeletConfig.extraArgs — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.kubelet.extraMounts` | `[]object` | no | Talos: KubeletConfig.extraMounts — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.kubelet.extraMounts[].destination` | `string` | yes | Talos: ExtraMount.destination — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.kubelet.extraMounts[].gidMappings` | `[]object` | no | Talos: ExtraMount.gidMappings — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.kubelet.extraMounts[].gidMappings[].containerID` | `integer` | yes | Talos: LinuxIDMapping.containerID — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.kubelet.extraMounts[].gidMappings[].hostID` | `integer` | yes | Talos: LinuxIDMapping.hostID — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.kubelet.extraMounts[].gidMappings[].size` | `integer` | yes | Talos: LinuxIDMapping.size — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.kubelet.extraMounts[].options` | `[]string` | no | Talos: ExtraMount.options — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.kubelet.extraMounts[].source` | `string` | no | Talos: ExtraMount.source — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.kubelet.extraMounts[].type` | `string` | no | Talos: ExtraMount.type — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.kubelet.extraMounts[].uidMappings` | `[]object` | no | Talos: ExtraMount.uidMappings — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.kubelet.extraMounts[].uidMappings[].containerID` | `integer` | yes | Talos: LinuxIDMapping.containerID — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.kubelet.extraMounts[].uidMappings[].hostID` | `integer` | yes | Talos: LinuxIDMapping.hostID — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.kubelet.extraMounts[].uidMappings[].size` | `integer` | yes | Talos: LinuxIDMapping.size — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.kubelet.image` | `string` | no | Talos: KubeletConfig.image — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.kubelet.nodeIP` | `object` | no | Talos: KubeletConfig.nodeIP — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.kubelet.nodeIP.validSubnets` | `[]string` | no | Talos: KubeletNodeIPConfig.validSubnets — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.kubelet.registerWithFQDN` | `boolean` | no | Talos: KubeletConfig.registerWithFQDN — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.kubelet.skipNodeRegistration` | `boolean` | no | Talos: KubeletConfig.skipNodeRegistration — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.logging` | `object` | no | Talos: MachineConfig.logging — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.logging.destinations` | `[]object` | yes | Talos: LoggingConfig.destinations — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.logging.destinations[].endpoint` | `string` | yes | Talos: LoggingDestination.endpoint — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.logging.destinations[].extraTags` | `map[string]string` | no | Talos: LoggingDestination.extraTags — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.logging.destinations[].format` | `string` | yes | Talos: LoggingDestination.format — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network` | `object` | no | Talos: MachineConfig.network — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.disableSearchDomain` | `boolean` | no | Talos: NetworkConfig.disableSearchDomain — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.extraHostEntries` | `[]object` | no | Talos: NetworkConfig.extraHostEntries — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.extraHostEntries[].aliases` | `[]string` | yes | Talos: ExtraHost.aliases — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.extraHostEntries[].ip` | `string` | yes | Talos: ExtraHost.ip — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.hostname` | `string` | no | Talos: NetworkConfig.hostname — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces` | `[]object` | no | Talos: NetworkConfig.interfaces — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].addresses` | `[]string` | no | Talos: Device.addresses — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bond` | `object` | no | Talos: Device.bond — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bond.adActorSysPrio` | `integer` | no | Talos: Bond.adActorSysPrio — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bond.adActorSystem` | `string` | no | Talos: Bond.adActorSystem — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bond.adSelect` | `string` | no | Talos: Bond.adSelect — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bond.adUserPortKey` | `integer` | no | Talos: Bond.adUserPortKey — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bond.allSlavesActive` | `integer` | no | Talos: Bond.allSlavesActive — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bond.arpAllTargets` | `string` | no | Talos: Bond.arpAllTargets — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bond.arpIPTarget` | `[]string` | no | Talos: Bond.arpIPTarget — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bond.arpInterval` | `integer` | no | Talos: Bond.arpInterval — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bond.arpValidate` | `string` | no | Talos: Bond.arpValidate — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bond.deviceSelectors` | `[]object` | no | Talos: Bond.deviceSelectors — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bond.deviceSelectors[].busPath` | `string` | no | Talos: NetworkDeviceSelector.busPath — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bond.deviceSelectors[].driver` | `string` | no | Talos: NetworkDeviceSelector.driver — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bond.deviceSelectors[].hardwareAddr` | `string` | no | Talos: NetworkDeviceSelector.hardwareAddr — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bond.deviceSelectors[].pciID` | `string` | no | Talos: NetworkDeviceSelector.pciID — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bond.deviceSelectors[].permanentAddr` | `string` | no | Talos: NetworkDeviceSelector.permanentAddr — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bond.deviceSelectors[].physical` | `boolean` | no | Talos: NetworkDeviceSelector.physical — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bond.downdelay` | `integer` | no | Talos: Bond.downdelay — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bond.failOverMac` | `string` | no | Talos: Bond.failOverMac — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bond.interfaces` | `[]string` | yes | Talos: Bond.interfaces — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bond.lacpRate` | `string` | no | Talos: Bond.lacpRate — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bond.lpInterval` | `integer` | no | Talos: Bond.lpInterval — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bond.miimon` | `integer` | no | Talos: Bond.miimon — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bond.minLinks` | `integer` | no | Talos: Bond.minLinks — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bond.mode` | `string` | yes | Talos: Bond.mode — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bond.numPeerNotif` | `integer` | no | Talos: Bond.numPeerNotif — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bond.packetsPerSlave` | `integer` | no | Talos: Bond.packetsPerSlave — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bond.peerNotifyDelay` | `integer` | no | Talos: Bond.peerNotifyDelay — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bond.primary` | `string` | no | Talos: Bond.primary — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bond.primaryReselect` | `string` | no | Talos: Bond.primaryReselect — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bond.resendIgmp` | `integer` | no | Talos: Bond.resendIgmp — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bond.tlbDynamicLb` | `integer` | no | Talos: Bond.tlbDynamicLb — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bond.updelay` | `integer` | no | Talos: Bond.updelay — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bond.useCarrier` | `boolean` | no | Talos: Bond.useCarrier — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bond.xmitHashPolicy` | `string` | no | Talos: Bond.xmitHashPolicy — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bridge` | `object` | no | Talos: Device.bridge — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bridge.interfaces` | `[]string` | yes | Talos: Bridge.interfaces — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bridge.stp` | `object` | no | Talos: Bridge.stp — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bridge.stp.enabled` | `boolean` | no | Talos: STP.enabled — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bridge.vlan` | `object` | no | Talos: Bridge.vlan — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bridge.vlan.vlanFiltering` | `boolean` | no | Talos: BridgeVLAN.vlanFiltering — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bridgePort` | `object` | no | Talos: Device.bridgePort — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].bridgePort.master` | `string` | no | Talos: BridgePort.master — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].cidr` | `string` | no | Talos: Device.cidr — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].deviceSelector` | `object` | no | Talos: Device.deviceSelector — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].deviceSelector.busPath` | `string` | no | Talos: NetworkDeviceSelector.busPath — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].deviceSelector.driver` | `string` | no | Talos: NetworkDeviceSelector.driver — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].deviceSelector.hardwareAddr` | `string` | no | Talos: NetworkDeviceSelector.hardwareAddr — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].deviceSelector.pciID` | `string` | no | Talos: NetworkDeviceSelector.pciID — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].deviceSelector.permanentAddr` | `string` | no | Talos: NetworkDeviceSelector.permanentAddr — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].deviceSelector.physical` | `boolean` | no | Talos: NetworkDeviceSelector.physical — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].dhcp` | `boolean` | no | Talos: Device.dhcp — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].dhcpOptions` | `object` | no | Talos: Device.dhcpOptions — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].dhcpOptions.duidv6` | `string` | no | Talos: DHCPOptions.duidv6 — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].dhcpOptions.ipv4` | `boolean` | no | Talos: DHCPOptions.ipv4 — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].dhcpOptions.ipv6` | `boolean` | no | Talos: DHCPOptions.ipv6 — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].dhcpOptions.routeMetric` | `integer` | yes | Talos: DHCPOptions.routeMetric — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].dummy` | `boolean` | no | Talos: Device.dummy — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].ignore` | `boolean` | no | Talos: Device.ignore — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].interface` | `string` | no | Talos: Device.interface — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].mtu` | `integer` | no | Talos: Device.mtu — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].routes` | `[]object` | no | Talos: Device.routes — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].routes[].gateway` | `string` | yes | Talos: Route.gateway — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].routes[].metric` | `integer` | no | Talos: Route.metric — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].routes[].mtu` | `integer` | no | Talos: Route.mtu — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].routes[].network` | `string` | yes | Talos: Route.network — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].routes[].source` | `string` | no | Talos: Route.source — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].vip` | `object` | no | Talos: Device.vip — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].vip.equinixMetal` | `object` | no | Talos: DeviceVIPConfig.equinixMetal — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].vip.hcloud` | `object` | no | Talos: DeviceVIPConfig.hcloud — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].vip.ip` | `string` | no | Talos: DeviceVIPConfig.ip — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].vlans` | `[]object` | no | Talos: Device.vlans — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].vlans[].addresses` | `[]string` | no | Talos: Vlan.addresses — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].vlans[].cidr` | `string` | no | Talos: Vlan.cidr — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].vlans[].dhcp` | `boolean` | no | Talos: Vlan.dhcp — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].vlans[].dhcpOptions` | `object` | no | Talos: Vlan.dhcpOptions — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].vlans[].dhcpOptions.duidv6` | `string` | no | Talos: DHCPOptions.duidv6 — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].vlans[].dhcpOptions.ipv4` | `boolean` | no | Talos: DHCPOptions.ipv4 — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].vlans[].dhcpOptions.ipv6` | `boolean` | no | Talos: DHCPOptions.ipv6 — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].vlans[].dhcpOptions.routeMetric` | `integer` | yes | Talos: DHCPOptions.routeMetric — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].vlans[].mtu` | `integer` | no | Talos: Vlan.mtu — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].vlans[].routes` | `[]object` | yes | Talos: Vlan.routes — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].vlans[].routes[].gateway` | `string` | yes | Talos: Route.gateway — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].vlans[].routes[].metric` | `integer` | no | Talos: Route.metric — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].vlans[].routes[].mtu` | `integer` | no | Talos: Route.mtu — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].vlans[].routes[].network` | `string` | yes | Talos: Route.network — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].vlans[].routes[].source` | `string` | no | Talos: Route.source — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].vlans[].vip` | `object` | no | Talos: Vlan.vip — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].vlans[].vip.equinixMetal` | `object` | no | Talos: DeviceVIPConfig.equinixMetal — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].vlans[].vip.hcloud` | `object` | no | Talos: DeviceVIPConfig.hcloud — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].vlans[].vip.ip` | `string` | no | Talos: DeviceVIPConfig.ip — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].vlans[].vlanId` | `integer` | yes | Talos: Vlan.vlanId — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].wireguard` | `object` | no | Talos: Device.wireguard — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].wireguard.firewallMark` | `integer` | no | Talos: DeviceWireguardConfig.firewallMark — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].wireguard.listenPort` | `integer` | no | Talos: DeviceWireguardConfig.listenPort — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].wireguard.peers` | `[]object` | no | Talos: DeviceWireguardConfig.peers — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].wireguard.peers[].allowedIPs` | `[]string` | no | Talos: DeviceWireguardPeer.allowedIPs — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].wireguard.peers[].endpoint` | `string` | no | Talos: DeviceWireguardPeer.endpoint — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].wireguard.peers[].persistentKeepaliveInterval` | `string` | no | Talos: DeviceWireguardPeer.persistentKeepaliveInterval — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.interfaces[].wireguard.peers[].publicKey` | `string` | no | Talos: DeviceWireguardPeer.publicKey — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.kubespan` | `object` | no | Talos: NetworkConfig.kubespan — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.kubespan.advertiseKubernetesNetworks` | `boolean` | no | Talos: NetworkKubeSpan.advertiseKubernetesNetworks — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.kubespan.allowDownPeerBypass` | `boolean` | no | Talos: NetworkKubeSpan.allowDownPeerBypass — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.kubespan.enabled` | `boolean` | no | Talos: NetworkKubeSpan.enabled — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.kubespan.filters` | `object` | no | Talos: NetworkKubeSpan.filters — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.kubespan.filters.endpoints` | `[]string` | no | Talos: KubeSpanFilters.endpoints — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.kubespan.filters.excludeAdvertisedNetworks` | `[]string` | no | Talos: KubeSpanFilters.excludeAdvertisedNetworks — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.kubespan.harvestExtraEndpoints` | `boolean` | no | Talos: NetworkKubeSpan.harvestExtraEndpoints — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.kubespan.mtu` | `integer` | no | Talos: NetworkKubeSpan.mtu — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.nameservers` | `[]string` | no | Talos: NetworkConfig.nameservers — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.network.searchDomains` | `[]string` | no | Talos: NetworkConfig.searchDomains — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.nodeAnnotations` | `map[string]string` | no | Talos: MachineConfig.nodeAnnotations — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.nodeLabels` | `map[string]string` | no | Talos: MachineConfig.nodeLabels — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.nodeTaints` | `map[string]string` | no | Talos: MachineConfig.nodeTaints — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.registries` | `object` | no | Talos: MachineConfig.registries — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.registries.config` | `map[string]object` | no | Talos: RegistriesConfig.config — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.registries.config.<key>.auth` | `object` | no | Talos: RegistryConfig.auth — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.registries.config.<key>.auth.username` | `string` | no | Talos: RegistryAuthConfig.username — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.registries.config.<key>.tls` | `object` | no | Talos: RegistryConfig.tls — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.registries.config.<key>.tls.ca` | `string (byte)` | no | Talos: RegistryTLSConfig.ca — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.registries.config.<key>.tls.clientIdentity` | `object` | no | Talos: RegistryTLSConfig.clientIdentity — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.registries.config.<key>.tls.clientIdentity.crt` | `string (byte)` | yes | Talos: PEMEncodedCertificateAndKey.crt — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.registries.config.<key>.tls.clientIdentity.key` | `string (byte)` | yes | Talos: PEMEncodedCertificateAndKey.key — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.registries.config.<key>.tls.insecureSkipVerify` | `boolean` | no | Talos: RegistryTLSConfig.insecureSkipVerify — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.registries.mirrors` | `map[string]object` | no | Talos: RegistriesConfig.mirrors — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.registries.mirrors.<key>.endpoints` | `[]string` | yes | Talos: RegistryMirrorConfig.endpoints — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.registries.mirrors.<key>.overridePath` | `boolean` | no | Talos: RegistryMirrorConfig.overridePath — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.registries.mirrors.<key>.skipFallback` | `boolean` | no | Talos: RegistryMirrorConfig.skipFallback — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.seccompProfiles` | `[]object` | no | Talos: MachineConfig.seccompProfiles — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.seccompProfiles[].name` | `string` | yes | Talos: MachineSeccompProfile.name — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.seccompProfiles[].value` | `object` | yes | Talos: MachineSeccompProfile.value — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
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
| `spec.documents[].config.machine.sysctls` | `map[string]string` | no | Talos: MachineConfig.sysctls — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.sysfs` | `map[string]string` | no | Talos: MachineConfig.sysfs — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.systemDiskEncryption` | `object` | no | Talos: MachineConfig.systemDiskEncryption — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.systemDiskEncryption.ephemeral` | `object` | no | Talos: SystemDiskEncryptionConfig.ephemeral — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.systemDiskEncryption.ephemeral.blockSize` | `integer` | no | Talos: EncryptionConfig.blockSize — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.systemDiskEncryption.ephemeral.cipher` | `string` | no | Talos: EncryptionConfig.cipher — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.systemDiskEncryption.ephemeral.keySize` | `integer` | no | Talos: EncryptionConfig.keySize — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.systemDiskEncryption.ephemeral.keys` | `[]object` | yes | Talos: EncryptionConfig.keys — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.systemDiskEncryption.ephemeral.keys[].kms` | `object` | no | Talos: EncryptionKey.kms — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.systemDiskEncryption.ephemeral.keys[].kms.endpoint` | `string` | yes | Talos: EncryptionKeyKMS.endpoint — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.systemDiskEncryption.ephemeral.keys[].nodeID` | `object` | no | Talos: EncryptionKey.nodeID — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.systemDiskEncryption.ephemeral.keys[].slot` | `integer` | yes | Talos: EncryptionKey.slot — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.systemDiskEncryption.ephemeral.keys[].tpm` | `object` | no | Talos: EncryptionKey.tpm — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.systemDiskEncryption.ephemeral.keys[].tpm.checkSecurebootStatusOnEnroll` | `boolean` | no | Talos: EncryptionKeyTPM.checkSecurebootStatusOnEnroll — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.systemDiskEncryption.ephemeral.options` | `[]string` | no | Talos: EncryptionConfig.options — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.systemDiskEncryption.ephemeral.provider` | `string` | yes | Talos: EncryptionConfig.provider — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.systemDiskEncryption.state` | `object` | no | Talos: SystemDiskEncryptionConfig.state — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.systemDiskEncryption.state.blockSize` | `integer` | no | Talos: EncryptionConfig.blockSize — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.systemDiskEncryption.state.cipher` | `string` | no | Talos: EncryptionConfig.cipher — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.systemDiskEncryption.state.keySize` | `integer` | no | Talos: EncryptionConfig.keySize — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.systemDiskEncryption.state.keys` | `[]object` | yes | Talos: EncryptionConfig.keys — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.systemDiskEncryption.state.keys[].kms` | `object` | no | Talos: EncryptionKey.kms — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.systemDiskEncryption.state.keys[].kms.endpoint` | `string` | yes | Talos: EncryptionKeyKMS.endpoint — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.systemDiskEncryption.state.keys[].nodeID` | `object` | no | Talos: EncryptionKey.nodeID — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.systemDiskEncryption.state.keys[].slot` | `integer` | yes | Talos: EncryptionKey.slot — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.systemDiskEncryption.state.keys[].tpm` | `object` | no | Talos: EncryptionKey.tpm — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.systemDiskEncryption.state.keys[].tpm.checkSecurebootStatusOnEnroll` | `boolean` | no | Talos: EncryptionKeyTPM.checkSecurebootStatusOnEnroll — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.systemDiskEncryption.state.options` | `[]string` | no | Talos: EncryptionConfig.options — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.systemDiskEncryption.state.provider` | `string` | yes | Talos: EncryptionConfig.provider — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.time` | `object` | no | Talos: MachineConfig.time — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.time.bootTimeout` | `string` | no | Talos: TimeConfig.bootTimeout — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.time.disabled` | `boolean` | no | Talos: TimeConfig.disabled — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.time.servers` | `[]string` | no | Talos: TimeConfig.servers — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.type` | `string` | no | Talos: MachineConfig.type — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.udev` | `object` | no | Talos: MachineConfig.udev — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.machine.udev.rules` | `[]string` | no | Talos: UdevConfig.rules — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.persist` | `boolean` | no | Talos: Config.persist — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].config.version` | `string` | yes | Talos: Config.version — https://docs.siderolabs.com/talos/v1.14/reference/configuration/v1alpha1/config |
| `spec.documents[].kernelModuleConfig` | `object` | no | kernelModuleConfig is a Talos KernelModuleConfig document; populated when kind is "KernelModuleConfig". |
| `spec.documents[].kernelModuleConfig.name` | `string` | yes | Talos: KernelModuleConfigV1Alpha1.name — https://docs.siderolabs.com/talos/v1.14/reference/configuration/runtime/kernelmoduleconfig |
| `spec.documents[].kernelModuleConfig.parameters` | `[]string` | no | Talos: KernelModuleConfigV1Alpha1.parameters — https://docs.siderolabs.com/talos/v1.14/reference/configuration/runtime/kernelmoduleconfig |
| `spec.documents[].kind` | `string` | yes | kind selects which of the fields below is populated. Allowed values: `SysctlConfig`, `KernelModuleConfig`, `Config`, `VolumeConfig`, `UserVolumeConfig`, `RawVolumeConfig`, `ResolverConfig`. |
| `spec.documents[].rawVolumeConfig` | `object` | no | rawVolumeConfig is a Talos RawVolumeConfig document (an unformatted partition for a CSI provisioner); populated when kind is "RawVolumeConfig". |
| `spec.documents[].rawVolumeConfig.encryption` | `object` | no | Talos: RawVolumeConfigV1Alpha1.encryption — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/rawvolumeconfig |
| `spec.documents[].rawVolumeConfig.encryption.allowDiscards` | `boolean` | no | Talos: EncryptionSpec.allowDiscards |
| `spec.documents[].rawVolumeConfig.encryption.blockSize` | `integer` | no | Talos: EncryptionSpec.blockSize |
| `spec.documents[].rawVolumeConfig.encryption.cipher` | `string` | no | Talos: EncryptionSpec.cipher |
| `spec.documents[].rawVolumeConfig.encryption.keySize` | `integer` | no | Talos: EncryptionSpec.keySize |
| `spec.documents[].rawVolumeConfig.encryption.keys` | `[]object` | yes | Talos: EncryptionSpec.keys |
| `spec.documents[].rawVolumeConfig.encryption.keys[].kms` | `object` | no | Talos: EncryptionKey.kms |
| `spec.documents[].rawVolumeConfig.encryption.keys[].kms.endpoint` | `string` | yes | Talos: EncryptionKeyKMS.endpoint |
| `spec.documents[].rawVolumeConfig.encryption.keys[].lockToState` | `boolean` | no | Talos: EncryptionKey.lockToState |
| `spec.documents[].rawVolumeConfig.encryption.keys[].nodeID` | `object` | no | Talos: EncryptionKey.nodeID |
| `spec.documents[].rawVolumeConfig.encryption.keys[].slot` | `integer` | yes | Talos: EncryptionKey.slot |
| `spec.documents[].rawVolumeConfig.encryption.keys[].tpm` | `object` | no | Talos: EncryptionKey.tpm |
| `spec.documents[].rawVolumeConfig.encryption.keys[].tpm.checkSecurebootStatusOnEnroll` | `boolean` | no | Talos: EncryptionKeyTPM.checkSecurebootStatusOnEnroll |
| `spec.documents[].rawVolumeConfig.encryption.keys[].tpm.options` | `object` | no | Talos: EncryptionKeyTPM.options |
| `spec.documents[].rawVolumeConfig.encryption.keys[].tpm.options.pcrs` | `[]integer` | no | Talos: EncryptionKeyTPMOptions.pcrs |
| `spec.documents[].rawVolumeConfig.encryption.options` | `[]string` | no | Talos: EncryptionSpec.options |
| `spec.documents[].rawVolumeConfig.encryption.provider` | `string` | yes | Talos: EncryptionSpec.provider |
| `spec.documents[].rawVolumeConfig.name` | `string` | yes | Talos: RawVolumeConfigV1Alpha1.name — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/rawvolumeconfig |
| `spec.documents[].rawVolumeConfig.provisioning` | `object` | no | Talos: RawVolumeConfigV1Alpha1.provisioning — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/rawvolumeconfig |
| `spec.documents[].rawVolumeConfig.provisioning.diskSelector` | `object` | no | Talos: ProvisioningSpec.diskSelector |
| `spec.documents[].rawVolumeConfig.provisioning.diskSelector.match` | `string` | no | Talos: DiskSelector.match |
| `spec.documents[].rawVolumeConfig.provisioning.grow` | `boolean` | no | Talos: ProvisioningSpec.grow |
| `spec.documents[].rawVolumeConfig.provisioning.maxSize` | `string` | no | Talos: ProvisioningSpec.maxSize |
| `spec.documents[].rawVolumeConfig.provisioning.minSize` | `string` | no | Talos: ProvisioningSpec.minSize |
| `spec.documents[].resolverConfig` | `object` | no | resolverConfig is a Talos ResolverConfig document (DNS nameservers and search domains); populated when kind is "ResolverConfig". |
| `spec.documents[].resolverConfig.hostDNS` | `object` | no | Talos: ResolverConfigV1Alpha1.hostDNS — https://docs.siderolabs.com/talos/v1.14/reference/configuration/network/resolverconfig |
| `spec.documents[].resolverConfig.hostDNS.enabled` | `boolean` | no | Talos: HostDNSConfig.enabled — https://docs.siderolabs.com/talos/v1.14/reference/configuration/network/resolverconfig |
| `spec.documents[].resolverConfig.hostDNS.forwardKubeDNSToHost` | `boolean` | no | Talos: HostDNSConfig.forwardKubeDNSToHost — https://docs.siderolabs.com/talos/v1.14/reference/configuration/network/resolverconfig |
| `spec.documents[].resolverConfig.hostDNS.resolveMemberNames` | `boolean` | no | Talos: HostDNSConfig.resolveMemberNames — https://docs.siderolabs.com/talos/v1.14/reference/configuration/network/resolverconfig |
| `spec.documents[].resolverConfig.nameservers` | `[]object` | no | Talos: ResolverConfigV1Alpha1.nameservers — https://docs.siderolabs.com/talos/v1.14/reference/configuration/network/resolverconfig |
| `spec.documents[].resolverConfig.nameservers[].address` | `string` | yes | Talos: NameserverConfig.address — https://docs.siderolabs.com/talos/v1.14/reference/configuration/network/resolverconfig |
| `spec.documents[].resolverConfig.nameservers[].protocol` | `string` | no | Talos: NameserverConfig.protocol — https://docs.siderolabs.com/talos/v1.14/reference/configuration/network/resolverconfig |
| `spec.documents[].resolverConfig.nameservers[].tlsServerName` | `string` | no | Talos: NameserverConfig.tlsServerName — https://docs.siderolabs.com/talos/v1.14/reference/configuration/network/resolverconfig |
| `spec.documents[].resolverConfig.searchDomains` | `object` | no | Talos: ResolverConfigV1Alpha1.searchDomains — https://docs.siderolabs.com/talos/v1.14/reference/configuration/network/resolverconfig |
| `spec.documents[].resolverConfig.searchDomains.disableDefault` | `boolean` | no | Talos: SearchDomainsConfig.disableDefault — https://docs.siderolabs.com/talos/v1.14/reference/configuration/network/resolverconfig |
| `spec.documents[].resolverConfig.searchDomains.domains` | `[]string` | no | Talos: SearchDomainsConfig.domains — https://docs.siderolabs.com/talos/v1.14/reference/configuration/network/resolverconfig |
| `spec.documents[].sysctlConfig` | `object` | no | sysctlConfig is a Talos SysctlConfig document; populated when kind is "SysctlConfig". |
| `spec.documents[].sysctlConfig.params` | `map[string]string` | yes | Talos: SysctlConfigV1Alpha1.params — https://docs.siderolabs.com/talos/v1.14/reference/configuration/runtime/sysctlconfig |
| `spec.documents[].userVolumeConfig` | `object` | no | userVolumeConfig is a Talos UserVolumeConfig document; populated when kind is "UserVolumeConfig". |
| `spec.documents[].userVolumeConfig.encryption` | `object` | no | Talos: UserVolumeConfigV1Alpha1.encryption — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/uservolumeconfig |
| `spec.documents[].userVolumeConfig.encryption.allowDiscards` | `boolean` | no | Talos: EncryptionSpec.allowDiscards |
| `spec.documents[].userVolumeConfig.encryption.blockSize` | `integer` | no | Talos: EncryptionSpec.blockSize |
| `spec.documents[].userVolumeConfig.encryption.cipher` | `string` | no | Talos: EncryptionSpec.cipher |
| `spec.documents[].userVolumeConfig.encryption.keySize` | `integer` | no | Talos: EncryptionSpec.keySize |
| `spec.documents[].userVolumeConfig.encryption.keys` | `[]object` | yes | Talos: EncryptionSpec.keys |
| `spec.documents[].userVolumeConfig.encryption.keys[].kms` | `object` | no | Talos: EncryptionKey.kms |
| `spec.documents[].userVolumeConfig.encryption.keys[].kms.endpoint` | `string` | yes | Talos: EncryptionKeyKMS.endpoint |
| `spec.documents[].userVolumeConfig.encryption.keys[].lockToState` | `boolean` | no | Talos: EncryptionKey.lockToState |
| `spec.documents[].userVolumeConfig.encryption.keys[].nodeID` | `object` | no | Talos: EncryptionKey.nodeID |
| `spec.documents[].userVolumeConfig.encryption.keys[].slot` | `integer` | yes | Talos: EncryptionKey.slot |
| `spec.documents[].userVolumeConfig.encryption.keys[].tpm` | `object` | no | Talos: EncryptionKey.tpm |
| `spec.documents[].userVolumeConfig.encryption.keys[].tpm.checkSecurebootStatusOnEnroll` | `boolean` | no | Talos: EncryptionKeyTPM.checkSecurebootStatusOnEnroll |
| `spec.documents[].userVolumeConfig.encryption.keys[].tpm.options` | `object` | no | Talos: EncryptionKeyTPM.options |
| `spec.documents[].userVolumeConfig.encryption.keys[].tpm.options.pcrs` | `[]integer` | no | Talos: EncryptionKeyTPMOptions.pcrs |
| `spec.documents[].userVolumeConfig.encryption.options` | `[]string` | no | Talos: EncryptionSpec.options |
| `spec.documents[].userVolumeConfig.encryption.provider` | `string` | yes | Talos: EncryptionSpec.provider |
| `spec.documents[].userVolumeConfig.filesystem` | `object` | no | Talos: UserVolumeConfigV1Alpha1.filesystem — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/uservolumeconfig |
| `spec.documents[].userVolumeConfig.filesystem.projectQuotaSupport` | `boolean` | no | Talos: FilesystemSpec.projectQuotaSupport — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/uservolumeconfig |
| `spec.documents[].userVolumeConfig.filesystem.type` | `string` | no | Talos: FilesystemSpec.type — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/uservolumeconfig |
| `spec.documents[].userVolumeConfig.filesystem.xfs` | `object` | no | Talos: FilesystemSpec.xfs — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/uservolumeconfig |
| `spec.documents[].userVolumeConfig.filesystem.xfs.minAllocationGroupSize` | `string` | no | Talos: XFSSpec.minAllocationGroupSize |
| `spec.documents[].userVolumeConfig.mount` | `object` | no | Talos: UserVolumeConfigV1Alpha1.mount — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/uservolumeconfig |
| `spec.documents[].userVolumeConfig.mount.disableAccessTime` | `boolean` | no | Talos: UserMountSpec.disableAccessTime — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/uservolumeconfig |
| `spec.documents[].userVolumeConfig.mount.secure` | `boolean` | no | Talos: UserMountSpec.secure — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/uservolumeconfig |
| `spec.documents[].userVolumeConfig.name` | `string` | yes | Talos: UserVolumeConfigV1Alpha1.name — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/uservolumeconfig |
| `spec.documents[].userVolumeConfig.provisioning` | `object` | no | Talos: UserVolumeConfigV1Alpha1.provisioning — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/uservolumeconfig |
| `spec.documents[].userVolumeConfig.provisioning.diskSelector` | `object` | no | Talos: ProvisioningSpec.diskSelector |
| `spec.documents[].userVolumeConfig.provisioning.diskSelector.match` | `string` | no | Talos: DiskSelector.match |
| `spec.documents[].userVolumeConfig.provisioning.grow` | `boolean` | no | Talos: ProvisioningSpec.grow |
| `spec.documents[].userVolumeConfig.provisioning.maxSize` | `string` | no | Talos: ProvisioningSpec.maxSize |
| `spec.documents[].userVolumeConfig.provisioning.minSize` | `string` | no | Talos: ProvisioningSpec.minSize |
| `spec.documents[].userVolumeConfig.scrub` | `object` | no | Talos: UserVolumeConfigV1Alpha1.scrub — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/uservolumeconfig |
| `spec.documents[].userVolumeConfig.scrub.enabled` | `boolean` | no | Talos: ScrubConfig.enabled |
| `spec.documents[].userVolumeConfig.scrub.interval` | `string` | no | Talos: ScrubConfig.interval |
| `spec.documents[].userVolumeConfig.trim` | `object` | no | Talos: UserVolumeConfigV1Alpha1.trim — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/uservolumeconfig |
| `spec.documents[].userVolumeConfig.trim.enabled` | `boolean` | no | Talos: TrimConfig.enabled |
| `spec.documents[].userVolumeConfig.trim.interval` | `string` | no | Talos: TrimConfig.interval |
| `spec.documents[].userVolumeConfig.volumeType` | `string` | no | Talos: UserVolumeConfigV1Alpha1.volumeType — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/uservolumeconfig |
| `spec.documents[].volumeConfig` | `object` | no | volumeConfig is a Talos VolumeConfig document (a system volume, e.g. STATE or EPHEMERAL, placed on its own disk); populated when kind is "VolumeConfig". |
| `spec.documents[].volumeConfig.encryption` | `object` | no | Talos: VolumeConfigV1Alpha1.encryption — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/volumeconfig |
| `spec.documents[].volumeConfig.encryption.allowDiscards` | `boolean` | no | Talos: EncryptionSpec.allowDiscards |
| `spec.documents[].volumeConfig.encryption.blockSize` | `integer` | no | Talos: EncryptionSpec.blockSize |
| `spec.documents[].volumeConfig.encryption.cipher` | `string` | no | Talos: EncryptionSpec.cipher |
| `spec.documents[].volumeConfig.encryption.keySize` | `integer` | no | Talos: EncryptionSpec.keySize |
| `spec.documents[].volumeConfig.encryption.keys` | `[]object` | yes | Talos: EncryptionSpec.keys |
| `spec.documents[].volumeConfig.encryption.keys[].kms` | `object` | no | Talos: EncryptionKey.kms |
| `spec.documents[].volumeConfig.encryption.keys[].kms.endpoint` | `string` | yes | Talos: EncryptionKeyKMS.endpoint |
| `spec.documents[].volumeConfig.encryption.keys[].lockToState` | `boolean` | no | Talos: EncryptionKey.lockToState |
| `spec.documents[].volumeConfig.encryption.keys[].nodeID` | `object` | no | Talos: EncryptionKey.nodeID |
| `spec.documents[].volumeConfig.encryption.keys[].slot` | `integer` | yes | Talos: EncryptionKey.slot |
| `spec.documents[].volumeConfig.encryption.keys[].tpm` | `object` | no | Talos: EncryptionKey.tpm |
| `spec.documents[].volumeConfig.encryption.keys[].tpm.checkSecurebootStatusOnEnroll` | `boolean` | no | Talos: EncryptionKeyTPM.checkSecurebootStatusOnEnroll |
| `spec.documents[].volumeConfig.encryption.keys[].tpm.options` | `object` | no | Talos: EncryptionKeyTPM.options |
| `spec.documents[].volumeConfig.encryption.keys[].tpm.options.pcrs` | `[]integer` | no | Talos: EncryptionKeyTPMOptions.pcrs |
| `spec.documents[].volumeConfig.encryption.options` | `[]string` | no | Talos: EncryptionSpec.options |
| `spec.documents[].volumeConfig.encryption.provider` | `string` | yes | Talos: EncryptionSpec.provider |
| `spec.documents[].volumeConfig.filesystem` | `object` | no | Talos: VolumeConfigV1Alpha1.filesystem — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/volumeconfig |
| `spec.documents[].volumeConfig.filesystem.xfs` | `object` | no | Talos: SystemVolumeFilesystemSpec.xfs — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/volumeconfig |
| `spec.documents[].volumeConfig.filesystem.xfs.minAllocationGroupSize` | `string` | no | Talos: XFSSpec.minAllocationGroupSize |
| `spec.documents[].volumeConfig.mount` | `object` | no | Talos: VolumeConfigV1Alpha1.mount — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/volumeconfig |
| `spec.documents[].volumeConfig.mount.disableAccessTime` | `boolean` | no | Talos: MountSpec.disableAccessTime — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/volumeconfig |
| `spec.documents[].volumeConfig.mount.secure` | `boolean` | no | Talos: MountSpec.secure — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/volumeconfig |
| `spec.documents[].volumeConfig.name` | `string` | yes | Talos: VolumeConfigV1Alpha1.name — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/volumeconfig |
| `spec.documents[].volumeConfig.provisioning` | `object` | no | Talos: VolumeConfigV1Alpha1.provisioning — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/volumeconfig |
| `spec.documents[].volumeConfig.provisioning.diskSelector` | `object` | no | Talos: ProvisioningSpec.diskSelector |
| `spec.documents[].volumeConfig.provisioning.diskSelector.match` | `string` | no | Talos: DiskSelector.match |
| `spec.documents[].volumeConfig.provisioning.grow` | `boolean` | no | Talos: ProvisioningSpec.grow |
| `spec.documents[].volumeConfig.provisioning.maxSize` | `string` | no | Talos: ProvisioningSpec.maxSize |
| `spec.documents[].volumeConfig.provisioning.minSize` | `string` | no | Talos: ProvisioningSpec.minSize |
| `spec.documents[].volumeConfig.scrub` | `object` | no | Talos: VolumeConfigV1Alpha1.scrub — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/volumeconfig |
| `spec.documents[].volumeConfig.scrub.enabled` | `boolean` | no | Talos: ScrubConfig.enabled |
| `spec.documents[].volumeConfig.scrub.interval` | `string` | no | Talos: ScrubConfig.interval |
| `spec.documents[].volumeConfig.trim` | `object` | no | Talos: VolumeConfigV1Alpha1.trim — https://docs.siderolabs.com/talos/v1.14/reference/configuration/block/volumeconfig |
| `spec.documents[].volumeConfig.trim.enabled` | `boolean` | no | Talos: TrimConfig.enabled |
| `spec.documents[].volumeConfig.trim.interval` | `string` | no | Talos: TrimConfig.interval |
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

