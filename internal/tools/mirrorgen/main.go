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
	"strings"

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
	// pkgNetwork holds the network document kinds (ResolverConfig first).
	pkgNetwork    = "github.com/siderolabs/talos/pkg/machinery/config/types/network"
	pkgK8s        = "github.com/siderolabs/talos/pkg/machinery/config/types/k8s"
	pkgStorage    = "github.com/siderolabs/talos/pkg/machinery/config/types/storage"
	pkgNethelpers = "github.com/siderolabs/talos/pkg/machinery/nethelpers"
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
	// The three upstream configuration types a subset override mirrors (see fieldOverride.omits).
	pkgKubeletV1Beta1    = "k8s.io/kubelet/config/v1beta1"
	pkgKubeProxyV1Alpha1 = "k8s.io/kube-proxy/config/v1alpha1"
	pkgKubeSchedulerV1   = "k8s.io/kube-scheduler/config/v1"
)

// rootKind is one shipped machinery document kind to mirror, derived from Talos's registry by
// deriveRoots; shipped.go lists which kinds are shipped.
type rootKind struct {
	pkgPath  string
	typeName string
	// kind is the discriminator value TalosConfigPatchDocument uses for
	// this root: Talos's own document kind string, or, for the legacy
	// v1alpha1.Config document which carries none, the name it is
	// referred to by.
	kind string
	// constPkg/constName name the kind constant and apiVersion sets the embedded meta.Meta on render, if the
	// root type embeds one. Config does not, so both are empty for it.
	constPkg, constName string
	apiVersion          string
	// what completes "<field> is a Talos ...; populated when kind is <kind>" in the union field's doc.
	what string
}

// kindConst is the Go expression of the kind constant in generated code, empty when the root has none.
func (r rootKind) kindConst() string {
	if r.constName == "" {
		return ""
	}

	return machImportAlias(r.constPkg) + "." + r.constName
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
	pkgMeta + ".URL":                        substScalar,
	"net/netip.Addr":                        substScalar,
	"net/netip.AddrPort":                    substScalar,
	"net/netip.Prefix":                      substScalar,
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
	// nethelpers.DNSProtocol: an int enum behind a TextMarshaler (Do53/DoT/DoH), like VolumeType.
	pkgNethelpers + ".DNSProtocol": substScalar,
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

	// omits, if set, makes the mirror a local copy of the upstream struct without the fields it names, keyed
	// "UpstreamStruct.jsonName" with the reason, instead of the whole upstream type (see subset.go). Use it
	// where the whole type is too large for the CRD or cannot be expressed in a schema. A document that sets
	// an omitted field is refused when it is read, with the field named. A row is also what makes a field
	// inexpressible in Ichor, so it needs a reason a maintainer can read: the generator fails on a row that
	// names no field, and every field not named here is kept.
	omits map[string]string
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
	// The same three upstream types as the v1alpha1 document's rows above, reached through the document kinds
	// whose only payload they are.
	"KubeAuditPolicyConfigV1Alpha1.AuditConfig":                     {pkgPath: pkgAuditV1, typeName: "Policy"},
	"KubeAuthorizerConfigV1Alpha1.AuthorizerWebhook":                {pkgPath: pkgAPIServerV1, typeName: "WebhookConfiguration"},
	"KubeCredentialProviderConfigV1Alpha1.CredentialProviderConfig": {pkgPath: pkgKubeletV1, typeName: "CredentialProviderConfig"},
	// The three configuration documents whose upstream type cannot be used whole (too large, or with a field a
	// CRD schema cannot express): a local subset, see omits.
	"KubeletConfigV1Alpha1.KubeletConfig":   {pkgPath: pkgKubeletV1Beta1, typeName: "KubeletConfiguration", omits: kubeletOmits},
	"KubeProxyConfigV1Alpha1.ProxyConfig":   {pkgPath: pkgKubeProxyV1Alpha1, typeName: "KubeProxyConfiguration", omits: kubeProxyOmits},
	"KubeSchedulerConfigV1Alpha1.PodConfig": {pkgPath: pkgKubeSchedulerV1, typeName: "KubeSchedulerConfiguration", omits: kubeSchedulerOmits},
	// The pod of a static pod, which Talos types as an untyped object: the core v1 PodSpec is its schema.
	"KubeStaticPodConfigV1Alpha1.PodSpec": {pkgPath: pkgCoreV1, typeName: "PodSpec"},
}

// refusedFields are typed fields of a shipped document that the mirror does not carry because Ichor owns what
// they set, keyed "StructName.FieldName" like secretFields. Unlike a secret field, a document that sets one is
// refused when it is read, naming the field, and the reason is part of the message.
var refusedFields = map[string]string{
	"KubeletConfigV1Alpha1.KubeletImage":     "the kubelet image follows spec.kubernetesVersion and is set by the Kubernetes upgrade",
	"KubeProxyConfigV1Alpha1.ProxyImage":     "the kube-proxy image follows spec.kubernetesVersion and is set by the Kubernetes upgrade",
	"KubeProxyConfigV1Alpha1.ProxyEnabled":   "whether kube-proxy runs is decided by spec.cni.kubeProxy",
	"KubeSchedulerConfigV1Alpha1.PodImage":   "the kube-scheduler image follows spec.kubernetesVersion and is set by the Kubernetes upgrade",
	"KubeSchedulerConfigV1Alpha1.PodEnabled": "whether kube-scheduler runs at all is part of the control plane's shape, which Ichor owns, and not something a config patch may switch off",
}

// sparseFields are machinery fields without `omitempty` that the mirror still emits as optional.
// Machinery tags them as always-present because a full machine config always has them, but a
// TalosConfigPatch is a sparse overlay: a patch that only sets cluster.apiServer must not have to
// repeat machine.type and machine.certSANs (and an empty value is ignored by the strategic merge,
// so leaving them out changes nothing on the node). Relaxing a required field is additive: no
// CRD field is removed or renamed. Keyed "StructName.FieldName" like fieldOverrides.
var sparseFields = map[string]bool{
	"Config.MachineConfig":          true,
	"Config.ClusterConfig":          true,
	"MachineConfig.MachineType":     true,
	"MachineConfig.MachineCertSANs": true,
}

// secretFields are machinery fields that carry secret material, keyed
// "StructName.FieldName" like fieldOverrides above, but for a different
// reason than fieldOverrides: these are dropped regardless of their Go
// type (most are plain strings or *x509 pointers, not meta.Unstructured),
// because they must not be representable, not because they can't be.
// Each entry's value names what it carries; entries fall into two groups
// for two different reasons, both tagged in the value string below so
// the generator's own report says which:
//
//   - Most entries: a TalosConfigPatch author scoped to an unrelated layer
//     (e.g. a sysctl tweak) could otherwise set one of these and have
//     configpatcher.Apply's strategic merge overwrite the real value the
//     secrets bundle generated, reaching a node as a cluster takeover
//     rather than a config error.
//   - Entries tagged "(user credential, not a bundle value)": these are
//     not bundle-generated at all -- a private registry password, a cloud
//     API token, a WireGuard private key -- so the takeover reasoning
//     above does not apply to them; they are dropped instead because
//     design principle 2 ("secrets are referenced, never embedded") rules
//     out writing a user's own credential into a TalosConfigPatch, which
//     the GitOps design explicitly puts in Git. A reference form
//     (TalosCluster.spec.secretsRef's shape: point at a Kubernetes Secret,
//     have the controller resolve it at render time) is the replacement
//     for the ones that need one's follow-up for which do.
//
// A CEL validation rule was considered and rejected for either group: it
// only guards whatever code path remembers to check it, including every
// kind and every field machinery adds in the future. Leaving the field out
// of the CRD schema entirely means the API server rejects it structurally
// -- there is no path to forget.
//
// suspicious.go is this table's safety net: every surviving field whose name
// suggests a secret must be either in this table or in reviewedSurvivors
// with a reason, and TestSuspiciousSurvivorsAreReviewed fails otherwise, so
// a sibling field this table missed (it has happened) or a new
// credential-shaped field a future machinery bump adds cannot ship silently.
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
	// Sibling of ClusterAESCBCEncryptionSecret above (same purpose, the
	// other supported etcd encryption cipher) -- missed in the original
	// pass because nothing checked for it until suspiciousSurvivors did.
	"ClusterConfig.ClusterSecretboxEncryptionSecret": "etcd secretbox encryption key",
	// User credentials, not bundle values -- design principle 2 ("secrets
	// are referenced, never embedded"); see this var's doc comment above.
	// Dropping these makes private-registry authentication inexpressible
	// through Ichor today's follow-up records that cost and the
	// reference-form replacement it needs (a separate work unit).
	"RegistryAuthConfig.RegistryPassword":      "registry password (user credential, not a bundle value)",
	"RegistryAuthConfig.RegistryAuth":          "registry auth string (user credential, not a bundle value)",
	"RegistryAuthConfig.RegistryIdentityToken": "registry identity token (user credential, not a bundle value)",
	// The private half of a registry mTLS client identity. The legacy
	// document reached it through machine.registries.config.<host>.tls, the
	// same key the RegistryTLSConfig document kind already refuses (kindClasses
	// marks it secret). The whole identity goes, not only the key: a
	// "clientIdentity" with a certificate and no key cannot be used.
	"RegistryTLSConfig.TLSClientIdentity": "registry mTLS client certificate and private key (user credential, not a bundle value)",
	// HTTP headers sent when fetching extraManifests URLs. Their usual job is
	// an Authorization or token header for a private source, so the map is a
	// credential container as a whole and its values cannot be told apart.
	"ClusterConfig.ExtraManifestHeaders": "HTTP headers for extraManifests fetches, usually Authorization (user credential, not a bundle value)",
	// The same map on the document kind (KubeExternalManifestConfig): dropped for the same reason, so the kind
	// fetches only from an unauthenticated URL.
	"KubeExternalManifestConfigV1Alpha1.HeadersSpec": "HTTP headers for a manifest fetch, usually Authorization (user credential, not a bundle value)",
	// Cloud provider API tokens and a WireGuard private key: also user
	// credentials under principle 2, but for features outside every
	// current phase, so unlike the registry ones above these need no
	// reference-form replacement yet -- dropping and recording is enough.
	"VIPEquinixMetalConfig.EquinixMetalAPIToken": "Equinix Metal API token (user credential, not a bundle value)",
	"VIPHCloudConfig.HCloudAPIToken":             "Hetzner Cloud API token (user credential, not a bundle value)",
	"DeviceWireguardConfig.WireguardPrivateKey":  "WireGuard private key (user credential, not a bundle value)",
	// Phase 3 (disk roles): EncryptionSpec.EncryptionKeys[].static is a disk
	// encryption passphrase embedded directly in the document -- Talos's own
	// EncryptionSpec.Redact method targets exactly this field for the same
	// reason. The whole "static" key source is dropped, not just its one
	// field, because an empty "static: {}" left behind is unusable and only
	// confusing. KMS/TPM/nodeID key sources carry no secret (an endpoint
	// URL, hardware-sealed, or derived) and are representable without this
	// problem, so they stay -- dropping KeyStatic does not make disk
	// encryption impossible, it leaves only the key types that keep the
	// secret out of the CR in the first place.
	//
	// This key is "EncryptionKey.KeyStatic" -- the bare machinery type
	// name, not whatever the mirror ends up calling it -- because both the
	// legacy v1alpha1.EncryptionKey and the modern block.EncryptionKey
	// share that name and field, and secretFields/fieldOverrides are both
	// matched against the walk's machinery name (walk.go builds fieldKey
	// from named.Obj().Name(), before typeRenames below is applied), not
	// the mirror name a struct ends up with after a rename. Getting that
	// backwards is exactly how this stopped working once: block.EncryptionKey
	// is renamed to VolumeEncryptionKey by typeRenames below, so this entry
	// only matched the legacy, unrenamed side until the lookup was fixed to
	// key on the machinery name -- the modern side (embedded in VolumeConfig/
	// UserVolumeConfig/RawVolumeConfig) silently kept its passphrase field.
	// One entry still drops both, but only because the lookup is keyed
	// correctly now; a table keyed by mirror name would need two entries
	// and break again on the next rename..
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
	// network documents: the newer package takes the prefix, as block does.
	pkgNetwork + ".HostDNSConfig": "ResolverHostDNSConfig",
	// k8s and storage documents: the newer package takes the prefix of its own area.
	pkgK8s + ".ResourcesConfig":      "KubeResourcesConfig",
	pkgStorage + ".ProvisioningSpec": "StorageProvisioningSpec",
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "mirrorgen: "+format+"\n", args...)
	os.Exit(1)
}

// generate walks every shipped root and returns the populated
// generator -- everything main does except reporting and writing files,
// factored out so tests can inspect what the walk actually dropped
// (g.droppedSecret, in particular) against the real machinery source,
// instead of a second hand-maintained copy of what secretFields should
// have caught.
func generate() *generator {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedTypes | packages.NeedTypesInfo |
			packages.NeedSyntax | packages.NeedDeps | packages.NeedImports,
	}

	pkgs, err := packages.Load(cfg, pkgTypesTree, pkgCoreV1, pkgSpecs, pkgAuditV1, pkgAPIServerV1, pkgKubeletV1,
		pkgKubeletV1Beta1, pkgKubeProxyV1Alpha1, pkgKubeSchedulerV1)
	if err != nil {
		fatalf("loading packages: %v", err)
	}

	for _, p := range pkgs {
		for _, e := range p.Errors {
			fatalf("%s: %v", p.PkgPath, e)
		}
	}

	reg, err := scanRegistry(pkgs)
	if err != nil {
		fatalf("%v", err)
	}

	roots, notes, err := deriveRoots(reg, kindClasses, shipped)
	if err != nil {
		fatalf("%v", err)
	}

	g := newGenerator(pkgs)
	g.roots, g.notes, g.pending = roots, notes, pendingKinds(kindClasses, shipped)

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

	return g
}

func main() {
	g := generate()

	if problems := append(g.payloadProblems(droppedPayloads), g.omissionProblems(fieldOverrides, refusedFields)...); len(problems) > 0 {
		fatalf("a shipped kind would be accepted with its content discarded:\n  %s", strings.Join(problems, "\n  "))
	}

	g.report()
	g.writeMirror("zz_generated.mirror.go")
	g.writeConversion("zz_generated.conversion.go")
	mustWriteFile("../../api/v1alpha1/zz_generated.talosconfigpatchdocument.go", unionSource(g.roots))
}
