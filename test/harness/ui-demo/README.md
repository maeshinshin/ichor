# ichor-ui demo cluster (KubeVirt)

A long-lived throwaway Talos cluster for looking at ichor-ui in a browser, built by the
product's own bootstrap: `ichor init` creates the control plane, a second VM is approved
with `ichor approve node --install-disk`. Everything lives in the outer `ichor-e2e`
namespace and is named `<prefix>-*` (prefix `ichor-ui-demo`; the current build is
`ichor-ui-demo3`).

ichor-ui runs with **users and OIDC on**: `init --admin-user admin` creates the first admin
and prints its generated password once (must change on first login). It needs an https
`--ui-url` and a Talos version contract of v1.14 or later. Tear it down when you are done.

## What it is

- `<prefix>-cp`: one schedulable control plane (preset `single`, Cilium with the geneve tunnel), created by `ichor init`.
- `<prefix>-w`: a worker found in maintenance mode by `spec.discovery` and approved by hand.
- Inside the cluster: Ichor (controller + ichor-ui) from `init`'s embedded manifest, pod/service
  subnets 10.248.0.0/16 and 10.249.0.0/16 (the outer cluster owns 10.244.0.0/16).
- Outside: `<prefix>-registry` (a `registry:2` Deployment + Service, emptyDir), `<prefix>-tools`
  (a busybox pod `ichor init` runs in, because the workstation cannot reach pod IPs) and the
  LoadBalancer Service `ichor-ui-demo` (`outer-service.yaml`): port 443 -> ichor-ui (nodePort
  30080 -> ichor-ui's HTTPS port 8443), 6443 -> the inner kube-apiserver. There is no port 80:
  the UI URL is https, and ichor-ui serves nothing but HTTPS.

## Steps

`P=ichor-ui-demo3`, `C=OUTER_CONTEXT` (your outer cluster's kube context); run inside `nix develop`. Replace `__PREFIX__` with `$P`.

VM memory defaults to 2560Mi for both VMs. Override per role before `up-demo` with
`ICHOR_UI_DEMO_CP_MEMORY` and `ICHOR_UI_DEMO_WORKER_MEMORY` (a plain `Mi`/`Gi` quantity, e.g.
`ICHOR_UI_DEMO_CP_MEMORY=3Gi`). Talos needs at least 2 GiB for a control plane and 1 GiB for a worker.

**ISO cache.** `up-demo`, `up` and `up-ha` clone one cached Talos ISO DataVolume
(`ichor-e2e-talos-v1-14-3-<schematic8>-base`, outer namespace `ichor-e2e`) instead of importing the ISO per VM.
The first run creates it and waits for the import; no `down*` deletes it, so delete it by hand when the Talos
version changes. `ICHOR_E2E_BASE_CACHE=0` restores the per-VM import. The VMs still boot the ISO in maintenance
mode and Ichor still installs Talos, so the install step stays exercised. Both VMs are created together.

**Boot order.** Each VM boots its system disk first and the ISO second. A blank disk is not bootable, so the
first boot falls through to the ISO (maintenance mode); after Ichor installs Talos every cold boot (a VM stopped
and started) comes up from the disk. With the ISO first Talos halts on a cold boot ("already installed to disk
but booted from another media").

**Other shapes.** `ICHOR_UI_DEMO_NO_WORKER=1` creates the control plane only. `ICHOR_UI_DEMO_HA=1` creates three
control planes (`<prefix>-cp`, `-cp2`, `-cp3`, no worker; `NO_WORKER` is ignored) and prints `CP_IP`, `CP2_IP`
and `CP3_IP`; every launcher pod carries the label `ichor.maesh.dev/e2e-group: <prefix>`, so an outer Service can
select all of one demo's VMs. `ICHOR_UI_DEMO_HA=extra` creates only a fourth control plane, `<prefix>-cp4`
(prints `CP4_IP`), next to a running HA demo, to replace one of its control planes. `down-demo` with either
value also deletes `-cp2`, `-cp3` and `-cp4` and their DataVolumes.

```sh
export ICHOR_E2E_KUBE_CONTEXT=$C ICHOR_UI_DEMO_PREFIX=$P
go build -o bin/kubevirt-harness ./test/harness/kubevirt

# 1. registry + images (no container runtime: ko builds them, crane pushes them)
sed s/__PREFIX__/$P/ test/harness/ui-demo/registry.yaml | kubectl --context $C -n ichor-e2e apply -f -
REG=$(kubectl --context $C -n ichor-e2e get svc $P-registry -o jsonpath={.spec.clusterIP})
kubectl --context $C -n ichor-e2e port-forward svc/$P-registry 5000:5000 &   # leave it running while you push
make demo-images VERSION=v0.0.0-demo REGISTRY=localhost:5000 DIGEST_FILE=bin/demo-digests.txt   # builds and pushes manager, ichor-ui, installer; prints digests
D=$(sed -n 's/.*ichor-installer@//p' bin/demo-digests.txt)   # the installer image's digest, for init below
cp bin/img/ichor/ichor bin/ichor-static   # the installembed ichor demo-images built

# 2. VMs in maintenance mode (prints CP_IP and WORKER_IP)
./bin/kubevirt-harness up-demo

# 3. the outer LoadBalancer first: init needs its address (LB_IP) for --ui-url and the cert.
#    (Cilium assigns the address on creation; it reaches the VM once the inner NodePort exists, step 6.)
sed s/__PREFIX__/$P/ test/harness/ui-demo/outer-service.yaml | kubectl --context $C -n ichor-e2e apply -f -
LB_IP=$(kubectl --context $C -n ichor-e2e get svc ichor-ui-demo -o jsonpath={.status.loadBalancer.ingress[0].ip})

# 4. self-signed cert for ichor-ui, with the LB address as an IP SAN (openssl is in nix develop)
nix develop --command openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 30 \
  -subj "/CN=ichor-ui" -addext "subjectAltName=IP:$LB_IP" -keyout bin/ui-demo.key -out bin/ui-demo.crt
chmod 0600 bin/ui-demo.key

# 5. tools pod; copy ichor-static, the cert and key in; init from inside it
kubectl --context $C -n ichor-e2e run $P-tools --image=busybox:1.37 --restart=Never -- sleep 86400
kubectl --context $C -n ichor-e2e cp bin/ichor-static $P-tools:/tmp/ichor
kubectl --context $C -n ichor-e2e cp bin/ui-demo.crt $P-tools:/tmp/ui.crt
kubectl --context $C -n ichor-e2e cp bin/ui-demo.key $P-tools:/tmp/ui.key
kubectl --context $C -n ichor-e2e exec $P-tools -- env HOME=/tmp/home /tmp/ichor init --yes --bundle-plaintext \
  --cluster $P --preset single --cni cilium --cni-tunnel geneve --endpoint-type node --endpoint-address $CP_IP \
  --node-endpoint $CP_IP:50000 --talos-version v1.14.5 --version-contract v1.14 --install-disk /dev/vda \
  --pod-subnets 10.248.0.0/16 --service-subnets 10.249.0.0/16 --registry-mirror ghcr.io=http://$REG:5000 \
  --discover-address $WORKER_IP \
  --ui-url https://$LB_IP --admin-user admin --ui-tls-cert /tmp/ui.crt --ui-tls-key /tmp/ui.key \
  --kube-api-server-url https://$LB_IP:6443 --kube-api-tls-server-name $CP_IP \
  --installer-digest $D --skip-signature-check
# `--installer-digest` and `--skip-signature-check` are what a demo build needs: the inner cluster cannot reach ghcr.io (the
# controller would resolve the installer's tag there for the Cilium Job and pause with CNIHandover InstallerImageUnresolved), and
# a demo image has no signature. init records both in the IchorInstallation.
# `--ui-url` must be https and needs --version-contract v1.14 or later when users are on.
# init prints the generated admin password ONCE (must change on first login). Save it at once to a
# 0600 file under bin/ (gitignored), e.g. umask 077 and redirect it to bin/ui-demo.admin-password.
# init waits for the node, the API server, the webhooks and the Cilium hand-over; allow 20-45 minutes. The secrets bundle and
# kubeconfig are under /tmp/home/.ichor/clusters/$P/ in the pod: copy them to bin/ as soon as they appear.

# 6. apply the config. init leaves the control plane at APIServerRestartRequired until this runs.
#    With a single control plane the API server is down for about a minute.
#    Run inside the tools pod (see the --local note below).
ichor rollout config --cluster $P -n ichor-system --yes --follow --local

# 7. the worker: init's --discover-address set spec.discovery, so wait for the pending TalosMachine, approve it.
#    (inside the pod, KUBECONFIG=/tmp/kc, with a static kubectl copied in)
KUBECONFIG=/tmp/kc ichor approve node <pending machine name> --role worker --install-disk /dev/vda --hostname $P-w --yes --local
# both TalosMachines go Ready=True and `get nodes` shows two Ready nodes after a few minutes

# 8. expose it: the inner NodePort for ichor-ui (outer Service was applied in step 3)
kubectl -n ichor-system apply -f test/harness/ui-demo/inner-nodeport.yaml          # inner cluster
# kubeconfig: init's, with server https://$LB_IP:6443 and tls-server-name: $CP_IP (a certSAN)
# written to bin/ui-demo.kubeconfig; `kubectl --kubeconfig bin/ui-demo.kubeconfig get nodes`
# UI: https://$LB_IP, user admin (self-signed: the browser warns)
```

`inner-nodeport.yaml` targets 8443. A demo that was created by an older tree serves its operator
certificate on 8080 and keeps that port through every upgrade (the installer pins it), so for such a
demo apply the file with `8080` in both port fields instead; a demo created from this tree uses 8443 as is.

The CLI verifies ichor-ui through the Service name against `ichor-ui-ca`, and ichor-ui answers that name with
Ichor's own certificate even though this demo serves its operator certificate for the load balancer address
(2026-11-03). From a build with that amendment, commands run from the tools pod need no
`--local`; a demo still on the earlier image (before its first upgrade) does, because the CLI then warns and
computes from the Kubernetes API directly.

## Update to the current tree

```sh
kubectl --context $C -n ichor-e2e port-forward svc/$P-registry 5000:5000 &   # only ever this service
PF=$!   # the port-forward's PID, to stop it below
V=v0.0.$(date +%s)-$(git rev-parse --short HEAD)   # always newer than the installed one
make demo-images VERSION=$V REGISTRY=localhost:5000 DIGEST_FILE=bin/demo-digests.txt
kill $PF   # stop the port-forward once the images are pushed
D=$(sed -n 's/.*ichor-installer@//p' bin/demo-digests.txt)   # the installer image's digest
bin/img/ichor/ichor upgrade ichor $V --cluster $P -n ichor-system --kubeconfig bin/ui-demo.kubeconfig --yes --follow \
  --installer-digest $D --skip-signature-check
```

The controller pins the installer image to a digest and checks its signature before it runs the Job.
A demo image is not signed, and the controller cannot resolve its tag: the Talos nodes reach the demo registry
through a containerd mirror for `ghcr.io`, the controller does not. So the upgrade names the digest
(`--installer-digest`, from `DIGEST_FILE`) and skips the signature check
(`--skip-signature-check`); the Job then pulls `ghcr.io/maeshinshin/ichor-installer@$D` through the mirror. The CLI,
`ichor version` and the UI say loudly that the check was skipped.

`demo-images` leaves the git tree unchanged;
`bin/img/ichor/ichor` is the CLI it built, so upgrade with that one (there is no `bin/ichor`).

Gotchas: the endpoint is the VM's pod IP and changes if the VM restarts (re-run init);
`ichor init` cannot be re-run against a node that already took its config, so after a failed
init delete and recreate the VMs (`down-demo`, `up-demo`).
`ichor init` also refuses to overwrite the secrets bundle of a run that already configured a node, kept under
`/tmp/home/.ichor/clusters/$P/`: once those VMs are gone, remove that directory first.

## Teardown

Every command names its target; nothing uses `-l` or `--all`.

```sh
C=OUTER_CONTEXT; P=ichor-ui-demo3
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
- `ichor delete node` leaves the Kubernetes Node in place until the VM is deleted. Then run
  `kubectl delete node <name>`.
- VM sizing: the harness gives both VMs 2560Mi by default. For the control plane that is enough;
  measured use is about 1.6Gi with ichor and ichor-ui. A memory-bound host can shrink the worker
  with `ICHOR_UI_DEMO_WORKER_MEMORY=1536Mi` (Talos minimum is 1 GiB for a worker, 2 GiB for a
  control plane); `ICHOR_UI_DEMO_CP_MEMORY` sets the control plane.
- Run `make demo-images` from a separate worktree for each agent or user. Two builds in one
  checkout overwrite each other's `dist/install.yaml` and `bin/img` and `bin/oci`.
- On this KubeVirt harness Cilium's default VXLAN tunnel does not pass traffic between VMs; use the geneve tunnel protocol (`ichor init --cni cilium --cni-tunnel geneve`, recorded as `spec.cni.tunnel`).
- For a multi-control-plane demo, point the outer LoadBalancer Service at one control plane VM (or only the joined ones); a selector matching VMs still in maintenance mode gives intermittent connection refused.
