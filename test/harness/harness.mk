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

# setup-test-e2e and cleanup-test-e2e are CI's provisioner for the talosctl-qemu harness: a throwaway Talos cluster on the runner, not the KubeVirt path above. Each
# does exactly one thing and is safe to call on its own - test-e2e itself never calls
# either, it only ever runs against whatever KUBECONFIG already points at.
TEST_E2E_KUBECONFIG ?= $(LOCALBIN)/test-e2e.kubeconfig
