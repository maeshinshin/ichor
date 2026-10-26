# ichor-ui demo cluster (KubeVirt)

A long-lived throwaway Talos cluster for looking at ichor-ui in a browser. Everything
lives in the outer `ichor-e2e` namespace and is named `ichor-ui-demo-*`.

ichor-ui runs with **OIDC off**: anyone who can reach the LoadBalancer address can read
and write every Ichor CR. Tear it down when you are done.

## What it is

- `ichor-ui-demo-cp`: one schedulable control plane, installed and bootstrapped by
  `kubevirt-harness up-demo`, adopted and approved through the `ichor` CLI.
- `ichor-ui-demo-w`: a worker left in maintenance mode; `spec.discovery` finds it and it
  shows as a pending machine.
- Inside the cluster (`ichor-system`): the controller (`ENABLE_WEBHOOKS=false`), ichor-ui,
  a NodePort Service on 30080, a TalosCluster, the control plane's TalosMachine and one
  TalosConfigPatch (`demo-sysctl`).
- Outside: Service `ichor-ui-demo` (type LoadBalancer) selecting the control plane's
  virt-launcher pod. Cilium's pool assigns a 192.168.0.64/26 address.
- `ichor-ui-demo-tools`: a busybox pod serving the controller/ichor-ui/ichor/kubectl
  binaries on :8000 for the inner pods' init containers (no registry, no docker). Keep it
  running: a restarted inner pod fetches its binary from it again.

## Steps (as run)

```sh
export ICHOR_E2E_KUBE_CONTEXT=admin@talos-cluster01
go build -o bin/kubevirt-harness ./test/harness/kubevirt
./bin/kubevirt-harness up-demo        # VMs, Talos install, bootstrap; writes bin/ui-demo.*
make build-ichor-ui                   # then CGO_ENABLED=0 go build -tags uiembed -o bin/ichor-ui ./cmd/ichor-ui
CGO_ENABLED=0 go build -o bin/manager-static cmd/main.go
CGO_ENABLED=0 go build -o bin/ichor ./cmd/ichor

kubectl --context admin@talos-cluster01 apply -f test/harness/ui-demo/tools-pod.yaml
# kubectl cp bin/{ichor-ui,manager-static->manager,ichor} and kubectl to ichor-ui-demo-tools:/srv/,
# and bin/ui-demo.{kubeconfig,talosconfig} / ui-demo-secrets.yaml to :/work/{kubeconfig,talosconfig,secrets.yaml}
# Inner kubectl: kubectl exec ichor-ui-demo-tools -- env KUBECONFIG=/work/kubeconfig /srv/kubectl ...
# inner: apply config/crd/bases/*.yaml (--server-side), inner.yaml (TOOLS_IP -> the tools pod IP),
#        config/rbac/ichor-ui/*.yaml (namespace system -> ichor-system; apply with
#        `-n ichor-system`: the RoleBinding ichor-ui-secrets-rolebinding has no namespace
#        in the source, so without it it lands in `default` and ichor-ui cannot read the
#        bundle Secret; or use kustomize with the namespace set), the secrets Secret
#        ichor-ui-demo-secrets (key secrets.yaml), ichor-crs.yaml (placeholders filled in)
# ichor adopt --cluster ichor-ui-demo -n ichor-system ...  (with discovery temporarily off:
#        adopt dials every machine, and a maintenance-mode worker has no credentials)
# ichor machine approve ichor-ui-demo-cp --role controlplane --local ...
# inner: apply ichor-patch.yaml, then put spec.discovery back
kubectl --context admin@talos-cluster01 apply -f test/harness/ui-demo/outer-service.yaml
kubectl --context admin@talos-cluster01 -n ichor-e2e get svc ichor-ui-demo   # EXTERNAL-IP
```

`ichor adopt` reports drift it cannot patch (install-time and pod/service subnet fields)
because the harness generates the node's config with the Talos library, not Ichor's render.
So the control plane's Ready condition is False/RebootRequired: Ichor refuses to apply.

## Teardown

Every command names its target; nothing uses `-l` or `--all`.

```sh
C=admin@talos-cluster01
kubectl --context $C -n ichor-e2e delete service ichor-ui-demo
kubectl --context $C -n ichor-e2e delete pod ichor-ui-demo-tools
ICHOR_E2E_KUBE_CONTEXT=$C ./bin/kubevirt-harness down-demo   # VMs ichor-ui-demo-cp/-w and their 4 DataVolumes
rm -f bin/ui-demo.kubeconfig bin/ui-demo.talosconfig bin/ui-demo-secrets.yaml
```

Deleting the VMs removes the inner cluster and everything in it.
