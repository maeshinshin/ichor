#!/usr/bin/env bash
# Run the gate (make test, then GOTOOLCHAIN=go1.27.4 make lint) for GATE_REF as one Job in a
# cluster, instead of on the workstation. make cluster-gate runs this; see the Makefile for the
# variables. The source is git archive of GATE_REF: committed files only, uncommitted work is not sent.
#   GATE_RENDER=1  print the PersistentVolumeClaim and Job manifests and exit; no cluster is contacted.
# The Job's own status decides the result: exit 0 only if it completed, so both steps passed. A dropped
# log stream is not a failure. The log lands in bin/cluster-gate/<job>.log.
# The PersistentVolumeClaim ichor-gate-cache keeps the module cache, the build cache and bin/ between runs.
# A run reuses an existing claim and refuses one whose storage class or size differs from the request.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"
START=$(date +%s)
REF=${GATE_REF:-HEAD}
SHA=$(git rev-parse --verify "$REF^{commit}")
SHORT=${SHA:0:7}
JOB=ichor-gate-$SHORT-$START
PVC=ichor-gate-cache
NS=${GATE_NAMESPACE:-ichor-e2e}
KUBECONFIG_FILE=${GATE_KUBECONFIG:-${KUBECONFIG:-$HOME/.kube/config}}
IMAGE=${GATE_IMAGE:-docker.io/library/golang:1.27.4-bookworm}
SC=${GATE_STORAGE_CLASS:-}
CACHE_SIZE=${GATE_CACHE_SIZE:-20Gi}
DEADLINE=${GATE_DEADLINE:-3600}
CPU=${GATE_CPU:-4} MEM=${GATE_MEM:-6Gi} CPU_MAX=${GATE_CPU_LIMIT:-8} MEM_MAX=${GATE_MEM_LIMIT:-10Gi}

# pod_script: runs inside the container. It waits for the source (kubectl exec copies it, then touches
# /work/ready), then runs both steps and exits non-zero if either failed.
pod_script() {
  sed "s/@SHA@/$SHA/" <<'EOF'
set -e
while [ ! -f /work/ready ]; do sleep 1; done
start=$(date +%s)
cd /work/src
export PATH=/cache/tools:$PATH GOMODCACHE=/cache/gomod GOCACHE=/cache/gocache
# shortcut: the tools are pinned here, not read from flake.nix, so a bump needs an edit in two places; drop when the pins come from the image.
[ -x /cache/tools/helm ] || { curl -fsSL https://get.helm.sh/helm-v4.3.1-linux-amd64.tar.gz | tar -xz -C /tmp && install -m0755 /tmp/linux-amd64/helm /cache/tools/helm; }
[ -x /cache/tools/kubectl ] || { curl -fsSL -o /tmp/kubectl https://dl.k8s.io/release/v1.37.1/bin/linux/amd64/kubectl && install -m0755 /tmp/kubectl /cache/tools/kubectl; }
[ -x /cache/tools/buf ] || { curl -fsSL -o /tmp/buf https://github.com/bufbuild/buf/releases/download/v1.73.0/buf-Linux-x86_64 && install -m0755 /tmp/buf /cache/tools/buf; }
# The tools need a git checkout: claimcheck, noticecheck and thirdpartynotices shell out to git.
git init -q && echo /bin >> .git/info/exclude && git add -A && git -c user.name=ichor-gate -c user.email=gate@example.invalid commit -q -m "gate snapshot @SHA@"
go version; helm version --short; kubectl version --client; buf --version
set +e # both steps run, and the last line of this script fails unless both exited 0
s=$(date +%s)
make test; t=$?
echo "GATE_STEP test exit=$t secs=$(( $(date +%s) - s ))"
s=$(date +%s)
GOTOOLCHAIN=go1.27.4 make lint; l=$?
echo "GATE_STEP lint exit=$l secs=$(( $(date +%s) - s ))"
echo "GATE_SECONDS=$(( $(date +%s) - start ))"
[ "$t" = 0 ] && [ "$l" = 0 ]
EOF
}

# claim_manifest: the PVC, kept after the run. job_manifest: the Job, deleted by name after the run.
claim_manifest() {
  cat <<EOF
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: $PVC
  labels: {app.kubernetes.io/name: ichor-gate}
spec:
  accessModes: [ReadWriteOnce]
  resources: {requests: {storage: $CACHE_SIZE}}
  ${SC:+storageClassName: $SC}
EOF
}

job_manifest() {
  cat <<EOF
apiVersion: batch/v1
kind: Job
metadata:
  name: $JOB
  labels: {app.kubernetes.io/name: ichor-gate, ichor.gate/sha: "$SHORT"}
spec:
  backoffLimit: 0
  activeDeadlineSeconds: $DEADLINE
  ttlSecondsAfterFinished: 3600
  template:
    metadata:
      labels: {app.kubernetes.io/name: ichor-gate, ichor.gate/sha: "$SHORT"}
    spec:
      restartPolicy: Never
      automountServiceAccountToken: false
      containers:
      - name: gate
        image: $IMAGE
        imagePullPolicy: IfNotPresent
        command: [/bin/sh, -c]
        args:
        - |
$(pod_script | sed 's/^/          /')
        resources:
          requests: {cpu: "$CPU", memory: $MEM, ephemeral-storage: 4Gi}
          limits: {cpu: "$CPU_MAX", memory: $MEM_MAX, ephemeral-storage: 20Gi}
        volumeMounts:
        - {name: work, mountPath: /work}
        # bin/ is mounted at its own path, not symlinked: go-install-tool compares readlink with realpath, and a symlinked bin/ makes it reinstall (and clobber) the custom golangci-lint every run.
        - {name: cache, mountPath: /work/src/bin, subPath: bin}
        - {name: cache, mountPath: /cache/gomod, subPath: gomod}
        - {name: cache, mountPath: /cache/gocache, subPath: gocache}
        - {name: cache, mountPath: /cache/tools, subPath: tools}
      volumes:
      - name: work
        emptyDir: {sizeLimit: 8Gi}
      - name: cache
        persistentVolumeClaim: {claimName: $PVC}
EOF
}

manifest() {
  claim_manifest
  echo ---
  job_manifest
}

if [ -n "${GATE_RENDER:-}" ]; then
  manifest
  exit 0
fi

kc() { kubectl --kubeconfig="$KUBECONFIG_FILE" ${GATE_CONTEXT:+--context="$GATE_CONTEXT"} -n "$NS" "$@"; }

# shortcut: refuses only while another gate Job is active, not while a finished one still holds the PVC; the PVC is ReadWriteOnce, so two runs would fight over bin/.
active=$(kc get jobs -l app.kubernetes.io/name=ichor-gate -o jsonpath='{range .items[?(@.status.active>0)]}{.metadata.name}{" "}{end}')
[ -z "$active" ] || { echo "cluster-gate: gate Jobs still active in $NS: $active; wait for them, they share $PVC" >&2; exit 1; }

# An existing claim is reused as it is: its spec is immutable, and replacing it would drop the cache. A
# different storage class or size is refused, not applied or replaced. An unset GATE_STORAGE_CLASS means
# whatever class the claim was bound with, since that is the cluster default the first run got.
# shortcut: sizes are compared as strings, so 20G and 20Gi count as a mismatch; parse them as quantities once someone sets GATE_CACHE_SIZE in another unit.
claim=$(kc get pvc "$PVC" --ignore-not-found -o jsonpath='{.spec.storageClassName}/{.spec.resources.requests.storage}')
if [ -n "$claim" ]; then
  have_sc=${claim%%/*} have_size=${claim#*/}
  [ -z "$SC" ] || [ "$have_sc" = "$SC" ] || { echo "cluster-gate: claim $PVC has storage class $have_sc, GATE_STORAGE_CLASS asks for $SC; delete the claim by hand to change it (this drops the cache)" >&2; exit 1; }
  [ "$have_size" = "$CACHE_SIZE" ] || { echo "cluster-gate: claim $PVC is $have_size, GATE_CACHE_SIZE asks for $CACHE_SIZE; delete the claim by hand to resize it (this drops the cache)" >&2; exit 1; }
  echo "cluster-gate: reusing claim $PVC ($have_size, storage class ${have_sc:-default})"
fi

mkdir -p bin/cluster-gate
LOG=bin/cluster-gate/$JOB.log
ARCHIVE=bin/cluster-gate/$JOB.tgz
created=
cleanup() {
  rm -f "$ARCHIVE"
  [ -z "$created" ] || kc delete job "$JOB" --ignore-not-found --cascade=foreground --wait=true >/dev/null \
    || echo "cluster-gate: could not delete Job $JOB; delete it by name" >&2
}
trap cleanup EXIT
trap 'exit 130' INT TERM

git archive --format=tar "$SHA" | gzip -1 >"$ARCHIVE"
created=1
if [ -z "$claim" ]; then
  manifest | kc apply -f - >/dev/null
else
  job_manifest | kc apply -f - >/dev/null
fi

POD=
t0=$(date +%s)
# the pod does not exist yet, so every early poll fails; the timeout below ends the wait
until POD=$(kc get pods -l job-name="$JOB" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null) && [ -n "$POD" ]; do
  [ $(($(date +%s) - t0)) -lt 900 ] || { echo "cluster-gate: no pod for $JOB after 900s" >&2; exit 1; }
  sleep 3
done
kc wait --for=condition=Ready "pod/$POD" --timeout=600s >/dev/null || { echo "cluster-gate: pod $POD never became Ready (queued? PVC unbound?)" >&2; exit 1; }
kc exec -i "$POD" -c gate -- sh -c 'mkdir -p /work/src && tar -xz --no-same-owner -C /work/src' <"$ARCHIVE"
kc exec "$POD" -c gate -- touch /work/ready

# job_state: "True/" for Complete, "/True" for Failed, "/" while running. A dropped connection reads as running.
job_state() { kc get job "$JOB" -o jsonpath='{.status.conditions[?(@.type=="Complete")].status}/{.status.conditions[?(@.type=="Failed")].status}' 2>/dev/null || true; }

# a dropped stream is not a failed gate: the Job's own state below decides
kc logs -f "$POD" -c gate 2>&1 | tee "$LOG" || true
state=$(job_state)
case $state in True/* | */True) ;; *) echo "cluster-gate: log stream ended before the Job did; waiting for the Job" ;; esac
for _ in $(seq 1 $(((DEADLINE + 600) / 10))); do
  case $state in True/* | */True) break ;; esac
  sleep 10
  state=$(job_state)
done
# the verdict comes from the Job's state, not from this fetch
kc logs "$POD" -c gate >"$LOG" 2>&1 || true

case $state in
  True/*) verdict=0; detail=PASS ;;
  */True) verdict=1; detail="FAIL ($(kc get job "$JOB" -o jsonpath='{.status.conditions[?(@.type=="Failed")].reason}' 2>/dev/null || true))" ;; # the reason only decorates a verdict already FAIL
  *) verdict=1; detail="UNKNOWN (no terminal Job condition; check the Job by name)" ;;
esac

echo "== cluster gate: $SHORT, $(($(date +%s) - START))s wall, $JOB =="
# no match when the pod died before printing a step; the verdict above does not depend on it
grep -E '^GATE_(STEP|SECONDS)' "$LOG" || true
if [ "$verdict" != 0 ]; then
  # no match when the failure was not a test failure; the verdict is already FAIL
  grep -hE -e '--- FAIL' -e '\[FAIL\]' -e $'FAIL\t' "$LOG" | sort -u || true
fi
echo "cluster-gate: $detail (log $LOG)"
exit $verdict
