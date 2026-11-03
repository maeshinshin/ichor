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

package controller

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/constants"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// DefaultTalosEgressPolicyName is the CiliumNetworkPolicy the controller maintains. The installer's static policies
// have no `world` egress to Talos nodes or
// S3; this object is what adds exactly the addresses Ichor's CRs name.
const DefaultTalosEgressPolicyName = "ichor-controller-talos-egress"

// talosEgressPorts are the TCP ports the dynamic policy opens to a machine or cluster endpoint: apid,
// the API server (cluster endpoint, /readyz) and etcd's peer port (MachineReclaim's member health
// probe, pkg/reclaim). Derived from Talos's constants where it has them.
// TalosEgressPorts returns talosEgressPorts for the chart's policy test (dist/chart).
func TalosEgressPorts() []int { return slices.Clone(talosEgressPorts) }

var talosEgressPorts = []int{constants.ApidPort, constants.DefaultControlPlanePort, constants.EtcdPeerPort}

var ciliumPolicyGVK = schema.GroupVersionKind{Group: "cilium.io", Version: "v2", Kind: "CiliumNetworkPolicy"}

const (
	talosEgressRecheck = time.Minute
	syncAttempts       = 3
	keyPorts           = "ports"
	keyNamespace       = "namespace"
	keySpec            = "spec"
	keyToPorts         = "toPorts"
	keyEgress          = "egress"
	keyMatchName       = "matchName"
	valueControllerMgr = "controller-manager"
	keyCIDR            = "cidr"
)

// EgressSyncer is what the TalosCluster reconciler calls before it probes a discovery range.
type EgressSyncer interface {
	// Sync makes the policy match the current CRs and reports whether it wrote it.
	Sync(ctx context.Context) (changed bool, err error)
}

// TalosEgress maintains the CiliumNetworkPolicy of the controller's and ichor-ui's egress to
// the machines, clusters and S3 stores Ichor's own objects name. It writes only a Kubernetes
// object in its own namespace, and never a spec of Ichor's.
type TalosEgress struct {
	// Client lists the Ichor kinds and reads and writes the policy (unstructured, uncached).
	Client client.Client
	// Namespace is the controller's namespace; the policy lives there.
	Namespace string
	// Name is the policy's name; empty means DefaultTalosEgressPolicyName.
	Name string
	// Disabled switches the whole thing off (--no-cilium-policies).
	Disabled bool
	// Settle is how long Reconcile waits after writing, so the agents have the rule before the
	// first dial. Zero in tests.
	Settle time.Duration

	mu        sync.Mutex
	lastWrite time.Time
}

func (e *TalosEgress) name() string {
	if e.Name == "" {
		return DefaultTalosEgressPolicyName
	}

	return e.Name
}

// egressTargets is everything the CRs ask the controller to reach.
type egressTargets struct {
	cidrs []string // machine, discovery and reclaim addresses: "10.0.0.1/32", "10.1.0.0/24"
	names []string // host names of machines and cluster endpoints
	s3    []s3Target
}

type s3Target struct {
	host string
	port int
}

var fqdnRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9_-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9_-]*[a-z0-9])?)*$`)

type targetSet struct {
	cidrs, names map[string]bool
}

// host adds a machine or cluster endpoint ("10.0.0.1", "10.0.0.1:50000", "k8s.example.com:6443").
func (s *targetSet) host(h string) {
	if h == "" {
		return
	}

	if hh, _, err := net.SplitHostPort(h); err == nil {
		h = hh
	}

	h = strings.TrimSuffix(strings.Trim(h, "[]"), ".")

	if a, err := netip.ParseAddr(h); err == nil {
		a = a.Unmap()
		s.cidrs[netip.PrefixFrom(a, a.BitLen()).String()] = true

		return
	}

	if h = strings.ToLower(h); fqdnRE.MatchString(h) {
		s.names[h] = true
	}
}

func (s *targetSet) cidr(c string) {
	if p, err := netip.ParsePrefix(c); err == nil {
		s.cidrs[p.Masked().String()] = true
	}
}

// collectTargets lists the Ichor kinds in every namespace. It lists every TalosMachine to read its
// endpoint and counts nothing (countplanes_guard_test.go allows it for that reason).
func collectTargets(ctx context.Context, c client.Reader) (egressTargets, error) {
	set := &targetSet{cidrs: map[string]bool{}, names: map[string]bool{}}

	var clusters ichorv1alpha1.TalosClusterList
	if err := c.List(ctx, &clusters); err != nil {
		return egressTargets{}, fmt.Errorf("listing TalosClusters: %w", err)
	}

	for i := range clusters.Items {
		cl := &clusters.Items[i]
		// The control-plane endpoint: the admin kubeconfig the Talos API issues points at it.
		set.host(cl.Spec.Endpoint.Address)

		if d := cl.Spec.Discovery; d != nil {
			for _, a := range d.Addresses {
				set.host(a)
			}

			for _, cidr := range d.CIDRs {
				set.cidr(cidr)
			}
		}
	}

	var machines ichorv1alpha1.TalosMachineList
	if err := c.List(ctx, &machines); err != nil {
		return egressTargets{}, fmt.Errorf("listing TalosMachines: %w", err)
	}

	for i := range machines.Items {
		set.host(machines.Items[i].Spec.Endpoint)
		set.host(machines.Items[i].Status.ObservedEndpoint)
	}

	var reclaims ichorv1alpha1.MachineReclaimList
	if err := c.List(ctx, &reclaims); err != nil {
		return egressTargets{}, fmt.Errorf("listing MachineReclaims: %w", err)
	}

	for i := range reclaims.Items {
		set.host(reclaims.Items[i].Spec.Address)
	}

	t := egressTargets{cidrs: keys(set.cidrs), names: keys(set.names)}

	var backups ichorv1alpha1.TalosEtcdBackupList
	if err := c.List(ctx, &backups); err != nil {
		return egressTargets{}, fmt.Errorf("listing TalosEtcdBackups: %w", err)
	}

	seen := map[s3Target]bool{}

	for i := range backups.Items {
		if st, ok := parseS3Endpoint(backups.Items[i].Spec.Destination.S3.Endpoint); ok && !seen[st] {
			seen[st] = true
			t.s3 = append(t.s3, st)
		}
	}

	slices.SortFunc(t.s3, func(a, b s3Target) int {
		return cmp.Or(cmp.Compare(a.host, b.host), cmp.Compare(a.port, b.port))
	})

	return t, nil
}

func keys(m map[string]bool) []string { return slices.Sorted(maps.Keys(m)) }

// parseS3Endpoint is the host and TCP port of "http(s)://host[:port]" (443 and 80 by scheme).
func parseS3Endpoint(endpoint string) (s3Target, bool) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Hostname() == "" {
		return s3Target{}, false
	}

	port := 443
	if u.Scheme == "http" {
		port = 80
	}

	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return s3Target{}, false
		}

		port = n
	}

	h := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if _, err := netip.ParseAddr(h); err != nil && !fqdnRE.MatchString(h) {
		return s3Target{}, false
	}

	return s3Target{host: h, port: port}, true
}

func portList(ports ...int) []any {
	out := make([]any, 0, len(ports))
	for _, p := range ports {
		out = append(out, map[string]any{"port": strconv.Itoa(p), "protocol": "TCP"})
	}

	return out
}

// clusterLocalService returns (name, namespace) for "svc.ns.svc[.cluster.local]" and "svc.ns.svc".
func clusterLocalService(host string) (string, string, bool) {
	host = strings.TrimSuffix(host, ".cluster.local")

	parts := strings.Split(host, ".")
	if len(parts) == 3 && parts[2] == "svc" {
		return parts[0], parts[1], true
	}

	return "", "", false
}

// buildEgress is the egress section for the targets. Pure, so the tests read it directly.
func buildEgress(t egressTargets) []any {
	var rules []any

	if len(t.cidrs) > 0 {
		set := make([]any, 0, len(t.cidrs))
		for _, c := range t.cidrs {
			set = append(set, map[string]any{keyCIDR: c})
		}

		rules = append(rules, map[string]any{
			"toCIDRSet": set,
			keyToPorts:  []any{map[string]any{keyPorts: portList(talosEgressPorts...)}},
		})
	}

	if len(t.names) > 0 {
		fq := make([]any, 0, len(t.names))
		for _, n := range t.names {
			fq = append(fq, map[string]any{keyMatchName: n})
		}

		rules = append(rules, map[string]any{
			"toFQDNs":  fq,
			keyToPorts: []any{map[string]any{keyPorts: portList(talosEgressPorts...)}},
		})
	}

	for _, s := range t.s3 {
		ports := []any{map[string]any{keyPorts: portList(s.port)}}

		addr, ipErr := netip.ParseAddr(s.host)

		switch name, ns, local := clusterLocalService(s.host); {
		case local:
			rules = append(rules, map[string]any{
				"toServices": []any{map[string]any{"k8sService": map[string]any{"serviceName": name, keyNamespace: ns}}},
				keyToPorts:   ports,
			})
		case ipErr == nil:
			rules = append(rules, map[string]any{
				"toCIDRSet": []any{map[string]any{keyCIDR: netip.PrefixFrom(addr, addr.BitLen()).String()}},
				keyToPorts:  ports,
			})
		default:
			rules = append(rules, map[string]any{
				"toFQDNs":  []any{map[string]any{keyMatchName: s.host}},
				keyToPorts: ports,
			})
		}
	}

	if len(rules) == 0 {
		// A rule with an egress section keeps the pods in deny mode; the empty rule allows nothing.
		rules = append(rules, map[string]any{})
	}

	return rules
}

// staticPolicy finds the installed controller policy (any name) and returns the app name label
// its pods carry; "" when Ichor's policies are not installed (Cilium absent, opt-out, or another CNI).
func (e *TalosEgress) staticPolicy(ctx context.Context) (string, error) {
	return controllerPolicyApp(ctx, e.Client, e.Namespace, e.name())
}

// controllerPolicyApp is the app.kubernetes.io/name label the installed controller policy carries
// (any policy in ns except skip); "" when Ichor's policies are not installed.
func controllerPolicyApp(ctx context.Context, c client.Client, ns, skip string) (string, error) {
	var list unstructured.UnstructuredList

	list.SetGroupVersionKind(schema.GroupVersionKind{Group: ciliumPolicyGVK.Group, Version: ciliumPolicyGVK.Version, Kind: "CiliumNetworkPolicyList"})

	if err := c.List(ctx, &list, client.InNamespace(ns)); err != nil {
		if meta.IsNoMatchError(err) || apierrors.IsNotFound(err) {
			return "", nil
		}

		return "", fmt.Errorf("listing CiliumNetworkPolicies: %w", err)
	}

	for i := range list.Items {
		o := &list.Items[i]
		if o.GetName() == skip {
			continue
		}

		labels, _, _ := unstructured.NestedStringMap(o.Object, keySpec, "endpointSelector", "matchLabels")
		if labels["control-plane"] == valueControllerMgr && labels["app.kubernetes.io/name"] != "" {
			return labels["app.kubernetes.io/name"], nil
		}
	}

	return "", nil
}

// writePolicy creates or updates the CiliumNetworkPolicy name in ns with spec, labelled for the
// controller's app. It reports whether it wrote.
func writePolicy(ctx context.Context, c client.Client, ns, name, app string, spec map[string]any) (bool, error) {
	cur := &unstructured.Unstructured{}
	cur.SetGroupVersionKind(ciliumPolicyGVK)

	err := c.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, cur)

	switch {
	case apierrors.IsNotFound(err):
		cur.Object = map[string]any{keySpec: spec}
		cur.SetGroupVersionKind(ciliumPolicyGVK)
		cur.SetNamespace(ns)
		cur.SetName(name)
		cur.SetLabels(map[string]string{nameLabel: app, "app.kubernetes.io/managed-by": "ichor-controller"})

		if err := c.Create(ctx, cur); err != nil {
			return false, fmt.Errorf("creating %s: %w", name, err)
		}

		return true, nil
	case err != nil:
		return false, fmt.Errorf("reading %s: %w", name, err)
	}

	have, _, _ := unstructured.NestedMap(cur.Object, keySpec)
	if equality.Semantic.DeepEqual(have, spec) {
		return false, nil
	}

	if err := unstructured.SetNestedField(cur.Object, spec, keySpec); err != nil {
		return false, err
	}

	if err := c.Update(ctx, cur); err != nil {
		return false, fmt.Errorf("updating %s: %w", name, err)
	}

	return true, nil
}

// removePolicy deletes the CiliumNetworkPolicy name in ns; one that is already gone is not an error.
func removePolicy(ctx context.Context, c client.Client, ns, name string) error {
	cur := &unstructured.Unstructured{}
	cur.SetGroupVersionKind(ciliumPolicyGVK)
	cur.SetNamespace(ns)
	cur.SetName(name)

	err := c.Delete(ctx, cur)

	switch {
	case apierrors.IsNotFound(err), meta.IsNoMatchError(err):
		return nil
	case err != nil:
		return fmt.Errorf("deleting %s: %w", name, err)
	}

	return nil
}

// Sync is the whole job: gate, collect, build, create or update. It returns changed when it wrote.
// Two reconcilers call it (the TalosCluster one and this controller's own), so a Conflict or
// AlreadyExists from the other one's write is retried against the fresh object.
func (e *TalosEgress) Sync(ctx context.Context) (bool, error) {
	var (
		changed bool
		err     error
	)

	for range syncAttempts {
		if changed, err = e.sync(ctx); !apierrors.IsConflict(err) && !apierrors.IsAlreadyExists(err) {
			break
		}
	}

	if changed {
		e.mu.Lock()
		e.lastWrite = time.Now()
		e.mu.Unlock()
	}

	return changed, err
}

func (e *TalosEgress) sync(ctx context.Context) (bool, error) {
	if e.Disabled || e.Namespace == "" {
		return false, nil
	}

	app, err := e.staticPolicy(ctx)
	if err != nil || app == "" {
		// Without the default-deny set, a lone egress rule would itself cut the pods off. Skip.
		return false, err
	}

	t, err := collectTargets(ctx, e.Client)
	if err != nil {
		return false, err
	}

	spec := map[string]any{
		"description": "Egress of the controller and ichor-ui to the machines, cluster endpoints and S3 stores " +
			"Ichor's own objects name. Written by the controller; edits are overwritten.",
		"endpointSelector": map[string]any{
			"matchLabels": map[string]any{"app.kubernetes.io/name": app},
			"matchExpressions": []any{map[string]any{
				"key": "control-plane", "operator": "In", "values": []any{valueControllerMgr, "ui-backend"},
			}},
		},
		keyEgress: buildEgress(t),
	}

	return writePolicy(ctx, e.Client, e.Namespace, e.name(), app, spec)
}

// +kubebuilder:rbac:groups=cilium.io,namespace=ichor-system,resources=ciliumnetworkpolicies,verbs=get;list;create;update;delete
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosclusters;talosmachines;talosetcdbackups;machinereclaims,verbs=get;list;watch

// Reconcile keeps the policy current. Every watched change maps to the same key.
func (e *TalosEgress) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	changed, err := e.Sync(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}

	if changed {
		logf.FromContext(ctx).Info("Updated the controller's egress policy", "policy", e.name())
	}

	// A periodic pass picks up Cilium's CRD appearing and a manual edit of the policy.
	return ctrl.Result{RequeueAfter: talosEgressRecheck}, nil
}

// SettleAfter waits until Settle has passed since the last write by either reconciler, so the
// agents have installed the rule before a dial. Counting a write the other reconciler made is the
// point: otherwise the TalosCluster pass sees no change, dials at once, and a failed probe waits
// a full requeue.
func (e *TalosEgress) SettleAfter(ctx context.Context) {
	e.mu.Lock()
	wait := time.Until(e.lastWrite.Add(e.Settle))
	e.mu.Unlock()

	if wait <= 0 {
		return
	}

	select {
	case <-ctx.Done():
	case <-time.After(wait):
	}
}

// SetupWithManager watches the kinds the policy is built from.
func (e *TalosEgress) SetupWithManager(mgr ctrl.Manager) error {
	var key reconcile.Request

	key.Namespace, key.Name = e.Namespace, e.name()
	one := handler.EnqueueRequestsFromMapFunc(func(context.Context, client.Object) []reconcile.Request {
		return []reconcile.Request{key}
	})

	return ctrl.NewControllerManagedBy(mgr).
		Named("talosegress").
		Watches(&ichorv1alpha1.TalosCluster{}, one).
		Watches(&ichorv1alpha1.TalosMachine{}, one).
		Watches(&ichorv1alpha1.TalosEtcdBackup{}, one).
		Watches(&ichorv1alpha1.MachineReclaim{}, one).
		Complete(e)
}
