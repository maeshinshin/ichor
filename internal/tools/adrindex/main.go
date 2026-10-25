// Command adrindex regenerates docs/adr/README.md's index table from each
// ADR file's own "Decided:"/"Status:" header line, so the table is never
// hand-maintained (and never conflicts across agents editing different
// ADRs at once -- the problem that motivated this command).
//
// Run via "make adr-index".
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := generateIndex("docs/adr"); err != nil {
		fmt.Fprintln(os.Stderr, "adrindex:", err)
		os.Exit(1)
	}
}
