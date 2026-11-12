# e2e takes the cluster it is given through KUBECONFIG rather than creating one:
# it runs Talos nodes as KubeVirt VMs on a real cluster, not talosctl's qemu provisioner.
# kubectl kuberc is disabled by default for test isolation; enable with:
# - KUBECTL_KUBERC=true

.PHONY: test-e2e-prereqs
test-e2e-prereqs: ## Verify KUBECONFIG and ICHOR_E2E_KUBE_CONTEXT point at a cluster with KubeVirt, CDI and the ichor-e2e namespace
	@test -n "$$KUBECONFIG" || { \
		echo "test-e2e: KUBECONFIG is not set. e2e takes the cluster it is given through KUBECONFIG; export it before running."; \
		exit 1; \
	}
	@test -r "$$KUBECONFIG" || { \
		echo "test-e2e: KUBECONFIG=$$KUBECONFIG is not a readable kubeconfig file."; \
		exit 1; \
	}
	@test -n "$$ICHOR_E2E_KUBE_CONTEXT" || { \
		echo "test-e2e: ICHOR_E2E_KUBE_CONTEXT is not set. Every e2e kubectl call is pinned to that context and never falls back to the current-context; export it before running."; \
		exit 1; \
	}
	@$(KUBECTL) config get-contexts "$$ICHOR_E2E_KUBE_CONTEXT" -o name >/dev/null 2>&1 || { \
		echo "test-e2e: ICHOR_E2E_KUBE_CONTEXT=$$ICHOR_E2E_KUBE_CONTEXT is not a context in KUBECONFIG=$$KUBECONFIG."; \
		exit 1; \
	}
	@$(KUBECTL) --context "$$ICHOR_E2E_KUBE_CONTEXT" get namespace ichor-e2e >/dev/null 2>&1 || { \
		echo "test-e2e: namespace 'ichor-e2e' was not found on the cluster at KUBECONFIG=$$KUBECONFIG. Create it before running e2e."; \
		exit 1; \
	}
	@$(KUBECTL) --context "$$ICHOR_E2E_KUBE_CONTEXT" get crd virtualmachines.kubevirt.io datavolumes.cdi.kubevirt.io >/dev/null 2>&1 || { \
		echo "test-e2e: KubeVirt and/or CDI CRDs were not found on the cluster at KUBECONFIG=$$KUBECONFIG. e2e runs Talos nodes as KubeVirt VMs and needs both installed."; \
		exit 1; \
	}

# Neither harness may provision inside the specs: a KubeVirt VM is meaningless
# on a CI runner with no KubeVirt, and talosctl's qemu provisioner is meaningless on the
# real cluster. So each harness gets its own setup/cleanup pair, each doing exactly one
# thing and safe to call on its own - test-e2e itself never calls either, it only ever
# runs against whatever KUBECONFIG, ICHOR_E2E_TALOS_NODES, ICHOR_E2E_INNER_KUBECONFIG and
# ICHOR_E2E_TALOSCONFIG already point at.

# --- KubeVirt harness (by hand or in CI against a host cluster) ---

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

# --- Running the real controller against the inner cluster: no path to deploy
# it inside the inner cluster yet, and no docker in this sandbox to build an image with,
# so it runs as a plain Pod on the outer cluster's own pod network, its statically built
# binary copied in rather than an image. Needs setup-test-e2e-kubevirt-ha first.

.PHONY: setup-test-e2e-kubevirt-controller
setup-test-e2e-kubevirt-controller: test-e2e-prereqs kubevirt-harness controller-static ## Run the real controller against the inner HA cluster, with its admission webhook reachable from the inner apiserver
	"$(LOCALBIN)/kubevirt-harness" up-controller

.PHONY: cleanup-test-e2e-kubevirt-controller
cleanup-test-e2e-kubevirt-controller: test-e2e-prereqs kubevirt-harness ## Tear down the controller pod and the inner cluster's ValidatingWebhookConfiguration; a no-op if there is nothing to tear down
	"$(LOCALBIN)/kubevirt-harness" down-controller
