// Package diagramlabels keeps the docs' draw.io diagrams to plain SVG text labels. draw.io writes a label as
// HTML when its cell has html=1, and an SVG that carries such a label has a foreignObject element; the
// diagrams were changed to plain text labels (html=0) so that phones render them. The rule is kept here, so
// a diagram added later cannot break it without a failing test. It is described in
// docs/development/contributing-docs.md, under Diagrams.
package diagramlabels

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// repoRoot is the module root, relative to this package's directory, where `go test` runs.
const repoRoot = "../../.."

type cell struct{ id, value, style string }

// walk returns every mxCell of the XML in src, the number of foreignObject elements, and the content attribute of
// the root svg (the escaped draw.io model of a .drawio.svg, empty otherwise).
func walk(src []byte) (cells []cell, foreign int, content string, err error) {
	d := xml.NewDecoder(bytes.NewReader(src))
	for {
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			return cells, foreign, content, nil
		}
		if err != nil {
			return nil, 0, "", err
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "foreignObject":
			foreign++
		case "svg":
			for _, a := range se.Attr {
				if a.Name.Local == "content" {
					content = a.Value
				}
			}
		case "mxCell":
			var c cell
			for _, a := range se.Attr {
				switch a.Name.Local {
				case "id":
					c.id = a.Value
				case "value":
					c.value = a.Value
				case "style":
					c.style = a.Value
				}
			}
			cells = append(cells, c)
		}
	}
}

// htmlLabel reports whether a draw.io style sets html=1, the flag that makes a cell's label HTML. A style that
// has no html key is plain: draw.io reads a missing key as the default, which is not HTML, so only html=1 is tested.
func htmlLabel(style string) bool {
	for token := range strings.SplitSeq(style, ";") {
		if token == "html=1" {
			return true
		}
	}
	return false
}

// htmlBreak reports whether a label value holds an HTML line break. Its line break must be a real newline (&#xa;
// in the XML), which is what draw.io writes; a <br> or <div> in the text is HTML written into a plain label.
func htmlBreak(value string) bool {
	v := strings.ToLower(value)
	return strings.Contains(v, "<br") || strings.Contains(v, "<div")
}

// problems returns one line per break of the rule in a diagram file: a cell with html=1 or an HTML line break in
// its value, named by its id and value, and any foreignObject in the SVG. A file without a readable model is an
// error, not a pass.
func problems(name string, src []byte) ([]string, error) {
	cells, foreign, content, err := walk(src)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if content != "" { // a .drawio.svg: draw.io escapes its model into the svg's content attribute
		if cells, _, _, err = walk([]byte(content)); err != nil {
			return nil, fmt.Errorf("%s: the model in the content attribute: %w", name, err)
		}
	}
	if len(cells) == 0 {
		return nil, fmt.Errorf("%s: no draw.io model (mxCell) found; a compressed model cannot be read, export the file again with make docs-diagrams", name)
	}
	var out []string
	for _, c := range cells {
		if htmlLabel(c.style) {
			out = append(out, fmt.Sprintf("%s: cell %q (value %q) has html=1; a label must be plain SVG text, html=0", name, c.id, c.value))
		}
		if htmlBreak(c.value) {
			out = append(out, fmt.Sprintf("%s: cell %q (value %q) has an HTML line break (<br> or <div>); use a real newline (&#xa;)", name, c.id, c.value))
		}
	}
	if foreign > 0 {
		out = append(out, fmt.Sprintf("%s: %d foreignObject element(s) in the SVG; labels drawn as HTML are not plain SVG text", name, foreign))
	}
	return out, nil
}

// TestDiagramLabelsAreSVGText fails when a diagram in docs/images carries an HTML label. The glob covers the
// exported pairs (NAME.light.drawio.svg, NAME.dark.drawio.svg) and the plain NAME.drawio sources.
func TestDiagramLabelsAreSVGText(t *testing.T) {
	var files []string
	for _, pattern := range []string{"*.drawio.svg", "*.drawio"} {
		matches, err := filepath.Glob(filepath.Join(repoRoot, "docs", "images", pattern))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, matches...)
	}
	if len(files) == 0 {
		t.Fatal("no diagrams under docs/images: the directory moved or the glob is wrong")
	}
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		got, err := problems(filepath.Base(file), src)
		if err != nil {
			t.Error(err)
		}
		for _, p := range got {
			t.Error(p)
		}
	}
}

func TestProblemsNamesTheCellAndTheForeignObject(t *testing.T) {
	plain := `<mxfile><diagram><mxGraphModel><root><mxCell id="0"/><mxCell id="a" value="Ready" style="text;html=0;"/><mxCell id="b" value="Done" style="text;html=1;"/></root></mxGraphModel></diagram></mxfile>`
	got, err := problems("x.drawio", []byte(plain))
	if err != nil || len(got) != 1 || !strings.Contains(got[0], `cell "b"`) {
		t.Errorf("plain model: got %q, %v; want one problem naming cell \"b\"", got, err)
	}

	// The same model escaped into a .drawio.svg, with a foreignObject in the rendering.
	svg := `<svg xmlns="http://www.w3.org/2000/svg" content="` + strings.NewReplacer(`"`, "&quot;", "<", "&lt;", ">", "&gt;").Replace(plain) + `"><foreignObject/></svg>`
	got, err = problems("x.drawio.svg", []byte(svg))
	if err != nil || len(got) != 2 {
		t.Errorf("svg model: got %q, %v; want the html=1 cell and the foreignObject", got, err)
	}

	if _, err := problems("y.drawio", []byte(`<mxfile><diagram>compressed-text</diagram></mxfile>`)); err == nil {
		t.Error("a file with no readable model passed; it must be an error")
	}
}

// TestProblemsFlagsHTMLLineBreaksAndPassesAMissingHTMLKey pins two of the rule's edges. A label with <br> or <div>
// is an HTML label in a plain cell, so it is named. A cell whose style has no html key passes: draw.io takes a missing
// key as the default, which is plain, so the test looks only for html=1.
func TestProblemsFlagsHTMLLineBreaksAndPassesAMissingHTMLKey(t *testing.T) {
	model := `<mxfile><diagram><mxGraphModel><root><mxCell id="0"/><mxCell id="br" value="a&lt;br&gt;b" style="text;html=0;"/><mxCell id="nokey" value="plain" style="text;fontFamily=Helvetica;"/></root></mxGraphModel></diagram></mxfile>`
	got, err := problems("x.drawio", []byte(model))
	if err != nil || len(got) != 1 || !strings.Contains(got[0], `cell "br"`) || !strings.Contains(got[0], "line break") {
		t.Errorf("got %q, %v; want one line-break problem naming cell \"br\"", got, err)
	}
}

var (
	fenced     = regexp.MustCompile("(?s)```.*?(?:```|$)|~~~.*?(?:~~~|$)")
	inlineCode = regexp.MustCompile("`[^`\n]*`")
	embed      = regexp.MustCompile(`\]\([^)\s]*light\.drawio\.svg\)`)
)

// hasDiagram reports whether a page embeds a light draw.io diagram outside code.
func hasDiagram(page string) bool {
	return embed.MatchString(inlineCode.ReplaceAllString(fenced.ReplaceAllString(page, ""), ""))
}

// TestEveryPageHasADiagram gates the docs rule that every published page embeds a diagram. It walks what
// site/src/content.config.ts globs (docs/**/*.md minus adr/**) and skips the generated reference pages.
func TestEveryPageHasADiagram(t *testing.T) {
	docs := filepath.Join(repoRoot, "docs")
	var missing []string
	err := filepath.WalkDir(docs, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return err
		}
		rel, _ := filepath.Rel(docs, path)
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "adr/") || strings.HasPrefix(rel, "reference/cli/") || strings.HasPrefix(rel, "reference/crds/") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err == nil && !hasDiagram(string(b)) {
			missing = append(missing, rel)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) > 0 {
		t.Errorf("pages without a draw.io diagram (docs/development/contributing-docs.md, Diagrams):\n%s", strings.Join(missing, "\n"))
	}
}

func TestHasDiagramIgnoresCode(t *testing.T) {
	const img = "![x](../images/a.light.drawio.svg)"
	for page, want := range map[string]bool{img: true, "`" + img + "`": false, "```md\n" + img + "\n```\n": false, "```\n```\n" + img: true} {
		if got := hasDiagram(page); got != want {
			t.Errorf("hasDiagram(%q) = %v, want %v", page, got, want)
		}
	}
}
