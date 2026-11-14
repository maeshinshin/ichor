# Placeholders build-ichor-embed refuses: they name no registry, so no cluster can pull them.
PLACEHOLDER_IMG := controller:latest
PLACEHOLDER_IMG_UI := ichor-ui:latest
# Image URL to use all building/pushing image targets
IMG ?= $(PLACEHOLDER_IMG)
# Image URL for the ichor-ui image target
IMG_UI ?= $(PLACEHOLDER_IMG_UI)
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

GATEWAY_API_VERSION ?= v1.6.1
GATEWAY_API_SHA256 ?= 24d931f22abd8e40c973264319ead7cfa09d0fb7716b7ab1ee2ff174cb063a73
# The seven CRDs Cilium's gatewayAPI reads; the standard channel's ListenerSet, TCPRoute and UDPRoute are left out.
GATEWAY_API_CRDS ?= backendtlspolicies gatewayclasses gateways grpcroutes httproutes referencegrants tlsroutes

.PHONY: gateway-api-crds
gateway-api-crds: ## Fetch the pinned Gateway API standard-channel install, verify its SHA-256 and extract the CRDs that ichor init --gateway-api applies.
	mkdir -p bin/gateway-api
	curl -fsSL -o bin/gateway-api/standard-install.yaml https://github.com/kubernetes-sigs/gateway-api/releases/download/$(GATEWAY_API_VERSION)/standard-install.yaml
	echo "$(GATEWAY_API_SHA256)  bin/gateway-api/standard-install.yaml" | sha256sum -c -
	go run ./internal/tools/gatewayapicrds -in bin/gateway-api/standard-install.yaml -out pkg/cni/gatewayapi/crds.yaml -names "$(GATEWAY_API_CRDS)" -version $(GATEWAY_API_VERSION)

.PHONY: cilium-goldens
cilium-goldens: ## Regenerate pkg/cni/cilium/testdata/bootstrap-*.golden.yaml from the embedded Cilium chart.
	go run ./internal/tools/ciliumgolden

.PHONY: bump-cilium-patch
bump-cilium-patch: ## Move the embedded Cilium chart to the newest patch of its minor, if the images are signed (hack/bump-cilium-patch.sh; CI runs it daily).
	hack/bump-cilium-patch.sh

# The foreign-cluster e2e tests (test/e2e/foreign) install this one-minor-older chart as the
# Cilium a foreign cluster starts with, so adopt's takeover of a live 1.19 is covered.
FOREIGN_CILIUM_CHART_VERSION ?= 1.19.10
FOREIGN_CILIUM_CHART_SHA256 ?= b7252afca3413a33e2b987c954e52441e96979e411e1495ecc6aefe8cd258863

.PHONY: foreign-cilium-chart
foreign-cilium-chart: ## Fetch the pinned Cilium 1.19 chart the foreign-cluster e2e tests install and verify its SHA-256.
	curl -fsSL -o pkg/cni/cilium/chart/cilium-$(FOREIGN_CILIUM_CHART_VERSION).tgz https://helm.cilium.io/cilium-$(FOREIGN_CILIUM_CHART_VERSION).tgz
	echo "$(FOREIGN_CILIUM_CHART_SHA256)  pkg/cni/cilium/chart/cilium-$(FOREIGN_CILIUM_CHART_VERSION).tgz" | sha256sum -c -

.PHONY: adr-index
adr-index: ## Regenerate docs/adr/README.md's index table from each ADR's own Decided:/Status: line.
	go run ./internal/tools/adrindex

.PHONY: docs-reference
docs-reference: ## Regenerate docs/reference/cli (from the ichor command tree) and docs/reference/crds (from config/crd/bases); run after manifests.
	go run ./internal/tools/gendocs

.PHONY: third-party-notices
third-party-notices: ui-install ## Regenerate THIRD-PARTY-NOTICES from go.mod, the Go module cache, ui/package-lock.json and the embedded Cilium chart.
	go run ./internal/tools/thirdpartynotices

.PHONY: generate
generate: controller-gen ## Generate mirror types (go generate) plus DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	go generate ./...
	"$(CONTROLLER_GEN)" object:headerFile="hack/boilerplate.go.txt",year=$(YEAR) paths="$(CONTROLLER_GEN_PATHS)"
	"$(CONTROLLER_GEN)" object:headerFile="hack/boilerplate.typedconfig.go.txt",year=$(YEAR) paths="./pkg/typedconfig"

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
	# -timeout: internal/controller alone takes about 470s on a GitHub runner and 530s on a
	# 4-core workstation, close to Go's default 10m. The ceiling sits well above both; see
	# the timing note in internal/controller/suite_test.go.
	KUBEBUILDER_ASSETS="$(shell "$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path)" go test -timeout 30m $$(go list ./... | grep -v /e2e) -coverprofile cover.out

.PHONY: remote-gate
remote-gate: ## Run the full gate (go test, lint, ui-ci) for HEAD as parallel Jobs in the cluster set in hack/remote-gate.sh; logs in remote-gate-out/.
	hack/remote-gate.sh

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
	mkdir -p $(LOCALBIN)
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

# Runs before build-installer, which keeps the placeholders: make test, make deploy and hack/remote-gate.sh apply
# that manifest to clusters that load images locally, so only the embed path refuses them.
.PHONY: check-embed-images
check-embed-images:
	@if [ "$(IMG)" = "$(PLACEHOLDER_IMG)" ] || [ "$(IMG_UI)" = "$(PLACEHOLDER_IMG_UI)" ]; then \
	  [ "$(IMG)" != "$(PLACEHOLDER_IMG)" ] || echo "build-ichor-embed: IMG is still the placeholder $(PLACEHOLDER_IMG); set IMG to the pushed controller image." >&2; \
	  [ "$(IMG_UI)" != "$(PLACEHOLDER_IMG_UI)" ] || echo "build-ichor-embed: IMG_UI is still the placeholder $(PLACEHOLDER_IMG_UI); set IMG_UI to the pushed ichor-ui image." >&2; \
	  echo "The embedded manifest would name images no cluster can pull. Working form:" >&2; \
	  echo "  make build-ichor-embed VERSION=v0.0.8-abc1234 IMG=ghcr.io/maeshinshin/ichor-controller:v0.0.8-abc1234 IMG_UI=ghcr.io/maeshinshin/ichor-ui:v0.0.8-abc1234 IMAGE_DIGESTS=bin/digests.txt" >&2; \
	  exit 1; \
	fi

.PHONY: build-ichor-embed
build-ichor-embed: check-embed-images build-installer ## Build the ichor CLI with Ichor's own install manifest embedded (what `ichor init` applies).
	cp dist/install.yaml internal/installmanifest/install.yaml
	cp dist/network-policies.yaml internal/installmanifest/network-policies.yaml
	go build -tags installembed -ldflags "$(LDFLAGS)" -o bin/ichor ./cmd/ichor

.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host.
	go run ./cmd

# The images are built by ko (.ko.yaml: linux/amd64 and linux/arm64 indexes, no container runtime). A layout in
# OCI_DIR/<name> is the compiled image; crane pushes it as it is, so the digest printed is the index digest.
OCI_DIR ?= bin/oci
## Registry host (and port) the images are pushed to: REGISTRY/maeshinshin/<name>:VERSION.
REGISTRY ?=
## A plain-HTTP local registry by default; the release workflow sets it empty for ghcr.io.
CRANE_INSECURE ?= --insecure
# ko-layout NAME IMPORT_PATH: compile one image into OCI_DIR/NAME without a registry (registry.invalid is a placeholder: nothing is pushed).
define ko-layout
@test "$(VERSION)" != dev || { echo "VERSION must not be 'dev', e.g. VERSION=v0.0.8-abc1234" >&2; exit 1; }
mkdir -p $(2)/kodata && cp THIRD-PARTY-NOTICES $(2)/kodata/
rm -rf $(OCI_DIR)/$(1)
mkdir -p $(2)/kodata && cp LICENSE NOTICE $(2)/kodata/
VERSION="$(VERSION)" KO_DOCKER_REPO=registry.invalid ko build --bare --push=false --oci-layout-path $(OCI_DIR)/$(1) $(2)
endef
# crane-push NAME: push OCI_DIR/NAME as REGISTRY/maeshinshin/NAME:VERSION and append its repo@digest line to DIGEST_FILE.
define crane-push
crane push $(CRANE_INSECURE) $(OCI_DIR)/$(1) "$(REGISTRY)/maeshinshin/$(1):$(VERSION)" | tee -a "$(DIGEST_FILE)"
endef

# The digest flow: the installer image embeds the install manifest, so it is built last,
# from a manifest that names the controller and ichor-ui images by digest (IMAGE_DIGESTS, below). Each step is a
# target so the release workflow can run them in separate jobs; demo-images runs them all.
IMAGE_TAG_CTL = ghcr.io/maeshinshin/ichor-controller:$(VERSION)
IMAGE_TAG_UI = ghcr.io/maeshinshin/ichor-ui:$(VERSION)
## File of "<repo>@<digest>" lines (the images-push-* targets write it) that build-installer and helm-package pin the controller and ichor-ui images with.
IMAGE_DIGESTS ?=
DIGEST_FILE ?= bin/digests.txt

.PHONY: demo-images
demo-images: ## Build the three images with ko and push them to REGISTRY (VERSION=v0.0.8-abc1234 REGISTRY=localhost:5000), both platforms; the install manifest names the controller and UI images by digest (DIGEST_FILE=bin/digests.txt); the tree stays unchanged.
	$(MAKE) images-build-core VERSION="$(VERSION)"
	$(MAKE) images-push-core VERSION="$(VERSION)" REGISTRY="$(REGISTRY)" DIGEST_FILE="$(DIGEST_FILE)"
	$(MAKE) images-build-installer VERSION="$(VERSION)" IMAGE_DIGESTS="$(DIGEST_FILE)"
	$(MAKE) images-push-installer VERSION="$(VERSION)" REGISTRY="$(REGISTRY)" DIGEST_FILE="$(DIGEST_FILE)"

.PHONY: images-build-core
images-build-core: ui-build ## Compile the controller and ichor-ui images into OCI_DIR (no registry).
	$(call ko-layout,ichor-controller,./cmd)
	$(call ko-layout,ichor-ui,./cmd/ichor-ui)

.PHONY: images-push-core
images-push-core: ## Push the controller and ichor-ui images from images-build-core (DIGEST_FILE receives their repo@digest lines).
	mkdir -p "$$(dirname "$(DIGEST_FILE)")"
	: > "$(DIGEST_FILE)"
	$(call crane-push,ichor-controller)
	$(call crane-push,ichor-ui)

.PHONY: images-build-installer
images-build-installer: ## Render the install manifest with the digests in IMAGE_DIGESTS (or tags, if empty), embed it and compile the installer image into OCI_DIR.
	$(MAKE) build-installer IMG=$(IMAGE_TAG_CTL) IMG_UI=$(IMAGE_TAG_UI) IMAGE_DIGESTS="$(IMAGE_DIGESTS)"
	cp dist/install.yaml internal/installmanifest/install.yaml
	cp dist/network-policies.yaml internal/installmanifest/network-policies.yaml
	$(call ko-layout,ichor-installer,./cmd/ichor)
	@# The same CLI, static, for the demo and hardware tests (they run it from bin/img/ichor/ichor).
	CGO_ENABLED=0 go build -tags installembed -trimpath -ldflags "-s -w $(LDFLAGS)" -o bin/img/ichor/ichor ./cmd/ichor

.PHONY: images-push-installer
images-push-installer: ## Push the installer image from images-build-installer (its repo@digest line is appended to DIGEST_FILE).
	$(call crane-push,ichor-installer)

# The two-step flow with tags in the manifest (no digests): build, then push to a registry of your own
# (docs/development/build-from-source.md). The release workflow uses the digest flow above.
.PHONY: demo-images-build
demo-images-build: images-build-core ## Compile the three images into OCI_DIR, the install manifest naming the images by tag (no registry).
	$(MAKE) images-build-installer VERSION="$(VERSION)"

.PHONY: demo-images-push
demo-images-push: images-push-core images-push-installer ## Push the images from demo-images-build (DIGEST_FILE=<path> receives repo@digest lines).

# KO_DOCKER_REPO=kind.local makes ko load each image into this kind cluster itself, and ko reads KIND_CLUSTER_NAME from the environment.
KIND_CLUSTER_NAME ?= kind

.PHONY: images-kind
images-kind: ui-build ## Build the controller and ichor-ui for this machine and load them into the kind cluster (KIND_CLUSTER_NAME) as kind.local/<name>:latest.
	for n in ichor-controller:./cmd ichor-ui:./cmd/ichor-ui; do \
	  VERSION="$(VERSION)" KO_DOCKER_REPO=kind.local/$${n%%:*} ko build --bare --platform linux/$$(go env GOARCH) $${n#*:}; \
	done

## Where build-installer writes install.yaml and network-policies.yaml.
INSTALL_OUT ?= dist

.PHONY: build-installer
build-installer: manifests generate kustomize ## Generate a consolidated YAML with CRDs and deployment. IMAGE_DIGESTS (repo@digest lines) pins IMG and IMG_UI by digest as <repo>:<tag>@<digest>; without it they stay tags.
	@# Built from a temp copy of config/, so the tracked kustomization.yaml files are never edited.
	mkdir -p $(INSTALL_OUT)
	@work=$$(mktemp -d) && trap 'rm -rf "$$work"' EXIT && cp -r config "$$work/config" && \
	ctl='${IMG}' && ui='${IMG_UI}' && \
	if [ -n "$(IMAGE_DIGESTS)" ]; then \
	  ctl=$$(hack/pin-image.sh "$(IMAGE_DIGESTS)" ichor-controller "$$ctl") && ui=$$(hack/pin-image.sh "$(IMAGE_DIGESTS)" ichor-ui "$$ui"); \
	fi && \
	(cd "$$work/config/manager" && "$(KUSTOMIZE)" edit set image controller="$$ctl") && \
	(cd "$$work/config/manager-ui" && "$(KUSTOMIZE)" edit set image ichor-ui="$$ui") && \
	"$(KUSTOMIZE)" build "$$work/config/installer" > $(INSTALL_OUT)/install.yaml && \
	"$(KUSTOMIZE)" build "$$work/config/cilium-network-policy" > $(INSTALL_OUT)/network-policies.yaml

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
# a git worktree kept in a dot directory of the repository, whose nested go.mod files may be broken (missing go.sum) and fail
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
	@# The custom build replaces the versioned binary, so the symlink stays valid and go-install-tool skips next time.
	@test -f .custom-gcl.yml && [ ! -f "$(GOLANGCI_LINT)-$(GOLANGCI_LINT_VERSION).custom" ] && { \
		echo "Building custom golangci-lint with plugins..." && \
		$(GOLANGCI_LINT) custom --destination $(LOCALBIN) --name golangci-lint-custom && \
		mv -f $(LOCALBIN)/golangci-lint-custom "$(GOLANGCI_LINT)-$(GOLANGCI_LINT_VERSION)" && \
		touch "$(GOLANGCI_LINT)-$(GOLANGCI_LINT_VERSION).custom"; \
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
APP_VERSION ?= v$(CHART_VERSION)
CHART_DIST ?= bin/chart

.PHONY: helm-package
helm-package: ## Package the chart into CHART_DIST (bin/chart) with CHART_VERSION and APP_VERSION. IMAGE_DIGESTS pins the default controller and ichor-ui images by digest.
	@# With digests the default repositories become <repo>:<tag>@<digest> (the templates add no tag to a repository with an @), in a temp copy.
	@work=$$(mktemp -d) && trap 'rm -rf "$$work"' EXIT && cp -r $(HELM_CHART_DIR) "$$work/ichor" && cp LICENSE NOTICE "$$work/ichor/" && \
	if [ -n "$(IMAGE_DIGESTS)" ]; then \
	  for n in ichor-controller ichor-ui; do \
	    pin=$$(hack/pin-image.sh "$(IMAGE_DIGESTS)" $$n ghcr.io/maeshinshin/$$n:$(APP_VERSION)) && \
	    sed -i "s|^\( *repository: \)ghcr.io/maeshinshin/$$n\$$|\1$$pin|" "$$work/ichor/values.yaml" && \
	    grep -qF "$$pin" "$$work/ichor/values.yaml" || { echo "helm-package: the chart has no default $$n image to pin" >&2; exit 1; }; \
	  done; \
	fi && \
	cp THIRD-PARTY-NOTICES "$$work/ichor/" && \
	$(HELM) package "$$work/ichor" --version $(CHART_VERSION) --app-version $(APP_VERSION) --destination $(CHART_DIST)


# The image names the install manifest carries (IMAGE_TAG_CTL and IMAGE_TAG_UI above).
RELEASE_REGISTRY ?= ghcr.io/maeshinshin
RELEASE_DIR ?= bin/release

.PHONY: release
release: ## Build the local release artifacts for VERSION=vX.Y.Z into bin/release (CLI with its install manifest, install.yaml, chart); pushes and tags nothing. IMAGE_DIGESTS=<the release workflow's digests.txt> names the controller and ichor-ui images by digest.
	@echo "$(VERSION)" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$$' || { echo "release: VERSION must be a semver tag with a leading v, like v0.1.0 (got '$(VERSION)'; dev is not a release)" >&2; exit 1; }
	$(MAKE) build-ichor-embed IMG=$(RELEASE_REGISTRY)/ichor-controller:$(VERSION) IMG_UI=$(RELEASE_REGISTRY)/ichor-ui:$(VERSION) IMG_INSTALLER=$(RELEASE_REGISTRY)/ichor-installer:$(VERSION)
	mkdir -p $(RELEASE_DIR)
	cp bin/ichor $(RELEASE_DIR)/ichor
	cp dist/install.yaml dist/network-policies.yaml $(RELEASE_DIR)/
	$(MAKE) helm-package CHART_VERSION=$(VERSION:v%=%) APP_VERSION=$(VERSION) CHART_DIST=$(RELEASE_DIR)

# The empty group \2 keeps sed from reading \1 and the version's first digit as \10.
.PHONY: release-prep
release-prep: ## Before pushing the tag vX.Y.Z (VERSION=vX.Y.Z): set the release number in README.md, and fail if CHANGELOG.md has no "## vX.Y.Z " section. Tags and pushes nothing.
	@echo "$(VERSION)" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$$' || { echo "release-prep: VERSION must be a final semver tag with a leading v, like v0.1.0 (got '$(VERSION)')" >&2; exit 1; }
	@grep -q '^## $(VERSION) ' CHANGELOG.md || { echo "release-prep: CHANGELOG.md has no section starting '## $(VERSION) '; the release workflow takes the release notes from it" >&2; exit 1; }
	sed -i -E 's/(VERSION=|--version )()[0-9]+\.[0-9]+\.[0-9]+/\1\2$(VERSION:v%=%)/' README.md

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

# Documentation site: the Markdown in docs/ rendered by Astro in
# site/, searched with Pagefind, published to GitHub Pages by
# .github/workflows/docs.yml. DOCS_SITE and DOCS_BASE (default
# https://ichor.maesh.dev and /) move it, e.g. for a fork.
# One build is one version at <DOCS_BASE>/<DOCS_VERSION>/ (next, the default, or
# a release tag vX.Y.Z); docs-site-all builds them all.

.PHONY: docs-install
docs-install: ## Install the docs site's dependencies (npm ci).
	cd site && npm ci --ignore-scripts

.PHONY: docs-diagrams
docs-diagrams: ## Re-export the draw.io diagrams in docs/images (NAME.light.drawio.svg is the source) as the light and dark SVG pair: make docs-diagrams [SOURCE=docs/images/NAME.light.drawio.svg].
	node hack/docs-diagrams.mjs $(SOURCE)

.PHONY: docs-site
docs-site: docs-install ## Build the docs site (the "next" version, docs/ of the working tree) into site/dist: Astro, the internal link check, then the Pagefind index.
	cd site && npm run build

.PHONY: docs-site-all
docs-site-all: docs-install ## Build next and every release tag (v* tags) into site/dist-all, with versions.json and the latest/ redirects, as CI publishes it.
	cd site && npm run build:all

.PHONY: docs-dev
docs-dev: docs-install ## Live preview of docs/ at http://localhost:4321/ichor/next/ (the search needs make docs-site).
	cd site && npm run dev

.PHONY: docs-preview
docs-preview: ## Serve site/dist under its base path, as GitHub Pages would (run make docs-site first).
	cd site && npm run preview

.PHONY: docs-check
docs-check: docs-site-all ## Build all versions of the docs site, then drive them in headless Chromium (search, sidebar, links, Copy page, version switcher and banner, phone width): make docs-check OUT=.docs-shots.
	node hack/docs-shots.mjs "$(or $(OUT),.docs-shots)" site/dist-all

include test/harness/harness.mk
