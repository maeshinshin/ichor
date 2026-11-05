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
| `spec.documents[].config.cluster` | `object` | no | Provides cluster specific configuration options. |
| `spec.documents[].config.cluster.acceptedCAs` **(deprecated)** | `[]object` | no | docgen:nodoc Deprecated: Use `KubeAPIServerCAConfig` instead. |
| `spec.documents[].config.cluster.acceptedCAs[].crt` **(deprecated)** | `string (byte)` | yes |  |
| `spec.documents[].config.cluster.adminKubeconfig` | `object` | no | Settings for admin kubeconfig generation. Certificate lifetime can be configured. |
| `spec.documents[].config.cluster.adminKubeconfig.certLifetime` | `string` | no | Admin kubeconfig certificate lifetime (default is 1 year). Field format accepts any Go time.Duration format ('1h' for one hour, '10m' for ten minutes). |
| `spec.documents[].config.cluster.allowSchedulingOnControlPlanes` **(deprecated)** | `boolean` | no | docgen:nodoc Deprecated: use `KubeNodeConfig` instead. |
| `spec.documents[].config.cluster.allowSchedulingOnMasters` **(deprecated)** | `boolean` | no | docgen:nodoc Deprecated: use `KubeNodeConfig` instead. |
| `spec.documents[].config.cluster.apiServer` **(deprecated)** | `object` | no | docgen:nodoc Deprecated: Use `KubeAPIServerConfig` instead. |
| `spec.documents[].config.cluster.apiServer.admissionControl` **(deprecated)** | `[]object` | no | docgen:nodoc Deprecated: Use `KubeAdmissionControlConfig` instead. |
| `spec.documents[].config.cluster.apiServer.admissionControl[].name` **(deprecated)** | `string` | yes | Name is the name of the admission controller. It must match the registered admission plugin name. |
| `spec.documents[].config.cluster.apiServer.auditPolicy` **(deprecated)** | `object` | no | docgen:nodoc Deprecated: Use `KubeAuditPolicyConfig` instead. |
| `spec.documents[].config.cluster.apiServer.auditPolicy.apiVersion` **(deprecated)** | `string` | no | APIVersion defines the versioned schema of this representation of an object. Servers should convert recognized schemas to the latest internal value, and may reject unrecognized values. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |
| `spec.documents[].config.cluster.apiServer.auditPolicy.kind` **(deprecated)** | `string` | no | Kind is a string value representing the REST resource this object represents. Servers may infer this from the endpoint the client submits requests to. Cannot be updated. In CamelCase. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |
| `spec.documents[].config.cluster.apiServer.auditPolicy.metadata` **(deprecated)** | `object` | no | ObjectMeta is included for interoperability with API infrastructure. |
| `spec.documents[].config.cluster.apiServer.auditPolicy.omitManagedFields` **(deprecated)** | `boolean` | no | OmitManagedFields indicates whether to omit the managed fields of the request and response bodies from being written to the API audit log. This is used as a global default - a value of 'true' will omit the managed fileds, otherwise the managed fields will be included in the API audit log. Note that this can also be specified per rule in which case the value specified in a rule will override the global default. |
| `spec.documents[].config.cluster.apiServer.auditPolicy.omitStages` **(deprecated)** | `[]string` | no | OmitStages is a list of stages for which no events are created. Note that this can also be specified per rule in which case the union of both are omitted. |
| `spec.documents[].config.cluster.apiServer.auditPolicy.rules` **(deprecated)** | `[]object` | yes | Rules specify the audit Level a request should be recorded at. A request may match multiple rules, in which case the FIRST matching rule is used. The default audit level is None, but can be overridden by a catch-all rule at the end of the list. PolicyRules are strictly ordered. |
| `spec.documents[].config.cluster.apiServer.auditPolicy.rules[].level` **(deprecated)** | `string` | yes | The Level that requests matching this rule are recorded at. |
| `spec.documents[].config.cluster.apiServer.auditPolicy.rules[].namespaces` **(deprecated)** | `[]string` | no | Namespaces that this rule matches. The empty string "" matches non-namespaced resources. An empty list implies every namespace. |
| `spec.documents[].config.cluster.apiServer.auditPolicy.rules[].nonResourceURLs` **(deprecated)** | `[]string` | no | NonResourceURLs is a set of URL paths that should be audited. `*`s are allowed, but only as the full, final step in the path. Examples: - `/metrics` - Log requests for apiserver metrics - `/healthz*` - Log all health checks |
| `spec.documents[].config.cluster.apiServer.auditPolicy.rules[].omitManagedFields` **(deprecated)** | `boolean` | no | OmitManagedFields indicates whether to omit the managed fields of the request and response bodies from being written to the API audit log. - a value of 'true' will drop the managed fields from the API audit log - a value of 'false' indicates that the managed fileds should be included in the API audit log Note that the value, if specified, in this rule will override the global default If a value is not specified then the global default specified in Policy.OmitManagedFields will stand. |
| `spec.documents[].config.cluster.apiServer.auditPolicy.rules[].omitStages` **(deprecated)** | `[]string` | no | OmitStages is a list of stages for which no events are created. Note that this can also be specified policy wide in which case the union of both are omitted. An empty list means no restrictions will apply. |
| `spec.documents[].config.cluster.apiServer.auditPolicy.rules[].resources` **(deprecated)** | `[]object` | no | Resources that this rule matches. An empty list implies all kinds in all API groups. |
| `spec.documents[].config.cluster.apiServer.auditPolicy.rules[].resources[].group` **(deprecated)** | `string` | no | Group is the name of the API group that contains the resources. The empty string represents the core API group. `*` matches all groups |
| `spec.documents[].config.cluster.apiServer.auditPolicy.rules[].resources[].resourceNames` **(deprecated)** | `[]string` | no | ResourceNames is a list of resource instance names that the policy matches. Using this field requires Resources to be specified. An empty list implies that every instance of the resource is matched. |
| `spec.documents[].config.cluster.apiServer.auditPolicy.rules[].resources[].resources` **(deprecated)** | `[]string` | no | Resources is a list of resources this rule applies to. For example: - `pods` matches pods. - `pods/log` matches the log subresource of pods. - `*` matches all resources and their subresources. - `pods/*` matches all subresources of pods. - `*/scale` matches all scale subresources. If wildcard is present, the validation rule will ensure resources do not overlap with each other. An empty list implies all resources and subresources in this API groups apply. |
| `spec.documents[].config.cluster.apiServer.auditPolicy.rules[].userGroups` **(deprecated)** | `[]string` | no | The user groups this rule applies to. A user is considered matching if it is a member of any of the UserGroups. An empty list implies every user group. |
| `spec.documents[].config.cluster.apiServer.auditPolicy.rules[].users` **(deprecated)** | `[]string` | no | The users (by authenticated user name) this rule applies to. An empty list implies every user. |
| `spec.documents[].config.cluster.apiServer.auditPolicy.rules[].verbs` **(deprecated)** | `[]string` | no | The verbs that match this rule. An empty list implies every verb. |
| `spec.documents[].config.cluster.apiServer.authorizationConfig` **(deprecated)** | `[]object` | no | docgen:nodoc Deprecated: Use `KubeAuthorizerConfig` instead. |
| `spec.documents[].config.cluster.apiServer.authorizationConfig[].name` **(deprecated)** | `string` | yes | Name is used to describe the authorizer. |
| `spec.documents[].config.cluster.apiServer.authorizationConfig[].type` **(deprecated)** | `string` | yes | Type is the name of the authorizer. Allowed values are `Node`, `RBAC`, and `Webhook`. |
| `spec.documents[].config.cluster.apiServer.authorizationConfig[].webhook` **(deprecated)** | `object` | no | webhook is the configuration for the webhook authorizer. |
| `spec.documents[].config.cluster.apiServer.authorizationConfig[].webhook.authorizedTTL` **(deprecated)** | `string` | yes | The duration to cache 'authorized' responses from the webhook authorizer. Same as setting `--authorization-webhook-cache-authorized-ttl` flag Default: 5m0s |
| `spec.documents[].config.cluster.apiServer.authorizationConfig[].webhook.cacheAuthorizedRequests` **(deprecated)** | `boolean` | no | CacheAuthorizedRequests specifies whether authorized requests should be cached. If set to true, the TTL for cached decisions can be configured via the AuthorizedTTL field. Default: true |
| `spec.documents[].config.cluster.apiServer.authorizationConfig[].webhook.cacheUnauthorizedRequests` **(deprecated)** | `boolean` | no | CacheUnauthorizedRequests specifies whether unauthorized requests should be cached. If set to true, the TTL for cached decisions can be configured via the UnauthorizedTTL field. Default: true |
| `spec.documents[].config.cluster.apiServer.authorizationConfig[].webhook.connectionInfo` **(deprecated)** | `object` | yes | ConnectionInfo defines how we talk to the webhook |
| `spec.documents[].config.cluster.apiServer.authorizationConfig[].webhook.connectionInfo.kubeConfigFile` **(deprecated)** | `string` | yes | Path to KubeConfigFile for connection info Required, if connectionInfo.Type is KubeConfig |
| `spec.documents[].config.cluster.apiServer.authorizationConfig[].webhook.connectionInfo.type` **(deprecated)** | `string` | yes | Controls how the webhook should communicate with the server. Valid values: - KubeConfigFile: use the file specified in kubeConfigFile to locate the server. - InClusterConfig: use the in-cluster configuration to call the SubjectAccessReview API hosted by kube-apiserver. This mode is not allowed for kube-apiserver. |
| `spec.documents[].config.cluster.apiServer.authorizationConfig[].webhook.failurePolicy` **(deprecated)** | `string` | yes | Controls the authorization decision when a webhook request fails to complete or returns a malformed response or errors evaluating matchConditions. Valid values: - NoOpinion: continue to subsequent authorizers to see if one of them allows the request - Deny: reject the request without consulting subsequent authorizers Required, with no default. |
| `spec.documents[].config.cluster.apiServer.authorizationConfig[].webhook.matchConditionSubjectAccessReviewVersion` **(deprecated)** | `string` | yes | MatchConditionSubjectAccessReviewVersion specifies the SubjectAccessReview version the CEL expressions are evaluated against Valid values: v1 Required, no default value |
| `spec.documents[].config.cluster.apiServer.authorizationConfig[].webhook.matchConditions` **(deprecated)** | `[]object` | yes | matchConditions is a list of conditions that must be met for a request to be sent to this webhook. An empty list of matchConditions matches all requests. There are a maximum of 64 match conditions allowed. The exact matching logic is (in order): 1. If at least one matchCondition evaluates to FALSE, then the webhook is skipped. 2. If ALL matchConditions evaluate to TRUE, then the webhook is called. 3. If at least one matchCondition evaluates to an error (but none are FALSE): - If failurePolicy=Deny, then the webhook rejects the request - If failurePolicy=NoOpinion, then the error is ignored and the webhook is skipped |
| `spec.documents[].config.cluster.apiServer.authorizationConfig[].webhook.matchConditions[].expression` **(deprecated)** | `string` | yes | expression represents the expression which will be evaluated by CEL. Must evaluate to bool. CEL expressions have access to the contents of the SubjectAccessReview in v1 version. If version specified by subjectAccessReviewVersion in the request variable is v1beta1, the contents would be converted to the v1 version before evaluating the CEL expression. - 'resourceAttributes' describes information for a resource access request and is unset for non-resource requests. e.g. has(request.resourceAttributes) && request.resourceAttributes.namespace == 'default' - 'nonResourceAttributes' describes information for a non-resource access request and is unset for resource requests. e.g. has(request.nonResourceAttributes) && request.nonResourceAttributes.path == '/healthz'. - 'user' is the user to test for. e.g. request.user == 'alice' - 'groups' is the groups to test for. e.g. ('group1' in request.groups) - 'extra' corresponds to the user.Info.GetExtra() method from the authenticator. - 'uid' is the information about the requesting user. e.g. request.uid == '1' Documentation on CEL: https://kubernetes.io/docs/reference/using-api/cel/ |
| `spec.documents[].config.cluster.apiServer.authorizationConfig[].webhook.subjectAccessReviewVersion` **(deprecated)** | `string` | yes | The API version of the authorization.k8s.io SubjectAccessReview to send to and expect from the webhook. Same as setting `--authorization-webhook-version` flag Valid values: v1beta1, v1 Required, no default value |
| `spec.documents[].config.cluster.apiServer.authorizationConfig[].webhook.timeout` **(deprecated)** | `string` | yes | Timeout for the webhook request Maximum allowed value is 30s. Required, no default value. |
| `spec.documents[].config.cluster.apiServer.authorizationConfig[].webhook.unauthorizedTTL` **(deprecated)** | `string` | yes | The duration to cache 'unauthorized' responses from the webhook authorizer. Same as setting `--authorization-webhook-cache-unauthorized-ttl` flag Default: 30s |
| `spec.documents[].config.cluster.apiServer.certSANs` **(deprecated)** | `[]string` | no | Extra certificate subject alternative names for the API server's certificate. |
| `spec.documents[].config.cluster.apiServer.disablePodSecurityPolicy` **(deprecated)** | `boolean` | no | docgen:nodoc |
| `spec.documents[].config.cluster.apiServer.env` **(deprecated)** | `map[string]string` | no | The `env` field allows for the addition of environment variables for the control plane component. |
| `spec.documents[].config.cluster.apiServer.extraArgs` **(deprecated)** | `map[string][]string` | no | Extra arguments to supply to the API server. |
| `spec.documents[].config.cluster.apiServer.extraVolumes` **(deprecated)** | `[]object` | no | Extra volumes to mount to the API server static pod. |
| `spec.documents[].config.cluster.apiServer.extraVolumes[].hostPath` **(deprecated)** | `string` | yes | Path on the host. |
| `spec.documents[].config.cluster.apiServer.extraVolumes[].mountPath` **(deprecated)** | `string` | yes | Path in the container. |
| `spec.documents[].config.cluster.apiServer.extraVolumes[].readonly` **(deprecated)** | `boolean` | no | Mount the volume read only. |
| `spec.documents[].config.cluster.apiServer.image` **(deprecated)** | `string` | no | The container image used in the API server manifest. |
| `spec.documents[].config.cluster.apiServer.resources` **(deprecated)** | `object` | no | Configure the API server resources. |
| `spec.documents[].config.cluster.apiServer.resources.limits` **(deprecated)** | `map[string]integer or string` | no | Limits configures the maximum cpu/memory resources a container can use. |
| `spec.documents[].config.cluster.apiServer.resources.requests` **(deprecated)** | `map[string]integer or string` | no | Requests configures the reserved cpu/memory resources. |
| `spec.documents[].config.cluster.clusterName` **(deprecated)** | `string` | no | docgen:nodoc Deprecated: Use `KubeClusterConfig` instead. |
| `spec.documents[].config.cluster.controlPlane` **(deprecated)** | `object` | no | docgen:nodoc Deprecated: Use `KubeClusterConfig` instead. |
| `spec.documents[].config.cluster.controlPlane.endpoint` **(deprecated)** | `string` | yes | Endpoint is the canonical controlplane endpoint, which can be an IP address or a DNS hostname. It is single-valued, and may optionally include a port number. |
| `spec.documents[].config.cluster.controlPlane.localAPIServerPort` **(deprecated)** | `integer` | no | docgen:nodoc Deprecated: Use `KubeAPIServerConfig` instead. |
| `spec.documents[].config.cluster.controllerManager` **(deprecated)** | `object` | no | docgen:nodoc Deprecated: Use `KubeControllerManagerConfig` instead. |
| `spec.documents[].config.cluster.controllerManager.env` **(deprecated)** | `map[string]string` | no | The `env` field allows for the addition of environment variables for the control plane component. |
| `spec.documents[].config.cluster.controllerManager.extraArgs` **(deprecated)** | `map[string][]string` | no | Extra arguments to supply to the controller manager. |
| `spec.documents[].config.cluster.controllerManager.extraVolumes` **(deprecated)** | `[]object` | no | Extra volumes to mount to the controller manager static pod. |
| `spec.documents[].config.cluster.controllerManager.extraVolumes[].hostPath` **(deprecated)** | `string` | yes | Path on the host. |
| `spec.documents[].config.cluster.controllerManager.extraVolumes[].mountPath` **(deprecated)** | `string` | yes | Path in the container. |
| `spec.documents[].config.cluster.controllerManager.extraVolumes[].readonly` **(deprecated)** | `boolean` | no | Mount the volume read only. |
| `spec.documents[].config.cluster.controllerManager.image` **(deprecated)** | `string` | no | The container image used in the controller manager manifest. |
| `spec.documents[].config.cluster.controllerManager.resources` **(deprecated)** | `object` | no | Configure the controller manager resources. |
| `spec.documents[].config.cluster.controllerManager.resources.limits` **(deprecated)** | `map[string]integer or string` | no | Limits configures the maximum cpu/memory resources a container can use. |
| `spec.documents[].config.cluster.controllerManager.resources.requests` **(deprecated)** | `map[string]integer or string` | no | Requests configures the reserved cpu/memory resources. |
| `spec.documents[].config.cluster.coreDNS` **(deprecated)** | `object` | no | docgen:nodoc Deprecated: Use `KubeCoreDNSConfig` instead. |
| `spec.documents[].config.cluster.coreDNS.disabled` **(deprecated)** | `boolean` | no | Disable coredns deployment on cluster bootstrap. |
| `spec.documents[].config.cluster.coreDNS.image` **(deprecated)** | `string` | no | The `image` field is an override to the default coredns image. |
| `spec.documents[].config.cluster.discovery` **(deprecated)** | `object` | no | docgen:nodoc Deprecated: Use 'DiscoveryServiceConfig' instead |
| `spec.documents[].config.cluster.discovery.enabled` **(deprecated)** | `boolean` | no | Enable the cluster membership discovery feature. Cluster discovery is based on individual registries which are configured under the registries field. |
| `spec.documents[].config.cluster.discovery.registries` **(deprecated)** | `object` | yes | Configure registries used for cluster member discovery. |
| `spec.documents[].config.cluster.discovery.registries.kubernetes` **(deprecated)** | `object` | yes | Kubernetes registry uses Kubernetes API server to discover cluster members and stores additional information as annotations on the Node resources. This feature is deprecated as it is not compatible with Kubernetes 1.32+. See https://github.com/siderolabs/talos/issues/9980 for more information. |
| `spec.documents[].config.cluster.discovery.registries.kubernetes.disabled` **(deprecated)** | `boolean` | no | Disable Kubernetes discovery registry. |
| `spec.documents[].config.cluster.discovery.registries.service` **(deprecated)** | `object` | yes | Service registry is using an external service to push and pull information about cluster members. |
| `spec.documents[].config.cluster.discovery.registries.service.disabled` **(deprecated)** | `boolean` | no | Disable external service discovery registry. |
| `spec.documents[].config.cluster.discovery.registries.service.endpoint` **(deprecated)** | `string` | no | External service endpoint. |
| `spec.documents[].config.cluster.etcd` | `object` | no | Etcd specific configuration options. |
| `spec.documents[].config.cluster.etcd.advertisedSubnets` | `[]string` | no | The `advertisedSubnets` field configures the networks to pick etcd advertised IP from. IPs can be excluded from the list by using negative match with `!`, e.g `!10.0.0.0/8`. Negative subnet matches should be specified last to filter out IPs picked by positive matches. If not specified, advertised IP is selected as the first routable address of the node. |
| `spec.documents[].config.cluster.etcd.extraArgs` | `map[string][]string` | no | Extra arguments to supply to etcd. Note that the following args are not allowed: - `name` - `data-dir` - `initial-cluster-state` - `listen-peer-urls` - `listen-client-urls` - `cert-file` - `key-file` - `trusted-ca-file` - `peer-client-cert-auth` - `peer-cert-file` - `peer-trusted-ca-file` - `peer-key-file` |
| `spec.documents[].config.cluster.etcd.image` | `string` | no | The container image used to create the etcd service. |
| `spec.documents[].config.cluster.etcd.listenSubnets` | `[]string` | no | The `listenSubnets` field configures the networks for the etcd to listen for peer and client connections. If `listenSubnets` is not set, but `advertisedSubnets` is set, `listenSubnets` defaults to `advertisedSubnets`. If neither `advertisedSubnets` nor `listenSubnets` is set, `listenSubnets` defaults to listen on all addresses. IPs can be excluded from the list by using negative match with `!`, e.g `!10.0.0.0/8`. Negative subnet matches should be specified last to filter out IPs picked by positive matches. If not specified, advertised IP is selected as the first routable address of the node. |
| `spec.documents[].config.cluster.etcd.subnet` **(deprecated)** | `string` | no | docgen:nodoc Deprecated: use EtcdAdvertistedSubnets |
| `spec.documents[].config.cluster.externalCloudProvider` | `object` | no | External cloud provider configuration. |
| `spec.documents[].config.cluster.externalCloudProvider.enabled` | `boolean` | no | Enable external cloud provider. |
| `spec.documents[].config.cluster.externalCloudProvider.manifests` | `[]string` | no | A list of urls that point to additional manifests for an external cloud provider. These will get automatically deployed as part of the bootstrap. |
| `spec.documents[].config.cluster.extraManifestHeaders` **(deprecated)** | `map[string]string` | no | docgen:nodoc Deprecated: Use `KubeExternalManifestConfig` instead. |
| `spec.documents[].config.cluster.extraManifests` **(deprecated)** | `[]string` | no | docgen:nodoc Deprecated: Use `KubeExternalManifestConfig` instead. |
| `spec.documents[].config.cluster.id` **(deprecated)** | `string` | no | docgen:nodoc Deprecated: Use 'DiscoveryIdentityConfig' document instead. |
| `spec.documents[].config.cluster.inlineManifests` **(deprecated)** | `[]object` | no | docgen:nodoc Deprecated: Use `KubeInlineManifestConfig` instead. |
| `spec.documents[].config.cluster.inlineManifests[].contents` **(deprecated)** | `string` | yes | Manifest contents as a string. |
| `spec.documents[].config.cluster.inlineManifests[].name` **(deprecated)** | `string` | yes | Name of the manifest. Name should be unique. |
| `spec.documents[].config.cluster.network` **(deprecated)** | `object` | no | docgen:nodoc Deprecated: Use `KubeNetworkConfig` and `KubeFlannelCNIConfig` instead. |
| `spec.documents[].config.cluster.network.cni` **(deprecated)** | `object` | no | The CNI used. Composed of "name" and "urls". The "name" key supports the following options: "flannel", "custom", and "none". "flannel" uses Talos-managed Flannel CNI, and that's the default option. "custom" uses custom manifests that should be provided in "urls". "none" indicates that Talos will not manage any CNI installation. |
| `spec.documents[].config.cluster.network.cni.flannel` **(deprecated)** | `object` | no | FlannelCNIConfig represents the Flannel CNI configuration options. docgen:nodoc |
| `spec.documents[].config.cluster.network.cni.flannel.extraArgs` **(deprecated)** | `[]string` | no | Extra arguments for 'flanneld'. |
| `spec.documents[].config.cluster.network.cni.flannel.kubeNetworkPoliciesEnabled` **(deprecated)** | `boolean` | no | Deploys kube-network-policies along with Flannel. This enables Kubernetes Network Policies support in the cluster. |
| `spec.documents[].config.cluster.network.cni.name` **(deprecated)** | `string` | no | Name of CNI to use. |
| `spec.documents[].config.cluster.network.cni.urls` **(deprecated)** | `[]string` | no | URLs containing manifests to apply for the CNI. Should be present for "custom", must be empty for "flannel" and "none". |
| `spec.documents[].config.cluster.network.dnsDomain` **(deprecated)** | `string` | yes | The domain used by Kubernetes DNS. The default is `cluster.local` |
| `spec.documents[].config.cluster.network.podSubnets` **(deprecated)** | `[]string` | yes | The pod subnet CIDR. |
| `spec.documents[].config.cluster.network.serviceSubnets` **(deprecated)** | `[]string` | yes | The service subnet CIDR. |
| `spec.documents[].config.cluster.proxy` **(deprecated)** | `object` | no | docgen:nodoc Deprecated: use `KubeProxyConfig` instead. |
| `spec.documents[].config.cluster.proxy.disabled` **(deprecated)** | `boolean` | no | Disable kube-proxy deployment on cluster bootstrap. |
| `spec.documents[].config.cluster.proxy.extraArgs` **(deprecated)** | `map[string][]string` | no | Extra arguments to supply to kube-proxy. |
| `spec.documents[].config.cluster.proxy.image` **(deprecated)** | `string` | no | The container image used in the kube-proxy manifest. |
| `spec.documents[].config.cluster.proxy.mode` **(deprecated)** | `string` | no | proxy mode of kube-proxy. The default is 'iptables'. |
| `spec.documents[].config.cluster.scheduler` **(deprecated)** | `object` | no | docgen:nodoc Deprecated: Use `KubeSchedulerConfig` instead. |
| `spec.documents[].config.cluster.scheduler.env` **(deprecated)** | `map[string]string` | no | The `env` field allows for the addition of environment variables for the control plane component. |
| `spec.documents[].config.cluster.scheduler.extraArgs` **(deprecated)** | `map[string][]string` | no | Extra arguments to supply to the scheduler. |
| `spec.documents[].config.cluster.scheduler.extraVolumes` **(deprecated)** | `[]object` | no | Extra volumes to mount to the scheduler static pod. |
| `spec.documents[].config.cluster.scheduler.extraVolumes[].hostPath` **(deprecated)** | `string` | yes | Path on the host. |
| `spec.documents[].config.cluster.scheduler.extraVolumes[].mountPath` **(deprecated)** | `string` | yes | Path in the container. |
| `spec.documents[].config.cluster.scheduler.extraVolumes[].readonly` **(deprecated)** | `boolean` | no | Mount the volume read only. |
| `spec.documents[].config.cluster.scheduler.image` **(deprecated)** | `string` | no | The container image used in the scheduler manifest. |
| `spec.documents[].config.cluster.scheduler.resources` **(deprecated)** | `object` | no | Configure the scheduler resources. |
| `spec.documents[].config.cluster.scheduler.resources.limits` **(deprecated)** | `map[string]integer or string` | no | Limits configures the maximum cpu/memory resources a container can use. |
| `spec.documents[].config.cluster.scheduler.resources.requests` **(deprecated)** | `map[string]integer or string` | no | Requests configures the reserved cpu/memory resources. |
| `spec.documents[].config.debug` | `boolean` | no | Enable verbose logging to the console. All system containers logs will flow into serial console. **Note:** To avoid breaking Talos bootstrap flow enable this option only if serial console can handle high message throughput. |
| `spec.documents[].config.machine` | `object` | no | Provides machine specific configuration options. |
| `spec.documents[].config.machine.acceptedCAs` | `[]object` | no | The certificates issued by certificate authorities are accepted in addition to issuing 'ca'. It is composed of a base64 encoded `crt``. |
| `spec.documents[].config.machine.acceptedCAs[].crt` | `string (byte)` | yes |  |
| `spec.documents[].config.machine.certSANs` | `[]string` | no | Extra certificate subject alternative names for the machine's certificate. By default, all non-loopback interface IPs are automatically added to the certificate's SANs. |
| `spec.documents[].config.machine.controlPlane` **(deprecated)** | `object` | no | docgen:nodoc Deprecated: Use `KubeControllerManagerConfig`/`KubeSchedulerConfig` instead. |
| `spec.documents[].config.machine.controlPlane.controllerManager` **(deprecated)** | `object` | no | docgen:nodoc Deprecated: Use `KubeControllerManagerConfig` instead. |
| `spec.documents[].config.machine.controlPlane.controllerManager.disabled` **(deprecated)** | `boolean` | no | Disable kube-controller-manager on the node. |
| `spec.documents[].config.machine.controlPlane.scheduler` **(deprecated)** | `object` | no | docgen:nodoc Deprecated: Use `KubeSchedulerConfig` instead. |
| `spec.documents[].config.machine.controlPlane.scheduler.disabled` **(deprecated)** | `boolean` | no | Disable kube-scheduler on the node. |
| `spec.documents[].config.machine.disks` **(deprecated)** | `[]object` | no | docgen:nodoc Deprecated: Use 'UserVolumeConfig' instead. |
| `spec.documents[].config.machine.disks[].device` **(deprecated)** | `string` | no | The name of the disk to use. |
| `spec.documents[].config.machine.disks[].partitions` **(deprecated)** | `[]object` | no | A list of partitions to create on the disk. |
| `spec.documents[].config.machine.disks[].partitions[].mountpoint` **(deprecated)** | `string` | no | Where to mount the partition. |
| `spec.documents[].config.machine.disks[].partitions[].size` **(deprecated)** | `string` | no | The size of partition: either bytes or human readable representation. If `size:` is omitted, the partition is sized to occupy the full disk. |
| `spec.documents[].config.machine.env` **(deprecated)** | `map[string]string` | no | docgen:nodoc Deprecated: Use 'EnvironmentConfig' instead. |
| `spec.documents[].config.machine.features` | `object` | no | Features describe individual Talos features that can be switched on or off. |
| `spec.documents[].config.machine.features.apidCheckExtKeyUsage` | `boolean` | no | docgen:nodoc |
| `spec.documents[].config.machine.features.diskQuotaSupport` | `boolean` | no | Enable XFS project quota support for EPHEMERAL partition and user disks. Also enables kubelet tracking of ephemeral disk usage in the kubelet via quota. |
| `spec.documents[].config.machine.features.hostDNS` **(deprecated)** | `object` | no | docgen:nodoc Deprecated: Use ResolverConfig document instead. |
| `spec.documents[].config.machine.features.hostDNS.enabled` **(deprecated)** | `boolean` | no | Enable host DNS caching resolver. |
| `spec.documents[].config.machine.features.hostDNS.forwardKubeDNSToHost` **(deprecated)** | `boolean` | no | Use the host DNS resolver as upstream for Kubernetes CoreDNS pods. When enabled, CoreDNS pods use host DNS server as the upstream DNS (instead of using configured upstream DNS resolvers directly). |
| `spec.documents[].config.machine.features.hostDNS.resolveMemberNames` **(deprecated)** | `boolean` | no | Resolve member hostnames using the host DNS resolver. When enabled, cluster member hostnames and node names are resolved using the host DNS resolver. This requires service discovery to be enabled. |
| `spec.documents[].config.machine.features.imageCache` **(deprecated)** | `object` | no | docgen:nodoc Deprecated: Use ImageCacheConfig document instead. |
| `spec.documents[].config.machine.features.imageCache.localEnabled` **(deprecated)** | `boolean` | no | Enable local image cache. |
| `spec.documents[].config.machine.features.kubePrism` **(deprecated)** | `object` | no | docgen:nodoc Deprecated: Use KubePrismConfig document instead. |
| `spec.documents[].config.machine.features.kubePrism.enabled` **(deprecated)** | `boolean` | no | Enable KubePrism support - will start local load balancing proxy. |
| `spec.documents[].config.machine.features.kubePrism.port` **(deprecated)** | `integer` | no | KubePrism port. |
| `spec.documents[].config.machine.features.kubernetesTalosAPIAccess` **(deprecated)** | `object` | no | docgen:nodoc Deprecated: use KubeTalosAPIAccessConfig instead. |
| `spec.documents[].config.machine.features.kubernetesTalosAPIAccess.allowedKubernetesNamespaces` **(deprecated)** | `[]string` | no | The list of Kubernetes namespaces Talos API access is available from. |
| `spec.documents[].config.machine.features.kubernetesTalosAPIAccess.allowedRoles` **(deprecated)** | `[]string` | no | The list of Talos API roles which can be granted for access from Kubernetes pods. Empty list means that no roles can be granted, so access is blocked. |
| `spec.documents[].config.machine.features.kubernetesTalosAPIAccess.enabled` **(deprecated)** | `boolean` | no | Enable Talos API access from Kubernetes pods. |
| `spec.documents[].config.machine.features.nodeAddressSortAlgorithm` | `string` | no | Select the node address sort algorithm. The 'v1' algorithm sorts addresses by the address itself. The 'v2' algorithm prefers more specific prefixes. If unset, defaults to 'v1'. |
| `spec.documents[].config.machine.features.rbac` | `boolean` | no | docgen:nodoc |
| `spec.documents[].config.machine.features.stableHostname` **(deprecated)** | `boolean` | no | docgen:nodoc Deprecated: use HostConfig instead. |
| `spec.documents[].config.machine.files` **(deprecated)** | `[]object` | no | docgen:nodoc Deprecated: Use dedicated configuration documents such as EtcFileConfig and CRICustomizationConfig instead. |
| `spec.documents[].config.machine.files[].content` **(deprecated)** | `string` | yes | The contents of the file. |
| `spec.documents[].config.machine.files[].op` **(deprecated)** | `string` | yes | The operation to use |
| `spec.documents[].config.machine.files[].path` **(deprecated)** | `string` | yes | The path of the file. |
| `spec.documents[].config.machine.files[].permissions` **(deprecated)** | `string` | yes | The file's permissions in octal. |
| `spec.documents[].config.machine.install` **(deprecated)** | `object` | no | docgen:nodoc Deprecated: Use the 'UnattendedInstall' multi-document config instead. |
| `spec.documents[].config.machine.install.bootloader` **(deprecated)** | `boolean` | no | docgen:nodoc Deprecated: It never worked. |
| `spec.documents[].config.machine.install.disk` **(deprecated)** | `string` | no | The disk used for installations. |
| `spec.documents[].config.machine.install.diskSelector` **(deprecated)** | `object` | no | Look up disk using disk attributes like model, size, serial and others. Always has priority over `disk`. |
| `spec.documents[].config.machine.install.diskSelector.busPath` **(deprecated)** | `string` | no | Disk bus path. |
| `spec.documents[].config.machine.install.diskSelector.modalias` **(deprecated)** | `string` | no | Disk modalias `/sys/block/\<dev>/device/modalias`. |
| `spec.documents[].config.machine.install.diskSelector.model` **(deprecated)** | `string` | no | Disk model `/sys/block/\<dev>/device/model`. |
| `spec.documents[].config.machine.install.diskSelector.name` **(deprecated)** | `string` | no | Disk name `/sys/block/\<dev>/device/name`. |
| `spec.documents[].config.machine.install.diskSelector.serial` **(deprecated)** | `string` | no | Disk serial number `/sys/block/\<dev>/serial`. |
| `spec.documents[].config.machine.install.diskSelector.size` **(deprecated)** | `string` | no | Disk size. |
| `spec.documents[].config.machine.install.diskSelector.type` **(deprecated)** | `string` | no | Disk Type. |
| `spec.documents[].config.machine.install.diskSelector.uuid` **(deprecated)** | `string` | no | Disk UUID `/sys/block/\<dev>/uuid`. |
| `spec.documents[].config.machine.install.diskSelector.wwid` **(deprecated)** | `string` | no | Disk WWID `/sys/block/\<dev>/wwid`. |
| `spec.documents[].config.machine.install.extensions` **(deprecated)** | `[]object` | no | docgen:nodoc Deprecated: Use custom `InstallImage` instead. |
| `spec.documents[].config.machine.install.extensions[].image` **(deprecated)** | `string` | yes | System extension image. |
| `spec.documents[].config.machine.install.extraKernelArgs` **(deprecated)** | `[]string` | no | docgen:nodoc Deprecated: Use Image Factory/imager instead to build a proper installer. |
| `spec.documents[].config.machine.install.grubUseUKICmdline` **(deprecated)** | `boolean` | no | Indicates if legacy GRUB bootloader should use kernel cmdline from the UKI instead of building it on the host. This changes the way cmdline is managed with GRUB bootloader to be more consistent with UKI/systemd-boot. |
| `spec.documents[].config.machine.install.image` **(deprecated)** | `string` | no | Allows for supplying the image used to perform the installation. Image reference for each Talos release can be found on [GitHub releases page](https://github.com/siderolabs/talos/releases). |
| `spec.documents[].config.machine.install.legacyBIOSSupport` **(deprecated)** | `boolean` | no | Indicates if MBR partition should be marked as bootable (active). Should be enabled only for the systems with legacy BIOS that doesn't support GPT partitioning scheme. |
| `spec.documents[].config.machine.install.wipe` **(deprecated)** | `boolean` | yes | Indicates if the installation disk should be wiped at installation time. Defaults to `true`. |
| `spec.documents[].config.machine.kernel` **(deprecated)** | `object` | no | docgen:nodoc Deprecated: Use 'KernelModuleConfig' instead. |
| `spec.documents[].config.machine.kernel.modules` **(deprecated)** | `[]object` | no | Kernel modules to load. |
| `spec.documents[].config.machine.kernel.modules[].name` **(deprecated)** | `string` | yes | Module name. |
| `spec.documents[].config.machine.kernel.modules[].parameters` **(deprecated)** | `[]string` | no | Module parameters, changes applied after reboot. |
| `spec.documents[].config.machine.kubelet` **(deprecated)** | `object` | no | docgen:nodoc Deprecated: Use `KubeletConfig` instead. |
| `spec.documents[].config.machine.kubelet.clusterDNS` **(deprecated)** | `[]string` | no | docgen:nodoc Deprecated: use `KubeletConfig` instead. |
| `spec.documents[].config.machine.kubelet.credentialProviderConfig` **(deprecated)** | `object` | no | The `KubeletCredentialProviderConfig` field is used to provide kubelet credential configuration. |
| `spec.documents[].config.machine.kubelet.credentialProviderConfig.apiVersion` **(deprecated)** | `string` | no | APIVersion defines the versioned schema of this representation of an object. Servers should convert recognized schemas to the latest internal value, and may reject unrecognized values. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |
| `spec.documents[].config.machine.kubelet.credentialProviderConfig.kind` **(deprecated)** | `string` | no | Kind is a string value representing the REST resource this object represents. Servers may infer this from the endpoint the client submits requests to. Cannot be updated. In CamelCase. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |
| `spec.documents[].config.machine.kubelet.credentialProviderConfig.providers` **(deprecated)** | `[]object` | yes | providers is a list of credential provider plugins that will be enabled by the kubelet. Multiple providers may match against a single image, in which case credentials from all providers will be returned to the kubelet. If multiple providers are called for a single image, the results are combined. If providers return overlapping auth keys, the value from the provider earlier in this list is attempted first. |
| `spec.documents[].config.machine.kubelet.credentialProviderConfig.providers[].apiVersion` **(deprecated)** | `string` | yes | Required input version of the exec CredentialProviderRequest. The returned CredentialProviderResponse MUST use the same encoding version as the input. Current supported values are: - credentialprovider.kubelet.k8s.io/v1 |
| `spec.documents[].config.machine.kubelet.credentialProviderConfig.providers[].args` **(deprecated)** | `[]string` | no | Arguments to pass to the command when executing it. |
| `spec.documents[].config.machine.kubelet.credentialProviderConfig.providers[].defaultCacheDuration` **(deprecated)** | `string` | yes | defaultCacheDuration is the default duration the plugin will cache credentials in-memory if a cache duration is not provided in the plugin response. This field is required. |
| `spec.documents[].config.machine.kubelet.credentialProviderConfig.providers[].env` **(deprecated)** | `[]object` | no | Env defines additional environment variables to expose to the process. These are unioned with the host's environment, as well as variables client-go uses to pass argument to the plugin. |
| `spec.documents[].config.machine.kubelet.credentialProviderConfig.providers[].env[].name` **(deprecated)** | `string` | yes |  |
| `spec.documents[].config.machine.kubelet.credentialProviderConfig.providers[].env[].value` **(deprecated)** | `string` | yes |  |
| `spec.documents[].config.machine.kubelet.credentialProviderConfig.providers[].matchImages` **(deprecated)** | `[]string` | yes | matchImages is a required list of strings used to match against images in order to determine if this provider should be invoked. If one of the strings matches the requested image from the kubelet, the plugin will be invoked and given a chance to provide credentials. Images are expected to contain the registry domain and URL path. Each entry in matchImages is a pattern which can optionally contain a port and a path. Globs can be used in the domain, but not in the port or the path. Globs are supported as subdomains like '*.k8s.io' or 'k8s.*.io', and top-level-domains such as 'k8s.*'. Matching partial subdomains like 'app*.k8s.io' is also supported. Each glob can only match a single subdomain segment, so *.io does not match *.k8s.io. A match exists between an image and a matchImage when all of the below are true: - Both contain the same number of domain parts and each part matches. - The URL path of an imageMatch must be a prefix of the target image URL path. - If the imageMatch contains a port, then the port must match in the image as well. Example values of matchImages: - 123456789.dkr.ecr.us-east-1.amazonaws.com - *.azurecr.io - gcr.io - *.*.registry.io - registry.io:8080/path |
| `spec.documents[].config.machine.kubelet.credentialProviderConfig.providers[].name` **(deprecated)** | `string` | yes | name is the required name of the credential provider. It must match the name of the provider executable as seen by the kubelet. The executable must be in the kubelet's bin directory (set by the --image-credential-provider-bin-dir flag). Required to be unique across all providers. |
| `spec.documents[].config.machine.kubelet.credentialProviderConfig.providers[].tokenAttributes` **(deprecated)** | `object` | no | tokenAttributes is the configuration for the service account token that will be passed to the plugin. The credential provider opts in to using service account tokens for image pull by setting this field. When this field is set, kubelet will generate a service account token bound to the pod for which the image is being pulled and pass to the plugin as part of CredentialProviderRequest along with other attributes required by the plugin. The service account metadata and token attributes will be used as a dimension to cache the credentials in kubelet. The cache key is generated by combining the service account metadata (namespace, name, UID, and annotations key+value for the keys defined in serviceAccountTokenAttribute.requiredServiceAccountAnnotationKeys and serviceAccountTokenAttribute.optionalServiceAccountAnnotationKeys). The pod metadata (namespace, name, UID) that are in the service account token are not used as a dimension to cache the credentials in kubelet. This means workloads that are using the same service account could end up using the same credentials for image pull. For plugins that don't want this behavior, or plugins that operate in pass-through mode; i.e., they return the service account token as-is, they can set the credentialProviderResponse.cacheDuration to 0. This will disable the caching of credentials in kubelet and the plugin will be invoked for every image pull. This does result in token generation overhead for every image pull, but it is the only way to ensure that the credentials are not shared across pods (even if they are using the same service account). |
| `spec.documents[].config.machine.kubelet.credentialProviderConfig.providers[].tokenAttributes.cacheType` **(deprecated)** | `string` | yes | cacheType indicates the type of cache key use for caching the credentials returned by the plugin when the service account token is used. The most conservative option is to set this to "Token", which means the kubelet will cache returned credentials on a per-token basis. This should be set if the returned credential's lifetime is limited to the service account token's lifetime. If the plugin's credential retrieval logic depends only on the service account and not on pod-specific claims, then the plugin can set this to "ServiceAccount". In this case, the kubelet will cache returned credentials on a per-serviceaccount basis. Use this when the returned credential is valid for all pods using the same service account. |
| `spec.documents[].config.machine.kubelet.credentialProviderConfig.providers[].tokenAttributes.optionalServiceAccountAnnotationKeys` **(deprecated)** | `[]string` | no | optionalServiceAccountAnnotationKeys is the list of annotation keys that the plugin is interested in and that are optional to be present in the service account. The keys defined in this list will be extracted from the corresponding service account and passed to the plugin as part of the CredentialProviderRequest. The plugin is responsible for validating the existence of annotations and their values. This field is optional and may be empty. Plugins may use this field to extract additional information required to fetch credentials. Keys in this list must be unique. |
| `spec.documents[].config.machine.kubelet.credentialProviderConfig.providers[].tokenAttributes.requireServiceAccount` **(deprecated)** | `boolean` | yes | requireServiceAccount indicates whether the plugin requires the pod to have a service account. If set to true, kubelet will only invoke the plugin if the pod has a service account. If set to false, kubelet will invoke the plugin even if the pod does not have a service account and will not include a token in the CredentialProviderRequest in that scenario. This is useful for plugins that are used to pull images for pods without service accounts (e.g., static pods). |
| `spec.documents[].config.machine.kubelet.credentialProviderConfig.providers[].tokenAttributes.requiredServiceAccountAnnotationKeys` **(deprecated)** | `[]string` | no | requiredServiceAccountAnnotationKeys is the list of annotation keys that the plugin is interested in and that are required to be present in the service account. The keys defined in this list will be extracted from the corresponding service account and passed to the plugin as part of the CredentialProviderRequest. If any of the keys defined in this list are not present in the service account, kubelet will not invoke the plugin and will return an error. This field is optional and may be empty. Plugins may use this field to extract additional information required to fetch credentials or allow workloads to opt in to using service account tokens for image pull. If non-empty, requireServiceAccount must be set to true. Keys in this list must be unique. This list needs to be mutually exclusive with optionalServiceAccountAnnotationKeys. |
| `spec.documents[].config.machine.kubelet.credentialProviderConfig.providers[].tokenAttributes.serviceAccountTokenAudience` **(deprecated)** | `string` | yes | serviceAccountTokenAudience is the intended audience for the projected service account token. |
| `spec.documents[].config.machine.kubelet.defaultRuntimeSeccompProfileEnabled` **(deprecated)** | `boolean` | no | docgen:nodoc Deprecated: use `KubeletConfig` instead. |
| `spec.documents[].config.machine.kubelet.disableManifestsDirectory` **(deprecated)** | `boolean` | no | docgen:nodoc Deprecated: locked to true in multi-doc config. |
| `spec.documents[].config.machine.kubelet.extraArgs` **(deprecated)** | `map[string][]string` | no | docgen:nodoc Deprecated: use `KubeletConfig` instead. |
| `spec.documents[].config.machine.kubelet.extraMounts` **(deprecated)** | `[]object` | no | docgen:nodoc Deprecated: removed in multi-doc config. |
| `spec.documents[].config.machine.kubelet.extraMounts[].destination` **(deprecated)** | `string` | yes | Destination is the absolute path where the mount will be placed in the container. |
| `spec.documents[].config.machine.kubelet.extraMounts[].gidMappings` **(deprecated)** | `[]object` | no | UID/GID mappings used for changing file owners w/o calling chown, fs should support it. Every mount point could have its own mapping. |
| `spec.documents[].config.machine.kubelet.extraMounts[].gidMappings[].containerID` **(deprecated)** | `integer` | yes | ContainerID is the starting UID/GID in the container. |
| `spec.documents[].config.machine.kubelet.extraMounts[].gidMappings[].hostID` **(deprecated)** | `integer` | yes | HostID is the starting UID/GID on the host to be mapped to 'ContainerID'. |
| `spec.documents[].config.machine.kubelet.extraMounts[].gidMappings[].size` **(deprecated)** | `integer` | yes | Size is the number of IDs to be mapped. |
| `spec.documents[].config.machine.kubelet.extraMounts[].options` **(deprecated)** | `[]string` | no | Options are fstab style mount options. |
| `spec.documents[].config.machine.kubelet.extraMounts[].source` **(deprecated)** | `string` | no | Source specifies the source path of the mount. |
| `spec.documents[].config.machine.kubelet.extraMounts[].type` **(deprecated)** | `string` | no | Type specifies the mount kind. |
| `spec.documents[].config.machine.kubelet.extraMounts[].uidMappings` **(deprecated)** | `[]object` | no | UID/GID mappings used for changing file owners w/o calling chown, fs should support it. Every mount point could have its own mapping. |
| `spec.documents[].config.machine.kubelet.extraMounts[].uidMappings[].containerID` **(deprecated)** | `integer` | yes | ContainerID is the starting UID/GID in the container. |
| `spec.documents[].config.machine.kubelet.extraMounts[].uidMappings[].hostID` **(deprecated)** | `integer` | yes | HostID is the starting UID/GID on the host to be mapped to 'ContainerID'. |
| `spec.documents[].config.machine.kubelet.extraMounts[].uidMappings[].size` **(deprecated)** | `integer` | yes | Size is the number of IDs to be mapped. |
| `spec.documents[].config.machine.kubelet.image` **(deprecated)** | `string` | no | docgen:nodoc Deprecated: use `KubeletConfig` instead. |
| `spec.documents[].config.machine.kubelet.nodeIP` **(deprecated)** | `object` | no | docgen:nodoc Deprecated: use `KubeNodeConfig` instead. |
| `spec.documents[].config.machine.kubelet.nodeIP.validSubnets` **(deprecated)** | `[]string` | no | The `validSubnets` field configures the networks to pick kubelet node IP from. For dual stack configuration, there should be two subnets: one for IPv4, another for IPv6. IPs can be excluded from the list by using negative match with `!`, e.g `!10.0.0.0/8`. Negative subnet matches should be specified last to filter out IPs picked by positive matches. If not specified, node IP is picked based on cluster podCIDRs: IPv4/IPv6 address or both. |
| `spec.documents[].config.machine.kubelet.registerWithFQDN` **(deprecated)** | `boolean` | no | docgen:nodoc Deprecated: use `KubeNodeConfig` instead. |
| `spec.documents[].config.machine.kubelet.skipNodeRegistration` **(deprecated)** | `boolean` | no | docgen:nodoc Deprecated: use `KubeNodeConfig` instead. |
| `spec.documents[].config.machine.logging` | `object` | no | Configures the logging system. |
| `spec.documents[].config.machine.logging.destinations` | `[]object` | yes | Logging destination. |
| `spec.documents[].config.machine.logging.destinations[].endpoint` | `string` | yes | Where to send logs. Supported protocols are "tcp" and "udp". |
| `spec.documents[].config.machine.logging.destinations[].extraTags` | `map[string]string` | no | Extra tags (key-value) pairs to attach to every log message sent. |
| `spec.documents[].config.machine.logging.destinations[].format` | `string` | yes | Logs format. |
| `spec.documents[].config.machine.network` **(deprecated)** | `object` | no | docgen:nodoc Deprecated: All fields within NetworkConfig are deprecated. Use multi-document network config types instead: HostnameConfig, NetworkDeviceConfig, ResolverConfig, StaticHostConfig, KubeSpanConfig. |
| `spec.documents[].config.machine.network.disableSearchDomain` **(deprecated)** | `boolean` | no | docgen:nodoc Deprecated: Use `ResolverConfig` instead. |
| `spec.documents[].config.machine.network.extraHostEntries` **(deprecated)** | `[]object` | no | docgen:nodoc Deprecated: Use `StaticHostConfig` instead. |
| `spec.documents[].config.machine.network.extraHostEntries[].aliases` **(deprecated)** | `[]string` | yes | The host alias. |
| `spec.documents[].config.machine.network.extraHostEntries[].ip` **(deprecated)** | `string` | yes | The IP of the host. |
| `spec.documents[].config.machine.network.hostname` **(deprecated)** | `string` | no | docgen:nodoc Deprecated: use `HostnameConfig` instead. |
| `spec.documents[].config.machine.network.interfaces` **(deprecated)** | `[]object` | no | docgen:nodoc Deprecated: use multi-doc network config. |
| `spec.documents[].config.machine.network.interfaces[].addresses` **(deprecated)** | `[]string` | no | Assigns static IP addresses to the interface. An address can be specified either in proper CIDR notation or as a standalone address (netmask of all ones is assumed). |
| `spec.documents[].config.machine.network.interfaces[].bond` **(deprecated)** | `object` | no | Bond specific options. |
| `spec.documents[].config.machine.network.interfaces[].bond.adActorSysPrio` **(deprecated)** | `integer` | no | A bond option. Please see the official kernel documentation. |
| `spec.documents[].config.machine.network.interfaces[].bond.adActorSystem` **(deprecated)** | `string` | no | A bond option. Please see the official kernel documentation. Not supported at the moment. |
| `spec.documents[].config.machine.network.interfaces[].bond.adSelect` **(deprecated)** | `string` | no | A bond option. Please see the official kernel documentation. |
| `spec.documents[].config.machine.network.interfaces[].bond.adUserPortKey` **(deprecated)** | `integer` | no | A bond option. Please see the official kernel documentation. |
| `spec.documents[].config.machine.network.interfaces[].bond.allSlavesActive` **(deprecated)** | `integer` | no | A bond option. Please see the official kernel documentation. |
| `spec.documents[].config.machine.network.interfaces[].bond.arpAllTargets` **(deprecated)** | `string` | no | A bond option. Please see the official kernel documentation. |
| `spec.documents[].config.machine.network.interfaces[].bond.arpIPTarget` **(deprecated)** | `[]string` | no | A bond option. Please see the official kernel documentation. Not supported at the moment. |
| `spec.documents[].config.machine.network.interfaces[].bond.arpInterval` **(deprecated)** | `integer` | no | A bond option. Please see the official kernel documentation. |
| `spec.documents[].config.machine.network.interfaces[].bond.arpValidate` **(deprecated)** | `string` | no | A bond option. Please see the official kernel documentation. |
| `spec.documents[].config.machine.network.interfaces[].bond.deviceSelectors` **(deprecated)** | `[]object` | no | Picks a network device using the selector. Mutually exclusive with `interfaces`. Supports partial match using wildcard syntax. |
| `spec.documents[].config.machine.network.interfaces[].bond.deviceSelectors[].busPath` **(deprecated)** | `string` | no | PCI, USB bus prefix, supports matching by wildcard. |
| `spec.documents[].config.machine.network.interfaces[].bond.deviceSelectors[].driver` **(deprecated)** | `string` | no | Kernel driver, supports matching by wildcard. |
| `spec.documents[].config.machine.network.interfaces[].bond.deviceSelectors[].hardwareAddr` **(deprecated)** | `string` | no | Device hardware (MAC) address, supports matching by wildcard. |
| `spec.documents[].config.machine.network.interfaces[].bond.deviceSelectors[].pciID` **(deprecated)** | `string` | no | PCI ID (vendor ID, product ID), supports matching by wildcard. |
| `spec.documents[].config.machine.network.interfaces[].bond.deviceSelectors[].permanentAddr` **(deprecated)** | `string` | no | Device permanent hardware address, supports matching by wildcard. The permanent address doesn't change when the link is enslaved to a bond, so it's recommended to use this field for bond members. |
| `spec.documents[].config.machine.network.interfaces[].bond.deviceSelectors[].physical` **(deprecated)** | `boolean` | no | Select only physical devices. |
| `spec.documents[].config.machine.network.interfaces[].bond.downdelay` **(deprecated)** | `integer` | no | A bond option. Please see the official kernel documentation. |
| `spec.documents[].config.machine.network.interfaces[].bond.failOverMac` **(deprecated)** | `string` | no | A bond option. Please see the official kernel documentation. |
| `spec.documents[].config.machine.network.interfaces[].bond.interfaces` **(deprecated)** | `[]string` | yes | The interfaces that make up the bond. |
| `spec.documents[].config.machine.network.interfaces[].bond.lacpRate` **(deprecated)** | `string` | no | A bond option. Please see the official kernel documentation. |
| `spec.documents[].config.machine.network.interfaces[].bond.lpInterval` **(deprecated)** | `integer` | no | A bond option. Please see the official kernel documentation. |
| `spec.documents[].config.machine.network.interfaces[].bond.miimon` **(deprecated)** | `integer` | no | A bond option. Please see the official kernel documentation. |
| `spec.documents[].config.machine.network.interfaces[].bond.minLinks` **(deprecated)** | `integer` | no | A bond option. Please see the official kernel documentation. |
| `spec.documents[].config.machine.network.interfaces[].bond.mode` **(deprecated)** | `string` | yes | A bond option. Please see the official kernel documentation. |
| `spec.documents[].config.machine.network.interfaces[].bond.numPeerNotif` **(deprecated)** | `integer` | no | A bond option. Please see the official kernel documentation. |
| `spec.documents[].config.machine.network.interfaces[].bond.packetsPerSlave` **(deprecated)** | `integer` | no | A bond option. Please see the official kernel documentation. |
| `spec.documents[].config.machine.network.interfaces[].bond.peerNotifyDelay` **(deprecated)** | `integer` | no | A bond option. Please see the official kernel documentation. |
| `spec.documents[].config.machine.network.interfaces[].bond.primary` **(deprecated)** | `string` | no | A bond option. Please see the official kernel documentation. |
| `spec.documents[].config.machine.network.interfaces[].bond.primaryReselect` **(deprecated)** | `string` | no | A bond option. Please see the official kernel documentation. |
| `spec.documents[].config.machine.network.interfaces[].bond.resendIgmp` **(deprecated)** | `integer` | no | A bond option. Please see the official kernel documentation. |
| `spec.documents[].config.machine.network.interfaces[].bond.tlbDynamicLb` **(deprecated)** | `integer` | no | A bond option. Please see the official kernel documentation. |
| `spec.documents[].config.machine.network.interfaces[].bond.updelay` **(deprecated)** | `integer` | no | A bond option. Please see the official kernel documentation. |
| `spec.documents[].config.machine.network.interfaces[].bond.useCarrier` **(deprecated)** | `boolean` | no | A bond option. Please see the official kernel documentation. |
| `spec.documents[].config.machine.network.interfaces[].bond.xmitHashPolicy` **(deprecated)** | `string` | no | A bond option. Please see the official kernel documentation. |
| `spec.documents[].config.machine.network.interfaces[].bridge` **(deprecated)** | `object` | no | Bridge specific options. |
| `spec.documents[].config.machine.network.interfaces[].bridge.interfaces` **(deprecated)** | `[]string` | yes | The interfaces that make up the bridge. |
| `spec.documents[].config.machine.network.interfaces[].bridge.stp` **(deprecated)** | `object` | no | Enable STP on this bridge. Please see the official kernel documentation. |
| `spec.documents[].config.machine.network.interfaces[].bridge.stp.enabled` **(deprecated)** | `boolean` | no | Whether Spanning Tree Protocol (STP) is enabled. |
| `spec.documents[].config.machine.network.interfaces[].bridge.vlan` **(deprecated)** | `object` | no | Enable VLAN-awareness on this bridge. Please see the official kernel documentation. |
| `spec.documents[].config.machine.network.interfaces[].bridge.vlan.vlanFiltering` **(deprecated)** | `boolean` | no | Whether VLAN filtering is enabled. |
| `spec.documents[].config.machine.network.interfaces[].bridgePort` **(deprecated)** | `object` | no | Configure this device as a bridge port. This can be used to dynamically assign network interfaces to a bridge. |
| `spec.documents[].config.machine.network.interfaces[].bridgePort.master` **(deprecated)** | `string` | no | The name of the bridge master interface |
| `spec.documents[].config.machine.network.interfaces[].cidr` **(deprecated)** | `string` | no | docgen:nodoc |
| `spec.documents[].config.machine.network.interfaces[].deviceSelector` **(deprecated)** | `object` | no | Picks a network device using the selector. Mutually exclusive with `interface`. Supports partial match using wildcard syntax. |
| `spec.documents[].config.machine.network.interfaces[].deviceSelector.busPath` **(deprecated)** | `string` | no | PCI, USB bus prefix, supports matching by wildcard. |
| `spec.documents[].config.machine.network.interfaces[].deviceSelector.driver` **(deprecated)** | `string` | no | Kernel driver, supports matching by wildcard. |
| `spec.documents[].config.machine.network.interfaces[].deviceSelector.hardwareAddr` **(deprecated)** | `string` | no | Device hardware (MAC) address, supports matching by wildcard. |
| `spec.documents[].config.machine.network.interfaces[].deviceSelector.pciID` **(deprecated)** | `string` | no | PCI ID (vendor ID, product ID), supports matching by wildcard. |
| `spec.documents[].config.machine.network.interfaces[].deviceSelector.permanentAddr` **(deprecated)** | `string` | no | Device permanent hardware address, supports matching by wildcard. The permanent address doesn't change when the link is enslaved to a bond, so it's recommended to use this field for bond members. |
| `spec.documents[].config.machine.network.interfaces[].deviceSelector.physical` **(deprecated)** | `boolean` | no | Select only physical devices. |
| `spec.documents[].config.machine.network.interfaces[].dhcp` **(deprecated)** | `boolean` | no | Indicates if DHCP should be used to configure the interface. The following DHCP options are supported: - `OptionClasslessStaticRoute` - `OptionDomainNameServer` - `OptionDNSDomainSearchList` - `OptionHostName` |
| `spec.documents[].config.machine.network.interfaces[].dhcpOptions` **(deprecated)** | `object` | no | DHCP specific options. `dhcp` *must* be set to true for these to take effect. |
| `spec.documents[].config.machine.network.interfaces[].dhcpOptions.duidv6` **(deprecated)** | `string` | no | Set client DUID (hex string). |
| `spec.documents[].config.machine.network.interfaces[].dhcpOptions.ipv4` **(deprecated)** | `boolean` | no | Enables DHCPv4 protocol for the interface (default is enabled). |
| `spec.documents[].config.machine.network.interfaces[].dhcpOptions.ipv6` **(deprecated)** | `boolean` | no | Enables DHCPv6 protocol for the interface (default is disabled). |
| `spec.documents[].config.machine.network.interfaces[].dhcpOptions.routeMetric` **(deprecated)** | `integer` | yes | The priority of all routes received via DHCP. |
| `spec.documents[].config.machine.network.interfaces[].dummy` **(deprecated)** | `boolean` | no | Indicates if the interface is a dummy interface. `dummy` is used to specify that this interface should be a virtual-only, dummy interface. |
| `spec.documents[].config.machine.network.interfaces[].ignore` **(deprecated)** | `boolean` | no | Indicates if the interface should be ignored (skips configuration). |
| `spec.documents[].config.machine.network.interfaces[].interface` **(deprecated)** | `string` | no | The interface name. Mutually exclusive with `deviceSelector`. |
| `spec.documents[].config.machine.network.interfaces[].mtu` **(deprecated)** | `integer` | no | The interface's MTU. If used in combination with DHCP, this will override any MTU settings returned from DHCP server. |
| `spec.documents[].config.machine.network.interfaces[].routes` **(deprecated)** | `[]object` | no | A list of routes associated with the interface. If used in combination with DHCP, these routes will be appended to routes returned by DHCP server. |
| `spec.documents[].config.machine.network.interfaces[].routes[].gateway` **(deprecated)** | `string` | yes | The route's gateway (if empty, creates link scope route). |
| `spec.documents[].config.machine.network.interfaces[].routes[].metric` **(deprecated)** | `integer` | no | The optional metric for the route. |
| `spec.documents[].config.machine.network.interfaces[].routes[].mtu` **(deprecated)** | `integer` | no | The optional MTU for the route. |
| `spec.documents[].config.machine.network.interfaces[].routes[].network` **(deprecated)** | `string` | yes | The route's network (destination). |
| `spec.documents[].config.machine.network.interfaces[].routes[].source` **(deprecated)** | `string` | no | The route's source address (optional). |
| `spec.documents[].config.machine.network.interfaces[].vip` **(deprecated)** | `object` | no | Virtual (shared) IP address configuration. |
| `spec.documents[].config.machine.network.interfaces[].vip.equinixMetal` **(deprecated)** | `object` | no | Specifies the Equinix Metal API settings to assign VIP to the node. |
| `spec.documents[].config.machine.network.interfaces[].vip.hcloud` **(deprecated)** | `object` | no | Specifies the Hetzner Cloud API settings to assign VIP to the node. |
| `spec.documents[].config.machine.network.interfaces[].vip.ip` **(deprecated)** | `string` | no | Specifies the IP address to be used. |
| `spec.documents[].config.machine.network.interfaces[].vlans` **(deprecated)** | `[]object` | no | VLAN specific options. |
| `spec.documents[].config.machine.network.interfaces[].vlans[].addresses` **(deprecated)** | `[]string` | no | The addresses in CIDR notation or as plain IPs to use. |
| `spec.documents[].config.machine.network.interfaces[].vlans[].cidr` **(deprecated)** | `string` | no | docgen:nodoc |
| `spec.documents[].config.machine.network.interfaces[].vlans[].dhcp` **(deprecated)** | `boolean` | no | Indicates if DHCP should be used. |
| `spec.documents[].config.machine.network.interfaces[].vlans[].dhcpOptions` **(deprecated)** | `object` | no | DHCP specific options. `dhcp` *must* be set to true for these to take effect. |
| `spec.documents[].config.machine.network.interfaces[].vlans[].dhcpOptions.duidv6` **(deprecated)** | `string` | no | Set client DUID (hex string). |
| `spec.documents[].config.machine.network.interfaces[].vlans[].dhcpOptions.ipv4` **(deprecated)** | `boolean` | no | Enables DHCPv4 protocol for the interface (default is enabled). |
| `spec.documents[].config.machine.network.interfaces[].vlans[].dhcpOptions.ipv6` **(deprecated)** | `boolean` | no | Enables DHCPv6 protocol for the interface (default is disabled). |
| `spec.documents[].config.machine.network.interfaces[].vlans[].dhcpOptions.routeMetric` **(deprecated)** | `integer` | yes | The priority of all routes received via DHCP. |
| `spec.documents[].config.machine.network.interfaces[].vlans[].mtu` **(deprecated)** | `integer` | no | The VLAN's MTU. |
| `spec.documents[].config.machine.network.interfaces[].vlans[].routes` **(deprecated)** | `[]object` | yes | A list of routes associated with the VLAN. |
| `spec.documents[].config.machine.network.interfaces[].vlans[].routes[].gateway` **(deprecated)** | `string` | yes | The route's gateway (if empty, creates link scope route). |
| `spec.documents[].config.machine.network.interfaces[].vlans[].routes[].metric` **(deprecated)** | `integer` | no | The optional metric for the route. |
| `spec.documents[].config.machine.network.interfaces[].vlans[].routes[].mtu` **(deprecated)** | `integer` | no | The optional MTU for the route. |
| `spec.documents[].config.machine.network.interfaces[].vlans[].routes[].network` **(deprecated)** | `string` | yes | The route's network (destination). |
| `spec.documents[].config.machine.network.interfaces[].vlans[].routes[].source` **(deprecated)** | `string` | no | The route's source address (optional). |
| `spec.documents[].config.machine.network.interfaces[].vlans[].vip` **(deprecated)** | `object` | no | The VLAN's virtual IP address configuration. |
| `spec.documents[].config.machine.network.interfaces[].vlans[].vip.equinixMetal` **(deprecated)** | `object` | no | Specifies the Equinix Metal API settings to assign VIP to the node. |
| `spec.documents[].config.machine.network.interfaces[].vlans[].vip.hcloud` **(deprecated)** | `object` | no | Specifies the Hetzner Cloud API settings to assign VIP to the node. |
| `spec.documents[].config.machine.network.interfaces[].vlans[].vip.ip` **(deprecated)** | `string` | no | Specifies the IP address to be used. |
| `spec.documents[].config.machine.network.interfaces[].vlans[].vlanId` **(deprecated)** | `integer` | yes | The VLAN's ID. |
| `spec.documents[].config.machine.network.interfaces[].wireguard` **(deprecated)** | `object` | no | Wireguard specific configuration. Includes things like private key, listen port, peers. |
| `spec.documents[].config.machine.network.interfaces[].wireguard.firewallMark` **(deprecated)** | `integer` | no | Specifies a device's firewall mark. |
| `spec.documents[].config.machine.network.interfaces[].wireguard.listenPort` **(deprecated)** | `integer` | no | Specifies a device's listening port. |
| `spec.documents[].config.machine.network.interfaces[].wireguard.peers` **(deprecated)** | `[]object` | no | Specifies a list of peer configurations to apply to a device. |
| `spec.documents[].config.machine.network.interfaces[].wireguard.peers[].allowedIPs` **(deprecated)** | `[]string` | no | AllowedIPs specifies a list of allowed IP addresses in CIDR notation for this peer. |
| `spec.documents[].config.machine.network.interfaces[].wireguard.peers[].endpoint` **(deprecated)** | `string` | no | Specifies the endpoint of this peer entry. |
| `spec.documents[].config.machine.network.interfaces[].wireguard.peers[].persistentKeepaliveInterval` **(deprecated)** | `string` | no | Specifies the persistent keepalive interval for this peer. Field format accepts any Go time.Duration format ('1h' for one hour, '10m' for ten minutes). |
| `spec.documents[].config.machine.network.interfaces[].wireguard.peers[].publicKey` **(deprecated)** | `string` | no | Specifies the public key of this peer. Can be extracted from private key by running `wg pubkey \< private.key > public.key && cat public.key`. |
| `spec.documents[].config.machine.network.kubespan` **(deprecated)** | `object` | no | docgen:nodoc Deprecated: Use `KubeSpanConfig` document instead. |
| `spec.documents[].config.machine.network.kubespan.advertiseKubernetesNetworks` **(deprecated)** | `boolean` | no | Control whether Kubernetes pod CIDRs are announced over KubeSpan from the node. If disabled, CNI handles encapsulating pod-to-pod traffic into some node-to-node tunnel, and KubeSpan handles the node-to-node traffic. If enabled, KubeSpan will take over pod-to-pod traffic and send it over KubeSpan directly. When enabled, KubeSpan should have a way to detect complete pod CIDRs of the node which is not always the case with CNIs not relying on Kubernetes for IPAM. |
| `spec.documents[].config.machine.network.kubespan.allowDownPeerBypass` **(deprecated)** | `boolean` | no | Skip sending traffic via KubeSpan if the peer connection state is not up. This provides configurable choice between connectivity and security: either traffic is always forced to go via KubeSpan (even if Wireguard peer connection is not up), or traffic can go directly to the peer if Wireguard connection can't be established. |
| `spec.documents[].config.machine.network.kubespan.enabled` **(deprecated)** | `boolean` | no | Enable the KubeSpan feature. Cluster discovery should be enabled with .cluster.discovery.enabled for KubeSpan to be enabled. |
| `spec.documents[].config.machine.network.kubespan.filters` **(deprecated)** | `object` | no | KubeSpan advanced filtering of network addresses . Settings in this section are optional, and settings apply only to the node. |
| `spec.documents[].config.machine.network.kubespan.filters.endpoints` **(deprecated)** | `[]string` | no | Filter node addresses which will be advertised as KubeSpan endpoints for peer-to-peer Wireguard connections. By default, all addresses are advertised, and KubeSpan cycles through all endpoints until it finds one that works. Default value: no filtering. |
| `spec.documents[].config.machine.network.kubespan.filters.excludeAdvertisedNetworks` **(deprecated)** | `[]string` | no | Filter networks (e.g., host addresses, pod CIDRs if enabled) which will be advertised over KubeSpan. By default, all networks are advertised. Use this filter to exclude some networks from being advertised. Note: excluded networks will not be reachable over KubeSpan, so make sure these networks are still reachable via some other route (e.g., direct connection). Default value: no filtering. |
| `spec.documents[].config.machine.network.kubespan.harvestExtraEndpoints` **(deprecated)** | `boolean` | no | KubeSpan can collect and publish extra endpoints for each member of the cluster based on Wireguard endpoint information for each peer. This feature is disabled by default, don't enable it with high number of peers (>50) in the KubeSpan network (performance issues). |
| `spec.documents[].config.machine.network.kubespan.mtu` **(deprecated)** | `integer` | no | KubeSpan link MTU size. Default value is 1420. |
| `spec.documents[].config.machine.network.nameservers` **(deprecated)** | `[]string` | no | docgen:nodoc Deprecated: Use `ResolverConfig` instead. |
| `spec.documents[].config.machine.network.searchDomains` **(deprecated)** | `[]string` | no | docgen:nodoc Deprecated: Use `ResolverConfig` instead. |
| `spec.documents[].config.machine.nodeAnnotations` **(deprecated)** | `map[string]string` | no | docgen:nodoc Deprecated: use `KubeNodeConfig` instead. |
| `spec.documents[].config.machine.nodeLabels` **(deprecated)** | `map[string]string` | no | docgen:nodoc Deprecated: use `KubeNodeConfig` instead. |
| `spec.documents[].config.machine.nodeTaints` **(deprecated)** | `map[string]string` | no | docgen:nodoc Deprecated: use `KubeNodeConfig` instead. |
| `spec.documents[].config.machine.registries` **(deprecated)** | `object` | no | docgen:nodoc Deprecated: Use `Registry*Config` instead. |
| `spec.documents[].config.machine.registries.config` **(deprecated)** | `map[string]object` | no | Specifies TLS & auth configuration for HTTPS image registries. Mutual TLS can be enabled with 'clientIdentity' option. The full hostname and port (if not using a default port 443) should be used as the key. The fallback key `*` can't be used for TLS configuration. TLS configuration can be skipped if registry has trusted server certificate. |
| `spec.documents[].config.machine.registries.config.<key>.auth` **(deprecated)** | `object` | no | The auth configuration for this registry. Note: changes to the registry auth will not be picked up by the CRI containerd plugin without a reboot. |
| `spec.documents[].config.machine.registries.config.<key>.auth.username` **(deprecated)** | `string` | no | Optional registry authentication. The meaning of each field is the same with the corresponding field in [`.docker/config.json`](https://docs.docker.com/engine/api/v1.41/#section/Authentication). |
| `spec.documents[].config.machine.registries.config.<key>.tls` **(deprecated)** | `object` | no | The TLS configuration for the registry. |
| `spec.documents[].config.machine.registries.config.<key>.tls.ca` **(deprecated)** | `string (byte)` | no | CA registry certificate to add the list of trusted certificates. Certificate should be base64-encoded. |
| `spec.documents[].config.machine.registries.config.<key>.tls.clientIdentity` **(deprecated)** | `object` | no | Enable mutual TLS authentication with the registry. Client certificate and key should be base64-encoded. |
| `spec.documents[].config.machine.registries.config.<key>.tls.clientIdentity.crt` **(deprecated)** | `string (byte)` | yes |  |
| `spec.documents[].config.machine.registries.config.<key>.tls.clientIdentity.key` **(deprecated)** | `string (byte)` | yes |  |
| `spec.documents[].config.machine.registries.config.<key>.tls.insecureSkipVerify` **(deprecated)** | `boolean` | no | Skip TLS server certificate verification (not recommended). |
| `spec.documents[].config.machine.registries.mirrors` **(deprecated)** | `map[string]object` | no | Specifies mirror configuration for each registry host namespace. This setting allows to configure local pull-through caching registires, air-gapped installations, etc. For example, when pulling an image with the reference `example.com:123/image:v1`, the `example.com:123` key will be used to lookup the mirror configuration. Optionally the `*` key can be used to configure a fallback mirror. Registry name is the first segment of image identifier, with 'docker.io' being default one. |
| `spec.documents[].config.machine.registries.mirrors.<key>.endpoints` **(deprecated)** | `[]string` | yes | List of endpoints (URLs) for registry mirrors to use. Endpoint configures HTTP/HTTPS access mode, host name, port and path (if path is not set, it defaults to `/v2`). |
| `spec.documents[].config.machine.registries.mirrors.<key>.overridePath` **(deprecated)** | `boolean` | no | Use the exact path specified for the endpoint (don't append /v2/). This setting is often required for setting up multiple mirrors on a single instance of a registry. |
| `spec.documents[].config.machine.registries.mirrors.<key>.skipFallback` **(deprecated)** | `boolean` | no | Skip fallback to the upstream endpoint, for example the mirror configuration for `docker.io` will not fallback to `registry-1.docker.io`. |
| `spec.documents[].config.machine.seccompProfiles` | `[]object` | no | Configures the seccomp profiles for the machine. |
| `spec.documents[].config.machine.seccompProfiles[].name` | `string` | yes | The `name` field is used to provide the file name of the seccomp profile. |
| `spec.documents[].config.machine.seccompProfiles[].value` | `object` | yes | The `value` field is used to provide the seccomp profile. |
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
| `spec.documents[].config.machine.sysctls` **(deprecated)** | `map[string]string` | no | docgen:nodoc Deprecated: Use 'SysctlConfig' instead. |
| `spec.documents[].config.machine.sysfs` **(deprecated)** | `map[string]string` | no | docgen:nodoc Deprecated: Use 'SysfsConfig' instead. |
| `spec.documents[].config.machine.systemDiskEncryption` **(deprecated)** | `object` | no | docgen:nodoc Deprecated: Use `VolumeConfig` instead. |
| `spec.documents[].config.machine.systemDiskEncryption.ephemeral` **(deprecated)** | `object` | no | Ephemeral partition encryption. |
| `spec.documents[].config.machine.systemDiskEncryption.ephemeral.blockSize` **(deprecated)** | `integer` | no | Defines the encryption sector size. |
| `spec.documents[].config.machine.systemDiskEncryption.ephemeral.cipher` **(deprecated)** | `string` | no | Cipher kind to use for the encryption. Depends on the encryption provider. |
| `spec.documents[].config.machine.systemDiskEncryption.ephemeral.keySize` **(deprecated)** | `integer` | no | Defines the encryption key length. |
| `spec.documents[].config.machine.systemDiskEncryption.ephemeral.keys` **(deprecated)** | `[]object` | yes | Defines the encryption keys generation and storage method. |
| `spec.documents[].config.machine.systemDiskEncryption.ephemeral.keys[].kms` **(deprecated)** | `object` | no | KMS managed encryption key. |
| `spec.documents[].config.machine.systemDiskEncryption.ephemeral.keys[].kms.endpoint` **(deprecated)** | `string` | yes | KMS endpoint to Seal/Unseal the key. The endpoint should be specified as a URL with an explicit scheme, e.g. `https://kms.example.com:4443`. With the `https://` scheme, the connection to the KMS server is established over TLS, and the server certificate is verified against the trusted root CAs. WARNING: if the scheme is omitted (e.g. `kms.example.com:4443`) or set to `grpc://`, the connection to the KMS server is established without TLS, and the disk encryption key material is transmitted in plaintext. Use plaintext endpoints only on fully trusted networks. Note: when encrypting the `STATE` volume, custom trusted root CAs from the machine configuration might not be available when unlocking the volume (as the machine configuration is stored on `STATE`), so the KMS server certificate should be signed by a CA trusted by default. |
| `spec.documents[].config.machine.systemDiskEncryption.ephemeral.keys[].nodeID` **(deprecated)** | `object` | no | Deterministically generated key from the node UUID and PartitionLabel. |
| `spec.documents[].config.machine.systemDiskEncryption.ephemeral.keys[].slot` **(deprecated)** | `integer` | yes | Key slot number for LUKS2 encryption. |
| `spec.documents[].config.machine.systemDiskEncryption.ephemeral.keys[].tpm` **(deprecated)** | `object` | no | Enable TPM based disk encryption. |
| `spec.documents[].config.machine.systemDiskEncryption.ephemeral.keys[].tpm.checkSecurebootStatusOnEnroll` **(deprecated)** | `boolean` | no | Check that Secureboot is enabled in the EFI firmware. If Secureboot is not enabled, the enrollment of the key will fail. As the TPM key is anyways bound to the value of PCR 7, changing Secureboot status or configuration after the initial enrollment will make the key unusable. |
| `spec.documents[].config.machine.systemDiskEncryption.ephemeral.options` **(deprecated)** | `[]string` | no | Additional --perf parameters for the LUKS2 encryption. |
| `spec.documents[].config.machine.systemDiskEncryption.ephemeral.provider` **(deprecated)** | `string` | yes | Encryption provider to use for the encryption. |
| `spec.documents[].config.machine.systemDiskEncryption.state` **(deprecated)** | `object` | no | State partition encryption. |
| `spec.documents[].config.machine.systemDiskEncryption.state.blockSize` **(deprecated)** | `integer` | no | Defines the encryption sector size. |
| `spec.documents[].config.machine.systemDiskEncryption.state.cipher` **(deprecated)** | `string` | no | Cipher kind to use for the encryption. Depends on the encryption provider. |
| `spec.documents[].config.machine.systemDiskEncryption.state.keySize` **(deprecated)** | `integer` | no | Defines the encryption key length. |
| `spec.documents[].config.machine.systemDiskEncryption.state.keys` **(deprecated)** | `[]object` | yes | Defines the encryption keys generation and storage method. |
| `spec.documents[].config.machine.systemDiskEncryption.state.keys[].kms` **(deprecated)** | `object` | no | KMS managed encryption key. |
| `spec.documents[].config.machine.systemDiskEncryption.state.keys[].kms.endpoint` **(deprecated)** | `string` | yes | KMS endpoint to Seal/Unseal the key. The endpoint should be specified as a URL with an explicit scheme, e.g. `https://kms.example.com:4443`. With the `https://` scheme, the connection to the KMS server is established over TLS, and the server certificate is verified against the trusted root CAs. WARNING: if the scheme is omitted (e.g. `kms.example.com:4443`) or set to `grpc://`, the connection to the KMS server is established without TLS, and the disk encryption key material is transmitted in plaintext. Use plaintext endpoints only on fully trusted networks. Note: when encrypting the `STATE` volume, custom trusted root CAs from the machine configuration might not be available when unlocking the volume (as the machine configuration is stored on `STATE`), so the KMS server certificate should be signed by a CA trusted by default. |
| `spec.documents[].config.machine.systemDiskEncryption.state.keys[].nodeID` **(deprecated)** | `object` | no | Deterministically generated key from the node UUID and PartitionLabel. |
| `spec.documents[].config.machine.systemDiskEncryption.state.keys[].slot` **(deprecated)** | `integer` | yes | Key slot number for LUKS2 encryption. |
| `spec.documents[].config.machine.systemDiskEncryption.state.keys[].tpm` **(deprecated)** | `object` | no | Enable TPM based disk encryption. |
| `spec.documents[].config.machine.systemDiskEncryption.state.keys[].tpm.checkSecurebootStatusOnEnroll` **(deprecated)** | `boolean` | no | Check that Secureboot is enabled in the EFI firmware. If Secureboot is not enabled, the enrollment of the key will fail. As the TPM key is anyways bound to the value of PCR 7, changing Secureboot status or configuration after the initial enrollment will make the key unusable. |
| `spec.documents[].config.machine.systemDiskEncryption.state.options` **(deprecated)** | `[]string` | no | Additional --perf parameters for the LUKS2 encryption. |
| `spec.documents[].config.machine.systemDiskEncryption.state.provider` **(deprecated)** | `string` | yes | Encryption provider to use for the encryption. |
| `spec.documents[].config.machine.time` **(deprecated)** | `object` | no | docgen:nodoc Deprecated: Use 'TimeSyncConfig' instead. |
| `spec.documents[].config.machine.time.bootTimeout` **(deprecated)** | `string` | no | Specifies the timeout when the node time is considered to be in sync unlocking the boot sequence. NTP sync will be still running in the background. Defaults to "infinity" (waiting forever for time sync) |
| `spec.documents[].config.machine.time.disabled` **(deprecated)** | `boolean` | no | Indicates if the time service is disabled for the machine. Defaults to `false`. |
| `spec.documents[].config.machine.time.servers` **(deprecated)** | `[]string` | no | Specifies time (NTP) servers to use for setting the system time. Defaults to `time.cloudflare.com`. Talos can also sync to the PTP time source (e.g provided by the hypervisor), provide the path to the PTP device as "/dev/ptp0" or "/dev/ptp_kvm". |
| `spec.documents[].config.machine.type` **(deprecated)** | `string` | no | Defines the role of the machine within the cluster. **Control Plane** Control Plane node type designates the node as a control plane member. This means it will host etcd along with the Kubernetes controlplane components such as API Server, Controller Manager, Scheduler. **Worker** Worker node type designates the node as a worker node. This means it will be an available compute node for scheduling workloads. This node type was previously known as "join"; that value is still supported but deprecated. |
| `spec.documents[].config.machine.udev` | `object` | no | Configures the udev system. |
| `spec.documents[].config.machine.udev.rules` | `[]string` | no | List of udev rules to apply to the udev system |
| `spec.documents[].config.persist` **(deprecated)** | `boolean` | no | docgen:nodoc Deprecated: Not supported anymore. |
| `spec.documents[].config.version` | `string` | yes | Indicates the schema used to decode the contents. |
| `spec.documents[].kernelModuleConfig` | `object` | no | kernelModuleConfig is a Talos KernelModuleConfig document; populated when kind is "KernelModuleConfig". |
| `spec.documents[].kernelModuleConfig.name` | `string` | yes | Module name. |
| `spec.documents[].kernelModuleConfig.parameters` | `[]string` | no | Module parameters, changes applied after reboot. |
| `spec.documents[].kind` | `string` | yes | kind selects which of the fields below is populated. Allowed values: `SysctlConfig`, `KernelModuleConfig`, `Config`, `VolumeConfig`, `UserVolumeConfig`, `RawVolumeConfig`. |
| `spec.documents[].rawVolumeConfig` | `object` | no | rawVolumeConfig is a Talos RawVolumeConfig document (an unformatted partition for a CSI provisioner); populated when kind is "RawVolumeConfig". |
| `spec.documents[].rawVolumeConfig.encryption` | `object` | no | The encryption describes how the volume is encrypted. |
| `spec.documents[].rawVolumeConfig.encryption.allowDiscards` | `boolean` | no | Allow TRIM/discard requests to be passed through to the underlying device when the encrypted volume is opened. Defaults to false. |
| `spec.documents[].rawVolumeConfig.encryption.blockSize` | `integer` | no | Defines the encryption sector size. |
| `spec.documents[].rawVolumeConfig.encryption.cipher` | `string` | no | Cipher to use for the encryption. Depends on the encryption provider. |
| `spec.documents[].rawVolumeConfig.encryption.keySize` | `integer` | no | Defines the encryption key length. |
| `spec.documents[].rawVolumeConfig.encryption.keys` | `[]object` | yes | Defines the encryption keys generation and storage method. |
| `spec.documents[].rawVolumeConfig.encryption.keys[].kms` | `object` | no | EncryptionKeyKMS represents a key that is generated and then sealed/unsealed by the KMS server. |
| `spec.documents[].rawVolumeConfig.encryption.keys[].kms.endpoint` | `string` | yes |  |
| `spec.documents[].rawVolumeConfig.encryption.keys[].lockToState` | `boolean` | no |  |
| `spec.documents[].rawVolumeConfig.encryption.keys[].nodeID` | `object` | no | EncryptionKeyNodeID represents deterministically generated key from the node UUID and PartitionLabel. |
| `spec.documents[].rawVolumeConfig.encryption.keys[].slot` | `integer` | yes |  |
| `spec.documents[].rawVolumeConfig.encryption.keys[].tpm` | `object` | no | EncryptionKeyTPM represents a key that is generated and then sealed/unsealed by the TPM. |
| `spec.documents[].rawVolumeConfig.encryption.keys[].tpm.checkSecurebootStatusOnEnroll` | `boolean` | no |  |
| `spec.documents[].rawVolumeConfig.encryption.keys[].tpm.options` | `object` | no | EncryptionKeyTPMOptions represents the options for TPM-based key protection. |
| `spec.documents[].rawVolumeConfig.encryption.keys[].tpm.options.pcrs` | `[]integer` | no | List of PCRs to bind the key to. If not set, defaults to PCR 7, can be disabled by passing an empty list. |
| `spec.documents[].rawVolumeConfig.encryption.options` | `[]string` | no | Additional --perf parameters for the LUKS2 encryption. |
| `spec.documents[].rawVolumeConfig.encryption.provider` | `string` | yes | Encryption provider to use for the encryption. |
| `spec.documents[].rawVolumeConfig.name` | `string` | yes | Name of the volume. Name might be between 1 and 34 characters long and can only contain: lowercase and uppercase ASCII letters, digits, and hyphens. |
| `spec.documents[].rawVolumeConfig.provisioning` | `object` | no | The provisioning describes how the volume is provisioned. |
| `spec.documents[].rawVolumeConfig.provisioning.diskSelector` | `object` | no | The disk selector expression. |
| `spec.documents[].rawVolumeConfig.provisioning.diskSelector.match` | `string` | no | The Common Expression Language (CEL) expression to match the disk. |
| `spec.documents[].rawVolumeConfig.provisioning.grow` | `boolean` | no | Should the volume grow to the size of the disk (if possible). |
| `spec.documents[].rawVolumeConfig.provisioning.maxSize` | `string` | no | The maximum size of the volume, if not specified the volume can grow to the size of the disk. Size is specified in bytes or in percents. It can be expressed in human readable format, e.g. 100MB. |
| `spec.documents[].rawVolumeConfig.provisioning.minSize` | `string` | no | The minimum size of the volume. Size is specified in bytes, but can be expressed in human readable format, e.g. 100MB. |
| `spec.documents[].sysctlConfig` | `object` | no | sysctlConfig is a Talos SysctlConfig document; populated when kind is "SysctlConfig". |
| `spec.documents[].sysctlConfig.params` **(deprecated)** | `map[string]string` | yes | Used to configure the machine's sysctls (kernel parameters under `/proc/sys`). Values from this document are merged with the deprecated v1alpha1 machine.sysctls values (if set), with this document taking precedence on key conflicts. |
| `spec.documents[].userVolumeConfig` | `object` | no | userVolumeConfig is a Talos UserVolumeConfig document; populated when kind is "UserVolumeConfig". |
| `spec.documents[].userVolumeConfig.encryption` | `object` | no | The encryption describes how the volume is encrypted. |
| `spec.documents[].userVolumeConfig.encryption.allowDiscards` | `boolean` | no | Allow TRIM/discard requests to be passed through to the underlying device when the encrypted volume is opened. Defaults to false. |
| `spec.documents[].userVolumeConfig.encryption.blockSize` | `integer` | no | Defines the encryption sector size. |
| `spec.documents[].userVolumeConfig.encryption.cipher` | `string` | no | Cipher to use for the encryption. Depends on the encryption provider. |
| `spec.documents[].userVolumeConfig.encryption.keySize` | `integer` | no | Defines the encryption key length. |
| `spec.documents[].userVolumeConfig.encryption.keys` | `[]object` | yes | Defines the encryption keys generation and storage method. |
| `spec.documents[].userVolumeConfig.encryption.keys[].kms` | `object` | no | EncryptionKeyKMS represents a key that is generated and then sealed/unsealed by the KMS server. |
| `spec.documents[].userVolumeConfig.encryption.keys[].kms.endpoint` | `string` | yes |  |
| `spec.documents[].userVolumeConfig.encryption.keys[].lockToState` | `boolean` | no |  |
| `spec.documents[].userVolumeConfig.encryption.keys[].nodeID` | `object` | no | EncryptionKeyNodeID represents deterministically generated key from the node UUID and PartitionLabel. |
| `spec.documents[].userVolumeConfig.encryption.keys[].slot` | `integer` | yes |  |
| `spec.documents[].userVolumeConfig.encryption.keys[].tpm` | `object` | no | EncryptionKeyTPM represents a key that is generated and then sealed/unsealed by the TPM. |
| `spec.documents[].userVolumeConfig.encryption.keys[].tpm.checkSecurebootStatusOnEnroll` | `boolean` | no |  |
| `spec.documents[].userVolumeConfig.encryption.keys[].tpm.options` | `object` | no | EncryptionKeyTPMOptions represents the options for TPM-based key protection. |
| `spec.documents[].userVolumeConfig.encryption.keys[].tpm.options.pcrs` | `[]integer` | no | List of PCRs to bind the key to. If not set, defaults to PCR 7, can be disabled by passing an empty list. |
| `spec.documents[].userVolumeConfig.encryption.options` | `[]string` | no | Additional --perf parameters for the LUKS2 encryption. |
| `spec.documents[].userVolumeConfig.encryption.provider` | `string` | yes | Encryption provider to use for the encryption. |
| `spec.documents[].userVolumeConfig.filesystem` | `object` | no | The filesystem describes how the volume is formatted. |
| `spec.documents[].userVolumeConfig.filesystem.projectQuotaSupport` | `boolean` | no | Enables project quota support, valid only for 'xfs' filesystem. Note: changing this value might require a full remount of the filesystem. |
| `spec.documents[].userVolumeConfig.filesystem.type` | `string` | no | Filesystem type. Default is `xfs`. |
| `spec.documents[].userVolumeConfig.filesystem.xfs` | `object` | no | XFS-specific filesystem options, valid only for 'xfs' filesystem. |
| `spec.documents[].userVolumeConfig.filesystem.xfs.minAllocationGroupSize` | `string` | no | The minimum size of an XFS allocation group. On non-rotational devices `mkfs.xfs` sizes the allocation group count to the number of CPUs, which on machines with many cores and a modest disk yields hundreds of tiny allocation groups. Talos bounds the allocation group size from below to keep the geometry sane; this option overrides that bound. Set to zero to use the `mkfs.xfs` defaults unchanged. Note: this only affects volumes at the time they are formatted. Size is specified in bytes, but can be expressed in human readable format, e.g. 100MB. |
| `spec.documents[].userVolumeConfig.mount` | `object` | no | The mount describes additional mount options. |
| `spec.documents[].userVolumeConfig.mount.disableAccessTime` | `boolean` | no | If true, disable file access time updates. |
| `spec.documents[].userVolumeConfig.mount.secure` | `boolean` | no | Enable secure mount options (nosuid, nodev, noexec). Defaults to true for better security. |
| `spec.documents[].userVolumeConfig.name` | `string` | yes | Name of the volume. Name might be between 1 and 34 characters long and can only contain: lowercase and uppercase ASCII letters, digits, and hyphens. |
| `spec.documents[].userVolumeConfig.provisioning` | `object` | no | The provisioning describes how the volume is provisioned. |
| `spec.documents[].userVolumeConfig.provisioning.diskSelector` | `object` | no | The disk selector expression. |
| `spec.documents[].userVolumeConfig.provisioning.diskSelector.match` | `string` | no | The Common Expression Language (CEL) expression to match the disk. |
| `spec.documents[].userVolumeConfig.provisioning.grow` | `boolean` | no | Should the volume grow to the size of the disk (if possible). |
| `spec.documents[].userVolumeConfig.provisioning.maxSize` | `string` | no | The maximum size of the volume, if not specified the volume can grow to the size of the disk. Size is specified in bytes or in percents. It can be expressed in human readable format, e.g. 100MB. |
| `spec.documents[].userVolumeConfig.provisioning.minSize` | `string` | no | The minimum size of the volume. Size is specified in bytes, but can be expressed in human readable format, e.g. 100MB. |
| `spec.documents[].userVolumeConfig.scrub` | `object` | no | The scrub describes the per-volume filesystem scrub configuration. |
| `spec.documents[].userVolumeConfig.scrub.enabled` | `boolean` | no | Enable or disable scrubbing for this volume. If not set, scrubbing is enabled by default when scrub section is present. |
| `spec.documents[].userVolumeConfig.scrub.interval` | `string` | no | The interval at which the volume is scrubbed, overriding the global scrub interval. |
| `spec.documents[].userVolumeConfig.trim` | `object` | no | The trim describes the per-volume filesystem trim (fstrim) configuration. |
| `spec.documents[].userVolumeConfig.trim.enabled` | `boolean` | no | Enable or disable trimming for this volume. If not set, trimming is enabled when the global FilesystemTrimConfig is present. |
| `spec.documents[].userVolumeConfig.trim.interval` | `string` | no | The interval at which the volume is trimmed, overriding the global trim interval. |
| `spec.documents[].userVolumeConfig.volumeType` | `string` | no | Volume type. |
| `spec.documents[].volumeConfig` | `object` | no | volumeConfig is a Talos VolumeConfig document (a system volume, e.g. STATE or EPHEMERAL, placed on its own disk); populated when kind is "VolumeConfig". |
| `spec.documents[].volumeConfig.encryption` | `object` | no | The encryption describes how the volume is encrypted. |
| `spec.documents[].volumeConfig.encryption.allowDiscards` | `boolean` | no | Allow TRIM/discard requests to be passed through to the underlying device when the encrypted volume is opened. Defaults to false. |
| `spec.documents[].volumeConfig.encryption.blockSize` | `integer` | no | Defines the encryption sector size. |
| `spec.documents[].volumeConfig.encryption.cipher` | `string` | no | Cipher to use for the encryption. Depends on the encryption provider. |
| `spec.documents[].volumeConfig.encryption.keySize` | `integer` | no | Defines the encryption key length. |
| `spec.documents[].volumeConfig.encryption.keys` | `[]object` | yes | Defines the encryption keys generation and storage method. |
| `spec.documents[].volumeConfig.encryption.keys[].kms` | `object` | no | EncryptionKeyKMS represents a key that is generated and then sealed/unsealed by the KMS server. |
| `spec.documents[].volumeConfig.encryption.keys[].kms.endpoint` | `string` | yes |  |
| `spec.documents[].volumeConfig.encryption.keys[].lockToState` | `boolean` | no |  |
| `spec.documents[].volumeConfig.encryption.keys[].nodeID` | `object` | no | EncryptionKeyNodeID represents deterministically generated key from the node UUID and PartitionLabel. |
| `spec.documents[].volumeConfig.encryption.keys[].slot` | `integer` | yes |  |
| `spec.documents[].volumeConfig.encryption.keys[].tpm` | `object` | no | EncryptionKeyTPM represents a key that is generated and then sealed/unsealed by the TPM. |
| `spec.documents[].volumeConfig.encryption.keys[].tpm.checkSecurebootStatusOnEnroll` | `boolean` | no |  |
| `spec.documents[].volumeConfig.encryption.keys[].tpm.options` | `object` | no | EncryptionKeyTPMOptions represents the options for TPM-based key protection. |
| `spec.documents[].volumeConfig.encryption.keys[].tpm.options.pcrs` | `[]integer` | no | List of PCRs to bind the key to. If not set, defaults to PCR 7, can be disabled by passing an empty list. |
| `spec.documents[].volumeConfig.encryption.options` | `[]string` | no | Additional --perf parameters for the LUKS2 encryption. |
| `spec.documents[].volumeConfig.encryption.provider` | `string` | yes | Encryption provider to use for the encryption. |
| `spec.documents[].volumeConfig.filesystem` | `object` | no | The filesystem describes how the volume is formatted. Note: this only takes effect at the time the volume is formatted. |
| `spec.documents[].volumeConfig.filesystem.xfs` | `object` | no | XFS-specific filesystem options. |
| `spec.documents[].volumeConfig.filesystem.xfs.minAllocationGroupSize` | `string` | no | The minimum size of an XFS allocation group. On non-rotational devices `mkfs.xfs` sizes the allocation group count to the number of CPUs, which on machines with many cores and a modest disk yields hundreds of tiny allocation groups. Talos bounds the allocation group size from below to keep the geometry sane; this option overrides that bound. Set to zero to use the `mkfs.xfs` defaults unchanged. Note: this only affects volumes at the time they are formatted. Size is specified in bytes, but can be expressed in human readable format, e.g. 100MB. |
| `spec.documents[].volumeConfig.mount` | `object` | no | The mount describes additional mount options. |
| `spec.documents[].volumeConfig.mount.disableAccessTime` | `boolean` | no | If true, disable file access time updates. Supported only for EPHEMERAL volume. |
| `spec.documents[].volumeConfig.mount.secure` | `boolean` | no | Enable secure mount options (nosuid, nodev). For dedicated ETCD and LOG volumes, this also enables noexec. Defaults to true for better security. Supported for EPHEMERAL and dedicated ETCD, CRI, KUBELET and LOG volumes. |
| `spec.documents[].volumeConfig.name` | `string` | yes | Name of the volume. |
| `spec.documents[].volumeConfig.provisioning` | `object` | no | The provisioning describes how the volume is provisioned. |
| `spec.documents[].volumeConfig.provisioning.diskSelector` | `object` | no | The disk selector expression. |
| `spec.documents[].volumeConfig.provisioning.diskSelector.match` | `string` | no | The Common Expression Language (CEL) expression to match the disk. |
| `spec.documents[].volumeConfig.provisioning.grow` | `boolean` | no | Should the volume grow to the size of the disk (if possible). |
| `spec.documents[].volumeConfig.provisioning.maxSize` | `string` | no | The maximum size of the volume, if not specified the volume can grow to the size of the disk. Size is specified in bytes or in percents. It can be expressed in human readable format, e.g. 100MB. |
| `spec.documents[].volumeConfig.provisioning.minSize` | `string` | no | The minimum size of the volume. Size is specified in bytes, but can be expressed in human readable format, e.g. 100MB. |
| `spec.documents[].volumeConfig.scrub` | `object` | no | The scrub describes the per-volume filesystem scrub configuration. |
| `spec.documents[].volumeConfig.scrub.enabled` | `boolean` | no | Enable or disable scrubbing for this volume. If not set, scrubbing is enabled by default when scrub section is present. |
| `spec.documents[].volumeConfig.scrub.interval` | `string` | no | The interval at which the volume is scrubbed, overriding the global scrub interval. |
| `spec.documents[].volumeConfig.trim` | `object` | no | The trim describes the per-volume filesystem trim (fstrim) configuration. |
| `spec.documents[].volumeConfig.trim.enabled` | `boolean` | no | Enable or disable trimming for this volume. If not set, trimming is enabled when the global FilesystemTrimConfig is present. |
| `spec.documents[].volumeConfig.trim.interval` | `string` | no | The interval at which the volume is trimmed, overriding the global trim interval. |
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

