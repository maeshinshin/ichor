// Command thirdpartynotices writes THIRD-PARTY-NOTICES: the licence texts, copyright lines and
// upstream NOTICE files of every component the shipped binaries and the UI carry. It reads the
// module list from "go list -deps" over the three binaries with their release build tags, the
// texts from the Go module cache, the Go toolchain and ui/node_modules, and adds the few
// entries nothing can derive (the preactcn upstreams).
//
// Run via "make third-party-notices" from the repository root (it needs ui/node_modules, which
// "make ui-install" creates, and a complete Go module cache).
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := run("THIRD-PARTY-NOTICES"); err != nil {
		fmt.Fprintln(os.Stderr, "thirdpartynotices:", err)
		os.Exit(1)
	}
}

func run(out string) error {
	comps, err := goComponents()
	if err != nil {
		return err
	}
	std, err := stdComponent()
	if err != nil {
		return err
	}
	npm, err := npmComponents("ui")
	if err != nil {
		return err
	}
	chart, err := chartComponent()
	if err != nil {
		return err
	}
	gw, err := gatewayAPI()
	if err != nil {
		return err
	}
	comps = append(comps, std, chart, trustedRoot(), gw)
	comps = append(comps, npm...)
	comps = append(comps, upstreamComponents()...)
	text, err := render(comps)
	if err != nil {
		return err
	}
	return os.WriteFile(out, []byte(text), 0o644)
}
