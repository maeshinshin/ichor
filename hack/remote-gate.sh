#!/usr/bin/env bash
# Run the full gate (go test, lint, ui-ci) for the current HEAD as parallel Jobs
# in the homelab cluster's ichor-e2e namespace; see CONTRIBUTING.md.
#   make remote-gate        (GATE_SHARDS="ctl:10 cli:4 uiapi:2 rest:3" to resize)
# The same file runs inside each pod as `remote-gate.sh pod <group> <k> <n>`.
# Logs land in remote-gate-out/<shard>.log. Only uncommitted work is NOT tested:
# the source is `git archive HEAD`.
set -euo pipefail

GOVER=$(awk '/^go /{print $2}' go.mod)
NODE_IMAGE=${NODE_IMAGE:-node:24.21.0} # the flake's nodejs
BUF=v1.73.0
HELM=v4.3.1
ENVTEST=${ENVTEST:-1.36.2-linux-amd64} # the dir under bin/k8s, shipped from the workstation
ENVTEST_SRC=${ENVTEST_SRC:-bin/k8s}
GATE_SHARDS=${GATE_SHARDS:-ctl:10 cli:4 uiapi:2 rest:3}
CPU=${GATE_CPU:-2}
MEM=${GATE_MEM:-3Gi}
MEML=${GATE_MEM_LIMIT:-8Gi}

# --- inside the pod ---------------------------------------------------------

# split_test <pkgdir> <k> <n>: run shard k of n of one package. Plain Test funcs go round-robin;
# Ginkgo specs are split by file (-ginkgo.focus-file), biggest files first, snake order.
split_test() {
  local d=$1 k=$2 n=$3 funcs suites files re=''
  suites=$(awk '/^func Test/{f=$2;sub(/\(.*/,"",f)} /RunSpecs\(/{print f}' "$d"/*_test.go | sort -u)
  funcs=$(grep -h '^func Test' "$d"/*_test.go | sed -E 's/^func (Test[A-Za-z0-9_]*)\(.*/\1/' |
    sort | grep -vxF -e TestMain ${suites:+-e "$suites"} | awk -v k="$k" -v n="$n" 'NR%n==k' || true)
  # shellcheck disable=SC2012
  mapfile -t sorted < <(ls -S "$d"/*_test.go)
  files=$(grep -l '^var _ = Describe' "${sorted[@]}" || true)
  if [ -n "$files" ]; then
    re=$(echo "$files" | xargs -n1 basename | sed 's/\.go$//' | awk -v k="$k" -v n="$n" \
      '{r=(NR-1)%(2*n); if ((r<n?r:2*n-1-r)==k) {printf "%s%s",s,$0; s="|"}}')
  fi
  local run
  run=$(echo "$funcs ${re:+$suites}" | xargs | tr ' ' '|')
  [ -n "$run" ] || { echo "empty shard"; return 0; }
  go test -count=1 -v -timeout 30m "$d" -run "^($run)\$" ${re:+-ginkgo.focus-file="/($re)\\.go\$"}
}

fetch() { curl -fsSL "$1" | tar xz -C /usr/local/bin --strip-components="$2" "$3"; }

pod_main() {
  local group=$1 k=$2 n=$3
  export HOME=/w GOCACHE=/w/gocache GOMODCACHE=/w/gomod GOFLAGS=-buildvcs=false
  export KUBEBUILDER_ASSETS=$PWD/bin/k8s/$ENVTEST PATH=$PWD/bin/k8s/$ENVTEST:$PATH # its kubectl too
  case $group in
  ctl) split_test ./internal/controller "$k" "$n" ;;
  cli) split_test ./internal/cli "$k" "$n" ;;
  uiapi) split_test ./internal/uiapi "$k" "$n" ;;
  rest) # the rest of `go list ./...` minus e2e and the packages the groups above or inst cover
    # shellcheck disable=SC2046
    go test -count=1 -timeout 30m $(go list ./... | grep -v -e /e2e -e internal/controller -e internal/cli \
      -e internal/uiapi -e dist/chart -e internal/installer -e test/harness/ui-demo | awk -v k="$k" -v n="$n" 'NR%n==k') ;;
  inst) # the packages that read dist/install.yaml or shell out to helm
    fetch "https://get.helm.sh/helm-$HELM-linux-amd64.tar.gz" 1 linux-amd64/helm
    make build-installer
    go test -count=1 -timeout 30m ./dist/chart/... ./internal/installer/... ./test/harness/ui-demo/... ;;
  lint)
    curl -fsSL -o /usr/local/bin/buf "https://github.com/bufbuild/buf/releases/download/$BUF/buf-Linux-x86_64"
    chmod +x /usr/local/bin/buf
    go mod tidy -diff && go mod verify && make lint-config lint vet ;;
  ui) make ui-ci ;;
  esac
}

if [ "${1:-}" = pod ]; then pod_main "$2" "$3" "$4"; exit; fi

# --- on the workstation -----------------------------------------------------

k() { kubectl --context admin@talos-cluster01 -n ichor-e2e "$@"; }
RUN=$(date +%d%H%M%S)
OUT=remote-gate-out
JOBS=()
mkdir -p "$OUT"
rm -f "$OUT"/*.log "$OUT"/*.status
START=$(date +%s)

# shellcheck disable=SC2329
cleanup() { # kill the shard waiters, then delete only the Jobs this run created, by explicit name (their pods go with them)
  jobs -p | xargs -r kill 2>/dev/null || true
  [ ${#JOBS[@]} -eq 0 ] || k delete job --ignore-not-found --wait=false "${JOBS[@]}" >/dev/null || true
}
trap cleanup EXIT
trap 'exit 130' INT TERM

git archive HEAD | gzip -1 >"$OUT/src.tgz"
tar -C "$ENVTEST_SRC" -cf - "$ENVTEST" | gzip -1 >"$OUT/envtest.tgz"

# job <name> <image> <group> <k> <n>: print the Job; its container waits for the source to be exec'd in.
job() {
  cat <<EOF
apiVersion: batch/v1
kind: Job
metadata:
  name: $1
  labels: {app.kubernetes.io/name: ichor-gate}
spec:
  backoffLimit: 0
  activeDeadlineSeconds: 3600
  ttlSecondsAfterFinished: 3600
  template:
    metadata:
      labels: {app.kubernetes.io/name: ichor-gate}
    spec:
      restartPolicy: Never
      automountServiceAccountToken: false # else helm and kubectl in the pod default to the ichor-e2e namespace
      containers:
      - name: gate
        image: $2
        command: [bash, -c, "mkdir -p /w/src; until [ -e /w/ready ]; do sleep 1; done; cd /w/src && exec bash hack/remote-gate.sh pod $3 $4 $5"]
        env: [{name: ENVTEST, value: "$ENVTEST"}]
        resources:
          requests: {cpu: "$CPU", memory: $MEM, ephemeral-storage: 8Gi}
          limits: {memory: $MEML, ephemeral-storage: 30Gi}
        volumeMounts: [{name: w, mountPath: /w}]
      volumes: [{name: w, emptyDir: {sizeLimit: 30Gi}}]
EOF
}

# shard <label> <image> <group> <k> <n>: run one Job to the end, log to $OUT/<label>.log, record PASS/FAIL.
shard() {
  local name=ichor-gate-$RUN-$1 pod t0 t1 rc=FAIL
  t0=$(date +%s)
  job "$name" "$2" "$3" "$4" "$5" | k apply -f - >/dev/null
  until pod=$(k get pod -l job-name="$name" -o name 2>/dev/null) && [ -n "$pod" ]; do sleep 1; done
  k wait --for=condition=Ready "$pod" --timeout=20m >/dev/null
  t1=$(date +%s)
  k exec -i "$pod" -- sh -c 'mkdir -p /w/src && tar xz -C /w/src' <"$OUT/src.tgz"
  case $3 in lint | ui) ;; *)
    k exec -i "$pod" -- sh -c 'mkdir -p /w/src/bin/k8s && tar xz -C /w/src/bin/k8s' <"$OUT/envtest.tgz" ;;
  esac
  k exec "$pod" -- touch /w/ready
  k logs -f "$pod" >"$OUT/$1.log" 2>&1 || true
  k wait --for=condition=complete "job/$name" --timeout=120s >/dev/null 2>&1 && rc=PASS
  echo "$rc $(($(date +%s) - t1))s (+$((t1 - t0))s queued for a node)" >"$OUT/$1.status"
}

launch() { JOBS+=("ichor-gate-$RUN-$1"); shard "$@" & }

GOIMG=golang:$GOVER
launch lint "$GOIMG" lint 0 1
launch ui "$NODE_IMAGE" ui 0 1
launch inst "$GOIMG" inst 0 1
LABELS=(lint ui inst)
for g in $GATE_SHARDS; do
  for ((i = 0; i < ${g#*:}; i++)); do
    launch "${g%:*}$i" "$GOIMG" "${g%:*}" "$i" "${g#*:}"
    LABELS+=("${g%:*}$i")
  done
done
wait

fails=0
echo "== remote gate: $(git rev-parse --short HEAD), $(($(date +%s) - START))s wall =="
for l in "${LABELS[@]}"; do
  s=$(cat "$OUT/$l.status" 2>/dev/null || echo "FAIL ?")
  printf '%-8s %s\n' "$l" "$s"
  [ "${s%% *}" = PASS ] || fails=1
done
if [ $fails = 1 ]; then
  echo "== failing tests =="
  grep -hE -e '--- FAIL' -e '\[FAIL\]' -e $'FAIL\t' "$OUT"/*.log | sort -u || true
fi
exit $fails
