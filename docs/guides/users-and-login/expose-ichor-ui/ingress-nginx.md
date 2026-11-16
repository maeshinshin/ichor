---
sidebar_order: 1
---

# Ingress-NGINX (re-encrypt to the backend)

The Ingress must be in `ichor-system`, because it names the Service. Annotations
(Ingress-NGINX annotations reference, "Backend Protocol" and "Backend Certificate
Authentication"):

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: ichor-ui
  namespace: ichor-system
  annotations:
    nginx.ingress.kubernetes.io/backend-protocol: "HTTPS"
    nginx.ingress.kubernetes.io/proxy-ssl-verify: "on"
    nginx.ingress.kubernetes.io/proxy-ssl-secret: "ichor-system/ichor-ui-ca"
    nginx.ingress.kubernetes.io/proxy-ssl-name: "ichor-ui-service.ichor-system.svc"
    nginx.ingress.kubernetes.io/proxy-ssl-server-name: "on"
spec:
  ingressClassName: nginx
  tls:
    - hosts: [ichor.example.com]
      secretName: ichor-example-com-tls
  rules:
    - host: ichor.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: ichor-ui-service
                port:
                  name: https
```

- `backend-protocol: "HTTPS"` makes NGINX speak TLS to the pod.
- `proxy-ssl-verify` is off by default; without it NGINX does not check the
  pod's certificate.
- `proxy-ssl-secret` takes `namespace/secretName`. The reference docs describe a
  Secret with `tls.crt` and `tls.key` (a client certificate, which ichor-ui does
  not ask for) that "should also contain" `ca.crt`. A Secret with only `ca.crt`,
  which is what `ichor-ui-ca` is, is accepted by Ingress-NGINX (chart
  4.15.1, controller 1.15.1): the request reaches ichor-ui and nginx uses the CA
  as `proxy_ssl_trusted_certificate`. Pointing the annotation at a Secret that
  holds another CA gives 502 (`upstream SSL certificate verify error`).
- `proxy-ssl-name` overrides the name NGINX verifies; `proxy-ssl-server-name`
  sends it as SNI. Use a name in the certificate. It is required: without it
  NGINX verifies the literal name `upstream_balancer` and every request is a
  502. A name that is not in the certificate is a 502 too (`upstream SSL
  certificate does not match`). Ingress-NGINX takes up to about half a minute
  to reload after an annotation change, so wait before you read a result.
- The public certificate for browsers is the Ingress's own `tls` Secret.
- If you supplied your own certificate, point `proxy-ssl-secret` at a Secret that
  holds its CA as `ca.crt`.
