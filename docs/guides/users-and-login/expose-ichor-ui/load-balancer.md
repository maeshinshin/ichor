---
sidebar_order: 3
---

# Load balancer passthrough (operator certificate)

![How a passthrough load balancer reaches ichor-ui. A LoadBalancer Service or TCP passthrough terminates nothing, so the browser or CLI sees ichor-ui's own certificate. You supply that certificate with --ui-tls-secret or ichorUI.tls.secretName. Without it only clients that trust the Ichor CA can verify.](../../../images/users-expose-load-balancer.light.drawio.svg)

*Nothing terminates TLS, so clients see the certificate ichor-ui serves: supply yours.*

A `Service` of type `LoadBalancer` (`ichorUI.service.type: LoadBalancer`) or a
TCP passthrough terminates nothing, so clients see ichor-ui's own certificate
and the name they typed. Supply your certificate:

```sh
ichor init --ui-tls-secret ichor-ui-cert --ui-url https://ichor.example.com ...
# or in the chart
helm upgrade ichor ... --set ichorUI.tls.secretName=ichor-ui-cert
```

The Secret is `kubernetes.io/tls`, and its certificate must cover the name
clients use. Without it, only clients given the Ichor CA verify:

```sh
kubectl -n ichor-system get configmap ichor-ui-ca -o jsonpath='{.data.ca\.crt}' > ca.crt
ichor --ca-file ca.crt ...        # or ICHOR_CA_FILE
```

An L7 cloud load balancer that cannot verify its backend is out of scope: use a
controller that can.
