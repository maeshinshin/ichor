package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// A file is one licence or notice text found in a component.
type file struct {
	Path   string // relative to the component, "LICENSE" for a root file
	Text   string
	Nested bool // below the component root, in a directory of a package the binaries link
}

// The kinds of component, in the order they are listed.
type kind int

const (
	kindGo kind = iota
	kindStd
	kindNPM
	kindCSS
	kindUpstream
	kindChart
	kindData
)

// A component is something an artifact carries that has its own licence.
type component struct {
	Kind    kind
	Name    string
	Version string
	URL     string
	Note    string
	Files   []file
	Notices []file
	// RefLabel names a licence that Files does not hold because the package ships no licence file;
	// the text is the one the Go modules already carry under that label.
	RefLabel  string
	Copyright []string // for RefLabel
}

func (c component) id() string {
	switch {
	case c.Version == "":
		return c.Name
	case c.Kind == kindStd || c.Kind == kindChart:
		return c.Name + " " + c.Version
	}
	return c.Name + "@" + c.Version
}

// The shipped binaries and the platforms they are built for (.goreleaser.yaml, .ko.yaml).
const linux = "linux"

// apache is the licence label of the Apache-2.0 texts the Go module pool resolves.
const apache = "Apache-2.0"

var builds = []struct {
	pkg, tags string
	goos      []string
}{
	{"./cmd", "", []string{linux}},
	{"./cmd/ichor-ui", "uiembed", []string{linux}},
	{"./cmd/ichor", "installembed", []string{linux, "darwin"}},
}

var (
	licenseName = regexp.MustCompile(`(?i)^(licen[sc]es?|copying|unlicense)([-_.].*)?$`)
	noticeName  = regexp.MustCompile(`(?i)^notice([-_.].*)?$`)
	codeExt     = regexp.MustCompile(`(?i)\.(go|js|ts|tsx|json|py|c|h)$`)
)

// docsLicense names licence files that cover a module's documentation, which no binary carries.
const docsLicense = "LICENSE.docs"

func isLicense(n string) bool {
	return (licenseName.MatchString(n) || n == "PATENTS") && n != docsLicense && !codeExt.MatchString(n)
}
func isNotice(n string) bool { return noticeName.MatchString(n) && !codeExt.MatchString(n) }

// readDirFiles returns the licence and notice files directly in dir, named rel/<name>.
func readDirFiles(dir, rel string, nested bool) (lic, notice []file, err error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() || (!isLicense(n) && !isNotice(n)) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			return nil, nil, err
		}
		f := file{Path: path.Join(rel, n), Text: string(b), Nested: nested}
		if isNotice(n) {
			notice = append(notice, f)
		} else {
			lic = append(lic, f)
		}
	}
	// A Markdown licence file next to a plain one only explains how the others apply.
	if len(lic) > 1 {
		lic = slices.DeleteFunc(lic, func(f file) bool { return strings.HasSuffix(f.Path, ".md") })
	}
	return lic, notice, nil
}

// goComponents lists the third-party modules the three binaries link, with the licence files of
// each module root and of every directory between the root and a linked package.
func goComponents() ([]component, error) {
	type mod struct {
		path, version, dir string
		pkgDirs            map[string]bool
	}
	mods := map[string]*mod{}
	format := `{{with .Module}}{{if not .Main}}{{.Path}}|{{.Version}}|{{.Dir}}|{{$.Dir}}{{end}}{{end}}`
	for _, b := range builds {
		for _, goos := range b.goos {
			for _, arch := range []string{"amd64", "arm64"} {
				cmd := exec.Command("go", "list", "-e", "-deps", "-tags", b.tags, "-f", format, b.pkg)
				cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+goos, "GOARCH="+arch)
				cmd.Stderr = os.Stderr
				out, err := cmd.Output()
				if err != nil {
					return nil, fmt.Errorf("go list %s (%s/%s): %w", b.pkg, goos, arch, err)
				}
				for line := range strings.FieldsSeq(string(out)) {
					p := strings.Split(line, "|")
					if len(p) != 4 || p[2] == "" {
						return nil, fmt.Errorf("go list gave %q: is the module cache complete (go mod download)?", line)
					}
					m := mods[p[0]+"@"+p[1]]
					if m == nil {
						m = &mod{p[0], p[1], p[2], map[string]bool{}}
						mods[p[0]+"@"+p[1]] = m
					}
					m.pkgDirs[p[3]] = true
				}
			}
		}
	}
	var out []component
	for _, m := range mods {
		c := component{Kind: kindGo, Name: m.path, Version: m.version}
		lic, notice, err := readDirFiles(m.dir, "", false)
		if err != nil {
			return nil, err
		}
		c.Files, c.Notices = lic, notice
		seen := map[string]bool{m.dir: true}
		for d := range m.pkgDirs {
			for ; !seen[d] && strings.HasPrefix(d, m.dir+string(filepath.Separator)); d = filepath.Dir(d) {
				seen[d] = true
				rel, _ := filepath.Rel(m.dir, d)
				l, n, err := readDirFiles(d, filepath.ToSlash(rel), true)
				if err != nil {
					return nil, err
				}
				c.Files, c.Notices = append(c.Files, l...), append(c.Notices, n...)
			}
		}
		if len(c.Files) == 0 {
			return nil, fmt.Errorf("%s has no licence file", c.id())
		}
		out = append(out, c)
	}
	return out, nil
}

// stdComponent is the Go standard library, which is no module: LICENSE and PATENTS of GOROOT.
func stdComponent() (component, error) {
	root, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		return component{}, err
	}
	lic, _, err := readDirFiles(strings.TrimSpace(string(root)), "", false)
	if err != nil {
		return component{}, err
	}
	// shortcut: the version is the go directive of go.mod, which the pinned toolchain equals; read it from the toolchain if they ever differ.
	v, err := exec.Command("go", "list", "-m", "-f", "{{.GoVersion}}").Output()
	if err != nil {
		return component{}, err
	}
	return component{Kind: kindStd, Name: "Go standard library", Version: "go" + strings.TrimSpace(string(v)), URL: "https://go.dev", Files: lic}, nil
}

// npmComponents lists the UI's production dependencies, plus the packages whose CSS ui/src/index.css inlines.
func npmComponents(uiDir string) ([]component, error) {
	b, err := os.ReadFile(filepath.Join(uiDir, "package-lock.json"))
	if err != nil {
		return nil, err
	}
	var lock struct {
		Packages map[string]struct {
			Version string `json:"version"`
			Dev     bool   `json:"dev"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(b, &lock); err != nil {
		return nil, err
	}
	css := map[string]bool{}
	index, err := os.ReadFile(filepath.Join(uiDir, "src/index.css"))
	if err != nil {
		return nil, err
	}
	for _, m := range regexp.MustCompile(`@import "([^."][^"]*)"`).FindAllStringSubmatch(string(index), -1) {
		parts := strings.Split(m[1], "/")
		n := 1
		if strings.HasPrefix(m[1], "@") {
			n = 2
		}
		css["node_modules/"+strings.Join(parts[:n], "/")] = true
	}
	var out []component
	for key, p := range lock.Packages {
		if key == "" || (p.Dev && !css[key]) {
			continue
		}
		k := kindNPM
		if p.Dev {
			k = kindCSS
		}
		c, err := npmComponent(filepath.Join(uiDir, key), strings.TrimPrefix(key, "node_modules/"), p.Version, k)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func npmComponent(dir, name, version string, k kind) (component, error) {
	c := component{Kind: k, Name: name, Version: version}
	var pkg struct {
		License    string          `json:"license"`
		Repository json.RawMessage `json:"repository"` // a string or {"url": ...}
	}
	pj, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return c, fmt.Errorf("%s: %w (run make ui-install)", name, err)
	}
	if err := json.Unmarshal(pj, &pkg); err != nil {
		return c, err
	}
	var repo struct{ URL string }
	if json.Unmarshal(pkg.Repository, &repo.URL) != nil {
		_ = json.Unmarshal(pkg.Repository, &repo)
	}
	c.URL = strings.TrimSuffix(strings.TrimPrefix(repo.URL, "git+"), ".git")
	if c.URL != "" && !strings.Contains(c.URL, "://") {
		c.URL = "https://github.com/" + c.URL // package.json shorthand "owner/repo"
	}
	if name == "lucide-preact" {
		// shortcut: named here because the licence file is the only place the Feather notice lives; move it to data if a second package needs a note.
		c.Note = "its licence file also carries the MIT notice for the icons derived from Feather"
	}
	if c.Files, c.Notices, err = readDirFiles(dir, "", false); err != nil {
		return c, err
	}
	if len(c.Files) > 0 {
		return c, nil
	}
	// No licence file in the package: use the licence its package.json names, with the copyright
	// lines its sources carry, and for the Google-derived code of @bufbuild/protobuf the BSD text of its header.
	if !strings.Contains(pkg.License, apache) {
		return c, fmt.Errorf("%s ships no licence file and declares %q", name, pkg.License)
	}
	c.RefLabel = apache
	c.Note = "declared as " + pkg.License + " in its package.json; the package ships no licence file"
	if c.Copyright, err = headerCopyrights(dir); err != nil {
		return c, err
	}
	if strings.Contains(pkg.License, "BSD-3-Clause") {
		h, err := os.ReadFile(filepath.Join(dir, "dist/esm/wire/varint.js"))
		if err != nil {
			return c, err
		}
		var text []string
		for l := range strings.SplitSeq(string(h), "\n") {
			if !strings.HasPrefix(l, "//") {
				break
			}
			text = append(text, strings.TrimPrefix(strings.TrimPrefix(l, "//"), " "))
		}
		c.Files = []file{{Path: "dist/esm/wire/varint.js (header)", Text: strings.Join(text, "\n")}}
		_, _, bsd := split(c.Files[0].Text) // those copyright lines belong to the BSD text, not to the Apache one
		c.Copyright = slices.DeleteFunc(c.Copyright, func(l string) bool { return slices.Contains(bsd, l) })
	}
	return c, nil
}

// headerCopyrights returns the distinct "Copyright" lines in the first lines of the package's .js files.
func headerCopyrights(dir string) ([]string, error) {
	set := map[string]bool{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".js") {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, l := range strings.SplitN(string(b), "\n", 6) {
			if l = strings.TrimSpace(strings.TrimLeft(l, "/* ")); copyrightLine.MatchString(l) {
				set[l] = true
			}
		}
		return nil
	})
	return sortedKeys(set), err
}

func sortedKeys(m map[string]bool) []string { return slices.Sorted(maps.Keys(m)) }

// The upstreams of the UI components that come from the preactcn registry (ui/components.json).
// shortcut: the list is fixed; extend it when a component from another upstream lands in ui/src.
func upstreamComponents() []component {
	mit := func(name, url, copyright, note string) component {
		return component{Kind: kindUpstream, Name: name, URL: url, Note: note, RefLabel: "MIT", Copyright: []string{copyright}}
	}
	return []component{
		mit("shadcn/ui", "https://github.com/shadcn-ui/ui", "Copyright (c) 2023 shadcn", "ui/src/components/ui and the @shadcn/react ports"),
		mit("Radix UI Primitives", "https://github.com/radix-ui/primitives", "Copyright (c) 2022 WorkOS", "ui/src/lib/radix"),
		mit("Base UI", "https://github.com/mui/base-ui", "Copyright (c) 2019 Material-UI SAS", "the Combobox in ui/src/lib/shims"),
		mit("sonner", "https://github.com/emilkowalski/sonner", "Copyright (c) 2023 Emil Kowalski", "ui/src/lib/shims/sonner.tsx"),
		mit("next-themes", "https://github.com/pacocoursey/next-themes", "Copyright (c) 2022 Paco Coursey", "ui/src/lib/shims/next-themes.tsx"),
	}
}

// chartComponent is the Cilium chart pkg/cni/cilium embeds, with the LICENSE inside the archive.
func chartComponent() (component, error) {
	src, err := os.ReadFile("pkg/cni/cilium/render.go")
	if err != nil {
		return component{}, err
	}
	m := regexp.MustCompile(`go:embed (chart/cilium-([0-9.]+)\.tgz)`).FindSubmatch(src)
	if m == nil {
		return component{}, fmt.Errorf("pkg/cni/cilium/render.go embeds no cilium chart")
	}
	f, err := os.Open("pkg/cni/cilium/" + string(m[1]))
	if err != nil {
		return component{}, err
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return component{}, err
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return component{}, fmt.Errorf("no LICENSE in %s", m[1])
		}
		if err != nil {
			return component{}, err
		}
		if h.Name == "cilium/LICENSE" {
			b, err := io.ReadAll(tr)
			return component{Kind: kindChart, Name: "Cilium Helm chart", Version: string(m[2]), URL: "https://github.com/cilium/cilium",
				Note: "pkg/cni/cilium/" + string(m[1]), Files: []file{{Path: "cilium/LICENSE", Text: string(b)}}}, err
		}
	}
}

// trustedRoot is the Sigstore public-good trusted root internal/imagesig embeds. Its licence is the
// Apache-2.0 of the sigstore/root-signing repository, which carries no copyright line.
func trustedRoot() component {
	return component{Kind: kindData, Name: "Sigstore public-good trusted root", URL: "https://github.com/sigstore/root-signing",
		Note: "internal/imagesig/trusted_root.json", RefLabel: apache}
}

// gatewayAPI is the Gateway API CRDs pkg/cni/gatewayapi embeds. The copyright lines are the ones the
// extractor carries at the top of the file from the upstream notice.
func gatewayAPI() (component, error) {
	src, err := os.ReadFile("pkg/cni/gatewayapi/crds.yaml")
	if err != nil {
		return component{}, err
	}
	m := regexp.MustCompile(`Gateway API (v[0-9.]+) standard-channel`).FindSubmatch(src)
	if m == nil {
		return component{}, fmt.Errorf("pkg/cni/gatewayapi/crds.yaml names no Gateway API version")
	}
	var copyrights []string
	for l := range strings.SplitSeq(string(src), "\n") {
		if !strings.HasPrefix(l, "#") {
			break
		}
		if l = strings.TrimSpace(strings.TrimPrefix(l, "#")); copyrightLine.MatchString(l) {
			copyrights = append(copyrights, l)
		}
	}
	if len(copyrights) == 0 {
		return component{}, fmt.Errorf("pkg/cni/gatewayapi/crds.yaml carries no copyright line")
	}
	return component{Kind: kindData, Name: "Gateway API CRDs", Version: string(m[1]), URL: "https://github.com/kubernetes-sigs/gateway-api",
		Note: "pkg/cni/gatewayapi/crds.yaml", RefLabel: apache, Copyright: copyrights}, nil
}
