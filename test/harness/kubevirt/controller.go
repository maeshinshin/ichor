/*
Copyright 2026 maeshinshin.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// "up-controller"/"down-controller" run the real controller against the inner cluster
// upHA (ha.go) already stood up, with its admission webhook reachable from the inner
// apiserver -- the path docs/adr/0007 proved by hand, now code. There is no path to
// deploy the controller inside the inner cluster yet, and no docker in this sandbox to
// build an image with, so the controller runs as a plain Pod on the *outer* cluster's own
// pod network (the one the VMs are bridge-bound onto), its statically built binary
// kubectl cp'd in -- not an image.
package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/maeshinshin/ichor/test/harness/webhookcert"
	"github.com/maeshinshin/ichor/test/internal/kube"
	"github.com/maeshinshin/ichor/test/utils"
)

// controllerPodName is fixed, like vmName/haCluster, for the same cross-invocation reason:
// "up-controller" and "down-controller" are separate process invocations.
const controllerPodName = "ichor-e2e-controller"

// controllerBinaryPath and haKubeconfigPath are what "make controller-static" and
// "up-ha" each publish; up-controller refuses to guess a path neither one wrote.
const (
	controllerBinaryPath = "manager-static"
	haKubeconfigPath     = "test-e2e-kubevirt-ha.kubeconfig"
)

// controllerWebhookCertSecret and controllerLeaderElectionNamespace are cmd/main.go's own
// hardcoded names (webhookSecretName, and leader election's namespace, which
// controller-runtime auto-detects from *this* pod's own namespace -- "ichor-e2e" on the
// outer cluster -- not from POD_NAMESPACE; see docs/adr/0007).
const (
	controllerWebhookCertSecret       = "ichor-webhook-server-cert"
	controllerLeaderElectionNamespace = namespace
	controllerValidatingWebhookConfig = "ichor-validating-webhook-configuration"
)

const controllerPodManifestTmpl = `apiVersion: v1
kind: Pod
metadata:
  name: %[1]s
  namespace: ichor-e2e
  labels:
    ichor.maesh.dev/e2e-harness: kubevirt-ha
spec:
  restartPolicy: Never
  containers:
    - name: controller
      image: alpine:3
      command: ["sleep", "infinity"]
`

// controllerWebhookManifestTmpl registers both validating webhooks cmd/main.go's
// SetupTalosMachineWebhookWithManager/SetupTalosClusterWebhookWithManager wire up, with a
// url (not a service: the inner apiserver has no route to an outer-cluster Service --
// ClusterIPs don't cross clusters, ADR 0007) pointed at the controller pod's own IP, and
// caBundle set to the CA cert-controller's rotator already generated.
const controllerWebhookManifestTmpl = `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingWebhookConfiguration
metadata:
  name: %[1]s
webhooks:
  - name: vtalosmachine-v1alpha1.kb.io
    admissionReviewVersions: ["v1"]
    sideEffects: None
    failurePolicy: Fail
    clientConfig:
      url: https://%[2]s:9443/validate-ichor-maesh-dev-v1alpha1-talosmachine
      caBundle: %[3]s
    rules:
      - apiGroups: ["ichor.maesh.dev"]
        apiVersions: ["v1alpha1"]
        resources: ["talosmachines"]
        operations: ["CREATE", "UPDATE", "DELETE"]
  - name: vtaloscluster-v1alpha1.kb.io
    admissionReviewVersions: ["v1"]
    sideEffects: None
    failurePolicy: Fail
    clientConfig:
      url: https://%[2]s:9443/validate-ichor-maesh-dev-v1alpha1-taloscluster
      caBundle: %[3]s
    rules:
      - apiGroups: ["ichor.maesh.dev"]
        apiVersions: ["v1alpha1"]
        resources: ["talosclusters"]
        operations: ["UPDATE", "DELETE"]
`

func upController() error {
	bin, err := projectBin()
	if err != nil {
		return err
	}
	binaryPath := filepath.Join(bin, controllerBinaryPath)
	if _, err := os.Stat(binaryPath); err != nil {
		return fmt.Errorf(`controller binary not found at %s; run "make controller-static" first: %w`, binaryPath, err)
	}
	kubeconfigPath := filepath.Join(bin, haKubeconfigPath)
	if _, err := os.Stat(kubeconfigPath); err != nil {
		return fmt.Errorf(`inner cluster kubeconfig not found at %s; run "make setup-test-e2e-kubevirt-ha" first: %w`,
			kubeconfigPath, err)
	}
	kubectlPath, err := exec.LookPath("kubectl")
	if err != nil {
		return fmt.Errorf("kubectl not on PATH: %w", err)
	}

	podIP, err := createControllerPod(binaryPath, kubeconfigPath, kubectlPath)
	if err != nil {
		return err
	}

	if err := prepareInnerClusterForController(); err != nil {
		return err
	}

	if err := startController(bin); err != nil {
		return err
	}

	caBundle, err := waitForWebhookCert()
	if err != nil {
		return err
	}

	if err := mintAndInstallLeafCert(bin, podIP, caBundle); err != nil {
		return err
	}

	// Applied to the *inner* cluster, not wherever KUBECONFIG points (the outer one): this
	// webhook has to be registered on the apiserver that will actually see TalosMachine
	// deletes, which is the inner cluster's own.
	webhookManifest := fmt.Sprintf(controllerWebhookManifestTmpl, controllerValidatingWebhookConfig, podIP, caBundle)
	if err := applyInnerManifest(webhookManifest); err != nil {
		return fmt.Errorf("applying the ValidatingWebhookConfiguration: %w", err)
	}

	logPath := filepath.Join(bin, "test-e2e-kubevirt-controller.log")
	if _, err := kube.Poll("waiting for the webhook server to start serving", 2*time.Minute, func() (string, bool) {
		log, err := os.ReadFile(logPath)
		return "", err == nil && strings.Contains(string(log), "Serving webhook server")
	}); err != nil {
		return fmt.Errorf("%w; see %s", err, logPath)
	}

	fmt.Println("--- controller pod running, webhook server up ---")
	fmt.Println(podIP)
	return nil
}

func downController() error {
	deleteWebhook := innerKubectl(verbDelete, "validatingwebhookconfiguration",
		controllerValidatingWebhookConfig, "--ignore-not-found")
	if _, err := deleteWebhook.CombinedOutput(); err != nil {
		fmt.Fprintln(os.Stderr, "kubevirt harness: deleting the inner ValidatingWebhookConfiguration:", err)
	}
	if _, err := utils.Run(kube.Command("-n", namespace, verbDelete, "pod", controllerPodName,
		"--wait=true", "--timeout=60s", "--ignore-not-found")); err != nil {
		return fmt.Errorf("deleting the controller pod: %w", err)
	}
	return nil
}

// createControllerPod creates the pod, waits for it to run, and copies the controller
// binary, the inner cluster's kubeconfig and a kubectl binary into it. It returns the
// pod's own IP.
func createControllerPod(binaryPath, kubeconfigPath, kubectlPath string) (string, error) {
	if err := kube.Apply(fmt.Sprintf(controllerPodManifestTmpl, controllerPodName)); err != nil {
		return "", fmt.Errorf("creating the controller pod: %w", err)
	}
	if _, err := kube.Poll("waiting for the controller pod to be running", 2*time.Minute, func() (string, bool) {
		phase, err := getJSONPath("pod", controllerPodName, ".status.phase")
		return "", err == nil && phase == "Running"
	}); err != nil {
		return "", err
	}

	podIP, err := kube.Poll("waiting for the controller pod's IP", 30*time.Second, func() (string, bool) {
		ip, err := getJSONPath("pod", controllerPodName, ".status.podIP")
		return ip, err == nil && ip != ""
	})
	if err != nil {
		return "", err
	}

	copies := [][2]string{
		{binaryPath, controllerPodName + ":/manager"},
		{kubeconfigPath, controllerPodName + ":/kubeconfig"},
		{kubectlPath, controllerPodName + ":/usr/local/bin/kubectl"},
	}
	for _, c := range copies {
		if _, err := utils.Run(kube.Command("-n", namespace, "cp", c[0], c[1])); err != nil {
			return "", fmt.Errorf("copying %s into the pod: %w", c[0], err)
		}
	}
	if _, err := podExec("chmod", "+x", "/manager", "/usr/local/bin/kubectl").CombinedOutput(); err != nil {
		return "", fmt.Errorf("making the copied binaries executable: %w", err)
	}
	return podIP, nil
}

// prepareInnerClusterForController creates two things the controller needs on the inner
// cluster before it will start cleanly, neither of which it creates itself (ADR 0007):
// the namespace leader election looks for (this outer pod's own namespace, "ichor-e2e" --
// not POD_NAMESPACE, which controller-runtime's leader-election namespace auto-detection
// ignores), and an empty webhook cert Secret (cert-controller's rotator only ever Gets
// then Updates one, never creates it).
func prepareInnerClusterForController() error {
	// "apply", not "create": naturally idempotent across repeated harness runs, unlike
	// "create" (which errors the second time -- observed for real, and the CLI's own
	// error text is the lowercase "already exists", not the API reason "AlreadyExists",
	// so matching on that string was never going to catch it reliably either).
	manifest := fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %[1]s
---
apiVersion: v1
kind: Secret
metadata:
  name: %[2]s
  namespace: %[1]s
`, controllerLeaderElectionNamespace, controllerWebhookCertSecret)
	if err := applyInnerManifest(manifest); err != nil {
		return fmt.Errorf("preparing the inner cluster for the controller: %w", err)
	}
	return nil
}

// startController starts /manager inside the pod, detached (like startRelay's
// port-forward in main.go), logging to bin/test-e2e-kubevirt-controller.log.
func startController(bin string) error {
	logFile, err := os.Create(filepath.Join(bin, "test-e2e-kubevirt-controller.log"))
	if err != nil {
		return fmt.Errorf("creating the controller log: %w", err)
	}
	cmd := podExecWithKubeconfig("--leader-elect")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting the controller: %w", err)
	}
	return nil
}

// waitForWebhookCert waits for cert-controller's rotator to write the Secret it manages
// (confirming it got far enough to generate a CA, even though the leaf it writes is never
// used -- see package webhookcert) and returns the CA certificate's own base64 PEM, read
// straight out of the Secret's "ca.crt" data key exactly as caBundle wants it.
func waitForWebhookCert() (string, error) {
	return kube.Poll("waiting for cert-controller to generate the webhook CA", 90*time.Second, func() (string, bool) {
		data, err := webhookCertData()
		if err != nil {
			return "", false
		}
		caBundle, ok := data["ca.crt"]
		return caBundle, ok && caBundle != ""
	})
}

// webhookCertData returns the data of the webhook cert Secret on the inner cluster.
func webhookCertData() (map[string]string, error) {
	out, err := innerKubectl("get", "secret", controllerWebhookCertSecret,
		"-n", controllerLeaderElectionNamespace, "-o", "json").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("reading the webhook cert secret: %w: %s", err, out)
	}
	var secret struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(out, &secret); err != nil {
		return nil, fmt.Errorf("decoding the webhook cert secret: %w", err)
	}
	return secret.Data, nil
}

// mintAndInstallLeafCert fetches the CA cert-controller generated (cert and key) and
// drops a new leaf for podIP, signed by that CA, into the pod's CertDir -- see package
// webhookcert for why this cannot be cert-controller's own leaf.
func mintAndInstallLeafCert(bin, podIP, caBundleB64 string) error {
	data, err := webhookCertData()
	if err != nil {
		return err
	}
	caKeyB64, ok := data["ca.key"]
	if !ok {
		return fmt.Errorf("webhook cert secret has no ca.key")
	}

	caCertPEM, err := base64.StdEncoding.DecodeString(caBundleB64)
	if err != nil {
		return fmt.Errorf("decoding ca.crt: %w", err)
	}
	caKeyPEM, err := base64.StdEncoding.DecodeString(caKeyB64)
	if err != nil {
		return fmt.Errorf("decoding ca.key: %w", err)
	}

	certPEM, keyPEM, err := webhookcert.MintLeafForIP(caCertPEM, caKeyPEM, podIP)
	if err != nil {
		return err
	}

	certPath := filepath.Join(bin, "webhook-tls.crt")
	keyPath := filepath.Join(bin, "webhook-tls.key")
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return err
	}

	if _, err := podExec("mkdir", "-p", "/tmp/k8s-webhook-server/serving-certs").CombinedOutput(); err != nil {
		return fmt.Errorf("creating the pod's CertDir: %w", err)
	}
	for _, c := range [][2]string{
		{certPath, controllerPodName + ":/tmp/k8s-webhook-server/serving-certs/tls.crt"},
		{keyPath, controllerPodName + ":/tmp/k8s-webhook-server/serving-certs/tls.key"},
	} {
		if _, err := utils.Run(kube.Command("-n", namespace, "cp", c[0], c[1])); err != nil {
			return fmt.Errorf("copying the minted cert into the pod: %w", err)
		}
	}
	return nil
}

// applyInnerManifest runs "kubectl apply -f -" against the inner cluster, from inside the
// controller pod (ADR 0007: a workstation has no route to the inner cluster's node IP,
// let alone its apiserver).
func applyInnerManifest(manifest string) error {
	cmd := innerKubectl("apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, out)
	}
	return nil
}

// podExec runs args inside the controller pod. "-i" is required for "kubectl exec" to
// forward the local process's stdin to the remote command at all -- without it, anything
// piped via cmd.Stdin (e.g. applyInnerManifest's "kubectl apply -f -") reaches the remote
// side as an already-closed, empty stdin.
func podExec(args ...string) *exec.Cmd {
	return kube.Command(append([]string{"-n", namespace, "exec", "-i", controllerPodName, "--"}, args...)...)
}

// podExecWithKubeconfig runs /manager inside the controller pod with the inner cluster's
// kubeconfig and leader election's namespace (prepareInnerClusterForController already
// created it) as its environment.
func podExecWithKubeconfig(managerArgs ...string) *exec.Cmd {
	env := make([]string, 0, 4+len(managerArgs))
	env = append(env, "env", "KUBECONFIG=/kubeconfig", "POD_NAMESPACE="+controllerLeaderElectionNamespace, "/manager")
	return podExec(append(env, managerArgs...)...)
}

// innerKubectl runs a kubectl command against the inner cluster, from inside the
// controller pod (which sits on the pod network the inner cluster's own node is on; a
// workstation has no route there at all, ADR 0007).
func innerKubectl(args ...string) *exec.Cmd {
	return podExec(append([]string{"env", "KUBECONFIG=/kubeconfig", "kubectl"}, args...)...)
}
