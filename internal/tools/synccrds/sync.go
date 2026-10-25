package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// specLine matches a top-level "spec:" line (never a nested one -- those are always
// indented), and endLine matches the chart wrapper's trailing "{{- end }}" line.
var (
	specLine = regexp.MustCompile(`(?m)^spec:$`)
	endLine  = regexp.MustCompile(`(?m)^\{\{- end \}\}[ \t]*$`)
)

// syncDir syncs every CRD schema in configDir into its matching file in chartDir.
func syncDir(configDir, chartDir string) error {
	matches, err := filepath.Glob(filepath.Join(configDir, "*.yaml"))
	if err != nil {
		return err
	}
	slices.Sort(matches)
	for _, configPath := range matches {
		if err := syncOne(configPath, chartDir); err != nil {
			return err
		}
	}
	return nil
}

// chartFilename derives dist/chart/templates/crd/'s filename for a config/crd/bases/
// file: config names it "<group>_<plural>.yaml", the chart names the same CRD
// "<plural>.<group>.yaml" -- both controller-gen/kubebuilder's own conventions, not
// something this command invents.
func chartFilename(configPath string) (string, error) {
	base := strings.TrimSuffix(filepath.Base(configPath), ".yaml")
	group, plural, ok := strings.Cut(base, "_")
	if !ok {
		return "", fmt.Errorf("%s: expected a filename of the form <group>_<plural>.yaml", configPath)
	}
	return plural + "." + group + ".yaml", nil
}

// specSection returns text from its one top-level "spec:" line to the end, failing
// loudly if that line is missing or not unique -- a silent wrong match would corrupt
// the chart file instead of just failing to update it.
func specSection(text, path string) (string, error) {
	locs := specLine.FindAllStringIndex(text, -1)
	if len(locs) != 1 {
		return "", fmt.Errorf(`%s: expected exactly one top-level "spec:" line, found %d`, path, len(locs))
	}
	return text[locs[0][0]:], nil
}

// syncOne replaces chartDir's copy of configPath's CRD "spec:" block with configPath's
// own, keeping everything in the chart file before "spec:" and from its trailing
// "{{- end }}" onward untouched.
func syncOne(configPath, chartDir string) error {
	chartName, err := chartFilename(configPath)
	if err != nil {
		return err
	}
	chartPath := filepath.Join(chartDir, chartName)

	configBytes, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	newSpec, err := specSection(string(configBytes), configPath)
	if err != nil {
		return err
	}

	chartBytes, err := os.ReadFile(chartPath)
	if err != nil {
		return fmt.Errorf(
			"%s does not exist; a new CRD needs its chart wrapper "+
				"({{- if .Values.crd.enabled }}/metadata templating) written by hand once, "+
				"modeled on an existing file in %s, before this can sync its spec: %w",
			chartPath, chartDir, err,
		)
	}
	chartText := string(chartBytes)

	specLocs := specLine.FindAllStringIndex(chartText, -1)
	if len(specLocs) != 1 {
		return fmt.Errorf(`%s: expected exactly one top-level "spec:" line, found %d`, chartPath, len(specLocs))
	}
	endLocs := endLine.FindAllStringIndex(chartText, -1)
	if len(endLocs) != 1 {
		return fmt.Errorf(`%s: expected exactly one trailing "{{- end }}" line, found %d`, chartPath, len(endLocs))
	}
	specStart, endStart := specLocs[0][0], endLocs[0][0]
	if endStart < specStart {
		return fmt.Errorf(`%s: "{{- end }}" appears before "spec:"`, chartPath)
	}

	updated := chartText[:specStart] + newSpec + chartText[endStart:]
	if updated == chartText {
		return nil
	}
	if err := os.WriteFile(chartPath, []byte(updated), 0o644); err != nil {
		return err
	}
	fmt.Printf("synced %s from %s\n", chartPath, configPath)
	return nil
}
