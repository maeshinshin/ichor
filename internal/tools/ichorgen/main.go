// Command ichorgen generates Ichor's own configuration schema, pkg/ichorconfig, from the mirror types
// mirrorgen wrote for the Talos machinery documents, and the table that converts it back.
//
// It is driven by `go generate` from pkg/typedconfig, after mirrorgen, and takes no flags.
package main

import (
	"fmt"
	"os"
)

const outFile = "../ichorconfig/zz_generated.config.go"

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ichorgen: "+format+"\n", args...)
	os.Exit(1)
}

func main() {
	src, err := loadSource()
	if err != nil {
		fatalf("%v", err)
	}

	g, err := run(src)
	if err != nil {
		fatalf("%v", err)
	}

	for _, c := range g.conflicts {
		fmt.Fprintln(os.Stderr, "conflict:", c)
	}

	if len(g.conflicts) > 0 {
		fatalf("%d settings are spelled two ways and no row decides", len(g.conflicts))
	}

	out, err := g.render()
	if err != nil {
		_ = os.WriteFile(outFile+".broken", out, 0o644)

		fatalf("%v", err)
	}

	if err := os.WriteFile(outFile, out, 0o644); err != nil {
		fatalf("%v", err)
	}
}
