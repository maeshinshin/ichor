# ichor-ui demo cluster (KubeVirt)

A long-lived throwaway Talos cluster for looking at ichor-ui in a browser, built by the
product's own bootstrap: `ichor init` creates the control plane, a second VM is approved
with `ichor machine approve --install-disk`. Everything lives in the outer `ichor-e2e`
namespace and is named `<prefix>-*` (prefix `ichor-ui-demo`, or `ichor-ui-demo2` for the
second build; see ADR 0081 for the decisions behind this).

ichor-ui runs with **OIDC off**: anyone who can reach the LoadBalancer address can read
and write every Ichor CR (ADR 0058). Tear it down when you are done.

## What it is

- `<prefix>-cp`: one schedulable control plane (preset `single`, flannel), created by `ichor init`.
- `<prefix>-w`: a worker found in maintenance mode by `spec.discovery` and approved by hand.
- Inside the cluster: Ichor (controller + ichor-ui) from `init`'s embedded manifest, pod/service
  subnets 10.248.0.0/16 and 10.249.0.0/16 (the outer cluster owns 10.244.0.0/16).
- Outside: `<prefix>-registry` (a `registry:2` Deployment + Service, emptyDir), `<prefix>-tools`
  (a busybox pod `ichor init` runs in, because the workstation cannot reach pod IPs) and the
  LoadBalancer Service `ichor-ui-demo` (`outer-service.yaml`): port 80 -> ichor-ui, 6443 -> the
  inner kube-apiserver.

## Steps

`P=ichor-ui-demo2`, `C=admin@talos-cluster01`; run inside `nix develop`. Replace `__PREFIX__` with `$P`.

```sh
export ICHOR_E2E_KUBE_CONTEXT=$C ICHOR_UI_DEMO_PREFIX=$P
go build -o bin/kubevirt-harness ./test/harness/kubevirt

# 1. registry + images (no container runtime: crane appends the binaries to a distroless base)
sed s/__PREFIX__/$P/ test/harness/ui-demo/registry.yaml | kubectl --context $C -n ichor-e2e apply -f -
REG=$(kubectl --context $C -n ichor-e2e get svc $P-registry -o jsonpath={.spec.clusterIP})
kubectl --context $C -n ichor-e2e port-forward svc/$P-registry 5000:5000 &   # leave it running while you push
make demo-images VERSION=v0.0.0-demo REGISTRY=localhost:5000   # builds and pushes manager, ichor-ui, installer; prints digests
cp bin/img/ichor/ichor bin/ichor-static   # the installembed ichor demo-images built

# 3. VMs in maintenance mode (prints CP_IP and WORKER_IP)
./bin/kubevirt-harness up-demo

# 4. tools pod; copy ichor-static and a static kubectl in; init from inside it
kubectl --context $C -n ichor-e2e run $P-tools --image=busybox:1.37 --restart=Never -- sleep 86400
kubectl --context $C -n ichor-e2e cp bin/ichor-static $P-tools:/tmp/ichor
kubectl --context $C -n ichor-e2e exec $P-tools -- env HOME=/tmp/home /tmp/ichor init --yes \
  --cluster $P --preset single --cni flannel --endpoint-type node --endpoint-address $CP_IP \
  --node-endpoint $CP_IP:50000 --talos-version v1.14.3 --version-contract v1.14 --install-disk /dev/vda \
  --pod-subnets 10.248.0.0/16 --service-subnets 10.249.0.0/16 --registry-mirror ghcr.io=http://$REG:5000 \
  --discover-address $WORKER_IP
# init waits for the node, the API server and the webhooks; allow 10-15 minutes. The secrets bundle and
# kubeconfig are under /tmp/home/.ichor/clusters/$P/ in the pod: copy them to bin/ (gitignored) at once.

# 5. the worker: init's --discover-address set spec.discovery, so wait for the pending TalosMachine, approve it.
#    (inside the pod, KUBECONFIG=/tmp/kc, with a static kubectl copied in)
KUBECONFIG=/tmp/kc ichor machine approve <pending machine name> --role worker --install-disk /dev/vda --hostname $P-w --yes
# both TalosMachines go Ready=True and `get nodes` shows two Ready nodes after a few minutes

# 6. expose it: inner NodePort for ichor-ui, then repoint the outer Service
kubectl -n ichor-system apply -f test/harness/ui-demo/inner-nodeport.yaml          # inner cluster
sed s/__PREFIX__/$P/ test/harness/ui-demo/outer-service.yaml | kubectl --context $C -n ichor-e2e apply -f -
# kubeconfig: init's, with server https://192.168.0.69:6443 and tls-server-name: $CP_IP (a certSAN)
# written to bin/ui-demo.kubeconfig; `kubectl --kubeconfig bin/ui-demo.kubeconfig get nodes`
```

## Update to the current tree

```sh
kubectl --context $C -n ichor-e2e port-forward svc/$P-registry 5000:5000 &   # only ever this service
PF=$!   # the port-forward's PID, to stop it below
V=v0.0.$(date +%s)-$(git rev-parse --short HEAD)   # always newer than the installed one
make demo-images VERSION=$V REGISTRY=localhost:5000
kill $PF   # stop the port-forward once the images are pushed
bin/img/ichor/ichor upgrade ichor $V --cluster $P -n ichor-system --kubeconfig bin/ui-demo.kubeconfig --yes --follow
```

`demo-images` leaves the git tree unchanged and fails if a binary is not freshly built with that `VERSION`;
`bin/img/ichor/ichor` is the CLI it built, so upgrade with that one (there is no `bin/ichor`).

Gotchas: the endpoint is the VM's pod IP and changes if the VM restarts (re-run init);
`ichor init` cannot be re-run against a node that already took its config, so after a failed
init delete and recreate the VMs (`down-demo`, `up-demo`).

## Teardown

Every command names its target; nothing uses `-l` or `--all`.

```sh
C=admin@talos-cluster01; P=ichor-ui-demo2
kubectl --context $C -n ichor-e2e delete pod $P-tools
kubectl --context $C -n ichor-e2e delete deployment $P-registry
kubectl --context $C -n ichor-e2e delete service $P-registry
ICHOR_UI_DEMO_PREFIX=$P ICHOR_E2E_KUBE_CONTEXT=$C ./bin/kubevirt-harness down-demo   # VMs $P-cp/-w and their 4 DataVolumes
rm -f bin/ui-demo.kubeconfig   # only if it is for this cluster
# the shared Service ichor-ui-demo: delete it only when no demo is left
```

Deleting the VMs removes the inner cluster and everything in it.

## Caveats

- The demo VMs use the KubeVirt pod network. Stopping or restarting a VM gives it a new IP.
  The Talos cluster (etcd, endpoints) cannot come back after that. Rebuild the demo with the
  teardown and up steps above instead.
- `ichor node remove` leaves the Kubernetes Node in place until the VM is deleted. Then run
  `kubectl delete node <name>`.
- VM sizing: the control plane needs 2560Mi. That is enough; measured use is about 1.6Gi with
  ichor and ichor-ui. Workers use 1536Mi.
- Run `make demo-images` from a separate worktree for each agent or user. Two builds in one
  checkout overwrite each other's `dist/install.yaml` and `bin/img`.
- On this KubeVirt harness Cilium's default VXLAN tunnel does not pass traffic between VMs; use the geneve tunnel protocol (`tunnelProtocol: geneve`, `cilium.Preset.WithTunnel`).
- For a multi-control-plane demo, point the outer LoadBalancer Service at one control plane VM (or only the joined ones); a selector matching VMs still in maintenance mode gives intermittent connection refused.
