---
sidebar_order: 4
---

# Kubelet serving certificates

```yaml
spec:
  security:
    kubeletServingCertificates: {enabled: true, verifyFromAPIServer: false}
```

By default a Talos kubelet serves a self-signed certificate on port 10250, so
the kube-apiserver cannot verify it when it opens logs, exec, port-forward and
metrics connections (and a metrics-server needs `--kubelet-insecure-tls`).
`enabled` renders `serverTLSBootstrap: true` for every kubelet (the `KubeletConfig`
document from v1.14, `machine.kubelet.extraConfig` before; Talos v1.14,
"Deploy Metrics Server"). 

## How requests are approved

Each kubelet then asks the `kubernetes.io/kubelet-serving`
signer for a certificate, and kube-controller-manager signs it with the Kubernetes
CA, but only after somebody approves the request. Ichor's controller does that,
instead of a third-party approver, and nothing else:

- It decides only requests of that signer, and only in a cluster that enabled the option.
- It approves a request when the requester is that node's own kubelet
  (`system:node:<name>`, group `system:nodes`), the subject is exactly
  `CN=system:node:<name>, O=system:nodes`, the usages are `digital signature` and
  `server auth` (with `key encipherment` allowed), the request has no extension but
  the names, and every DNS name and IP address in it belongs to that node. What
  belongs to the node is read from the node over the authenticated Talos API (the
  addresses Talos gives the kubelet as `--node-ip`, its node name and hostname),
  not from the Kubernetes Node object.
- It denies a request that fails any of those, with the reason on the request
  (`kubectl describe csr`) and a Warning Event on the TalosCluster.
- It leaves a request pending, never denied, when it cannot place it: the node
  name is not one of the cluster's machines, or the node cannot be read right now.
  You can still approve such a request yourself.

Until a kubelet's request is approved and signed it keeps serving the certificate
it had, so switching the option on cannot take a node down; the kubelet restarts
once on each node. `kubectl get csr` shows the requests and who approved them
(`IchorKubeletServing`). The condition `KubeletServingCertificates` on the
TalosCluster is True when every machine has been issued a certificate, and
otherwise names the machines still waiting; the machine's
`status.kubeletServingCertificate` has the request and the expiry. The kubelet
renews before expiry and Ichor approves the renewal the same way. After a
Kubernetes CA rotation Talos clears a kubelet's certificates and the kubelet asks
again.

## verifyFromAPIServer

`verifyFromAPIServer` also sets `--kubelet-certificate-authority` on the
kube-apiserver (the Kubernetes CA, `/system/secrets/kubernetes/kube-apiserver/ca.crt`),
so the apiserver refuses a kubelet whose certificate is not signed by it. A node
without a CA-signed certificate would break `kubectl logs` and `exec`, so Ichor
renders the flag only after the condition `KubeletServingCertificates` is True
and then records `KubeletServingVerified`; once recorded it stays, so a node that
joins later does not take it away. Turning `verifyFromAPIServer` or `enabled` off
removes the flag. Applying it restarts the kube-apiserver on each control plane,
one at a time, without a node reboot.

A metrics-server you run yourself needs no `--kubelet-insecure-tls` once the
option is on, because it verifies the kubelet against the cluster CA that every
pod receives and the certificates carry the node's IP address. Ichor installs
none.

## Verify on a cluster

To verify on a cluster (what to look at, not a promise about your network):

```sh
kubectl get csr                              # Approved,Issued, requestor system:node:<name>
kubectl get talosmachine -o yaml | grep -A4 kubeletServingCertificate
kubectl get talosclusters -o yaml | grep -B1 -A3 KubeletServing
kubectl logs -n kube-system <a pod on another node>   # works with verifyFromAPIServer on
openssl s_client -connect <node-ip>:10250 </dev/null 2>/dev/null | openssl x509 -noout -issuer -ext subjectAltName
```
