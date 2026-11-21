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

// Package kube is the one place the e2e harness decides which cluster a kubectl call
// targets. Every call to the outer (KubeVirt) cluster goes through Command, which always
// carries an explicit --context taken from ICHOR_E2E_KUBE_CONTEXT and refuses to build a
// command when it is unset, so nothing can fall through to kubectl's ambient
// current-context (which on a workstation may be a different, real cluster). Deletes are
// further limited to the e2e namespace (NamespaceEnv).
package kube

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/maeshinshin/ichor/test/utils"
)

// Namespace is the only namespace the harness may delete from; manifests name it __NAMESPACE__ and Apply fills it in.
var Namespace = namespaceFromEnv()

func namespaceFromEnv() string {
	if v := os.Getenv(NamespaceEnv); v != "" {
		return v
	}
	return "ichor-e2e"
}

const (
	// ContextEnv names the kubeconfig context every outer-cluster kubectl call targets.
	ContextEnv = "ICHOR_E2E_KUBE_CONTEXT"
	// NamespaceEnv overrides the outer namespace the harness works in.
	NamespaceEnv = "ICHOR_E2E_NAMESPACE"
	// StorageClassEnv overrides the StorageClass the harness's DataVolumes and PVCs request.
	StorageClassEnv = "ICHOR_E2E_STORAGE_CLASS"

	flagContext    = "--context"
	flagKubeconfig = "--kubeconfig"
)

// StorageClass returns the StorageClass the harness requests: ICHOR_E2E_STORAGE_CLASS, which
// ResolveStorageClass sets from the cluster's default class when it is unset.
func StorageClass() string {
	return strings.TrimSpace(os.Getenv(StorageClassEnv))
}

// ResolveStorageClass makes ICHOR_E2E_STORAGE_CLASS non-empty before any VM is created: when it
// is unset it takes the outer cluster's default StorageClass, and fails at once when there is none
// (a wrong class leaves every PVC Pending until a 10-minute timeout).
func ResolveStorageClass() error {
	if StorageClass() != "" {
		return nil
	}
	out, err := Command("get", "storageclass", "-o", "json").Output()
	if err != nil {
		return fmt.Errorf("listing StorageClasses to find the default: %w", err)
	}
	c, err := pickStorageClass(out)
	if err != nil {
		return err
	}
	return os.Setenv(StorageClassEnv, c)
}

// pickStorageClass returns the one class in a StorageClassList marked default.
func pickStorageClass(list []byte) (string, error) {
	var l struct {
		Items []struct {
			Metadata struct {
				Name        string            `json:"name"`
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal(list, &l); err != nil {
		return "", fmt.Errorf("reading the StorageClass list: %w", err)
	}
	var def []string
	for _, i := range l.Items {
		if i.Metadata.Annotations["storageclass.kubernetes.io/is-default-class"] == "true" {
			def = append(def, i.Metadata.Name)
		}
	}
	if len(def) != 1 {
		return "", fmt.Errorf("%s is unset and the outer cluster has %d default StorageClasses (%v): set %s to the class the VM disks use",
			StorageClassEnv, len(def), def, StorageClassEnv)
	}
	return def[0], nil
}

// flagsWithValue are the kubectl flags (as separate args) whose next arg is a value, so
// verb detection does not mistake that value for the verb.
var flagsWithValue = map[string]bool{
	"-n": true, "--namespace": true, flagContext: true, flagKubeconfig: true,
	"--cluster": true, "--user": true, "-s": true, "--server": true,
}

// Command returns a kubectl command aimed at the outer cluster. If the target cannot be
// established safely, the returned Cmd's Err is set and running it fails without
// executing kubectl.
func Command(args ...string) *exec.Cmd {
	full, err := Args(os.Getenv(ContextEnv), args)
	return build(full, err)
}

// InnerCommand returns a kubectl command aimed at the inner (Talos-in-KubeVirt) cluster
// through its own kubeconfig file. It never deletes.
func InnerCommand(kubeconfig string, args ...string) *exec.Cmd {
	full, err := InnerArgs(kubeconfig, args)
	return build(full, err)
}

func build(full []string, err error) *exec.Cmd {
	if err != nil {
		cmd := exec.Command("kubectl")
		cmd.Err = err
		return cmd
	}
	return exec.Command("kubectl", full...)
}

// Args returns the kubectl arguments (without the binary) for an outer-cluster call, with
// --context kubeContext prepended. It errors when kubeContext is empty, when args carry
// their own cluster-selecting flags, or when a delete is not confined to Namespace.
func Args(kubeContext string, args []string) ([]string, error) {
	if strings.TrimSpace(kubeContext) == "" {
		return nil, fmt.Errorf("%s is not set; refusing to run kubectl against an implicit current-context", ContextEnv)
	}
	if err := checkNoTargetFlags(args); err != nil {
		return nil, err
	}
	if verb := verbOf(args); verb == "delete" {
		if err := checkDelete(args); err != nil {
			return nil, err
		}
	}
	return append([]string{flagContext, kubeContext}, args...), nil
}

// InnerArgs is Args for the inner cluster: --kubeconfig instead of --context, no deletes.
func InnerArgs(kubeconfig string, args []string) ([]string, error) {
	if strings.TrimSpace(kubeconfig) == "" {
		return nil, errors.New("inner kubeconfig path is empty; refusing to run kubectl against an implicit cluster")
	}
	if err := checkNoTargetFlags(args); err != nil {
		return nil, err
	}
	if verbOf(args) == "delete" {
		return nil, errors.New("refusing kubectl delete through the inner-cluster helper")
	}
	return append([]string{flagKubeconfig, kubeconfig}, args...), nil
}

// RequireContext returns the configured context name, or an error if unset.
func RequireContext() (string, error) {
	c := strings.TrimSpace(os.Getenv(ContextEnv))
	if c == "" {
		return "", fmt.Errorf("%s is not set; refusing to guess which cluster to target", ContextEnv)
	}
	return c, nil
}

// VerifyContextExists checks, from the local kubeconfig only (no cluster is contacted),
// that the configured context is defined. A typo would otherwise surface as a vague
// kubectl error partway through a run.
func VerifyContextExists() error {
	c, err := RequireContext()
	if err != nil {
		return err
	}
	out, err := exec.Command("kubectl", "config", "get-contexts", c, "-o", "name").Output()
	if err != nil || strings.TrimSpace(string(out)) != c {
		return fmt.Errorf("%s=%q is not a context in the current kubeconfig", ContextEnv, c)
	}
	return nil
}

func checkNoTargetFlags(args []string) error {
	for _, a := range args {
		for _, f := range []string{flagContext, flagKubeconfig, "--cluster", "--server"} {
			if a == f || strings.HasPrefix(a, f+"=") {
				return fmt.Errorf("kubectl arg %q selects a target; the target is fixed by %s", a, ContextEnv)
			}
		}
	}
	return nil
}

// verbOf returns the first non-flag arg.
func verbOf(args []string) string {
	skip := false
	for _, a := range args {
		switch {
		case skip:
			skip = false
		case flagsWithValue[a]:
			skip = true
		case !strings.HasPrefix(a, "-"):
			return a
		}
	}
	return ""
}

// checkDelete requires an explicit -n/--namespace of Namespace and no cluster-wide or
// selector-wide scope.
func checkDelete(args []string) error {
	ns := ""
	for i, a := range args {
		switch {
		case a == "-n" || a == "--namespace":
			if i+1 < len(args) {
				ns = args[i+1]
			}
		case strings.HasPrefix(a, "--namespace="):
			ns = strings.TrimPrefix(a, "--namespace=")
		case a == "-A" || a == "--all-namespaces" || a == "--all" || strings.HasPrefix(a, "--all="):
			return fmt.Errorf("refusing kubectl delete with %q", a)
		case a == "-f" || a == "-l" || a == "--selector" || strings.HasPrefix(a, "--selector=") ||
			strings.HasPrefix(a, "--filename"):
			return fmt.Errorf("refusing kubectl delete with %q: name the objects explicitly", a)
		}
	}
	if ns != Namespace {
		return fmt.Errorf("refusing kubectl delete in namespace %q: only %q is allowed", ns, Namespace)
	}
	return nil
}

// fillNamespace names the e2e namespace in a manifest that says __NAMESPACE__.
func fillNamespace(manifest string) string {
	return strings.ReplaceAll(manifest, "__NAMESPACE__", Namespace)
}

// Apply runs `kubectl apply -f -` against the outer cluster with manifest on stdin.
func Apply(manifest string) error {
	manifest = fillNamespace(manifest)
	cmd := Command("apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	_, err := utils.Run(cmd)
	return err
}

// Poll retries check every 5s until it reports ready, returning its value, or fails after timeout.
func Poll(step string, timeout time.Duration, check func() (string, bool)) (string, error) {
	fmt.Println(step)
	deadline := time.Now().Add(timeout)
	for {
		if v, ok := check(); ok {
			return v, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("%s: timed out after %s", step, timeout)
		}
		time.Sleep(5 * time.Second)
	}
}
