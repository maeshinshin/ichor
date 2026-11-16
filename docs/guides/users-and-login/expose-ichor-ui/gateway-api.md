---
sidebar_order: 2
---

# Gateway API: BackendTLSPolicy

`BackendTLSPolicy` is `gateway.networking.k8s.io/v1`, in the Standard channel
since Gateway API v1.4 (it was `v1alpha3` before). Your Gateway implementation
must support it, and the CRDs installed in the cluster must be v1.4 or later.
Release 1.4.1 notes say a policy supports a single `targetRefs` entry.

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: BackendTLSPolicy
metadata:
  name: ichor-ui
  namespace: ichor-system
spec:
  targetRefs:
    - group: ""
      kind: Service
      name: ichor-ui-service
  validation:
    hostname: ichor-ui-service.ichor-system.svc
    caCertificateRefs:
      - group: ""
        kind: ConfigMap
        name: ichor-ui-ca
```

- It lives in the Service's namespace; `caCertificateRefs` may name only objects
  in the policy's own namespace. That is why Ichor publishes `ichor-ui-ca` in
  `ichor-system`.
- The Core-support reference is one ConfigMap with the key `ca.crt`, which is
  what `ichor-ui-ca` is. A Secret reference is implementation-specific.
- `validation.hostname` is the SNI and the name the certificate must match.
- The HTTPRoute that sends traffic to the Service references port 8443 (named
  `https`). A route in another namespace needs a `ReferenceGrant`.
- Check the policy's `Accepted` and `ResolvedRefs` conditions.
- Implementation: Envoy Gateway v1.9.2 (its chart installs the Gateway API v1.6.1
  CRDs, `BackendTLSPolicy` served as `v1`). With the policy, `Accepted` and
  `ResolvedRefs` are set and requests reach ichor-ui; a ConfigMap with another CA or a
  `hostname` that is not in the certificate gives 503 from Envoy, and without the
  policy Envoy speaks plain HTTP to port 8443 (ichor-ui answers 400). Envoy can take
  up to a minute to apply a change, so wait and repeat the request. A
  Gateway needs a `listeners[].hostname` (singular), not `hostnames`.
