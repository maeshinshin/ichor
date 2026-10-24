// Command mirrorgen generates mirror types for the Talos machinery config
// document kinds on Ichor's allow-list, plus conversions in both
// directions. See docs/adr/0003-typed-config-mirror-types.md.
//
// It is driven by `go generate` from pkg/typedconfig (see doc.go) and takes
// no flags: the allow-list and the type substitution table are the only
// configuration, and both are hardcoded here on purpose (ADR 0003, rule 0).
package main

import (
	"fmt"
	"go/types"
	"os"

	"golang.org/x/tools/go/packages"
)

// The machinery packages this generator ever walks into. A field type from
// outside this set (e.g. github.com/siderolabs/crypto/x509) is still
// mirrored as a struct if it has usable exported fields; these constants
// are only needed to recognize the special-cased types below.
const (
	pkgRuntime  = "github.com/siderolabs/talos/pkg/machinery/config/types/runtime"
	pkgV1Alpha1 = "github.com/siderolabs/talos/pkg/machinery/config/types/v1alpha1"
	pkgMeta     = "github.com/siderolabs/talos/pkg/machinery/config/types/meta"
)

// rootKind is one allow-listed machinery document kind to mirror. Adding a
// kind here is the only way to grow the generated API surface (rule 0: no
// CLI flags, no config file). This is also the allow-list half of the pair
// ADR 0003 "P2-2" calls for: api/v1alpha1.TalosConfigPatchDocument's "kind"
// enum is the other half. They can't be the same Go value -- the enum is a
// +kubebuilder:validation:Enum marker, plain text controller-gen reads from
// a source comment -- so main_test.go checks the two agree against the
// generated CRD instead.
type rootKind struct {
	pkgPath  string
	typeName string
	// kind is the discriminator value TalosConfigPatchDocument uses for
	// this root: Talos's own document kind string, or, for the legacy
	// v1alpha1.Config document which carries none, the name ADR 0003 refers
	// to it by.
	kind string
	// kindConst/apiVersion set the embedded meta.Meta on render, if the root
	// type embeds one. Config does not, so both are empty for it.
	kindConst  string
	apiVersion string
}

var roots = []rootKind{
	{pkgRuntime, "SysctlConfigV1Alpha1", "SysctlConfig", "mruntime.SysctlConfigKind", "v1alpha1"},
	{pkgRuntime, "KernelModuleConfigV1Alpha1", "KernelModuleConfig", "mruntime.KernelModuleConfigKind", "v1alpha1"},
	{pkgV1Alpha1, "Config", "Config", "", ""},
}

// substKind says how a substituted-by-type field converts to and from its
// mirror representation.
type substKind int

const (
	substScalar   substKind = iota // mirror is a string; round-trips through the type's own YAML/Text marshaller
	substArgValue                  // meta.ArgValue specifically: mirror is []string (see substitution.go)
)

// mirrorExpr is the mirror-side Go type for a substKind: every substScalar
// entry is a string, and the one substArgValue entry (meta.ArgValue) is
// []string -- see substitution.go.
func (k substKind) mirrorExpr() string {
	if k == substArgValue {
		return "[]string"
	}

	return "string"
}

// substTable is the ten-ish-entry table ADR 0003 calls for: field types that
// have no faithful structural-schema representation, keyed by Go type, not
// by field path. Base64Bytes is deliberately absent: its underlying type is
// already []byte, so it is handled by the generic named-[]byte rule in
// walk.go instead of needing an entry here.
var substTable = map[string]substKind{
	"time.Duration":                         substScalar,
	pkgMeta + ".Addr":                       substScalar,
	pkgMeta + ".Prefix":                     substScalar,
	pkgMeta + ".AddrPort":                   substScalar,
	pkgMeta + ".ArgValue":                   substArgValue,
	pkgV1Alpha1 + ".DiskSize":               substScalar,
	pkgV1Alpha1 + ".FileMode":               substScalar,
	pkgV1Alpha1 + ".InstallDiskSizeMatcher": substScalar,
	// Endpoint was not on the ADR's list: it embeds *url.URL anonymously
	// (no exported fields of its own, custom YAML marshal to/from a plain
	// URL string) and was only discovered by walking v1alpha1.Config. Same
	// treatment as the rest of the table (rule 4: add and report).
	pkgV1Alpha1 + ".Endpoint": substScalar,
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "mirrorgen: "+format+"\n", args...)
	os.Exit(1)
}

func main() {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedTypes | packages.NeedTypesInfo |
			packages.NeedSyntax | packages.NeedDeps | packages.NeedImports,
	}

	pkgs, err := packages.Load(cfg, pkgRuntime, pkgV1Alpha1)
	if err != nil {
		fatalf("loading packages: %v", err)
	}

	for _, p := range pkgs {
		for _, e := range p.Errors {
			fatalf("%s: %v", p.PkgPath, e)
		}
	}

	g := newGenerator(pkgs)

	for _, r := range roots {
		named := g.lookupType(r.pkgPath, r.typeName)

		st, ok := named.Underlying().(*types.Struct)
		if !ok {
			fatalf("root %s.%s is not a struct", r.pkgPath, r.typeName)
		}

		g.structMapping(named, st)

		def := g.structs[named]
		def.isRoot = true
		def.rootKind = r
	}

	g.report()
	g.writeMirror("zz_generated.mirror.go")
	g.writeConversion("zz_generated.conversion.go")
}
