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

package stack_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	utilyaml "k8s.io/apimachinery/pkg/util/yaml"

	"github.com/maeshinshin/ichor/pkg/stack"
	"github.com/maeshinshin/ichor/pkg/stack/argocd"
	"github.com/maeshinshin/ichor/pkg/stack/kokumi"
)

// ADR 0099 decision 10: the CiliumNetworkPolicies of the kokumi and argocd namespaces. The tests
// derive every flow from the rendered objects, so a version bump that moves a port or renames a
// label fails here and not on a cluster.

const (
	ichorNS       = "ichor-system"
	ichorPolicies = "../../config/cilium-network-policy/policies.yaml"
	kindCNP       = "CiliumNetworkPolicy"
	egress        = "egress"
	apiserverPort = 6443
	registryPort  = 5000
)

type obj = map[string]any

type pod struct {
	ns, name string
	labels   map[string]string
	ports    []int // containerPorts, flag ports and the targetPorts of Services selecting it
	probes   []int
	metrics  []int
}

type world struct {
	pods     []*pod
	policies []obj
	kinds    map[string]int
}

func decode(t *testing.T, b []byte) []obj {
	t.Helper()

	var out []obj

	dec := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(b), 4096)

	for {
		o := obj{}

		err := dec.Decode(&o)
		if errors.Is(err, io.EOF) {
			return out
		}

		if err != nil {
			t.Fatalf("parsing YAML: %v", err)
		}

		if len(o) > 0 {
			out = append(out, o)
		}
	}
}

func dig(o any, path ...string) any {
	for _, p := range path {
		m, ok := o.(obj)
		if !ok {
			return nil
		}

		o = m[p]
	}

	return o
}

func list(o any) []any { l, _ := o.([]any); return l }

func strs(o any) map[string]string {
	out := map[string]string{}

	m, _ := o.(obj)
	for k, v := range m {
		out[k] = fmt.Sprint(v)
	}

	return out
}

func num(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int64:
		return int(n)
	case int:
		return n
	case string:
		i, _ := strconv.Atoi(n)
		return i
	}

	return 0
}

var flagPort = regexp.MustCompile(`^--[a-z-]*(?:port|address)=:?(\d+)$`)

// load renders both bundles and reads the Ichor controller's labels from its own policy file.
func load(t *testing.T) *world {
	t.Helper()

	k, err := kokumi.Render(kokumi.Options{AdminPasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}

	a, err := argocd.Render(argocd.Options{RedisPassword: "x"})
	if err != nil {
		t.Fatal(err)
	}

	w := &world{kinds: map[string]int{}}

	var services []obj

	for _, o := range append(decode(t, k), decode(t, a)...) {
		kind, _ := o["kind"].(string)
		w.kinds[kind]++

		switch kind {
		case "CiliumNetworkPolicy":
			w.policies = append(w.policies, o)
		case "Service":
			services = append(services, o)
		case "Deployment", "StatefulSet":
			w.pods = append(w.pods, podOf(o))
		}
	}

	for _, s := range services {
		sel := strs(dig(s, "spec", "selector"))
		for _, p := range w.pods {
			if p.ns != dig(s, "metadata", "namespace") || len(sel) == 0 || !matches(p.labels, sel) {
				continue
			}

			for _, sp := range list(dig(s, "spec", "ports")) {
				if n := num(dig(sp, "targetPort")); n != 0 {
					p.ports = append(p.ports, n)
				}
			}
		}
	}

	// The Ichor controller is not in the stack; its labels and namespace are the ones its own
	// policy selects.
	for _, o := range decode(t, mustRead(t, ichorPolicies)) {
		if dig(o, "metadata", "name") == "controller-manager" {
			labels := strs(dig(o, "spec", "endpointSelector", "matchLabels"))
			w.pods = append(w.pods, &pod{ns: ichorNS, name: "ichor-controller", labels: labels})
			w.policies = append(w.policies, ichorPolicy(o))
		}
	}

	// CoreDNS, which the stack's DNS rule names.
	dnsLabels := map[string]string{"k8s-app": "kube-dns"}
	w.pods = append(w.pods, &pod{ns: "kube-system", name: "coredns", labels: dnsLabels, ports: []int{53}})

	return w
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()

	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}

	return b
}

// ichorPolicy returns the Ichor controller policy with its namespace set the way kustomize does,
// cut down to the egress rules that name the Kokumi registry (the rest is ADR 0100's, not this
// unit's, and names ports of pods the test does not render).
func ichorPolicy(o obj) obj {
	var rules []any

	for _, r := range list(dig(o, "spec", egress)) {
		for _, peer := range list(dig(r, "toEndpoints")) {
			if parseSelector(peer, ichorNS).ns == kokumi.Namespace {
				rules = append(rules, r)
			}
		}
	}

	return obj{
		"metadata": obj{"name": "controller-manager", "namespace": ichorNS},
		"spec":     obj{"endpointSelector": dig(o, "spec", "endpointSelector"), egress: rules},
	}
}

func podOf(o obj) *pod {
	tmpl := dig(o, "spec", "template")
	p := &pod{
		ns: fmt.Sprint(dig(o, "metadata", "namespace")), name: fmt.Sprint(dig(o, "metadata", "name")),
		labels: strs(dig(tmpl, "metadata", "labels")),
	}

	for _, c := range list(dig(tmpl, "spec", "containers")) {
		named := map[string]int{}

		for _, cp := range list(dig(c, "ports")) {
			p.ports = append(p.ports, num(dig(cp, "containerPort")))
			named[fmt.Sprint(dig(cp, "name"))] = num(dig(cp, "containerPort"))
		}

		var flags []int

		for _, a := range list(dig(c, "args")) {
			if m := flagPort.FindStringSubmatch(fmt.Sprint(a)); m != nil {
				n, _ := strconv.Atoi(m[1])
				flags = append(flags, n)

				if strings.HasPrefix(fmt.Sprint(a), "--metrics") {
					p.metrics = append(p.metrics, n)
				}
			}
		}

		p.ports = append(p.ports, flags...)

		for _, probe := range []string{"livenessProbe", "readinessProbe", "startupProbe"} {
			port := dig(c, probe, "httpGet", "port")
			if port == nil {
				port = dig(c, probe, "tcpSocket", "port")
			}

			if s, ok := port.(string); ok {
				if _, err := strconv.Atoi(s); err != nil {
					p.probes = append(p.probes, named[s])

					continue
				}
			}

			if port != nil {
				p.probes = append(p.probes, num(port))
			}
		}

		// Argo CD names its metrics port.
		if n, ok := named["metrics"]; ok {
			p.metrics = append(p.metrics, n)
		}
	}

	return p
}

func matches(labels, want map[string]string) bool {
	for k, v := range want {
		if labels[k] != v {
			return false
		}
	}

	return true
}

// selector is a peer or endpoint selector with the namespace resolved.
type selector struct {
	ns     string
	labels map[string]string
	exprs  []any
}

func parseSelector(sel any, policyNS string) selector {
	s := selector{ns: policyNS, labels: map[string]string{}, exprs: list(dig(sel, "matchExpressions"))}

	for k, v := range strs(dig(sel, "matchLabels")) {
		k = strings.TrimPrefix(strings.TrimPrefix(k, "k8s:"), "any:")
		if k == "io.kubernetes.pod.namespace" {
			s.ns = v

			continue
		}

		s.labels[k] = v
	}

	return s
}

func (s selector) matches(p *pod) bool {
	if p.ns != s.ns || !matches(p.labels, s.labels) {
		return false
	}

	for _, e := range s.exprs {
		key := fmt.Sprint(dig(e, "key"))
		has := func(v any) bool { return fmt.Sprint(v) == p.labels[key] }
		if dig(e, "operator") != "In" || !slices.ContainsFunc(list(dig(e, "values")), has) {
			return false
		}
	}

	return true
}

func policyNS(p obj) string { return fmt.Sprint(dig(p, "metadata", "namespace")) }

func (w *world) selects(p obj, x *pod) bool {
	return parseSelector(dig(p, "spec", "endpointSelector"), policyNS(p)).matches(x)
}

func portsOf(rule any) []int {
	var out []int

	for _, tp := range list(dig(rule, "toPorts")) {
		for _, pp := range list(dig(tp, "ports")) {
			out = append(out, num(dig(pp, "port")))
		}
	}

	return out
}

// egressAllows: some policy selecting src has an egress rule to dst on port.
func (w *world) egressAllows(src, dst *pod, port int) bool {
	return w.ruleAllows(src, egress, "toEndpoints", dst, nil, port)
}

// ingressAllows: some policy selecting dst has an ingress rule from src on port.
func (w *world) ingressAllows(dst, src *pod, port int) bool {
	return w.ruleAllows(dst, "ingress", "fromEndpoints", src, nil, port)
}

func (w *world) entityAllows(self *pod, dir, entity string, port int) bool {
	key := map[string]string{"ingress": "fromEntities", egress: "toEntities"}[dir]

	return w.ruleAllows(self, dir, "", nil, &entityQuery{key: key, name: entity}, port)
}

type entityQuery struct{ key, name string }

func (w *world) ruleAllows(self *pod, dir, peerKey string, peer *pod, ent *entityQuery, port int) bool {
	for _, p := range w.policies {
		if !w.selects(p, self) {
			continue
		}

		for _, r := range list(dig(p, "spec", dir)) {
			if !slices.Contains(portsOf(r), port) {
				continue
			}

			if ent != nil && slices.ContainsFunc(list(dig(r, ent.key)), func(e any) bool { return e == ent.name }) {
				return true
			}

			if peer != nil && slices.ContainsFunc(list(dig(r, peerKey)), func(s any) bool {
				return parseSelector(s, policyNS(p)).matches(peer)
			}) {
				return true
			}
		}
	}

	return false
}

func (w *world) pod(t *testing.T, ns, name string) *pod {
	t.Helper()

	for _, p := range w.pods {
		if p.ns == ns && p.name == name {
			return p
		}
	}

	t.Fatalf("no pod %s/%s in the rendered manifests", ns, name)

	return nil
}

func (w *world) stackPods() []*pod {
	var out []*pod

	for _, p := range w.pods {
		if p.ns == kokumi.Namespace || p.ns == argocd.Namespace {
			out = append(out, p)
		}
	}

	return out
}

func TestPoliciesParseAndAreInBothBundles(t *testing.T) {
	for _, c := range []struct{ component, ns string }{{"kokumi", kokumi.Namespace}, {"argocd", argocd.Namespace}} {
		b, err := stack.Policies(c.component)
		if err != nil {
			t.Fatal(err)
		}

		docs := decode(t, b)
		if len(docs) < 6 {
			t.Fatalf("%s: %d policies, want a default deny plus the allows", c.component, len(docs))
		}

		seen := map[string]bool{}

		for _, d := range docs {
			name := fmt.Sprint(dig(d, "metadata", "name"))
			bad := d["apiVersion"] != "cilium.io/v2" || d["kind"] != kindCNP || policyNS(d) != c.ns
			if bad || name == "" || seen[name] {
				t.Fatalf("%s: bad or duplicate policy %q: %v", c.component, name, d)
			}

			seen[name] = true

			if dig(d, "spec", "endpointSelector") == nil || dig(d, "spec", "description") == nil {
				t.Errorf("%s/%s: needs an endpointSelector and a description", c.component, name)
			}
		}
	}

	if _, err := stack.Policies("nope"); err == nil {
		t.Fatal("an unknown component must be an error")
	}
}

func TestRenderIncludesPoliciesUnlessOmitted(t *testing.T) {
	for name, render := range map[string]func(omit bool) ([]byte, error){
		"kokumi": func(omit bool) ([]byte, error) {
			return kokumi.Render(kokumi.Options{AdminPasswordHash: "x", OmitPolicies: omit})
		},
		"argocd": func(omit bool) ([]byte, error) {
			return argocd.Render(argocd.Options{RedisPassword: "x", OmitPolicies: omit})
		},
	} {
		with, err := render(false)
		if err != nil {
			t.Fatal(err)
		}

		without, err := render(true)
		if err != nil {
			t.Fatal(err)
		}

		want, _ := stack.Policies(name)
		marker := []byte("kind: " + kindCNP)
		n, m := bytes.Count(with, marker), bytes.Count(without, marker)

		if m != 0 || n != bytes.Count(want, marker) || len(decode(t, with)) != len(decode(t, without))+n {
			t.Errorf("%s: %d policies with, %d without", name, n, m)
		}
	}
}

func TestEachNamespaceHasDefaultDenyAndDNS(t *testing.T) {
	w := load(t)
	dns := w.pod(t, "kube-system", "coredns")

	for _, ns := range []string{kokumi.Namespace, argocd.Namespace} {
		var deny bool

		for _, p := range w.policies {
			if policyNS(p) == ns && dig(p, "metadata", "name") == "default-deny" {
				sel, _ := dig(p, "spec", "endpointSelector").(obj)
				deny = len(sel) == 0 && len(list(dig(p, "spec", "ingress"))) == 1 && len(list(dig(p, "spec", egress))) == 1
			}
		}

		if !deny {
			t.Errorf("%s has no default-deny of ingress and egress for every pod", ns)
		}

		for _, x := range w.stackPods() {
			if x.ns != ns {
				continue
			}

			for _, port := range []int{53} {
				if !w.egressAllows(x, dns, port) {
					t.Errorf("%s cannot reach DNS", x.name)
				}
			}
		}
	}

	// UDP and TCP, with a dns rule so the names are visible.
	for _, p := range w.policies {
		if dig(p, "metadata", "name") != "allow-dns" {
			continue
		}

		r := list(dig(p, "spec", egress))[0]
		ports := list(dig(list(dig(r, "toPorts"))[0], "ports"))

		protos := make([]string, 0, len(ports))
		for _, pp := range ports {
			protos = append(protos, fmt.Sprint(dig(pp, "protocol")))
		}

		slices.Sort(protos)

		if !slices.Equal(protos, []string{"TCP", "UDP"}) || dig(list(dig(r, "toPorts"))[0], "rules", "dns") == nil {
			t.Errorf("%s/allow-dns: protocols %v, dns rule %v", policyNS(p), protos, dig(list(dig(r, "toPorts"))[0], "rules"))
		}
	}
}

// Every selector in every policy selects a pod of the rendered manifests (or CoreDNS and the
// Ichor controller, which the stack does not render).
func TestEverySelectorMatchesARenderedPod(t *testing.T) {
	w := load(t)

	anyPod := func(s selector) bool {
		return slices.ContainsFunc(w.pods, s.matches)
	}

	for _, p := range w.policies {
		ns, name := policyNS(p), fmt.Sprint(dig(p, "metadata", "name"))

		if sel, _ := dig(p, "spec", "endpointSelector").(obj); len(sel) != 0 && !anyPod(parseSelector(sel, ns)) {
			t.Errorf("%s/%s: endpointSelector matches no pod", ns, name)
		}

		for _, dir := range []struct{ d, k string }{{"ingress", "fromEndpoints"}, {egress, "toEndpoints"}} {
			for _, r := range list(dig(p, "spec", dir.d)) {
				for _, s := range list(dig(r, dir.k)) {
					if !anyPod(parseSelector(s, ns)) {
						t.Errorf("%s/%s: %s selector %v matches no pod", ns, name, dir.k, s)
					}
				}
			}
		}
	}
}

// Every port in a rule is a port of the pods it names: the destination's for an egress rule to
// endpoints, the selected pod's for an ingress rule. Entity rules to the apiserver use 6443.
func TestEveryPortMatchesARenderedObject(t *testing.T) {
	w := load(t)

	for _, p := range w.policies {
		ns, name := policyNS(p), fmt.Sprint(dig(p, "metadata", "name"))
		selected := slices.DeleteFunc(slices.Clone(w.pods), func(x *pod) bool { return !w.selects(p, x) })

		for _, r := range list(dig(p, "spec", "ingress")) {
			for _, port := range portsOf(r) {
				if !slices.ContainsFunc(selected, func(x *pod) bool { return slices.Contains(x.ports, port) }) {
					t.Errorf("%s/%s: ingress port %d is not a port of any selected pod", ns, name, port)
				}
			}
		}

		for _, r := range list(dig(p, "spec", egress)) {
			ports := portsOf(r)

			if slices.Contains(list(dig(r, "toEntities")), any("kube-apiserver")) {
				if !slices.Equal(ports, []int{apiserverPort}) {
					t.Errorf("%s/%s: apiserver egress ports %v, want [%d]", ns, name, ports, apiserverPort)
				}

				continue
			}

			peers := list(dig(r, "toEndpoints"))
			if len(peers) == 0 || slices.Equal(ports, []int{53}) {
				continue
			}

			for _, port := range ports {
				for _, peer := range peers {
					s := parseSelector(peer, ns)
					if !slices.ContainsFunc(w.pods, func(x *pod) bool { return s.matches(x) && slices.Contains(x.ports, port) }) {
						t.Errorf("%s/%s: egress port %d is not a port of the pods %v selects", ns, name, port, peer)
					}
				}
			}
		}
	}
}

func TestEveryNamedFlowIsAllowedOnBothSides(t *testing.T) {
	w := load(t)

	registry := w.pod(t, kokumi.Namespace, "kokumi-registry")
	repo := w.pod(t, argocd.Namespace, "argocd-repo-server")
	ctrl := w.pod(t, argocd.Namespace, "argocd-application-controller")
	srv := w.pod(t, argocd.Namespace, "argocd-server")
	redis := w.pod(t, argocd.Namespace, "argocd-redis")
	kctrl := w.pod(t, kokumi.Namespace, "kokumi-controller-manager")
	ichor := w.pod(t, ichorNS, "ichor-controller")

	flows := []struct {
		name     string
		src, dst *pod
		port     int
	}{
		{"argocd repo-server to the registry", repo, registry, registryPort},
		{"argocd application-controller to the registry", ctrl, registry, registryPort},
		{"kokumi to its own registry", kctrl, registry, registryPort},
		{"ichor controller publishes into the registry", ichor, registry, registryPort},
		{"argocd server to repo-server", srv, repo, 8081},
		{"argocd application-controller to repo-server", ctrl, repo, 8081},
		{"argocd server to redis", srv, redis, 6379},
		{"argocd repo-server to redis", repo, redis, 6379},
		{"argocd application-controller to redis", ctrl, redis, 6379},
	}

	for _, f := range flows {
		if !w.egressAllows(f.src, f.dst, f.port) {
			t.Errorf("%s: no egress rule from %s/%s", f.name, f.src.ns, f.src.name)
		}

		// The Ichor controller's side is its own policy; the receiving side is checked for all.
		if !w.ingressAllows(f.dst, f.src, f.port) {
			t.Errorf("%s: no ingress rule on %s/%s", f.name, f.dst.ns, f.dst.name)
		}
	}

	// Nobody else reaches the registry or redis: a stranger in each namespace is refused.
	for _, c := range []struct {
		dst  *pod
		port int
	}{{registry, registryPort}, {redis, 6379}} {
		stranger := &pod{ns: "default", labels: map[string]string{"app": "x"}}
		if w.ingressAllows(c.dst, stranger, c.port) {
			t.Errorf("%s accepts a pod from any namespace", c.dst.name)
		}
	}

	if w.ingressAllows(registry, srv, registryPort) || w.ingressAllows(redis, kctrl, 6379) {
		t.Error("the Argo CD server must not reach the registry, nor Kokumi redis")
	}
}

// The ichor-system side of the publish flow is written in config/cilium-network-policy; the
// receiving side is in the kokumi bundle. Both must exist, or a default-deny cuts publishing.
func TestIchorToRegistryFlowIsInBothDirections(t *testing.T) {
	w := load(t)

	registry := w.pod(t, kokumi.Namespace, "kokumi-registry")
	ichor := w.pod(t, ichorNS, "ichor-controller")

	if !w.egressAllows(ichor, registry, registryPort) {
		t.Error("ichor-system's policy has no egress to the Kokumi registry on 5000")
	}

	if !w.ingressAllows(registry, ichor, registryPort) {
		t.Error("kokumi's policy has no ingress from the Ichor controller on 5000")
	}
}

func TestEveryPodReachesTheAPIServerAndAcceptsItsProbesAndMetrics(t *testing.T) {
	w := load(t)

	for _, x := range w.stackPods() {
		// The registry and redis never call the API (the registry mounts no token).
		needsAPI := x.name != "kokumi-registry" && x.name != "argocd-redis"
		if needsAPI && !w.entityAllows(x, egress, "kube-apiserver", apiserverPort) {
			t.Errorf("%s: no egress to kube-apiserver on %d", x.name, apiserverPort)
		}

		for _, port := range x.probes {
			if !w.entityAllows(x, "ingress", "host", port) {
				t.Errorf("%s: kubelet probe port %d is not allowed from host", x.name, port)
			}
		}

		for _, port := range x.metrics {
			if !w.entityAllows(x, "ingress", "cluster", port) {
				t.Errorf("%s: metrics port %d is not allowed from cluster", x.name, port)
			}
		}
	}

	// Users reach the two UIs by port-forward, which arrives from the node.
	if !w.entityAllows(w.pod(t, argocd.Namespace, "argocd-server"), "ingress", "host", 8080) ||
		!w.entityAllows(w.pod(t, kokumi.Namespace, "kokumi-server"), "ingress", "host", 8080) {
		t.Error("argocd-server and kokumi-server must accept port-forwards (host) on 8080")
	}
}

// Neither component ships a webhook today, so no apiserver ingress is needed. If one appears the
// ingress must allow [kube-apiserver, remote-node] (ADR 0100 amendment): fail until it is added.
func TestNoWebhooksAreRendered(t *testing.T) {
	w := load(t)

	for _, kind := range []string{"ValidatingWebhookConfiguration", "MutatingWebhookConfiguration", "APIService"} {
		if w.kinds[kind] != 0 {
			t.Errorf("a %s is rendered: add a policy admitting kube-apiserver and remote-node to its port", kind)
		}
	}

	for _, p := range w.policies {
		for _, r := range list(dig(p, "spec", "ingress")) {
			if slices.Contains(list(dig(r, "fromEntities")), any("kube-apiserver")) {
				t.Errorf("%s: ingress from kube-apiserver without a webhook", dig(p, "metadata", "name"))
			}
		}
	}
}

// The chart's own Kubernetes NetworkPolicies are switched off: Cilium unions them with ours and
// the chart's argocd-server one allows all ingress.
func TestArgoCDChartNetworkPoliciesAreOff(t *testing.T) {
	if w := load(t); w.kinds["NetworkPolicy"] != 0 {
		t.Fatalf("%d Kubernetes NetworkPolicies are rendered", w.kinds["NetworkPolicy"])
	}
}
