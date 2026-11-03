# Image URL to use all building/pushing image targets
IMG ?= controller:latest
# Image URL for the ichor-ui image target
IMG_UI ?= ichor-ui:latest
# Image URL for the installer image target
IMG_INSTALLER ?= ichor-installer:latest
# VERSION is linked into every Ichor binary (internal/version); "dev" means unversioned.
VERSION ?= dev
LDFLAGS := -X github.com/maeshinshin/ichor/internal/version.Version=$(VERSION)
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
	"$(CONTROLLER_GEN)" rbac:roleName=manager-role crd webhook paths="$(CONTROLLER_GEN_PATHS)" output:crd:artifacts:config=config/crd/bases
	$(MAKE) sync-crds

.PHONY: sync-crds
sync-crds: ## Sync config/crd/bases/ into dist/chart/templates/crd/ (CONTRIBUTING.md explains why, in place of kubebuilder edit --plugins=helm/v2-alpha).
	go run ./internal/tools/synccrds

CILIUM_CHART_VERSION ?= 1.20.4
CILIUM_CHART_SHA256 ?= 505abff8385811df1a949776fc42e9f10697989a332acc86d6e8933efcb855ed

.PHONY: cilium-chart
cilium-chart: ## Fetch the pinned Cilium chart into pkg/cni/cilium/chart/ and verify its SHA-256.
	curl -fsSL -o pkg/cni/cilium/chart/cilium-$(CILIUM_CHART_VERSION).tgz https://helm.cilium.io/cilium-$(CILIUM_CHART_VERSION).tgz
	echo "$(CILIUM_CHART_SHA256)  pkg/cni/cilium/chart/cilium-$(CILIUM_CHART_VERSION).tgz" | sha256sum -c -

.PHONY: adr-index
adr-index: ## Regenerate docs/adr/README.md's index table from each ADR's own Decided:/Status: line.
	go run ./internal/tools/adrindex

.PHONY: generate
generate: controller-gen ## Generate mirror types (go generate) plus DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	go generate ./...
	"$(CONTROLLER_GEN)" object:headerFile="hack/boilerplate.go.txt",year=$(YEAR) paths="$(CONTROLLER_GEN_PATHS)"

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...
	# internal/uiapi/rbac_markers.go lives behind the ichorui_rbac build tag so
	# make manifests never merges its markers into manager-role; that tag also means the
	# file is never type-checked by a plain "go vet ./..." (or "go build"/"go test"). This
	# is the one place that compiles it, so a syntax or type error in it is still caught.
	go vet -tags ichorui_rbac ./internal/uiapi/...

.PHONY: test
test: manifests generate fmt vet setup-envtest build-installer ## Run tests.
	KUBEBUILDER_ASSETS="$(shell "$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path)" go test $$(go list ./... | grep -v /e2e) -coverprofile cover.out

.PHONY: test-e2e
test-e2e: test-e2e-prereqs manifests generate fmt vet ## Run the PR-sized (short) e2e set against the cluster given by KUBECONFIG.
	go test -tags=e2e ./test/e2e/ -v -ginkgo.v -ginkgo.label-filter=short

.PHONY: test-e2e-nightly
test-e2e-nightly: test-e2e-prereqs manifests generate fmt vet ## Run the full (short + nightly) e2e set against the cluster given by KUBECONFIG.
	go test -tags=e2e ./test/e2e/ -v -ginkgo.v -ginkgo.label-filter="short || nightly"

.PHONY: controller-static
controller-static: ## Build a statically linked controller binary to copy into the verification pod (not an image)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o "$(LOCALBIN)/manager-static" cmd/main.go

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
	@test -n "$(TEST_E2E_WORKER)" || { echo "setup-test-e2e: no worker address found in $(LOCALBIN)/test-e2e-create.log; refusing to run talosctl reset without an explicit node"; exit 1; }
	talosctl --context ichor-test-e2e reset --nodes $(TEST_E2E_WORKER) --graceful=false --reboot --wait
	@if [ -n "$$GITHUB_ENV" ]; then echo "ICHOR_E2E_TALOS_NODES=$(TEST_E2E_WORKER)" >> "$$GITHUB_ENV"; fi

.PHONY: cleanup-test-e2e
cleanup-test-e2e: ## Tear down the cluster setup-test-e2e provisioned; a no-op if there is nothing to tear down
	@KUBECONFIG=$(TEST_E2E_KUBECONFIG) talosctl cluster destroy --name ichor-test-e2e 2>/dev/null || true

.PHONY: lint
lint: golangci-lint proto-lint ## Run golangci-lint and buf lint (proto-lint is a prerequisite so CI catches proto drift)
	"$(GOLANGCI_LINT)" run

.PHONY: lint-config
lint-config: golangci-lint ## Verify golangci-lint linter configuration
	"$(GOLANGCI_LINT)" config verify

##@ Build

.PHONY: build
build: manifests generate fmt vet ## Build manager binary.
	go build -ldflags "$(LDFLAGS)" -o bin/manager ./cmd

.PHONY: build-ichor
build-ichor: fmt vet ## Build the ichor CLI binary (placeholder install manifest; see build-ichor-embed).
	go build -ldflags "$(LDFLAGS)" -o bin/ichor ./cmd/ichor

.PHONY: build-ichor-embed
build-ichor-embed: build-installer ## Build the ichor CLI with Ichor's own install manifest embedded (what `ichor init` applies).
	cp dist/install.yaml internal/installmanifest/install.yaml
	cp dist/network-policies.yaml internal/installmanifest/network-policies.yaml
	go build -tags installembed -ldflags "$(LDFLAGS)" -o bin/ichor ./cmd/ichor

.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host.
	go run ./cmd

# If you wish to build the manager image targeting other platforms you can use the --platform flag.
# (i.e. docker build --platform linux/arm64). However, you must enable docker buildKit for it.
# More info: https://docs.docker.com/develop/develop-images/build_enhancements/
.PHONY: docker-build
docker-build: ## Build docker image with the manager.
	$(CONTAINER_TOOL) build --build-arg VERSION=$(VERSION) -t ${IMG} .

.PHONY: docker-build-installer
docker-build-installer: build-installer ## Build the installer image: the ichor binary with this tree's install manifest embedded.
	cp dist/install.yaml internal/installmanifest/install.yaml
	cp dist/network-policies.yaml internal/installmanifest/network-policies.yaml
	$(CONTAINER_TOOL) build -f Dockerfile.installer --build-arg VERSION=$(VERSION) -t ${IMG_INSTALLER} .

.PHONY: demo-images
demo-images: ui-build ## Build the three demo images with crane and push them to REGISTRY (VERSION=v0.0.8-abc1234 REGISTRY=localhost:5000); the tree stays unchanged.
	@# CRANE_INSECURE (default --insecure, for a local registry) is read by images.sh; the release workflow sets it empty.
	$(MAKE) build-installer IMG=ghcr.io/maeshinshin/ichor-controller:$(VERSION) IMG_UI=ghcr.io/maeshinshin/ichor-ui:$(VERSION)
	VERSION="$(VERSION)" REGISTRY="$(REGISTRY)" test/harness/ui-demo/images.sh

# The release workflow runs the two halves in separate jobs: the build job fetches dependencies and holds no
# registry token, the push job holds the token and runs no dependency fetch.
.PHONY: demo-images-build
demo-images-build: ui-build ## Compile the three image binaries into bin/img (the first half of demo-images, no registry).
	$(MAKE) build-installer IMG=ghcr.io/maeshinshin/ichor-controller:$(VERSION) IMG_UI=ghcr.io/maeshinshin/ichor-ui:$(VERSION)
	VERSION="$(VERSION)" test/harness/ui-demo/images.sh build

.PHONY: demo-images-push
demo-images-push: ## Layer the binaries from demo-images-build on the pinned base and push them (DIGEST_FILE=<path> receives repo@digest lines).
	VERSION="$(VERSION)" REGISTRY="$(REGISTRY)" test/harness/ui-demo/images.sh push

.PHONY: docker-build-ichor-ui
docker-build-ichor-ui: ## Build docker image with ichor-ui, the real UI embedded (not the placeholder).
	$(CONTAINER_TOOL) build -f Dockerfile.ichor-ui -t ${IMG_UI} .

.PHONY: build-installer
build-installer: manifests generate kustomize ## Generate a consolidated YAML with CRDs and deployment.
	@# Built from a temp copy of config/, so the tracked kustomization.yaml files are never edited.
	mkdir -p dist
	@work=$$(mktemp -d) && trap 'rm -rf "$$work"' EXIT && cp -r config "$$work/config" && \
	(cd "$$work/config/manager" && "$(KUSTOMIZE)" edit set image controller=${IMG}) && \
	(cd "$$work/config/manager-ui" && "$(KUSTOMIZE)" edit set image ichor-ui=${IMG_UI}) && \
	"$(KUSTOMIZE)" build "$$work/config/installer" > dist/install.yaml && \
	"$(KUSTOMIZE)" build "$$work/config/cilium-network-policy" > dist/network-policies.yaml

##@ Deployment

# KUBE_CONTEXT is optional: when set, every kubectl call in install, uninstall, deploy and
# undeploy is pinned to that kubeconfig context; when empty, kubectl uses the current-context.
KUBE_CONTEXT ?=
KUBE_CONTEXT_FLAG = $(if $(KUBE_CONTEXT),--context $(KUBE_CONTEXT))

.PHONY: install
install: manifests kustomize ## Install CRDs into the K8s cluster specified in ~/.kube/config. Set KUBE_CONTEXT to pick a context other than the current one.
	@crds="$$( "$(KUSTOMIZE)" build config/crd 2>/dev/null || true )"; \
	if [ -n "$$crds" ]; then echo "$$crds" | "$(KUBECTL)" $(KUBE_CONTEXT_FLAG) apply -f -; else echo "No CRDs to install; skipping."; fi

.PHONY: deploy
deploy: build-installer ## Deploy controller to the K8s cluster specified in ~/.kube/config. Set KUBE_CONTEXT to pick a context other than the current one.
	"$(KUBECTL)" $(KUBE_CONTEXT_FLAG) apply -f dist/install.yaml

##@ Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p "$(LOCALBIN)"

## Tool Binaries
KUBECTL ?= kubectl
KUSTOMIZE ?= $(LOCALBIN)/kustomize
# controller-gen (unlike `go list ./...`) descends into dot directories, so "./..." would walk
# .claude/worktrees/*, whose nested go.mod files may be broken (missing go.sum) and fail
# the whole run. List the real code roots instead; "{a,b}" is its multi-path syntax.
CONTROLLER_GEN_PATHS ?= {./api/...,./cmd/...,./internal/...,./pkg/...}
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

## Chart version and appVersion for helm-package (a release tag vX.Y.Z gives CHART_VERSION=X.Y.Z, APP_VERSION=vX.Y.Z, the image tag).
CHART_VERSION ?= $(shell sed -n 's/^version: //p' $(HELM_CHART_DIR)/Chart.yaml)
APP_VERSION ?= $(CHART_VERSION)
CHART_DIST ?= bin/chart

.PHONY: helm-package
helm-package: ## Package the chart into CHART_DIST (bin/chart) with CHART_VERSION and APP_VERSION.
	$(HELM) package $(HELM_CHART_DIR) --version $(CHART_VERSION) --app-version $(APP_VERSION) --destination $(CHART_DIST)


.PHONY: helm-status
helm-status: ## Show Helm release status.
	$(HELM) status $(HELM_RELEASE) --namespace $(HELM_NAMESPACE)


##@ Connect API (proto)

.PHONY: proto-generate
proto-generate: ## Regenerate Go types and Connect handlers/clients from proto/ into gen/go.
	buf generate

.PHONY: proto-lint
proto-lint: ## Lint the proto/ sources with buf.
	buf lint

.PHONY: proto-breaking
proto-breaking: ## Check proto/ for breaking changes against main; a no-op until proto/ lands on main.
	@if git cat-file -e main:proto 2>/dev/null; then \
		buf breaking proto --against '.git#branch=main,subdir=proto'; \
	else \
		echo "proto-breaking: proto/ does not exist on main yet; nothing to compare"; \
	fi
##@ UI

# ui/ (Vite + Preact + TypeScript, preactcn components) is embedded into the
# ichor-ui binary. None of these targets are prerequisites of the
# existing `build`/`test`/`lint` targets above: cmd/ichor-ui builds against a
# committed placeholder (see cmd/ichor-ui/webfs_dev.go) when the UI hasn't
# been built, so Go-only workflows never need npm.

.PHONY: ui-install
ui-install: ## Install UI dependencies (npm ci).
	cd ui && npm ci --ignore-scripts

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
ui-ci: ui-typecheck ui-lint ui-test ui-build ## Install, type-check, lint, test and build the UI, in order.

.PHONY: build-ichor-ui
build-ichor-ui: ui-build ## Build the ichor-ui binary with the real UI embedded.
	go build -tags uiembed -o bin/ichor-ui ./cmd/ichor-ui

##@ ichor-ui RBAC

# internal/uiapi's own +kubebuilder:rbac markers live behind the ichorui_rbac build tag
# (see internal/uiapi/rbac_markers.go) so the `manifests` target above -- which scans
# CONTROLLER_GEN_PATHS with no special tags -- never sees them and never merges them into
# manager-role. This target is the only one that passes --load-build-tags=ichorui_rbac,
# so the UI backend's ClusterRole stays in its own file, generated on its own.

.PHONY: manifests-ichor-ui
manifests-ichor-ui: controller-gen ## Generate the UI backend's own ClusterRole, kept separate from manager-role.
	"$(CONTROLLER_GEN)" --load-build-tags=ichorui_rbac rbac:roleName=ichor-ui-role paths="./internal/uiapi/..." output:dir=config/rbac/ichor-ui

##@ UI screens

.PHONY: ui-test
ui-test: ui-install ## Run the UI's own unit tests (node --test; no test framework dependency added).
	cd ui && npm test

.PHONY: ui-proto-generate
ui-proto-generate: ui-install ## Regenerate the TS Connect client into ui/src/gen. Run before plain `buf generate`/`make proto-generate` if ui/node_modules is missing - buf.gen.yaml's TS plugin resolves to ui/node_modules/.bin/protoc-gen-es.
	buf generate

.PHONY: ui-shots
ui-shots: ## Screenshot UI pages (desktop+phone, light+dark): make ui-shots OUT=.ui-shots/x URL=http://host/ [PATHS="/ /machines"].
	node hack/ui-shots.mjs "$(OUT)" "$(URL)" $(PATHS)

include test/harness/harness.mk
