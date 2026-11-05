package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"

	"github.com/maeshinshin/ichor/internal/cli"
)

// frontmatter is the header every generated page starts with. strconv.Quote writes a valid YAML
// double-quoted string for the plain-text titles and descriptions used here.
func frontmatter(title, description string) string {
	return fmt.Sprintf("---\ntitle: %s\ndescription: %s\n---\n\n", strconv.Quote(title), strconv.Quote(description))
}

// generateCLI replaces dir with one Markdown page per available ichor command.
func generateCLI(dir string) error {
	root := cli.RootCommand()
	disableAutoGenTag(root)

	byFile := map[string]*cobra.Command{}
	collect(root, byFile)

	if err := os.RemoveAll(dir); err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	prepend := func(file string) string {
		cmd := byFile[filepath.Base(file)]
		if cmd == nil {
			return ""
		}

		return frontmatter(cmd.CommandPath(), cmd.Short)
	}

	return doc.GenMarkdownTreeCustom(root, dir, prepend, func(name string) string { return name })
}

// disableAutoGenTag drops cobra's "Auto generated on <date>" footer, which would change every run.
func disableAutoGenTag(cmd *cobra.Command) {
	cmd.DisableAutoGenTag = true

	for _, c := range cmd.Commands() {
		disableAutoGenTag(c)
	}
}

// collect maps each page file name cobra will write to its command.
func collect(cmd *cobra.Command, into map[string]*cobra.Command) {
	if !cmd.IsAvailableCommand() || cmd.IsAdditionalHelpTopicCommand() {
		return
	}

	into[strings.ReplaceAll(cmd.CommandPath(), " ", "_")+".md"] = cmd

	for _, c := range cmd.Commands() {
		collect(c, into)
	}
}
