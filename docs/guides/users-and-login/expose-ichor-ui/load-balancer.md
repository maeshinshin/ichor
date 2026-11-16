---
sidebar_order: 3
---

# Load balancer passthrough (operator certificate)

A `Service` of type `LoadBalancer` (`ichorUI.service.type: LoadBalancer`) or a
TCP passthrough terminates nothing, so clients see ichor-ui's own certificate
and the name they typed. Supply your certificate:

```sh
ichor init --ui-tls-secret ichor-ui-cert --ui-url https://ichor.example.com ...
# or in the chart
helm upgrade ichor ... --set ichorUI.tls.secretName=ichor-ui-cert
```

The Secret is `kubernetes.io/tls` and its certificate must cover the name
clients use. Without it, only clients given the Ichor CA verify:
`ichor --ca-file ca.crt ...` (or `ICHOR_CA_FILE`, from
`kubectl -n ichor-system get configmap ichor-ui-ca -o jsonpath='{.data.ca\.crt}'`).
An L7 cloud load balancer that cannot verify its backend is out of scope: use a
controller that can.
