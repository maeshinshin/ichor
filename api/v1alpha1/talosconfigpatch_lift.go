package v1alpha1

import (
	"encoding/json"
	"fmt"
	"slices"
)

const (
	kindCoreDNS  = "KubeCoreDNSConfig"
	kindTimeSync = "TimeSyncConfig"
)

// liftV1Alpha1 moves the settings that Talos 1.14 spells in a document of their own out of the v1alpha1 document of a
// patch an older Ichor stored, and into that document, so ConfigFromDocuments can carry them. Each move is typed and
// loses nothing; a setting it does not move stays in the v1alpha1 document, where ConfigFromDocuments either carries it
// (a setting both spell) or refuses it by name. A move whose target document already sets the same field is an error.
func liftV1Alpha1(docs []TalosConfigPatchDocument) ([]TalosConfigPatchDocument, error) {
	var (
		insts []instance
		trees = map[instance]map[string]any{}
	)

	for _, d := range docs {
		key, ok := TalosConfigPatchDocumentFieldKey(d.Kind)
		if !ok {
			return docs, nil // ConfigFromDocuments names the unknown kind
		}

		tree, err := documentTree(d, key)
		if err != nil {
			return nil, err
		}

		name, _ := tree["name"].(string)
		i := instance{d.Kind, name}

		if _, dup := trees[i]; dup {
			return docs, nil // ConfigFromDocuments names the duplicate
		}

		insts = append(insts, i)
		trees[i] = tree
	}

	root := trees[instance{configKind, ""}]
	if root == nil {
		return docs, nil
	}

	l := &lifter{insts: insts, trees: trees}

	if err := l.moveAll(root); err != nil {
		return nil, err
	}

	out := make([]TalosConfigPatchDocument, 0, len(l.insts))

	for _, i := range l.insts {
		key, _ := TalosConfigPatchDocumentFieldKey(i.kind)

		raw, err := json.Marshal(map[string]any{"kind": i.kind, key: l.trees[i]})
		if err != nil {
			return nil, err
		}

		var d TalosConfigPatchDocument
		if err := json.Unmarshal(raw, &d); err != nil {
			return nil, err
		}

		out = append(out, d)
	}

	return out, nil
}

// moveAll makes every move out of the v1alpha1 tree root. It is the one table of moves.
func (l *lifter) moveAll(root map[string]any) error {
	for _, move := range []func(map[string]any) error{
		l.certSANs, l.disabledToEnabled, l.timeServers, l.kernelModules, l.hostEntries, l.resolver, l.mirrors,
		l.imageCache, l.inlineManifests,
	} {
		if err := move(root); err != nil {
			return err
		}
	}

	return nil
}

// LiftSettings moves the settings of the v1alpha1 tree root that Talos 1.14 spells in a document of their own into
// docs, keyed by {kind, name} (name empty for an unnamed document), making the documents it needs and leaving the
// others as they are. It is the move liftV1Alpha1 makes for a patch, for a caller that holds raw trees. On an error
// root and docs may be half moved.
func LiftSettings(root map[string]any, docs map[[2]string]map[string]any) error {
	l := &lifter{trees: map[instance]map[string]any{}}

	for k, t := range docs {
		i := instance{k[0], k[1]}
		l.insts = append(l.insts, i)
		l.trees[i] = t
	}

	if err := l.moveAll(root); err != nil {
		return err
	}

	for _, i := range l.insts {
		docs[[2]string{i.kind, i.name}] = l.trees[i]
	}

	return nil
}

type lifter struct {
	insts []instance
	trees map[instance]map[string]any
}

// into is the tree of a document of the kind (named name when it has one), made when the patch has none.
func (l *lifter) into(kind, name string) map[string]any {
	i := instance{kind, name}
	if t, ok := l.trees[i]; ok {
		return t
	}

	t := map[string]any{}
	if name != "" {
		t["name"] = name
	}

	l.trees[i] = t
	l.insts = append(l.insts, i)

	return t
}

// put sets path (dotted) of the document; a field the document sets already is a conflict.
func (l *lifter) put(kind, name, path string, v any) error {
	t := l.into(kind, name)
	keys := splitPath(path)

	for _, k := range keys[:len(keys)-1] {
		next, ok := t[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			t[k] = next
		}

		t = next
	}

	last := keys[len(keys)-1]
	if _, set := t[last]; set {
		return fmt.Errorf("%s %s is set in the v1alpha1 document and in the %s document", kind, path, kind)
	}

	t[last] = v

	return nil
}

// putList is put for a list the v1alpha1 document may spell in several entries of the same name (two
// extraHostEntries of one address, two kernel modules of one name): the entries' items join in order, each once.
func (l *lifter) putList(kind, name, path string, v any) error {
	t := l.into(kind, name)

	if prev, ok := t[path].([]any); ok {
		for _, x := range asList(v) {
			if !slices.Contains(prev, x) {
				prev = append(prev, x)
			}
		}

		t[path] = prev

		return nil
	}

	return l.put(kind, name, path, dedupe(asList(v)))
}

func dedupe(in []any) []any {
	out := make([]any, 0, len(in))

	for _, x := range in {
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}

	return out
}

func splitPath(p string) []string {
	var out []string

	start := 0

	for i := 0; i <= len(p); i++ {
		if i == len(p) || p[i] == '.' {
			out = append(out, p[start:i])
			start = i + 1
		}
	}

	return out
}

// take removes the value at path of the tree and the objects it leaves empty.
func take(tree map[string]any, path string) (any, bool) {
	keys := splitPath(path)
	parents := []map[string]any{tree}

	for _, k := range keys[:len(keys)-1] {
		next, ok := parents[len(parents)-1][k].(map[string]any)
		if !ok {
			return nil, false
		}

		parents = append(parents, next)
	}

	last := keys[len(keys)-1]

	v, ok := parents[len(parents)-1][last]
	if !ok {
		return nil, false
	}

	delete(parents[len(parents)-1], last)

	for i := len(parents) - 1; i > 0; i-- {
		if len(parents[i]) > 0 {
			break
		}

		delete(parents[i-1], keys[i-1])
	}

	return v, true
}

// cluster.apiServer.certSANs -> KubeAPIServerConfig certExtraSANs.
func (l *lifter) certSANs(root map[string]any) error {
	if v, ok := take(root, "cluster.apiServer.certSANs"); ok {
		return l.put("KubeAPIServerConfig", "", "certExtraSANs", v)
	}

	return nil
}

// A disabled switch of v1alpha1 is the inverse enabled switch of the document.
func (l *lifter) disabledToEnabled(root map[string]any) error {
	for _, m := range []struct{ from, kind string }{
		{"cluster.coreDNS.disabled", kindCoreDNS},
		{"machine.time.disabled", kindTimeSync},
	} {
		v, ok := take(root, m.from)

		if b, isBool := v.(bool); ok && isBool {
			if err := l.put(m.kind, "", "enabled", !b); err != nil {
				return err
			}
		}
	}

	return nil
}

// machine.time.servers -> TimeSyncConfig ntp.servers.
func (l *lifter) timeServers(root map[string]any) error {
	if v, ok := take(root, "machine.time.servers"); ok {
		return l.put(kindTimeSync, "", "ntp.servers", v)
	}

	return nil
}

// machine.kernel.modules[] {name, parameters} -> one KernelModuleConfig named after the module.
func (l *lifter) kernelModules(root map[string]any) error {
	return eachEntry(root, "machine.kernel.modules", func(e map[string]any) error {
		name, _ := e["name"].(string)

		if p, ok := e["parameters"]; ok {
			return l.putList("KernelModuleConfig", name, "parameters", p)
		}

		l.into("KernelModuleConfig", name)

		return nil
	})
}

// machine.network.extraHostEntries[] {ip, aliases} -> one StaticHostConfig named after the address.
func (l *lifter) hostEntries(root map[string]any) error {
	return eachEntry(root, "machine.network.extraHostEntries", func(e map[string]any) error {
		ip, _ := e["ip"].(string)

		return l.putList("StaticHostConfig", ip, "hostnames", e["aliases"])
	})
}

// machine.network.nameservers, searchDomains and disableSearchDomain -> ResolverConfig.
func (l *lifter) resolver(root map[string]any) error {
	if v, ok := take(root, "machine.network.nameservers"); ok {
		servers := make([]any, 0, len(asList(v)))

		for _, s := range asList(v) {
			servers = append(servers, map[string]any{"address": s})
		}

		if err := l.put("ResolverConfig", "", "nameservers", servers); err != nil {
			return err
		}
	}

	if v, ok := take(root, "machine.network.searchDomains"); ok {
		if err := l.put("ResolverConfig", "", "searchDomains.domains", v); err != nil {
			return err
		}
	}

	if v, ok := take(root, "machine.network.disableSearchDomain"); ok {
		return l.put("ResolverConfig", "", "searchDomains.disableDefault", v)
	}

	return nil
}

// machine.registries.mirrors.HOST {endpoints, overridePath, skipFallback} -> RegistryMirrorConfig named HOST, whose
// endpoints each carry the overridePath of the mirror (Talos applies the one flag to every endpoint).
func (l *lifter) mirrors(root map[string]any) error {
	v, ok := take(root, "machine.registries.mirrors")
	if !ok {
		return nil
	}

	mirrors, _ := v.(map[string]any)

	for host, m := range mirrors {
		mirror, _ := m.(map[string]any)
		override := mirror["overridePath"]

		var endpoints []any

		for _, e := range asList(mirror["endpoints"]) {
			ep := map[string]any{"url": e}
			if override != nil {
				ep["overridePath"] = override
			}

			endpoints = append(endpoints, ep)
		}

		if len(endpoints) > 0 {
			if err := l.put("RegistryMirrorConfig", host, "endpoints", endpoints); err != nil {
				return err
			}
		}

		if s, ok := mirror["skipFallback"]; ok {
			if err := l.put("RegistryMirrorConfig", host, "skipFallback", s); err != nil {
				return err
			}
		}

		l.into("RegistryMirrorConfig", host)
	}

	return nil
}

// machine.features.imageCache.localEnabled -> ImageCacheConfig local.enabled.
func (l *lifter) imageCache(root map[string]any) error {
	if v, ok := take(root, "machine.features.imageCache.localEnabled"); ok {
		return l.put("ImageCacheConfig", "", "local.enabled", v)
	}

	return nil
}

// cluster.inlineManifests[] {name, contents} -> one KubeInlineManifestConfig named after the manifest.
func (l *lifter) inlineManifests(root map[string]any) error {
	return eachEntry(root, "cluster.inlineManifests", func(e map[string]any) error {
		name, _ := e["name"].(string)

		return l.put("KubeInlineManifestConfig", name, "manifest", e["contents"])
	})
}

func eachEntry(root map[string]any, path string, f func(map[string]any) error) error {
	v, ok := take(root, path)
	if !ok {
		return nil
	}

	for _, e := range asList(v) {
		m, _ := e.(map[string]any)
		if err := f(m); err != nil {
			return err
		}
	}

	return nil
}

func asList(v any) []any {
	l, _ := v.([]any)

	return slices.Clone(l)
}
