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

// Command talosq runs one authenticated, read-only talosctl query in the outer namespace for a
// hardware run: `talosq etcd|version|health|get TALOSCONFIG NODE [NODE...|RESOURCE]`. The talosconfig is read from a
// file and never printed.
package main

import (
	"fmt"
	"os"

	"github.com/maeshinshin/ichor/test/internal/kube"
	"github.com/maeshinshin/ichor/test/internal/talosctl"
)

const namespace = "ichor-e2e"

func main() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: talosq etcd|version|health|get TALOSCONFIG NODE [NODE...|RESOURCE]")
		os.Exit(2)
	}

	if err := kube.VerifyContextExists(); err != nil {
		fmt.Fprintln(os.Stderr, "talosq:", err)
		os.Exit(1)
	}

	cfg, err := os.ReadFile(os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, "talosq:", err)
		os.Exit(1)
	}

	var out string

	switch os.Args[1] {
	case "etcd":
		out, err = talosctl.EtcdMembers(namespace, os.Args[3], cfg)
	case "get":
		if len(os.Args) != 5 {
			err = fmt.Errorf("usage: talosq get TALOSCONFIG NODE RESOURCE")
			break
		}

		out, err = talosctl.GetAuthenticated(namespace, os.Args[3], os.Args[4], cfg)
	case "version":
		out, err = talosctl.Version(namespace, os.Args[3], cfg)
	case "health":
		out, err = talosctl.Health(namespace, os.Args[3:], cfg)
	default:
		err = fmt.Errorf("unknown query %q", os.Args[1])
	}

	fmt.Println(out)

	if err != nil {
		fmt.Fprintln(os.Stderr, "talosq:", err)
		os.Exit(1)
	}
}
