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

	if err := doc.GenMarkdownTreeCustom(root, dir, prepend, func(name string) string { return name }); err != nil {
		return err
	}

	return labelFences(dir)
}

// disableAutoGenTag drops cobra's "Auto generated on <date>" footer, which would change every run.
func disableAutoGenTag(cmd *cobra.Command) {
	cmd.DisableAutoGenTag = true

	for _, c := range cmd.Commands() {
		disableAutoGenTag(c)
	}
}

// labelFences gives the code blocks cobra writes a language, so the site highlights them: the usage
// line and the examples are shell, the flag tables under the "Options" headings are plain text.
func labelFences(dir string) error {
	files, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		return err
	}

	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			return err
		}

		lines := strings.Split(string(b), "\n")
		heading, inFence := "", false

		for i, l := range lines {
			switch {
			case inFence:
				inFence = l != "```"
			case l == "```":
				lang := "sh"
				if strings.HasPrefix(heading, "### Options") {
					lang = "text"
				}

				lines[i], inFence = "```"+lang, true
			case strings.HasPrefix(l, "```"):
				inFence = true
			case strings.HasPrefix(l, "#"):
				heading = l
			}
		}

		if err := os.WriteFile(file, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
			return err
		}
	}

	return nil
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
