# Image URL to use all building/pushing image targets
IMG ?= controller:latest
# Image URL for the ichor-ui image target (ADR 0020)
IMG_UI ?= ichor-ui:latest
# YEAR defines the year value used for substituting the YEAR placeholder in the boilerplate header.
YEAR ?= $(shell date +%Y)

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# CONTAINER_TOOL defines the container tool to be used for building images.
# Be aware that the target commands are only tested with Docker which is
# scaffolded by default. However, you might want to replace it to use other
# tools. (i.e. podman)
CONTAINER_TOOL ?= docker

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk command is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: controller-gen manifests-ichor-ui ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	"$(CONTROLLER_GEN)" rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases
	$(MAKE) sync-crds

.PHONY: sync-crds
sync-crds: ## Sync config/crd/bases/ into dist/chart/templates/crd/ (CONTRIBUTING.md explains why, in place of kubebuilder edit --plugins=helm/v2-alpha).
	go run ./internal/tools/synccrds

.PHONY: adr-index
adr-index: ## Regenerate docs/adr/README.md's index table from each ADR's own Decided:/Status: line.
	go run ./internal/tools/adrindex

.PHONY: generate
generate: controller-gen ## Generate mirror types (go generate) plus DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	go generate ./...
	"$(CONTROLLER_GEN)" object:headerFile="hack/boilerplate.go.txt",year=$(YEAR) paths="./..."

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...
	# internal/uiapi/rbac_markers.go lives behind the ichorui_rbac build tag (ADR 0018) so
	# make manifests never merges its markers into manager-role; that tag also means the
	# file is never type-checked by a plain "go vet ./..." (or "go build"/"go test"). This
	# is the one place that compiles it, so a syntax or type error in it is still caught.
	go vet -tags ichorui_rbac ./internal/uiapi/...

.PHONY: test
test: manifests generate fmt vet setup-envtest ## Run tests.
	KUBEBUILDER_ASSETS="$(shell "$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path)" go test $$(go list ./... | grep -v /e2e) -coverprofile cover.out

# e2e takes the cluster it is given through KUBECONFIG rather than creating one (ADR 0007):
# it runs Talos nodes as KubeVirt VMs on a real cluster, not talosctl's qemu provisioner.
# kubectl kuberc is disabled by default for test isolation; enable with:
# - KUBECTL_KUBERC=true

.PHONY: test-e2e-prereqs
test-e2e-prereqs: ## Verify KUBECONFIG points at a cluster with KubeVirt, CDI and the ichor-e2e namespace
	@test -n "$$KUBECONFIG" || { \
		echo "test-e2e: KUBECONFIG is not set. e2e takes the cluster it is given through KUBECONFIG (see docs/adr/0007); export it before running."; \
		exit 1; \
	}
	@test -r "$$KUBECONFIG" || { \
		echo "test-e2e: KUBECONFIG=$$KUBECONFIG is not a readable kubeconfig file."; \
		exit 1; \
	}
	@$(KUBECTL) get namespace ichor-e2e >/dev/null 2>&1 || { \
		echo "test-e2e: namespace 'ichor-e2e' was not found on the cluster at KUBECONFIG=$$KUBECONFIG. Create it before running e2e (see docs/adr/0007)."; \
		exit 1; \
	}
	@$(KUBECTL) get crd virtualmachines.kubevirt.io datavolumes.cdi.kubevirt.io >/dev/null 2>&1 || { \
		echo "test-e2e: KubeVirt and/or CDI CRDs were not found on the cluster at KUBECONFIG=$$KUBECONFIG. e2e runs Talos nodes as KubeVirt VMs (ADR 0007) and needs both installed."; \
		exit 1; \
	}

.PHONY: test-e2e
test-e2e: test-e2e-prereqs manifests generate fmt vet ## Run the PR-sized (short) e2e set against the cluster given by KUBECONFIG.
	go test -tags=e2e ./test/e2e/ -v -ginkgo.v -ginkgo.label-filter=short

.PHONY: test-e2e-nightly
test-e2e-nightly: test-e2e-prereqs manifests generate fmt vet ## Run the full (short + nightly) e2e set against the cluster given by KUBECONFIG.
	go test -tags=e2e ./test/e2e/ -v -ginkgo.v -ginkgo.label-filter="short || nightly"

# Neither harness may provision inside the specs (ADR 0007): a KubeVirt VM is meaningless
# on a CI runner with no KubeVirt, and talosctl's qemu provisioner is meaningless on the
# real cluster. So each harness gets its own setup/cleanup pair, each doing exactly one
# thing and safe to call on its own - test-e2e itself never calls either, it only ever
# runs against whatever KUBECONFIG, ICHOR_E2E_TALOS_NODES, ICHOR_E2E_INNER_KUBECONFIG and
# ICHOR_E2E_TALOSCONFIG already point at.

# --- KubeVirt harness (talos-cluster01, by hand or in CI against that cluster) ---

.PHONY: kubevirt-harness
kubevirt-harness: ## Build the KubeVirt e2e harness binary
	go build -o "$(LOCALBIN)/kubevirt-harness" ./test/harness/kubevirt

.PHONY: setup-test-e2e-kubevirt
setup-test-e2e-kubevirt: test-e2e-prereqs kubevirt-harness ## Boot a single-node Talos control plane as a KubeVirt VM, configure and bootstrap it; export ICHOR_E2E_TALOS_NODES, ICHOR_E2E_INNER_KUBECONFIG and ICHOR_E2E_TALOSCONFIG
	# Not "go run": it wraps the binary in its own process and does not reliably forward
	# SIGTERM to it (observed for real - the harness's own signal-triggered cleanup never
	# ran), which is exactly the case that must not leave the VM/DataVolumes behind.
	"$(LOCALBIN)/kubevirt-harness" up

.PHONY: cleanup-test-e2e-kubevirt
cleanup-test-e2e-kubevirt: test-e2e-prereqs kubevirt-harness ## Tear down the VM, its relay pod and port-forward, and the DataVolumes setup-test-e2e-kubevirt created; a no-op if there is nothing to tear down
	"$(LOCALBIN)/kubevirt-harness" down

# --- KubeVirt harness, three control planes (nightly only: ~40Gi of Ceph for a few
# minutes, for the removal-ordering scenarios design's "Nodes" section describes that need
# real etcd membership and a real kubelet drain -- envtest has neither) ---

.PHONY: setup-test-e2e-kubevirt-ha
setup-test-e2e-kubevirt-ha: test-e2e-prereqs kubevirt-harness ## Boot three Talos control planes as KubeVirt VMs, configure and bootstrap them; export ICHOR_E2E_HA_TALOSCONFIG and ICHOR_E2E_HA_KUBECONFIG
	"$(LOCALBIN)/kubevirt-harness" up-ha

.PHONY: cleanup-test-e2e-kubevirt-ha
cleanup-test-e2e-kubevirt-ha: test-e2e-prereqs kubevirt-harness ## Tear down the three VMs and their DataVolumes (including the shared ISO); a no-op if there is nothing to tear down
	"$(LOCALBIN)/kubevirt-harness" down-ha

# --- talosctl-qemu harness (CI, GitHub's ubuntu-24.04 runners) ---
TEST_E2E_KUBECONFIG ?= $(LOCALBIN)/test-e2e.kubeconfig

.PHONY: setup-test-e2e
setup-test-e2e: ## Provision the throwaway qemu Talos cluster CI's e2e job runs against; publishes KUBECONFIG and ICHOR_E2E_TALOS_NODES
	KUBECONFIG=$(TEST_E2E_KUBECONFIG) talosctl cluster create qemu --name ichor-test-e2e --workers 1 | tee $(LOCALBIN)/test-e2e-create.log
	@if [ -n "$$GITHUB_ENV" ]; then echo "KUBECONFIG=$(TEST_E2E_KUBECONFIG)" >> "$$GITHUB_ENV"; fi
	# shortcut: scrapes the worker's address out of "talosctl cluster create"'s
	# human-readable summary table, a format never observed (this sandbox has no root to
	# run talosctl cluster create qemu) and not a documented interface; upgrade the first
	# time a QEMU-capable CI run goes red because the scrape found nothing, by replacing
	# it with the provisioner's state directory under ~/.talos/clusters/<name>/ or a
	# machine-readable talosctl output if one exists by then.
	$(eval TEST_E2E_WORKER := $(shell grep -i worker $(LOCALBIN)/test-e2e-create.log | grep -oE '[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+' | head -1))
	talosctl --context ichor-test-e2e reset --nodes $(TEST_E2E_WORKER) --graceful=false --reboot --wait
	@if [ -n "$$GITHUB_ENV" ]; then echo "ICHOR_E2E_TALOS_NODES=$(TEST_E2E_WORKER)" >> "$$GITHUB_ENV"; fi

.PHONY: cleanup-test-e2e
cleanup-test-e2e: ## Tear down the cluster setup-test-e2e provisioned; a no-op if there is nothing to tear down
	@KUBECONFIG=$(TEST_E2E_KUBECONFIG) talosctl cluster destroy --name ichor-test-e2e 2>/dev/null || true

.PHONY: lint
lint: golangci-lint ## Run golangci-lint linter
	"$(GOLANGCI_LINT)" run

.PHONY: lint-fix
lint-fix: golangci-lint ## Run golangci-lint linter and perform fixes
	"$(GOLANGCI_LINT)" run --fix

.PHONY: lint-config
lint-config: golangci-lint ## Verify golangci-lint linter configuration
	"$(GOLANGCI_LINT)" config verify

##@ Build

.PHONY: build
build: manifests generate fmt vet ## Build manager binary.
	go build -o bin/manager cmd/main.go

.PHONY: build-ichor
build-ichor: fmt vet ## Build the ichor CLI binary (placeholder install manifest; see build-ichor-embed).
	go build -o bin/ichor ./cmd/ichor

.PHONY: build-ichor-embed
build-ichor-embed: build-installer ## Build the ichor CLI with Ichor's own install manifest embedded (what `ichor init` applies).
	cp dist/install.yaml internal/installmanifest/install.yaml
	go build -tags installembed -o bin/ichor ./cmd/ichor

.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host.
	go run ./cmd/main.go

# If you wish to build the manager image targeting other platforms you can use the --platform flag.
# (i.e. docker build --platform linux/arm64). However, you must enable docker buildKit for it.
# More info: https://docs.docker.com/develop/develop-images/build_enhancements/
.PHONY: docker-build
docker-build: ## Build docker image with the manager.
	$(CONTAINER_TOOL) build -t ${IMG} .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	$(CONTAINER_TOOL) push ${IMG}

.PHONY: docker-build-ichor-ui
docker-build-ichor-ui: ## Build docker image with ichor-ui, the real UI embedded (not the placeholder; ADR 0020).
	$(CONTAINER_TOOL) build -f Dockerfile.ichor-ui -t ${IMG_UI} .

# PLATFORMS defines the target platforms for the manager image be built to provide support to multiple
# architectures. (i.e. make docker-buildx IMG=myregistry/mypoperator:0.0.1). To use this option you need to:
# - be able to use docker buildx. More info: https://docs.docker.com/build/buildx/
# - have enabled BuildKit. More info: https://docs.docker.com/develop/develop-images/build_enhancements/
# - be able to push the image to your registry (i.e. if you do not set a valid value via IMG=<myregistry/image:<tag>> then the export will fail)
# To adequately provide solutions that are compatible with multiple platforms, you should consider using this option.
PLATFORMS ?= linux/arm64,linux/amd64,linux/s390x,linux/ppc64le
.PHONY: docker-buildx
docker-buildx: ## Build and push docker image for the manager for cross-platform support
	# copy existing Dockerfile and insert --platform=${BUILDPLATFORM} into Dockerfile.cross, and preserve the original Dockerfile
	sed -e '1 s/\(^FROM\)/FROM --platform=\$$\{BUILDPLATFORM\}/; t' -e ' 1,// s//FROM --platform=\$$\{BUILDPLATFORM\}/' Dockerfile > Dockerfile.cross
	- $(CONTAINER_TOOL) buildx create --name ichor-builder
	$(CONTAINER_TOOL) buildx use ichor-builder
	- $(CONTAINER_TOOL) buildx build --push --platform=$(PLATFORMS) --tag ${IMG} -f Dockerfile.cross .
	- $(CONTAINER_TOOL) buildx rm ichor-builder
	rm Dockerfile.cross

.PHONY: build-installer
build-installer: manifests generate kustomize ## Generate a consolidated YAML with CRDs and deployment.
	mkdir -p dist
	cd config/manager && "$(KUSTOMIZE)" edit set image controller=${IMG}
	cd config/manager-ui && "$(KUSTOMIZE)" edit set image ichor-ui=${IMG_UI}
	"$(KUSTOMIZE)" build config/default > dist/install.yaml

##@ Deployment

ifndef ignore-not-found
  ignore-not-found = false
endif

.PHONY: install
install: manifests kustomize ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	@crds="$$( "$(KUSTOMIZE)" build config/crd 2>/dev/null || true )"; \
	if [ -n "$$crds" ]; then echo "$$crds" | "$(KUBECTL)" apply -f -; else echo "No CRDs to install; skipping."; fi

.PHONY: uninstall
uninstall: manifests kustomize ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	@crds="$$( "$(KUSTOMIZE)" build config/crd 2>/dev/null || true )"; \
	if [ -n "$$crds" ]; then echo "$$crds" | "$(KUBECTL)" delete --ignore-not-found=$(ignore-not-found) -f -; else echo "No CRDs to delete; skipping."; fi

.PHONY: deploy
deploy: manifests kustomize ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	cd config/manager && "$(KUSTOMIZE)" edit set image controller=${IMG}
	cd config/manager-ui && "$(KUSTOMIZE)" edit set image ichor-ui=${IMG_UI}
	"$(KUSTOMIZE)" build config/default | "$(KUBECTL)" apply -f -

.PHONY: undeploy
undeploy: kustomize ## Undeploy controller from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	"$(KUSTOMIZE)" build config/default | "$(KUBECTL)" delete --ignore-not-found=$(ignore-not-found) -f -

##@ Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p "$(LOCALBIN)"

## Tool Binaries
KUBECTL ?= kubectl
KUSTOMIZE ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest
GOLANGCI_LINT ?= $(LOCALBIN)/golangci-lint

## Tool Versions
KUSTOMIZE_VERSION ?= v5.8.1
CONTROLLER_TOOLS_VERSION ?= v0.21.0

#ENVTEST_VERSION is the controller-runtime version to use for setup-envtest, derived from go.mod
ENVTEST_VERSION ?= $(shell v='$(call gomodver,sigs.k8s.io/controller-runtime)'; \
  [ -n "$$v" ] || { echo "Set ENVTEST_VERSION manually (controller-runtime replace has no tag)" >&2; exit 1; }; \
  printf '%s\n' "$$v")

#ENVTEST_K8S_VERSION is the version of Kubernetes to use for setting up ENVTEST binaries (i.e. 1.31)
ENVTEST_K8S_VERSION ?= $(shell v='$(call gomodver,k8s.io/api)'; \
  [ -n "$$v" ] || { echo "Set ENVTEST_K8S_VERSION manually (k8s.io/api replace has no tag)" >&2; exit 1; }; \
  printf '%s\n' "$$v" | sed -E 's/^v?[0-9]+\.([0-9]+).*/1.\1/')

GOLANGCI_LINT_VERSION ?= v2.15.0
.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Download kustomize locally if necessary.
$(KUSTOMIZE): $(LOCALBIN)
	$(call go-install-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v5,$(KUSTOMIZE_VERSION))

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

.PHONY: setup-envtest
setup-envtest: envtest ## Download the binaries required for ENVTEST in the local bin directory.
	@echo "Setting up envtest binaries for Kubernetes version $(ENVTEST_K8S_VERSION)..."
	@"$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path || { \
		echo "Error: Failed to set up envtest binaries for version $(ENVTEST_K8S_VERSION)."; \
		exit 1; \
	}

.PHONY: envtest
envtest: $(ENVTEST) ## Download setup-envtest locally if necessary.
$(ENVTEST): $(LOCALBIN)
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT): $(LOCALBIN)
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/v2/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))
	@test -f .custom-gcl.yml && { \
		echo "Building custom golangci-lint with plugins..." && \
		$(GOLANGCI_LINT) custom --destination $(LOCALBIN) --name golangci-lint-custom && \
		mv -f $(LOCALBIN)/golangci-lint-custom $(GOLANGCI_LINT); \
	} || true

# go-install-tool will 'go install' any package with custom target and name of binary, if it doesn't exist
# $1 - target path with name of binary
# $2 - package url which can be installed
# $3 - specific version of package
define go-install-tool
@[ -f "$(1)-$(3)" ] && [ "$$(readlink -- "$(1)" 2>/dev/null)" = "$(1)-$(3)" ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
rm -f "$(1)" ;\
GOBIN="$(LOCALBIN)" go install $${package} ;\
mv "$(LOCALBIN)/$$(basename "$(1)")" "$(1)-$(3)" ;\
} ;\
ln -sf "$$(realpath "$(1)-$(3)")" "$(1)"
endef

define gomodver
$(shell go list -m -f '{{if .Replace}}{{.Replace.Version}}{{else}}{{.Version}}{{end}}' $(1) 2>/dev/null)
endef

##@ Helm Deployment

## Helm binary to use for deploying the chart
HELM ?= helm
## Namespace to deploy the Helm release
HELM_NAMESPACE ?= ichor-system
## Name of the Helm release
HELM_RELEASE ?= ichor
## Path to the Helm chart directory
HELM_CHART_DIR ?= dist/chart
## Additional arguments to pass to helm commands
HELM_EXTRA_ARGS ?=

.PHONY: install-helm
install-helm: ## Install the latest version of Helm.
	@command -v $(HELM) >/dev/null 2>&1 || { \
		echo "Installing Helm..." && \
		curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-4 | bash; \
	}

.PHONY: helm-deploy
helm-deploy: install-helm ## Deploy manager to the K8s cluster via Helm. Specify an image with IMG.
	$(HELM) upgrade --install $(HELM_RELEASE) $(HELM_CHART_DIR) \
		--namespace $(HELM_NAMESPACE) \
		--create-namespace \
		--set manager.image.repository=$${IMG%:*} \
		--set manager.image.tag=$${IMG##*:} \
		--wait \
		--timeout 5m \
		$(HELM_EXTRA_ARGS)

.PHONY: helm-uninstall
helm-uninstall: ## Uninstall the Helm release from the K8s cluster.
	$(HELM) uninstall $(HELM_RELEASE) --namespace $(HELM_NAMESPACE)

.PHONY: helm-status
helm-status: ## Show Helm release status.
	$(HELM) status $(HELM_RELEASE) --namespace $(HELM_NAMESPACE)

.PHONY: helm-history
helm-history: ## Show Helm release history.
	$(HELM) history $(HELM_RELEASE) --namespace $(HELM_NAMESPACE)

.PHONY: helm-rollback
helm-rollback: ## Rollback to previous Helm release.
	$(HELM) rollback $(HELM_RELEASE) --namespace $(HELM_NAMESPACE)

##@ Connect API (proto)

.PHONY: proto-generate
proto-generate: ## Regenerate Go types and Connect handlers/clients from proto/ into gen/go (ADR 0016).
	buf generate

.PHONY: proto-lint
proto-lint: ## Lint the proto/ sources with buf.
	buf lint

.PHONY: proto-breaking
proto-breaking: ## Check proto/ for breaking changes against main (ADR 0016); a no-op until proto/ lands on main.
	@if git cat-file -e main:proto 2>/dev/null; then \
		buf breaking proto --against '.git#branch=main,subdir=proto'; \
	else \
		echo "proto-breaking: proto/ does not exist on main yet; nothing to compare"; \
	fi
##@ UI

# ui/ (Vite + Preact + TypeScript, preactcn components) is embedded into the
# ichor-ui binary (ADR 0017). None of these targets are prerequisites of the
# existing `build`/`test`/`lint` targets above: cmd/ichor-ui builds against a
# committed placeholder (see cmd/ichor-ui/webfs_dev.go) when the UI hasn't
# been built, so Go-only workflows never need npm.

.PHONY: ui-install
ui-install: ## Install UI dependencies (npm ci).
	cd ui && npm ci

.PHONY: ui-build
ui-build: ui-install ## Build the UI; Vite writes straight into cmd/ichor-ui/dist.
	cd ui && npm run build

.PHONY: ui-typecheck
ui-typecheck: ui-install ## Type-check the UI (tsc --noEmit).
	cd ui && npm run typecheck

.PHONY: ui-lint
ui-lint: ui-install ## Lint the UI (eslint).
	cd ui && npm run lint

.PHONY: ui-ci
ui-ci: ui-typecheck ui-lint ui-build ## Install, type-check, lint and build the UI, in order.

.PHONY: build-ichor-ui
build-ichor-ui: ui-build ## Build the ichor-ui binary with the real UI embedded.
	go build -tags uiembed -o bin/ichor-ui ./cmd/ichor-ui

##@ ichor-ui RBAC (ADR 0018)

# internal/uiapi's own +kubebuilder:rbac markers live behind the ichorui_rbac build tag
# (see internal/uiapi/rbac_markers.go) so the `manifests` target above -- which scans
# paths="./..." with no special tags -- never sees them and never merges them into
# manager-role. This target is the only one that passes --load-build-tags=ichorui_rbac,
# so the UI backend's ClusterRole stays in its own file, generated on its own.

.PHONY: manifests-ichor-ui
manifests-ichor-ui: controller-gen ## Generate the UI backend's own ClusterRole, kept separate from manager-role.
	"$(CONTROLLER_GEN)" --load-build-tags=ichorui_rbac rbac:roleName=ichor-ui-role paths="./internal/uiapi/..." output:dir=config/rbac/ichor-ui

##@ UI screens (ADR 0021)

.PHONY: ui-test
ui-test: ui-install ## Run the UI's own unit tests (node --test; no test framework dependency added).
	cd ui && npm test

.PHONY: ui-proto-generate
ui-proto-generate: ui-install ## Regenerate the TS Connect client into ui/src/gen (ADR 0021). Run before plain `buf generate`/`make proto-generate` if ui/node_modules is missing - buf.gen.yaml's TS plugin resolves to ui/node_modules/.bin/protoc-gen-es.
	buf generate
