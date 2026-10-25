// Command synccrds copies each CRD's schema from config/crd/bases/ into the matching
// file in dist/chart/templates/crd/, byte for byte. It replaces the "spec:" block
// (controller-gen's output: group, names, scope, versions/schema) and leaves everything
// around it -- the "{{- if .Values.crd.enabled }}" wrapper, the
// "{{- if .Values.crd.keep }}" annotation templating, and the trailing "{{- end }}" --
// untouched, since none of that exists in config/ for this command to copy from.
//
// Run via "make sync-crds" (also run automatically by "make manifests").
// CONTRIBUTING.md explains why this replaced "kubebuilder edit --plugins=helm/v2-alpha"
// for the whole chart: that plugin regenerated dist/chart/templates/ wholesale and
// reverted the chart's own hand-edits in the process, twice. A CRD's schema carries no
// hand-edits, so syncing just that piece is the one part still worth automating.
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := syncDir("config/crd/bases", "dist/chart/templates/crd"); err != nil {
		fmt.Fprintln(os.Stderr, "synccrds:", err)
		os.Exit(1)
	}
}
