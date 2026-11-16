---
sidebar_order: 7
---

# ichor-ui is HTTPS only

ichor-ui serves TLS on 8443 and no plain HTTP. The Service port is
named `https`. The certificate is yours (`ichorUI.tls.secretName`) or a leaf from
the Ichor serving CA, whose public part the controller publishes as the
ConfigMap and Secret `ichor-ui-ca` in `ichor-system`. How to put an Ingress, a
Gateway or a load balancer in front is in [Users and login](../users-and-login/expose-ichor-ui.md).

> [!WARNING]
> ichor-ui no longer serves plain HTTP. Point the edge at ichor-ui on 8443 before you upgrade.

Moving an existing release:

- Plain HTTP edge: ichor-ui no longer serves plain HTTP. Re-point the edge at
  `https://<ichor-ui>:8443` and trust `ichor-ui-ca` before you upgrade. The
  value `ichorUI.plaintextPort` was removed; a values file that still sets it
  is ignored.
- An install that already served TLS on 8080 sets `ichorUI.port=8080` to keep
  its Service and edge as they are.
- The ServiceMonitor verifies against `ichor-ui-ca` by default; override with
  `ichorUI.tls.caConfigMap`, `caSecret` and `caKey`. It never skips
  verification.
