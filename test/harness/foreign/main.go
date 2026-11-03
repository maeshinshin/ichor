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

// Command foreign builds and tears down Talos clusters Ichor did not create: `foreign up|down|registry [flags]`. See
// package test/internal/foreign.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/maeshinshin/ichor/test/internal/foreign"
	"github.com/maeshinshin/ichor/test/internal/kube"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: foreign up|down|registry [flags]")
		os.Exit(2)
	}
	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	var o foreign.Options
	var regPort string
	var keepRegistry bool
	fs.StringVar(&o.Prefix, "prefix", foreign.PrefixRoot, "object name prefix (must start with "+foreign.PrefixRoot+")")
	fs.StringVar(&o.TalosVersion, "talos", "", "Talos version, e.g. v1.14.3 (default: the cached ISO's)")
	fs.StringVar(&o.K8sVersion, "k8s", "", "Kubernetes version (default: v1.36.5, one patch below the release set)")
	fs.IntVar(&o.ControlPlanes, "cps", 1, "control planes (1..3)")
	fs.IntVar(&o.Workers, "workers", 1, "workers (0..4)")
	fs.StringVar(&o.CNI, "cni", foreign.CNIFlannel, "talos-flannel|none|cilium-inline|cilium-helm|cilium-cli|calico")
	fs.BoolVar(&o.NoKubeProxy, "no-kube-proxy", false, "disable kube-proxy")
	fs.BoolVar(&o.NoKubePrism, "no-kubeprism", false, "disable KubePrism")
	fs.BoolVar(&o.DataDisk, "data-disk", false, "workers get a second disk with a plain and a LUKS2 user volume (v1.14)")
	fs.BoolVar(&o.UserManifest, "user-manifest", false, "add a user inline manifest of a non-Cilium app")
	fs.StringVar(&o.CPMemory, "cp-memory", "", "control plane memory (default 2560Mi)")
	fs.StringVar(&o.WorkerMemory, "worker-memory", "", "worker memory (default 1536Mi)")
	fs.BoolVar(&o.RegistryMirror, "registry-mirror", false, "create <prefix>-registry and mirror ghcr.io to it")
	fs.StringVar(&o.RelayPort, "relay-port", "5015", "local port of the API tunnel")
	fs.StringVar(&o.CiliumChart, "cilium-chart", "pkg/cni/cilium/chart/cilium-1.18.0.tgz", "cilium chart archive")
	var sets string
	fs.StringVar(&sets, "cilium-set", "",
		"comma-separated extra --set values for the cilium-* variants, e.g. operator.replicas=1")
	fs.StringVar(&regPort, "registry-port", "5014", "registry: local port to forward to")
	fs.BoolVar(&keepRegistry, "keep-registry", false, "down: keep the registry (and the images in it)")
	_ = fs.Parse(os.Args[2:])
	if sets != "" {
		o.CiliumSets = strings.Split(sets, ",")
	}

	if err := kube.VerifyContextExists(); err != nil {
		fmt.Fprintln(os.Stderr, "foreign:", err)
		os.Exit(1)
	}
	var err error
	switch os.Args[1] {
	case "up":
		var c *foreign.Cluster
		if c, err = foreign.Up(o); err == nil {
			fmt.Println("CPS=", c.CPs, "WORKERS=", c.Workers, "DIR=", c.Dir)
		} else {
			fmt.Fprintln(os.Stderr, "foreign: up failed:", err)
		}
	case "down":
		err = foreign.Down(o.Prefix, !keepRegistry)
	case "registry":
		if _, err = foreign.EnsureRegistry(o.Prefix); err == nil {
			var dir string
			if dir, err = foreign.Dir(o.Prefix); err == nil {
				err = foreign.PortForward(dir, "registry", "svc/"+o.Prefix+"-registry", regPort+":5000")
			}
		}
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "foreign:", err)
		os.Exit(1)
	}
}
