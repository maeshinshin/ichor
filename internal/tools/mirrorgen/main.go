// Command mirrorgen generates mirror types for the Talos machinery config
// document kinds on Ichor's allow-list, plus conversions in both
// directions.
//
// It is driven by `go generate` from pkg/typedconfig (see doc.go) and takes
// no flags: the allow-list and the type substitution table are the only
// configuration, and both are hardcoded here on purpose (rule 0).
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
	// pkgBlock holds the volume config document kinds (phase 3: disk roles).
	pkgBlock = "github.com/siderolabs/talos/pkg/machinery/config/types/block"
	// pkgResourcesBlock is a different package from pkgBlock (also named
	// "block") that some of pkgBlock's own fields are typed in, e.g.
	// block.EncryptionProviderType -- the resources/block package.
	pkgResourcesBlock = "github.com/siderolabs/talos/pkg/machinery/resources/block"
	// pkgCel holds cel.Expression, the compiled-CEL type volume disk
	// selectors are written in; see substTable below.
	pkgCel = "github.com/siderolabs/talos/pkg/machinery/cel"
)

// Packages fieldOverrides (below) points into. None of these four are new
// dependencies: all are already in go.mod, pulled in transitively by
// controller-runtime/client-go.
const (
	pkgCoreV1      = "k8s.io/api/core/v1"
	pkgSpecs       = "github.com/opencontainers/runtime-spec/specs-go"
	pkgAuditV1     = "k8s.io/apiserver/pkg/apis/audit/v1"
	pkgAPIServerV1 = "k8s.io/apiserver/pkg/apis/apiserver/v1"
	// pkgKubeletV1 is the one fieldOverrides entry that IS a new direct
	// dependency ("KubeletCredentialProviderConfig gets an
	// override"): k8s.io/kubelet wasn't pulled in transitively by anything
	// already in go.mod.
	pkgKubeletV1 = "k8s.io/kubelet/config/v1"
)

// rootKind is one allow-listed machinery document kind to mirror. Adding a
// kind here is the only way to grow the generated API surface (rule 0: no
// CLI flags, no config file). This is also the allow-list half of the pair
// "P2-2" calls for: api/v1alpha1.TalosConfigPatchDocument's "kind"
// enum is the other half. They can't be the same Go value -- the enum is a
// +kubebuilder:validation:Enum marker, plain text controller-gen reads from
// a source comment -- so main_test.go checks the two agree against the
// generated CRD instead.
type rootKind struct {
	pkgPath  string
	typeName string
	// kind is the discriminator value TalosConfigPatchDocument uses for
	// this root: Talos's own document kind string, or, for the legacy
	// v1alpha1.Config document which carries none, the name it is
	// referred to by.
	kind string
	// kindConst/apiVersion set the embedded meta.Meta on render, if the root
	// type embeds one. Config does not, so both are empty for it.
	kindConst  string
	apiVersion string
}

// apiVersionV1Alpha1 is every allow-listed kind's document API version today;
// Config has none (see rootKind.apiVersion's doc).
const apiVersionV1Alpha1 = "v1alpha1"

var roots = []rootKind{
	{pkgRuntime, "SysctlConfigV1Alpha1", "SysctlConfig", "mruntime.SysctlConfigKind", apiVersionV1Alpha1},
	{pkgRuntime, "KernelModuleConfigV1Alpha1", "KernelModuleConfig", "mruntime.KernelModuleConfigKind", apiVersionV1Alpha1},
	{pkgV1Alpha1, "Config", "Config", "", ""},
	// Phase 3 (disk roles): placing EPHEMERAL or STATE on their own disk, a
	// user volume, and a raw volume handed to a CSI provisioner are each a
	// separate modern document kind, not a v1alpha1.Config field.
	{pkgBlock, "VolumeConfigV1Alpha1", "VolumeConfig", "mblock.VolumeConfigKind", apiVersionV1Alpha1},
	{pkgBlock, "UserVolumeConfigV1Alpha1", "UserVolumeConfig", "mblock.UserVolumeConfigKind", apiVersionV1Alpha1},
	{pkgBlock, "RawVolumeConfigV1Alpha1", "RawVolumeConfig", "mblock.RawVolumeConfigKind", apiVersionV1Alpha1},
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

// substTable is the ten-ish-entry table of field types that
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
	// Endpoint was not on the list: it embeds *url.URL anonymously
	// (no exported fields of its own, custom YAML marshal to/from a plain
	// URL string) and was only discovered by walking v1alpha1.Config. Same
	// treatment as the rest of the table (rule 4: add and report).
	pkgV1Alpha1 + ".Endpoint": substScalar,
	// Phase 3 additions, same reasoning as the rest of the table: each is a
	// struct with no usable exported fields (cel.Expression holds a
	// compiled AST; block.Size/ByteSize hold private fields) behind a
	// TextMarshaler/TextUnmarshaler, or a plain int enum behind one
	// (block.VolumeType etc.) -- round-tripping the text form is more
	// useful on the wire than the enum's bare ordinal would be.
	pkgCel + ".Expression":                        substScalar,
	pkgBlock + ".Size":                            substScalar,
	pkgBlock + ".ByteSize":                        substScalar,
	pkgResourcesBlock + ".VolumeType":             substScalar,
	pkgResourcesBlock + ".FilesystemType":         substScalar,
	pkgResourcesBlock + ".EncryptionProviderType": substScalar,
}

// fieldOverride names the upstream type one specific field mirrors as,
// instead of being dropped. See fieldOverrides below.
type fieldOverride struct {
	pkgPath  string
	typeName string

	// localWrapper, if set, is the name of a hand-written type in
	// pkg/typedconfig to use as the mirror type instead of pkgPath.typeName
	// directly. Needed only for MachineSeccompProfileValue: specs.LinuxSeccomp
	// has no DeepCopy of its own, and controller-gen's object:generate assumes
	// one exists on any struct-shaped field type it doesn't recognize --
	// fine for the other four overrides (all Kubernetes API types, which
	// always provide one) but not for this one. A hand-written DeepCopyInto
	// fixes that, but only if the type carrying it is never itself a slice
	// element of pointers elsewhere (controller-gen's generated code for
	// that shape assumes a value-typed element and double-dereferences);
	// MachineSeccompProfile is exactly that shape
	// (MachineConfig.MachineSeccompProfiles []*MachineSeccompProfile), so
	// the hand-written methods live one level down, on a local type that
	// mirrors specs.LinuxSeccomp's fields exactly (identical JSON shape,
	// so the generic conversion helpers and the CRD schema are unaffected)
	// and appears only as a plain pointer field. See
	// pkg/typedconfig/seccomp.go.
	localWrapper string
}

// fieldOverrides is the field-level table's follow-up calls for.
// All eleven meta.Unstructured fields the walk below would otherwise drop
// share one Go type, so substTable above -- keyed by type -- can't tell them
// apart; this table is keyed "StructName.FieldName" instead, unambiguously,
// and is consulted before mapType ever sees the field's (always
// meta.Unstructured) static type. Each entry here is one of the five's measurement found generates a clean CRD
// schema with no new
// dependency; the other six measured unstructured fields are left dropped
// (new dependency, controller-gen errors, or no single schema to mirror).
var fieldOverrides = map[string]fieldOverride{
	"ResourcesConfig.Requests":                              {pkgPath: pkgCoreV1, typeName: "ResourceList"},
	"ResourcesConfig.Limits":                                {pkgPath: pkgCoreV1, typeName: "ResourceList"},
	"MachineSeccompProfile.MachineSeccompProfileValue":      {pkgPath: pkgSpecs, typeName: "LinuxSeccomp", localWrapper: "SeccompProfile"},
	"APIServerConfig.AuditPolicyConfig":                     {pkgPath: pkgAuditV1, typeName: "Policy"},
	"AuthorizationConfigAuthorizerConfig.AuthorizerWebhook": {pkgPath: pkgAPIServerV1, typeName: "WebhookConfiguration"},
	// Sixth override "KubeletCredentialProviderConfig gets an
	// override" follow-up: generates a clean CRD schema like the five above,
	// but (unlike them) needs a new direct dependency, k8s.io/kubelet --
	// that was the only thing holding it back, not the generator.
	"KubeletConfig.KubeletCredentialProviderConfig": {pkgPath: pkgKubeletV1, typeName: "CredentialProviderConfig"},
}

// secretFields are machinery fields that carry cluster secret material --
// certificates, tokens, encryption keys -- keyed "StructName.FieldName"
// like fieldOverrides above, but for a different reason: these are dropped
// regardless of their Go type (most are plain strings or *x509 pointers,
// not meta.Unstructured), because they must not be representable, not
// because they can't be. A TalosConfigPatch author scoped to an unrelated
// layer (e.g. a sysctl tweak) could otherwise set one of these and have
// configpatcher.Apply's strategic merge overwrite the real value the
// secrets bundle generated, reaching a node as a cluster takeover rather
// than a config error.
//
// A CEL validation rule was considered and rejected: it only guards
// whatever code path remembers to check it, including every kind and every
// field machinery adds in the future. Leaving the field out of the CRD
// schema entirely means the API server rejects it structurally -- there is
// no path to forget.
var secretFields = map[string]string{ // "Struct.Field" -> what it carries
	"ClusterConfig.ClusterSecret":                 "cluster discovery secret",
	"ClusterConfig.BootstrapToken":                "etcd cluster bootstrap token",
	"ClusterConfig.ClusterAESCBCEncryptionSecret": "etcd AES-CBC encryption key",
	"ClusterConfig.ClusterCA":                     "cluster CA key pair",
	"ClusterConfig.ClusterAggregatorCA":           "aggregator CA key pair",
	"ClusterConfig.ClusterServiceAccount":         "Kubernetes service account signing key",
	"EtcdConfig.RootCA":                           "etcd CA key pair",
	"MachineConfig.MachineToken":                  "machine join token",
	"MachineConfig.MachineCA":                     "machine CA key pair",
	// Phase 3 (disk roles): EncryptionSpec.EncryptionKeys[].static is a disk
	// encryption passphrase embedded directly in the document -- Talos's own
	// EncryptionSpec.Redact method targets exactly this field for the same
	// reason. The whole "static" key source is dropped, not just its one
	// field, because an empty "static: {}" left behind is unusable and only
	// confusing. KMS/TPM/nodeID key sources carry no secret (an endpoint
	// URL, hardware-sealed, or derived), so they stay. This key is shared by
	// the legacy v1alpha1.EncryptionKey and the modern block.EncryptionKey
	// (same field name, both struct literals below have an identically
	// named and shaped "EncryptionKeyStatic" type), so one entry drops both.
	"EncryptionKey.KeyStatic": "disk encryption static passphrase",
}

// typeRenames is the disambiguation table for the handful of block types
// whose bare name collides with an existing, unrelated legacy v1alpha1
// mirror struct of the same name (EncryptionKey and friends: v1alpha1 has
// its own, older disk-encryption-key type family). Mirror struct names are
// one flat namespace (zz_generated.mirror.go), so a real collision --
// unlike the "same meta.Unstructured type" problem fieldOverrides solves --
// has to be resolved by renaming one side; the legacy v1alpha1 names stay
// put; the newer, less-established block ones take the "Volume" prefix.
var typeRenames = map[string]string{
	pkgBlock + ".EncryptionKey":       "VolumeEncryptionKey",
	pkgBlock + ".EncryptionKeyKMS":    "VolumeEncryptionKeyKMS",
	pkgBlock + ".EncryptionKeyTPM":    "VolumeEncryptionKeyTPM",
	pkgBlock + ".EncryptionKeyNodeID": "VolumeEncryptionKeyNodeID",
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "mirrorgen: "+format+"\n", args...)
	os.Exit(1)
}

func main() {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedTypes | packages.NeedTypesInfo |
			packages.NeedDeps | packages.NeedImports,
	}

	pkgs, err := packages.Load(cfg, pkgRuntime, pkgV1Alpha1, pkgBlock, pkgCoreV1, pkgSpecs, pkgAuditV1, pkgAPIServerV1, pkgKubeletV1)
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
		def.addPage(referencePage(r))
	}

	g.report()
	g.writeMirror("zz_generated.mirror.go")
	g.writeConversion("zz_generated.conversion.go")
}
