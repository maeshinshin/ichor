---
sidebar_order: 2
---

# Gateway API: BackendTLSPolicy

![How a Gateway reaches ichor-ui. The browser connects to the Gateway and its HTTPRoute, whose listener has a hostname. The Gateway re-encrypts to ichor-ui-service on port 8443. A BackendTLSPolicy in ichor-system names the host name and the CA ConfigMap ichor-ui-ca. It needs Gateway API v1.4 or later CRDs, and a route in another namespace needs a ReferenceGrant.](../../../images/users-expose-gateway.light.drawio.svg)

*The `BackendTLSPolicy` tells the Gateway which CA and name to verify ichor-ui with.*

`BackendTLSPolicy` is `gateway.networking.k8s.io/v1`, Standard channel since
Gateway API v1.4 (`v1alpha3` before). Your implementation must support it, with
v1.4 or later CRDs installed. A policy takes a single `targetRefs` entry.

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

- The policy lives in the Service's namespace, and `caCertificateRefs` may name only objects in it: that is why Ichor publishes `ichor-ui-ca` in `ichor-system`.
- Core support is one ConfigMap with key `ca.crt`, which `ichor-ui-ca` is. A Secret reference is implementation-specific.
- `validation.hostname` is the SNI and the name the certificate must match.
- The HTTPRoute references Service port 8443 (named `https`). A route in another namespace needs a `ReferenceGrant`.
- A Gateway needs `listeners[].hostname` (singular), not `hostnames`.
- Check the policy's `Accepted` and `ResolvedRefs` conditions.
- Envoy Gateway v1.9.2 (Gateway API v1.6.1 CRDs): with the policy, requests reach ichor-ui. A ConfigMap with another CA, or a `hostname` not in the certificate, gives 503; without the policy Envoy speaks plain HTTP to 8443 and ichor-ui answers 400. Envoy can take a minute to apply a change.
