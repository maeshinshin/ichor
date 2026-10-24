# e2e takes the cluster it is given through KUBECONFIG rather than creating one:
# it runs Talos nodes as KubeVirt VMs on a real cluster, not talosctl's qemu provisioner.
# kubectl kuberc is disabled by default for test isolation; enable with:
# - KUBECTL_KUBERC=true

.PHONY: test-e2e-prereqs
test-e2e-prereqs: ## Verify KUBECONFIG points at a cluster with KubeVirt, CDI and the ichor-e2e namespace
	@test -n "$$KUBECONFIG" || { \
		echo "test-e2e: KUBECONFIG is not set. e2e takes the cluster it is given through KUBECONFIG; export it before running."; \
		exit 1; \
	}
	@test -r "$$KUBECONFIG" || { \
		echo "test-e2e: KUBECONFIG=$$KUBECONFIG is not a readable kubeconfig file."; \
		exit 1; \
	}
	@$(KUBECTL) get namespace ichor-e2e >/dev/null 2>&1 || { \
		echo "test-e2e: namespace 'ichor-e2e' was not found on the cluster at KUBECONFIG=$$KUBECONFIG. Create it before running e2e."; \
		exit 1; \
	}
	@$(KUBECTL) get crd virtualmachines.kubevirt.io datavolumes.cdi.kubevirt.io >/dev/null 2>&1 || { \
		echo "test-e2e: KubeVirt and/or CDI CRDs were not found on the cluster at KUBECONFIG=$$KUBECONFIG. e2e runs Talos nodes as KubeVirt VMs and needs both installed."; \
		exit 1; \
	}

# Neither harness may provision inside the specs: a KubeVirt VM is meaningless
# on a CI runner with no KubeVirt, and talosctl's qemu provisioner is meaningless on the
# real cluster. So each harness gets its own setup/cleanup pair, each doing exactly one
# thing and safe to call on its own - test-e2e itself never calls either, it only ever
# runs against whatever KUBECONFIG and ICHOR_E2E_TALOS_NODES already point at.

# --- KubeVirt harness (talos-cluster01, by hand or in CI against that cluster) ---

.PHONY: setup-test-e2e-kubevirt
setup-test-e2e-kubevirt: test-e2e-prereqs ## Boot the e2e Talos node as a KubeVirt VM; prints its address, export it as ICHOR_E2E_TALOS_NODES
	go run ./test/harness/kubevirt up

.PHONY: cleanup-test-e2e-kubevirt
cleanup-test-e2e-kubevirt: ## Tear down the VM and DataVolumes setup-test-e2e-kubevirt created; a no-op if there is nothing to tear down
	go run ./test/harness/kubevirt down
