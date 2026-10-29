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
// further limited to the ichor-e2e namespace.
package kube

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/maeshinshin/ichor/test/utils"
)

const (
	// ContextEnv names the kubeconfig context every outer-cluster kubectl call targets.
	ContextEnv = "ICHOR_E2E_KUBE_CONTEXT"
	// Namespace is the only namespace the harness may delete from.
	Namespace = "ichor-e2e"

	flagContext    = "--context"
	flagKubeconfig = "--kubeconfig"
)

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

// Apply runs `kubectl apply -f -` against the outer cluster with manifest on stdin.
func Apply(manifest string) error {
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
