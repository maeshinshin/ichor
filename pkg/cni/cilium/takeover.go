/*
Copyright 2026 maeshinshin.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cilium

import (
	"bytes"
	"cmp"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/mod/semver"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/release"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes"

	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/values"
)

// This file is the library behind taking over a Cilium that something else
// installed. It is pure: callers pass clients and live
// facts in, and nothing here decides to run it. The pieces, in the order the
// Job uses them:
//
//	ForeignRecords, LiveValues / LiveValuesFromUser  what the live release says
//	CheckVersion                                     the version rule
//	OwnedKeyConflicts, AcceptConflicts               Ichor-owned keys
//	DiffLive                                         the diff gate
//	CheckChartCRDs                                   the chart's crds/ check
//	RetireForeignRecord                              back up, then remove the record
//
// Taking over itself is Apply: with no Ichor record it runs Install with
// TakeOwnership (helm.go).

const (
	// PriorReleaseBackupName is the Secret in the Ichor namespace that keeps a
	// copy of the foreign release records after they are removed.
	PriorReleaseBackupName = "ichor-cilium-prior-release"

	// helmReleaseSecretType is the Type of Helm's Secret storage driver.
	helmReleaseSecretType corev1.SecretType = "helm.sh/release.v1"

	// maxBackupBytes keeps the backup Secret under the 1 MiB object limit.
	maxBackupBytes = 900 << 10

	// maxRecordBytes caps a decoded release record (a zip-bomb guard).
	maxRecordBytes = 64 << 20

	keyTunnelProtocol       = "tunnelProtocol"
	keyKubeProxyReplacement = "kubeProxyReplacement"

	backupMetaSuffix    = ".meta"
	backupReleaseSuffix = ".release"
)

// --- release records -------------------------------------------------------

// DecodeReleaseRecord decodes the "release" key of a Helm release Secret:
// base64, then gzip (older records are not compressed; Helm tells them apart by
// the gzip magic bytes), then JSON. This is the decoding of Helm's Secrets
// storage driver (pkg/storage/driver/util.go, decodeRelease), which is not
// exported.
func DecodeReleaseRecord(data []byte) (*release.Release, error) {
	b, err := base64.StdEncoding.DecodeString(string(data))
	if err != nil {
		return nil, fmt.Errorf("cilium: release record is not base64: %w", err)
	}

	if bytes.HasPrefix(b, []byte{0x1f, 0x8b, 0x08}) {
		zr, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			return nil, fmt.Errorf("cilium: release record is not gzip: %w", err)
		}

		defer func() { _ = zr.Close() }()

		if b, err = io.ReadAll(io.LimitReader(zr, maxRecordBytes)); err != nil {
			return nil, fmt.Errorf("cilium: reading release record: %w", err)
		}
	}

	var rel release.Release
	if err := json.Unmarshal(b, &rel); err != nil {
		return nil, fmt.Errorf("cilium: release record is not a Helm release: %w", err)
	}

	return &rel, nil
}

// Record is one stored revision of a Helm release: the Secret and what it decodes to.
type Record struct {
	Secret  corev1.Secret
	Release *release.Release
}

// Foreign is the release record history of a Cilium that Ichor did not install.
type Foreign struct {
	// Namespace is where the records live.
	Namespace string
	// Records are every revision, oldest first.
	Records []Record
}

// Latest is the newest revision.
func (f *Foreign) Latest() Record { return f.Records[len(f.Records)-1] }

// ForeignOptions select where to look.
type ForeignOptions struct {
	// IchorNamespace is where Ichor keeps its own release records (ichor-system);
	// records there are never foreign.
	IchorNamespace string
	// Namespaces to search; empty searches every namespace.
	Namespaces []string
}

// ForeignRecords finds the Helm release records of a release named "cilium"
// outside Ichor's own storage namespace. It returns (nil, nil) when there are
// none (case C, no record). Records in several namespaces are an error: Ichor
// cannot say which one is the live release.
func ForeignRecords(ctx context.Context, kube kubernetes.Interface, o ForeignOptions) (*Foreign, error) {
	namespaces := o.Namespaces
	if len(namespaces) == 0 {
		namespaces = []string{metav1.NamespaceAll}
	}

	var secrets []corev1.Secret

	for _, ns := range namespaces {
		list, err := kube.CoreV1().Secrets(ns).List(ctx, metav1.ListOptions{
			LabelSelector: "owner=helm,name=" + ReleaseName,
		})
		if err != nil {
			return nil, fmt.Errorf("cilium: listing Helm release records: %w", err)
		}

		for _, s := range list.Items {
			if s.Namespace != o.IchorNamespace && s.Type == helmReleaseSecretType {
				secrets = append(secrets, s)
			}
		}
	}

	if len(secrets) == 0 {
		return nil, nil //nolint:nilnil // no foreign release is a normal answer
	}

	f := &Foreign{Namespace: secrets[0].Namespace}

	for _, s := range secrets {
		if s.Namespace != f.Namespace {
			return nil, fmt.Errorf("cilium: Helm release records named %s exist in namespaces %s and %s; "+
				"remove the stale one with helm first", ReleaseName, f.Namespace, s.Namespace)
		}

		rel, err := DecodeReleaseRecord(s.Data["release"])
		if err != nil {
			return nil, fmt.Errorf("cilium: record %s/%s: %w", s.Namespace, s.Name, err)
		}

		f.Records = append(f.Records, Record{Secret: s, Release: rel})
	}

	slices.SortFunc(f.Records, func(a, b Record) int { return cmp.Compare(a.Release.Version, b.Release.Version) })

	return f, nil
}

// RequireDeployed refuses a foreign release that is not in the deployed state:
// takeover copies what is live, and a failed or pending release does not say
// what that is.
func (f *Foreign) RequireDeployed() error {
	rel := f.Latest().Release
	if rel.Info == nil || rel.Info.Status != release.StatusDeployed {
		status := release.StatusUnknown
		if rel.Info != nil {
			status = rel.Info.Status
		}

		return fmt.Errorf("cilium: the existing Helm release %s/%s is %s at revision %d, not deployed; "+
			"repair it with the tool that installed it first", f.Namespace, ReleaseName, status, rel.Version)
	}

	return nil
}

// LiveValues are the complete values the foreign release runs with: the chart
// defaults stored in the record merged with the user values (Config). The
// merge is needed because the record's Config holds only what the user set, and
// the owned-key comparison needs the defaults (ipam.mode defaults to
// cluster-pool).
func LiveValues(rel *release.Release) (map[string]any, error) {
	if rel == nil || rel.Chart == nil {
		return nil, errors.New("cilium: release record has no chart")
	}

	vals, err := chartutil.CoalesceValues(rel.Chart, rel.Config)
	if err != nil {
		return nil, fmt.Errorf("cilium: merging release values with chart defaults: %w", err)
	}

	return vals, nil
}

// LiveValuesFromUser is LiveValues for a Cilium with no release record (case C):
// the values the operator gave to `helm template`, merged with the embedded
// chart's defaults. shortcut: assumes the live chart's defaults for the owned
// keys equal the pinned chart's, true across the allowed minors for every owned
// key; the diff gate would show anything else.
func LiveValuesFromUser(user map[string]any) (map[string]any, error) {
	ch, err := freshChart()
	if err != nil {
		return nil, err
	}

	return chartutil.CoalesceValues(ch, user)
}

// UserValues removes the Ichor-owned keys from vals, the user values that
// become spec.cni.cilium.values. vals is not
// modified. Pass a record's Config (not LiveValues), which holds only what the
// user set.
func UserValues(vals map[string]any) map[string]any {
	out := values.Clone(vals)
	for path := range OwnedKeys {
		values.Delete(out, path)
	}

	pruneEmptyMaps(out)

	return out
}

// pruneEmptyMaps removes, recursively, the maps that stripping the owned keys left empty
// ({"cgroup":{"autoMount":{}}}).
func pruneEmptyMaps(m map[string]any) {
	for k, v := range m {
		sub, ok := v.(map[string]any)
		if !ok {
			continue
		}

		pruneEmptyMaps(sub)

		if len(sub) == 0 {
			delete(m, k)
		}
	}
}

// --- version rule ----------------------------------------------------------

// VersionCheck is the outcome of an accepted version.
type VersionCheck struct {
	// Live and Target are the Cilium versions, without a "v".
	Live, Target string
	// Upgrade is true when Live is the minor below Target: adoption is then an
	// upgrade and is announced as one.
	Upgrade bool
}

// CheckVersion applies the version rule (precondition
// 1): the live Cilium minor equals the embedded chart's minor, or is exactly
// one lower. live is a version or an image reference ("v1.17.5",
// "quay.io/cilium/cilium:v1.17.5@sha256:...").
func CheckVersion(live string) (VersionCheck, error) {
	target := "v" + ChartVersion

	lv, err := parseVersion(live)
	if err != nil {
		return VersionCheck{}, err
	}

	res := VersionCheck{Live: strings.TrimPrefix(lv, "v"), Target: ChartVersion}

	lmaj, lmin := majorMinor(lv)
	tmaj, tmin := majorMinor(target)

	switch {
	case lmaj != tmaj, lmin > tmin:
		return res, fmt.Errorf("cilium: live version %s is newer than the version Ichor installs (%s); "+
			"Ichor never downgrades a CNI", res.Live, ChartVersion)
	case lmin == tmin:
		return res, nil
	case lmin == tmin-1:
		res.Upgrade = true

		return res, nil
	default:
		return res, fmt.Errorf("cilium: live version %s is more than one minor behind %s; "+
			"upgrade it to %d.%d with the tool that installed it first", res.Live, ChartVersion, tmaj, tmin-1)
	}
}

// String is the plan wording: "live 1.18.0, chart 1.18.0: the same minor".
func (c VersionCheck) String() string {
	how := "the same minor"
	if c.Upgrade {
		how = "one minor below (adoption is an upgrade)"
	}

	return fmt.Sprintf("live version %s, chart %s: %s", c.Live, c.Target, how)
}

func parseVersion(s string) (string, error) {
	v, _, _ := strings.Cut(s, "@")

	if i := strings.LastIndex(v, ":"); i >= 0 {
		v = v[i+1:]
	}

	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}

	if !semver.IsValid(v) {
		return "", fmt.Errorf("cilium: cannot read a version from %q", s)
	}

	return v, nil
}

func majorMinor(v string) (major, minor int) {
	parts := strings.SplitN(strings.TrimPrefix(semver.MajorMinor(v), "v"), ".", 2)
	major, _ = strconv.Atoi(parts[0])
	minor, _ = strconv.Atoi(parts[1])

	return major, minor
}

// --- owned keys ------------------------------------------------------------

// Level says what a conflict needs.
type Level int

const (
	// Informational: Ichor makes the value match the Talos guide; reported only.
	Informational Level = iota
	// NeedsAccept: Ichor would change a running data plane; the operator must
	// pass --accept-cilium-changes.
	NeedsAccept
	// Blocking: never accepted (IPAM and tunnel protocol cannot change in place).
	Blocking
)

// Conflict is one Ichor-owned key whose live value is not what Ichor sets.
type Conflict struct {
	Key         string
	Live, Want  any
	Level       Level
	Description string
}

func (c Conflict) String() string {
	return fmt.Sprintf("%s: live %v, Ichor sets %v (%s)", c.Key, c.Live, c.Want, c.Description)
}

// OwnedKeyConflicts compares live (the complete live values, LiveValues or
// LiveValuesFromUser) with the keys Ichor owns for kubeProxy and tunnel. It also reports native routing as blocking,
// since Ichor's
// tunnelProtocol would turn it into a tunnelled data plane. The result is
// sorted by key; empty means nothing conflicts.
func OwnedKeyConflicts(live map[string]any, kubeProxy cni.KubeProxyMode, tunnel string) ([]Conflict, error) {
	// Only the owned layer is compared, and the control-plane count sets a default: 0 is enough.
	_, owned, err := Layers(kubeProxy, tunnel, 0, 0)
	if err != nil {
		return nil, err
	}

	var out []Conflict

	for path, why := range OwnedKeys {
		want, set := values.Get(owned, path)
		have, _ := values.Get(live, path)

		// Enabled mode sets no KubePrism address (ADR 0159), so a live one has no Ichor value to conflict with.
		// The takeover still removes it; DiffLive reports that.
		if !set && kubeProxy == cni.KubeProxyEnabled && (path == keyServiceHost || path == keyServicePort) {
			continue
		}

		if sameOwned(path, have, want) || secretsNamespaceOfFeatureOff(live, path) {
			continue
		}

		out = append(out, Conflict{Key: path, Live: have, Want: want, Level: ownedLevel(path), Description: why})
	}

	if mode, _ := values.Get(live, "routingMode"); mode == "native" {
		out = append(out, Conflict{
			Key: "routingMode", Live: mode, Want: "tunnel", Level: Blocking,
			Description: "Ichor sets tunnelProtocol, which needs tunnel routing, and the tunnel protocol cannot change in place",
		})
	}

	slices.SortFunc(out, func(a, b Conflict) int { return strings.Compare(a.Key, b.Key) })

	return out, nil
}

// secretsNamespaceOfFeatureOff reports whether path is the secretsNamespace name of a Cilium feature
// (ingress, Gateway API, Envoy config, BGP) that is off in live: the chart's default for an unused
// feature (BGP's is kube-system) is no conflict. The TLS namespace has no switch.
// shortcut: Envoy config's switch is not read back from a Talos inline manifest (its ConfigMap key is
// also set by ingress and Gateway API), so it counts as off there; its chart default is Cilium's own namespace.
func secretsNamespaceOfFeatureOff(live map[string]any, path string) bool {
	block, _, ok := strings.Cut(path, ".secretsNamespace.")
	if !ok || block == "tls" {
		return false
	}

	on, _ := values.Get(live, block+".enabled")

	return fmt.Sprint(on) != valueTrue
}

func ownedLevel(path string) Level {
	switch path {
	case keyIPAMMode, keyTunnelProtocol:
		return Blocking
	case keyKubeProxyReplacement, "k8sServiceHost", "k8sServicePort":
		return NeedsAccept
	default:
		return Informational
	}
}

func sameOwned(path string, have, want any) bool {
	return fmt.Sprint(normOwned(path, have)) == fmt.Sprint(normOwned(path, want))
}

// normOwned maps the spellings of one value to one: Helm accepts "true",
// "strict" and true for kubeProxyReplacement, an unset tunnel protocol is
// vxlan, a port may be a number or a string, and capability sets are unordered.
func normOwned(path string, v any) any {
	switch path {
	case keyKubeProxyReplacement:
		s := fmt.Sprint(v)

		return s == "true" || s == "strict"
	case keyReadSecretsOnly:
		return v == nil || fmt.Sprint(v) == valueTrue // the chart reads an unset flag as true
	case keySecretsBackend:
		if v == "local" {
			return "" // the alias of true, the same as unset
		}
	case keyTunnelProtocol:
		if v == nil || v == "" {
			return "vxlan"
		}
	}

	if list, ok := v.([]any); ok {
		strs := make([]string, len(list))
		for i, e := range list {
			strs[i] = fmt.Sprint(e)
		}

		slices.Sort(strs)

		return strs
	}

	if list, ok := v.([]string); ok {
		strs := slices.Clone(list)
		slices.Sort(strs)

		return strs
	}

	if v == nil {
		return ""
	}

	return v
}

// AcceptConflicts is the gate over OwnedKeyConflicts: a blocking conflict is
// always an error, a NeedsAccept one is an error unless acceptChanges
// (--accept-cilium-changes), and informational ones never are.
func AcceptConflicts(cs []Conflict, acceptChanges bool) error {
	var errs []error

	for _, c := range cs {
		switch {
		case c.Level == Blocking:
			errs = append(errs, fmt.Errorf("%s: cannot be taken over, it cannot change on a running data plane", c))
		case c.Level == NeedsAccept && !acceptChanges:
			errs = append(errs, fmt.Errorf("%s: needs --accept-cilium-changes", c))
		}
	}

	return errors.Join(errs...)
}

// --- diff gate -------------------------------------------------------------

// ObjectRef names a Kubernetes object.
type ObjectRef struct {
	APIVersion, Kind, Namespace, Name string
}

func (r ObjectRef) String() string {
	if r.Namespace == "" {
		return r.Kind + "/" + r.Name
	}

	return r.Kind + " " + r.Namespace + "/" + r.Name
}

// LiveGetter reads a live object as an unstructured map. A NotFound error means
// the object does not exist.
type LiveGetter func(ctx context.Context, ref ObjectRef) (map[string]any, error)

// DiffKind says how a path differs.
type DiffKind string

const (
	// DiffChanged: both sides have the path with different values; Helm would
	// overwrite the live one.
	DiffChanged DiffKind = "Changed"
	// DiffRenderedOnly: the chart sets a path the live object lacks; Helm would add it.
	DiffRenderedOnly DiffKind = "RenderedOnly"
	// DiffLiveOnly: the live object has a path the chart does not render; the
	// takeover keeps it (the three-way merge has no record of it) and a later
	// Upgrade may drop it.
	DiffLiveOnly DiffKind = "LiveOnly"
	// DiffObjectMissing: the chart renders an object that does not exist live.
	DiffObjectMissing DiffKind = "ObjectMissing"
)

// Difference is one rendered-versus-live difference.
type Difference struct {
	Object   ObjectRef
	Path     string
	Kind     DiffKind
	Rendered any
	Live     any
}

func (d Difference) String() string {
	if d.Kind == DiffObjectMissing {
		return fmt.Sprintf("%s: %s", d.Object, d.Kind)
	}

	return fmt.Sprintf("%s %s: %s (chart %s, live %s)", d.Object, d.Path, d.Kind,
		shortValue(d.Rendered), shortValue(d.Live))
}

// maxPrintedValue bounds one value in the plan output (a ConfigMap entry can be several KB).
const maxPrintedValue = 200

// shortValue prints v, cut to maxPrintedValue bytes with its full length.
func shortValue(v any) string {
	s := fmt.Sprintf("%v", v)
	if len(s) <= maxPrintedValue {
		return s
	}

	return fmt.Sprintf("%s… (%d bytes)", s[:maxPrintedValue], len(s))
}

// DiffLive is the diff gate (precondition 4): it renders
// the pinned chart with vals, the effective values, and compares every rendered
// object with the live one fetched through get. Volatile and server-owned
// fields are removed from both sides, as are Helm's ownership markers (takeover
// rewrites them) and Secret data (never compared, never returned). An empty
// result lets the takeover proceed. False positives (server-defaulted fields)
// are safe; false negatives are not.
func DiffLive(ctx context.Context, vals map[string]any, get LiveGetter) ([]Difference, error) {
	rendered, err := renderChart(vals)
	if err != nil {
		return nil, err
	}

	objs, err := splitObjects(rendered)
	if err != nil {
		return nil, err
	}

	var out []Difference

	for _, want := range objs {
		ref := refOf(want)

		live, err := get(ctx, ref)

		switch {
		case apierrors.IsNotFound(err), err == nil && live == nil:
			out = append(out, Difference{Object: ref, Kind: DiffObjectMissing})

			continue
		case err != nil:
			return nil, fmt.Errorf("cilium: reading live %s: %w", ref, err)
		}

		if _, set := values.Get(want, "metadata.namespace"); !set && ref.Namespace != "" {
			values.Set(want, "metadata.namespace", ref.Namespace)
		}

		out = append(out, diffMaps(ref, "", normalizeObject(want), normalizeObject(live))...)
	}

	return out, nil
}

func splitObjects(manifest []byte) ([]map[string]any, error) {
	dec := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(manifest), 4096)

	var objs []map[string]any

	for {
		var o map[string]any

		if err := dec.Decode(&o); err != nil {
			if errors.Is(err, io.EOF) {
				return objs, nil
			}

			return nil, fmt.Errorf("cilium: reading rendered chart: %w", err)
		}

		if len(o) > 0 {
			objs = append(objs, o)
		}
	}
}

// clusterScoped are the kinds the Cilium chart renders without a namespace.
// shortcut: a fixed list instead of a RESTMapper keeps the library free of a
// cluster connection; an unlisted kind would be looked up in kube-system and
// reported ObjectMissing, a safe failure.
var clusterScoped = map[string]bool{
	"ClusterRole": true, "ClusterRoleBinding": true, "CustomResourceDefinition": true, "Namespace": true,
	"APIService": true, "PriorityClass": true, "IngressClass": true, "StorageClass": true,
	"ValidatingWebhookConfiguration": true, "MutatingWebhookConfiguration": true,
}

func refOf(o map[string]any) ObjectRef {
	ref := ObjectRef{
		APIVersion: str(o["apiVersion"]),
		Kind:       str(o["kind"]),
	}

	meta, _ := o["metadata"].(map[string]any)
	ref.Name = str(meta["name"])

	if clusterScoped[ref.Kind] {
		return ref
	}

	if ref.Namespace = str(meta["namespace"]); ref.Namespace == "" {
		ref.Namespace = ReleaseNamespace
	}

	return ref
}

func str(v any) string {
	s, _ := v.(string)

	return s
}

// volatileMeta are metadata fields the API server owns.
var volatileMeta = []string{
	"uid", "resourceVersion", "generation", "creationTimestamp", "managedFields", "selfLink",
	"deletionTimestamp", "deletionGracePeriodSeconds",
}

// volatileAnnotations are written by tools and controllers, or are Helm's
// ownership markers, which the takeover rewrites.
var volatileAnnotations = []string{
	"kubectl.kubernetes.io/last-applied-configuration", "deployment.kubernetes.io/revision",
	annotationReleaseName, annotationReleaseNamespace,
	// Talos writes this onto the inline-manifest objects it applies; it is its bookkeeping.
	"config.k8s.io/owning-inventory",
}

// normalizeObject returns a copy of o without the fields that are never a user
// difference. Secret payloads are dropped entirely.
func normalizeObject(o map[string]any) map[string]any {
	o = values.Clone(o)

	delete(o, "status")

	if str(o["kind"]) == "Secret" {
		delete(o, "data")
		delete(o, "stringData")
	}

	meta, _ := o["metadata"].(map[string]any)
	for _, k := range volatileMeta {
		delete(meta, k)
	}

	pruneMap(meta, "annotations", volatileAnnotations)
	pruneMap(meta, "labels", []string{labelManagedBy})

	return o
}

func pruneMap(meta map[string]any, field string, keys []string) {
	m, _ := meta[field].(map[string]any)
	for _, k := range keys {
		delete(m, k)
	}

	if len(m) == 0 {
		delete(meta, field)
	}
}

// serverDefaulted are fields the API server fills in when a manifest leaves
// them out; as live-only paths they are noise, not a difference.
// shortcut: a short list of the defaults the Cilium chart's workloads hit; a
// missed default is a reported false positive, which --accept-cilium-diff covers.
var serverDefaulted = map[string]bool{
	"terminationMessagePath": true, "terminationMessagePolicy": true, "dnsPolicy": true,
	"restartPolicy": true, "schedulerName": true, "revisionHistoryLimit": true,
	"progressDeadlineSeconds": true, "defaultMode": true, "protocol": true,
	"serviceAccount": true, "terminationGracePeriodSeconds": true,
	"successThreshold": true, "timeoutSeconds": true, "periodSeconds": true, "failureThreshold": true,
	"apiVersion": true, "sessionAffinity": true, "clusterIP": true, "clusterIPs": true,
	"internalTrafficPolicy": true, "ipFamilies": true, "ipFamilyPolicy": true,
	"concurrencyPolicy": true, "successfulJobsHistoryLimit": true, "failedJobsHistoryLimit": true,
	"suspend": true, "imagePullPolicy": true, "strategy": true, "updateStrategy": true,
}

const kindDaemonSet = "DaemonSet"

// serverDefaultValue reports a live-only path whose value is exactly the default the API server
// fills in when the manifest omits it: the rolling-update surge and unavailable limits the chart
// leaves half unset, and a Service's type. Matching the value keeps a real change visible.
func serverDefaultValue(ref ObjectRef, path string, live any) bool {
	got := fmt.Sprint(live)

	switch ref.Kind {
	case kindDaemonSet:
		return (path == "spec.updateStrategy.rollingUpdate.maxSurge" && got == "0") ||
			(path == "spec.updateStrategy.rollingUpdate.maxUnavailable" && got == "1")
	case "Deployment":
		return (path == "spec.strategy.rollingUpdate.maxSurge" || path == "spec.strategy.rollingUpdate.maxUnavailable") &&
			got == "25%"
	case "Service":
		return path == "spec.type" && got == "ClusterIP"
	}

	return false
}

func diffMaps(ref ObjectRef, path string, want, have map[string]any) []Difference {
	keys := slices.Collect(maps.Keys(want))

	for k := range have {
		if _, ok := want[k]; !ok {
			keys = append(keys, k)
		}
	}

	slices.Sort(keys)

	var out []Difference

	for _, k := range keys {
		p := joinPath(path, k)
		w, inWant := want[k]
		h, inHave := have[k]

		switch {
		case isEmpty(w) && isEmpty(h):
		case !inHave || isEmpty(h):
			out = append(out, Difference{Object: ref, Path: p, Kind: DiffRenderedOnly, Rendered: w})
		case !inWant || isEmpty(w):
			if hm, isMap := h.(map[string]any); isMap {
				// Name the live-only leaves, not the map that holds them.
				out = append(out, diffMaps(ref, p, nil, hm)...)
			} else if !serverDefaulted[k] && !serverDefaultValue(ref, p, h) {
				out = append(out, Difference{Object: ref, Path: p, Kind: DiffLiveOnly, Live: h})
			}
		default:
			out = append(out, diffValues(ref, p, w, h)...)
		}
	}

	return out
}

func joinPath(path, k string) string {
	if path == "" {
		return k
	}

	return path + "." + k
}

func isEmpty(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case map[string]any:
		return len(t) == 0
	case []any:
		return len(t) == 0
	default:
		return false
	}
}

func diffValues(ref ObjectRef, path string, want, have any) []Difference {
	switch w := want.(type) {
	case map[string]any:
		if h, ok := have.(map[string]any); ok {
			return diffMaps(ref, path, w, h)
		}
	case []any:
		if h, ok := have.([]any); ok {
			return diffLists(ref, path, w, h)
		}
	default:
		if sameScalar(want, have) {
			return nil
		}
	}

	return []Difference{{Object: ref, Path: path, Kind: DiffChanged, Rendered: want, Live: have}}
}

// sameScalar is equality that tells a string from a number from a bool, but not
// an int64 from a float64 (YAML and the API decode numbers differently).
func sameScalar(a, b any) bool {
	return scalarClass(a) == scalarClass(b) && fmt.Sprint(a) == fmt.Sprint(b)
}

func scalarClass(v any) string {
	switch v.(type) {
	case string:
		return "string"
	case bool:
		return "bool"
	case map[string]any, []any:
		return "composite"
	case nil:
		return "nil"
	default:
		return "number"
	}
}

func diffLists(ref ObjectRef, path string, want, have []any) []Difference {
	switch {
	case allNamed(want) && allNamed(have):
		return diffNamed(ref, path, want, have)
	case allScalar(want) && allScalar(have):
		return diffScalars(ref, path, want, have)
	default:
		var out []Difference

		for i := range max(len(want), len(have)) {
			p := fmt.Sprintf("%s[%d]", path, i)

			switch {
			case i >= len(have):
				out = append(out, Difference{Object: ref, Path: p, Kind: DiffRenderedOnly, Rendered: want[i]})
			case i >= len(want):
				out = append(out, Difference{Object: ref, Path: p, Kind: DiffLiveOnly, Live: have[i]})
			default:
				out = append(out, diffValues(ref, p, want[i], have[i])...)
			}
		}

		return out
	}
}

func allNamed(l []any) bool {
	for _, e := range l {
		m, ok := e.(map[string]any)
		if !ok || str(m["name"]) == "" {
			return false
		}
	}

	return true
}

func allScalar(l []any) bool {
	for _, e := range l {
		if scalarClass(e) == "composite" {
			return false
		}
	}

	return true
}

// diffNamed pairs list elements (containers, env, volumes, ports) by name.
func diffNamed(ref ObjectRef, path string, want, have []any) []Difference {
	index := func(l []any) (map[string]map[string]any, []string) {
		m := map[string]map[string]any{}

		var names []string

		for _, e := range l {
			em, _ := e.(map[string]any)
			n := str(em["name"])

			if _, dup := m[n]; !dup {
				names = append(names, n)
			}

			m[n] = em
		}

		return m, names
	}

	w, wnames := index(want)
	h, hnames := index(have)

	var out []Difference

	for _, n := range wnames {
		p := fmt.Sprintf("%s[%s]", path, n)

		if he, ok := h[n]; ok {
			out = append(out, diffMaps(ref, p, w[n], he)...)
		} else {
			out = append(out, Difference{Object: ref, Path: p, Kind: DiffRenderedOnly, Rendered: w[n]})
		}
	}

	for _, n := range hnames {
		if _, ok := w[n]; !ok {
			out = append(out, Difference{Object: ref, Path: fmt.Sprintf("%s[%s]", path, n), Kind: DiffLiveOnly, Live: h[n]})
		}
	}

	return out
}

// diffScalars compares lists such as container args as multisets, so one extra
// argument is one difference and not a shifted list.
func diffScalars(ref ObjectRef, path string, want, have []any) []Difference {
	remaining := slices.Clone(have)

	var out []Difference

	for _, w := range want {
		i := slices.IndexFunc(remaining, func(h any) bool { return sameScalar(w, h) })
		if i < 0 {
			out = append(out, Difference{Object: ref, Path: path, Kind: DiffRenderedOnly, Rendered: w})

			continue
		}

		remaining = slices.Delete(remaining, i, i+1)
	}

	for _, h := range remaining {
		out = append(out, Difference{Object: ref, Path: path, Kind: DiffLiveOnly, Live: h})
	}

	// The same elements in another order is a difference too (arg order can matter).
	if len(out) == 0 && !slices.EqualFunc(want, have, sameScalar) {
		out = append(out, Difference{Object: ref, Path: path, Kind: DiffChanged, Rendered: want, Live: have})
	}

	return out
}

// --- chart crds/ -----------------------------------------------------------

func crdNames(ch *chart.Chart) []string {
	crds := ch.CRDObjects()
	names := make([]string, 0, len(crds))

	for _, c := range crds {
		names = append(names, c.Name)
	}

	return names
}

// CheckChartCRDs is an error when the pinned chart ships a crds/ directory (and subcharts). The
// takeover design relies on there being none: Helm's Install applies crds/ before the
// release, which would hand the CRDs of a running Cilium to a release that the Cilium operator
// manages itself. A chart bump that adds one needs a decision, not a silent CRD install.
func CheckChartCRDs() error {
	ch, err := loadChart()
	if err != nil {
		return err
	}

	if names := crdNames(ch); len(names) > 0 {
		return fmt.Errorf("cilium: chart %s ships crds/ (%s); taking over a running Cilium would install them "+
			"through Helm", ChartVersion, strings.Join(names, ", "))
	}

	return nil
}

// --- foreign record backup and removal ------------------------------------

// RetireOptions control RetireForeignRecord.
type RetireOptions struct {
	// IchorNamespace is where the backup goes and where Ichor's own records live.
	IchorNamespace string
	// Namespaces to look for the foreign record in; empty searches every namespace.
	Namespaces []string
	// RemoveForeignReleaseRecord is the operator's consent to delete the foreign
	// release record after it is backed up (open question 1). False
	// keeps the record, and with it a second Helm history for the same objects;
	// nothing is backed up or changed.
	RemoveForeignReleaseRecord bool
}

// RetireResult says what RetireForeignRecord did.
type RetireResult struct {
	// Kept is true when the option was off or there was no foreign record.
	Kept bool
	// Backup is the backup Secret's name, when one was written.
	Backup string
	// Removed lists the deleted record Secrets as namespace/name.
	Removed []string
}

// RetireForeignRecord removes the Helm release records of the foreign "cilium"
// release, after copying them into the Secret PriorReleaseBackupName in
// IchorNamespace. It deletes the record Secrets only, never the release's
// objects: it does not call helm uninstall, which would delete the CNI. Each
// record is deleted only after the backup is read back and found identical,
// and only if it still is the Helm "cilium" record with the same UID. Safe to
// run again: a second run finds no record and does nothing, and a backup from
// an earlier partial run is extended, never overwritten.
//
// Call it after Apply wrote Ichor's record and the rollout is Ready.
func RetireForeignRecord(ctx context.Context, kube kubernetes.Interface, o RetireOptions) (RetireResult, error) {
	if !o.RemoveForeignReleaseRecord {
		return RetireResult{Kept: true}, nil
	}

	if o.IchorNamespace == "" {
		return RetireResult{}, errors.New("cilium: RetireForeignRecord needs the Ichor namespace")
	}

	f, err := ForeignRecords(ctx, kube, ForeignOptions{IchorNamespace: o.IchorNamespace, Namespaces: o.Namespaces})
	if err != nil {
		return RetireResult{}, err
	}

	if f == nil {
		return RetireResult{Kept: true}, nil
	}

	if err := writeBackup(ctx, kube, o.IchorNamespace, f); err != nil {
		return RetireResult{}, err
	}

	res := RetireResult{Backup: PriorReleaseBackupName}

	for _, r := range f.Records {
		if err := deleteRecord(ctx, kube, r.Secret); err != nil {
			return res, err
		}

		res.Removed = append(res.Removed, r.Secret.Namespace+"/"+r.Secret.Name)
	}

	return res, nil
}

type recordMeta struct {
	Namespace   string            `json:"namespace"`
	Name        string            `json:"name"`
	Type        corev1.SecretType `json:"type"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

func writeBackup(ctx context.Context, kube kubernetes.Interface, ns string, f *Foreign) error {
	data := map[string][]byte{}
	total := 0

	for _, r := range f.Records {
		meta, err := json.Marshal(recordMeta{
			Namespace: r.Secret.Namespace, Name: r.Secret.Name, Type: r.Secret.Type,
			Labels: r.Secret.Labels, Annotations: r.Secret.Annotations,
		})
		if err != nil {
			return fmt.Errorf("cilium: encoding record metadata: %w", err)
		}

		key := r.Secret.Namespace + "." + r.Secret.Name
		data[key+backupMetaSuffix] = meta
		data[key+backupReleaseSuffix] = r.Secret.Data["release"]
		total += len(meta) + len(r.Secret.Data["release"])
	}

	if total > maxBackupBytes {
		return fmt.Errorf("cilium: the %d foreign release records are %d bytes, too big for one backup Secret; "+
			"nothing was deleted, prune the old revisions with helm first", len(f.Records), total)
	}

	secrets := kube.CoreV1().Secrets(ns)

	cur, err := secrets.Get(ctx, PriorReleaseBackupName, metav1.GetOptions{})

	switch {
	case apierrors.IsNotFound(err):
		_, err = secrets.Create(ctx, &corev1.Secret{
			Name: PriorReleaseBackupName, Namespace: ns,
			Data: data,
		}, metav1.CreateOptions{})
	case err == nil:
		if cur.Data == nil {
			cur.Data = map[string][]byte{}
		}

		maps.Copy(cur.Data, data)

		_, err = secrets.Update(ctx, cur, metav1.UpdateOptions{})
	}

	if err != nil {
		return fmt.Errorf("cilium: writing backup Secret %s/%s: %w", ns, PriorReleaseBackupName, err)
	}

	// Read back what is stored; deleting the originals trusts only this.
	stored, err := secrets.Get(ctx, PriorReleaseBackupName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("cilium: reading back backup Secret: %w", err)
	}

	for k, v := range data {
		if !bytes.Equal(stored.Data[k], v) {
			return fmt.Errorf("cilium: backup Secret %s/%s does not hold %s as written; nothing was deleted",
				ns, PriorReleaseBackupName, k)
		}
	}

	return nil
}

func deleteRecord(ctx context.Context, kube kubernetes.Interface, rec corev1.Secret) error {
	secrets := kube.CoreV1().Secrets(rec.Namespace)

	cur, err := secrets.Get(ctx, rec.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("cilium: re-reading record %s/%s: %w", rec.Namespace, rec.Name, err)
	}

	if cur.UID != rec.UID || cur.Type != helmReleaseSecretType ||
		cur.Labels["owner"] != "helm" || cur.Labels["name"] != ReleaseName {
		return fmt.Errorf("cilium: %s/%s is no longer the Helm record that was backed up; not deleting it",
			rec.Namespace, rec.Name)
	}

	err = secrets.Delete(ctx, rec.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &cur.UID}})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("cilium: deleting record %s/%s: %w", rec.Namespace, rec.Name, err)
	}

	return nil
}
